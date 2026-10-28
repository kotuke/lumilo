package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// Timing.
const (
	probeInterval       = time.Second      // probe a served or loading server
	externalScanEvery   = 2 * time.Second  // re-scan for a foreign server
	hangAfter           = 12 * time.Second // ready server silent this long = unresponsive
	stoppingLongAfter   = 12 * time.Second // a stop this long sets StoppingLong
	firstUpdateCheck    = 10 * time.Second // after the engine is first detected
	updateCheckInterval = 24 * time.Hour
	closeWait           = 10 * time.Second // Close: graceful stop before force
	updateStopWait      = 20 * time.Second // Update: graceful stop before force
	portDialTimeout     = 300 * time.Millisecond
	probeTimeout        = 3 * time.Second
	detectTimeout       = 30 * time.Second
	removeTimeout       = 2 * time.Minute // RemoveModel: deleting the files of a model
	feedTimeout         = 30 * time.Second
)

const (
	tailLines     = 50  // output lines kept for ClassifyFailure
	freePortRange = 200 // ports tried above a busy one
	maxLine       = 64 << 10
)

// Settings are the user's choices that shape the server.
type Settings struct {
	engine.Config
	// LANAddress is the computer's address on the local network, "" when
	// unknown. In AccessNetwork it is the address other devices use, so the
	// view shows it instead of 0.0.0.0.
	LANAddress string
	// Contexts is the context window chosen for each model, by engine ID
	// and Model.ID; a missing one is the engine's choice. The server gets
	// the one of the engine and model it runs: Config.ContextTokens is
	// ignored.
	Contexts map[ModelKey]int
	// Options are the vision and draft choices of each model, by engine ID
	// and Model.ID; the server gets the ones of the engine and model it
	// runs: Config.NoVision and Config.Draft are ignored.
	Options map[ModelKey]engine.ModelOptions
}

// ModelKey names a model of an engine: two engines may name models alike.
type ModelKey struct{ Engine, Model string }

// Options configures a Supervisor.
type Options struct {
	// Engine is the engine to run first; Engines are all the engines the
	// user can switch to (SwitchEngine), Engine among them. Engines may be
	// empty: then Engine is the only one.
	Engine  engine.Engine
	Engines []engine.Engine
	// Settings are the initial settings.
	Settings Settings
	// LogDir is where the server output log is written.
	LogDir string
	// OnChange is called with every new View, from a background goroutine.
	// It must not call Close.
	OnChange func(View)
	// OpenTerminal runs argv with env in a new Terminal window.
	OpenTerminal func(argv, env []string) error
	// OnModel is called when a downloaded model became the selected one
	// (Download), so the app can remember it. It runs on a background
	// goroutine.
	OnModel func(engineID, modelID string)
	// OnRemove is called when a model was deleted (RemoveModel), so the
	// app forgets what it stored for it. It runs on a background goroutine.
	OnRemove func(engineID, modelID string)
	// JobsFile keeps the downloads that did not finish (DownloadModel)
	// across runs; "" keeps them for this run only. A new supervisor shows
	// the ones it holds as paused.
	JobsFile string
}

// deps are the supervisor's links to the operating system.
type deps struct {
	clock     clock
	starter   starter
	signalPID func(pid int, sig syscall.Signal) error
	manual    bool // no background tick loop; tests call tick
}

// Supervisor runs one engine's server. Commands are safe to call from any
// goroutine and return at once, except Close; work happens in the
// background and results arrive through Options.OnChange:
//
//	Start()                     start our server with the current settings
//	StartOn(port int)           start on another port for this run only (after
//	                            a port conflict); Settings keep their port
//	Stop()                      stop our server gracefully
//	ForceStop()                 force our stuck or slow server to quit
//	Restart()                   stop, then start with the current settings
//	KillExternal()              stop a server that Lumilo did not start
//	Release()                   unload the weights, keep the server
//	Restore()                   load the weights back
//	SelectModel(id string)      pick a model; restarts a running server
//	Apply(s Settings)           store new settings; Restart applies them
//	PendingRestart() bool       settings differ from the running server's
//	Install()                   install the engine with its Installer
//	Update()                    stop our server, upgrade, start again
//	CheckUpdate()               ask the release feed now; View.Checking and
//	                            View.Check report it
//	RunAgent(id string)         start the server if needed, then the agent
//	Download(id string)         start the server online with a model it
//	                            downloads first (engine.ParseModel syntax);
//	                            once it serves, the model is the selected one
//	                            (an engine without engine.Downloader only)
//	DownloadModel(id, o)        download a model apart from the server
//	                            (View.Downloads); CancelDownload and
//	                            DismissDownload end its job
//	SetModelOptions(id, o)      store a model's vision and draft choices
//	                            before a Start queued after it
//	SwitchEngine(id, model)     stop our server, then run another engine
//	                            with model ("" = the first one it has)
//	Recheck()                   detect the engine and its installer again
//	View() View                 the current view
//	Settings() Settings         the current settings
//	Close()                     stop our server and the background work; blocks
//
// State-changing commands run in order on one worker goroutine; long
// work (installs, network calls, agent start) runs on its own goroutines.
type Supervisor struct {
	opts Options
	d    deps

	// The engine being run and what comes with it; guarded by mu, read
	// without it through engine(). SwitchEngine replaces them on the worker.
	eng     engine.Engine
	logPath string
	agents  []engine.Agent
	all     []engine.Engine // the engines to switch to, eng among them
	engines []EngineInfo    // what detect found about each of all
	// switching is set from the stop of our server for a switch until the
	// new engine is in place; no server starts meanwhile.
	switching bool
	// removal is the deletion of a model (RemoveModel) and its count
	// (PlanRemove); while it runs, no server starts.
	removal Removal
	// epoch counts engine switches: a result of the old engine's work
	// (feed, install) that arrives after a switch is dropped.
	epoch int

	// dl is the model of a download run (Download): the server runs it
	// online until it serves, then it becomes the selected model. "" = none.
	dl string
	// dlTotal is what the engine said the download fetches; dlDone what
	// dlMeter (Engine.WatchDownload, nil = none) measured of it so far;
	// dlLoaded is set once the run started to load the model (the files
	// are there). A start of the run resets them.
	dlTotal, dlDone int64
	dlMeter         func() int64
	dlLoaded        bool
	// jobs are the downloads apart from the server (DownloadModel), in
	// the order they were asked, of every engine; drops are the partial
	// files of removed jobs that wait for their engine's download to end.
	jobs  []*job
	drops []partialDrop
	// saveMu makes saveJobs write one at a time; savedEmpty: the file
	// holds no job.
	saveMu     sync.Mutex
	savedEmpty bool

	cmds    chan func()
	dirty   chan struct{}
	done    chan struct{} // closed by Close
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	startMu sync.Mutex // serializes server starts and Close
	probeMu sync.Mutex // one probe at a time, applied in the order it was taken
	once    sync.Once

	mu   sync.Mutex
	cond *sync.Cond // broadcast after every change

	closed    bool
	settings  Settings
	inst      engine.Install
	models    []engine.Model
	latest    string
	phase     Phase
	update    UpdatePhase
	updateErr string
	// updateReason says why the last update failed when brew itself did
	// not fail (View.UpdateReason).
	updateReason UpdateReason
	failure      engine.Failure
	freePort     int
	external     *engine.Running
	snap         engine.Snapshot
	meter        speedMeter
	tail         []string
	lastLine     string
	nextCheck    time.Time
	// The update check the user asked for (CheckUpdate): one runs while
	// checking; check is the result of the last one, checkSeq counts them.
	checking bool
	check    CheckResult
	checkSeq int
	lastScan time.Time

	// Our server.
	gen          int // bumped on every start; stale callbacks compare it
	proc         process
	procDone     chan struct{}
	runCfg       engine.Config
	override     portOverride // the port StartOn gave the running server
	restoreReq   bool         // our own Restore call is in flight
	stopReq      bool
	stopAt       time.Time
	stoppingLong bool
	killAt       time.Time // SIGKILL the group then; zero = none
	restartAfter bool      // start again once the server exits
	answeredAt   time.Time // last time the ready server answered
	activityAt   time.Time // last request activity, for IdleLeft

	// extKillPID is a foreign server to SIGKILL at extKillAt if it still
	// serves on extKillPort: the PID alone may belong to another program
	// by then.
	extKillPID  int
	extKillPort int
	extKillAt   time.Time

	// notice is the last notice for the panel (View.Notice).
	notice Notice

	// lastView is the view the publisher sent last; it skips equal ones.
	lastView *View
}

