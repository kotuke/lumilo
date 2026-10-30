package panel

import (
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/engines/tensorfold"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/internal/hf"
	"github.com/kotuke/lumilo/locales"
)

// The README's models: the two Qwen models Lumilo suggests, at the sizes of
// unsloth's UD-Q4_K_M files on Hugging Face (2026-10-28).
var (
	qwen38Llama = engine.Model{ID: "/hub/models--unsloth--Qwen3.8-27B-GGUF/snapshots/c/Qwen3.8-27B-UD-Q4_K_M.gguf",
		Name: "Qwen3.8-27B-GGUF", Owner: "unsloth", Variant: "UD-Q4_K_M", SizeBytes: 16_464_440_224,
		ContextLength: 262144}
	qwen36Llama = engine.Model{ID: "/hub/models--unsloth--Qwen3.6-35B-A3B-GGUF/snapshots/c/Qwen3.6-35B-A3B-UD-Q4_K_M.gguf",
		Name: "Qwen3.6-35B-A3B-GGUF", Owner: "unsloth", Variant: "UD-Q4_K_M", SizeBytes: 22_134_528_992,
		ContextLength: 262144}
	qwen38Splash = engine.Model{ID: "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", Name: "Qwen3.8-27B-GGUF",
		Owner: "unsloth", Variant: "UD-Q4_K_M", SizeBytes: 16_464_440_224, ContextLength: 262144,
		Features: engine.Features{Vision: true, VisionBytes: 927_606_848, Drafts: []engine.Draft{{
			Kind: engine.DraftDFlash, Name: "incoai/Qwen3.8-27B-DFlash2", ID: "incoai/Qwen3.8-27B-DFlash2",
			SizeBytes: 1_266_000_000, Always: true}}}}
	qwen36Splash = engine.Model{ID: "unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M", Name: "Qwen3.6-35B-A3B-GGUF",
		Owner: "unsloth", Variant: "UD-Q4_K_M", SizeBytes: 22_134_528_992, ContextLength: 262144}
	// Under TensorFold: MLX checkpoints, Qwen3.8-27B with its DFlash2 drafter
	// (sizes from Hugging Face, 2026-10-29).
	qwen38TF = engine.Model{ID: "TensorFold/Qwen3.8-27B-MLX-4bit", Name: "Qwen3.8-27B-MLX-4bit", Owner: "TensorFold",
		SizeBytes: 16_054_541_349, ContextLength: 262144, KVElements: 32768, Features: engine.Features{Vision: true,
			Drafts: []engine.Draft{{Kind: engine.DraftDFlash, Name: "z-lab/Qwen3.8-27B-DFlash2", ID: "z-lab/Qwen3.8-27B-DFlash2",
				SizeBytes: 1_731_967_053, Default: true}}}}
	qwen36TF = engine.Model{ID: "mlx-community/Qwen3.6-35B-A3B-4bit", Name: "Qwen3.6-35B-A3B-4bit",
		Owner: "mlx-community", SizeBytes: 19_772_000_000, ContextLength: 262144}
	// qwen36Download is Qwen3.6-35B-A3B as typed, before its files are on disk.
	qwen36Download = engine.Model{ID: "unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M", Owner: "unsloth",
		Name: "Qwen3.6-35B-A3B-GGUF", Variant: "UD-Q4_K_M"}
)

// qwen36Variants are some of unsloth/Qwen3.6-35B-A3B-GGUF's files.
var qwen36Variants = []hf.Variant{
	{Tag: "UD-Q4_K_M", Bytes: 22_134_528_992, Parts: 1},
	{Tag: "UD-IQ4_XS", Bytes: 17_730_509_792, Parts: 1},
	{Tag: "MXFP4_MOE", Bytes: 21_706_144_736, Parts: 1},
	{Tag: "UD-Q6_K", Bytes: 29_308_320_736, Parts: 1},
	{Tag: "Q8_0", Bytes: 36_903_140_320, Parts: 1},
}

// readmeEngines are the three engines, installed.
func readmeEngines() []core.EngineInfo {
	e := tensorfold.New()
	info := e.Info()
	return append(engineList(), core.EngineInfo{ID: info.ID, Name: info.Name, Logo: info.Logo,
		Installed: true, Version: "1.1.9", Install: e.Installer().Describe()})
}

