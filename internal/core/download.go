package core

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// DownloadState is where a download job is.
type DownloadState int

const (
	DownloadRunning    DownloadState = iota // fetching files
	DownloadCancelling                      // stopping after Cancel; it goes away when stopped
	DownloadFailed                          // stopped with a Failure; Retry or Dismiss
	DownloadDone                            // installed: the model is in View.Models
	// DownloadQueued waits for the engine's running download to end (one
	// runs at a time), or for an update of the engine to finish.
	DownloadQueued
	// DownloadPaused stopped with what it fetched kept: the user paused
	// it, Lumilo quit, or the user switched to another engine.
	// DownloadModel resumes it; DismissDownload removes it and its partial
	// files.
	DownloadPaused
)

// DownloadJob is a model being downloaded apart from the server
// (engine.Downloader): the server keeps running its model meanwhile.
type DownloadJob struct {
	// Model is the name as ParseModel split it; Model.ID is what was asked.
	Model engine.Model
	// Options are what the model was downloaded with: NoVision downloads
	// it for text only.
	Options engine.ModelOptions
	State   DownloadState
	// Done is the bytes fetched; Total the bytes in all, 0 = not known yet.
	// A queued or paused job keeps the last ones it reported.
	Done, Total int64
	// Failure is why the download stopped (DownloadFailed). Its Offline is
	// false: a download is online.
	Failure engine.Failure
	// Installed is the Model.ID the engine lists for it once done; it may
	// differ from Model.ID (llama.cpp lists a file path).
	Installed string
}

// stopWhy is what a stopped runner does with its job.
type stopWhy int

const (
	stopPause   stopWhy = iota // keep the files; the job is paused (Pause, quit, engine switch)
	stopDrop                   // the user cancelled: drop the files and the job
	stopRequeue                // keep the files; the job runs again later (engine update)
)

// job is a DownloadJob with what runs it.
type job struct {
	DownloadJob
	eng   engine.Engine
	since time.Time // when it first began: DropPartial looks at files since
	// While its runner runs: cancel stops it, exited closes when it has
	// returned, and stop says what then becomes of the job.
	cancel context.CancelFunc
	exited chan struct{}
	stop   stopWhy
}

// alive reports whether the job's runner has not returned yet.
func (j *job) alive() bool {
	if j.exited == nil {
		return false
	}
	select {
	case <-j.exited:
		return false
	default:
		return true
	}
}

// partialDrop is a DropPartial that waits for the engine's running
// download to end: an engine's partial files are not told apart by job
// (Splash's are the whole cache's, llama.cpp's the repository's).
type partialDrop struct {
	eng   engine.Engine
	model string
	since time.Time
}

// DownloadModel downloads the model id (engine.ParseModel syntax) with the
// options o, apart from the server: the job shows in View.Downloads, and
// the model joins View.Models once installed; the selected model stays
// (unless there was none). One download of an engine runs at a time: the
// others queue (DownloadQueued) and start in the order they were asked.
// The call resumes a paused or failed job of the model, from what it
// fetched, with o; one already queued or running stays as it is, and a
// finished one is replaced. Nothing happens during an engine switch or
// install, or before the engine is installed.
//
// An engine without engine.Downloader downloads on the server's start
// instead (Download), and o is ignored.
func (s *Supervisor) DownloadModel(id string, o engine.ModelOptions) {
	if _, ok := s.engine().(engine.Downloader); !ok {
		s.Download(id)
		return
	}
	s.do(func() {
		s.mu.Lock()
		_, ok := s.eng.(engine.Downloader)
		if !ok || s.closed || s.switching || s.removal.Running || !s.inst.Installed || id == "" || s.phase == PhaseInstalling {
			s.mu.Unlock()
			return
		}
		md, err := s.eng.ParseModel(id)
		if err != nil {
			s.mu.Unlock()
			return
		}
		if j := s.jobLocked(md.ID); j != nil {
			switch j.State {
			case DownloadPaused, DownloadFailed:
				j.State, j.Options, j.Failure = DownloadQueued, o, engine.Failure{}
			case DownloadDone:
				s.jobs = slices.DeleteFunc(s.jobs, func(x *job) bool { return x == j })
				j = nil
			default: // on its way already
				s.mu.Unlock()
				return
			}
			if j != nil {
				s.mu.Unlock()
				s.jobsChanged()
				return
			}
		}
		s.jobs = append(s.jobs, &job{DownloadJob: DownloadJob{Model: md, Options: o, State: DownloadQueued},
			eng: s.eng, since: s.d.clock.Now()})
		s.mu.Unlock()
		s.jobsChanged()
	})
}

