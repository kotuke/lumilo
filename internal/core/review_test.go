package core

import (
	"errors"
	"fmt"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// The network access without a key would serve every interface open:
// the start is refused with FailNoKey and nothing runs.
func TestNetworkWithoutKeyRefused(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	st := h.s.Settings()
	st.Access, st.APIKey = engine.AccessNetwork, ""
	h.s.Apply(st)
	h.s.Start()
	v := h.waitPhase(PhaseError)
	if v.Failure.Kind != engine.FailNoKey || h.st.count() != 0 {
		t.Errorf("failure %+v, starts %d", v.Failure, h.st.count())
	}
	// With a key it starts.
	st.APIKey = "sk-1"
	h.s.Apply(st)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	if h.st.count() != 1 {
		t.Errorf("starts %d", h.st.count())
	}
}

// A server that outlives the stop of an engine switch (it ignores even
// SIGKILL for a while) must not block every later start once it exits.
func TestProcessOutlivesSwitch(t *testing.T) {
	other := newFakeEngine()
	other.id = "other"
	h := newHarness(t, newFakeEngine(), other)
	stubborn(h)
	h.st.ignoreKill = true
	p := h.startReady()
	h.s.SwitchEngine("other", "")
	deadline := time.Now().Add(3 * time.Second)
	for v := h.s.View(); v.EngineID != "other" || v.Switching || v.Phase != PhaseStopped; v = h.s.View() {
		if time.Now().After(deadline) {
			t.Fatalf("switch did not end: %+v", v)
		}
		h.clk.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
	p.exit(137) // the old server ends at last
	h.wait("old process gone", func(View) bool {
		h.s.mu.Lock()
		defer h.s.mu.Unlock()
		return h.s.proc == nil
	})
	h.s.Start()
	h.waitPhase(PhaseLoading)
	if h.st.count() != 2 || h.st.last().argv[0] != "/other/serve" {
		t.Errorf("starts %d, argv %v", h.st.count(), h.st.last().argv)
	}
}

// Apply never rolls the selected model back: the app builds the settings
// it applies from an older read of them.
func TestApplyKeepsSelectedModel(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	old := h.s.Settings() // model m/b
	h.s.SelectModel("m/a")
	old.IdleRelease = time.Minute
	h.s.Apply(old)
	h.s.doWait(func() {})
	if st := h.s.Settings(); st.Model != "m/a" || st.IdleRelease != time.Minute {
		t.Errorf("settings %+v", st)
	}
}

// Contexts and options are the engine's own: another engine with a model
// of the same name gets none of them.
func TestContextPerEngine(t *testing.T) {
	other := newFakeEngine()
	other.id = "other"
	h := newHarness(t, newFakeEngine(), other)
	h.waitPhase(PhaseStopped)
	st := h.s.Settings()
	st.Contexts = map[ModelKey]int{{"fake", "m/b"}: 65536}
	st.Options = map[ModelKey]engine.ModelOptions{{"fake", "m/b"}: {Draft: "mtp"}}
	h.s.Apply(st)
	h.s.SwitchEngine("other", "m/b")
	h.wait("switched", func(v View) bool { return v.EngineID == "other" && v.Phase == PhaseStopped && !v.Switching })
	h.s.Start()
	h.waitPhase(PhaseLoading)
	if p := h.st.last(); p.argv[0] != "/other/serve" || slices.Contains(p.argv, "--ctx") {
		t.Errorf("other engine got fake's context: %v", p.argv)
	}
	h.s.mu.Lock()
	cfg := h.s.resolvedLocked()
	h.s.mu.Unlock()
	if cfg.Draft != "" {
		t.Errorf("other engine got fake's draft %q", cfg.Draft)
	}
}

// The delayed SIGKILL of a foreign server checks that the PID still is
// that server on its port: the number may belong to another program by
// then.
func TestKillExternalChecksPID(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: 60, Port: h.port}} })
	h.s.Recheck()
	h.waitPhase(PhaseExternal)
	h.mu.Lock()
	h.alive[60] = true // alive, but no longer the engine's server
	h.mu.Unlock()
	h.s.KillExternal()
	h.waitPhase(PhaseStopped)
	eng.set(func(e *fakeEngine) { e.running = nil })
	h.step(5 * time.Second)
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Equal(h.kills, []string{"60:interrupt"}) {
		t.Errorf("kills %v", h.kills)
	}
}

// The same PID on another port is another server too.
func TestKillExternalChecksPort(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: 60, Port: h.port}} })
	h.s.Recheck()
	h.waitPhase(PhaseExternal)
	h.mu.Lock()
	h.alive[60] = true
	h.mu.Unlock()
	h.s.KillExternal()
	h.waitPhase(PhaseStopped)
	eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: 60, Port: h.port + 1}} })
	h.step(5 * time.Second)
	h.mu.Lock()
	defer h.mu.Unlock()
	if slices.Contains(h.kills, "60:killed") {
		t.Errorf("kills %v", h.kills)
	}
}

