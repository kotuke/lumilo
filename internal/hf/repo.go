package hf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/kotuke/lumilo/internal/gguf"
)

// Rules are what the caller's engine loads of a repository's quantizations.
// The zero value takes everything.
type Rules struct {
	// RefuseGGUF are the GGUF quantization types the engine does not load,
	// by the canonical names QuantOf gives ("BF16", "TQ1_0", "PQ2_0").
	RefuseGGUF []string
	// MLX, when set, are the MLX quantizations the engine loads; an MLX
	// checkpoint without one (unquantized) is refused.
	MLX *MLXRules
}

// MLXRules are the MLX quantizations an engine loads: "affine" (the mode
// of a config.json quantization without one) in Bits and Groups, or
// another mode named in Modes.
type MLXRules struct {
	Bits   []int
	Groups []int
	Modes  []string // modes other than affine, e.g. "mxfp4", "nvfp4"
}

// RepoInfo is what Repo found about a repository.
type RepoInfo struct {
	ID string
	// GGUF: the repository holds GGUF variants; otherwise it is a
	// safetensors checkpoint (MLX) or holds no model.
	GGUF bool
	// Variants are the GGUF variants the engine loads, as Variants orders
	// them; Refused counts the ones left out by Rules.RefuseGGUF.
	Variants []Variant
	Refused  int
	// Weights is the size of the safetensors checkpoint (MLX).
	Weights int64
	// Vision is the size of the vision projector an engine would load
	// (the BF16 or F16 mmproj, else the first); 0 when there is none.
	Vision int64
	// DFlash is the smallest DFlash draft GGUF (dflash-*.gguf); 0 = none.
	DFlash     int64
	DFlashFile string
	// Context, KVElements and NextN are from the GGUF header of the first
	// variant or from config.json; 0 when unknown.
	Context    int
	KVElements int64
	NextN      int
	// Quant is the MLX quantization as config.json gives it, e.g.
	// "affine 4-bit g64", "mxfp4", "none"; "" when not asked.
	Quant string
	// Unsupported is set when the engine cannot run the checkpoint: the
	// MLX quantization Quant is not in Rules.MLX.
	Unsupported bool
}

// treeFile is an entry of the repository tree.
type treeFile struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (c *Client) tree(ctx context.Context, repo string) ([]File, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.ContainsAny(repo, "?#") || strings.Contains(name, "/") {
		return nil, fmt.Errorf("hf: %q is not owner/repo", repo)
	}
	u := c.base() + "/api/models/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/tree/main?recursive=true"
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var tree []treeFile
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, fmt.Errorf("hf: tree: %w", err)
	}
	var files []File
	for _, e := range tree {
		if e.Type == "file" {
			files = append(files, File{Path: e.Path, Size: e.Size})
		}
	}
	return files, nil
}

// headerBytes is how much of a GGUF Repo reads for its header: the
// architecture's keys come before the tokenizer's, within the first few
// kilobytes of every file seen (OrcaCyber-Nano: 3 KB).
const headerBytes = 512 << 10

