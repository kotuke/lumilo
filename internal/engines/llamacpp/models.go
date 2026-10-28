package llamacpp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/gguf"
)

// Model.ID is the absolute path of the GGUF file (of its first part when it
// is split), which ServeCommand passes as -m. A path, not an "-hf
// owner/repo:quant" name, because:
//   - it needs no network and no resolution: -hf with --offline still has to
//     map the quant tag to a file, and llama.cpp's tag of
//     "OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf" is "7G"
//     ("llama-server -cl", live);
//   - GET /props reports model_path, the same path, so a running server
//     maps back to its Model (fixture ready.props);
//   - it works for files outside any cache.
//
// Models lists GGUF files in the Hugging Face hub caches and in the flat
// cache directories (llamacpp.go hubDirs, flatDirs). It reads each file's
// GGUF header and keeps only text models: see isTextModel.

var splitRe = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

// Models lists the GGUF text models found locally.
func (l *LlamaCpp) Models(ctx context.Context) ([]engine.Model, error) {
	var out []engine.Model
	seen := map[string]bool{}
	add := func(ms []engine.Model) {
		for _, m := range ms {
			if !seen[m.ID] {
				seen[m.ID] = true
				out = append(out, m)
			}
		}
	}
	for _, d := range l.hubDirs {
		ms, err := hubModels(ctx, d)
		if err != nil {
			return nil, err
		}
		add(ms)
	}
	for _, d := range l.flatDirs {
		ms, err := flatModels(ctx, d)
		if err != nil {
			return nil, err
		}
		add(ms)
	}
	sort.Slice(out, func(i, j int) bool { return sortKey(out[i]) < sortKey(out[j]) })
	return out, nil
}

func sortKey(m engine.Model) string {
	return strings.ToLower(m.Owner + "/" + m.Name + ":" + m.Variant + "\x00" + m.ID)
}

