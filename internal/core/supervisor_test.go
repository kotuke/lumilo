package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// fakeEngine is an engine whose state the test sets.
type fakeEngine struct {
	mu       sync.Mutex
	inst     engine.Install
	models   []engine.Model
	running  []engine.Running
	snap     engine.Snapshot
	latest   string
	feedErr  error         // LatestVersion fails with it
	feedWait chan struct{} // LatestVersion waits for it to close
	feeds    int           // LatestVersion calls
	probes   []engine.Target
	released int

	probeWait   chan struct{} // Probe waits for it to close, when set
	inProbe     int           // Probe calls running now
	maxInProbe  int           // most Probe calls that ran at once
	restoreWait chan struct{} // Restore waits for it to close, when set
	restores    int           // Restore calls
	modelsWait  chan struct{} // Models waits for it to close, when set
	inModels    chan struct{} // Models signals on it before waiting, when set

	id        string // "" = "fake"
	noRelease bool   // Caps.Release is false, as llama.cpp's

	// run is the installer's run of brew <verb> <formula>; nil = success.
	run func(verb string, log io.Writer) (string, error)
	// meter is what WatchDownload returns; watched counts its calls.
	meter   func() int64
	watched int
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		inst:   engine.Install{Installed: true, Version: "1.4.0", Ready: true},
		models: []engine.Model{{ID: "m/a"}, {ID: "m/b"}},
	}
}

func (e *fakeEngine) set(f func(e *fakeEngine)) { e.mu.Lock(); f(e); e.mu.Unlock() }

func (e *fakeEngine) ID() string {
	if e.id != "" {
		return e.id
	}
	return "fake"
}
func (e *fakeEngine) Info() engine.Info {
	return engine.Info{
		ID: e.ID(), Name: strings.ToUpper(e.ID()[:1]) + e.ID()[1:],
		Logo:        engine.Logo{Kind: engine.LogoSVG, Dark: []byte("<svg/>")},
		Caps:        engine.Caps{Release: !e.noRelease},
		Suggestions: []engine.Suggestion{{ID: "hf/new:Q4", Name: "New"}},
		ModelSearch: engine.SearchSpec{Sources: []engine.SearchSource{{Filter: "gguf"}}},
		Agents:      []engine.Agent{{ID: "claude", Name: "Claude"}},
	}
}

// The fake engine is its own installer, with the formula tap/<id>.
func (e *fakeEngine) Installer() engine.Installer { return fakeInstaller{e} }

type fakeInstaller struct{ e *fakeEngine }

func (f fakeInstaller) Describe() engine.InstallInfo {
	return engine.InstallInfo{Method: engine.InstallHomebrew, Size: 1, InstallCommand: "brew install tap/" + f.e.ID(),
		UpgradeCommand: "brew upgrade tap/" + f.e.ID()}
}
func (f fakeInstaller) Detect(ctx context.Context) engine.Install { return f.e.Detect(ctx) }
func (f fakeInstaller) Install(_ context.Context, log io.Writer) (string, error) {
	return f.e.runBrew("install", log)
}
func (f fakeInstaller) Upgrade(_ context.Context, log io.Writer) (string, error) {
	return f.e.runBrew("upgrade", log)
}
func (f fakeInstaller) Latest(ctx context.Context) (string, error) { return f.e.LatestVersion(ctx) }
func (f fakeInstaller) ReleaseNotesURL(string) string              { return "" }

func (e *fakeEngine) runBrew(verb string, log io.Writer) (string, error) {
	e.mu.Lock()
	run := e.run
	e.mu.Unlock()
	if run == nil {
		return "", nil
	}
	return run(verb, log)
}

func (e *fakeEngine) WatchDownload(string) func() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.watched++
	return e.meter
}
func (e *fakeEngine) ParseModel(spec string) (engine.Model, error) {
	owner, rest, ok := strings.Cut(spec, "/")
	if !ok {
		return engine.Model{}, errors.New("bad spec")
	}
	name, variant, _ := strings.Cut(rest, ":")
	return engine.Model{ID: spec, Owner: owner, Name: name, Variant: variant}, nil
}
func (e *fakeEngine) Detect(context.Context) engine.Install {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inst
}
func (e *fakeEngine) LatestVersion(ctx context.Context) (string, error) {
	e.mu.Lock()
	wait := e.feedWait
	e.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.feeds++
	return e.latest, e.feedErr
}
func (e *fakeEngine) Models(context.Context) ([]engine.Model, error) {
	e.mu.Lock()
	wait, in, models := e.modelsWait, e.inModels, slices.Clone(e.models)
	e.mu.Unlock()
	if in != nil {
		in <- struct{}{}
	}
	if wait != nil {
		<-wait
	}
	return models, nil
}
func (e *fakeEngine) ServeCommand(c engine.Config) ([]string, []string) {
	argv := []string{"/" + e.ID() + "/serve", "--model", c.Model, "--port", fmt.Sprint(c.Port)}
	if c.ContextTokens > 0 {
		argv = append(argv, "--ctx", fmt.Sprint(c.ContextTokens))
	}
	if c.ModelUpdates {
		argv = append(argv, "--online")
	}
	return argv, []string{"KEY=" + c.APIKey}
}
func (e *fakeEngine) StopPolicy() engine.StopPolicy {
	return engine.StopPolicy{Graceful: syscall.SIGINT, Force: syscall.SIGTERM, KillWait: 5 * time.Second}
}
func (e *fakeEngine) ParseLine(line string, _ bool) (engine.Event, bool) {
	switch line {
	case "READY":
		return engine.Event{Kind: engine.EventReady, Line: line}, true
	case "FETCHING":
		return engine.Event{Kind: engine.EventDownloading, Line: line, Bytes: 5e9}, true
	case "LOADING":
		return engine.Event{Kind: engine.EventLoading, Line: line}, true
	}
	return engine.Event{}, false
}
func (e *fakeEngine) ClassifyFailure(code int, tail []string) engine.Failure {
	for _, l := range tail {
		switch {
		case strings.Contains(l, "already serving"):
			return engine.Failure{Kind: engine.FailAlreadyServing, Code: code, Line: l}
		case strings.Contains(l, "model not found"):
			return engine.Failure{Kind: engine.FailModel, Code: code, Line: l}
		case strings.Contains(l, "memory"):
			return engine.Failure{Kind: engine.FailMemory, Code: code, Line: l}
		}
	}
	return engine.Failure{Kind: engine.FailExit, Code: code}
}
func (e *fakeEngine) FindRunning() ([]engine.Running, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.running), nil
}
func (e *fakeEngine) Probe(_ context.Context, t engine.Target) engine.Snapshot {
	e.mu.Lock()
	e.probes = append(e.probes, t)
	e.inProbe++
	e.maxInProbe = max(e.maxInProbe, e.inProbe)
	snap, wait := e.snap, e.probeWait
	e.mu.Unlock()
	if wait != nil {
		<-wait
	}
	e.mu.Lock()
	e.inProbe--
	e.mu.Unlock()
	return snap
}
func (e *fakeEngine) Release(context.Context, engine.Target) error {
	e.mu.Lock()
	e.released++
	e.mu.Unlock()
	return nil
}
func (e *fakeEngine) Restore(context.Context, engine.Target) error {
	e.mu.Lock()
	e.restores++
	wait := e.restoreWait
	e.mu.Unlock()
	if wait != nil {
		<-wait
	}
	return nil
}
func (e *fakeEngine) ChatURL(t engine.Target) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", t.Port)
}
func (e *fakeEngine) AgentCommand(id string, t engine.Target) ([]string, []string) {
	return []string{"/fake/agent", id, fmt.Sprint(t.Port)}, []string{"KEY=" + t.APIKey}
}