// New starts a supervisor. It detects the engine in the background.
func New(o Options) *Supervisor {
	return newSupervisor(o, deps{
		clock:     realClock{},
		starter:   execStarter{},
		signalPID: signalPID,
	})
}

func newSupervisor(o Options, d deps) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	all := slices.Clone(o.Engines)
	if !slices.Contains(all, o.Engine) {
		all = append([]engine.Engine{o.Engine}, all...)
	}
	s := &Supervisor{
		eng:      o.Engine,
		opts:     o,
		d:        d,
		logPath:  filepath.Join(o.LogDir, o.Engine.Info().ID+".log"),
		agents:   o.Engine.Info().Agents,
		all:      all,
		cmds:     make(chan func(), 64),
		dirty:    make(chan struct{}, 1),
		done:     make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		settings: o.Settings,
		phase:    PhaseStopped,
		jobs:     loadJobs(o.JobsFile, all),
	}
	s.cond = sync.NewCond(&s.mu)
	s.savedEmpty = len(s.jobs) == 0 // no file to write until a job comes
	s.goBG(s.worker)
	s.goBG(s.publisher)
	if !d.manual {
		s.goBG(s.tickLoop)
	}
	s.do(s.detect)
	return s
}

// engine is the engine being run, for code that does not hold s.mu.
func (s *Supervisor) engine() engine.Engine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eng
}

// Commands.

func (s *Supervisor) Start() { s.do(s.startCmd) }

// Download starts the server online with the model id, which the engine
// downloads before it loads it. A running server of ours restarts with it.
// The run keeps id as its model until it serves; then the model, as the
// engine lists it, becomes the selected one and Options.OnModel hears of
// it. A stop, another model or a switch drops the download; a failure
// keeps it, so Start tries again.
func (s *Supervisor) Download(id string) {
	s.do(func() {
		s.mu.Lock()
		// A foreign server, or ours on its way down, cannot take the
		// download: dl would wait for the next Start, which is not a
		// download, and hijack it.
		if s.closed || s.switching || s.removal.Running || !s.inst.Installed || id == "" || s.stopReq || s.phase == PhaseExternal {
			s.mu.Unlock()
			return
		}
		s.dropDownloadLocked()
		s.dl = id
		if s.proc != nil {
			s.restartAfter = true
			s.stopLocked(false)
			s.mu.Unlock()
			s.notify()
			return
		}
		s.mu.Unlock()
		s.notify()
		s.startServer(nil, false)
		s.mu.Lock()
		// No run began (a foreign server took the port, an update runs...):
		// forget the download. A failed start keeps it, so Start retries.
		if s.proc == nil && s.phase != PhaseError {
			s.dropDownloadLocked()
		}
		s.mu.Unlock()
		s.notify()
	})
}

// ParseModel checks a model name in the syntax of the engine being run
// (engine.Engine.ParseModel).
func (s *Supervisor) ParseModel(spec string) (engine.Model, error) {
	return s.engine().ParseModel(spec)
}

// dropDownloadLocked forgets the download run.
func (s *Supervisor) dropDownloadLocked() {
	s.dl, s.dlTotal, s.dlDone, s.dlMeter, s.dlLoaded = "", 0, 0, nil, false
}

// ModelSearch says where to look for models to download for the engine being
// run (engine.Info.ModelSearch).
func (s *Supervisor) ModelSearch() engine.SearchSpec {
	return s.engine().Info().ModelSearch
}

// SwitchEngine runs the engine with ID id instead of the current one, with
// model selected ("" = the first it has). Our server stops first (the panel
// asks before); a server Lumilo did not start is never touched, only
// forgotten. Nothing happens while an install or update runs, or for an
// unknown engine.
func (s *Supervisor) SwitchEngine(id, model string) {
	var next engine.Engine
	for _, e := range s.all {
		if e.Info().ID == id {
			next = e
		}
	}
	if next == nil {
		return
	}
	s.goBG(func() {
		// startMu lets a start in progress finish first, so its process is
		// the one stopped below; once switching is set, none begins.
		s.startMu.Lock()
		s.mu.Lock()
		if s.closed || s.switching || s.removal.Running || s.eng == next || s.phase == PhaseInstalling || s.update == UpdateUpdating {
			s.mu.Unlock()
			s.startMu.Unlock()
			return
		}
		s.switching = true
		s.restartAfter = false
		s.mu.Unlock()
		s.startMu.Unlock()
		s.notify()

		s.stopAndWait(closeWait)
		s.doWait(func() { s.swapEngine(next, model) })
	})
}

// swapEngine puts next in place of the current engine and detects it. It
// runs on the worker, after our server stopped.
func (s *Supervisor) swapEngine(next engine.Engine, model string) {
	s.mu.Lock()
	old := s.eng
	s.eng = next
	s.epoch++
	s.gen++ // late lines and probes of the old server are dropped
	s.logPath = filepath.Join(s.opts.LogDir, next.Info().ID+".log")
	s.agents = next.Info().Agents
	s.inst = engine.Install{}
	s.models = nil
	s.latest, s.update, s.updateErr, s.updateReason = "", UpdateNone, "", UpdateReasonNone
	s.checking, s.check = false, CheckNone
	s.nextCheck = time.Time{}
	s.extKillPID, s.extKillPort = 0, 0
	s.override = portOverride{}
	s.dropDownloadLocked()
	s.removal = Removal{}       // a count of the old engine's model
	s.holdJobsLocked(old, true) // they wait for the engine to run again
	s.tail, s.lastLine = nil, ""
	s.settings.Model = model
	s.setStoppedLocked()
	s.switching = false
	s.mu.Unlock()
	s.saveJobs()
	s.notify()
	s.detect()
}