// jobLocked is the job of the model id of the engine being run, nil if none.
func (s *Supervisor) jobLocked(id string) *job {
	for _, j := range s.jobs {
		if j.eng == s.eng && j.Model.ID == id {
			return j
		}
	}
	return nil
}

// CancelDownload stops the download of id and removes what it fetched: a
// running one goes away once the engine stopped (DownloadCancelling), a
// queued, paused or failed one at once.
func (s *Supervisor) CancelDownload(id string) { s.removeJob(id, true) }

// DismissDownload removes a job of id that does not run: a failed, paused
// or queued one with its partial files, a finished one from the view.
func (s *Supervisor) DismissDownload(id string) { s.removeJob(id, false) }

// PauseDownload stops the download of id and keeps what it fetched, for
// DownloadModel to resume it: a running or queued job is DownloadPaused at
// once (a running one's engine stops meanwhile, and the next queued job
// starts once it has), others stay as they are.
func (s *Supervisor) PauseDownload(id string) {
	s.mu.Lock()
	j := s.jobLocked(id)
	if j == nil || (j.State != DownloadRunning && j.State != DownloadQueued) {
		s.mu.Unlock()
		return
	}
	j.State, j.stop = DownloadPaused, stopPause
	if j.alive() {
		j.cancel()
	}
	s.mu.Unlock()
	s.jobsChanged()
}

func (s *Supervisor) removeJob(id string, cancelRunning bool) {
	s.mu.Lock()
	j := s.jobLocked(id)
	if j == nil {
		s.mu.Unlock()
		return
	}
	switch j.State {
	case DownloadRunning:
		if cancelRunning {
			j.State, j.stop = DownloadCancelling, stopDrop
			j.cancel()
		}
	case DownloadCancelling:
	default:
		if j.alive() {
			// Queued again by an update, and still stopping: it goes as a
			// cancelled one, once stopped.
			j.State, j.stop = DownloadCancelling, stopDrop
			break
		}
		s.jobs = slices.DeleteFunc(s.jobs, func(x *job) bool { return x == j })
		if j.State != DownloadDone {
			s.drops = append(s.drops, partialDrop{j.eng, j.Model.ID, j.since})
		}
	}
	s.mu.Unlock()
	s.jobsChanged()
}

// SetModelOptions stores the vision and draft choices of a model of the
// engine being run, on the worker: a Start queued after it uses them.
func (s *Supervisor) SetModelOptions(id string, o engine.ModelOptions) {
	s.do(func() {
		s.mu.Lock()
		key := ModelKey{Engine: s.eng.Info().ID, Model: id}
		if s.settings.Options == nil {
			s.settings.Options = map[ModelKey]engine.ModelOptions{}
		}
		if o == (engine.ModelOptions{}) {
			delete(s.settings.Options, key)
		} else {
			s.settings.Options[key] = o
		}
		s.mu.Unlock()
		s.notify()
	})
}

// jobsChanged follows a change of the jobs: the partial files of removed
// jobs go, unless their engine still downloads; the next queued job
// starts; the list is saved and the view published.
func (s *Supervisor) jobsChanged() {
	s.mu.Lock()
	var drops []partialDrop
	s.drops = slices.DeleteFunc(s.drops, func(p partialDrop) bool {
		if s.engineBusyLocked(p.eng) {
			return false
		}
		drops = append(drops, p)
		return true
	})
	s.mu.Unlock()
	for _, p := range drops {
		if d, ok := p.eng.(engine.Downloader); ok {
			d.DropPartial(p.model, p.since)
		}
	}
	s.schedule()
	s.saveJobs()
	s.notify()
}

// engineBusyLocked reports whether a download of e runs or stops.
func (s *Supervisor) engineBusyLocked(e engine.Engine) bool {
	return slices.ContainsFunc(s.jobs, func(j *job) bool { return j.eng == e && j.alive() })
}