// readmeTensorFold is TensorFold serving Qwen3.8-27B with its DFlash2
// drafter, at the 128K that fits on 48 GB, enough for Claude Code: the
// drafter makes it faster than llama.cpp's GGUF.
func readmeTensorFold(ph core.Phase) core.View {
	v := withPhase(ph)
	core.Describe(&v, tensorfold.New())
	v.Version, v.ChatURL = "1.1.9", ""
	v.Engines = readmeEngines()
	v.Model, v.Models = qwen38TF, []engine.Model{qwen38TF, qwen36TF}
	v.Snapshot.Model, v.Snapshot.ContextTokens = qwen38TF.ID, 131072
	v.Snapshot.MemoryUsed, v.Snapshot.DiskUsed, v.Snapshot.DiskLimit = 22_400_000_000, 0, 0
	v.Speed.History = nil
	for _, s := range sampleHistory {
		v.Speed.History = append(v.Speed.History, s*1.15)
	}
	v.Speed.Average, v.Speed.FirstToken, v.Speed.Read = 44, 900*time.Millisecond, 880
	if ph == core.PhaseGenerating {
		v.Speed.Current = 47
	}
	return v
}

// readmeLlama is llama.cpp serving Qwen3.8-27B: a dense 27B model runs
// slower than the mockup's sample, and llama.cpp reports no budget nor
// conversations on disk.
func readmeLlama(ph core.Phase) core.View {
	v := llamaView()
	v.Phase = ph
	v.Model, v.Models = qwen38Llama, []engine.Model{qwen38Llama, qwen36Llama}
	v.Snapshot.Model, v.Snapshot.MemoryUsed = qwen38Llama.ID, 20_100_000_000
	v.Speed.History = nil
	for _, s := range sampleHistory {
		v.Speed.History = append(v.Speed.History, s*0.68)
	}
	v.Speed.Average, v.Speed.FirstToken, v.Speed.Read = 26, 1400*time.Millisecond, 610
	v.Engines = readmeEngines()
	if ph == core.PhaseGenerating {
		// llama-server counts a request's tokens when it ends: no live speed.
		v.Snapshot.MemoryUsed, v.Snapshot.Active = 20_600_000_000, 1
	}
	return v
}

// readmeSplash is Splash serving Qwen3.8-27B with its DFlash2 draft.
func readmeSplash(ph core.Phase) core.View {
	v := withPhase(ph)
	v.Model, v.Models = qwen38Splash, []engine.Model{qwen38Splash, qwen36Splash}
	v.Snapshot.Model = qwen38Splash.ID
	v.Engines = readmeEngines()
	if ph == core.PhaseGenerating || ph == core.PhaseReady {
		v.Snapshot.MemoryUsed = 21_200_000_000
	}
	return v
}