// StartOn starts the server on port for this run only: the settings keep
// their port, so the next start tries it again, and a server on port does
// not count as running with other settings (PendingRestart). A restart
// (model switch, Restart, update) stays on port while the settings' port is
// the one it replaced; a stop or a failure ends the run.
func (s *Supervisor) StartOn(port int) {
	s.do(func() {
		s.mu.Lock()
		cfg := s.resolvedLocked()
		s.override = portOverride{from: cfg.Port, to: port}
		cfg.Port = port
		s.mu.Unlock()
		s.startServer(&cfg, false)
		s.mu.Lock()
		if s.proc == nil { // the start did not happen
			s.override = portOverride{}
		}
		s.mu.Unlock()
		s.notify()
	})
}

func (s *Supervisor) Stop() {
	s.do(func() {
		s.mu.Lock()
		s.restartAfter, s.dl = false, ""
		s.stopLocked(false)
		s.mu.Unlock()
		s.notify()
	})
}

func (s *Supervisor) ForceStop() {
	s.do(func() {
		s.mu.Lock()
		s.restartAfter, s.dl = false, ""
		s.stopLocked(true)
		s.mu.Unlock()
		s.notify()
	})
}

func (s *Supervisor) Restart() { s.do(s.restartCmd) }

func (s *Supervisor) KillExternal() {
	s.do(func() {
		s.mu.Lock()
		if s.phase != PhaseExternal || s.external == nil {
			s.mu.Unlock()
			return
		}
		pid := s.external.PID
		_ = s.d.signalPID(pid, syscall.SIGINT)
		s.extKillPID, s.extKillPort = pid, s.external.Port
		s.extKillAt = s.d.clock.Now().Add(s.eng.StopPolicy().KillWait)
		s.setStoppedLocked()
		s.mu.Unlock()
		s.notify()
	})
}

func (s *Supervisor) Release() { s.goBG(func() { s.weights(s.engine().Release, false) }) }
func (s *Supervisor) Restore() { s.goBG(func() { s.weights(s.engine().Restore, true) }) }

func (s *Supervisor) SelectModel(id string) {
	s.do(func() {
		s.mu.Lock()
		s.settings.Model, s.dl = id, ""
		if s.proc != nil && !s.stopReq && s.runCfg.Model != id {
			s.restartAfter = true
			s.stopLocked(false)
		}
		s.mu.Unlock()
		s.notify()
	})
}

// Apply stores new settings; Restart applies them to a running server.
// Config.Model is ignored: the supervisor owns the selected model
// (SelectModel, SwitchEngine, Download), and a model chosen after the
// caller read Settings must not be rolled back.
func (s *Supervisor) Apply(st Settings) {
	st.AllowedHosts = slices.Clone(st.AllowedHosts)
	st.Contexts = maps.Clone(st.Contexts)
	st.Options = maps.Clone(st.Options)
	s.do(func() {
		s.mu.Lock()
		st.Model = s.settings.Model
		s.settings = st
		s.mu.Unlock()
		s.notify()
	})
}

// PendingRestart reports whether our running server uses other settings
// than the current ones.
func (s *Supervisor) PendingRestart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingLocked()
}

// Settings returns the current settings.
func (s *Supervisor) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.settings
	st.AllowedHosts = slices.Clone(st.AllowedHosts)
	st.Contexts = maps.Clone(st.Contexts)
	st.Options = maps.Clone(st.Options)
	return st
}

func (s *Supervisor) Install() { s.goBG(s.runInstall) }
func (s *Supervisor) Update()  { s.goBG(s.runUpdate) }
func (s *Supervisor) Recheck() { s.do(s.detect) }

// CheckUpdate asks the release feed for the newest version now, for the
// user: View.Checking is set at once, before it returns, and View.Check
// holds the result once the feed answered. A call while such a check runs
// does nothing.
func (s *Supervisor) CheckUpdate() {
	s.mu.Lock()
	if s.closed || s.checking {
		s.mu.Unlock()
		return
	}
	s.checking, s.check = true, CheckNone
	s.checkSeq++
	s.mu.Unlock()
	s.notify()
	s.goBG(func() { s.checkUpdate(true) })
}

func (s *Supervisor) RunAgent(id string) { s.goBG(func() { s.runAgent(id) }) }

// Close stops our server (gracefully, then by force) and all background
// work. It blocks until done; OnChange is not called after it returns.
func (s *Supervisor) Close() {
	s.once.Do(func() {
		s.startMu.Lock()
		s.mu.Lock()
		s.closed = true
		s.restartAfter = false
		s.mu.Unlock()
		s.startMu.Unlock()
		s.stopAndWait(closeWait)
		close(s.done)
		s.cancel()
		s.cond.Broadcast()
		s.wg.Wait()
		s.saveJobs() // the stopped downloads, paused, with what they fetched
	})
}

// Background plumbing.

func (s *Supervisor) goBG(f func()) {
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.wg.Add(1)
	}
	s.mu.Unlock()
	if closed {
		return
	}
	go func() {
		defer s.wg.Done()
		f()
	}()
}

// do queues f on the worker.
func (s *Supervisor) do(f func()) {
	select {
	case s.cmds <- f:
	case <-s.done:
	}
}

// doWait queues f on the worker and waits for it to run.
func (s *Supervisor) doWait(f func()) bool {
	ran := make(chan struct{})
	s.do(func() { f(); close(ran) })
	select {
	case <-ran:
		return true
	case <-s.done:
		return false
	}
}

func (s *Supervisor) worker() {
	for {
		select {
		case f := <-s.cmds:
			f()
		case <-s.done:
			return
		}
	}
}