// fakeClock moves only when the test advances it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeWaiter{c.now.Add(d), ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if c.now.Before(w.at) {
			kept = append(kept, w)
		} else {
			w.ch <- c.now
		}
	}
	c.waiters = kept
}

// fakeProc is a server process driven by the test.
type fakeProc struct {
	pid    int
	argv   []string
	stdout io.Writer
	stderr io.Writer
	exitOn map[syscall.Signal]int // signals that end the process, with exit codes
	// ignoreKill: SIGKILL does not end it either (a process stuck in the
	// kernel); the test ends it with exit.
	ignoreKill bool

	mu      sync.Mutex
	signals []syscall.Signal
	once    sync.Once
	code    int
	exited  chan struct{}
}

func (p *fakeProc) Pid() int { return p.pid }

func (p *fakeProc) Signal(sig syscall.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, sig)
	p.mu.Unlock()
	if sig == syscall.SIGKILL && !p.ignoreKill {
		p.exit(137)
	} else if code, ok := p.exitOn[sig]; ok {
		p.exit(code)
	}
	return nil
}

func (p *fakeProc) Wait() int { <-p.exited; return p.code }

func (p *fakeProc) exit(code int) {
	p.once.Do(func() { p.code = code; close(p.exited) })
}

func (p *fakeProc) sent() []syscall.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.signals)
}

func (p *fakeProc) say(line string) { _, _ = io.WriteString(p.stdout, line+"\n") }

type fakeStarter struct {
	mu         sync.Mutex
	procs      []*fakeProc
	exitOn     map[syscall.Signal]int
	ignoreKill bool
}

func (st *fakeStarter) Start(argv, _ []string, stdout, stderr io.Writer) (process, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	p := &fakeProc{pid: 1000 + len(st.procs), argv: argv, stdout: stdout, stderr: stderr,
		exitOn: st.exitOn, ignoreKill: st.ignoreKill, exited: make(chan struct{})}
	st.procs = append(st.procs, p)
	return p, nil
}

func (st *fakeStarter) count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.procs)
}

func (st *fakeStarter) last() *fakeProc {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.procs[len(st.procs)-1]
}

type harness struct {
	t       *testing.T
	s       *Supervisor
	eng     *fakeEngine
	clk     *fakeClock
	st      *fakeStarter
	port    int
	brew    func(args []string) (string, error)
	brewOut string   // written to the log by the fake brew
	kills   []string // signals sent to foreign PIDs
	alive   map[int]bool
	mu      sync.Mutex
	term    [][]string
	termErr error    // what OpenTerminal returns
	chosen  []string // OnModel calls, "engine model"
	views   int      // OnChange calls
}

func newHarness(t *testing.T, eng *fakeEngine, others ...engine.Engine) *harness {
	return newHarnessAs(t, eng, eng, others...)
}

// newHarnessAs runs as, an engine built on eng (e.g. with a Downloader).
func newHarnessAs(t *testing.T, eng *fakeEngine, as engine.Engine, others ...engine.Engine) *harness {
	t.Helper()
	return newHarnessWith(t, eng, as, nil, others...)
}

// newHarnessWith is newHarnessAs with opt changing the options first.
func newHarnessWith(t *testing.T, eng *fakeEngine, as engine.Engine, opt func(*Options), others ...engine.Engine) *harness {
	t.Helper()
	h := &harness{t: t, eng: eng, clk: &fakeClock{now: time.Unix(1_800_000_000, 0)},
		st:    &fakeStarter{exitOn: map[syscall.Signal]int{syscall.SIGINT: 0}},
		port:  freeTestPort(t),
		alive: map[int]bool{}}
	h.brew = func([]string) (string, error) { return "", nil }
	h.brewOut = "==> brew output"
	d := deps{
		clock:   h.clk,
		starter: h.st,
		signalPID: func(pid int, sig syscall.Signal) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			if sig == 0 {
				if h.alive[pid] {
					return nil
				}
				return syscall.ESRCH
			}
			h.kills = append(h.kills, fmt.Sprintf("%d:%v", pid, sig))
			return nil
		},
		manual: true,
	}
	eng.set(func(e *fakeEngine) {
		e.run = func(verb string, log io.Writer) (string, error) {
			if log != nil {
				_, _ = io.WriteString(log, h.brewOut+"\n")
			}
			return h.brew([]string{verb, "tap/" + e.ID()})
		}
	})
	o := Options{
		Engine:  as,
		Engines: append([]engine.Engine{as}, others...),
		OnModel: func(e, m string) {
			h.mu.Lock()
			h.chosen = append(h.chosen, e+" "+m)
			h.mu.Unlock()
		},
		Settings: Settings{Config: engine.Config{Model: "m/b", Port: h.port, APIKey: "k"}},
		LogDir:   t.TempDir(),
		OpenTerminal: func(argv, _ []string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.term = append(h.term, argv)
			return h.termErr
		},
		OnChange: func(View) {
			h.mu.Lock()
			h.views++
			h.mu.Unlock()
		},
	}
	if opt != nil {
		opt(&o)
	}
	h.s = newSupervisor(o, d)
	t.Cleanup(h.s.Close)
	h.s.doWait(func() {}) // the initial detection has run
	return h
}

