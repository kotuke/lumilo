package splash

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/gguf"
)

var _ engine.Adopter = (*Splash)(nil)

// splitFirst is the first part of a split GGUF: …-00001-of-0000N.gguf.
var splitFirst = regexp.MustCompile(`-00001-of-\d{5}\.gguf$`)

// Adoptable lists the GGUF models in the hub caches that Splash serves but
// has not installed: downloaded for llama.cpp, which reads the hub cache
// as it is, or by another program. Download installs one from those files
// (install/models.py prepare finds them in the cache and fetches only what
// is missing: the vision projector, the DFlash2 draft), in seconds when
// all are there. A model is kept when its general.architecture is one
// modelSearch names and its variant is not one Splash refuses; its ID is
// OWNER/REPO:VARIANT, the tag gguf.Tag gives the file. Of a repository
// only the snapshot refs/main names is read, else the newest.
func (s *Splash) Adoptable(ctx context.Context) ([]engine.Model, error) {
	installed, err := s.Models(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, m := range installed {
		// The link's name and the tag of its file: "Mix-15.7G" and
		// "AI-Defense-Mix-15.7G" name the same file (readModel).
		have[strings.ToLower(m.ID)] = true
		have[strings.ToLower(m.Owner+"/"+m.Name+":"+m.Variant)] = true
	}
	spec := modelSearch()
	var archs, refuse []string
	for _, src := range spec.Sources {
		if src.Filter == "gguf" {
			archs = src.Architectures
		}
	}
	for _, r := range spec.RefuseGGUF {
		refuse = append(refuse, strings.ToUpper(r))
	}
	seen := map[string]bool{}
	var out []engine.Model
	for _, root := range s.hubDirs {
		repos, _ := os.ReadDir(root)
		for _, r := range repos {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			owner, repo, ok := hubRepo(r)
			if !ok {
				continue
			}
			snap := latestSnapshot(filepath.Join(root, r.Name()))
			if snap == "" {
				continue
			}
			for _, m := range adoptableIn(snap, owner, repo, archs, refuse) {
				key := strings.ToLower(m.ID)
				if have[key] || seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out, nil
}

// hubRepo splits a hub cache folder models--OWNER--REPO.
func hubRepo(e os.DirEntry) (owner, repo string, ok bool) {
	rest, ok := strings.CutPrefix(e.Name(), "models--")
	if !ok || !e.IsDir() {
		return "", "", false
	}
	owner, repo, ok = strings.Cut(rest, "--")
	return owner, repo, ok && repoPartRe.MatchString(owner) && repoPartRe.MatchString(repo)
}

// latestSnapshot is the snapshot refs/main names, else the newest; "" when
// there is none.
func latestSnapshot(dir string) string {
	snaps, err := os.ReadDir(filepath.Join(dir, "snapshots"))
	if err != nil || len(snaps) == 0 {
		return ""
	}
	if b, err := os.ReadFile(filepath.Join(dir, "refs", "main")); err == nil {
		main := strings.TrimSpace(string(b))
		if slices.ContainsFunc(snaps, func(e os.DirEntry) bool { return e.Name() == main }) {
			return filepath.Join(dir, "snapshots", main)
		}
	}
	best, at := "", int64(0)
	for _, e := range snaps {
		if info, err := e.Info(); err == nil && (best == "" || info.ModTime().UnixNano() > at) {
			best, at = e.Name(), info.ModTime().UnixNano()
		}
	}
	return filepath.Join(dir, "snapshots", best)
}

// adoptableIn are the models Splash serves among the GGUFs at the root of
// a snapshot: whole files and the first parts of split ones, whose parts
// are all there.
func adoptableIn(snap, owner, repo string, archs, refuse []string) []engine.Model {
	entries, err := os.ReadDir(snap)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	var out []engine.Model
	for _, name := range names {
		low := strings.ToLower(name)
		if !strings.HasSuffix(low, ".gguf") || strings.HasPrefix(name, ".") || gguf.Sidecar(name) != "" {
			continue
		}
		if gguf.Stem(name)+".gguf" != name && !splitFirst.MatchString(name) {
			continue // a later part of a split model
		}
		path := filepath.Join(snap, name)
		size, ok := partsSize(snap, name, names)
		if !ok {
			continue
		}
		meta, err := gguf.ReadFile(path)
		if err != nil || !meta.TextModel() || !slices.Contains(archs, meta.Arch) {
			continue
		}
		tag := gguf.Tag(name, gguf.RepoBase(repo), meta.DisplayName())
		if !variantRe.MatchString(tag) || refused(tag, refuse) {
			continue
		}
		vision := slices.ContainsFunc(names, func(n string) bool { return gguf.Sidecar(n) == "mmproj" })
		out = append(out, engine.Model{ID: owner + "/" + repo + ":" + tag, Name: repo, Owner: owner, Variant: tag,
			SizeBytes: size, ContextLength: meta.Context, KVElements: meta.KVElements(),
			Features: engine.Features{Vision: vision}})
	}
	return out
}

// partsSize is the size of a GGUF, of all its parts when it is split;
// false when a part is missing or unreadable (a link to a deleted blob).
func partsSize(dir, name string, names []string) (int64, bool) {
	if !splitFirst.MatchString(name) {
		info, err := os.Stat(filepath.Join(dir, name))
		return sizeOf(info, err)
	}
	stem := gguf.Stem(name)
	n := name[len(name)-len("00002.gguf") : len(name)-len(".gguf")]
	want, _ := strconv.Atoi(n)
	var total int64
	parts := 0
	for _, part := range names {
		if gguf.Stem(part) != stem || !strings.HasSuffix(part, "-of-"+n+".gguf") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, part))
		size, ok := sizeOf(info, err)
		if !ok {
			return 0, false
		}
		total += size
		parts++
	}
	return total, parts == want
}

func sizeOf(info os.FileInfo, err error) (int64, bool) {
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	return info.Size(), true
}

// refused reports whether tag names a type Splash does not load: the type
// itself or its end after "-" or "_" ("UD-BF16", "Q8_0" is kept).
func refused(tag string, refuse []string) bool {
	up := strings.ToUpper(tag)
	for _, r := range refuse {
		if up == r || strings.HasSuffix(up, "-"+r) || strings.HasSuffix(up, "_"+r) {
			return true
		}
	}
	return false
}