// readmeBoards are the README's pictures: both engines, the Qwen models.
// The other boards keep their fixtures; these only show.
func readmeBoards() []board {
	en := locales.In("en")
	out := []board{
		// llama.cpp serves Qwen3.8-27B while Qwen3.6-35B-A3B downloads.
		{name: "readme-llama-downloading", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := withJob(readmeLlama(core.PhaseReady), qwen36Download, core.DownloadRunning, 8_400_000_000, 22_134_528_992)
			v.Models = v.Models[:1]
			// Served for 25 minutes before the download started.
			served := v
			served.Downloads = nil
			b.show(served, 25*time.Minute)
			b.show(v, 0)
			b.click("Qwen3.8-27B-GGUF")
			b.clock = b.clock.Add(10 * time.Second)
			v.Downloads[0].Done = 9_000_000_000
			b.p.SetView(v)
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("llama.cpp 0.7.0", "Qwen3.6-35B-A3B", "9 of 22.1 GB · 60 MB/s · "+en.DlLeft("4 min"))
		}},
		// TensorFold generates with Qwen3.8-27B and its DFlash2 drafter.
		{name: "readme-tensorfold-generating", build: func(b *bench) {
			b.rec.eng = tensorfold.New()
			b.show(readmeTensorFold(core.PhaseGenerating), 2*time.Hour+40*time.Minute)
		}, check: func(b *bench) {
			b.want("TensorFold 1.1.9", "Qwen3.8-27B-MLX-4bit", "Generating", "22.4 GB", "128K")
		}},
		// TensorFold's model list: image input and the DFlash2 drafter.
		{name: "readme-tensorfold-models", build: func(b *bench) {
			b.rec.eng = tensorfold.New()
			b.show(readmeTensorFold(core.PhaseReady), 40*time.Minute)
			b.click("Qwen3.8-27B-MLX-4bit")
		}, check: func(b *bench) {
			b.want("TensorFold 1.1.9", "Qwen3.6-35B-A3B-4bit", "Speculative decoding: on")
		}},
		// Splash with Qwen3.8-27B looks up Qwen3.6-35B-A3B's files.
		{name: "readme-splash-variants", build: func(b *bench) {
			b.show(readmeSplash(core.PhaseStopped), 0)
			b.click("Qwen3.8-27B-GGUF")
			b.click(b.m.AddModel())
			b.tt.Frame()
			b.typeText("unsloth/Qwen3.6-35B-A3B-GGUF:")
			b.p.SetModelVariants("unsloth/Qwen3.6-35B-A3B-GGUF", qwen36Variants)
			b.tt.Frame()
			b.tt.Key(0, ui.KeyDown)
		}, check: func(b *bench) {
			b.want("Splash 1.4.0", "UD-Q4_K_M", "22.1 GB", "Q8_0")
		}},
		// The header switches the engine.
		{name: "readme-engines", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(readmeLlama(core.PhaseReady), time.Hour)
			b.openEngines("llama.cpp 0.7.0")
		}, check: func(b *bench) {
			b.want("Splash", "llama.cpp", "TensorFold", "1.4.0", "0.7.0", "1.1.9")
		}},
		// llama.cpp open to the network, with its key and context.
		{name: "readme-llama-network", build: func(b *bench) {
			pr := defaultPrefs()
			pr.Access, pr.APIKey = engine.AccessNetwork, "sk-lumilo-0000000000003f9a"
			b.p.SetPrefs(pr)
			b.rec.eng = llamacpp.New()
			v := readmeLlama(core.PhaseReady)
			v.Access, v.Address = engine.AccessNetwork, "192.168.1.20:8000"
			b.show(v, time.Hour)
			b.click("Settings")
		}, check: func(b *bench) {
			b.want("Engine", "llama.cpp", "API key", "The model’s native context is 256K")
		}},
		// Delete Qwen3.6-35B-A3B from Splash: the dialog says what it frees.
		{name: "readme-splash-remove", build: func(b *bench) {
			b.show(readmeSplash(core.PhaseReady), time.Hour)
			b.click("Qwen3.8-27B-GGUF")
			b.click(en.RmLabel(qwen36Splash.Name))
			v := readmeSplash(core.PhaseReady)
			v.Removal = core.Removal{Model: qwen36Splash.ID, Counted: true,
				Plan: engine.RemovePlan{Bytes: 23_037_000_000, Shared: true}}
			b.show(v, 0)
		}, check: func(b *bench) {
			b.called("PlanRemove " + qwen36Splash.ID)
			b.want(en.RmTitle(qwen36Splash.Name), en.RmFrees("23 GB"))
		}},
	}
	// The languages, by engine: Splash, llama.cpp, then TensorFold.
	for i, lang := range []string{"en", "ru", "de", "fr", "es", "ja", "zh-Hans", "ar"} {
		view, eng := readmeSplash, engine.Engine(nil)
		switch {
		case i >= 6:
			view, eng = readmeTensorFold, tensorfold.New()
		case i >= 3:
			view, eng = readmeLlama, llamacpp.New()
		}
		out = append(out, board{name: "readme-lang-" + lang, lang: lang,
			build: func(b *bench) {
				if eng != nil {
					b.rec.eng = eng
				}
				b.show(view(core.PhaseGenerating), 2*time.Hour+14*time.Minute)
			},
			check: func(b *bench) {
				b.want(b.m.PillGenerating(), b.m.SpeedTitle(), b.m.Memory(), b.m.ActStop(),
					b.m.SubGenerating(format.ShortDuration(lang, 2*time.Hour+14*time.Minute)))
				if i < 3 && !slices.Contains(b.tt.Texts(), b.m.AgentsTitle()) {
					b.t.Error("no agents under Splash")
				}
			}})
	}
	return out
}
