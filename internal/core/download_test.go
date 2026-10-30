package core

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// dlEngine is the fake engine with a Downloader the test drives: each
// Download hands its request and progress to the test and waits for the
// result it is given.
type dlEngine struct {
	*fakeEngine
	calls chan *dlCall

	mu      sync.Mutex
	dropped []string // DropPartial calls
}

type dlCall struct {
	req      engine.DownloadRequest
	progress func(done, total int64)
	ctx      context.Context
	result   chan dlResult
}

type dlResult struct {
	id  string
	err error
}

func newDLEngine() *dlEngine {
	return &dlEngine{fakeEngine: newFakeEngine(), calls: make(chan *dlCall, 4)}
}

func (e *dlEngine) Download(ctx context.Context, req engine.DownloadRequest, progress func(done, total int64)) (string, error) {
	c := &dlCall{req: req, progress: progress, ctx: ctx, result: make(chan dlResult, 1)}
	e.calls <- c
	select {
	case r := <-c.result:
		return r.id, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (e *dlEngine) DropPartial(model string, _ time.Time) {
	e.mu.Lock()
	e.dropped = append(e.dropped, model)
	e.mu.Unlock()
}

func (e *dlEngine) droppedList() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.dropped)
}

func (e *dlEngine) next(t *testing.T) *dlCall {
	t.Helper()
	select {
	case c := <-e.calls:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("no Download call")
		return nil
	}
}

func job0(v View) DownloadJob {
	if len(v.Downloads) == 0 {
		return DownloadJob{State: -1}
	}
	return v.Downloads[0]
}

// A download runs apart from the server: the running server keeps its
// model and phase, the job reports its bytes, and once done the model
// joins the list without becoming the selected one.
func TestDownloadJobSucceeds(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.startReady()
	starts := h.st.count()

	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{})
	c := eng.next(t)
	if c.req != (engine.DownloadRequest{Model: "hf/new:Q4"}) {
		t.Errorf("request %+v", c.req)
	}
	c.progress(400, 1000)
	v := h.wait("progress", func(v View) bool { return job0(v).Done == 400 })
	j := job0(v)
	if j.State != DownloadRunning || j.Total != 1000 || j.Model.Name != "new" || j.Model.Variant != "Q4" {
		t.Errorf("job %+v", j)
	}
	if v.Phase != PhaseReady || v.Downloading || v.Download != "" {
		t.Errorf("the server took part: phase %d, %+v", v.Phase, v)
	}

	// One at a time: another download queues behind this one.
	h.s.DownloadModel("hf/other:Q8", engine.ModelOptions{})
	h.s.doWait(func() {})
	if v := h.s.View(); len(v.Downloads) != 2 || v.Downloads[1].State != DownloadQueued {
		t.Errorf("the second download did not queue: %+v", v.Downloads)
	}
	select {
	case c := <-eng.calls:
		t.Errorf("a second download started: %+v", c.req)
	default:
	}

	eng.set(func(e *fakeEngine) {
		e.models = append(e.models, engine.Model{ID: "/cache/new-Q4.gguf", Name: "new"})
	})
	c.result <- dlResult{id: "/cache/new-Q4.gguf"}
	v = h.wait("done", func(v View) bool { return job0(v).State == DownloadDone })
	if j := job0(v); j.Installed != "/cache/new-Q4.gguf" || j.Done != 1000 {
		t.Errorf("done job %+v", j)
	}
	if len(v.Models) != 3 || v.Model.ID != "m/b" || v.Phase != PhaseReady || v.PendingRestart {
		t.Errorf("after download: models %v, model %s, phase %d", v.Models, v.Model.ID, v.Phase)
	}
	// The queued one starts once the first is done.
	if c := eng.next(t); c.req.Model != "hf/other:Q8" {
		t.Errorf("next %+v", c.req)
	}
	h.s.CancelDownload("hf/other:Q8")
	h.wait("second gone", func(v View) bool { return len(v.Downloads) == 1 })
	eng.mu.Lock()
	eng.dropped = nil
	eng.mu.Unlock()
	if n := h.st.count(); n != starts {
		t.Errorf("the server restarted: %d starts", n)
	}

	h.s.DismissDownload("hf/new:Q4")
	h.wait("dismissed", func(v View) bool { return len(v.Downloads) == 0 })
	if d := eng.droppedList(); len(d) != 0 {
		t.Errorf("a finished download's files were dropped: %v", d)
	}
}

