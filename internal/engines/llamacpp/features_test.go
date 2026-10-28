package llamacpp

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

// A model with MTP layers, its projector and a DFlash draft named after it
// (as orcarouter/OrcaCyber-Nano-1.0 publishes them): the features Models
// reports, and the flags each choice gives.
func TestFeaturesAndOptions(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "Orca-Mix-15.7G.gguf")
	write(t, model, ggufFile([]kv{
		{"general.architecture", "qwen35"},
		{"general.name", "Orca"},
		{"qwen35.block_count", uint32(65)},
		{"qwen35.context_length", uint32(262144)},
		{"qwen35.attention.head_count_kv", uint32(4)},
		{"qwen35.attention.key_length", uint32(256)},
		{"qwen35.nextn_predict_layers", uint32(1)},
		{"qwen35.full_attention_interval", uint32(4)},
		{"tokenizer.ggml.model", "gpt2"},
	}, 10), 0o644)
	mmproj := filepath.Join(dir, "mmproj-Orca-F16.gguf")
	draft := filepath.Join(dir, "Orca-DFlash2-Q8_0.gguf")
	write(t, mmproj, []byte("projector"), 0o644)
	write(t, draft, []byte("draft"), 0o644)

	ms := ggufModels(dir)
	if len(ms) != 1 {
		t.Fatalf("models %+v", ms)
	}
	m := ms[0]
	f := m.Features
	if !f.Vision || f.VisionBytes != 9 || len(f.Drafts) != 2 || m.KVElements != 16*4*512 {
		t.Fatalf("model %+v", m)
	}
	if d := f.Drafts[0]; d.Kind != engine.DraftMTP || d.ID != "mtp" || d.Always {
		t.Errorf("mtp %+v", d)
	}
	if d := f.Drafts[1]; d.Kind != engine.DraftDFlash || d.ID != draft || d.SizeBytes != 5 {
		t.Errorf("dflash %+v", d)
	}

	e := New()
	args := func(cfg engine.Config) string {
		cfg.Port = 8123
		argv, _ := e.ServeCommand(cfg)
		return strings.Join(argv, " ")
	}
	for _, c := range []struct {
		cfg      engine.Config
		has, not []string
	}{
		{engine.Config{Model: model}, []string{"--mmproj " + mmproj}, []string{"--spec-type", "--no-mmproj"}},
		{engine.Config{Model: model, NoVision: true}, nil, []string{"--mmproj", "--no-mmproj"}},
		{engine.Config{Model: "o/r-GGUF:Q4_K_M", NoVision: true}, []string{"-hf o/r-GGUF:Q4_K_M", "--no-mmproj"}, nil},
		{engine.Config{Model: model, Draft: "mtp"}, []string{"--spec-type draft-mtp"}, []string{"-md"}},
		{engine.Config{Model: model, Draft: draft}, []string{"-md " + draft + " --spec-type draft-dflash"}, nil},
		{engine.Config{Model: model, Draft: engine.DraftOff}, nil, []string{"--spec-type"}},
		// A path that is not the draft beside the model is not passed.
		{engine.Config{Model: model, Draft: "/elsewhere/x.gguf"}, nil, []string{"-md", "--spec-type"}},
	} {
		got := args(c.cfg)
		for _, s := range c.has {
			if !strings.Contains(got, s) {
				t.Errorf("%+v: no %q in %s", c.cfg, s, got)
			}
		}
		for _, s := range c.not {
			if slices.Contains(strings.Fields(got), s) {
				t.Errorf("%+v: %q in %s", c.cfg, s, got)
			}
		}
	}
}
