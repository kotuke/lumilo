package panel

import (
	"strings"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/locales"
)

// counted is v with the deletion of the sample model counted.
func counted(v core.View) core.View {
	v.Removal = core.Removal{Model: sampleModel.ID, Counted: true,
		Plan: engine.RemovePlan{Bytes: 15_700_000_000, Shared: true}}
	return v
}

// removeBoards are the deletion of an installed model: the trash button of
// a model row, its dialog, a model whose files are going and a failure.
func removeBoards() []board {
	en := locales.In("en")
	label := en.RmLabel(sampleModel.Name)
	ask := func(b *bench) {
		b.openModels(withPhase(core.PhaseReady))
		b.click(label)
	}
	return []board{
		// The dialog shows at once and waits for the count; then it says
		// what goes, that the cache is shared and the server stops.
		{name: "picker-remove-dialog", build: func(b *bench) {
			ask(b)
			b.show(counted(withPhase(core.PhaseReady)), 0)
		}, check: func(b *bench) {
			b.called("PlanRemove " + sampleModel.ID)
			b.want(en.RmTitle(sampleModel.Name),
				strings.Join([]string{en.RmFrees("15.7 GB"), en.RmShared(), en.RmStops()}, " "))
			b.click(en.RmBtn())
			b.called("RemoveModel " + sampleModel.ID)
			b.notCalled("SelectModel " + sampleModel.ID)
		}},
		{name: "picker-remove-dialog-ru", lang: "ru", build: func(b *bench) {
			b.openModels(withPhase(core.PhaseStopped))
			b.click(locales.In("ru").RmLabel(sampleModel.Name))
			b.show(counted(withPhase(core.PhaseStopped)), 0)
		}, check: func(b *bench) {
			ru := locales.In("ru")
			b.want(ru.RmTitle(sampleModel.Name), ru.RmFrees("15,7 ГБ")+" "+ru.RmShared())
			b.none(ru.RmStops())
		}},
		// Counting: Delete waits; Cancel forgets the count.
		{name: "picker-remove-counting", build: ask, check: func(b *bench) {
			b.want(en.RmCounting())
			b.click(en.RmBtn())
			b.notCalled("RemoveModel " + sampleModel.ID)
			b.click(en.CancelBtn())
			b.called("CancelRemove")
			b.none(en.RmTitle(sampleModel.Name))
		}},
		// Its files are going: it cannot be picked, nor deleted again.
		{name: "picker-removing", build: func(b *bench) {
			v := withPhase(core.PhaseStopped)
			v.Removal = core.Removal{Model: sampleModel.ID, Running: true}
			b.openModels(v)
		}, check: func(b *bench) {
			b.want(en.RmDeleting())
			b.none(label)
		}},
		// A deletion that failed, or was refused once the dialog closed, is
		// a notice with the reason; the list closes so it shows.
		{name: "remove-failed-notice", build: func(b *bench) {
			v := withPhase(core.PhaseStopped)
			v.Removal = core.Removal{Model: sampleModel.ID, Running: true}
			b.openModels(v)
			v.Removal = core.Removal{}
			v.Notice = core.Notice{Kind: core.NoticeRemoveFailed, Text: "permission denied", Seq: 1}
			b.show(v, 0)
		}, check: func(b *bench) {
			b.want(en.RmFailed(), "permission denied")
			b.none(label)
		}},
		// A download writes where the files are: the button only shows.
		{name: "picker-remove-blocked", build: func(b *bench) {
			b.openModels(withJob(withPhase(core.PhaseReady), arexModel, core.DownloadRunning, 1, 10))
		}, check: func(b *bench) {
			b.click(label)
			b.notCalled("PlanRemove " + sampleModel.ID)
			b.none(en.RmTitle(sampleModel.Name))
		}},
	}
}
