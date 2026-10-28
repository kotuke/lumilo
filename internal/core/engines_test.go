package core

import (
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

func otherEngine() *fakeEngine {
	e := newFakeEngine()
	e.id = "other"
	e.inst = engine.Install{Ready: true} // not installed
	e.models = []engine.Model{{ID: "o/x"}}
	return e
}

// The engine list shows every engine with what Detect found.
func TestEngineList(t *testing.T) {
	h := newHarness(t, newFakeEngine(), otherEngine())
	v := h.waitPhase(PhaseStopped)
	if len(v.Engines) != 2 || v.Engines[0].ID != "fake" || v.Engines[0].Name != "Fake" || !v.Engines[0].Installed ||
		v.Engines[0].Version != "1.4.0" || string(v.Engines[0].Logo.Dark) != "<svg/>" || v.Engines[1].ID != "other" || v.Engines[1].Installed || v.Engines[1].Version != "" {
		t.Errorf("engines %+v", v.Engines)
	}
	if v.EngineID != "fake" || v.Install.InstallCommand != "brew install tap/fake" || !v.Caps.Release || len(v.Suggestions) != 1 {
		t.Errorf("engine parts %+v", v)
	}
}

// A switch stops our server, then shows the other engine: here not
// installed, so the install screen, with its own formula. A switch back
// runs the first engine with the model given.
func TestSwitchEngineStopsOurServer(t *testing.T) {
	other := otherEngine()
	h := newHarness(t, newFakeEngine(), other)
	p := h.startReady()
	h.s.SwitchEngine("other", "")
	v := h.waitPhase(PhaseNotInstalled)
	if !slices.Contains(p.sent(), syscall.SIGINT) {
		t.Errorf("our server got %v", p.sent())
	}
	if v.EngineID != "other" || v.Install.InstallCommand != "brew install tap/other" || v.Version != "" || v.Snapshot.Reachable || v.Switching {
		t.Errorf("after switch %+v", v)
	}
	// Nothing starts while the engine is missing.
	h.s.Start()
	h.s.doWait(func() {})
	if h.st.count() != 1 {
		t.Errorf("%d starts", h.st.count())
	}

	h.s.SwitchEngine("fake", "m/a")
	v = h.wait("back", func(v View) bool { return v.EngineID == "fake" && v.Phase == PhaseStopped })
	if v.Model.ID != "m/a" || v.Version != "1.4.0" {
		t.Errorf("back %+v", v.Model)
	}
}

// A server Lumilo did not start is never signalled by a switch.
func TestSwitchEngineLeavesExternalServer(t *testing.T) {
	eng := newFakeEngine()
	eng.running = []engine.Running{{PID: 4242, Port: 9999, Model: "m/a"}}
	h := newHarness(t, eng, otherEngine())
	h.waitPhase(PhaseExternal)
	h.s.SwitchEngine("other", "")
	h.waitPhase(PhaseNotInstalled)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.kills) != 0 {
		t.Errorf("signals to foreign servers: %v", h.kills)
	}
}

// No switch while an install runs; an unknown engine is ignored.
func TestSwitchEngineRefused(t *testing.T) {
	other := otherEngine()
	h := newHarness(t, other, newFakeEngine())
	h.waitPhase(PhaseNotInstalled)
	release := make(chan struct{})
	h.brew = func([]string) (string, error) { <-release; return "", nil }
	h.s.Install()
	h.waitPhase(PhaseInstalling)
	h.s.SwitchEngine("fake", "")
	h.s.SwitchEngine("nope", "")
	h.s.doWait(func() {})
	if v := h.s.View(); v.EngineID != "other" || v.Phase != PhaseInstalling {
		t.Errorf("switched during install: %s %d", v.EngineID, v.Phase)
	}
	close(release)
}

// The context of the selected model goes to the server; another model's
// context restarts nothing, its own does.
func TestContextPerModel(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	st := h.s.Settings()
	st.Contexts = map[ModelKey]int{{"fake", "m/b"}: 65536, {"fake", "m/a"}: 32768}
	h.s.Apply(st)
	p := h.startReady()
	if i := slices.Index(p.argv, "--ctx"); i < 0 || p.argv[i+1] != "65536" {
		t.Errorf("argv %v", p.argv)
	}
	st.Contexts = map[ModelKey]int{{"fake", "m/b"}: 65536, {"fake", "m/a"}: 131072}
	h.s.Apply(st)
	h.s.doWait(func() {})
	if h.s.PendingRestart() {
		t.Error("another model's context asks for a restart")
	}
	st.Contexts = map[ModelKey]int{{"fake", "m/b"}: 0}
	h.s.Apply(st)
	h.s.doWait(func() {})
	if !h.s.PendingRestart() {
		t.Error("the model's own context does not ask for a restart")
	}
}

