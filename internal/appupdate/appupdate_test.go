package appupdate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeRelease struct {
	version string
	err     error
	gate    chan struct{} // Install waits for it when not nil
	mu      sync.Mutex
	runs    int
}

func (r *fakeRelease) Version() string { return r.version }

func (r *fakeRelease) Install(ctx context.Context, progress func(done, total int64)) error {
	r.mu.Lock()
	r.runs++
	r.mu.Unlock()
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	progress(10, 10)
	return r.err
}

type fakeSource struct {
	mu      sync.Mutex
	enabled bool
	release *fakeRelease
	err     error
	checks  int
}

func (s *fakeSource) Enabled() bool { return s.enabled }

func (s *fakeSource) Check(context.Context) (Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks++
	if s.err != nil {
		return nil, s.err
	}
	if s.release == nil {
		return nil, nil
	}
	return s.release, nil
}

// harness runs an updater whose background checks fire on tick.
type harness struct {
	u      *Updater
	tick   chan time.Time
	states chan State
	mu     sync.Mutex
	waits  []time.Duration // what the updater asked to wait, in order
}

func (h *harness) lastWait() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waits[len(h.waits)-1]
}

func start(t *testing.T, src *fakeSource, automatic bool) *harness {
	t.Helper()
	h := &harness{tick: make(chan time.Time), states: make(chan State, 64)}
	h.u = New(Options{
		Source:    src,
		Automatic: func() bool { return automatic },
		OnChange:  func(s State) { h.states <- s },
		After: func(d time.Duration) <-chan time.Time {
			h.mu.Lock()
			h.waits = append(h.waits, d)
			h.mu.Unlock()
			return h.tick
		},
	})
	h.u.Start()
	t.Cleanup(h.u.Close)
	return h
}

// until waits for a state that ok accepts.
func (h *harness) until(t *testing.T, ok func(State) bool) State {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-h.states:
			if ok(s) {
				return s
			}
		case <-deadline:
			t.Fatalf("no such state; last %+v", h.u.State())
		}
	}
}

func TestDisabledDoesNothing(t *testing.T) {
	src := &fakeSource{release: &fakeRelease{version: "0.2.0"}}
	h := start(t, src, true)
	h.u.Check()
	if s := h.u.State(); s.Enabled || s.Phase != PhaseIdle {
		t.Fatalf("state %+v", s)
	}
	if src.checks != 0 {
		t.Fatal("a disabled updater asked the feed")
	}
}

func TestBackgroundCheckDownloadsAndWaitsForRestart(t *testing.T) {
	src := &fakeSource{enabled: true, release: &fakeRelease{version: "0.2.0"}}
	h := start(t, src, true)
	h.tick <- time.Now()
	s := h.until(t, func(s State) bool { return s.Phase == PhaseReady })
	if s.Version != "0.2.0" || s.Check != CheckNone {
		t.Fatalf("state %+v", s)
	}
}

func TestManualDownloadsOnlyWhenAsked(t *testing.T) {
	r := &fakeRelease{version: "0.2.0"}
	src := &fakeSource{enabled: true, release: r}
	h := start(t, src, false)
	h.tick <- time.Now()
	h.until(t, func(s State) bool { return s.Phase == PhaseAvailable })
	if r.runs != 0 {
		t.Fatal("downloaded without being asked")
	}
	h.u.Download()
	h.until(t, func(s State) bool { return s.Phase == PhaseReady })
}

func TestUserCheckResults(t *testing.T) {
	src := &fakeSource{enabled: true}
	h := start(t, src, false)

	h.u.Check()
	s := h.until(t, func(s State) bool { return s.CheckSeq == 1 })
	if s.Check != CheckUpToDate || s.Phase != PhaseIdle {
		t.Fatalf("up to date: %+v", s)
	}

	src.mu.Lock()
	src.err = errors.New("offline")
	src.mu.Unlock()
	h.u.Check()
	s = h.until(t, func(s State) bool { return s.CheckSeq == 2 })
	if s.Check != CheckFailed {
		t.Fatalf("failed: %+v", s)
	}

	src.mu.Lock()
	src.err, src.release = nil, &fakeRelease{version: "0.3.0"}
	src.mu.Unlock()
	h.u.Check()
	s = h.until(t, func(s State) bool { return s.CheckSeq == 3 })
	if s.Check != CheckFound || s.Version != "0.3.0" || s.Phase != PhaseAvailable {
		t.Fatalf("found: %+v", s)
	}
}

func TestBackgroundFailureStaysQuiet(t *testing.T) {
	src := &fakeSource{enabled: true, err: errors.New("offline")}
	h := start(t, src, true)
	h.tick <- time.Now()
	s := h.until(t, func(s State) bool { return s.Phase != PhaseChecking })
	if s.Phase != PhaseIdle || s.Check != CheckNone {
		t.Fatalf("state %+v", s)
	}
	// The loop asks for its next wait after the state settles.
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.waits)
		h.mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if w := h.lastWait(); w != retryWait {
		t.Fatalf("next check in %v after a failure, want %v", w, retryWait)
	}
}

func TestFailedInstallCanBeRetried(t *testing.T) {
	r := &fakeRelease{version: "0.2.0", err: errors.New("bad signature")}
	src := &fakeSource{enabled: true, release: r}
	h := start(t, src, true)
	h.tick <- time.Now()
	h.until(t, func(s State) bool { return s.Phase == PhaseFailed })
	r.mu.Lock()
	r.err = nil
	r.mu.Unlock()
	h.u.Download()
	h.until(t, func(s State) bool { return s.Phase == PhaseReady })
}

func TestCheckWhileReadyDoesNotAskAgain(t *testing.T) {
	src := &fakeSource{enabled: true, release: &fakeRelease{version: "0.2.0"}}
	h := start(t, src, true)
	h.tick <- time.Now()
	h.until(t, func(s State) bool { return s.Phase == PhaseReady })
	h.u.Check()
	s := h.until(t, func(s State) bool { return s.CheckSeq == 1 })
	if s.Check != CheckFound || s.Phase != PhaseReady || src.checks != 1 {
		t.Fatalf("state %+v, checks %d", s, src.checks)
	}
}
