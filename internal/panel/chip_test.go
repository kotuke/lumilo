package panel

import (
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/locales"
)

// chipBoards are the model-updates chip of the server card, a toggle for
// Settings → "Check for model updates".
func chipBoards() []board {
	en := locales.In("en")
	withUpdates := func(b *bench, on bool) {
		pr := defaultPrefs()
		pr.ModelUpdates = on
		b.p.SetPrefs(pr)
	}
	return []board{
		// Off: muted, and a click turns the checks on.
		{name: "chip-updates-off", build: func(b *bench) {
			withUpdates(b, false)
			b.show(withPhase(core.PhaseStopped), time.Minute)
		}, check: func(b *bench) {
			b.want(en.NoUpdates())
			b.none(en.UpdatesOn(), en.AppliesOnRestart())
			b.click(en.NoUpdates())
			b.called("SetPrefs")
			if !b.lastPrefs().ModelUpdates {
				b.t.Error("the chip did not turn model updates on")
			}
			b.tt.Frame()
			b.want(en.UpdatesOn())
			// Stopped: the next start reads it, nothing to restart.
			b.none(en.AppliesOnRestart())
			// Settings shows the same switch, on, and keeps it.
			b.click(b.m.Settings())
			b.tt.Frame()
			if !b.p.set.updates || !b.lastPrefs().ModelUpdates {
				b.t.Errorf("settings switch %v, stored %v", b.p.set.updates, b.lastPrefs().ModelUpdates)
			}
		}},
		// On, with the server running: a click turns them off, and the card
		// says it applies on restart, with a Restart button.
		{name: "chip-updates-on", build: func(b *bench) {
			withUpdates(b, true)
			b.show(withPhase(core.PhaseReady), time.Minute)
		}, check: func(b *bench) {
			b.want(en.UpdatesOn())
			b.click(en.UpdatesOn())
			if b.lastPrefs().ModelUpdates {
				b.t.Error("the chip did not turn model updates off")
			}
			v := withPhase(core.PhaseReady)
			v.PendingRestart = true
			b.show(v, time.Second)
			b.want(en.NoUpdates(), en.AppliesOnRestart())
			b.click(en.Restart())
			b.called("Restart")
		}},
		{name: "chip-updates-restart-ru", lang: "ru", build: func(b *bench) {
			withUpdates(b, true)
			b.show(withPhase(core.PhaseReady), time.Minute)
			b.click(locales.In("ru").UpdatesOn())
			v := withPhase(core.PhaseReady)
			v.PendingRestart = true
			b.show(v, time.Second)
		}, check: func(b *bench) {
			ru := locales.In("ru")
			b.want(ru.NoUpdates(), ru.AppliesOnRestart())
		}},
	}
}
