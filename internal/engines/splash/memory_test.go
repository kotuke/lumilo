package splash

import (
	"encoding/json"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

// Text only is --language-only; Draft has no flag (Splash always runs its
// DFlash2 draft).
func TestServeOptions(t *testing.T) {
	e := New()
	on, _ := e.ServeCommand(engine.Config{Model: "o/r:Q4_K_M", Port: 1, Draft: engine.DraftOff})
	off, _ := e.ServeCommand(engine.Config{Model: "o/r:Q4_K_M", Port: 1, NoVision: true})
	if slices.Contains(on, "--language-only") || !slices.Contains(off, "--language-only") {
		t.Errorf("vision on %q, off %q", on, off)
	}
	for _, a := range on {
		if strings.Contains(a, "draft") {
			t.Errorf("draft flag in %q", on)
		}
	}
}

// TestEstimateOrcaCyber holds engine.Estimate with Splash's numbers to what
// Splash itself planned for OrcaCyber-Nano-1.0:Mix-15.7G at 256K
// (testdata/lifecycle/after_gen1.status.txt, memory_plan).
func TestEstimateOrcaCyber(t *testing.T) {
	data, err := os.ReadFile("testdata/lifecycle/after_gen1.status.txt")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	body = body[strings.Index(body, "{"):]
	var st struct {
		Plan struct {
			Model struct {
				Layers   int   `json:"attention_layers"`
				KVHeads  int   `json:"kv_heads"`
				HeadDim  int   `json:"head_dimension"`
				MaxCtx   int   `json:"maximum_context_tokens"`
				PageTok  int64 `json:"kv_page_tokens"`
				PageByte int64 `json:"kv_page_bytes"`
			} `json:"model"`
			Budget struct {
				Fixed   int64 `json:"fixed_runtime_bytes"`
				Target  int64 `json:"target_weights_bytes"`
				Draft   int64 `json:"draft_weights_bytes"`
				Vision  int64 `json:"vision_weights_bytes"`
				Minimum int64 `json:"minimum_required_bytes"`
				Hard    int64 `json:"hard_budget_bytes"`
			} `json:"budget"`
		} `json:"memory_plan"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	pm, pb := st.Plan.Model, st.Plan.Budget

	// The model as Models lists it: the GGUF, the F16 mmproj and the BF16
	// draft as they are on disk, and the header's shape (gguf_test.go).
	m := engine.Model{
		SizeBytes:     15_695_148_832,
		ContextLength: 262144,
		KVElements:    16 * 4 * 2 * 256,
		Features: engine.Features{Vision: true, VisionBytes: 927_606_848, Drafts: []engine.Draft{{
			Kind: engine.DraftDFlash, Name: "incoai/Qwen3.8-27B-DFlash2", ID: "incoai/Qwen3.8-27B-DFlash2",
			SizeBytes: draftMemory(3_848_817_896), Always: true}}},
	}
	mc := New().Info().Caps.Memory

	// The KV cache of one token is Splash's page over its tokens: 33,280.
	if int64(pm.Layers*pm.KVHeads*2*pm.HeadDim) != m.KVElements {
		t.Fatalf("shape %+v", pm)
	}
	perToken := float64(m.KVElements) * mc.KVBytesPerElement
	if want := float64(pm.PageByte) / float64(pm.PageTok); perToken != want {
		t.Errorf("KV per token %v, Splash %v", perToken, want)
	}

	near := func(name string, got, want int64, tol float64) {
		t.Helper()
		if d := math.Abs(float64(got-want)) / float64(want); d > tol {
			t.Errorf("%s: %.2f GB, Splash %.2f GB (%.1f%% off)", name, float64(got)/1e9, float64(want)/1e9, d*100)
		}
	}
	// Weights and draft as Splash holds them.
	need := engine.Estimate(m, engine.Config{}, mc)
	near("draft", need.Draft, pb.Draft, 0.01)
	near("weights", need.Weights+need.Vision+need.Draft, pb.Target+pb.Draft+pb.Vision, 0.01)
	// Without the KV cache: Splash's fixed runtime, its start floor
	// (minimum_required_bytes 20.49e9) less the first KV extent.
	noKV := m
	noKV.KVElements = 0
	near("fixed", engine.Estimate(noKV, engine.Config{}, mc).Total(), pb.Fixed, 0.02)
	// The whole 256K context resident: the fixed runtime and 256K × 33,280.
	full := pb.Fixed + int64(pm.MaxCtx)*pm.PageByte/pm.PageTok
	near("256K", need.Total(), full, 0.02)
	if need.Context != 262144 || need.Total() < pb.Minimum {
		t.Errorf("need %+v below Splash's floor %d", need, pb.Minimum)
	}

	// It fits the 39.1 GB hard budget of the 48 GB Mac; at 1M tokens it
	// does not, and with Auto Splash shortens the context instead.
	if _, fit := engine.Judge(m, engine.Config{}, mc, pb.Hard); fit != engine.Fits {
		t.Errorf("256K: %v", fit)
	}
	if _, fit := engine.Judge(m, engine.Config{ContextTokens: 1_010_000}, mc, pb.Hard); fit != engine.FitsNot {
		t.Errorf("1M: %v", fit)
	}
	if _, fit := engine.Judge(m, engine.Config{}, mc, 24e9); fit != engine.FitsShorter {
		t.Errorf("auto on 24 GB: %v", fit)
	}
	// Text only drops the projector; the draft cannot be turned off.
	if n := engine.Estimate(m, engine.Config{NoVision: true, Draft: engine.DraftOff}, mc); n.Vision != 0 || n.Draft == 0 {
		t.Errorf("no vision: %+v", n)
	}
}
