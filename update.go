package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"github.com/kotuke/lumilo/internal/appupdate"
	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
)

// mygoSource is the release feed of mygo.Updater: the manifest of the
// newest GitHub release, signed with the key in mygo.json.
type mygoSource struct{}

func (mygoSource) Enabled() bool { return mygo.Updater.Enabled() }

func (mygoSource) Check(ctx context.Context) (appupdate.Release, error) {
	up, err := mygo.Updater.Check(ctx)
	if err != nil {
		log.Println("update check:", err)
		return nil, err
	}
	if up == nil {
		return nil, nil
	}
	return mygoRelease{up}, nil
}

type mygoRelease struct{ u *mygo.Update }

func (r mygoRelease) Version() string { return r.u.Version }
func (r mygoRelease) Install(ctx context.Context, progress func(done, total int64)) error {
	err := r.u.Install(ctx, progress)
	if err != nil {
		log.Println("update install:", err)
	} else {
		log.Println("update: version", r.u.Version, "installed; it runs after a restart")
	}
	return err
}

// releaseBuild reports whether the running bundle is a published release:
// notarized, with the ticket stapled into it. A bundle built on this Mac
// has none, and its updates download only when the user asks, so that a
// release never replaces a build being tried without a word.
func releaseBuild() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	// Lumilo.app/Contents/MacOS/Lumilo → Lumilo.app/Contents/CodeResources
	_, err = os.Stat(filepath.Join(filepath.Dir(filepath.Dir(exe)), "CodeResources"))
	return err == nil
}

// startUpdates makes the updater of the app and starts its checks.
func (a *app) startUpdates() {
	a.release = releaseBuild()
	a.upd = appupdate.New(appupdate.Options{
		Source:    mygoSource{},
		Automatic: a.autoUpdates,
		OnChange: func(s appupdate.State) {
			if !a.quitting.Load() {
				a.win.Update(func() { a.panel.SetAppUpdate(s) })
			}
		},
	})
	a.panel.SetAppInfo(mygo.App.Version(), a.release)
	a.panel.SetAppUpdate(a.upd.State())
	a.upd.Start()
}

// autoUpdates reports whether a version found in the background downloads
// at once.
func (a *app) autoUpdates() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.release && !a.st.ManualUpdates
}

func (a *app) CheckAppUpdate()    { a.upd.Check() }
func (a *app) DownloadAppUpdate() { a.upd.Download() }

// RestartToUpdate restarts into the version installed. Our server stops
// with the app, and its downloads pause: the new version starts them again
// (resumeAfterUpdate).
func (a *app) RestartToUpdate() {
	v := a.sup.View()
	r := resumeState{At: time.Now(), Engine: v.EngineID, Serve: ownServer(v.Phase)}
	for _, j := range v.Downloads {
		if j.State == core.DownloadRunning || j.State == core.DownloadQueued {
			r.Downloads = append(r.Downloads, resumeJob{ID: j.Model.ID, Options: j.Options})
		}
	}
	if path := a.resumePath(); path != "" {
		if b, err := json.Marshal(r); err == nil {
			if err := os.WriteFile(path, b, 0o600); err != nil {
				log.Println("update:", err)
			}
		}
	}
	mygo.App.Relaunch()
}

// ownServer reports whether the phase is of a server that Lumilo runs.
func ownServer(p core.Phase) bool {
	switch p {
	case core.PhaseLoading, core.PhaseReady, core.PhaseGenerating, core.PhaseReleased, core.PhaseRestoring:
		return true
	}
	return false
}

// resumeState is what ran when the app restarted to update.
type resumeState struct {
	At        time.Time   `json:"at"`
	Engine    string      `json:"engine"`
	Serve     bool        `json:"serve"`
	Downloads []resumeJob `json:"downloads,omitempty"`
}

type resumeJob struct {
	ID      string              `json:"id"`
	Options engine.ModelOptions `json:"options"`
}

// resumeFor is how long after the restart the state still applies: a
// launch much later is not the restart's.
const resumeFor = 10 * time.Minute

func (a *app) resumePath() string {
	if a.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.path), "resume.json")
}

// resumeAfterUpdate starts the server and the downloads that ran before the
// restart into this version, once; it runs after the Keychain was read. The
// server starts with the engine's model and the saved settings.
func (a *app) resumeAfterUpdate() {
	path := a.resumePath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	os.Remove(path)
	var r resumeState
	if json.Unmarshal(b, &r) != nil || time.Since(r.At) > resumeFor || time.Since(r.At) < 0 {
		return
	}
	a.mu.Lock()
	same := r.Engine == a.st.Engine
	a.mu.Unlock()
	if !same {
		return
	}
	log.Printf("update: resuming after the restart: server %v, %d downloads", r.Serve, len(r.Downloads))
	if r.Serve {
		a.sup.Start()
	}
	for _, j := range r.Downloads {
		a.sup.DownloadModel(j.ID, j.Options)
	}
}