// With no model at all, the downloaded one becomes the selected one.
func TestDownloadJobFirstModel(t *testing.T) {
	eng := newDLEngine()
	eng.models = nil
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.s.mu.Lock()
	h.s.settings.Model = ""
	h.s.mu.Unlock()
	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{NoVision: true})
	c := eng.next(t)
	if !c.req.NoVision {
		t.Errorf("request %+v", c.req)
	}
	eng.set(func(e *fakeEngine) { e.models = []engine.Model{{ID: "hf/new:Q4"}} })
	c.result <- dlResult{id: "hf/new:Q4"}
	v := h.wait("done", func(v View) bool { return job0(v).State == DownloadDone })
	if v.Model.ID != "hf/new:Q4" || job0(v).Options != (engine.ModelOptions{NoVision: true}) {
		t.Errorf("model %q, job %+v", v.Model.ID, job0(v))
	}
}

// Cancel stops the engine's download, drops its partial files and the job.
func TestDownloadJobCancel(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{})
	c := eng.next(t)
	c.progress(10, 1000)
	h.wait("running", func(v View) bool { return job0(v).Done == 10 })
	h.s.CancelDownload("hf/new:Q4")
	h.wait("gone", func(v View) bool { return len(v.Downloads) == 0 })
	if c.ctx.Err() == nil {
		t.Error("the download's context was not cancelled")
	}
	if d := eng.droppedList(); !slices.Equal(d, []string{"hf/new:Q4"}) {
		t.Errorf("DropPartial %v", d)
	}
	// Another can start now.
	h.s.DownloadModel("hf/other:Q8", engine.ModelOptions{})
	if c := eng.next(t); c.req.Model != "hf/other:Q8" {
		t.Errorf("next %+v", c.req)
	}
}

// A failure stays on the job with the engine's reason; a retry with other
// options replaces it, and dismissing a failed job drops its files.
func TestDownloadJobFailure(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{})
	c := eng.next(t)
	c.result <- dlResult{err: engine.Failure{Kind: engine.FailNoVision, Code: 1, Line: "error: no vision projector"}}
	v := h.wait("failed", func(v View) bool { return job0(v).State == DownloadFailed })
	if f := job0(v).Failure; f.Kind != engine.FailNoVision || f.Line != "error: no vision projector" || f.Offline {
		t.Errorf("failure %+v", f)
	}
	if v.Phase != PhaseStopped {
		t.Errorf("phase %d", v.Phase)
	}

	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{NoVision: true})
	c = eng.next(t)
	if !c.req.NoVision {
		t.Errorf("retry %+v", c.req)
	}
	v = h.wait("retrying", func(v View) bool { return job0(v).State == DownloadRunning })
	if len(v.Downloads) != 1 {
		t.Errorf("jobs %+v", v.Downloads)
	}
	c.result <- dlResult{err: context.DeadlineExceeded} // not a Failure: its text
	v = h.wait("failed again", func(v View) bool { return job0(v).State == DownloadFailed })
	if f := job0(v).Failure; f.Kind != engine.FailExit || f.Line != context.DeadlineExceeded.Error() {
		t.Errorf("failure %+v", f)
	}
	h.s.DismissDownload("hf/new:Q4")
	h.wait("dismissed", func(v View) bool { return len(v.Downloads) == 0 })
	deadline := time.Now().Add(3 * time.Second)
	for len(eng.droppedList()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if d := eng.droppedList(); !slices.Equal(d, []string{"hf/new:Q4"}) {
		t.Errorf("DropPartial %v", d)
	}
}

// A download the engine then does not list is a failure, not a lost row.
func TestDownloadJobNotListed(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{})
	eng.next(t).result <- dlResult{id: "/cache/elsewhere.gguf"}
	v := h.wait("failed", func(v View) bool { return job0(v).State == DownloadFailed })
	if f := job0(v).Failure; f.Kind != engine.FailExit || !strings.Contains(f.Line, "/cache/elsewhere.gguf") {
		t.Errorf("failure %+v", f)
	}
}