// hubModels reads a hub cache: models--<owner>--<repo>/snapshots/<commit>/
// holds symlinks into blobs/. A file present in several snapshots is
// listed once, from the snapshot that refs/main names, else the newest.
func hubModels(ctx context.Context, root string) ([]engine.Model, error) {
	repos, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []engine.Model
	for _, r := range repos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rest, ok := strings.CutPrefix(r.Name(), "models--")
		if !ok || !r.IsDir() {
			continue
		}
		owner, repo, ok := strings.Cut(rest, "--")
		if !ok || owner == "" || repo == "" {
			continue
		}
		dir := filepath.Join(root, r.Name())
		main := ""
		if b, err := os.ReadFile(filepath.Join(dir, "refs", "main")); err == nil {
			main = strings.TrimSpace(string(b))
		}
		snaps, err := os.ReadDir(filepath.Join(dir, "snapshots"))
		if err != nil {
			continue
		}
		// The refs/main snapshot first, then the newest.
		sort.SliceStable(snaps, func(i, j int) bool {
			if (snaps[i].Name() == main) != (snaps[j].Name() == main) {
				return snaps[i].Name() == main
			}
			return modTime(snaps[i]) > modTime(snaps[j])
		})
		files := map[string]bool{}
		for _, s := range snaps {
			for _, m := range snapshotModels(filepath.Join(dir, "snapshots", s.Name())) {
				base := filepath.Base(m.ID)
				if files[base] {
					continue
				}
				files[base] = true
				m.Owner, m.Variant = owner, variantOf(repo, base, m.Name)
				m.Name = repo
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// snapshotModels are the models of a snapshot: in its root, and the split
// GGUFs repositories keep in a folder of their own one level down
// ("Q8_0/model-Q8_0-00001-of-00002.gguf", as hf.ParseVariants offers them
// and Download fetches them).
func snapshotModels(snap string) []engine.Model {
	out := ggufModels(snap)
	entries, _ := os.ReadDir(snap)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			for _, m := range ggufModels(filepath.Join(snap, e.Name())) {
				if splitRe.MatchString(filepath.Base(m.ID)) {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

func modTime(e os.DirEntry) int64 {
	if fi, err := e.Info(); err == nil {
		return fi.ModTime().UnixNano()
	}
	return 0
}

// flatModels reads a directory of .gguf files. Their names carry no
// reliable owner, so the name is the file name.
func flatModels(ctx context.Context, root string) ([]engine.Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
	ms := ggufModels(root)
	for i := range ms {
		ms[i].Name, ms[i].Variant = stem(filepath.Base(ms[i].ID)), ""
	}
	return ms, nil
}

// ggufModels returns the text models among the .gguf files of dir, with ID
// and SizeBytes set. Of a split model only part 1 is listed, with the size
// of all parts; one with a missing part is left out.
func ggufModels(dir string) []engine.Model {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []engine.Model
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".gguf") || strings.HasPrefix(name, ".") || gguf.Sidecar(name) != "" {
			continue
		}
		path := filepath.Join(dir, name)
		size, ok := fileSize(path) // follows the snapshot symlink
		if !ok {
			continue
		}
		if m := splitRe.FindStringSubmatch(name); m != nil {
			if m[2] != "00001" {
				continue
			}
			total, ok := splitSize(dir, m[1], m[3])
			if !ok {
				continue
			}
			size = total
		}
		meta, err := gguf.ReadFile(path)
		if err != nil || !meta.TextModel() {
			continue
		}
		out = append(out, engine.Model{ID: path, SizeBytes: size, ContextLength: meta.Context,
			KVElements: meta.KVElements(), Features: features(path, meta), Name: meta.DisplayName()})
	}
	return out
}

// features are what llama-server can load with the model at path:
//   - its vision projector, the only mmproj-*.gguf beside it (mmprojFor),
//     passed as --mmproj unless Config.NoVision;
//   - its own multi-token prediction layers (<arch>.nextn_predict_layers,
//     1 in OrcaCyber-Nano), run by "--spec-type draft-mtp": libllama 0.7.0
//     builds an MTP graph for qwen35, qwen35moe, qwen4exp, glm4_moe,
//     deepseek and others, and refuses a model without such layers ("context
//     type MTP requested but model doesn't contain MTP layers", checked live
//     with Qwen3-0.6B);
//   - a DFlash draft beside it, the only dflash-*.gguf (the name -hf gives
//     the DFlash sidecar of ggml-org's repositories), passed as -md with
//     "--spec-type draft-dflash".
func features(path string, meta gguf.Meta) engine.Features {
	var f engine.Features
	if mm := mmprojFor(path); mm != "" {
		f.Vision = true
		f.VisionBytes, _ = fileSize(mm)
	}
	if meta.NextN > 0 {
		f.Drafts = append(f.Drafts, engine.Draft{Kind: engine.DraftMTP, Name: "MTP", ID: draftMTP})
	}
	if d := sidecarFor(path, "dflash"); d != "" {
		size, _ := fileSize(d)
		f.Drafts = append(f.Drafts, engine.Draft{Kind: engine.DraftDFlash, Name: gguf.Stem(d), ID: d, SizeBytes: size})
	}
	return f
}

// draftMTP is the Config.Draft of the model's own MTP layers.
const draftMTP = "mtp"

func fileSize(path string) (int64, bool) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return 0, false
	}
	return st.Size(), true
}

// splitSize sums <base>-0000k-of-<n>.gguf for k = 1..n.
func splitSize(dir, base, n string) (int64, bool) {
	var count int
	if _, err := fmt.Sscanf(n, "%d", &count); err != nil || count < 1 {
		return 0, false
	}
	var total int64
	for k := 1; k <= count; k++ {
		size, ok := fileSize(filepath.Join(dir, fmt.Sprintf("%s-%05d-of-%s.gguf", base, k, n)))
		if !ok {
			return 0, false
		}
		total += size
	}
	return total, true
}

// isMmprojName: multimodal projectors are named mmproj-*.gguf or
// *-mmproj-*.gguf by convention (llama.cpp's -hf picks them that way).
func isMmprojName(name string) bool { return gguf.Sidecar(name) == "mmproj" }

func stem(name string) string { return gguf.Stem(name) }

// variantOf is the variant tag of a GGUF file of repo whose general.name is
// model: the file name without the longest of the repository's name and
// the model's name it starts with (gguf.Tag), e.g. "Q8_0" for
// Qwen3-0.6B-GGUF/Qwen3-0.6B-Q8_0.gguf and "Mix-15.7G" for
// OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf ("OrcaCyber Nano 1.0 AI
// Defense"). Splash's model list derives its tags the same way, so one
// file has one name in both engines.
func variantOf(repo, file, model string) string {
	return gguf.Tag(file, gguf.RepoBase(repo), model)
}
