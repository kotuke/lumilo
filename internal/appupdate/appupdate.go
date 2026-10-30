// Package appupdate keeps Lumilo itself up to date: it asks the release
// feed for a newer version once a day, downloads and installs it in the
// background (the running app stays as it is), and tells the panel that
// the new version waits for a restart. The app restarts only when the user
// asks. mygo.Updater is the feed and the installer (Source).
package appupdate

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Release is a newer version that Source.Check found.
type Release interface {
	Version() string
	// Install downloads the release, checks its signature and puts it in
	// place of the installed app; progress gets the bytes downloaded.
	Install(ctx context.Context, progress func(done, total int64)) error
}

// Source is where releases come from.
type Source interface {
	// Enabled reports whether the app can update itself.
	Enabled() bool
	// Check returns the newest release, or nil when the app is up to date.
	Check(ctx context.Context) (Release, error)
}

// Phase is where the update of the app is.
type Phase int

const (
	PhaseIdle        Phase = iota // nothing found (yet)
	PhaseChecking                 // asking the feed
	PhaseAvailable                // found; waits for Download (automatic downloads are off)
	PhaseDownloading              // downloading and installing
	PhaseReady                    // installed: runs after a restart
	PhaseFailed                   // the download or the install failed
)

// CheckResult is the answer to the check the user asked for (Check).
type CheckResult int

const (
	CheckNone     CheckResult = iota // none asked, or it runs
	CheckUpToDate                    // the app is the newest version
	CheckFound                       // a newer version: see State.Version
	CheckFailed                      // the feed did not answer
)

// State is what the panel shows.
type State struct {
	// Enabled: the app can update itself. When it cannot (a development
	// build, a bundle the user cannot replace), nothing else is set.
	Enabled bool
	Phase   Phase
	// Version is the release found, downloading or ready.
	Version string
	// Done and Total are the bytes downloaded and to download.
	Done, Total int64
	// Check is the result of the last check the user asked for; CheckSeq
	// grows with each one that ends.
	Check    CheckResult
	CheckSeq int
}

const (
	// firstWait is how long after launch the first check runs: a version
	// that came out while the app was off shows soon, without slowing the
	// start.
	firstWait = 30 * time.Second
	interval  = 24 * time.Hour
	// retryWait is the next try after a check or download that failed.
	retryWait = time.Hour
	timeout   = 30 * time.Minute
)

// Options configure an Updater.
type Options struct {
	Source Source
	// Automatic reports whether a release found in the background is
	// downloaded at once; read at each check.
	Automatic func() bool
	// OnChange gets each new state, on the updater's goroutine.
	OnChange func(State)
	// After is the clock of the checks; nil means time.After.
	After func(time.Duration) <-chan time.Time
}

// Updater checks for and installs new versions of the app.
type Updater struct {
	o    Options
	cmds chan func()
	done chan struct{}
	stop context.CancelFunc
	ctx  context.Context

	mu      sync.Mutex
	st      State
	release Release
	// failed: the last background check did not reach the feed; the next
	// one comes sooner.
	failed bool
}

// New returns an updater; Start begins its checks.
func New(o Options) *Updater {
	if o.After == nil {
		o.After = time.After
	}
	if o.Automatic == nil {
		o.Automatic = func() bool { return true }
	}
	ctx, stop := context.WithCancel(context.Background())
	u := &Updater{o: o, cmds: make(chan func(), 8), done: make(chan struct{}), ctx: ctx, stop: stop}
	u.st.Enabled = o.Source != nil && o.Source.Enabled()
	return u
}

// Start runs the checks in the background: soon after launch, then daily.
func (u *Updater) Start() {
	if !u.st.Enabled {
		return
	}
	go u.loop()
}

// Close stops the checks and a download in progress.
func (u *Updater) Close() {
	u.stop()
}

// State is the current state.
func (u *Updater) State() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// Check asks the feed now, for the user: the answer comes in State.Check.
// A release found is downloaded as a background check's would be.
func (u *Updater) Check() { u.send(func() { u.check(true) }) }