// wait polls the view until ok or fails after a deadline.
func (h *harness) wait(what string, ok func(View) bool) View {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		v := h.s.View()
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s; phase %d, view %+v", what, v.Phase, v)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitPhase waits for phase p. The supervisor shows PhaseLoading before it
// starts the process, so for that phase it also waits for the process.
func (h *harness) waitPhase(p Phase) View {
	h.t.Helper()
	return h.wait(fmt.Sprintf("phase %d", p), func(v View) bool {
		if v.Phase != p {
			return false
		}
		if p != PhaseLoading {
			return true
		}
		h.s.mu.Lock()
		defer h.s.mu.Unlock()
		return h.s.proc != nil
	})
}

func (h *harness) snap(f func(s *engine.Snapshot)) {
	h.eng.set(func(e *fakeEngine) { f(&e.snap) })
}

// step advances the clock by d and runs one tick.
func (h *harness) step(d time.Duration) {
	h.clk.Advance(d)
	h.s.tick()
}

// startReady starts our server and makes it ready.
func (h *harness) startReady() *fakeProc {
	h.t.Helper()
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	p := h.st.last()
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{Reachable: true, Ready: true, Authorized: true} })
	p.say("READY")
	h.waitPhase(PhaseReady)
	return p
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestStartLoadingReadyGenerating(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	v := h.waitPhase(PhaseStopped)
	if v.Model.ID != "m/b" || len(v.Models) != 2 {
		t.Fatalf("model %q, models %v", v.Model.ID, v.Models)
	}
	if v.Address != fmt.Sprintf("127.0.0.1:%d", h.port) {
		t.Errorf("address %q", v.Address)
	}
	h.s.Start()
	h.waitPhase(PhaseLoading)
	p := h.st.last()
	if !slices.Contains(p.argv, "m/b") {
		t.Errorf("argv %v", p.argv)
	}
	// Loading: the server does not answer yet.
	h.step(time.Second)
	if v := h.s.View(); v.Phase != PhaseLoading || v.ChatURL != "" {
		t.Fatalf("phase %d chat %q", v.Phase, v.ChatURL)
	}
	// A probe that says ready ends loading.
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{Reachable: true, Ready: true, Authorized: true} })
	h.step(time.Second)
	v = h.waitPhase(PhaseReady)
	if v.ChatURL == "" {
		t.Error("no chat URL when ready")
	}
	h.snap(func(s *engine.Snapshot) { s.Active = 1 })
	h.step(time.Second)
	h.waitPhase(PhaseGenerating)
	h.snap(func(s *engine.Snapshot) { s.Active = 0; s.Completed = 1 })
	h.step(time.Second)
	h.waitPhase(PhaseReady)
	h.snap(func(s *engine.Snapshot) { s.Released = true })
	h.step(time.Second)
	h.waitPhase(PhaseReleased)
}

func TestReadyFromOutputLine(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
}

func TestUnexpectedExitClassified(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	p := h.st.last()
	p.say("loading weights")
	p.say("error: not enough memory")
	p.exit(1)
	v := h.waitPhase(PhaseError)
	if v.Failure.Kind != engine.FailMemory || v.Failure.Code != 1 {
		t.Errorf("failure %+v", v.Failure)
	}
	if v.LastLine != "error: not enough memory" {
		t.Errorf("last line %q", v.LastLine)
	}
	// Restart from the error starts a new process.
	h.s.Restart()
	h.waitPhase(PhaseLoading)
	if h.st.count() != 2 {
		t.Errorf("starts = %d", h.st.count())
	}
}

func TestPortBusyPreflight(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", h.port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	h.s.Start()
	v := h.waitPhase(PhaseError)
	if v.Failure.Kind != engine.FailPortBusy {
		t.Fatalf("failure %+v", v.Failure)
	}
	if v.FreePort <= h.port {
		t.Errorf("free port %d, busy %d", v.FreePort, h.port)
	}
	if h.st.count() != 0 {
		t.Error("server started on a busy port")
	}
	free := v.FreePort
	h.s.StartOn(free)
	v = h.waitPhase(PhaseLoading)
	if v.Port != free || h.s.Settings().Port != h.port {
		t.Errorf("port %d, settings %d, want %d and %d", v.Port, h.s.Settings().Port, free, h.port)
	}
}

// StartOn is for one run: the settings keep their port, the address chip
// shows the port that runs, and the override alone is no pending restart.
func TestStartOnIsOneRun(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	free := freeTestPort(t)
	h.s.StartOn(free)
	h.waitPhase(PhaseLoading)
	p := h.st.last()
	if !slices.Contains(p.argv, fmt.Sprint(free)) {
		t.Errorf("argv %v", p.argv)
	}
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{Reachable: true, Ready: true, Authorized: true} })
	p.say("READY")
	v := h.waitPhase(PhaseReady)
	if v.Port != free || v.Address != fmt.Sprintf("127.0.0.1:%d", free) {
		t.Errorf("view port %d, address %q, want %d", v.Port, v.Address, free)
	}
	if got := h.s.Settings().Port; got != h.port {
		t.Errorf("settings port %d, want %d", got, h.port)
	}
	if v.PendingRestart || h.s.PendingRestart() {
		t.Error("restart pending only because of the override")
	}
	// Applying the same settings again changes nothing.
	h.s.Apply(h.s.Settings())
	h.s.doWait(func() {})
	if h.s.PendingRestart() {
		t.Error("restart pending after Apply of the same settings")
	}
	// A real port change in the settings still counts.
	st := h.s.Settings()
	st.Port = freeTestPort(t)
	h.s.Apply(st)
	h.wait("pending restart", func(v View) bool { return v.PendingRestart })
	// Back to the user's port: nothing pending again. After a stop the next
	// start uses the settings port, not the override.
	st.Port = h.port
	h.s.Apply(st)
	h.wait("no pending restart", func(v View) bool { return !v.PendingRestart })
	// A restart for another model is the same run: it keeps the port, or the
	// user would meet the port error again.
	h.s.SelectModel("m/a")
	h.wait("restart on the run's port", func(View) bool { return h.st.count() == 2 })
	if p := h.st.last(); !slices.Contains(p.argv, fmt.Sprint(free)) || !slices.Contains(p.argv, "m/a") {
		t.Errorf("restart argv %v, want port %d and model m/a", p.argv, free)
	}
	v = h.waitPhase(PhaseLoading)
	if v.Port != free || v.PendingRestart {
		t.Errorf("after the restart: port %d, pending %v", v.Port, v.PendingRestart)
	}
	h.s.Stop()
	h.waitPhase(PhaseStopped)
	if v := h.s.View(); v.Port != h.port {
		t.Errorf("stopped view port %d, want %d", v.Port, h.port)
	}
	h.s.Start()
	h.wait("third start", func(View) bool { return h.st.count() == 3 })
	if p := h.st.last(); !slices.Contains(p.argv, fmt.Sprint(h.port)) {
		t.Errorf("next start argv %v, want port %d", p.argv, h.port)
	}
}

