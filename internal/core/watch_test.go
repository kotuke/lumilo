package core

import (
	"slices"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

func modelIDs(v View) []string {
	var ids []string
	for _, m := range v.Models {
		ids = append(ids, m.ID)
	}
	return ids
}

// While the panel is open, a model pulled outside Lumilo shows within
// modelScanEvery; a closed panel reads no folders.
func TestWatchRereadsModels(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	eng.set(func(e *fakeEngine) { e.models = append(e.models, engine.Model{ID: "m/c"}) })

	h.step(modelScanEvery)
	if ids := modelIDs(h.s.View()); slices.Contains(ids, "m/c") {
		t.Fatalf("closed panel re-read the models: %v", ids)
	}

	h.s.Watch(true)
	h.s.doWait(func() {}) // the detection on open
	if ids := modelIDs(h.s.View()); !slices.Contains(ids, "m/c") {
		t.Fatalf("open did not detect the new model: %v", ids)
	}

	eng.set(func(e *fakeEngine) { e.models = []engine.Model{{ID: "m/a"}} }) // m/b removed by hand
	h.step(modelScanEvery / 2)
	if ids := modelIDs(h.s.View()); len(ids) != 3 {
		t.Fatalf("re-read before modelScanEvery: %v", ids)
	}
	h.step(modelScanEvery / 2)
	v := h.s.View()
	if ids := modelIDs(v); !slices.Equal(ids, []string{"m/a"}) {
		t.Fatalf("models %v", ids)
	}
	if v.Model.ID != "m/a" {
		t.Errorf("selected %q, want the first left once m/b is gone", v.Model.ID)
	}

	h.s.Watch(false)
	eng.set(func(e *fakeEngine) { e.models = []engine.Model{{ID: "m/a"}, {ID: "m/d"}} })
	h.step(modelScanEvery)
	if ids := modelIDs(h.s.View()); len(ids) != 1 {
		t.Errorf("closed panel re-read the models: %v", ids)
	}
}

// Opening the panel detects the other engines again: one installed or
// removed with brew shows so in the engine list.
func TestWatchRedetectsEngines(t *testing.T) {
	other := otherEngine()
	h := newHarness(t, newFakeEngine(), other)
	h.waitPhase(PhaseStopped)
	other.set(func(e *fakeEngine) { e.inst = engine.Install{Installed: true, Ready: true, Version: "0.8.0"} })

	h.s.Watch(true)
	h.s.doWait(func() {})
	v := h.s.View()
	if len(v.Engines) != 2 || !v.Engines[1].Installed || v.Engines[1].Version != "0.8.0" {
		t.Fatalf("engines %+v", v.Engines)
	}

	// Still open: no second detection until the panel opens again.
	other.set(func(e *fakeEngine) { e.inst = engine.Install{Ready: true} })
	h.s.Watch(true)
	h.s.doWait(func() {})
	if !h.s.View().Engines[1].Installed {
		t.Errorf("an open panel detected the engines again")
	}
	h.s.Watch(false)
	h.s.Watch(true)
	h.s.doWait(func() {})
	if h.s.View().Engines[1].Installed {
		t.Errorf("reopening did not see the engine removed")
	}
}

// A re-read that began before a download finished does not put back the
// list without the new model, nor move the selection off it.
func TestWatchKeepsNewerList(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.s.Watch(true)
	h.s.doWait(func() {})

	in, release := make(chan struct{}), make(chan struct{})
	eng.set(func(e *fakeEngine) { e.inModels, e.modelsWait = in, release })
	ticked := make(chan struct{})
	go func() { h.step(modelScanEvery); close(ticked) }()
	<-in // the re-read holds the old list now
	eng.set(func(e *fakeEngine) { e.inModels, e.modelsWait = nil, nil })

	// What finishDownload does once the new model is installed.
	h.s.doWait(func() {
		h.s.mu.Lock()
		h.s.setModelsLocked([]engine.Model{{ID: "m/a"}, {ID: "m/b"}, {ID: "m/new"}}, nil)
		h.s.settings.Model = "m/new"
		h.s.mu.Unlock()
	})
	close(release)
	<-ticked

	v := h.s.View()
	if ids := modelIDs(v); !slices.Contains(ids, "m/new") || v.Model.ID != "m/new" {
		t.Errorf("models %v, selected %q", ids, v.Model.ID)
	}
}

// No look at the disk while a model's files are being deleted.
func TestWatchWaitsForRemoval(t *testing.T) {
	eng := newFakeEngine()
	h := newHarness(t, eng)
	h.waitPhase(PhaseStopped)
	h.s.mu.Lock()
	h.s.removal.Running = true
	h.s.mu.Unlock()
	eng.set(func(e *fakeEngine) { e.models = []engine.Model{{ID: "m/a"}} })

	h.s.Watch(true)
	h.s.doWait(func() {})
	h.step(modelScanEvery)
	if ids := modelIDs(h.s.View()); len(ids) != 2 {
		t.Errorf("read the models during a removal: %v", ids)
	}
}