// A failed update is cleared once the installed version is the newest
// known, e.g. after an update by hand and a Recheck.
func TestRecheckClearsStaleUpdateFailed(t *testing.T) {
	eng := newFakeEngine()
	eng.latest = "1.5.0"
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.s.CheckUpdate()
	h.wait("update available", func(v View) bool { return v.Update == UpdateAvailable })
	h.brew = func([]string) (string, error) { return "Warning: fake 1.4.0 already installed", nil }
	h.s.Update()
	v := h.wait("update failed", func(v View) bool { return v.Update == UpdateFailed })
	if v.UpdateReason != UpdateReasonNoNewer {
		t.Errorf("reason %d", v.UpdateReason)
	}
	eng.set(func(e *fakeEngine) { e.inst.Version = "1.5.0" })
	h.s.Recheck()
	v = h.wait("failure cleared", func(v View) bool { return v.Update == UpdateNone })
	if v.UpdateError != "" || v.UpdateReason != UpdateReasonNone || v.Version != "1.5.0" {
		t.Errorf("after recheck %+v", v)
	}
}

// An agent asked for while our server stops gets a notice, not silence.
func TestRunAgentWhileStoppingNotice(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	stubborn(h)
	h.startReady()
	h.s.Stop()
	h.waitPhase(PhaseStopping)
	h.s.RunAgent("claude")
	v := h.wait("notice", func(v View) bool { return v.Notice.Kind == NoticeAgentUnavailable })
	if v.Notice.Seq != 1 {
		t.Errorf("notice %+v", v.Notice)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.term) != 0 {
		t.Errorf("agent opened: %v", h.term)
	}
}

// A terminal that fails to open the agent shows as a notice.
func TestOpenTerminalErrorNotice(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	h.mu.Lock()
	h.termErr = errors.New("terminal: open: boom")
	h.mu.Unlock()
	h.s.RunAgent("claude")
	v := h.wait("notice", func(v View) bool { return v.Notice.Kind == NoticeTerminalFailed })
	if v.Notice.Text != "terminal: open: boom" {
		t.Errorf("notice %+v", v.Notice)
	}
	h.mu.Lock()
	h.termErr = fmt.Errorf("app: %w", ErrKeyNotStored)
	h.mu.Unlock()
	h.s.RunAgent("claude")
	v = h.wait("key notice", func(v View) bool { return v.Notice.Kind == NoticeKeyNotStored })
	if v.Notice.Seq != 2 {
		t.Errorf("seq %d", v.Notice.Seq)
	}
}

// A download asked for while a foreign server runs starts nothing and
// leaves nothing behind for the next normal Start.
func TestDownloadInExternalLeavesNothing(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: 70, Port: h.port}} })
	h.s.Recheck()
	h.waitPhase(PhaseExternal)
	h.s.Download("hf/new:Q4")
	h.s.doWait(func() {})
	eng.set(func(e *fakeEngine) { e.running = nil })
	h.s.Recheck()
	v := h.waitPhase(PhaseStopped)
	if v.Download != "" {
		t.Errorf("download kept: %q", v.Download)
	}
	h.s.Start()
	h.waitPhase(PhaseLoading)
	if p := h.st.last(); p.argv[2] != "m/b" || slices.Contains(p.argv, "--online") {
		t.Errorf("start hijacked: %v", p.argv)
	}
}

// Nor does one asked for while our server stops.
func TestDownloadWhileStoppingLeavesNothing(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	stubborn(h)
	p := h.startReady()
	h.s.Stop()
	h.waitPhase(PhaseStopping)
	h.s.Download("hf/new:Q4")
	h.s.doWait(func() {})
	p.exit(0)
	v := h.waitPhase(PhaseStopped)
	if v.Download != "" {
		t.Errorf("download kept: %q", v.Download)
	}
	h.s.Start()
	h.waitPhase(PhaseLoading)
	if p := h.st.last(); p.argv[2] != "m/b" || slices.Contains(p.argv, "--online") {
		t.Errorf("start hijacked: %v", p.argv)
	}
}

// The tick runs every second; a view that did not change is not sent.
func TestTickPublishesOnlyChanges(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	settle := func() int {
		h.s.doWait(func() {})
		time.Sleep(30 * time.Millisecond) // the publisher runs
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.views
	}
	h.step(time.Second)
	before := settle()
	for range 5 {
		h.step(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	if after := settle(); after != before {
		t.Errorf("%d views sent for unchanged ticks", after-before)
	}
	// A change is sent.
	h.snap(func(s *engine.Snapshot) { s.Active = 1 })
	h.step(time.Second)
	h.wait("generating", func(v View) bool { return v.Phase == PhaseGenerating })
	if after := settle(); after == before {
		t.Error("the change was not sent")
	}
}

// stubborn makes the servers of h ignore the stop signals. Close would
// wait for them on the fake clock, so they end before it runs.
func stubborn(h *harness) {
	h.st.exitOn = map[syscall.Signal]int{}
	h.t.Cleanup(func() {
		h.st.mu.Lock()
		defer h.st.mu.Unlock()
		for _, p := range h.st.procs {
			p.exit(0)
		}
	})
}