// A download run starts online with the new model, shows Downloading until
// the engine loads it, and once it serves makes the listed model the
// selected one: no restart is pending, the app hears of it.
func TestDownload(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.s.Download("hf/new:Q4")
	v := h.waitPhase(PhaseLoading)
	p := h.st.last()
	if !slices.Equal(p.argv[:3], []string{"/fake/serve", "--model", "hf/new:Q4"}) || !slices.Contains(p.argv, "--online") {
		t.Errorf("argv %v", p.argv)
	}
	if !v.Downloading || v.Download != "hf/new:Q4" || v.Model.Name != "new" || v.Model.Variant != "Q4" || v.PendingRestart {
		t.Errorf("downloading view %+v", v)
	}
	p.say("FETCHING")
	h.wait("bytes", func(v View) bool { return v.DownloadTotal == 5e9 })
	p.say("LOADING")
	h.wait("loading", func(v View) bool { return !v.Downloading && v.Download == "hf/new:Q4" })

	// The engine lists the model under another ID (a file path).
	eng.set(func(e *fakeEngine) {
		e.models = append(e.models, engine.Model{ID: "/cache/new-Q4.gguf", Name: "new"})
		e.snap = engine.Snapshot{Reachable: true, Ready: true, Authorized: true, Model: "/cache/new-Q4.gguf"}
	})
	p.say("READY")
	v = h.wait("chosen", func(v View) bool { return v.Download == "" && v.Model.ID == "/cache/new-Q4.gguf" })
	if v.PendingRestart || len(v.Models) != 3 || h.s.Settings().Model != "/cache/new-Q4.gguf" {
		t.Errorf("after download %+v", v)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Equal(h.chosen, []string{"fake /cache/new-Q4.gguf"}) {
		t.Errorf("OnModel %v", h.chosen)
	}
}

// A failed download keeps the request, so Start tries it again; a stop
// drops it and the earlier model is back.
func TestDownloadFailsThenStops(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.Download("hf/missing")
	h.waitPhase(PhaseLoading)
	h.st.last().exit(1)
	v := h.waitPhase(PhaseError)
	if v.Download != "hf/missing" || v.Downloading {
		t.Errorf("failed %+v", v)
	}
	h.s.Start()
	h.waitPhase(PhaseLoading)
	if p := h.st.last(); p.argv[2] != "hf/missing" {
		t.Errorf("retry argv %v", p.argv)
	}
	h.s.Stop()
	v = h.waitPhase(PhaseStopped)
	if v.Download != "" || v.Model.ID != "m/b" {
		t.Errorf("after stop %+v", v.Model)
	}
}

// A download run reports what the engine says it fetches (the sizes of
// its fetches added up) and what the engine's meter measured so far, on
// every tick while it fetches.
func TestDownloadProgress(t *testing.T) {
	eng := newFakeEngine()
	var done atomic.Int64
	eng.meter = func() int64 { return done.Load() }
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.s.Download("hf/new:Q4")
	h.waitPhase(PhaseLoading)
	p := h.st.last()
	p.say("FETCHING")
	p.say("FETCHING") // the draft, from another repository
	h.wait("total", func(v View) bool { return v.DownloadTotal == 10e9 })
	done.Store(1_500_000_000)
	h.step(time.Second)
	v := h.wait("bytes", func(v View) bool { return v.DownloadBytes == 1_500_000_000 })
	if !v.Downloading || v.DownloadTotal != 10e9 {
		t.Errorf("progress %+v", v)
	}
	// A meter that reads less (a file renamed meanwhile) does not go back.
	done.Store(1_000_000_000)
	h.step(time.Second)
	if v := h.s.View(); v.DownloadBytes != 1_500_000_000 {
		t.Errorf("went back to %d", v.DownloadBytes)
	}
	// Once the model loads, the files are there: no more reads.
	p.say("LOADING")
	h.wait("loading", func(v View) bool { return !v.Downloading })
	done.Store(9e9)
	h.step(time.Second)
	if v := h.s.View(); v.DownloadBytes != 1_500_000_000 {
		t.Errorf("read while loading: %d", v.DownloadBytes)
	}
	eng.set(func(e *fakeEngine) {
		if e.watched != 1 {
			t.Errorf("%d meters", e.watched)
		}
	})
}