// A switch to another engine stops the download, keeping what it
// fetched: the engine's jobs wait, paused, for it to run again.
func TestDownloadJobSwitch(t *testing.T) {
	eng := newDLEngine()
	other := newFakeEngine()
	other.id = "other"
	h := newHarnessAs(t, eng.fakeEngine, eng, other)
	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{})
	h.s.DownloadModel("hf/next:Q4", engine.ModelOptions{})
	c := eng.next(t)
	c.progress(300, 1000)
	// Both jobs are in the list before the switch: a download asked for
	// while a switch runs is dropped.
	h.wait("running", func(v View) bool { return job0(v).Done == 300 && len(v.Downloads) == 2 })
	h.s.SwitchEngine("other", "")
	h.wait("switched", func(v View) bool { return v.EngineID == "other" && len(v.Downloads) == 0 })
	select {
	case <-c.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the download went on after the switch")
	}
	h.s.SwitchEngine("fake", "")
	v := h.wait("back", func(v View) bool {
		return v.EngineID == "fake" && len(v.Downloads) == 2 &&
			v.Downloads[0].State == DownloadPaused && v.Downloads[1].State == DownloadPaused
	})
	if j := v.Downloads[0]; j.Done != 300 || j.Total != 1000 {
		t.Errorf("paused job %+v", j)
	}
	if d := eng.droppedList(); len(d) != 0 {
		t.Errorf("a switch dropped files: %v", d)
	}
	select {
	case c := <-eng.calls:
		t.Errorf("a paused download started by itself: %+v", c.req)
	case <-time.After(50 * time.Millisecond):
	}
}

// Downloads queue one behind the other, in the order asked; a queued one
// can be cancelled before it starts, and a second ask for a queued model
// changes nothing.
func TestDownloadQueue(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	for _, id := range []string{"hf/a:Q4", "hf/b:Q4", "hf/c:Q4", "hf/b:Q4"} {
		h.s.DownloadModel(id, engine.ModelOptions{})
	}
	a := eng.next(t)
	v := h.wait("queued", func(v View) bool { return len(v.Downloads) == 3 })
	var states []DownloadState
	for _, j := range v.Downloads {
		states = append(states, j.State)
	}
	if a.req.Model != "hf/a:Q4" || !slices.Equal(states, []DownloadState{DownloadRunning, DownloadQueued, DownloadQueued}) {
		t.Fatalf("first %s, states %v", a.req.Model, states)
	}
	h.s.CancelDownload("hf/b:Q4")
	h.wait("b gone", func(v View) bool { return len(v.Downloads) == 2 })
	a.result <- dlResult{err: engine.Failure{Kind: engine.FailModel, Line: "no such tag"}}
	if c := eng.next(t); c.req.Model != "hf/c:Q4" {
		t.Errorf("after a: %s", c.req.Model)
	}
	if d := eng.droppedList(); !slices.Equal(d, []string{"hf/b:Q4"}) {
		t.Errorf("DropPartial %v", d)
	}
}

