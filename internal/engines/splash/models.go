package splash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/gguf"
)

// Splash keeps a model as a symlink models/<owner>/<repo>[:<variant>] to an
// assembly directory models/.resolved/<sha> whose model.json lists the files
// (install/models.py selection_link, installation_kind; install/assembly.py).
// A link whose directory holds model.json is servable. One that holds
// manifest.json is an old Splash package that 1.4 refuses
// (install/models.py refuse_package). Links with revision, draft or
// language-only options are hashed under .selections; of those, only the
// language-only one of a model is listed (textOnlyLink), as the model's ID
// without vision: Lumilo installs it for a model whose repository has no
// vision projector.
//
// Splash serves only the families in install/families.py and has no marker
// for non-LLM models, so everything servable is listed except DFlash2 drafts.

// The model ID syntax of server/serve_options.py (REPO_ID, VARIANT).
var (
	repoPartRe = regexp.MustCompile(`^[A-Za-z0-9_](?:[A-Za-z0-9._-]*[A-Za-z0-9_])?$`)
	variantRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Models lists the servable models under the models directory.
func (s *Splash) Models(ctx context.Context) ([]engine.Model, error) {
	return listModels(ctx, s.modelsDir)
}

func listModels(ctx context.Context, root string) ([]engine.Model, error) {
	owners, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []engine.Model
	for _, o := range owners {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !o.IsDir() || strings.HasPrefix(o.Name(), ".") || !repoPartRe.MatchString(o.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, o.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type()&os.ModeSymlink == 0 || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			repo, variant, hasVariant := strings.Cut(e.Name(), ":")
			if !repoPartRe.MatchString(repo) || (hasVariant && !variantRe.MatchString(variant)) {
				continue
			}
			id := o.Name() + "/" + e.Name()
			if strings.Contains(strings.ToLower(id), "dflash") {
				continue
			}
			if m, ok := readModel(filepath.Join(root, o.Name(), e.Name()), id); ok {
				out = append(out, m)
			}
		}
	}
	// Text-only installations (--language-only) live under .selections,
	// named by a hash of the selection (selectionLink); one whose plain
	// link is listed already is the same model.
	listed := map[string]bool{}
	for _, m := range out {
		listed[m.ID] = true
	}
	sels, _ := os.ReadDir(filepath.Join(root, ".selections"))
	for _, e := range sels {
		if e.Type()&os.ModeSymlink == 0 || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, ".selections", e.Name())
		a, ok := readAssembly(dir)
		if !ok || listed[a.model] || filepath.Base(textOnlyLink(root, a.model)) != e.Name() {
			continue // another kind of selection (revision, draft, local files)
		}
		if strings.Contains(strings.ToLower(a.model), "dflash") {
			continue
		}
		if m, ok := readModel(dir, a.model); ok {
			listed[m.ID] = true
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
	})
	return out, nil
}

// readModel reads the assembly at dir, the selection link of id.
func readModel(dir, id string) (engine.Model, bool) {
	a, ok := readAssembly(dir)
	if !ok {
		return engine.Model{}, false
	}
	owner, rest, _ := strings.Cut(id, "/")
	repo, variant, _ := strings.Cut(rest, ":")
	m := engine.Model{ID: id, Name: repo, Owner: owner, Variant: variant, SizeBytes: a.target,
		ContextLength: nativeContext(dir), Features: a.features()}
	m.KVElements = configKV(dir)
	if a.gguf != "" {
		// The tag both engines derive from the file name (gguf.Tag),
		// not the one typed: "Mix-15.7G" and "AI-Defense-Mix-15.7G"
		// name the same file.
		if meta, err := gguf.ReadFile(a.gguf); err == nil {
			m.Variant = gguf.Tag(a.gguf, repo, meta.DisplayName())
			if kv := meta.KVElements(); kv > 0 {
				m.KVElements = kv
			}
		}
	}
	return m, true
}

// textOnlyLink is the selection link of id served text only ("splash
// serve --model id --language-only"): .selections/<SHA-256 of the compact
// JSON ["id",null,true,null]> under root (install/models.py selection_link
// and _hashed_link: [model, revision, language_only, draft_model]). Checked
// against "install/models.py --model … --language-only link" on 2026-10-27.
func textOnlyLink(root, id string) string {
	data, _ := json.Marshal([]any{id, nil, true, nil})
	sum := sha256.Sum256(data)
	return filepath.Join(root, ".selections", hex.EncodeToString(sum[:]))
}

// plainLink is the selection link of id with vision: models/OWNER/REPO[:VARIANT].
func plainLink(root, id string) string { return filepath.Join(root, filepath.FromSlash(id)) }

// assembly is what model.json of an assembly lists, by part. model.json
// maps each assembly path to a record with "bytes" and the source "path"
// (install/upstream.py assembly_record); "sources" names the target's and
// the draft's repositories.
type assembly struct {
	target int64  // target/: the model's weights (GGUF parts or MLX shards)
	vision int64  // vision/: the vision tower or projector; 0 = none
	draft  int64  // draft/: the DFlash2 draft, as stored (BF16)
	gguf   string // the first target GGUF, "" for an MLX target
	drafts string // the draft's repository, e.g. "incoai/Qwen3.8-27B-DFlash2"
	model  string // the model ID the assembly was installed for
}

// draftShrink is what Splash keeps in memory of its BF16 DFlash2 draft: it
// quantizes the weights to 4 bits in groups of 64 ("a BF16 weight is
// quantized into whole af4g64 groups", engine/splash). Measured on
// OrcaCyber-Nano: draft_weights_bytes 1,266,647,040 of /status for the
// 3,848,817,896 bytes of incoai/Qwen3.8-27B-DFlash2 (0.329).
const draftShrink = 0.329