// An update restarts the server on the port it ran on, override or not.
func TestStartOnSurvivesUpdateRestart(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.brew = func([]string) (string, error) {
		eng.set(func(e *fakeEngine) { e.inst.Version = "1.5.0" })
		return "", nil
	}
	h.waitPhase(PhaseStopped)
	free := freeTestPort(t)
	h.s.StartOn(free)
	h.waitPhase(PhaseLoading)
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{Reachable: true, Ready: true, Authorized: true} })
	h.st.last().say("READY")
	h.waitPhase(PhaseReady)
	h.s.Update()
	h.wait("restart after update", func(v View) bool { return h.st.count() == 2 && v.Phase == PhaseLoading })
	if p := h.st.last(); !slices.Contains(p.argv, fmt.Sprint(free)) {
		t.Errorf("argv after update %v, want port %d", p.argv, free)
	}
	if h.s.PendingRestart() {
		t.Error("restart pending after the update restart")
	}
}

// The "Ready" log line starts a probe at once, so the memory and the
// context are known before the next tick.
func TestReadyLineProbesAtOnce(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	h.snap(func(s *engine.Snapshot) {
		*s = engine.Snapshot{Reachable: true, Ready: true, Authorized: true,
			MemoryUsed: 20e9, MemoryLimit: 39e9, ContextTokens: 262144}
	})
	h.st.last().say("READY") // no tick
	v := h.wait("first snapshot", func(v View) bool { return v.Phase == PhaseReady && v.Snapshot.MemoryLimit > 0 })
	if v.Snapshot.MemoryUsed != 20e9 || v.Snapshot.ContextTokens != 262144 {
		t.Errorf("snapshot %+v", v.Snapshot)
	}
}

// Probes never overlap, and a probe that waits behind another is applied
// after it: the speed meter sees the snapshots in the order they were taken.
func TestProbesDoNotOverlap(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	gate := make(chan struct{})
	var open sync.Once
	release := func() { open.Do(func() { close(gate) }) }
	t.Cleanup(release) // a failed test must not leave the probe hanging
	h.eng.set(func(e *fakeEngine) {
		e.snap = engine.Snapshot{Reachable: true, Ready: true, Authorized: true}
		e.probeWait = gate
	})
	ticked := make(chan struct{})
	go func() { h.step(time.Second); close(ticked) }() // a probe that hangs
	h.wait("probe started", func(View) bool {
		h.eng.mu.Lock()
		defer h.eng.mu.Unlock()
		return h.eng.inProbe == 1
	})
	h.st.last().say("READY") // wants its own probe
	h.s.doWait(func() {})
	time.Sleep(20 * time.Millisecond)
	h.eng.mu.Lock()
	running := h.eng.inProbe
	h.eng.mu.Unlock()
	if running != 1 {
		t.Fatalf("%d probes run at once, want 1", running)
	}
	h.eng.set(func(e *fakeEngine) { e.probeWait = nil })
	release()
	<-ticked
	h.wait("both probes done", func(View) bool {
		h.eng.mu.Lock()
		defer h.eng.mu.Unlock()
		return len(h.eng.probes) == 2 && h.eng.inProbe == 0
	})
	h.eng.mu.Lock()
	most := h.eng.maxInProbe
	h.eng.mu.Unlock()
	if most != 1 {
		t.Errorf("up to %d probes ran at once", most)
	}
}

// Weights on their way back are not "Ready": the engine says restoring
// (and not released), and our own Restore call shows it from the click.
func TestRestoringIsNotReady(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	h.snap(func(s *engine.Snapshot) { s.Released = true; s.MemoryUsed = 2e9; s.MemoryLimit = 39e9 })
	h.step(time.Second)
	h.waitPhase(PhaseReleased)

	// Our Restore call: restoring at once, although the last probe still
	// says released.
	gate := make(chan struct{})
	var open sync.Once
	release := func() { open.Do(func() { close(gate) }) }
	t.Cleanup(release)
	h.eng.set(func(e *fakeEngine) { e.restoreWait = gate })
	h.s.Restore()
	v := h.waitPhase(PhaseRestoring)
	if !v.Phase.Served() || v.Snapshot.MemoryLimit == 0 {
		t.Errorf("restoring view: served %v, snapshot %+v", v.Phase.Served(), v.Snapshot)
	}
	h.step(time.Second) // a probe in between still says released
	if got := h.s.View().Phase; got != PhaseRestoring {
		t.Errorf("phase %d after a stale probe, want restoring", got)
	}
	// The engine reports restoring; Released is false then.
	h.snap(func(s *engine.Snapshot) { s.Released = false; s.Restoring = true })
	release()
	h.wait("restore call ended", func(View) bool {
		h.eng.mu.Lock()
		defer h.eng.mu.Unlock()
		return h.eng.restores == 1
	})
	h.step(time.Second)
	if got := h.waitPhase(PhaseRestoring); got.IdleLeft != 0 {
		t.Errorf("idle countdown while restoring: %v", got.IdleLeft)
	}
	// Weights are back.
	h.snap(func(s *engine.Snapshot) { s.Restoring = false; s.MemoryUsed = 20e9 })
	h.step(time.Second)
	if v := h.waitPhase(PhaseReady); v.Snapshot.MemoryUsed != 20e9 {
		t.Errorf("snapshot %+v", v.Snapshot)
	}
}

// A request to a released server restores the weights without Lumilo's
// Restore: only the engine's flag tells.
func TestRestoringFromEngineFlag(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	h.snap(func(s *engine.Snapshot) { s.Released = true })
	h.step(time.Second)
	h.waitPhase(PhaseReleased)
	h.snap(func(s *engine.Snapshot) { s.Released, s.Restoring, s.Active = false, true, 1 })
	h.step(time.Second)
	h.waitPhase(PhaseRestoring)
	h.snap(func(s *engine.Snapshot) { s.Restoring = false })
	h.step(time.Second)
	h.waitPhase(PhaseGenerating)
}

func TestAlreadyServingPreflight(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: 77, Port: h.port, Model: "m/x"}} })
	h.s.Start()
	v := h.waitPhase(PhaseExternal)
	if v.External == nil || v.External.PID != 77 || h.st.count() != 0 {
		t.Fatalf("external %+v, starts %d", v.External, h.st.count())
	}
}

func TestAlreadyServingExit(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	p := h.st.last()
	h.eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: p.pid, Port: h.port}, {PID: 78, Port: h.port}} })
	p.say("error: Fake is already serving")
	p.exit(1)
	v := h.waitPhase(PhaseExternal)
	if v.External.PID != 78 {
		t.Errorf("external %+v; our own PID must be skipped", v.External)
	}
}

