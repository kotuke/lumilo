package tensorfold

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// families are the model types the native Metal engine serves and the
// checkpoint formats it reads for each, from "tensorfold capabilities
// --json" of 1.1.9 on an Apple M5 (testdata/capabilities.json "families").
// Left out: fish_omni (Drama speech, not a chat model) and glm_moe_dsa
// (EXLR8, a format Lumilo does not download). "tensorfold pull" refuses
// any other model type ("no registered Zig family serves model_type
// lfm3", fixture pull-refused-lfm3.txt); Models and Download do the same.
var families = map[string][]string{
	"gemma4":         mlxFormats([]int{2, 3, 4, 5, 6, 8}, []int{64}),
	"gemma4_unified": mlxFormats([]int{2, 3, 4, 5, 6, 8}, []int{64}),
	"glm5_next":      {"mlx-q4g64"},
	"lfm2_moe":       mlxFormats([]int{4, 6, 8}, []int{64}),
	"nemotron_h":     mlxFormats([]int{2, 3, 4, 5, 6, 8}, []int{32, 64, 128}),
	"qwen3_5":        mlxFormats([]int{2, 3, 4, 5, 6, 8}, []int{32, 64, 128}),
	"qwen4":          append([]string{"bf16"}, mlxFormats([]int{4, 8}, []int{32, 64, 128})...),
	"qwen4_exp":      mlxFormats([]int{2, 3, 4, 5, 6, 8}, []int{32, 64, 128}),
	"qwen4_text":     append([]string{"bf16"}, mlxFormats([]int{4, 8}, []int{32, 64, 128})...),
	"spark2_5":       append([]string{"bf16"}, mlxFormats([]int{2, 3, 4, 5, 6, 8}, []int{64})...),
	"step5_vl":       {"mlx-q2g128"},
}

// capabilitiesFamilies reads the families of "tensorfold capabilities
// --json" (testdata/capabilities.json "families"), with the formats Lumilo
// downloads: MLX ones and bf16. A family with none of them (fish_omni's
// fp8-block128, glm_moe_dsa's exlr8) is left out.
func capabilitiesFamilies(data []byte) (map[string][]string, error) {
	var c struct {
		Families map[string][]string `json:"families"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for name, formats := range c.Families {
		for _, f := range formats {
			if f == "bf16" || strings.HasPrefix(f, "mlx-") {
				out[name] = append(out[name], f)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("capabilities list no family")
	}
	return out, nil
}

// capabilitiesNoJPEG reports whether "capabilities --json" says the JPEG
// decoder is missing or of another release: its optional library
// libjpeg-turbo is not "usable" (testdata/capabilities.json: Homebrew's
// 3.2.1 where the server needs 3.1.4.1, which Homebrew does not have;
// the server then answers a JPEG "JPEG images need libjpeg-turbo
// 3.1.4.1", and serves PNG). A list without it says nothing.
func capabilitiesNoJPEG(data []byte) bool {
	var c struct {
		Libraries []struct {
			Name   string `json:"name"`
			Usable *bool  `json:"usable"`
		} `json:"optional_libraries"`
	}
	if json.Unmarshal(data, &c) != nil {
		return false
	}
	for _, l := range c.Libraries {
		if l.Name == "libjpeg-turbo" && l.Usable != nil {
			return !*l.Usable
		}
	}
	return false
}

// families are the families the installed TensorFold serves, from its
// "capabilities --json", read again when its executable changes (an
// update); the list of families.go when that fails (no TensorFold yet, an
// older build without the command). It may run the executable for a
// moment.
func (tf *TensorFold) families(ctx context.Context) map[string][]string {
	f, _ := tf.capabilities(ctx)
	return f
}

// capabilities are the families (families) and whether JPEG images are
// refused (capabilitiesNoJPEG) of the installed executable, read once per
// executable.
func (tf *TensorFold) capabilities(ctx context.Context) (map[string][]string, bool) {
	bin := tf.binPath()
	st, err := os.Stat(bin)
	if err != nil {
		return families, false
	}
	real, _ := filepath.EvalSymlinks(bin)
	key := real + "@" + st.ModTime().String()
	tf.mu.Lock()
	if tf.famKey == key && tf.fams != nil {
		f, nj := tf.fams, tf.noJPEG
		tf.mu.Unlock()
		return f, nj
	}
	tf.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "capabilities", "--json")
	cmd.Env = environ()
	out, err := cmd.Output()
	fams, noJPEG := families, false
	if err == nil {
		if f, perr := capabilitiesFamilies(out); perr == nil {
			fams = f
		}
		noJPEG = capabilitiesNoJPEG(out)
	}
	tf.mu.Lock()
	tf.famKey, tf.fams, tf.noJPEG = key, fams, noJPEG
	tf.mu.Unlock()
	return fams, noJPEG
}

// mlxFormats are the capabilities' names of MLX affine quantizations:
// "mlx-q<bits>g<group>".
func mlxFormats(bits, groups []int) []string {
	var out []string
	for _, b := range bits {
		for _, g := range groups {
			out = append(out, fmt.Sprintf("mlx-q%dg%d", b, g))
		}
	}
	return out
}

// checkpoint is what a config.json says about a checkpoint, for the
// family checks.
type checkpoint struct {
	ModelType string
	Format    string // "mlx-q4g64", "bf16", or the quantization another tool wrote ("other")
	Vision    bool   // the config has a vision tower (vision_config)
	MTP       int    // multi-token prediction layers
	// Dense27: a qwen3_5 checkpoint of the Qwen3.8-27B class.
	Dense27 bool
	// Drafter: a DFlash2 drafter for --drafter, which config.json marks
	// with "dflash_config" (pull.zig configKind), e.g.
	// z-lab/Qwen3.8-27B-DFlash2 (model_type qwen3).
	Drafter bool
}

// parseCheckpoint reads config.json. Formats follow the capabilities'
// names: MLX's "quantization" {"bits": 4, "group_size": 64} (mode affine,
// the default) is "mlx-q4g64"; no quantization is "bf16".
func parseCheckpoint(data []byte) (checkpoint, error) {
	type quant struct {
		Bits  int    `json:"bits"`
		Group int    `json:"group_size"`
		Mode  string `json:"mode"`
	}
	type text struct {
		ModelType string `json:"model_type"`
		Hidden    int    `json:"hidden_size"`
		Tied      *bool  `json:"tie_word_embeddings"`
		MTP       int    `json:"mtp_num_hidden_layers"`
		NextN     int    `json:"num_nextn_predict_layers"`
	}
	var c struct {
		text
		Text   *text           `json:"text_config"`
		Vision json.RawMessage `json:"vision_config"`
		DFlash json.RawMessage `json:"dflash_config"`
		Quant  *quant          `json:"quantization"`
		QuantC json.RawMessage `json:"quantization_config"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return checkpoint{}, err
	}
	t := c.text
	if c.Text != nil {
		t = *c.Text
	}
	cp := checkpoint{ModelType: c.ModelType, Vision: present(c.Vision), Drafter: present(c.DFlash),
		MTP: max(t.MTP, t.NextN, c.MTP, c.NextN)}
	switch {
	case c.Quant != nil && (c.Quant.Mode == "" || c.Quant.Mode == "affine"):
		cp.Format = fmt.Sprintf("mlx-q%dg%d", c.Quant.Bits, c.Quant.Group)
	case c.Quant != nil:
		cp.Format = c.Quant.Mode
	case len(c.QuantC) > 0 && string(c.QuantC) != "null":
		cp.Format = "other"
	default:
		cp.Format = "bf16"
	}
	// metal.zig qwen27Engine: a qwen3_5 checkpoint is the 27B's unless it
	// ties its embeddings or has the 2B's hidden size (2048), at the top
	// or in text_config.
	if cp.ModelType == "qwen3_5" {
		cp.Dense27 = true
		for _, x := range []text{c.text, t} {
			if (x.Tied != nil && *x.Tied) || x.Hidden == qwen35SmallHidden {
				cp.Dense27 = false
			}
		}
	}
	return cp, nil
}