// fileURL is the download address of a repository file.
func (c *Client) fileURL(repo, file string) string {
	owner, name, _ := strings.Cut(repo, "/")
	parts := strings.Split(file, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return c.base() + "/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/resolve/main/" + strings.Join(parts, "/")
}

// Header reads the GGUF header of a repository file with one ranged
// request.
func (c *Client) Header(ctx context.Context, repo, file string) (gguf.Meta, error) {
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.fileURL(repo, file), nil)
	if err != nil {
		return gguf.Meta{}, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", headerBytes-1))
	resp, err := hc.Do(req)
	if err != nil {
		return gguf.Meta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return gguf.Meta{}, fmt.Errorf("hf: %s: %s", resp.Status, file)
	}
	return gguf.Read(io.LimitReader(resp.Body, headerBytes))
}

// Repo looks at a repository for the "Add model…" field: the variants of
// a GGUF repository that rules let through, with the header of the first
// for the KV cache, or the config.json of a safetensors (MLX) checkpoint.
// It asks the hub one to three times: the tree, then a header or config.
func (c *Client) Repo(ctx context.Context, repo string, rules Rules) (RepoInfo, error) {
	info := RepoInfo{ID: repo}
	files, err := c.tree(ctx, repo)
	if err != nil {
		return info, err
	}
	all := ParseVariants(repo, files)
	if len(all) == 0 {
		return c.checkpoint(ctx, info, files, rules)
	}
	info.GGUF = true
	info.Vision, info.DFlash, info.DFlashFile = sidecars(files)

	// The header of the first variant names the model, which shortens the
	// tag of a lone GGUF, and gives the shape of its KV cache.
	if first := firstFile(files, all[0]); first != "" {
		if meta, err := c.Header(ctx, repo, first); err == nil {
			info.Context, info.KVElements, info.NextN = meta.Context, meta.KVElements(), meta.NextN
			if len(all) == 1 && all[0].Parts == 1 {
				all[0].Tag = tagOfLone(repo, first, meta.DisplayName(), all[0].Tag)
			} else if named := parseVariants(repo, files, meta.DisplayName()); len(named) == len(all) {
				all = named
			}
		}
	}
	for _, v := range all {
		if q := QuantOf(v.Tag); q != "" && slices.Contains(rules.RefuseGGUF, q) {
			info.Refused++
			continue
		}
		info.Variants = append(info.Variants, v)
	}
	return info, nil
}

// tagOfLone is the tag of the only GGUF of a repository: the file name past
// the model's name, when that is shorter than the tag past the repository's
// name (Mix-15.7G, not AI-Defense-Mix-15.7G).
func tagOfLone(repo, file, model, tag string) string {
	if t := gguf.Tag(file, gguf.RepoBase(repo), model); t != "" && len(t) < len(tag) {
		return t
	}
	return tag
}

// firstFile is the first file of variant v (its first part).
func firstFile(files []File, v Variant) string {
	for _, f := range files {
		base := path.Base(f.Path)
		if gguf.Sidecar(base) != "" || !strings.HasSuffix(strings.ToLower(base), ".gguf") {
			continue
		}
		stem := gguf.Stem(base)
		if !strings.HasSuffix(strings.ToLower(stem), strings.ToLower(v.Tag)) {
			continue
		}
		if v.Parts > 1 && !strings.Contains(base, "-00001-of-") {
			continue
		}
		return f.Path
	}
	return ""
}

// sidecars are the sizes of the vision projector (BF16, else F16, else the
// first) and of the smallest DFlash draft in the root.
func sidecars(files []File) (vision, dflash int64, dflashFile string) {
	var first int64
	for _, f := range files {
		if strings.Contains(f.Path, "/") {
			continue
		}
		low := strings.ToLower(f.Path)
		switch gguf.Sidecar(f.Path) {
		case "mmproj":
			if first == 0 {
				first = f.Size
			}
			if vision == 0 && (strings.Contains(low, "bf16") || strings.Contains(low, "f16")) {
				vision = f.Size
			}
		case "dflash":
			if dflash == 0 || f.Size < dflash {
				dflash, dflashFile = f.Size, f.Path
			}
		}
	}
	if vision == 0 {
		vision = first
	}
	return vision, dflash, dflashFile
}

// checkpoint reads a safetensors checkpoint: its size from the tree, and
// its quantization, context and attention from config.json.
func (c *Client) checkpoint(ctx context.Context, info RepoInfo, files []File, rules Rules) (RepoInfo, error) {
	hasConfig := false
	for _, f := range files {
		switch {
		case f.Path == "config.json":
			hasConfig = true
		case strings.HasSuffix(f.Path, ".safetensors") && !strings.Contains(f.Path, "/"):
			info.Weights += f.Size
		}
	}
	if !hasConfig || info.Weights == 0 {
		return info, nil
	}
	body, err := c.get(ctx, c.fileURL(info.ID, "config.json"))
	if err != nil {
		return info, err
	}
	cfg := ParseConfig(body)
	info.Context, info.KVElements, info.Quant = cfg.Context, cfg.KVElements, cfg.Quant
	if rules.MLX != nil {
		info.Unsupported = !rules.MLX.allows(cfg)
	}
	return info, nil
}

// Config is what ParseConfig reads from a config.json.
type Config struct {
	Context    int   // max_position_embeddings
	KVElements int64 // K and V elements per token, all attention layers
	// Quant names the quantization: "affine 4-bit g64", "mxfp4", "none";
	// "other" for a quantization_config of another tool (Model Optimizer,
	// compressed-tensors).
	Quant string
	Mode  string
	Bits  int
	Group int
}

// ParseConfig reads a Transformers or MLX config.json: the text model's
// sizes at the top or under text_config, and MLX's quantization
// ({"group_size": 64, "bits": 4, "mode": "affine"}, also kept as
// quantization_config).
func ParseConfig(data []byte) Config {
	type text struct {
		Max      int      `json:"max_position_embeddings"`
		Layers   int      `json:"num_hidden_layers"`
		KVHeads  int      `json:"num_key_value_heads"`
		Heads    int      `json:"num_attention_heads"`
		HeadDim  int      `json:"head_dim"`
		Hidden   int      `json:"hidden_size"`
		Interval int      `json:"full_attention_interval"`
		Types    []string `json:"layer_types"`
	}
	type quant struct {
		Group  int    `json:"group_size"`
		Bits   int    `json:"bits"`
		Mode   string `json:"mode"`
		Method string `json:"quant_method"`
	}
	var c struct {
		text
		Text   *text  `json:"text_config"`
		Quant  *quant `json:"quantization"`
		QuantC *quant `json:"quantization_config"`
	}
	var out Config
	if json.Unmarshal(data, &c) != nil {
		return out
	}
	t := c.text
	if c.Text != nil {
		t = *c.Text
	}
	out.Context = t.Max
	dim := t.HeadDim
	if dim == 0 && t.Heads > 0 {
		dim = t.Hidden / t.Heads
	}
	if t.Layers > 0 && t.KVHeads > 0 && dim > 0 {
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
		out.KVElements = int64(layers) * int64(t.KVHeads) * int64(2*dim)
	}
	q := c.Quant
	if q == nil && c.QuantC != nil && c.QuantC.Bits > 0 {
		q = c.QuantC
	}
	switch {
	case q != nil:
		out.Mode, out.Bits, out.Group = q.Mode, q.Bits, q.Group
		if out.Mode == "" {
			out.Mode = "affine"
		}
		if out.Mode == "affine" {
			out.Quant = fmt.Sprintf("affine %d-bit g%d", q.Bits, q.Group)
		} else {
			out.Quant = out.Mode
		}
	case c.QuantC != nil:
		out.Quant, out.Mode = "other", "other"
	default:
		out.Quant = "none"
	}
	return out
}

func (r *MLXRules) allows(c Config) bool {
	switch c.Mode {
	case "":
		return false // unquantized
	case "other":
		return true // another tool's format: the engine decides
	case "affine":
		return slices.Contains(r.Bits, c.Bits) && slices.Contains(r.Groups, c.Group)
	}
	return slices.Contains(r.Modes, c.Mode)
}

// quantTagRe finds a quantization name in a variant tag.
var quantTagRe = regexp.MustCompile(`(?i)(?:^|[-_.])((?:UD-)?(?:I?Q\d_(?:K|\d|XXS|XS|NL|S|M)(?:_(?:XL|XS|S|M|L|P|MOE))*|[TP]Q\d_0|MXFP4(?:_MOE)?|NVFP4|BF16|F16|F32))(?:$|[-_.])`)

// QuantOf is the GGUF tensor type a variant tag names, as llama.cpp's
// ggml type names spell it: "Q4_K" for Q4_K_M or UD-Q4_K_XL, "IQ3_S" for
// IQ3_M and IQ3_XS (mixes of IQ3_S tensors), "MXFP4" for MXFP4_MOE; "" when
// the tag names none ("Mix-15.7G").
func QuantOf(tag string) string {
	m := quantTagRe.FindAllStringSubmatch(tag, -1)
	if m == nil {
		return ""
	}
	q := strings.ToUpper(m[len(m)-1][1])
	q = strings.TrimPrefix(q, "UD-")
	switch {
	case strings.HasPrefix(q, "MXFP4"):
		return "MXFP4"
	case strings.HasPrefix(q, "IQ"):
		switch q {
		case "IQ2_M":
			return "IQ2_S"
		case "IQ3_M", "IQ3_XS":
			return "IQ3_S"
		}
		return q
	case len(q) >= 4 && q[0] == 'Q' && q[2] == '_' && q[3] == 'K':
		return q[:4]
	case len(q) >= 4 && q[0] == 'Q' && q[2] == '_':
		return q[:4]
	}
	return q
}
