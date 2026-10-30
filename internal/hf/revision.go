package hf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/kotuke/lumilo/internal/gguf"
)

// Revision is a repository at one commit, with the checksums of its files:
// what a download needs to fetch them and lay them out in the hub cache.
type Revision struct {
	Commit string // the commit main points at, e.g. "13ac0662…"
	Files  []RevFile
}

// RevFile is a file of a Revision.
type RevFile struct {
	Path string // from the repository root (rfilename)
	Size int64
	// SHA256 is the file's checksum when it is stored with Git LFS (every
	// GGUF is), which names its blob in the hub cache; "" for a plain Git
	// file.
	SHA256 string
	BlobID string // the Git blob id (SHA-1)
}

// Revision asks the hub for the commit main points at and the files there
// with their checksums, in one request: GET
// /api/models/OWNER/REPO/revision/main?blobs=true answers "sha" and
// "siblings" with "rfilename", "size", "blobId" and, for LFS files,
// "lfs": {"sha256", "size"} (checked live with ggml-org/Qwen3-0.6B-GGUF
// on 2026-10-27).
func (c *Client) Revision(ctx context.Context, repo string) (Revision, error) {
	return c.RevisionAt(ctx, repo, "main")
}

// RevisionAt is Revision at revision, a branch or a commit: a commit
// answers itself as "sha".
func (c *Client) RevisionAt(ctx context.Context, repo, revision string) (Revision, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.ContainsAny(repo, "?#") || strings.Contains(name, "/") {
		return Revision{}, fmt.Errorf("hf: %q is not owner/repo", repo)
	}
	u := c.base() + "/api/models/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/revision/" +
		url.PathEscape(revision) + "?blobs=true"
	body, err := c.get(ctx, u)
	if err != nil {
		return Revision{}, err
	}
	var r struct {
		SHA      string `json:"sha"`
		Siblings []struct {
			Name   string `json:"rfilename"`
			Size   int64  `json:"size"`
			BlobID string `json:"blobId"`
			LFS    *struct {
				SHA256 string `json:"sha256"`
				Size   int64  `json:"size"`
			} `json:"lfs"`
		} `json:"siblings"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Revision{}, fmt.Errorf("hf: revision: %w", err)
	}
	if r.SHA == "" {
		return Revision{}, fmt.Errorf("hf: revision of %s: no commit", repo)
	}
	rev := Revision{Commit: r.SHA}
	for _, s := range r.Siblings {
		f := RevFile{Path: s.Name, Size: s.Size, BlobID: s.BlobID}
		if s.LFS != nil {
			f.SHA256 = s.LFS.SHA256
			if s.LFS.Size > 0 {
				f.Size = s.LFS.Size
			}
		}
		rev.Files = append(rev.Files, f)
	}
	return rev, nil
}

// FileURL is the download address of file at revision (a commit, or
// "main"), each part of its path escaped.
func (c *Client) FileURL(repo, revision, file string) string {
	owner, name, _ := strings.Cut(repo, "/")
	parts := strings.Split(file, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return c.base() + "/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/resolve/" +
		url.PathEscape(revision) + "/" + strings.Join(parts, "/")
}

// FileList is the files of a Revision as File values, for VariantFiles and
// Projector.
func (r Revision) FileList() []File {
	out := make([]File, len(r.Files))
	for i, f := range r.Files {
		out[i] = File{Path: f.Path, Size: f.Size}
	}
	return out
}

// VariantFiles are the GGUF files "owner/repo:TAG" names, as llama-server's
// -hf picks them: model GGUFs (not gguf.Sidecar) whose name, without
// ".gguf" and the split suffix (gguf.Stem), ends in TAG after "-", "_" or
// ".", or is TAG; in the root, or the parts of a split in a folder of its
// own. Of several such files or splits the one whose name ends in "-TAG"
// wins, then the first. A split comes with all its parts, in part order;
// nil when nothing matches or a part is missing.
func VariantFiles(files []File, tag string) []string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return nil
	}
	type group struct {
		dir, stem string
		parts     map[int]string
		total     int
		dash      bool // the name ends in "-TAG"
	}
	var groups []*group
	find := func(dir, stem string) *group {
		for _, g := range groups {
			if g.dir == dir && g.stem == stem {
				return g
			}
		}
		g := &group{dir: dir, stem: stem, parts: map[int]string{}}
		groups = append(groups, g)
		return g
	}
	for _, f := range files {
		dir, base := path.Split(f.Path)
		if !strings.HasSuffix(strings.ToLower(base), ".gguf") || gguf.Sidecar(base) != "" {
			continue
		}
		stem := gguf.Stem(base)
		low := strings.ToLower(stem)
		match := low == tag
		dash := false
		if !match && len(low) > len(tag) && strings.HasSuffix(low, tag) {
			sep := low[len(low)-len(tag)-1]
			match = sep == '-' || sep == '_' || sep == '.'
			dash = sep == '-'
		}
		if !match {
			continue
		}
		part, total := 1, 1
		if m := splitRe.FindStringSubmatch(base[:len(base)-len(".gguf")]); m != nil {
			part, _ = strconv.Atoi(m[2])
			total, _ = strconv.Atoi(m[3])
		} else if dir != "" {
			continue // a lone GGUF in a folder
		}
		if strings.Count(dir, "/") > 1 {
			continue
		}
		g := find(dir, stem)
		if g.total != 0 && g.total != total {
			continue
		}
		g.total, g.dash = total, dash || low == tag
		g.parts[part] = f.Path
	}
	var best *group
	for _, g := range groups {
		if len(g.parts) != g.total {
			continue
		}
		if best == nil || g.dash && !best.dash {
			best = g
		}
	}
	if best == nil {
		return nil
	}
	out := make([]string, 0, best.total)
	for k := 1; k <= best.total; k++ {
		p, ok := best.parts[k]
		if !ok {
			return nil
		}
		out = append(out, p)
	}
	return out
}

// Projector is the vision projector to fetch with a model: a mmproj GGUF in
// the root, F16 first, then BF16, then F32, then the first one; "" when
// there is none.
func Projector(files []File) string {
	var all []string
	for _, f := range files {
		if strings.Contains(f.Path, "/") || !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") || gguf.Sidecar(f.Path) != "mmproj" {
			continue
		}
		all = append(all, f.Path)
	}
	for _, prec := range []string{"f16", "bf16", "f32"} {
		for _, p := range all {
			low := strings.ToLower(gguf.Stem(p))
			// "bf16" ends in "f16" too: F16 must stand on its own.
			if strings.HasSuffix(low, prec) && (len(low) == len(prec) || strings.ContainsRune("-_.", rune(low[len(low)-len(prec)-1]))) {
				return p
			}
		}
	}
	if len(all) > 0 {
		return all[0]
	}
	return ""
}

// Manifest asks the hub which files "owner/repo:TAG" names, the way
// llama-server's -hf does: GET /v2/OWNER/REPO/manifests/TAG answers
// "ggufFile" and, for a model with a vision projector, "mmprojFile", each
// with its "rfilename" (checked live on 2026-10-27: ggml-org/Qwen3-0.6B-GGUF
// Q4_0 names Qwen3-0.6B-Q4_0.gguf and no projector;
// unsloth/Qwen3.8-27B-GGUF UD-Q4_K_M names Qwen3.8-27B-UD-Q4_K_M.gguf and
// mmproj-BF16.gguf). Of a split GGUF it names the first part. The hub
// answers only a client it takes for llama.cpp, by its User-Agent.
func (c *Client) Manifest(ctx context.Context, repo, tag string) (model, projector string, err error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") || tag == "" {
		return "", "", fmt.Errorf("hf: %q:%q is not owner/repo:tag", repo, tag)
	}
	u := c.base() + "/v2/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/manifests/" + url.PathEscape(tag)
	body, err := c.getUA(ctx, u, "llama-cpp")
	if err != nil {
		return "", "", err
	}
	var m struct {
		GGUF *struct {
			Name string `json:"rfilename"`
		} `json:"ggufFile"`
		MMProj *struct {
			Name string `json:"rfilename"`
		} `json:"mmprojFile"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return "", "", fmt.Errorf("hf: manifest: %w", err)
	}
	if m.GGUF == nil || m.GGUF.Name == "" {
		return "", "", fmt.Errorf("hf: manifest of %s:%s names no GGUF", repo, tag)
	}
	if m.MMProj != nil {
		projector = m.MMProj.Name
	}
	return m.GGUF.Name, projector, nil
}

// SplitParts are the paths of every part of the split GGUF whose part
// first is, in part order, from files; just first when it is no split, nil
// when a part is missing.
func SplitParts(files []File, first string) []string {
	dir, base := path.Split(first)
	stem := strings.TrimSuffix(base, path.Ext(base))
	m := splitRe.FindStringSubmatch(stem)
	if m == nil {
		return []string{first}
	}
	n, _ := strconv.Atoi(m[3])
	have := map[string]bool{}
	for _, f := range files {
		have[f.Path] = true
	}
	out := make([]string, 0, n)
	for k := 1; k <= n; k++ {
		p := fmt.Sprintf("%s%s-%05d-of-%s.gguf", dir, m[1], k, m[3])
		if !have[p] {
			return nil
		}
		out = append(out, p)
	}
	return out
}
