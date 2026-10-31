package core

import (
	"context"
	"slices"

	"github.com/kotuke/lumilo/internal/engine"
)

// Removal is the deletion of an installed model (engine.Remover): what
// it frees, counted first for the confirmation (Supervisor.PlanRemove),
// then the deletion itself (Supervisor.RemoveModel).
type Removal struct {
	Model string // Model.ID; "" = none
	// Counted: Plan holds what the deletion frees.
	Counted bool
	Plan    engine.RemovePlan
	// Running is set while the files are being deleted.
	Running bool
	// Error is why the count failed, as the engine said it; "" when it did
	// not. A deletion that fails or is refused is a NoticeRemoveFailed.
	Error string
}

// PlanRemove counts what deleting the installed model id frees, off the
// worker: View.Removal shows it once counted. It does nothing while a
// deletion runs.
func (s *Supervisor) PlanRemove(id string) {
	s.mu.Lock()
	rm, ok := s.eng.(engine.Remover)
	if !ok || s.closed || s.removal.Running || id == "" {
		s.mu.Unlock()
		return
	}
	s.removal = Removal{Model: id}
	eng := s.eng
	s.mu.Unlock()
	s.notify()
	s.goBG(func() {
		ctx, cancel := context.WithTimeout(s.ctx, detectTimeout)
		plan, err := rm.PlanRemove(ctx, id)
		cancel()
		s.mu.Lock()
		if s.eng != eng || s.removal.Model != id || s.removal.Running {
			s.mu.Unlock()
			return
		}
		if err != nil {
			s.removal.Error = err.Error()
		} else {
			s.removal.Counted, s.removal.Plan = true, plan
		}
		s.mu.Unlock()
		s.notify()
	})
}

// CancelRemove forgets a count that was not followed by a deletion.
func (s *Supervisor) CancelRemove() {
	s.mu.Lock()
	changed := !s.removal.Running && s.removal.Model != ""
	if changed {
		s.removal = Removal{}
	}
	s.mu.Unlock()
	if changed {
		s.notify()
	}
}

// RemoveModel deletes the installed model id from the disk. Our server
// stops first when it runs the model; nothing starts until the deletion
// ends. It does nothing while a download of the engine runs or waits,
// while the engine installs, updates or is being switched, and when a
// server started elsewhere runs the model. The model leaves View.Models;
// when it was the selected one, another installed model is picked, and
// Options.OnRemove hears of it so the app forgets the model's choices.
// A refusal or a failure is a NoticeRemoveFailed with the reason.
func (s *Supervisor) RemoveModel(id string) {
	s.goBG(func() {
		// startMu lets a start in progress finish first, so its process is
		// the one stopped below; once removal.Running is set, none begins.
		s.startMu.Lock()
		s.mu.Lock()
		rm, ok := s.eng.(engine.Remover)
		if !ok || s.closed || id == "" {
			s.mu.Unlock()
			s.startMu.Unlock()
			return
		}
		if why := s.unremovableLocked(id); why != "" {
			// The dialog that asked has closed: the panel says why.
			s.removal = Removal{}
			s.noticeLocked(NoticeRemoveFailed, why)
			s.mu.Unlock()
			s.startMu.Unlock()
			s.notify()
			return
		}
		plan := s.removal
		s.removal = Removal{Model: id, Running: true}
		if plan.Model == id {
			s.removal.Counted, s.removal.Plan = plan.Counted, plan.Plan
		}
		ours := s.proc != nil && s.runCfg.Model == id
		if ours {
			s.restartAfter = false
		}
		eng := s.eng
		s.mu.Unlock()
		s.startMu.Unlock()
		s.notify()

		if ours {
			// Its files may go while it runs (macOS frees them once
			// closed), but a server that held the model would keep Splash
			// from deleting its assembly.
			s.stopAndWait(closeWait)
		}
		ctx, cancel := context.WithTimeout(s.ctx, removeTimeout)
		err := rm.Remove(ctx, id)
		cancel()
		lctx, lcancel := context.WithTimeout(s.ctx, detectTimeout)
		models, merr := eng.Models(lctx)
		adopt := adoptable(lctx, eng)
		lcancel()

		s.mu.Lock()
		s.removal = Removal{}
		if err != nil {
			s.noticeLocked(NoticeRemoveFailed, err.Error())
		}
		removed := merr == nil && !slices.ContainsFunc(models, func(m engine.Model) bool { return m.ID == id })
		if merr == nil && s.eng == eng {
			s.setModelsLocked(models, adopt)
			if removed && s.settings.Model == id {
				s.settings.Model = ""
				s.pickModelLocked()
			}
		}
		if removed {
			key := ModelKey{Engine: eng.Info().ID, Model: id}
			delete(s.settings.Contexts, key)
			delete(s.settings.Options, key)
			s.jobs = slices.DeleteFunc(s.jobs, func(j *job) bool {
				return j.eng == eng && j.State == DownloadDone && (j.Installed == id || j.Model.ID == id)
			})
		}
		s.mu.Unlock()
		s.notify()
		if removed && s.opts.OnRemove != nil {
			s.opts.OnRemove(eng.Info().ID, id)
		}
	})
}

// unremovableLocked is why the model id cannot be deleted now, in
// English, "" when it can (RemoveModel).
func (s *Supervisor) unremovableLocked(id string) string {
	name := s.eng.Info().Name
	switch {
	case s.removal.Running:
		return "another model is being deleted"
	case s.switching:
		return "the engine is being switched"
	case !s.inst.Installed || s.phase == PhaseInstalling:
		return name + " is not installed"
	case s.update == UpdateUpdating:
		return name + " is updating"
	case !slices.ContainsFunc(s.models, func(m engine.Model) bool { return m.ID == id }):
		return id + " is not installed"
	case s.phase == PhaseExternal && s.external != nil && s.external.Model == id:
		return "a server started outside Lumilo runs it"
	case slices.ContainsFunc(s.jobs, func(j *job) bool {
		return j.eng == s.eng && (j.alive() || j.State == DownloadQueued)
	}):
		// A download writes where the files go; one that waits would start.
		return "a model is downloading"
	}
	return ""
}
