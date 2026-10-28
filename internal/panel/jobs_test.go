package panel

import (
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/hf"
	"github.com/kotuke/lumilo/locales"
)

// arexModel is a typed model whose repository has no vision projector.
var arexModel = engine.Model{ID: "BoldingBuilds/AREX-2-Abliterated-GGUF:Abliterated-Q4_K_M", Owner: "BoldingBuilds",
	Name: "AREX-2-Abliterated-GGUF", Variant: "Abliterated-Q4_K_M"}

// tinyModel and pausedModel are more typed models, to queue.
var (
	tinyModel   = engine.Model{ID: "acme/Tiny-1B-GGUF:Q4_K_M", Owner: "acme", Name: "Tiny-1B-GGUF", Variant: "Q4_K_M"}
	pausedModel = engine.Model{ID: "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", Owner: "unsloth", Name: "Qwen3.8-27B-GGUF",
		Variant: "UD-Q4_K_M"}
)

// withJob is v with one download job of md.
func withJob(v core.View, md engine.Model, state core.DownloadState, done, total int64) core.View {
	v.Downloads = []core.DownloadJob{{Model: md, State: state, Done: done, Total: total}}
	return v
}

// openModels opens the model list of a view with a model.
func (b *bench) openModels(v core.View) {
	b.t.Helper()
	b.show(v, 0)
	b.click("OrcaCyber-Nano-1.0")
	b.tt.Frame()
}