// notify publishes a new View and wakes waiters. Call it without s.mu.
func (s *Supervisor) notify() {
	s.cond.Broadcast()
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

func (s *Supervisor) publisher() {
	for {
		select {
		case <-s.dirty:
			if s.opts.OnChange == nil {
				continue
			}
			// notify runs on every tick and output line; a view equal to
			// the last one sent would only make the panel build a frame.
			// Fields that change with time (IdleLeft, Speed, StoppingLong)
			// make the view differ when they change.
			v := s.View()
			if s.lastView != nil && reflect.DeepEqual(*s.lastView, v) {
				continue
			}
			s.lastView = &v
			s.opts.OnChange(v)
		case <-s.done:
			return
		}
	}
}

func (s *Supervisor) tickLoop() {
	for {
		select {
		case <-s.d.clock.After(probeInterval):
			s.tick()
		case <-s.done:
			return
		}
	}
}

// tick runs the timers, the external re-scan and one probe.
func (s *Supervisor) tick() {
	now := s.d.clock.Now()
	s.mu.Lock()
	if s.proc != nil && s.stopReq && !s.stoppingLong && now.Sub(s.stopAt) >= stoppingLongAfter {
		s.stoppingLong = true
	}
	if s.proc != nil && !s.killAt.IsZero() && !now.Before(s.killAt) {
		_ = s.proc.Signal(syscall.SIGKILL)
		s.killAt = time.Time{}
	}
	var killPID, killPort int
	if s.extKillPID != 0 && !now.Before(s.extKillAt) {
		killPID, killPort = s.extKillPID, s.extKillPort
		s.extKillPID, s.extKillPort = 0, 0
	}
	check := s.inst.Installed && !s.nextCheck.IsZero() && !now.Before(s.nextCheck)
	if check {
		s.nextCheck = now.Add(updateCheckInterval)
	}
	// Re-scan while showing a foreign server (it may exit) and while stopped
	// with no server of ours (one may be started elsewhere).
	idle := s.proc == nil && s.inst.Installed && (s.phase == PhaseStopped || s.phase == PhaseError)
	scan := (s.phase == PhaseExternal || idle) && now.Sub(s.lastScan) >= externalScanEvery
	if scan {
		s.lastScan = now
	}
	var meter func() int64
	if s.downloadingLocked() {
		meter = s.dlMeter
	}
	gen := s.gen
	s.mu.Unlock()

	if meter != nil {
		// The meter walks folders: not under s.mu.
		n := meter()
		s.mu.Lock()
		if gen == s.gen && s.downloadingLocked() && n > s.dlDone {
			s.dlDone = n
		}
		s.mu.Unlock()
	}

	if killPID != 0 {
		s.killExternalNow(killPID, killPort)
	}
	if check {
		s.goBG(func() { s.checkUpdate(false) })
	}
	if scan {
		s.rescanExternal()
	}
	s.probe()
	s.notify()
}

// killExternalNow sends SIGKILL to the foreign server pid that KillExternal
// asked to quit, if it still serves on port. A PID that is no longer that
// server's (it quit, and the number went to another program) is left
// alone, as is one the engine cannot find any more.
func (s *Supervisor) killExternalNow(pid, port int) {
	if s.d.signalPID(pid, 0) != nil {
		return
	}
	rs, err := s.engine().FindRunning()
	if err != nil {
		return
	}
	for _, r := range rs {
		if r.PID == pid && r.Port == port {
			_ = s.d.signalPID(pid, syscall.SIGKILL)
			return
		}
	}
}

// Detection.

func (s *Supervisor) detect() {
	ctx, cancel := context.WithTimeout(s.ctx, detectTimeout)
	defer cancel()
	eng := s.engine()
	inst := eng.Installer().Detect(ctx)
	var models []engine.Model
	var running []engine.Running
	if inst.Installed {
		models, _ = eng.Models(ctx)
		running, _ = eng.FindRunning()
	}
	// The others, for the engine list of the settings: a look at fixed
	// paths, no process for the engines Lumilo has.
	infos := make([]EngineInfo, 0, len(s.all))
	for _, e := range s.all {
		in := inst
		if e != eng {
			in = e.Installer().Detect(ctx)
		}
		info := e.Info()
		infos = append(infos, EngineInfo{ID: info.ID, Name: info.Name, Logo: info.Logo, Installed: in.Installed,
			Version: in.Version, Install: e.Installer().Describe()})
	}

	s.mu.Lock()
	s.engines = infos
	s.inst = inst
	s.models = models
	s.pickModelLocked()
	// A failed update is stale once the installed version is the newest
	// known (updated by hand, or by a later brew run).
	if s.update == UpdateFailed && inst.Installed && s.latest != "" && compareVersions(inst.Version, s.latest) >= 0 {
		s.update, s.updateErr, s.updateReason = UpdateNone, "", UpdateReasonNone
	}
	if inst.Installed && s.nextCheck.IsZero() {
		s.nextCheck = s.d.clock.Now().Add(firstUpdateCheck)
	}
	switch s.phase {
	case PhaseStopped, PhaseNotInstalled, PhaseNoInstaller, PhaseInstallFailed, PhaseExternal:
		if s.proc != nil {
			break
		}
		switch {
		case !inst.Installed && inst.Ready:
			s.setPhaseLocked(PhaseNotInstalled)
		case !inst.Installed:
			s.setPhaseLocked(PhaseNoInstaller)
		default:
			if r, ok := pickRunning(running, s.settings.Port, 0); ok {
				if s.external == nil || s.external.PID != r.PID {
					s.setExternalLocked(r)
				}
			} else {
				s.setStoppedLocked()
			}
		}
	}
	s.mu.Unlock()
	s.notify()
}

// pickModelLocked keeps the selected model if it is installed, else picks
// the first one.
func (s *Supervisor) pickModelLocked() {
	if len(s.models) == 0 {
		return
	}
	for _, m := range s.models {
		if m.ID == s.settings.Model {
			return
		}
	}
	s.settings.Model = s.models[0].ID
}

// pickRunning prefers a server on port; ownPID is skipped.
func pickRunning(rs []engine.Running, port, ownPID int) (engine.Running, bool) {
	var first *engine.Running
	for i := range rs {
		if rs[i].PID == ownPID && ownPID != 0 {
			continue
		}
		if rs[i].Port == port {
			return rs[i], true
		}
		if first == nil {
			first = &rs[i]
		}
	}
	if first == nil {
		return engine.Running{}, false
	}
	return *first, true
}

func (s *Supervisor) rescanExternal() {
	rs, err := s.engine().FindRunning()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil && (s.phase == PhaseStopped || s.phase == PhaseError) {
		// Skip a foreign server we are stopping right now.
		if r, ok := pickRunning(rs, s.settings.Port, s.extKillPID); ok {
			s.setExternalLocked(r)
		}
		return
	}
	if s.phase != PhaseExternal || s.external == nil {
		return
	}
	for _, r := range rs {
		if r.PID == s.external.PID {
			return
		}
	}
	s.setStoppedLocked()
}

// Starting and stopping our server.

func (s *Supervisor) startCmd() {
	s.mu.Lock()
	broken := s.proc != nil && s.phase == PhaseError
	s.mu.Unlock()
	if broken { // alive but unable to serve
		s.restartCmd()
		return
	}
	s.startServer(nil, false)
}

func (s *Supervisor) restartCmd() {
	s.mu.Lock()
	if s.proc != nil {
		s.stopLocked(false)
		s.restartAfter = true
		s.mu.Unlock()
		s.notify()
		return
	}
	s.mu.Unlock()
	s.startServer(nil, false)
}

// startServer runs preflight checks and spawns the server with cfg (nil =
// the current settings). appendLog keeps the log (after a brew run).
func (s *Supervisor) startServer(cfg *engine.Config, appendLog bool) {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	if s.closed || s.switching || s.removal.Running || !s.inst.Installed || s.proc != nil || s.update == UpdateUpdating ||
		(s.phase != PhaseStopped && s.phase != PhaseError) {
		s.mu.Unlock()
		return
	}
	c := s.resolvedLocked()
	if cfg != nil {
		c = *cfg
	} else if o := s.override; o.to != 0 && c.Port == o.from {
		c.Port = o.to // a restart (model switch, new settings) keeps the run's port
	} else {
		s.override = portOverride{}
	}
	if c.Access == engine.AccessNetwork && c.APIKey == "" {
		// Every interface without a key: anyone on the network could use
		// the server. Refuse rather than serve open.
		s.failLocked(engine.Failure{Kind: engine.FailNoKey}, 0)
		s.mu.Unlock()
		s.notify()
		return
	}
	s.mu.Unlock()

	if rs, err := s.engine().FindRunning(); err == nil {
		for _, r := range rs {
			if r.Port == c.Port {
				s.mu.Lock()
				s.setExternalLocked(r)
				s.mu.Unlock()
				s.notify()
				return
			}
		}
	}
	busy, err := portBusy(c.Host, c.Port)
	if busy || err != nil {
		f := engine.Failure{Kind: engine.FailPortBusy}
		free := 0
		if busy {
			free = findFreePort(c.Host, c.Port)
		} else {
			f = engine.Failure{Kind: engine.FailExit, Code: -1, Line: err.Error()}
		}
		s.mu.Lock()
		s.failLocked(f, free)
		s.mu.Unlock()
		s.notify()
		return
	}

	argv, env := s.engine().ServeCommand(c)
	logf := s.openLog(!appendLog)
	s.mu.Lock()
	dl := s.dl
	s.mu.Unlock()
	var meter func() int64
	if dl != "" && dl == c.Model {
		meter = s.engine().WatchDownload(dl) // its baseline: before the server starts
	}

	s.mu.Lock()
	s.gen++
	gen := s.gen
	s.tail = nil
	s.lastLine = ""
	s.external = nil
	s.snap = engine.Snapshot{}
	s.meter.reset(true)
	s.failure = engine.Failure{}
	s.freePort = 0
	s.stopReq, s.stoppingLong, s.restartAfter = false, false, false
	s.killAt = time.Time{}
	s.runCfg = c
	s.restoreReq = false
	s.dlTotal, s.dlDone, s.dlMeter, s.dlLoaded = 0, 0, meter, false
	s.setPhaseLocked(PhaseLoading)
	s.mu.Unlock()
	s.notify()

	var logw io.Writer
	if logf != nil {
		logw = logf
	}
	p, err := s.d.starter.Start(argv, env,
		&lineWriter{s: s, gen: gen, log: logw},
		&lineWriter{s: s, gen: gen, log: logw, stderr: true})
	if err != nil {
		if logf != nil {
			logf.Close()
		}
		s.mu.Lock()
		s.failLocked(engine.Failure{Kind: engine.FailExit, Code: -1, Line: err.Error()}, 0)
		s.mu.Unlock()
		s.notify()
		return
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.proc = p
	s.procDone = done
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.waitExit(gen, p, done, logf)
	}()
}

func (s *Supervisor) waitExit(gen int, p process, done chan struct{}, logf *os.File) {
	code := p.Wait()
	if logf != nil {
		logf.Close()
	}
	s.mu.Lock()
	stopped := s.stopReq
	tail := slices.Clone(s.tail)
	host, port, own := s.runCfg.Host, s.runCfg.Port, p.Pid()
	online := s.runCfg.ModelUpdates
	s.mu.Unlock()

	var f engine.Failure
	var free int
	var ext *engine.Running
	if !stopped {
		f = s.engine().ClassifyFailure(code, tail)
		switch f.Kind {
		case engine.FailAlreadyServing:
			if rs, err := s.engine().FindRunning(); err == nil {
				if r, ok := pickRunning(rs, port, own); ok {
					ext = &r
				}
			}
		case engine.FailPortBusy:
			free = findFreePort(host, port)
		case engine.FailModel:
			// Missing files mean "download them" only when the run did
			// not try to (offline); online it is a failed download.
			f.Offline = !online
		}
	}

	s.mu.Lock()
	// The process is gone whatever happened meanwhile: a process that
	// outlived stopAndWait (an engine switch then bumped gen) must not stay
	// in proc, or no server could start again.
	close(done)
	if s.proc == p {
		s.proc = nil
		s.procDone = nil
	}
	if gen != s.gen {
		// A later run or engine owns the phase now.
		s.mu.Unlock()
		s.notify()
		return
	}
	restart := s.restartAfter && !s.closed
	s.restartAfter = false
	if !restart {
		s.override = portOverride{} // the run ended: the settings' port again
	}
	switch {
	case stopped:
		s.setStoppedLocked()
	case ext != nil:
		s.setExternalLocked(*ext)
	default:
		s.failLocked(f, free)
	}
	s.mu.Unlock()
	s.notify()
	if restart {
		s.do(func() { s.startServer(nil, false) })
	}
}

// stopLocked sends the stop signal to our server. force sends the Force
// signal and arms the SIGKILL timer.
func (s *Supervisor) stopLocked(force bool) {
	if s.proc == nil {
		return
	}
	pol := s.eng.StopPolicy()
	now := s.d.clock.Now()
	if !s.stopReq {
		s.stopReq = true
		s.stopAt = now
		s.setPhaseLocked(PhaseStopping)
		if !force {
			_ = s.proc.Signal(pol.Graceful)
		}
	}
	if force {
		_ = s.proc.Signal(pol.Force)
		s.killAt = now.Add(pol.KillWait)
	}
}

// stopAndWait stops our server and waits for it to exit: gracefully for
// up to wait, then by force.
func (s *Supervisor) stopAndWait(wait time.Duration) {
	s.mu.Lock()
	done := s.procDone
	s.stopLocked(false)
	s.mu.Unlock()
	s.notify()
	if done == nil || s.waitFor(done, wait) {
		return
	}
	s.mu.Lock()
	s.stopLocked(true)
	s.mu.Unlock()
	s.notify()
	if s.waitFor(done, s.engine().StopPolicy().KillWait) {
		return
	}
	s.mu.Lock()
	if s.proc != nil {
		_ = s.proc.Signal(syscall.SIGKILL)
	}
	s.mu.Unlock()
	s.waitFor(done, waitDelay+time.Second)
}

// waitFor waits for ch or the timeout; it reports whether ch closed.
func (s *Supervisor) waitFor(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-s.d.clock.After(d):
		return false
	}
}

