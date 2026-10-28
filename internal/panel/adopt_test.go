package panel

import (
	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/locales"
)

// qwen38OnDisk is Qwen3.8-27B as llama.cpp downloaded it: in the hub
// cache, not installed for Splash (engine.Adopter).
var qwen38OnDisk = engine.Model{ID: "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", Name: "Qwen3.8-27B-GGUF",
	Owner: "unsloth", Variant: "UD-Q4_K_M", SizeBytes: 16_464_440_224, ContextLength: 262144}

// adoptBoards: models on disk that the engine can add from their files.
func adoptBoards() []board {
	en := locales.In("en")
	add := en.Adopt() + " " + qwen38OnDisk.Name
	return []board{
		// The model list ends with them; Add downloads what is missing.
		{name: "picker-adopt", build: func(b *bench) {
			v := withPhase(core.PhaseStopped)
			v.Adoptable = []engine.Model{qwen38OnDisk}
			b.openModels(v)
		}, check: func(b *bench) {
			b.want(en.AdoptTitle(), "Qwen3.8-27B-GGUF", "unsloth · UD-Q4_K_M · 16.5 GB")
			b.click(add)
			b.called("Download " + qwen38OnDisk.ID)
		}},
		{name: "picker-adopt-ru", lang: "ru", build: func(b *bench) {
			v := withPhase(core.PhaseStopped)
			v.Adoptable = []engine.Model{qwen38OnDisk}
			b.openModels(v)
		}, check: func(b *bench) {
			ru := locales.In("ru")
			b.want(ru.AdoptTitle(), ru.Adopt(), "unsloth · UD-Q4_K_M · 16,5 ГБ")
		}},
		// No model yet: the suggestion that is on disk is offered to add,
		// not to download again.
		{name: "suggestions-adopt", build: func(b *bench) {
			v := noModels(withPhase(core.PhaseStopped))
			v.Adoptable = []engine.Model{qwen38OnDisk}
			b.show(v, 0)
		}, check: func(b *bench) {
			b.want(en.AdoptTitle(), "Qwen3.6-35B-A3B", "Download Qwen3.6-35B-A3B")
			b.none("Download Qwen3.8-27B")
			b.click(add)
			b.called("Download " + qwen38OnDisk.ID)
		}},
		// Once added, it is a download row, not an Add button.
		{name: "picker-adopt-running", build: func(b *bench) {
			v := withJob(withPhase(core.PhaseStopped), qwen38OnDisk, core.DownloadRunning, 0, 0)
			v.Adoptable = []engine.Model{qwen38OnDisk}
			b.openModels(v)
		}, check: func(b *bench) {
			b.none(en.AdoptTitle(), add)
		}},
	}
}