func TestUnresponsiveOnlyAfterReady(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	for range 20 { // a long load never counts as a hang
		h.step(time.Second)
	}
	if v := h.s.View(); v.Phase != PhaseLoading {
		t.Fatalf("phase %d while loading", v.Phase)
	}
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{Reachable: true, Ready: true, Authorized: true} })
	h.step(time.Second)
	h.waitPhase(PhaseReady)
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{} })
	for range 11 {
		h.step(time.Second)
	}
	if v := h.s.View(); v.Phase != PhaseReady {
		t.Fatalf("phase %d after 11 s", v.Phase)
	}
	h.step(time.Second)
	h.waitPhase(PhaseUnresponsive)
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{Reachable: true, Ready: true, Authorized: true} })
	h.step(time.Second)
	h.waitPhase(PhaseReady)

	// Hang again, then force quit from Unresponsive.
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{} })
	h.step(13 * time.Second)
	h.waitPhase(PhaseUnresponsive)
	p := h.st.last()
	h.s.ForceStop()
	h.waitPhase(PhaseStopping)
	h.wait("force signal", func(View) bool { return slices.Contains(p.sent(), syscall.SIGTERM) })
	h.step(5 * time.Second)
	h.waitPhase(PhaseStopped)
	if got := p.sent(); !slices.Equal(got, []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}) {
		t.Errorf("signals %v", got)
	}
}

func TestBrokenServerIsError(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	p := h.startReady()
	p.say("Engine stopped · failed 3 times")
	h.snap(func(s *engine.Snapshot) { s.Broken = true })
	h.step(time.Second)
	v := h.waitPhase(PhaseError)
	if v.Failure.Kind != engine.FailExit || v.Failure.Line != "Engine stopped · failed 3 times" {
		t.Errorf("failure %+v", v.Failure)
	}
	// Start stops the live broken server and starts a new one.
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{} })
	h.s.Start()
	h.waitPhase(PhaseLoading)
	if h.st.count() != 2 {
		t.Errorf("starts = %d", h.st.count())
	}
}

func TestStopStoppingLongForce(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.st.exitOn = map[syscall.Signal]int{} // ignores SIGINT and SIGTERM
	p := h.startReady()
	h.s.Stop()
	h.waitPhase(PhaseStopping)
	h.step(11 * time.Second)
	if h.s.View().StoppingLong {
		t.Fatal("StoppingLong too early")
	}
	h.step(time.Second)
	h.wait("stopping long", func(v View) bool { return v.StoppingLong && v.Phase == PhaseStopping })
	h.s.ForceStop()
	h.wait("force", func(View) bool { return len(p.sent()) == 2 })
	h.step(4 * time.Second)
	if h.s.View().Phase != PhaseStopping {
		t.Fatal("killed before KillWait")
	}
	h.step(time.Second)
	v := h.waitPhase(PhaseStopped)
	if v.StoppingLong {
		t.Error("StoppingLong kept after stop")
	}
	if got := p.sent(); !slices.Equal(got, []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL}) {
		t.Errorf("signals %v", got)
	}
}

func TestExternal(t *testing.T) {
	eng := newFakeEngine()
	eng.running = []engine.Running{{PID: 50, Port: 9999}, {PID: 51, Port: 0, Model: "m/x"}}
	h := newHarness(t, eng)
	// Prefer the server on the configured port.
	h.eng.set(func(e *fakeEngine) { e.running[1].Port = h.port })
	h.s.Recheck()
	v := h.wait("external on our port", func(v View) bool {
		return v.Phase == PhaseExternal && v.External.PID == 51
	})
	if v.Address != fmt.Sprintf("127.0.0.1:%d", h.port) || v.Access != engine.AccessLocal || v.Model.ID != "m/x" {
		t.Errorf("address %q access %d", v.Address, v.Access)
	}
	// Unauthorized metrics are not an error.
	h.snap(func(s *engine.Snapshot) { *s = engine.Snapshot{Reachable: true, Ready: true} })
	h.step(time.Second)
	if v := h.s.View(); v.Phase != PhaseExternal || !v.Snapshot.Reachable {
		t.Fatalf("phase %d snapshot %+v", v.Phase, v.Snapshot)
	}
	h.eng.mu.Lock()
	last := h.eng.probes[len(h.eng.probes)-1]
	h.eng.mu.Unlock()
	if last.Port != h.port || last.APIKey != "k" {
		t.Errorf("probe target %+v", last)
	}
	// It disappears.
	h.eng.set(func(e *fakeEngine) { e.running = e.running[:1] })
	h.step(2 * time.Second)
	h.waitPhase(PhaseStopped)
}

func TestExternalAppearsWhileStopped(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: 70, Port: h.port}} })
	h.step(2 * time.Second)
	v := h.wait("external adopted", func(v View) bool { return v.Phase == PhaseExternal })
	if v.External.PID != 70 {
		t.Errorf("external %+v", v.External)
	}
}

func TestKillExternal(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.eng.set(func(e *fakeEngine) { e.running = []engine.Running{{PID: 60, Port: h.port}} })
	h.s.Recheck()
	h.waitPhase(PhaseExternal)
	h.mu.Lock()
	h.alive[60] = true
	h.mu.Unlock()
	h.s.KillExternal()
	h.waitPhase(PhaseStopped)
	h.step(5 * time.Second)
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Equal(h.kills, []string{"60:interrupt", "60:killed"}) {
		t.Errorf("kills %v", h.kills)
	}
}