// present: a JSON field that is there and not null.
func present(v json.RawMessage) bool { return len(v) > 0 && string(v) != "null" }

// qwen35SmallHidden is the hidden size of Qwen3.5-2B (tf.qwen35.config.hidden).
const qwen35SmallHidden = 2048

// supported says why the engine cannot serve cp with the families fams
// (TensorFold.families), or "" when it can. A drafter is not served: it
// goes with a model (--drafter).
func (cp checkpoint) supported(fams map[string][]string) string {
	if cp.Drafter {
		return "a DFlash2 drafter is not a model of its own"
	}
	formats, ok := fams[cp.ModelType]
	if !ok {
		return fmt.Sprintf("TensorFold does not serve model_type %q", cp.ModelType)
	}
	if !slices.Contains(formats, cp.Format) {
		return fmt.Sprintf("TensorFold does not read %s checkpoints in format %s", cp.ModelType, cp.Format)
	}
	return ""
}

// visionOK: the engine reads images for the model with --vision. The
// native Metal engine refuses --vision for every family but GLM-5.3-Flash,
// Qwen3.8-27B, Qwen3.8 Flash Next, Gemma 4 and Step-5 (metal.zig open;
// fixture lifecycle/vision.stderr, refused for Qwen3.5-2B), and a
// checkpoint without a vision tower has nothing to read them with.
func (cp checkpoint) visionOK() bool {
	if !cp.Vision {
		return false
	}
	switch cp.ModelType {
	case "glm5_next", "qwen4_exp", "gemma4", "gemma4_unified", "step5_vl":
		return true
	case "qwen3_5":
		return cp.Dense27
	}
	return false
}

// drafterOK: the model takes an external drafter (--drafter): only
// Qwen3.8-27B-class and Gemma 4 dense checkpoints ("--drafter is supported
// for Qwen3.8-27B-class checkpoints … and Gemma 4 dense", metal.zig open).
func (cp checkpoint) drafterOK() bool {
	switch cp.ModelType {
	case "qwen3_5":
		return cp.Dense27
	case "gemma4", "gemma4_unified":
		return true
	}
	return false
}