// Removing a job while another of the engine downloads waits with its
// partial files: the engine cannot tell them apart from the running one's.
func TestDownloadDropWaits(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.s.DownloadModel("hf/a:Q4", engine.ModelOptions{})
	eng.next(t).result <- dlResult{err: engine.Failure{Kind: engine.FailExit, Line: "network"}}
	h.wait("a failed", func(v View) bool { return job0(v).State == DownloadFailed })
	h.s.DownloadModel("hf/b:Q4", engine.ModelOptions{})
	b := eng.next(t)
	h.s.DismissDownload("hf/a:Q4")
	h.wait("a gone", func(v View) bool { return len(v.Downloads) == 1 })
	if d := eng.droppedList(); len(d) != 0 {
		t.Errorf("dropped while b runs: %v", d)
	}
	b.result <- dlResult{err: engine.Failure{Kind: engine.FailExit, Line: "network"}}
	deadline := time.Now().Add(3 * time.Second)
	for len(eng.droppedList()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if d := eng.droppedList(); !slices.Equal(d, []string{"hf/a:Q4"}) {
		t.Errorf("DropPartial %v", d)
	}
}

// An update of the engine stops its download, keeping what it fetched,
// and the download goes on once the update is done.
func TestDownloadUpdateHolds(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	upgraded := make(chan bool, 1)
	var c *dlCall
	h.brew = func([]string) (string, error) {
		// The download stopped before the engine's files change.
		upgraded <- c.ctx.Err() != nil
		eng.set(func(e *fakeEngine) { e.inst.Version = "1.5.0" })
		return "Upgraded", nil
	}
	h.s.DownloadModel("hf/a:Q4", engine.ModelOptions{})
	c = eng.next(t)
	c.progress(10, 100)
	h.wait("running", func(v View) bool { return job0(v).Done == 10 })
	h.s.Update()
	if stopped := <-upgraded; !stopped {
		t.Error("the download ran during the upgrade")
	}
	again := eng.next(t)
	if again.req.Model != "hf/a:Q4" {
		t.Errorf("resumed %+v", again.req)
	}
	v := h.wait("running again", func(v View) bool { return job0(v).State == DownloadRunning })
	if d := eng.droppedList(); len(d) != 0 || v.Update != UpdateNone {
		t.Errorf("dropped %v, update %d", d, v.Update)
	}
}

// Unfinished downloads survive a quit: Close keeps their files and the
// jobs file, and the next run shows them paused with what they fetched;
// nothing starts until the user resumes one, which fetches the rest.
func TestDownloadsSurviveQuit(t *testing.T) {
	file := filepath.Join(t.TempDir(), "downloads.json")
	withFile := func(o *Options) { o.JobsFile = file }
	eng := newDLEngine()
	h := newHarnessWith(t, eng.fakeEngine, eng, withFile)
	h.s.DownloadModel("hf/a:Q4", engine.ModelOptions{NoVision: true})
	h.s.DownloadModel("hf/b:Q8", engine.ModelOptions{})
	h.s.doWait(func() {})
	c := eng.next(t)
	c.progress(400, 1000)
	h.wait("progress", func(v View) bool { return job0(v).Done == 400 })
	h.s.Close()
	if c.ctx.Err() == nil {
		t.Error("Close left the download running")
	}
	if d := eng.droppedList(); len(d) != 0 {
		t.Errorf("Close dropped partial files: %v", d)
	}

	eng2 := newDLEngine()
	h2 := newHarnessWith(t, eng2.fakeEngine, eng2, withFile)
	v := h2.s.View()
	if len(v.Downloads) != 2 {
		t.Fatalf("jobs after restart %+v", v.Downloads)
	}
	a, b := v.Downloads[0], v.Downloads[1]
	if a.State != DownloadPaused || a.Model.ID != "hf/a:Q4" || a.Done != 400 || a.Total != 1000 || !a.Options.NoVision {
		t.Errorf("a %+v", a)
	}
	if b.State != DownloadPaused || b.Model.ID != "hf/b:Q8" || b.Model.Variant != "Q8" {
		t.Errorf("b %+v", b)
	}
	select {
	case c := <-eng2.calls:
		t.Fatalf("a download started at launch: %+v", c.req)
	case <-time.After(50 * time.Millisecond):
	}

	h2.s.DownloadModel("hf/a:Q4", a.Options) // Resume
	r := eng2.next(t)
	if r.req != (engine.DownloadRequest{Model: "hf/a:Q4", NoVision: true}) {
		t.Errorf("resumed %+v", r.req)
	}
	if j := job0(h2.s.View()); j.State != DownloadRunning || j.Done != 400 {
		t.Errorf("resumed job %+v", j) // keeps its bytes until the engine reports
	}
	// Remove drops a paused job's partial files.
	h2.s.DismissDownload("hf/b:Q8")
	h2.wait("b gone", func(v View) bool { return len(v.Downloads) == 1 })
	eng2.set(func(e *fakeEngine) { e.models = append(e.models, engine.Model{ID: "hf/a:Q4"}) })
	r.result <- dlResult{id: "hf/a:Q4"}
	h2.wait("done", func(v View) bool { return job0(v).State == DownloadDone })
	deadline := time.Now().Add(3 * time.Second)
	for len(eng2.droppedList()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if d := eng2.droppedList(); !slices.Equal(d, []string{"hf/b:Q8"}) {
		t.Errorf("DropPartial %v", d)
	}
	h2.s.Close()
	eng3 := newDLEngine()
	if h3 := newHarnessWith(t, eng3.fakeEngine, eng3, withFile); len(h3.s.View().Downloads) != 0 {
		t.Errorf("finished and removed jobs came back: %+v", h3.s.View().Downloads)
	}
}

// An engine without a Downloader downloads on the server's start.
func TestDownloadModelFallback(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{})
	v := h.waitPhase(PhaseLoading)
	if v.Download != "hf/new:Q4" || len(v.Downloads) != 0 {
		t.Errorf("view %+v", v)
	}
}

// A model's options set on the worker reach a Start queued after them.
func TestSetModelOptionsBeforeStart(t *testing.T) {
	h := newHarness(t, newFakeEngine())
	h.waitPhase(PhaseStopped)
	h.s.SetModelOptions("m/b", engine.ModelOptions{NoVision: true})
	h.s.Start()
	h.waitPhase(PhaseLoading)
	h.s.mu.Lock()
	cfg := h.s.runCfg
	h.s.mu.Unlock()
	if !cfg.NoVision {
		t.Errorf("run config %+v", cfg)
	}
}

// A missing model is "turn on downloads" only for an offline run.
func TestFailModelOffline(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	h.st.last().say("model not found")
	h.st.last().exit(1)
	v := h.waitPhase(PhaseError)
	if v.Failure.Kind != engine.FailModel || !v.Failure.Offline {
		t.Errorf("offline failure %+v", v.Failure)
	}

	st := h.s.Settings()
	st.ModelUpdates = true
	h.s.Apply(st)
	h.s.Start()
	h.waitPhase(PhaseLoading)
	h.st.last().say("model not found")
	h.st.last().exit(1)
	v = h.waitPhase(PhaseError)
	if v.Failure.Kind != engine.FailModel || v.Failure.Offline {
		t.Errorf("online failure %+v", v.Failure)
	}
}

// Pause stops a running download and keeps what it fetched: the job is
// paused at once with its bytes, nothing is dropped, the queued one after
// it starts, and DownloadModel resumes the paused one after that. A
// queued job pauses without starting.
func TestDownloadPause(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.s.DownloadModel("hf/a:Q4", engine.ModelOptions{})
	a := eng.next(t)
	h.s.DownloadModel("hf/b:Q4", engine.ModelOptions{})
	h.s.DownloadModel("hf/c:Q4", engine.ModelOptions{})
	a.progress(300, 1000)
	h.wait("a running, b and c queued", func(v View) bool { return job0(v).Done == 300 && len(v.Downloads) == 3 })

	h.s.PauseDownload("hf/c:Q4") // queued: paused, never started
	h.s.PauseDownload("hf/a:Q4")
	v := h.wait("a paused", func(v View) bool { return job0(v).State == DownloadPaused })
	if j := job0(v); j.Done != 300 || j.Total != 1000 {
		t.Errorf("paused a: %+v", j)
	}
	if a.ctx.Err() == nil {
		t.Error("the download's context was not cancelled")
	}
	b := eng.next(t)
	if b.req.Model != "hf/b:Q4" {
		t.Fatalf("after the pause: %s", b.req.Model)
	}
	if d := eng.droppedList(); len(d) != 0 {
		t.Errorf("DropPartial %v", d)
	}
	h.s.PauseDownload("hf/a:Q4") // paused already: nothing changes

	// Resumed: it waits for b, then goes on.
	h.s.DownloadModel("hf/a:Q4", engine.ModelOptions{})
	h.wait("a queued", func(v View) bool { return job0(v).State == DownloadQueued })
	b.result <- dlResult{id: "hf/b:Q4"}
	if c := eng.next(t); c.req.Model != "hf/a:Q4" {
		t.Errorf("resumed %s", c.req.Model)
	}
	v = h.s.View()
	for _, j := range v.Downloads {
		if j.Model.ID == "hf/c:Q4" && j.State != DownloadPaused {
			t.Errorf("c: %+v", j)
		}
	}
}

// A resume while the paused download still stops queues it: it runs once
// the engine has stopped, and does not end up paused.
func TestDownloadPauseResumeWhileStopping(t *testing.T) {
	eng := newDLEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	slow := &slowStop{dlEngine: eng, release: make(chan struct{})}
	h.s.mu.Lock()
	h.s.eng = slow
	h.s.mu.Unlock()
	h.s.DownloadModel("hf/a:Q4", engine.ModelOptions{})
	eng.next(t)
	h.s.PauseDownload("hf/a:Q4")
	h.wait("paused", func(v View) bool { return job0(v).State == DownloadPaused })
	h.s.DownloadModel("hf/a:Q4", engine.ModelOptions{})
	h.wait("queued", func(v View) bool { return job0(v).State == DownloadQueued })
	close(slow.release)
	if c := eng.next(t); c.req.Model != "hf/a:Q4" {
		t.Errorf("resumed %s", c.req.Model)
	}
}

// slowStop is dlEngine whose Download returns from a cancel only once
// release closes, as an engine that takes a while to stop.
type slowStop struct {
	*dlEngine
	release chan struct{}
}

func (e *slowStop) Download(ctx context.Context, req engine.DownloadRequest, progress func(done, total int64)) (string, error) {
	id, err := e.dlEngine.Download(ctx, req, progress)
	if ctx.Err() != nil {
		select {
		case <-e.release:
		case <-time.After(3 * time.Second):
		}
	}
	return id, err
}
