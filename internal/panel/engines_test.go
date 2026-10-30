package panel

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/engines/splash"
	"github.com/kotuke/lumilo/internal/hf"
)

// noModels is a stopped engine without a model.
func noModels(v core.View) core.View {
	v.Phase = core.PhaseStopped
	v.Model, v.Models = engine.Model{}, nil
	v.Snapshot, v.IdleLeft, v.ChatURL = engine.Snapshot{}, 0, ""
	return v
}

func (b *bench) lastPrefs() Prefs {
	b.t.Helper()
	if len(b.rec.prefs) == 0 {
		b.t.Fatal("no prefs stored")
	}
	return b.rec.prefs[len(b.rec.prefs)-1]
}

// engineBoards are the boards of the engine choice, the context size, the
// suggested models and the model download.
func engineBoards() []board {
	nativeModel := sampleModel
	nativeModel.ContextLength = 262144
	withNative := func() core.View {
		v := withPhase(core.PhaseReady)
		v.Model, v.Models = nativeModel, []engine.Model{nativeModel}
		return v
	}
	return []board{
		// llama.cpp: its logo and name, no Unload (no release on request),
		// the agents Lumilo points at it.
		{name: "llamacpp-ready", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(llamaView(), time.Hour)
		}, check: func(b *bench) {
			b.want("llama.cpp 0.7.0", "Qwen3-0.6B-GGUF", "ggml-org · Q8_0", "Memory", "Stop",
				"Launch agent", "Claude", "Codex", "OpenCode", "Pi")
			for _, s := range b.tt.Texts() {
				if s == "Unload" { // "Unloads after … idle" stays: that is --sleep-idle-seconds
					b.t.Error("an Unload button")
				}
			}
		}},
		{name: "llamacpp-settings-network", build: func(b *bench) {
			pr := defaultPrefs()
			pr.Access, pr.APIKey = engine.AccessNetwork, "sk-lumilo-0000000000003f9a"
			b.p.SetPrefs(pr)
			b.show(llamaView(), time.Hour)
			b.click("Settings")
		}, check: func(b *bench) {
			// llama-server checks no Host: no allowed names.
			b.want("Engine", "Splash", "llama.cpp", "0.7.0", "API key", "Context", "Auto",
				"The model’s native context is 40K")
			b.none("Allowed host names")
		}},
		// The engine list; a switch while the server runs asks first.
		{name: "settings-engines", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click("Settings")
			b.click("llama.cpp")
		}, check: func(b *bench) {
			b.want("Engine", "Splash", "1.4.0", "0.7.0",
				"Switching to llama.cpp stops the server. Requests in progress are cut off", "Switch")
			b.notCalled("SwitchEngine llamacpp")
			b.click("Switch")
			b.called("SwitchEngine llamacpp")
			b.settle()
			if b.p.screen != screenMain {
				b.t.Error("still in Settings after the switch")
			}
		}},
		{name: "settings-engine-stopped", build: func(b *bench) {
			b.show(withPhase(core.PhaseStopped), 0)
			b.click("Settings")
		}, check: func(b *bench) {
			b.click("llama.cpp")
			b.called("SwitchEngine llamacpp")
			b.none("Switch")
		}},
		// The context select: Auto, the sizes up to the native window, Other.
		{name: "settings-context", build: func(b *bench) {
			b.show(withNative(), time.Hour)
			b.click("Settings")
			b.click("Auto")
		}, check: func(b *bench) {
			b.want("32K", "64K", "128K", "256K", "Other…")
			b.click("64K")
			b.tt.Frame()
			if got := b.lastPrefs().Contexts[nativeModel.ID]; got != 65536 {
				b.t.Errorf("context %d", got)
			}
			b.want("Changes apply after the server restarts")
		}},
		// Other…: any size in K; past the native window, a warning.
		{name: "settings-context-other", build: func(b *bench) {
			b.show(withNative(), time.Hour)
			b.click("Settings")
			b.click("Auto")
			b.click("Other…")
			b.tt.Frame()
			b.tt.Type("512")
			b.tt.Key(0, ui.KeyEnter)
			b.tt.Frame()
		}, check: func(b *bench) {
			if got := b.lastPrefs().Contexts[nativeModel.ID]; got != 512*1024 {
				b.t.Errorf("context %d", got)
			}
			b.want("512K",
				"Past the model’s native context of 256K, answers can get worse at every length")
		}},
		{name: "settings-context-bad", build: func(b *bench) {
			b.show(withNative(), time.Hour)
			b.click("Settings")
			b.click("Auto")
			b.click("Other…")
			b.tt.Frame()
			b.tt.Type("lots")
			b.tt.Key(0, ui.KeyEnter)
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("Size in K tokens", "Enter a whole number of K tokens (K = 1024)")
		}},
		// No model: Splash's suggestions in place of the model and Start.
		{name: "suggestions", build: func(b *bench) {
			b.show(noModels(withPhase(core.PhaseStopped)), 0)
		}, check: func(b *bench) {
			b.want("No models yet", "Qwen3.8-27B", "27B parameters · 21.2 GB", "Qwen3.6-35B-A3B",
				"36B, 3B active · 24.1 GB", "Add model…")
			b.none("Start", "Launch agent", "Ling 3.0 Flash")
			b.click("Download Qwen3.8-27B")
			b.called("Download unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M")
		}},
		// llama.cpp's two Qwen models.
		{name: "suggestions-llamacpp", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(noModels(llamaView()), 0)
		}, check: func(b *bench) {
			b.want("Qwen3.8-27B", "27B parameters · 17.4 GB", "Qwen3.6-35B-A3B")
			b.none("Ling 3.0 Flash")
			b.click("Download Qwen3.6-35B-A3B")
			b.called("Download unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M")
		}},
		// Add model: a name the engine refuses, then one it takes.
		{name: "add-model", build: func(b *bench) {
			b.show(withPhase(core.PhaseStopped), 0)
			b.click("OrcaCyber-Nano-1.0")
			b.click("Add model…")
			b.tt.Frame()
			b.tt.Type("not a model")
			b.tt.Key(0, ui.KeyEnter)
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("Enter owner/repo or owner/repo:variant")
			b.notCalled("Download not a model")
			b.tt.Key(ui.Cmd, ui.KeyA)
			b.tt.Type("unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M")
			b.tt.Key(0, ui.KeyEnter)
			b.tt.Frame()
			b.called("Download unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M")
		}},
		{name: "add-model-open", build: func(b *bench) {
			b.show(noModels(withPhase(core.PhaseStopped)), 0)
			b.click("Add model…")
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("Type a model's name to search Hugging Face, or paste owner/repo:variant")
		}},
		// Add model: the repositories that match what is typed, for Splash
		// GGUF and MLX alike, one highlighted.
		{name: "add-model-search", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("qwen3.8")
			b.searched("qwen3.8", sampleHits)
			b.tt.Key(0, ui.KeyDown)
			b.tt.Key(0, ui.KeyDown)
		}, check: func(b *bench) {
			b.want("unsloth/Qwen3.8-27B-GGUF", "6,009,562 downloads", "MLX · 3,586,440 downloads")
		}},
		// The same under the suggestions, with llama.cpp: GGUF only.
		{name: "add-model-search-suggestions", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(noModels(llamaView()), 0)
			b.click("Add model…")
			b.tt.Frame()
			b.typeText("qwen3.8")
			b.searched("qwen3.8", sampleHits[:2])
		}, check: func(b *bench) {
			b.want("unsloth/Qwen3.8-27B-GGUF", "ggml-org/Qwen3.8-27B-GGUF")
			b.none("MLX")
		}},
		// A chosen repository lists its variants, 4-bit first, with sizes and
		// the memory warning of the suggestions.
		{name: "add-model-variants", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("unsloth/Qwen3.8-27B-GGUF:")
			b.p.SetModelVariants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
			b.tt.Frame()
			b.tt.Key(0, ui.KeyDown)
		}, check: func(b *bench) {
			b.want("UD-Q4_K_M", "16.5 GB", "Needs about 58.2 GB of memory, more than the 38.7 GB available")
		}},
		// More variants than fit scroll; what follows the colon narrows them.
		{name: "add-model-variants-many", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(noModels(llamaView()), 0)
			b.click("Add model…")
			b.tt.Frame()
			b.typeText("unsloth/Qwen3.8-27B-GGUF:")
			b.p.SetModelVariants("unsloth/Qwen3.8-27B-GGUF", manyVariants)
			b.tt.Frame()
			for range 7 {
				b.tt.Key(0, ui.KeyDown)
			}
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("UD-Q4_K_M", "UD-Q6_K")
		}},
		// Waiting for the variants.
		{name: "add-model-variants-loading", build: func(b *bench) {
			b.openAdd(false)
			b.typeText("unsloth/Qwen3.8-27B-GGUF:")
		}, check: func(b *bench) {
			b.want("Looking up variants…")
		}},
		// A download run: the model being fetched, its size when known.
		{name: "downloading", build: func(b *bench) {
			v := noModels(withPhase(core.PhaseLoading))
			v.Phase = core.PhaseLoading
			m, _ := splash.New().ParseModel("unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M")
			v.Model, v.Download, v.Downloading, v.DownloadTotal = m, m.ID, true, 21_240_000_000
			b.show(v, 40*time.Second)
		}, check: func(b *bench) {
			b.want("Downloading", "Qwen3.8-27B-GGUF", "unsloth · UD-Q4_K_M", "Downloading the model, 21.2 GB…", "Cancel")
			b.none("No models yet")
		}},
	}
}

// manyVariants are the variants of unsloth/Qwen3.8-27B-GGUF as hf lists them.
var manyVariants = func() []hf.Variant {
	var vs []hf.Variant
	for _, f := range []struct {
		tag  string
		size int64
	}{
		{"UD-Q4_K_M", 16_464_440_224}, {"UD-IQ4_XS", 14_252_845_984}, {"UD-Q4_K_S", 15_358_213_024},
		{"Q4_0", 16_056_478_688}, {"Q4_1", 17_540_705_248}, {"UD-Q4_K_XL", 17_559_178_144},
		{"UD-IQ1_S", 6_192_222_208}, {"UD-IQ1_M", 6_729_166_848}, {"UD-IQ2_XXS", 7_266_070_528},
		{"UD-Q5_K_M", 19_771_509_664}, {"UD-Q6_K", 21_983_677_344}, {"Q8_0", 29_047_086_048},
		{"BF16", 54_657_735_616},
	} {
		vs = append(vs, hf.Variant{Tag: f.tag, Bytes: f.size, Parts: 1})
	}
	return vs
}()