// Probing.

// probeSoon probes at once in the background, without waiting for the next
// tick. It queues behind a probe that runs.
func (s *Supervisor) probeSoon() {
	s.goBG(func() {
		s.probe()
		s.notify()
	})
}

// probe takes one snapshot and applies it. Probes never overlap: a later
// one starts after the earlier one was applied, so snapshots reach the
// speed meter in the order they were taken.
func (s *Supervisor) probe() {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	s.mu.Lock()
	gen, extPID := s.gen, 0
	var t engine.Target
	switch {
	case s.proc != nil && !s.stopReq && (s.phase == PhaseLoading || s.phase == PhaseUnresponsive || s.phase.Served()):
		t = engine.Target{Port: s.runCfg.Port, APIKey: s.runCfg.APIKey}
	case s.phase == PhaseExternal && s.external != nil:
		extPID = s.external.PID
		t = engine.Target{Port: s.external.Port, APIKey: s.settings.APIKey}
	default:
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(s.ctx, probeTimeout)
	snap := s.engine().Probe(ctx, t)
	cancel()
	now := s.d.clock.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if extPID != 0 {
		s.applyExternalLocked(extPID, snap, now)
	} else {
		s.applyOwnLocked(gen, snap, now)
	}
}

func (s *Supervisor) applyOwnLocked(gen int, snap engine.Snapshot, now time.Time) {
	if gen != s.gen || s.proc == nil || s.stopReq {
		return
	}
	switch s.phase {
	case PhaseLoading:
		if !snap.Reachable || !snap.Ready {
			return // hangs while loading are expected
		}
		s.becomeReadyLocked(now)
	case PhaseReady, PhaseGenerating, PhaseReleased, PhaseRestoring, PhaseUnresponsive:
		if !snap.Reachable {
			if s.phase != PhaseUnresponsive && now.Sub(s.answeredAt) >= hangAfter {
				s.setPhaseLocked(PhaseUnresponsive)
			}
			return
		}
		s.answeredAt = now
	default:
		return
	}
	woke := s.snap.Released && !snap.Released
	s.snap = snap
	if snap.Broken {
		s.failLocked(engine.Failure{Kind: engine.FailExit, Line: s.lastLine}, 0)
		return
	}
	s.setPhaseLocked(servedPhase(snap, s.restoreReq))
	s.countLocked(snap, now)
	if woke {
		s.activityAt = now
	}
}

func (s *Supervisor) applyExternalLocked(pid int, snap engine.Snapshot, now time.Time) {
	if s.phase != PhaseExternal || s.external == nil || s.external.PID != pid || !snap.Reachable {
		return
	}
	if !s.snap.Reachable {
		s.activityAt = now // first answer counts as becoming ready
	}
	woke := s.snap.Released && !snap.Released
	s.snap = snap
	s.countLocked(snap, now)
	if woke {
		s.activityAt = now
	}
}

// countLocked feeds the speed meter and notes request activity, which the
// idle countdown (View.IdleLeft) runs from. The caller also restarts the
// countdown when the weights come back: the engine's own idle timer starts
// again then, whether or not a counted request woke it.
func (s *Supervisor) countLocked(snap engine.Snapshot, now time.Time) {
	if s.meter.add(snap, now) || snap.Active > 0 {
		s.activityAt = now
	}
}

func (s *Supervisor) becomeReadyLocked(now time.Time) {
	s.answeredAt = now
	s.activityAt = now
	s.setPhaseLocked(PhaseReady)
	if s.dl != "" && !s.closed {
		s.wg.Add(1) // goBG takes s.mu
		go func() {
			defer s.wg.Done()
			s.finishDownload()
		}()
	}
}

// finishDownload makes the model of a download run that now serves the
// selected one: as the engine lists it once downloaded (the request
// itself, or the file the server reports, as llama.cpp's GGUF path), or
// the request when it is not listed.
func (s *Supervisor) finishDownload() {
	s.mu.Lock()
	id, gen, eng := s.dl, s.gen, s.eng
	s.mu.Unlock()
	if id == "" {
		return
	}
	s.probe() // the snapshot names the model the server loaded
	ctx, cancel := context.WithTimeout(s.ctx, detectTimeout)
	models, err := eng.Models(ctx)
	cancel()

	s.mu.Lock()
	if gen != s.gen || s.dl != id || s.eng != eng {
		s.mu.Unlock()
		return
	}
	if err == nil {
		s.models = models
	}
	chosen := id
	if !slices.ContainsFunc(s.models, func(m engine.Model) bool { return m.ID == id }) && s.snap.Model != "" &&
		slices.ContainsFunc(s.models, func(m engine.Model) bool { return m.ID == s.snap.Model }) {
		chosen = s.snap.Model
	}
	s.dropDownloadLocked()
	s.settings.Model = chosen
	// The running server is the one the settings now ask for: no restart.
	want := s.resolvedLocked()
	s.runCfg.Model, s.runCfg.ModelUpdates, s.runCfg.ContextTokens = want.Model, want.ModelUpdates, want.ContextTokens
	s.runCfg.NoVision, s.runCfg.Draft = want.NoVision, want.Draft
	engID := eng.Info().ID
	s.mu.Unlock()
	s.notify()
	if s.opts.OnModel != nil {
		s.opts.OnModel(engID, chosen)
	}
}

// servedPhase is the phase of a server that answers. Weights on their way
// back (restoring: the engine says so, or our own Restore call runs) are
// not ready yet, whatever the other flags say.
func servedPhase(snap engine.Snapshot, restoring bool) Phase {
	switch {
	case snap.Restoring || restoring:
		return PhaseRestoring
	case snap.Released:
		return PhaseReleased
	case snap.Active > 0:
		return PhaseGenerating
	}
	return PhaseReady
}

// onLine handles one line of our server's output.
func (s *Supervisor) onLine(gen int, line string, stderr bool) {
	ev, ok := s.engine().ParseLine(line, stderr)
	s.mu.Lock()
	if gen != s.gen {
		s.mu.Unlock()
		return
	}
	if strings.TrimSpace(line) != "" {
		s.tail = append(s.tail, line)
		if len(s.tail) > tailLines {
			s.tail = slices.Delete(s.tail, 0, len(s.tail)-tailLines)
		}
		s.lastLine = line
	}
	if ok && s.dl != "" && s.phase == PhaseLoading {
		switch ev.Kind {
		case engine.EventDownloading:
			s.dlTotal += ev.Bytes
		case engine.EventLoading:
			s.dlLoaded = true // the files are there
		}
	}
	ready := ok && ev.Kind == engine.EventReady && s.phase == PhaseLoading
	if ready {
		s.becomeReadyLocked(s.d.clock.Now())
	}
	s.mu.Unlock()
	s.notify()
	if ready {
		// The first snapshot (memory, context) would wait for the next tick.
		s.probeSoon()
	}
}

// Weights.

// weights runs a Release or Restore. A Restore of our own server shows as
// PhaseRestoring from the click to the end of the call, before the engine
// reports it; a probe then shows the result at once.
func (s *Supervisor) weights(op func(context.Context, engine.Target) error, restore bool) {
	s.mu.Lock()
	var t engine.Target
	own := false
	switch {
	case s.phase == PhaseExternal && s.external != nil:
		t = engine.Target{Port: s.external.Port, APIKey: s.settings.APIKey}
	case s.proc != nil && s.phase.Served():
		t = engine.Target{Port: s.runCfg.Port, APIKey: s.runCfg.APIKey}
		own = true
	default:
		s.mu.Unlock()
		return
	}
	gen := s.gen
	if restore && own && s.phase == PhaseReleased {
		s.restoreReq = true
		s.setPhaseLocked(PhaseRestoring)
	}
	s.mu.Unlock()
	if restore && own {
		s.notify()
	}
	ctx, cancel := context.WithTimeout(s.ctx, feedTimeout)
	defer cancel()
	_ = op(ctx, t)

	s.mu.Lock()
	if gen == s.gen {
		s.restoreReq = false
	}
	s.mu.Unlock()
	s.probe()
	s.notify()
}

// Install and update.

func (s *Supervisor) runInstall() {
	s.mu.Lock()
	if s.closed || s.switching || s.removal.Running || s.inst.Installed || !s.inst.Ready || s.phase == PhaseInstalling {
		s.mu.Unlock()
		return
	}
	s.updateErr = ""
	s.setPhaseLocked(PhaseInstalling)
	s.mu.Unlock()
	s.notify()

	last, err := s.runInstaller(engine.Installer.Install)
	ctx, cancel := context.WithTimeout(s.ctx, detectTimeout)
	inst := s.engine().Installer().Detect(ctx)
	cancel()

	s.mu.Lock()
	s.inst = inst
	if err != nil || !inst.Installed {
		s.updateErr = installError(last, err)
		s.setPhaseLocked(PhaseInstallFailed)
		s.mu.Unlock()
		s.notify()
		return
	}
	s.setPhaseLocked(PhaseStopped)
	s.mu.Unlock()
	s.notify()
	s.do(s.detect)
}

func (s *Supervisor) runUpdate() {
	s.mu.Lock()
	if s.closed || s.switching || s.removal.Running || !s.inst.Installed || !s.inst.Ready || s.update == UpdateUpdating {
		s.mu.Unlock()
		return
	}
	s.update = UpdateUpdating
	s.updateErr, s.updateReason = "", UpdateReasonNone
	ran := s.proc != nil && !s.stopReq
	cfg := s.runCfg
	ovr := s.override
	oldVersion := s.inst.Version
	s.restartAfter = false
	// The engine's files change under a download: it stops, keeping what
	// it fetched, and runs again once the update is done.
	held := s.holdJobsLocked(s.eng, false)
	s.mu.Unlock()
	s.notify()

	for _, ch := range held {
		s.waitFor(ch, updateStopWait)
	}
	s.stopAndWait(updateStopWait)
	last, err := s.runInstaller(engine.Installer.Upgrade)
	ctx, cancel := context.WithTimeout(s.ctx, detectTimeout)
	inst := s.engine().Installer().Detect(ctx)
	cancel()

	s.mu.Lock()
	if ran {
		s.override = ovr // the stop for the update ended the run; the restart is the same run
	}
	if inst.Installed {
		s.inst = inst
	}
	s.updateReason = UpdateReasonNone
	if err != nil || !inst.Installed || inst.Version == oldVersion {
		s.update = UpdateFailed
		s.updateErr = installError(last, err)
		if err == nil && inst.Installed {
			// The installer ran fine and left the version as it was: it has
			// no newer version yet (the release feed is ahead of it, as a
			// Homebrew formula can be).
			s.updateReason = UpdateReasonNoNewer
		}
	} else {
		s.update = UpdateNone
		s.updateErr = ""
	}
	s.mu.Unlock()
	s.notify()
	s.jobsChanged() // the held download goes on
	if ran {
		s.startServer(&cfg, true)
	}
}

// runInstaller runs op (Install or Upgrade) of the engine's installer,
// appending its output to the log.
func (s *Supervisor) runInstaller(op func(engine.Installer, context.Context, io.Writer) (string, error)) (string, error) {
	logf := s.openLog(false)
	var w io.Writer
	if logf != nil {
		defer logf.Close()
		w = logf
	}
	return op(s.engine().Installer(), s.ctx, w)
}

func installError(last string, err error) string {
	switch {
	case last != "":
		return last
	case err != nil:
		return err.Error()
	}
	return ""
}

// errNoVersion is a feed that answered without a version.
var errNoVersion = errors.New("the release feed gave no version")

// checkUpdate asks the release feed for the newest version and updates
// Latest and Update with it. user is a check the user asked for
// (CheckUpdate): it ends View.Checking and sets View.Check, failed too
// when the engine is not installed. The daily check (tick) is silent: its
// errors show nowhere and it leaves Checking and Check alone, so a result
// the user is reading does not change under them.
func (s *Supervisor) checkUpdate(user bool) {
	s.mu.Lock()
	installed, eng, epoch := s.inst.Installed, s.eng, s.epoch
	s.mu.Unlock()
	if !installed && !user {
		return
	}
	var v string
	err := errNoVersion
	if installed {
		ctx, cancel := context.WithTimeout(s.ctx, feedTimeout)
		v, err = eng.Installer().Latest(ctx)
		cancel()
		if err == nil && v == "" {
			err = errNoVersion
		}
	}
	s.mu.Lock()
	if epoch != s.epoch { // the engine was switched meanwhile: the answer is for the other
		s.mu.Unlock()
		return
	}
	res := CheckFailed
	if err == nil {
		s.latest = v
		if s.update == UpdateNone || s.update == UpdateAvailable {
			s.update = UpdateNone
			if compareVersions(v, s.inst.Version) > 0 {
				s.update = UpdateAvailable
			}
		}
		res = CheckUpToDate
		if compareVersions(v, s.inst.Version) > 0 {
			res = CheckAvailable
		}
	}
	if user {
		s.checking, s.check = false, res
	}
	s.mu.Unlock()
	if err == nil || user {
		s.notify()
	}
}

// compareVersions compares dotted numeric versions ("1.4.10" > "1.4.9");
// missing parts count as 0, a leading "v" is ignored.
func compareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// Agents.

func (s *Supervisor) runAgent(id string) {
	s.mu.Lock()
	needStart := !s.phase.Served() && s.phase != PhaseLoading
	s.mu.Unlock()
	if needStart && !s.doWait(s.startCmd) {
		return
	}

	s.mu.Lock()
	for !s.closed && s.phase == PhaseLoading {
		s.cond.Wait()
	}
	if s.closed {
		s.mu.Unlock()
		return
	}
	if !s.phase.Served() || s.update == UpdateUpdating {
		// Stopping, not answering, failed to start, or updating: say so
		// rather than do nothing.
		s.noticeLocked(NoticeAgentUnavailable, "")
		s.mu.Unlock()
		s.notify()
		return
	}
	t := engine.Target{Port: s.runCfg.Port, APIKey: s.runCfg.APIKey}
	if s.phase == PhaseExternal && s.external != nil {
		t = engine.Target{Port: s.external.Port, APIKey: s.settings.APIKey}
	}
	s.mu.Unlock()

	argv, env := s.engine().AgentCommand(id, t)
	if s.opts.OpenTerminal == nil {
		return
	}
	if err := s.opts.OpenTerminal(argv, env); err != nil {
		kind := NoticeTerminalFailed
		if errors.Is(err, ErrKeyNotStored) {
			kind = NoticeKeyNotStored
		}
		s.mu.Lock()
		s.noticeLocked(kind, err.Error())
		s.mu.Unlock()
		s.notify()
	}
}

// ErrKeyNotStored is what Options.OpenTerminal returns (wrapped) when the
// key the agent needs is not in the Keychain, where the agent's script
// reads it.
var ErrKeyNotStored = errors.New("the API key is not in the Keychain")

// noticeLocked sets a new notice for the panel.
func (s *Supervisor) noticeLocked(kind NoticeKind, text string) {
	s.notice = Notice{Kind: kind, Text: text, Seq: s.notice.Seq + 1}
}

// State helpers; all need s.mu.

func (s *Supervisor) setPhaseLocked(p Phase) {
	s.phase = p
	if p != PhaseStopping {
		s.stoppingLong = false
	}
}

func (s *Supervisor) setStoppedLocked() {
	s.external = nil
	s.snap = engine.Snapshot{}
	s.meter.reset(false)
	s.failure = engine.Failure{}
	s.freePort = 0
	s.setPhaseLocked(PhaseStopped)
}

func (s *Supervisor) setExternalLocked(r engine.Running) {
	s.external = &r
	s.snap = engine.Snapshot{}
	s.meter.reset(false)
	s.failure = engine.Failure{}
	s.freePort = 0
	s.lastScan = s.d.clock.Now()
	s.setPhaseLocked(PhaseExternal)
}

func (s *Supervisor) failLocked(f engine.Failure, freePort int) {
	s.override = portOverride{}
	s.failure = f
	s.freePort = freePort
	s.snap = engine.Snapshot{}
	s.setPhaseLocked(PhaseError)
}

// resolvedLocked is the server config for the current settings.
func (s *Supervisor) resolvedLocked() engine.Config {
	c := s.settings.Config
	c.AllowedHosts = slices.Clone(c.AllowedHosts)
	if c.Host == "" {
		c.Host = bindHost(c.Access)
	}
	if s.dl != "" { // a download run: the new model, online
		c.Model, c.ModelUpdates = s.dl, true
	}
	key := ModelKey{Engine: s.eng.Info().ID, Model: c.Model}
	c.ContextTokens = s.settings.Contexts[key]
	o := s.settings.Options[key]
	c.NoVision, c.Draft = o.NoVision, o.Draft
	return c
}

// portOverride is the port StartOn gave the running server (to) instead of
// the one in the settings (from).
type portOverride struct{ from, to int }

func (s *Supervisor) pendingLocked() bool {
	if s.proc == nil || s.stopReq {
		return false
	}
	want := s.resolvedLocked()
	if o := s.override; o.to != 0 && want.Port == o.from {
		want.Port = o.to // the settings still say what the override replaced
	}
	return !configEqual(s.runCfg, want)
}

func configEqual(a, b engine.Config) bool {
	return a.Model == b.Model && a.Port == b.Port && a.Host == b.Host &&
		a.Access == b.Access && a.APIKey == b.APIKey && a.IdleRelease == b.IdleRelease &&
		a.ModelUpdates == b.ModelUpdates && a.ContextTokens == b.ContextTokens &&
		a.NoVision == b.NoVision && a.Draft == b.Draft &&
		slices.Equal(a.AllowedHosts, b.AllowedHosts)
}

// bindHost is the address the server listens on for access.
func bindHost(a engine.Access) string {
	if a == engine.AccessNetwork {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// shownHost is the host the address chip shows: for the network access
// the computer's LAN address, which other devices dial, when it is known.
func shownHost(a engine.Access, lan string) string {
	if a == engine.AccessNetwork && lan != "" {
		return lan
	}
	return bindHost(a)
}

// View returns the current view.
func (s *Supervisor) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.d.clock.Now()
	v := View{
		Version:        s.inst.Version,
		Latest:         s.latest,
		Phase:          s.phase,
		Update:         s.update,
		UpdateError:    s.updateErr,
		UpdateReason:   s.updateReason,
		Notice:         s.notice,
		Checking:       s.checking,
		Check:          s.check,
		CheckSeq:       s.checkSeq,
		Failure:        s.failure,
		FreePort:       s.freePort,
		Models:         slices.Clone(s.models),
		Speed:          s.meter.view(),
		StoppingLong:   s.stoppingLong,
		LastLine:       s.lastLine,
		LogPath:        s.logPath,
		PendingRestart: s.pendingLocked(),
	}
	Describe(&v, s.eng)
	v.Agents = slices.Clone(s.agents)
	v.Engines = slices.Clone(s.engines)
	v.Downloads = s.downloadsLocked()
	v.Switching = s.switching
	v.Removal = s.removal
	if s.latest != "" {
		v.NotesURL = s.eng.Installer().ReleaseNotesURL(s.latest)
	}
	model := s.settings.Model
	if s.external != nil && s.external.Model != "" {
		model = s.external.Model // the model the foreign server runs
	}
	v.Model = engine.Model{ID: model}
	if s.dl != "" && s.external == nil {
		model = s.dl
		v.Download = s.dl
		v.Downloading = s.downloadingLocked()
		v.DownloadTotal, v.DownloadBytes = s.dlTotal, s.dlDone
		v.Model = engine.Model{ID: model}
		if m, err := s.eng.ParseModel(model); err == nil {
			v.Model = m
		}
	}
	for _, m := range s.models {
		if m.ID == model {
			v.Model = m
		}
	}

	var t engine.Target
	if s.external != nil {
		r := *s.external
		v.External = &r
		v.Port = r.Port
		v.Access = engine.AccessLocal
		v.Address = net.JoinHostPort("127.0.0.1", strconv.Itoa(r.Port))
		t = engine.Target{Port: r.Port, APIKey: s.settings.APIKey}
	} else {
		c := s.resolvedLocked()
		if s.proc != nil {
			c = s.runCfg
		}
		v.Port = c.Port
		v.Access = c.Access
		v.Address = net.JoinHostPort(shownHost(c.Access, s.settings.LANAddress), strconv.Itoa(c.Port))
		t = engine.Target{Port: c.Port, APIKey: c.APIKey}
	}

	if s.phase.Served() {
		v.Snapshot = s.snap
		v.ChatURL = s.eng.ChatURL(t)
		if idle := s.snap.IdleRelease; idle > 0 && !s.snap.Released && !s.snap.Restoring && s.phase != PhaseRestoring && s.snap.Active == 0 {
			v.IdleLeft = max(idle-now.Sub(s.activityAt), 0)
		}
	}
	return v
}

// Ports.

// portBusy reports whether another program holds host:port. err is set
// when the address itself cannot be used (e.g. an invalid host).
func portBusy(host string, port int) (bool, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return true, nil
		}
		return false, err
	}
	ln.Close()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), portDialTimeout)
	if err == nil {
		c.Close()
		return true, nil
	}
	return false, nil
}

