package core

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

// rmEngine is the fake engine with a Downloader and a Remover: Remove
// takes the model off the list, after hold closes when it is set.
type rmEngine struct {
	*dlEngine
	mu      sync.Mutex
	removed []string
	hold    chan struct{}
	err     error // what Remove returns; the model stays then
}

func newRMEngine() *rmEngine { return &rmEngine{dlEngine: newDLEngine()} }

func (e *rmEngine) PlanRemove(_ context.Context, id string) (engine.RemovePlan, error) {
	return engine.RemovePlan{Bytes: int64(len(id)), Shared: true}, nil
}

func (e *rmEngine) Remove(ctx context.Context, id string) error {
	e.mu.Lock()
	hold, err := e.hold, e.err
	e.removed = append(e.removed, id)
	e.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if err != nil {
		return err
	}
	e.set(func(f *fakeEngine) {
		f.models = slices.DeleteFunc(f.models, func(m engine.Model) bool { return m.ID == id })
	})
	return nil
}

func (e *rmEngine) calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.removed)
}

// Deleting the model our server runs stops the server, picks another
// model, forgets the model's choices and starts nothing.
func TestRemoveModelStopsServer(t *testing.T) {
	eng := newRMEngine()
	var forgot []string
	var mu sync.Mutex
	h := newHarnessWith(t, eng.fakeEngine, eng, func(o *Options) {
		o.Settings.Contexts = map[ModelKey]int{{Engine: "fake", Model: "m/b"}: 8192, {Engine: "fake", Model: "m/a"}: 4096}
		o.OnRemove = func(e, m string) {
			mu.Lock()
			forgot = append(forgot, e+" "+m)
			mu.Unlock()
		}
	})
	if v := h.s.View(); !v.CanRemove {
		t.Fatal("CanRemove is off for a Remover")
	}
	h.startReady()
	starts := h.st.count()

	h.s.PlanRemove("m/b")
	v := h.wait("count", func(v View) bool { return v.Removal.Counted })
	if v.Removal.Model != "m/b" || v.Removal.Plan != (engine.RemovePlan{Bytes: 3, Shared: true}) {
		t.Errorf("count %+v", v.Removal)
	}

	h.s.RemoveModel("m/b")
	v = h.wait("deleted", func(v View) bool { return len(v.Models) == 1 && !v.Removal.Running })
	if v.Model.ID != "m/a" || v.Removal != (Removal{}) {
		t.Errorf("selected %q, removal %+v", v.Model.ID, v.Removal)
	}
	if v.Phase != PhaseStopped || h.st.count() != starts {
		t.Errorf("phase %d, %d starts: the server did not stop, or started again", v.Phase, h.st.count()-starts)
	}
	st := h.s.Settings()
	if _, ok := st.Contexts[ModelKey{Engine: "fake", Model: "m/b"}]; ok || len(st.Contexts) != 1 {
		t.Errorf("contexts %v", st.Contexts)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(forgot, []string{"fake m/b"}) {
		t.Errorf("OnRemove %v", forgot)
	}
}

// Nothing starts while the files go, and a failure stays in the view.
func TestRemoveModelBlocksStartAndFails(t *testing.T) {
	eng := newRMEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.waitPhase(PhaseStopped)
	hold := make(chan struct{})
	eng.mu.Lock()
	eng.hold, eng.err = hold, errors.New("permission denied")
	eng.mu.Unlock()

	h.s.RemoveModel("m/a")
	h.wait("running", func(v View) bool { return v.Removal.Running && v.Removal.Model == "m/a" })
	h.s.Start()
	h.s.SwitchEngine("other", "")
	h.s.doWait(func() {})
	if h.st.count() != 0 {
		t.Error("a server started during the deletion")
	}
	close(hold)
	v := h.wait("failed", func(v View) bool { return !v.Removal.Running })
	if v.Notice.Kind != NoticeRemoveFailed || v.Notice.Text != "permission denied" || len(v.Models) != 2 || v.Model.ID != "m/b" {
		t.Errorf("notice %+v, removal %+v, models %+v", v.Notice, v.Removal, v.Models)
	}
}

// A download of the engine keeps its files: no deletion meanwhile, nor of
// a model a server started elsewhere runs, nor of one not installed.
func TestRemoveModelRefused(t *testing.T) {
	eng := newRMEngine()
	h := newHarnessAs(t, eng.fakeEngine, eng)
	h.waitPhase(PhaseStopped)

	h.s.RemoveModel("m/none")
	h.wait("not installed", func(v View) bool { return v.Notice.Text == "m/none is not installed" })
	h.s.DownloadModel("hf/new:Q4", engine.ModelOptions{})
	c := eng.next(t)
	h.s.RemoveModel("m/a")
	// The dialog that asked is gone: the refusal is a notice.
	v := h.wait("refused", func(v View) bool { return v.Notice.Text == "a model is downloading" })
	if v.Notice.Kind != NoticeRemoveFailed {
		t.Errorf("notice %+v", v.Notice)
	}
	if got := eng.calls(); len(got) != 0 {
		t.Errorf("deleted %v", got)
	}
	c.result <- dlResult{err: errors.New("no")}
	h.wait("failed download", func(v View) bool { return job0(v).State == DownloadFailed })
	h.s.RemoveModel("m/a")
	h.wait("deleted", func(v View) bool { return len(v.Models) == 1 })
	if got := eng.calls(); !slices.Equal(got, []string{"m/a"}) {
		t.Errorf("deleted %v", got)
	}
}