// jobBoards are the boards of downloads apart from the server: rows in the
// model list and among the suggestions, and the failures the downloads and
// the server explain.
func jobBoards() []board {
	en := locales.In("en")
	const total = 16_550_000_000
	running := func(b *bench) {
		// About 40 %, two views 10 s apart: 50 MB/s.
		v := withJob(withPhase(core.PhaseReady), arexModel, core.DownloadRunning, 6_120_000_000, total)
		b.openModels(v)
		b.clock = b.clock.Add(10 * time.Second)
		v.Downloads[0].Done = 6_620_000_000
		b.p.SetView(v)
		b.tt.Frame()
	}
	return []board{
		// The server keeps serving its model; the new one downloads in the
		// list with its bar, speed and time left, and cannot be picked.
		{name: "picker-downloading", build: running, check: func(b *bench) {
			b.want("AREX-2-Abliterated-GGUF", "Ready", "6.6 of 16.6 GB · 50 MB/s · "+en.DlLeft("4 min"))
			b.click("AREX-2-Abliterated-GGUF")
			b.notCalled("SelectModel " + arexModel.ID)
			b.click(en.DlCancel() + " AREX-2-Abliterated-GGUF")
			b.called("CancelDownload " + arexModel.ID)
		}},
		{name: "picker-downloading-ru", lang: "ru", build: running, check: func(b *bench) {
			b.want("6,6 из 16,6 ГБ · 50 МБ/с · " + locales.In("ru").DlLeft("4 мин"))
		}},
		// No byte yet: the engine checks the model; the bar sweeps.
		{name: "picker-download-starting", build: func(b *bench) {
			b.openModels(withJob(withPhase(core.PhaseStopped), arexModel, core.DownloadRunning, 0, 0))
		}, check: func(b *bench) { b.want(en.DlPreparing(), "Start") }},
		// A failure stays in the list with its reason, Retry and Remove.
		{name: "picker-download-failed", build: func(b *bench) {
			v := withJob(withPhase(core.PhaseReady), arexModel, core.DownloadFailed, 1_000_000_000, total)
			v.Downloads[0].Failure = engine.Failure{Kind: engine.FailExit, Code: 1, Line: "error: cannot install: connection reset"}
			b.openModels(v)
		}, check: func(b *bench) {
			b.want(en.DlFailed(), en.ActRetry(), en.DlRemove())
			b.click(en.ActRetry())
			b.called("Download " + arexModel.ID)
			b.click(en.DlRemove())
			b.called("DismissDownload " + arexModel.ID)
		}},
		// The model has no image input: Retry downloads it for text only.
		{name: "picker-download-novision", build: func(b *bench) {
			v := withJob(withPhase(core.PhaseReady), arexModel, core.DownloadFailed, 0, 0)
			v.Downloads[0].Failure = engine.Failure{Kind: engine.FailNoVision, Code: 1,
				Line: "error: the GGUF repository has no BF16, F32 or F16 vision projector (no GGUF named mmproj); use --language-only to serve text only"}
			b.openModels(v)
		}, check: func(b *bench) {
			b.want(en.DlFailNoVision(), en.DlTextOnly())
			b.click(en.DlTextOnly())
			b.called("Download " + arexModel.ID + " text-only")
		}},
		// Done: an ordinary row, with its tag; the selected model stays.
		{name: "picker-downloaded", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			md := arexModel
			md.SizeBytes = total
			v.Models = append(v.Models, md)
			v = withJob(v, arexModel, core.DownloadDone, total, total)
			v.Downloads[0].Installed = md.ID
			b.openModels(v)
		}, check: func(b *bench) {
			b.want(en.Downloaded(), "Abliterated-Q4_K_M · 16.6 GB")
			b.click("AREX-2-Abliterated-GGUF")
			b.called("SelectModel " + arexModel.ID)
		}},
		// No model yet: the suggestion being downloaded shows its progress;
		// another one queues behind it.
		{name: "suggestions-downloading", build: func(b *bench) {
			v := noModels(withPhase(core.PhaseStopped))
			sg := v.Suggestions[0]
			md, _ := b.rec.ParseModel(sg.ID)
			b.show(withJob(v, md, core.DownloadRunning, 8_480_000_000, 0), 0)
		}, check: func(b *bench) {
			b.want("Qwen3.8-27B", "8.5 of 21.2 GB", "Qwen3.6-35B-A3B")
			b.click("Download Qwen3.6-35B-A3B")
			b.called("Download unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M")
		}},
		// Several downloads: one runs, the next is queued, one Lumilo quit
		// during waits paused with what it fetched.
		{name: "picker-queued-paused", build: func(b *bench) {
			v := withJob(withPhase(core.PhaseReady), arexModel, core.DownloadRunning, 6_120_000_000, total)
			v.Downloads = append(v.Downloads,
				core.DownloadJob{Model: tinyModel, State: core.DownloadQueued},
				core.DownloadJob{Model: pausedModel, State: core.DownloadPaused, Done: 3_240_000_000, Total: 17_400_000_000})
			b.openModels(v)
		}, check: func(b *bench) {
			b.want(en.DlQueued(), en.DlPaused()+" · 3.2 of 17.4 GB", en.DlResume(), en.DlRemove())
			b.click(en.DlCancel() + " " + tinyModel.Name)
			b.called("CancelDownload " + tinyModel.ID)
			b.click(en.DlResume())
			b.called("Download " + pausedModel.ID)
			b.click(en.DlRemove())
			b.called("DismissDownload " + pausedModel.ID)
		}},
		{name: "picker-queued-paused-ru", lang: "ru", build: func(b *bench) {
			v := withJob(withPhase(core.PhaseReady), arexModel, core.DownloadQueued, 0, 0)
			v.Downloads = append(v.Downloads,
				core.DownloadJob{Model: pausedModel, State: core.DownloadPaused, Done: 3_240_000_000, Total: 17_400_000_000})
			b.openModels(v)
		}, check: func(b *bench) {
			ru := locales.In("ru")
			b.want(ru.DlQueued(), ru.DlPaused()+" · 3,2 из 17,4 ГБ", ru.DlResume())
		}},
		// The search found nothing, or could not reach the hub.
		{name: "add-model-nothing-found", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("zzqx")
			b.searched("zzqx", nil)
		}, check: func(b *bench) { b.want(en.AddModelNothing()) }},
		{name: "add-model-unreachable", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("qwen")
			b.tt.Advance(searchDelay)
			b.p.SetModelSearchFailed("qwen")
			b.tt.Frame()
		}, check: func(b *bench) { b.want(en.AddModelUnreachable()) }},
		{name: "add-model-no-variants", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("acme/Empty-GGUF:")
			b.p.SetModelInfo("acme/Empty-GGUF", hf.RepoInfo{ID: "acme/Empty-GGUF", GGUF: true, Refused: 2})
			b.tt.Frame()
		}, check: func(b *bench) { b.want(en.AddModelNoVariants("Splash")) }},
		// A repository without a vision projector: Splash gets the model for
		// text only from the start, and the summary says so.
		{name: "add-model-text-only", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("BoldingBuilds/AREX-2-Abliterated-GGUF:")
			b.p.SetModelInfo("BoldingBuilds/AREX-2-Abliterated-GGUF", hf.RepoInfo{ID: "BoldingBuilds/AREX-2-Abliterated-GGUF",
				GGUF: true, Variants: []hf.Variant{{Tag: "Abliterated-Q4_K_M", Bytes: total, Parts: 1}}})
			b.tt.Frame()
			b.click("Abliterated-Q4_K_M")
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("BoldingBuilds · Abliterated-Q4_K_M · 16.6 GB", en.TextOnly())
			b.click("Download AREX-2-Abliterated-GGUF")
			b.called("Download " + arexModel.ID + " text-only")
		}},
		// The server could not prepare the model's image input: one click
		// serves it for text only.
		{name: "error-no-vision", build: func(b *bench) {
			b.show(failed(engine.FailNoVision, 1, "error: the GGUF repository has no BF16, F32 or F16 vision projector (no GGUF named mmproj); use --language-only to serve text only"), time.Minute)
		}, check: func(b *bench) {
			b.want(en.ErrTitleNoVision(), en.ErrActTextOnly())
			b.none(en.ErrTitleModel())
			b.click(en.ErrActTextOnly())
			b.called("ServeTextOnly " + sampleModel.ID)
		}},
		{name: "error-no-vision-ru", lang: "ru", build: func(b *bench) {
			b.show(failed(engine.FailNoVision, 1, "error: the GGUF repository has no BF16, F32 or F16 vision projector (no GGUF named mmproj); use --language-only to serve text only"), time.Minute)
		}, check: func(b *bench) {
			b.want(locales.In("ru").ErrTitleNoVision(), "Запустить без изображений")
		}},
		// An online run that could not fetch its model: no word of the
		// model-updates switch.
		{name: "error-model-online", build: func(b *bench) {
			b.show(failed(engine.FailModel, 1, "error: cannot resolve a/b: 404 Client Error"), time.Minute)
		}, check: func(b *bench) {
			b.want(en.ErrTitleModelOnline())
			b.none(en.ErrTitleModel())
		}},
	}
}