// findFreePort is the first free port above port; 0 when none.
func findFreePort(host string, port int) int {
	for p := port + 1; p <= min(port+freePortRange, 65535); p++ {
		if busy, err := portBusy(host, p); err == nil && !busy {
			return p
		}
	}
	return 0
}

// Log.

// openLog opens the server log for appending; truncate empties it first.
// It returns nil when the log cannot be opened.
func (s *Supervisor) openLog(truncate bool) *os.File {
	if s.opts.LogDir == "" {
		return nil
	}
	_ = os.MkdirAll(s.opts.LogDir, 0o755)
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if truncate {
		flags |= os.O_TRUNC
	}
	s.mu.Lock()
	path := s.logPath
	s.mu.Unlock()
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// lineWriter copies one output stream to the log and hands its lines to
// the supervisor.
type lineWriter struct {
	s      *Supervisor
	gen    int
	stderr bool
	log    io.Writer
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	if w.log != nil {
		_, _ = w.log.Write(p)
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		w.s.onLine(w.gen, line, w.stderr)
	}
	if len(w.buf) > maxLine {
		w.s.onLine(w.gen, string(w.buf), w.stderr)
		w.buf = nil
	}
	return len(p), nil
}

// downloadingLocked reports whether a download run fetches files: it is
// loading and the model is not loading yet.
func (s *Supervisor) downloadingLocked() bool {
	return s.dl != "" && s.external == nil && s.phase == PhaseLoading && !s.dlLoaded
}
