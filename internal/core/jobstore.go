package core

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// The downloads that did not finish survive a quit: Options.JobsFile
// keeps them, and the next run shows them paused (DownloadPaused), with
// what they fetched, until the user resumes or removes them. Nothing
// starts on its own at launch: Lumilo may open at login on a network the
// user does not want a model of many gigabytes fetched on.

// savedJobs is the jobs file.
type savedJobs struct {
	Version int        `json:"version"`
	Jobs    []savedJob `json:"jobs"`
}

type savedJob struct {
	Engine  string              `json:"engine"` // engine.Info.ID
	Model   string              `json:"model"`  // Model.ID as asked
	Options engine.ModelOptions `json:"options"`
	Done    int64               `json:"done"`
	Total   int64               `json:"total"`
	Since   time.Time           `json:"since"`
}

const jobsVersion = 1

// loadJobs reads the jobs file into paused jobs of the engines in all; a
// job of an engine Lumilo no longer has, or without a Downloader, is left
// out.
func loadJobs(path string, all []engine.Engine) []*job {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Println("downloads:", err)
		}
		return nil
	}
	var f savedJobs
	if err := json.Unmarshal(data, &f); err != nil || f.Version != jobsVersion {
		log.Println("downloads: unreadable", path, err)
		return nil
	}
	var out []*job
	for _, sj := range f.Jobs {
		for _, e := range all {
			if e.Info().ID != sj.Engine {
				continue
			}
			if _, ok := e.(engine.Downloader); !ok {
				continue
			}
			md, err := e.ParseModel(sj.Model)
			if err != nil {
				continue
			}
			out = append(out, &job{DownloadJob: DownloadJob{Model: md, Options: sj.Options, State: DownloadPaused,
				Done: sj.Done, Total: sj.Total}, eng: e, since: sj.Since})
		}
	}
	return out
}

// saveJobs writes the jobs that did not finish to Options.JobsFile, as
// they are now: one write at a time, each taking its copy inside the
// turn, so the newest lands last. A failed job is kept too: what it
// fetched is resumed by its retry.
func (s *Supervisor) saveJobs() {
	path := s.opts.JobsFile
	if path == "" {
		return
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	f := savedJobs{Version: jobsVersion, Jobs: []savedJob{}}
	s.mu.Lock()
	for _, j := range s.jobs {
		if j.State == DownloadDone || j.State == DownloadCancelling {
			continue
		}
		f.Jobs = append(f.Jobs, savedJob{Engine: j.eng.Info().ID, Model: j.Model.ID, Options: j.Options,
			Done: j.Done, Total: j.Total, Since: j.since})
	}
	s.mu.Unlock()
	if len(f.Jobs) == 0 && s.savedEmpty {
		return
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err == nil {
		err = writeFileAtomic(path, data)
	}
	if err != nil {
		log.Println("downloads:", err)
		return
	}
	s.savedEmpty = len(f.Jobs) == 0
}

// writeFileAtomic writes data to path through a temporary file and a
// rename, so a crash leaves the old file or the new one.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".downloads-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(data, '\n'))
	if err := tmp.Close(); werr == nil {
		werr = err
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}