func TestSpeedMeter(t *testing.T) {
	var m speedMeter
	m.reset(true)
	t0 := time.Unix(0, 0)
	snap := engine.Snapshot{Reachable: true, Authorized: true}
	at := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Second) }

	// Generating: Current is the live rate between probes.
	snap.Active, snap.OutputTokens, snap.DecodeTime = 1, 50, time.Second
	m.add(snap, at(1))
	if m.speed.Current != 50 || m.speed.Tokens != 50 || len(m.speed.History) != 0 {
		t.Fatalf("current %v tokens %v history %v", m.speed.Current, m.speed.Tokens, m.speed.History)
	}
	// Tokens count up while the request runs.
	snap.OutputTokens, snap.DecodeTime = 120, 2*time.Second
	m.add(snap, at(1))
	if m.speed.Tokens != 120 {
		t.Fatalf("tokens %v, want 120", m.speed.Tokens)
	}
	// Finished: one entry of Δtokens/Δdecode.
	snap.Active, snap.Completed, snap.OutputTokens, snap.DecodeTime = 0, 1, 150, 3*time.Second
	if !m.add(snap, at(2)) {
		t.Fatal("completion not reported")
	}
	if !slices.Equal(m.speed.History, []float64{50}) || m.speed.Current != 0 || m.speed.Tokens != 0 || !m.speed.LastRequest.Equal(at(2)) {
		t.Fatalf("speed %+v", m.speed)
	}
	// Two requests finished between probes: two entries of the same rate.
	snap.Completed, snap.OutputTokens, snap.DecodeTime = 3, 350, 4*time.Second
	m.add(snap, at(3))
	if !slices.Equal(m.speed.History, []float64{50, 200, 200}) {
		t.Fatalf("history %v", m.speed.History)
	}
	// The 1-token Restore ping is not a speed sample.
	snap.Completed, snap.OutputTokens, snap.DecodeTime = 4, 351, 5*time.Second
	m.add(snap, at(4))
	if len(m.speed.History) != 3 || !m.speed.LastRequest.Equal(at(4)) {
		t.Fatalf("history %v last %v", m.speed.History, m.speed.LastRequest)
	}
	// Unauthorized probes do not move the baseline.
	m.add(engine.Snapshot{Reachable: true}, at(5))
	// History keeps 20, Average is the mean of the last 10.
	for i := range 25 {
		snap.Completed++
		snap.OutputTokens += int64(10 * (i + 1))
		snap.DecodeTime += time.Second
		m.add(snap, at(6+i))
	}
	if len(m.speed.History) != historyMax || m.speed.History[historyMax-1] != 250 {
		t.Fatalf("history %v", m.speed.History)
	}
	if m.speed.Average != 205 { // mean of 160..250
		t.Errorf("average %v", m.speed.Average)
	}
	// Read and FirstToken come from the snapshot.
	snap.PrefillTokens, snap.PrefillTime, snap.FirstToken = 1000, 2*time.Second, 300*time.Millisecond
	m.add(snap, at(40))
	if m.speed.Read != 500 || m.speed.FirstToken != 300*time.Millisecond {
		t.Errorf("read %v first %v", m.speed.Read, m.speed.FirstToken)
	}
	// An external server: the first probe is only a baseline.
	m.reset(false)
	m.add(snap, at(41))
	if len(m.speed.History) != 0 {
		t.Errorf("history from baseline %v", m.speed.History)
	}
}

// A request's history entry is its speed over all of it, the tail of the
// last interval included; Current spans the last three intervals.
func TestSpeedPerRequest(t *testing.T) {
	var m speedMeter
	m.reset(true)
	t0 := time.Unix(0, 0)
	at := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Second) }
	snap := engine.Snapshot{Reachable: true, Authorized: true}
	m.add(snap, at(0)) // idle
	// Bursts of a draft model: 120, 40, 140, 60 tokens a second.
	for i, tok := range []int64{120, 160, 300, 360} {
		snap.Active, snap.OutputTokens, snap.DecodeTime = 1, tok, time.Duration(i+1)*time.Second
		m.add(snap, at(i+1))
	}
	if m.speed.Current != 80 { // (40+140+60)/3
		t.Errorf("current %v, want the last three seconds' 80", m.speed.Current)
	}
	// It ends 0.5 s into the next interval with 30 tokens more: 390 in 4.5 s.
	snap.Active, snap.Completed, snap.OutputTokens, snap.DecodeTime = 0, 1, 390, 4500*time.Millisecond
	m.add(snap, at(5))
	if len(m.speed.History) != 1 || m.speed.History[0] != 390/4.5 || m.speed.Current != 0 {
		t.Errorf("history %v current %v, want one entry of %v", m.speed.History, m.speed.Current, 390/4.5)
	}
	// An idle probe moves the start: the next request counts from it.
	m.add(snap, at(6))
	snap.Completed, snap.OutputTokens, snap.DecodeTime = 2, 490, 5500*time.Millisecond
	m.add(snap, at(7))
	if len(m.speed.History) != 2 || m.speed.History[1] != 100 {
		t.Errorf("history %v", m.speed.History)
	}
}

// Tokens seen without decode time (the first tokens in flight, before an
// engine's clock runs) and a stretch shorter than liveMin give no live
// speed: the menu bar once read over 3,000 tok/s from 1,300 such tokens
// and a 0.4 s tail.
func TestSpeedLiveNoSpike(t *testing.T) {
	var m speedMeter
	m.reset(true)
	t0 := time.Unix(0, 0)
	at := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Second) }
	snap := engine.Snapshot{Reachable: true, Authorized: true}
	m.add(snap, at(0))
	snap.Active, snap.OutputTokens = 1, 1300 // first seen: no time yet
	m.add(snap, at(1))
	snap.OutputTokens, snap.DecodeTime = 1330, 400*time.Millisecond
	m.add(snap, at(2))
	if m.speed.Current != 0 {
		t.Errorf("current %v after 0.4 s", m.speed.Current)
	}
	snap.OutputTokens, snap.DecodeTime = 1380, 1400*time.Millisecond
	m.add(snap, at(3))
	if m.speed.Current != 80/1.4 {
		t.Errorf("current %v, want %v", m.speed.Current, 80/1.4)
	}
}

func TestSpeedThroughProbes(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	h.snap(func(s *engine.Snapshot) { s.Completed, s.OutputTokens, s.DecodeTime = 2, 100, 2*time.Second })
	h.step(time.Second)
	v := h.s.View()
	if !slices.Equal(v.Speed.History, []float64{50, 50}) || v.Speed.Average != 50 {
		t.Errorf("speed %+v", v.Speed)
	}
}

func TestIdleCountdown(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	h.snap(func(s *engine.Snapshot) { s.IdleRelease = 10 * time.Minute })
	h.step(3 * time.Minute)
	if got := h.s.View().IdleLeft; got != 7*time.Minute {
		t.Fatalf("idle left %v", got)
	}
	h.snap(func(s *engine.Snapshot) { s.Completed, s.OutputTokens, s.DecodeTime = 1, 10, time.Second })
	h.step(time.Second)
	if got := h.s.View().IdleLeft; got != 10*time.Minute {
		t.Fatalf("idle left after a request %v", got)
	}
	h.step(11 * time.Minute)
	if got := h.s.View().IdleLeft; got != 0 {
		t.Fatalf("idle left past the limit %v", got)
	}
	h.snap(func(s *engine.Snapshot) { s.Released = true })
	h.step(time.Second)
	if v := h.s.View(); v.IdleLeft != 0 || v.Phase != PhaseReleased {
		t.Fatalf("released: idle %v phase %d", v.IdleLeft, v.Phase)
	}
}

func TestCheckUpdate(t *testing.T) {
	eng := newFakeEngine()
	eng.latest = "1.4.10"
	h := newHarness(t, eng)
	h.wait("detected", func(v View) bool { return v.Version != "" })
	h.step(9 * time.Second)
	if h.s.View().Latest != "" {
		t.Fatal("checked before 10 s")
	}
	h.step(time.Second)
	v := h.wait("update available", func(v View) bool { return v.Update == UpdateAvailable })
	if v.Latest != "1.4.10" {
		t.Errorf("latest %q", v.Latest)
	}
	if compareVersions("1.4.9", "1.4.10") >= 0 || compareVersions("1.4", "1.4.0") != 0 || compareVersions("v2", "1.9.9") <= 0 {
		t.Error("compareVersions")
	}
}

