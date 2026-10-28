package core

import (
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// An engine that sleeps by itself and cannot unload on request (llama.cpp,
// Caps.Release is false): the idle countdown runs, the server falls asleep
// (Released), and Restore wakes it with the countdown starting over.
func TestSleepOnIdleWithoutRelease(t *testing.T) {
	eng := newFakeEngine()
	eng.noRelease = true
	h := newHarness(t, eng)
	h.startReady()
	h.snap(func(s *engine.Snapshot) { s.IdleRelease = 5 * time.Minute })
	h.step(2 * time.Minute)
	v := h.s.View()
	if v.Caps.Release {
		t.Error("the view says the engine can release on request")
	}
	if v.Phase != PhaseReady || v.IdleLeft != 3*time.Minute {
		t.Fatalf("counting down: phase %d, idle left %v", v.Phase, v.IdleLeft)
	}

	// The server sleeps by itself: Released, no countdown.
	h.step(4 * time.Minute)
	if got := h.s.View().IdleLeft; got != 0 {
		t.Fatalf("idle left past the limit %v", got)
	}
	h.snap(func(s *engine.Snapshot) { s.Released, s.MemoryUsed = true, 150e6 })
	h.step(time.Second)
	v = h.waitPhase(PhaseReleased)
	if v.IdleLeft != 0 || !v.Snapshot.Released || v.Snapshot.IdleRelease != 5*time.Minute {
		t.Fatalf("asleep: idle left %v, snapshot %+v", v.IdleLeft, v.Snapshot)
	}
	h.step(10 * time.Minute)
	if got := h.s.View().IdleLeft; got != 0 {
		t.Fatalf("idle left while asleep %v", got)
	}

	// Load: the one-token request wakes it. That request is not counted as
	// activity here (a probe sees no new request), yet the countdown starts
	// from the wake.
	h.s.Restore()
	h.wait("restore call", func(View) bool {
		h.eng.mu.Lock()
		defer h.eng.mu.Unlock()
		return h.eng.restores == 1
	})
	h.snap(func(s *engine.Snapshot) { s.Released, s.MemoryUsed = false, 1100e6 })
	h.step(time.Second)
	v = h.waitPhase(PhaseReady)
	if v.IdleLeft != 5*time.Minute {
		t.Errorf("idle left after the wake %v, want 5m", v.IdleLeft)
	}
	h.step(time.Minute)
	if got := h.s.View().IdleLeft; got != 4*time.Minute {
		t.Errorf("idle left a minute after the wake %v, want 4m", got)
	}
}

// A server somebody else started wakes up too: the countdown starts again.
func TestExternalWakeRestartsCountdown(t *testing.T) {
	eng := newFakeEngine()
	eng.noRelease = true
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.eng.set(func(e *fakeEngine) {
		e.running = []engine.Running{{PID: 77, Port: h.port, Model: "m/x"}}
		e.snap = engine.Snapshot{Reachable: true, Ready: true, Authorized: true, IdleRelease: 5 * time.Minute}
	})
	h.s.Start()
	h.waitPhase(PhaseExternal)
	h.step(time.Second)
	h.step(4 * time.Minute)
	h.snap(func(s *engine.Snapshot) { s.Released = true })
	h.step(time.Second)
	if v := h.s.View(); v.Phase != PhaseExternal || !v.Snapshot.Released || v.IdleLeft != 0 {
		t.Fatalf("asleep: phase %d idle left %v snapshot %+v", v.Phase, v.IdleLeft, v.Snapshot)
	}
	h.step(10 * time.Minute)
	h.snap(func(s *engine.Snapshot) { s.Released = false })
	h.step(time.Second)
	if got := h.s.View().IdleLeft; got != 5*time.Minute {
		t.Errorf("idle left after the wake %v, want 5m", got)
	}
}