// draftMemory is what a DFlash2 draft of that many bytes on disk takes in
// memory.
func draftMemory(bytes int64) int64 { return int64(float64(bytes) * draftShrink) }

// readAssembly reports whether dir is a servable assembly, and its parts.
func readAssembly(dir string) (assembly, bool) {
	var a assembly
	data, err := os.ReadFile(filepath.Join(dir, "model.json"))
	if err != nil {
		return a, false // missing, or a package (manifest.json) that 1.4 refuses
	}
	var m struct {
		Model string `json:"model"`
		Files map[string]struct {
			Bytes int64  `json:"bytes"`
			Path  string `json:"path"`
		} `json:"files"`
		Sources struct {
			Draft struct {
				Repo string `json:"repo"`
			} `json:"draft"`
		} `json:"sources"`
	}
	if json.Unmarshal(data, &m) != nil {
		return a, false
	}
	var first string
	for name, f := range m.Files {
		switch {
		case strings.HasPrefix(name, "target/"):
			if f.Path != "" {
				if _, err := os.Stat(f.Path); err != nil {
					// Its weights are gone (deleted from the hub cache by
					// another program, or by llama.cpp's Remove): Splash
					// cannot serve it.
					return a, false
				}
			}
			a.target += f.Bytes
			if strings.HasSuffix(name, ".gguf") && (first == "" || name < first) {
				first, a.gguf = name, f.Path
			}
		case strings.HasPrefix(name, "vision/") && name != "vision/config.json":
			a.vision += f.Bytes
		case strings.HasPrefix(name, "draft/"):
			a.draft += f.Bytes
		}
	}
	a.drafts = m.Sources.Draft.Repo
	a.model = m.Model
	return a, true
}

// features: the vision tower when the assembly holds one, and the DFlash2
// draft, which Splash always runs: "--draft-model" only names another one
// (install/launcher.py:408-414), and a family without a draft is refused
// (install/upstream.py _resolve_draft, lines 704-716).
func (a assembly) features() engine.Features {
	f := engine.Features{Vision: a.vision > 0, VisionBytes: a.vision}
	if a.drafts != "" || a.draft > 0 {
		name := a.drafts
		if name == "" {
			name = "DFlash2"
		}
		f.Drafts = []engine.Draft{{Kind: engine.DraftDFlash, Name: name, ID: name,
			SizeBytes: draftMemory(a.draft), Always: true}}
	}
	return f
}

// configKV is the KV elements per token from an MLX assembly's config.json:
// text_config's layers, KV heads and head size; a hybrid's linear-attention
// layers (layer_types "linear_attention", or all but every
// full_attention_interval-th) keep no KV cache. 0 when it does not say.
func configKV(dir string) int64 {
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return 0
	}
	return ConfigKV(data)
}

// ConfigKV is configKV of the config.json data.
func ConfigKV(data []byte) int64 {
	type text struct {
		Layers   int      `json:"num_hidden_layers"`
		KVHeads  int      `json:"num_key_value_heads"`
		Heads    int      `json:"num_attention_heads"`
		HeadDim  int      `json:"head_dim"`
		Hidden   int      `json:"hidden_size"`
		Interval int      `json:"full_attention_interval"`
		Types    []string `json:"layer_types"`
	}
	var c struct {
		text
		Text *text `json:"text_config"`
	}
	if json.Unmarshal(data, &c) != nil {
		return 0
	}
	t := c.text
	if c.Text != nil {
		t = *c.Text
	}
	dim := t.HeadDim
	if dim == 0 && t.Heads > 0 {
		dim = t.Hidden / t.Heads
	}
	if t.Layers <= 0 || t.KVHeads <= 0 || dim <= 0 {
		return 0
	}
	layers := t.Layers
	switch {
	case len(t.Types) > 0:
		layers = 0
		for _, ty := range t.Types {
			if !strings.Contains(ty, "linear") {
				layers++
			}
		}
	case t.Interval > 0:
		layers /= t.Interval
	}
	return int64(layers) * int64(t.KVHeads) * int64(2*dim)
}

// nativeContext is the model's native window from the assembly's
// config.json (a link into models/.metadata): max_position_embeddings of
// its text_config (Qwen3.5 layout, 262144 for the installed OrcaCyber
// model), else at the top level; 0 when neither is there. Splash serves
// that window by default and turns YaRN on past it ("splash serve --help",
// --max-context).
func nativeContext(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return 0
	}
	var c struct {
		Max  int `json:"max_position_embeddings"`
		Text struct {
			Max int `json:"max_position_embeddings"`
		} `json:"text_config"`
	}
	if json.Unmarshal(data, &c) != nil {
		return 0
	}
	if c.Text.Max > 0 {
		return c.Text.Max
	}
	return max(c.Max, 0)
}

// ParseModel checks "owner/repo[:VARIANT]" with the patterns of
// server/serve_options.py, which "splash serve --model" applies.
func (s *Splash) ParseModel(spec string) (engine.Model, error) {
	spec = strings.TrimSpace(spec)
	repoID, variant, hasVariant := strings.Cut(spec, ":")
	owner, repo, ok := strings.Cut(repoID, "/")
	if !ok || !repoPartRe.MatchString(owner) || !repoPartRe.MatchString(repo) ||
		(hasVariant && !variantRe.MatchString(variant)) {
		return engine.Model{}, fmt.Errorf("splash: %q is not OWNER/REPO[:VARIANT]", spec)
	}
	return engine.Model{ID: spec, Name: repo, Owner: owner, Variant: variant}, nil
}
