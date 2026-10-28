package panel

import (
	"slices"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
)

// noText fails when a text is exactly s (none matches parts of texts too:
// "Unload" is inside "Unloads after 7 minutes idle").
func (b *bench) noText(texts ...string) {
	b.t.Helper()
	for _, s := range texts {
		if slices.Contains(b.tt.Texts(), s) {
			b.t.Errorf("unexpected button %q", s)
		}
	}
}

// idleBoards are llama.cpp's idle unload: the server sleeps by itself after
// --sleep-idle-seconds, so the panel shows the countdown and the Released
// state but no Unload button (the engine has no release request). Load stays:
// Restore wakes the server with a one-token request.
func idleBoards() []board {
	asleep := func() core.View {
		v := llamaView()
		v.Phase = core.PhaseReleased
		v.IdleLeft = 0
		v.Snapshot.Released, v.Snapshot.MemoryUsed = true, 150_000_000
		return v
	}
	return []board{
		{name: "llamacpp-idle-countdown", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := llamaView()
			v.Phase, v.IdleLeft = core.PhaseReady, 7*time.Minute
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			if b.p.view.Caps.Release {
				b.t.Error("llama.cpp caps say it can release on request")
			}
			b.want("Ready", "Unloads after 7 minutes idle", "Memory", "Stop")
			b.noText("Unload", "Load")
			b.none("Unloaded")
		}},
		{name: "llamacpp-idle-last-minute", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := llamaView()
			v.Phase, v.IdleLeft = core.PhaseReady, 40*time.Second
			b.show(v, 0)
		}, check: func(b *bench) {
			b.want("Unloads after 40 seconds idle")
			b.noText("Unload", "Load")
		}},
		{name: "llamacpp-idle-never", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := llamaView()
			v.Phase, v.IdleLeft = core.PhaseReady, 0 // "Never": no --sleep-idle-seconds
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			b.want("Ready", "Memory")
			b.noText("Unload", "Load")
			b.none("idle")
		}},
		{name: "llamacpp-released", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(asleep(), 3*time.Hour+2*time.Minute)
		}, check: func(b *bench) {
			b.want("Unloaded", "0.2 GB", "Load", "Loads on first request", "Stop")
			b.noText("Unload")
			b.none("Ready", "idle")
			b.click("Load")
			b.called("Restore")
			b.notCalled("Release")
		}},
		{name: "llamacpp-restoring", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := asleep()
			v.Phase = core.PhaseRestoring
			v.Snapshot.Released, v.Snapshot.Restoring = false, true
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			b.want("Loading", "Load", "Stop")
			b.noText("Unload")
			b.none("Unloaded", "idle")
		}},
		{name: "llamacpp-external-asleep", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := asleep()
			v.Phase = core.PhaseExternal
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			b.want("Load")
			b.noText("Unload")
			b.click("Load")
			b.called("Restore")
		}},
	}
}