// A check the user asks for reports that it runs and how it ended: up to
// date, a newer version, or a feed that failed.
func TestCheckUpdateByUser(t *testing.T) {
	for _, tc := range []struct {
		name   string
		latest string
		err    error
		want   CheckResult
		update UpdatePhase
	}{
		{"up to date", "1.4.0", nil, CheckUpToDate, UpdateNone},
		{"newer", "1.4.1", nil, CheckAvailable, UpdateAvailable},
		{"feed error", "", errors.New("503"), CheckFailed, UpdateNone},
		{"no version", "", nil, CheckFailed, UpdateNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng := newFakeEngine()
			gate := make(chan struct{})
			eng.latest, eng.feedErr, eng.feedWait = tc.latest, tc.err, gate
			h := newHarness(t, eng)
			h.wait("detected", func(v View) bool { return v.Version != "" })
			h.s.CheckUpdate()
			v := h.s.View()
			if !v.Checking || v.Check != CheckNone || v.CheckSeq != 1 {
				t.Fatalf("while checking: checking %v, check %v, seq %d", v.Checking, v.Check, v.CheckSeq)
			}
			// A second click while the check runs starts no other.
			h.s.CheckUpdate()
			if v := h.s.View(); v.CheckSeq != 1 {
				t.Errorf("a second check started: seq %d", v.CheckSeq)
			}
			close(gate)
			v = h.wait("check done", func(v View) bool { return !v.Checking })
			if v.Check != tc.want || v.Update != tc.update || v.CheckSeq != 1 {
				t.Errorf("check %v, update %v, seq %d; want %v, %v", v.Check, v.Update, v.CheckSeq, tc.want, tc.update)
			}
			if tc.err == nil && v.Latest != tc.latest {
				t.Errorf("latest %q, want %q", v.Latest, tc.latest)
			}
		})
	}
}

// The daily check stays silent: a failed one shows nowhere, and none of
// them touches the result of the user's check.
func TestCheckUpdateDailySilent(t *testing.T) {
	eng := newFakeEngine()
	eng.latest = "1.4.0"
	h := newHarness(t, eng)
	h.wait("detected", func(v View) bool { return v.Version != "" })
	h.s.CheckUpdate()
	h.wait("user check done", func(v View) bool { return v.Check == CheckUpToDate })
	eng.set(func(e *fakeEngine) { e.feedErr = errors.New("offline") })
	h.step(10 * time.Second) // the first daily check is due
	h.wait("daily check ran", func(View) bool {
		var n int
		eng.set(func(e *fakeEngine) { n = e.feeds })
		return n == 2
	})
	// The next one finds a newer version: Latest and Update follow it,
	// the user's result stays.
	eng.set(func(e *fakeEngine) { e.feedErr, e.latest = nil, "1.4.5" })
	h.step(updateCheckInterval)
	v := h.wait("second daily check", func(v View) bool { return v.Latest == "1.4.5" })
	if v.Checking || v.Check != CheckUpToDate || v.CheckSeq != 1 || v.Update != UpdateAvailable {
		t.Errorf("after daily checks: checking %v, check %v, seq %d, update %v", v.Checking, v.Check, v.CheckSeq, v.Update)
	}
}

// A check asked for without the engine installed fails at once.
func TestCheckUpdateNotInstalled(t *testing.T) {
	eng := newFakeEngine()
	eng.inst = engine.Install{Ready: true}
	h := newHarness(t, eng)
	h.s.CheckUpdate()
	if v := h.wait("check done", func(v View) bool { return !v.Checking }); v.Check != CheckFailed {
		t.Errorf("check %v", v.Check)
	}
}

func TestUpdateSuccess(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	var args []string
	h.brew = func(a []string) (string, error) {
		args = a
		eng.set(func(e *fakeEngine) { e.inst.Version = "1.5.0" })
		return "Upgraded", nil
	}
	old := h.startReady()
	h.s.Update()
	v := h.wait("restarted after update", func(v View) bool {
		return v.Update == UpdateNone && v.Version == "1.5.0" && v.Phase == PhaseLoading
	})
	if !slices.Equal(args, []string{"upgrade", "tap/fake"}) {
		t.Errorf("brew args %v", args)
	}
	if h.st.count() != 2 || !slices.Equal(old.sent(), []syscall.Signal{syscall.SIGINT}) {
		t.Errorf("starts %d, old signals %v", h.st.count(), old.sent())
	}
	if v.Latest != "" && v.Update != UpdateNone {
		t.Errorf("update %d", v.Update)
	}
}