// Download downloads and installs the release found, when automatic
// downloads are off, or again after a failed one.
func (u *Updater) Download() {
	u.send(func() {
		u.mu.Lock()
		ok := u.release != nil && (u.st.Phase == PhaseAvailable || u.st.Phase == PhaseFailed)
		u.mu.Unlock()
		if ok {
			u.download()
		}
	})
}

// send hands f to the updater's goroutine without waiting: the panel
// calls from the UI thread, and a download keeps the goroutine busy. A
// command that finds the queue full is dropped; the panel offers no
// buttons while a download runs.
func (u *Updater) send(f func()) {
	if !u.st.Enabled {
		return
	}
	select {
	case u.cmds <- f:
	default:
	}
}

func (u *Updater) loop() {
	wait := u.o.After(firstWait)
	for {
		select {
		case <-u.ctx.Done():
			return
		case f := <-u.cmds:
			f()
		case <-wait:
			u.check(false)
		}
		// The next background check: a day after the last one, sooner after
		// a failure; none once a version waits for the restart.
		u.mu.Lock()
		phase, failed := u.st.Phase, u.failed
		u.mu.Unlock()
		switch {
		case phase == PhaseReady:
			wait = nil
		case phase == PhaseFailed || failed:
			wait = u.o.After(retryWait)
		default:
			wait = u.o.After(interval)
		}
	}
}

// check asks the feed and, when there is a newer release, downloads it
// (automatic) or offers it (PhaseAvailable).
func (u *Updater) check(manual bool) {
	u.mu.Lock()
	if u.st.Phase == PhaseDownloading || u.st.Phase == PhaseReady {
		// Nothing to ask: the newest version is on its way or installed.
		if manual {
			u.st.Check, u.st.CheckSeq = CheckFound, u.st.CheckSeq+1
		}
		st := u.st
		u.mu.Unlock()
		u.changed(st)
		return
	}
	prev := u.st.Phase
	u.st.Phase = PhaseChecking
	if manual {
		u.st.Check = CheckNone
	}
	st := u.st
	u.mu.Unlock()
	u.changed(st)

	ctx, cancel := context.WithTimeout(u.ctx, time.Minute)
	r, err := u.o.Source.Check(ctx)
	cancel()
	if errors.Is(err, context.Canceled) && u.ctx.Err() != nil {
		return
	}

	u.mu.Lock()
	u.failed = err != nil && !manual
	switch {
	case err != nil:
		// A background failure keeps what was shown: the user sees a
		// failed check only when they asked for one.
		u.st.Phase = prev
		if manual {
			u.st.Check = CheckFailed
		}
	case r == nil:
		u.st.Phase, u.st.Version, u.release = PhaseIdle, "", nil
		if manual {
			u.st.Check = CheckUpToDate
		}
	default:
		u.st.Phase, u.st.Version, u.release = PhaseAvailable, r.Version(), r
		if manual {
			u.st.Check = CheckFound
		}
	}
	if manual {
		u.st.CheckSeq++
	}
	found := u.st.Phase == PhaseAvailable
	st = u.st
	u.mu.Unlock()
	u.changed(st)
	if found && u.o.Automatic() {
		u.download()
	}
}

// download installs the release found; the app runs it after a restart.
func (u *Updater) download() {
	u.mu.Lock()
	r := u.release
	u.st.Phase, u.st.Done, u.st.Total = PhaseDownloading, 0, 0
	st := u.st
	u.mu.Unlock()
	u.changed(st)

	ctx, cancel := context.WithTimeout(u.ctx, timeout)
	defer cancel()
	last := time.Time{}
	err := r.Install(ctx, func(done, total int64) {
		// A few updates a second are enough for a progress bar.
		if now := time.Now(); now.Sub(last) >= 200*time.Millisecond || done == total {
			last = now
			u.mu.Lock()
			u.st.Done, u.st.Total = done, total
			st := u.st
			u.mu.Unlock()
			u.changed(st)
		}
	})
	if u.ctx.Err() != nil {
		return
	}
	u.mu.Lock()
	if err != nil {
		u.st.Phase = PhaseFailed
	} else {
		u.st.Phase = PhaseReady
	}
	st = u.st
	u.mu.Unlock()
	u.changed(st)
}

func (u *Updater) changed(st State) {
	if u.o.OnChange != nil {
		u.o.OnChange(st)
	}
}