// schedule starts the first queued job of the engine being run when none
// of its downloads runs, and its files may change: no switch, install or
// update is under way.
func (s *Supervisor) schedule() {
	s.mu.Lock()
	d, ok := s.eng.(engine.Downloader)
	if !ok || s.closed || s.switching || s.removal.Running || s.update == UpdateUpdating || s.phase == PhaseInstalling ||
		!s.inst.Installed || s.engineBusyLocked(s.eng) {
		s.mu.Unlock()
		return
	}
	i := slices.IndexFunc(s.jobs, func(j *job) bool { return j.eng == s.eng && j.State == DownloadQueued })
	if i < 0 {
		s.mu.Unlock()
		return
	}
	j := s.jobs[i]
	ctx, cancel := context.WithCancel(s.ctx)
	j.State, j.cancel, j.exited, j.stop = DownloadRunning, cancel, make(chan struct{}), stopPause
	s.mu.Unlock()
	s.notify()
	s.goBG(func() { s.runJob(ctx, d, j) })
}

// runJob downloads j with d and records how it ended.
func (s *Supervisor) runJob(ctx context.Context, d engine.Downloader, j *job) {
	defer s.jobsChanged() // the next one starts
	defer close(j.exited)
	progress := func(done, total int64) {
		s.mu.Lock()
		changed := j.State == DownloadRunning && (done != j.Done || total != j.Total)
		if changed {
			j.Done, j.Total = done, total
		}
		s.mu.Unlock()
		if changed {
			s.notify()
		}
	}
	id, err := d.Download(ctx, engine.DownloadRequest{Model: j.Model.ID, NoVision: j.Options.NoVision}, progress)
	stopped := ctx.Err() != nil
	s.mu.Lock()
	j.cancel()
	if stopped {
		switch j.stop {
		case stopDrop: // the user cancelled: nothing of it stays
			s.jobs = slices.DeleteFunc(s.jobs, func(x *job) bool { return x == j })
			s.mu.Unlock()
			d.DropPartial(j.Model.ID, j.since)
			return
		case stopRequeue:
			j.State = DownloadQueued
		default: // Pause, Close or a switch: what it fetched waits for a resume
			if j.State != DownloadQueued { // resumed while it stopped
				j.State = DownloadPaused
			}
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if err != nil {
		var f engine.Failure
		if !errors.As(err, &f) {
			f = engine.Failure{Kind: engine.FailExit, Code: -1, Line: err.Error()}
		}
		f.Offline = false
		s.mu.Lock()
		j.State, j.Failure = DownloadFailed, f
		s.mu.Unlock()
		return
	}

	lctx, cancel := context.WithTimeout(s.ctx, detectTimeout)
	models, merr := j.eng.Models(lctx)
	adopt := adoptable(lctx, j.eng)
	cancel()
	s.mu.Lock()
	j.State, j.Installed = DownloadDone, id
	if j.Total > 0 {
		j.Done = j.Total
	}
	if merr == nil && !slices.ContainsFunc(models, func(m engine.Model) bool { return m.ID == id }) {
		// Installed where the engine does not look: nothing to pick, so it
		// is a failure the user sees, not a row that silently goes away.
		j.State, j.Installed = DownloadFailed, ""
		j.Failure = engine.Failure{Kind: engine.FailExit, Line: "downloaded, but the engine does not list " + id}
	}
	if merr == nil && s.eng == j.eng {
		s.setModelsLocked(models, adopt)
		s.pickModelLocked() // the first model when there was none
	}
	s.mu.Unlock()
}

// holdJobsLocked stops the running downloads of e, keeping what they
// fetched: a switch pauses e's jobs (paused true), the queued ones too; an
// update of e queues the running one again. It returns what to wait for:
// the runners that still run.
func (s *Supervisor) holdJobsLocked(e engine.Engine, paused bool) []chan struct{} {
	var wait []chan struct{}
	s.jobs = slices.DeleteFunc(s.jobs, func(j *job) bool { return j.eng == e && paused && j.State == DownloadDone })
	for _, j := range s.jobs {
		if j.eng != e {
			continue
		}
		switch {
		case j.State == DownloadRunning && paused:
			j.stop = stopPause
			j.cancel()
		case j.State == DownloadRunning:
			j.State, j.stop = DownloadQueued, stopRequeue
			j.cancel()
		case j.State == DownloadQueued && paused:
			j.State = DownloadPaused
		}
		if j.alive() {
			wait = append(wait, j.exited)
		}
	}
	return wait
}

// downloadsLocked is View.Downloads.
func (s *Supervisor) downloadsLocked() []DownloadJob {
	if len(s.jobs) == 0 {
		return nil
	}
	var out []DownloadJob
	for _, j := range s.jobs {
		if j.eng == s.eng { // another engine's jobs wait for it to run again
			out = append(out, j.DownloadJob)
		}
	}
	return out
}