func TestUpdateUnchangedFails(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.brew = func([]string) (string, error) { return "Warning: fake 1.4.0 already installed", nil }
	h.startReady()
	h.s.Update()
	v := h.wait("update failed", func(v View) bool { return v.Update == UpdateFailed && v.Phase == PhaseLoading })
	if v.UpdateError != "Warning: fake 1.4.0 already installed" || v.Version != "1.4.0" {
		t.Errorf("error %q version %q", v.UpdateError, v.Version)
	}
	if v.UpdateReason != UpdateReasonNoNewer {
		t.Errorf("reason %d, want UpdateReasonNoNewer", v.UpdateReason)
	}
	if h.st.count() != 2 {
		t.Errorf("old server not restarted: starts %d", h.st.count())
	}
	// The restart keeps the brew output for the Log button.
	h.st.last().say("after update")
	if log := readFile(t, v.LogPath); !strings.Contains(log, "==> brew output") || !strings.Contains(log, "after update") {
		t.Errorf("log %q", log)
	}
	// A user start truncates the log.
	h.s.Stop()
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	h.st.last().say("fresh")
	if log := readFile(t, v.LogPath); log != "fresh\n" {
		t.Errorf("log after start %q", log)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateWhenStoppedDoesNotStart(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.brew = func([]string) (string, error) { return "", errors.New("exit status 1") }
	h.s.Update()
	v := h.wait("update failed", func(v View) bool { return v.Update == UpdateFailed })
	if v.UpdateError != "exit status 1" || h.st.count() != 0 {
		t.Errorf("error %q starts %d", v.UpdateError, h.st.count())
	}
}

func TestInstall(t *testing.T) {
	eng := newFakeEngine()
	eng.inst = engine.Install{Ready: true}
	h := newHarness(t, eng)
	h.waitPhase(PhaseNotInstalled)
	h.brew = func([]string) (string, error) { return "Error: no bottle", errors.New("exit status 1") }
	h.s.Install()
	v := h.waitPhase(PhaseInstallFailed)
	if v.UpdateError != "Error: no bottle" {
		t.Errorf("error %q", v.UpdateError)
	}
	// Retry succeeds.
	h.brew = func([]string) (string, error) {
		eng.set(func(e *fakeEngine) { e.inst.Installed, e.inst.Version = true, "1.4.0" })
		return "", nil
	}
	h.s.Install()
	v = h.waitPhase(PhaseStopped)
	if v.Version != "1.4.0" || v.UpdateError != "" {
		t.Errorf("version %q error %q", v.Version, v.UpdateError)
	}
}

func TestNoBrew(t *testing.T) {
	eng := newFakeEngine()
	eng.inst = engine.Install{}
	h := newHarness(t, eng)
	h.waitPhase(PhaseNoInstaller)
	eng.set(func(e *fakeEngine) { e.inst = engine.Install{Ready: true} })
	h.s.Recheck()
	h.waitPhase(PhaseNotInstalled)
}

func TestRunAgentWaitsForReady(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.RunAgent("claude")
	h.waitPhase(PhaseLoading)
	h.step(time.Second)
	h.mu.Lock()
	early := len(h.term)
	h.mu.Unlock()
	if early != 0 {
		t.Fatal("agent opened before ready")
	}
	h.st.last().say("READY")
	h.wait("agent opened", func(View) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.term) == 1
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Equal(h.term[0], []string{"/fake/agent", "claude", fmt.Sprint(h.port)}) {
		t.Errorf("agent argv %v", h.term[0])
	}
}

func TestRunAgentGivesUpOnError(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.RunAgent("claude")
	h.waitPhase(PhaseLoading)
	h.st.last().exit(1)
	h.waitPhase(PhaseError)
	h.s.Close() // waits for the agent goroutine
	if len(h.term) != 0 {
		t.Error("agent opened after a failed start")
	}
}

func TestSelectModelAndPendingRestart(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	st := h.s.Settings()
	st.IdleRelease = time.Minute
	h.s.Apply(st)
	h.wait("pending restart", func(v View) bool { return v.PendingRestart })
	if !h.s.PendingRestart() {
		t.Error("PendingRestart false")
	}
	h.s.SelectModel("m/a")
	h.wait("restart with the new model", func(v View) bool { return v.Phase == PhaseLoading && h.st.count() == 2 })
	if p := h.st.last(); !slices.Contains(p.argv, "m/a") {
		t.Errorf("argv %v", p.argv)
	}
	if h.s.PendingRestart() {
		t.Error("PendingRestart after restart")
	}
}

// The vision and draft choices of the running model need a restart; those
// of another model do not.
func TestModelOptionsPendingRestart(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.startReady()
	st := h.s.Settings()
	st.Options = map[ModelKey]engine.ModelOptions{{"fake", "other/model"}: {NoVision: true}}
	h.s.Apply(st)
	if h.s.PendingRestart() {
		t.Error("another model's options need a restart")
	}
	st.Options = map[ModelKey]engine.ModelOptions{{"fake", st.Model}: {Draft: "mtp"}}
	h.s.Apply(st)
	h.wait("pending restart", func(v View) bool { return v.PendingRestart })
	h.s.mu.Lock()
	cfg := h.s.resolvedLocked()
	h.s.mu.Unlock()
	if cfg.Draft != "mtp" || cfg.NoVision {
		t.Errorf("config %+v", cfg)
	}
}

func TestCloseStopsChild(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	p := h.startReady()
	h.s.Close()
	if got := p.sent(); !slices.Equal(got, []syscall.Signal{syscall.SIGINT}) {
		t.Errorf("signals %v", got)
	}
	select {
	case <-p.exited:
	default:
		t.Error("child still running")
	}
	h.s.Start() // no-op after Close
	if h.st.count() != 1 {
		t.Errorf("starts %d", h.st.count())
	}
}

func TestCloseForcesStuckChild(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.st.exitOn = map[syscall.Signal]int{}
	p := h.startReady()
	closed := make(chan struct{})
	go func() { h.s.Close(); close(closed) }()
	h.wait("graceful signal", func(View) bool { return len(p.sent()) == 1 })
	// Let the close wait and the kill wait run out.
	for !slices.Contains(p.sent(), syscall.SIGKILL) {
		h.clk.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
	<-closed
	if got := p.sent(); !slices.Equal(got, []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL}) {
		t.Errorf("signals %v", got)
	}
}

func TestOnChangePublishes(t *testing.T) {
	eng := newFakeEngine()
	got := make(chan View, 100)
	s := newSupervisor(Options{Engine: eng, OnChange: func(v View) {
		select {
		case got <- v:
		default:
		}
	}}, deps{clock: &fakeClock{}, starter: &fakeStarter{}, manual: true})
	defer s.Close()
	select {
	case v := <-got:
		if v.EngineID != "fake" {
			t.Errorf("view %+v", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no OnChange")
	}
}

func TestBindAndShownHost(t *testing.T) {
	for _, c := range []struct {
		a          engine.Access
		lan        string
		bind, show string
	}{
		{engine.AccessLocal, "192.168.1.20", "127.0.0.1", "127.0.0.1"},
		{engine.AccessNetwork, "192.168.1.20", "0.0.0.0", "192.168.1.20"},
		{engine.AccessNetwork, "", "0.0.0.0", "0.0.0.0"},
	} {
		if got := bindHost(c.a); got != c.bind {
			t.Errorf("%d: bind %q, want %q", c.a, got, c.bind)
		}
		if got := shownHost(c.a, c.lan); got != c.show {
			t.Errorf("%d %q: shown %q, want %q", c.a, c.lan, got, c.show)
		}
	}
}

// In the network access the server listens on every interface and the view
// shows the address other devices use: the LAN address, or 0.0.0.0 when it
// is not known. Our own probes dial the loopback address either way.
func TestNetworkAccessAddress(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	for _, lan := range []string{"192.168.1.20", ""} {
		st := h.s.Settings()
		st.Access, st.LANAddress = engine.AccessNetwork, lan
		h.s.Apply(st)
		host := lan
		if host == "" {
			host = "0.0.0.0"
		}
		want := fmt.Sprintf("%s:%d", host, h.port)
		h.wait("network address "+want, func(v View) bool { return v.Access == engine.AccessNetwork && v.Address == want })
		h.s.mu.Lock()
		bind := h.s.resolvedLocked().Host
		h.s.mu.Unlock()
		if bind != "0.0.0.0" {
			t.Errorf("lan %q: bind %q, want 0.0.0.0", lan, bind)
		}
	}
}
