package panel

import (
	"testing"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
)

// orcaFeatures is OrcaCyber-Nano-1.0 as Splash lists it: its F16 vision
// projector and the DFlash2 draft Splash always runs, with the shape of its
// KV cache.
func orcaFeatures(v core.View) core.View {
	md := v.Model
	md.SizeBytes, md.ContextLength, md.KVElements = 15_695_148_832, 262144, 32768
	md.Features = engine.Features{Vision: true, VisionBytes: 927_606_848, Drafts: []engine.Draft{{
		Kind: engine.DraftDFlash, Name: "incoai/Qwen3.8-27B-DFlash2", ID: "incoai/Qwen3.8-27B-DFlash2",
		SizeBytes: 1_266_000_000, Always: true}}}
	v.Model, v.Models = md, []engine.Model{md}
	return v
}

// llamaFeatures is the same file under llama.cpp, which offers its MTP
// layers and the DFlash draft beside it.
func llamaFeatures() core.View {
	v := llamaView()
	md := engine.Model{ID: "/hub/models--orcarouter--OrcaCyber-Nano-1.0/snapshots/c/OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf",
		Name: "OrcaCyber-Nano-1.0", Owner: "orcarouter", Variant: "Mix-15.7G", SizeBytes: 15_695_148_832,
		ContextLength: 262144, KVElements: 32768, Features: engine.Features{Vision: true, VisionBytes: 927_606_848,
			Drafts: []engine.Draft{
				{Kind: engine.DraftMTP, Name: "MTP", ID: "mtp"},
				{Kind: engine.DraftDFlash, Name: "OrcaCyber-Nano-1.0-AI-Defense-DFlash2-Q8_0",
					ID:        "/hub/models--orcarouter--OrcaCyber-Nano-1.0/snapshots/c/OrcaCyber-Nano-1.0-AI-Defense-DFlash2-Q8_0.gguf",
					SizeBytes: 2_056_414_112},
			}}}
	plain := v.Model
	v.Model, v.Models = md, []engine.Model{md, plain}
	v.Snapshot.Model = md.ID
	return v
}

func featureBoards() []board {
	return []board{
		// The model list: the selected row and "Add model…" under it, hovered,
		// do not touch; the eye and the bolt mark what the model can do.
		{name: "model-popup-hover-add", build: func(b *bench) {
			b.show(orcaFeatures(withPhase(core.PhaseStopped)), 0)
			b.click("OrcaCyber-Nano-1.0")
			b.tt.Frame()
			r, _ := b.tt.Find("Add model…")
			b.tt.Move(r.X+r.W/2, r.Y+r.H/2)
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("Mix-15.7G · 15.7 GB", "Image input: on", "Speculative decoding: on",
				"Models from Hugging Face. A download does not stop the server; switching models restarts it")
			row, _ := b.tt.Find("Mix-15.7G · 15.7 GB")
			add, ok := b.tt.Find("Add model…")
			// The row is 40 high around its two lines (5.5 below the
			// second); 2 + 2 more keep "Add model…" off it.
			if !ok || row.H == 0 || add.Y < row.Y+row.H+7 {
				b.t.Errorf("rows touch: model %+v, add %+v", row, add)
			}
		}},
		// Splash: vision on by default, the draft always on; the estimate
		// counts both and the 256K KV cache.
		{name: "settings-features-splash", build: func(b *bench) {
			b.show(orcaFeatures(withPhase(core.PhaseReady)), 0)
			b.click("Settings")
		}, check: func(b *bench) {
			b.want("Image input", "Speculative decoding", "Needs about 28.8 GB of memory")
			b.click("Image input")
			if o := b.lastPrefs().Options[b.p.view.Model.ID]; !o.NoVision {
				b.t.Errorf("vision off not stored: %+v", o)
			}
			b.tt.Frame()
			b.want("Needs about 27.9 GB of memory", "Restart")
			b.click("Speculative decoding") // disabled: nothing changes
			if o := b.lastPrefs().Options[b.p.view.Model.ID]; o.Draft != "" {
				b.t.Errorf("draft changed: %+v", o)
			}
		}},
		// llama.cpp: MTP or the DFlash draft, chosen in a select.
		{name: "settings-features-llamacpp", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(llamaFeatures(), 0)
			b.click("Settings")
		}, check: func(b *bench) {
			// f16 KV of 256K: 17.2 GB on top of the weights.
			b.want("Image input", "Speculative decoding", "Off", "Needs about 34.8 GB of memory")
			b.click("Off")
			b.click("MTP")
			b.tt.Frame()
			if o := b.lastPrefs().Options[b.p.view.Model.ID]; o.Draft != "mtp" {
				b.t.Errorf("draft %+v", o)
			}
		}},
		// The eye and the bolt of a model row toggle the model's image input
		// and draft, as Settings does, without picking the row; Splash's
		// draft always runs and only shows.
		{name: "model-popup-toggles", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := llamaFeatures()
			v.Phase = core.PhaseStopped
			b.show(v, 0)
			b.click("OrcaCyber-Nano-1.0")
			b.click("Image input: on")
			b.tt.Frame()
		}, check: func(b *bench) {
			id := b.p.view.Model.ID
			if o := b.lastPrefs().Options[id]; !o.NoVision {
				b.t.Errorf("vision off not stored: %+v", o)
			}
			b.want("Image input: off", "Speculative decoding: off")
			b.click("Speculative decoding: off")
			b.tt.Frame()
			if o := b.lastPrefs().Options[id]; o.Draft != "mtp" {
				b.t.Errorf("draft %+v", o)
			}
			b.want("Speculative decoding: on")
			if !b.p.modelsOpen {
				b.t.Error("a toggle closed the model list")
			}
			b.notCalled("SelectModel " + id)
			for _, c := range b.rec.calls {
				if len(c) > 12 && c[:12] == "SelectModel " {
					b.t.Errorf("a toggle picked a model: %s", c)
				}
			}
		}},
		{name: "model-popup-toggles-splash", build: func(b *bench) {
			b.show(orcaFeatures(withPhase(core.PhaseStopped)), 0)
			b.click("OrcaCyber-Nano-1.0")
		}, check: func(b *bench) {
			n := len(b.rec.prefs)
			b.click("Speculative decoding: on") // always on: nothing changes
			b.tt.Frame()
			if len(b.rec.prefs) != n {
				b.t.Errorf("the fixed draft stored %+v", b.lastPrefs())
			}
			b.notCalled("SelectModel " + b.p.view.Model.ID)
		}},
		// The clear button of "Add model…".
		{name: "add-model-clear", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("unsloth/qwen")
		}, check: func(b *bench) {
			b.want("Clear")
			b.click("Clear")
			b.tt.Frame()
			if b.text() != "" {
				b.t.Errorf("text %q", b.text())
			}
			b.none("Clear")
		}},
	}
}

func TestFeatureRowsHiddenWithoutFeatures(t *testing.T) {
	b := newBench(t, "en")
	b.show(withPhase(core.PhaseReady), 0)
	b.click("Settings")
	b.none("Image input", "Speculative decoding")
}
