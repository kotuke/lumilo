package main

import (
	"errors"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/appupdate"
	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/engines/splash"
	"github.com/kotuke/lumilo/internal/engines/tensorfold"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/internal/hf"
	"github.com/kotuke/lumilo/internal/panel"
	"github.com/kotuke/lumilo/internal/settings"
	"github.com/kotuke/lumilo/internal/terminal"
	"github.com/kotuke/lumilo/internal/trayicon"
)

// secretStore is the Keychain as the app uses it (mygo.SecretsModule).
type secretStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// keyState is what the app knows of the API key in the Keychain.
type keyState int

const (
	keyLoading    keyState = iota // not read yet (startup)
	keyOK                         // read, or there is none: a.key is it
	keyUnreadable                 // the Keychain has an item that could not be read
)

type app struct {
	win   *mygo.Window
	tray  *mygo.Tray
	panel *panel.Panel
	sup   *core.Supervisor
	// upd keeps Lumilo itself up to date (update.go).
	upd *appupdate.Updater
	// release: the bundle is a published release (releaseBuild).
	release bool
	// look runs the lookups of the "Add model…" field off the main thread.
	look *hf.Lookup

	quitting atomic.Bool
	// blurredAt is when the panel last hid because it lost focus. A click on
	// the icon blurs the panel first and only then arrives.
	blurredAt time.Time
	// area is the work area of the display that shows the panel.
	area mygo.Rectangle

	// Main thread only.
	view       core.View
	shown      trayicon.Icon // the icon and tooltip set last
	shownTip   string
	haveShown  bool
	step       int            // breathing frame
	stopBreath chan struct{}  // non-nil while the breathing ticker runs
	tps        trayicon.Speed // the speed the icon shows, eased and held
	stopSpeed  chan struct{}  // non-nil while the speed ticker runs

	path string // settings file

	// keyReady is closed once loadSystem has read the Keychain.
	keyReady chan struct{}

	mu        sync.Mutex  // guards the fields below; used off the main thread too
	secrets   secretStore // the Keychain, under the service of the running bundle; nil until loadSystem
	service   string      // Keychain service, the bundle identifier
	dev       bool        // a `mygo dev` build (identifier ends in ".dev")
	st        settings.Settings
	key       string   // the API key, "" for none
	keyState  keyState // whether key is what the Keychain holds
	retired   []string // keys replaced during this run
	keyStored bool     // key is in the Keychain (or there is none)
	lan       string   // the LAN address, as lanAddress found it last

	saveMu  sync.Mutex     // one settings save at a time, newest state last
	applyMu sync.Mutex     // one Apply to the supervisor at a time, newest last
	keyMu   sync.Mutex     // one Keychain write at a time
	saves   sync.WaitGroup // saves in flight; quitting waits for them
}

// ready runs once the app is up: panel, window, tray, then the supervisor,
// whose first change arrives at once and needs the window. What may block
// on the system (scutil, plutil, the Keychain, the network interfaces)
// runs after, off the main thread: loadSystem.
func (a *app) ready() {
	a.keyReady = make(chan struct{})
	fresh := a.loadSettings()
	mygo.App.SetLocale(a.st.Language)
	if src := mygo.ThemeSource(a.st.Theme); src == mygo.ThemeLight || src == mygo.ThemeDark {
		mygo.Theme.SetSource(src)
	}

	engines := []engine.Engine{splash.New(), llamacpp.New(), tensorfold.New()}
	eng := engines[0]
	for _, e := range engines {
		if e.Info().ID == a.st.Engine {
			eng = e
		}
	}
	// An unknown engine in the file runs the first one: the contexts and
	// options the panel edits must be stored under the engine that runs.
	a.mu.Lock()
	a.st.Engine = eng.Info().ID
	a.mu.Unlock()
	a.panel = panel.New(a)
	a.look = &hf.Lookup{Client: &hf.Client{}, Deliver: func(f func()) { a.win.Update(f) }}
	a.panel.SetMemory(physicalMemory())
	a.panel.SetTerminals(installedTerminals())
	a.panel.SetPrefs(a.prefs())
	a.panel.OnResize = afterFrame(func(fn func()) { a.win.Update(fn) }, a.resize)

	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "Lumilo",
		Width:             panel.Width,
		Height:            400,
		Hidden:            true,
		Frameless:         true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		AlwaysOnTop:       true,
		SkipTaskbar:       true,
		Transparent:       true, // the panel draws its own rounded card
		Content:           ui.View(a.panel.View),
	})
	a.area = mygo.Screen.PrimaryDisplay().WorkArea
	a.win.OnBlur(func() {
		a.blurredAt = time.Now()
		a.hide()
	})
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting.Load() {
			e.PreventDefault()
			a.hide()
		}
	})

	t, err := mygo.NewTray(mygo.TrayOptions{ID: "lumilo", ToolTip: "Lumilo"})
	if err != nil {
		log.Fatal("tray: ", err)
	}
	a.tray = t
	t.OnClick(a.toggle)
	t.OnRightClick(func() {
		a.hide()
		t.SetMenu(a.menu()) // rebuilt each time: the language may have changed
		defer t.SetMenu(nil)
		t.PopUpMenu()
	})
	t.OnAppearanceChange(a.drawTray)
	mygo.App.OnLocaleChanged(func(string) { a.drawTray() })
	mygo.Theme.OnUpdated(func() { a.drawTray() }) // Reduce Motion may have changed

	// Unfinished downloads wait beside the settings, paused, for the next
	// run.
	jobs := ""
	if a.path != "" {
		jobs = filepath.Join(filepath.Dir(a.path), "downloads.json")
	}
	// Without the key yet: commands that start a server wait for it
	// (afterKey), and loadSystem applies it.
	a.sup = core.New(core.Options{
		JobsFile:     jobs,
		Engine:       eng,
		Engines:      engines,
		Settings:     a.coreSettings(eng.Info().ID),
		OnModel:      a.onModel,
		OnRemove:     a.onRemove,
		LogDir:       a.logDir(),
		OnChange:     a.onChange,
		OpenTerminal: a.openTerminal,
	})
	a.applyView(a.sup.View())
	a.startUpdates()

	mygo.App.OnSecondInstance(func([]string, string) { a.win.Update(func() { a.open() }) })
	// Set before the windows close: the panel window cancels its close
	// unless the app is quitting.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting.Store(true) })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		a.quitting.Store(true)
		a.stopBreathing()
		if a.stopSpeed != nil {
			close(a.stopSpeed)
			a.stopSpeed = nil
		}
		a.upd.Close()
		a.sup.Close() // stops our server; a foreign one is never touched
		a.tray.Destroy()
		a.waitSaves(2 * time.Second)
	})
	go a.loadSystem(fresh)
}

// loadSettings reads the settings file. fresh reports that there was none:
// a first run, whose allowed hosts loadSystem fills in.
func (a *app) loadSettings() (fresh bool) {
	if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
		a.path = filepath.Join(dir, "settings.json")
		_, err := os.Stat(a.path)
		fresh = errors.Is(err, os.ErrNotExist)
	}
	st, err := settings.Load(a.path, settings.Defaults(""))
	if err != nil {
		log.Println(err)
	}
	a.mu.Lock()
	a.st = st
	a.mu.Unlock()
	return fresh
}

// loadSystem asks the system what may make the main thread wait: the
// bundle identifier (plutil), the host name (scutil), the API key (the
// Keychain may ask the user) and the LAN address. It runs once, on its own
// goroutine, and hands the results to the supervisor and the panel.
func (a *app) loadSystem(fresh bool) {
	service := secretService()
	secrets := mygo.Secrets.Service(service)
	host := ""
	if fresh {
		host = localHostName()
	}
	got, err := secrets.Get(secretAccount)
	if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
		log.Println("keychain:", err)
	}
	lan := lanAddress()

	a.mu.Lock()
	a.secrets, a.service, a.dev = secrets, service, strings.HasSuffix(service, ".dev")
	a.lan = lan
	if fresh && host != "" && len(a.st.AllowedHosts) == 0 {
		a.st.AllowedHosts = settings.Defaults(host).AllowedHosts
	}
	newKey := a.takeKeyLocked(got, err)
	a.mu.Unlock()

	terminal.Setup(a.terminalConfig())
	close(a.keyReady)
	if newKey {
		a.storeKey()
	}
	a.applyCore()
	a.resumeAfterUpdate()
	a.win.Update(func() {
		a.panel.SetLANAddress(lan)
		a.panel.SetPrefs(a.prefs())
		a.applyLoginFirstRun()
	})
}

// takeKeyLocked takes the Keychain's answer for the API key. An item that
// could not be read is never replaced: the key stays "" and the network
// access refuses to start (engine.FailNoKey) until a later run reads it.
// No item at all in the network access (a file of the three-mode settings,
// say) gets a new key, to store. It reports whether it made one.
func (a *app) takeKeyLocked(got []byte, err error) (newKey bool) {
	switch {
	case err == nil:
		a.key, a.keyState, a.keyStored = string(got), keyOK, true
	case errors.Is(err, mygo.ErrSecretNotFound):
		a.key, a.keyState, a.keyStored = "", keyOK, true
		if a.st.Access == engine.AccessNetwork {
			a.key, a.keyStored = newAPIKey(), false
			return true
		}
	default:
		a.key, a.keyState, a.keyStored = "", keyUnreadable, false
	}
	return false
}

// afterKey runs f once the Keychain was read, so that a server never
// starts before its key is known.
func (a *app) afterKey(f func()) {
	select {
	case <-a.keyReady:
		f()
	default:
		go func() { <-a.keyReady; f() }()
	}
}

// applyLoginFirstRun sets the login item once, on the first run of a
// packaged app. A `mygo dev` build must not keep one: it would register the
// development bundle in the source tree, so it removes any it finds.
func (a *app) applyLoginFirstRun() {
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev {
		go func() {
			if mygo.App.OpenAtLogin() {
				_ = mygo.App.SetOpenAtLogin(false)
			}
		}()
		return
	}
	a.mu.Lock()
	todo := !a.st.LoginApplied
	on := a.st.OpenAtLogin
	a.mu.Unlock()
	if !todo {
		return
	}
	go func() {
		if err := mygo.App.SetOpenAtLogin(on); err != nil {
			log.Println("login item:", err)
			return
		}
		a.update(func(s *settings.Settings) { s.LoginApplied = true })
	}()
}

// update changes the stored settings and saves them in the background.
func (a *app) update(f func(*settings.Settings)) {
	a.mu.Lock()
	f(&a.st)
	a.mu.Unlock()
	a.persistAsync()
}

// persistAsync saves the settings off the calling goroutine.
func (a *app) persistAsync() {
	a.saves.Add(1)
	go func() {
		defer a.saves.Done()
		a.persist()
	}()
}

// persist saves the settings as they are now. Saves run one at a time and
// each takes its copy inside the turn, so the newest state lands last.
func (a *app) persist() {
	if a.path == "" {
		return
	}
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	st := a.st.Clone()
	a.mu.Unlock()
	if err := settings.Save(a.path, st); err != nil {
		log.Println("settings:", err)
	}
}

// waitSaves waits up to d for the saves in flight.
func (a *app) waitSaves(d time.Duration) {
	done := make(chan struct{})
	go func() { a.saves.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// prefs is the settings as the panel edits them: the contexts and options
// of the engine being run.
func (a *app) prefs() panel.Prefs {
	a.mu.Lock()
	defer a.mu.Unlock()
	return panel.Prefs{
		Access:       a.st.Access,
		Port:         a.st.Port,
		APIKey:       a.key,
		AllowedHosts: append([]string(nil), a.st.AllowedHosts...),
		IdleRelease:  a.st.IdleRelease(),
		ModelUpdates: a.st.ModelUpdates,
		Language:     a.st.Language,
		Terminal:     a.st.Terminal,
		OpenAtLogin:  a.st.OpenAtLogin,
		AnimateIcon:  a.st.AnimateIcon,
		AppUpdates:   !a.st.ManualUpdates,
		Contexts:     maps.Clone(a.st.Contexts[a.st.Engine]),
		Options:      maps.Clone(a.st.Options[a.st.Engine]),
	}
}

// coreSettings is the supervisor's configuration, with engineID's model.
func (a *app) coreSettings(engineID string) core.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx, opt := coreChoices(a.st)
	return core.Settings{
		Contexts: ctx,
		Options:  opt,
		Config: engine.Config{
			Model:        a.st.Model(engineID),
			Port:         a.st.Port,
			Access:       a.st.Access,
			APIKey:       serverKey(a.st.Access, a.key),
			AllowedHosts: append([]string(nil), a.st.AllowedHosts...),
			IdleRelease:  a.st.IdleRelease(),
			ModelUpdates: a.st.ModelUpdates,
		},
		LANAddress: a.lan,
	}
}

// coreChoices are the contexts and options of st keyed as the supervisor
// keys them.
func coreChoices(st settings.Settings) (map[core.ModelKey]int, map[core.ModelKey]engine.ModelOptions) {
	ctx := map[core.ModelKey]int{}
	for eng, m := range st.Contexts {
		for id, n := range m {
			ctx[core.ModelKey{Engine: eng, Model: id}] = n
		}
	}
	opt := map[core.ModelKey]engine.ModelOptions{}
	for eng, m := range st.Options {
		for id, o := range m {
			opt[core.ModelKey{Engine: eng, Model: id}] = o
		}
	}
	return ctx, opt
}

// applyCore hands the current settings to the supervisor. Calls run one at
// a time and each reads the settings inside its turn, so the newest
// settings arrive last. The supervisor keeps its own model (Apply).
func (a *app) applyCore() {
	if a.sup == nil {
		return
	}
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	a.mu.Lock()
	eng := a.st.Engine
	a.mu.Unlock()
	a.sup.Apply(a.coreSettings(eng))
}

// serverKey is the key the server demands: none on this computer, where
// the chat page and local apps call it without one; the stored key on the
// network. The key stays in the Keychain either way. On the network
// without a key the supervisor refuses to start (engine.FailNoKey).
func serverKey(access engine.Access, key string) string {
	if access == engine.AccessNetwork {
		return key
	}
	return ""
}

func (a *app) logDir() string {
	dir, _ := mygo.App.Path(mygo.PathLogs)
	return dir
}

func (a *app) terminalConfig() terminal.Config {
	cache, _ := mygo.App.Path(mygo.PathCache)
	return terminal.Config{
		Dir:     filepath.Join(cache, "agents"),
		Service: a.service,
		Account: secretAccount,
	}
}

// onModel remembers a downloaded model as the engine's choice. It runs on
// a supervisor goroutine.
func (a *app) onModel(engineID, modelID string) {
	a.update(func(s *settings.Settings) { s.SetModel(engineID, modelID) })
}

// onRemove forgets what was stored for a model deleted from the disk. It
// runs on a supervisor goroutine.
func (a *app) onRemove(engineID, modelID string) {
	a.update(func(s *settings.Settings) { s.ForgetModel(engineID, modelID) })
	a.win.Update(func() { a.panel.SetPrefs(a.prefs()) })
}

// onChange runs on a supervisor goroutine; the view goes to the main thread.
func (a *app) onChange(v core.View) {
	if a.quitting.Load() {
		return
	}
	a.win.Update(func() { a.applyView(v) })
}

// applyView shows a new view in the panel and the tray. Main thread.
func (a *app) applyView(v core.View) {
	a.view = v
	a.panel.SetView(v)
	a.drawTray()
}

// afterFrame makes the panel's OnResize. The panel calls it while it builds
// a frame, which lays out at the window's old size: a resize then has Core
// Animation scale that frame to the new size, text and all. post runs the
// resize after the frame instead (Window.Update), from a later pass of the
// run loop: the window's new size then draws a frame of its own, in the
// transaction that resizes the window (MyGoSurfaceView's updateLayer), and
// no frame shows scaled.
func afterFrame(post func(func()), resize func(h int)) func(h int) {
	return func(h int) { post(func() { resize(h) }) }
}

// resize fits the window to the panel's content, within the work area.
func (a *app) resize(h int) {
	a.win.SetSize(panel.Width, min(h, a.area.Height-12))
}

func (a *app) hide() {
	a.win.Hide()
	a.panel.Reset()
	a.sup.Watch(false)
}

// menu is the right-click menu of the tray item.
func (a *app) menu() *mygo.Menu {
	item := &mygo.MenuItem{Role: mygo.RoleQuit} // "Quit Lumilo" where mygo has the words
	loc := mygo.App.Locale()
	switch i18n.Base(loc) {
	case "en", "zh":
	default:
		item.Label = format.Messages(loc).Quit()
	}
	return mygo.NewMenu([]*mygo.MenuItem{item})
}

// toggle shows the panel under the icon, or hides it.
func (a *app) toggle() {
	// A second click on the icon: the panel already hid when it lost focus.
	if a.win.IsVisible() || time.Since(a.blurredAt) < 400*time.Millisecond {
		a.hide()
		return
	}
	a.open()
}

// open places the panel under the icon and shows it.
func (a *app) open() {
	anchor := a.tray.Bounds()
	if anchor.Width > 0 {
		a.area = mygo.Screen.DisplayNearestPoint(mygo.Point{X: anchor.X, Y: anchor.Y}).WorkArea
	}
	go a.refreshLAN() // the computer may have joined another network
	a.sup.Watch(true) // models and engines may have changed outside Lumilo
	a.panel.SetView(a.sup.View())
	a.panel.SetTerminals(installedTerminals()) // one may have been installed since
	a.panel.SetPrefs(a.prefs())
	// The panel scrolls its body past the screen's height.
	a.panel.SetMaxHeight(int(a.area.Height) - 12)
	// Size before showing: a hidden window builds no frames.
	a.resize(a.panel.Measure(mygo.Theme.IsDark()))
	a.win.Invalidate()
	if anchor.Width > 0 {
		b := a.win.Bounds()
		x := anchor.X + (anchor.Width-b.Width)/2
		x = max(a.area.X, min(x, a.area.X+max(0, a.area.Width-b.Width)))
		a.win.SetPosition(x, anchor.Y+anchor.Height+6)
	} else {
		a.win.Center()
	}
	// A window hidden for 2 s drops its frame, so the first frame after Show
	// is empty: show it clear and fade in once the frame is drawn.
	a.win.SetOpacity(0)
	a.win.Show()
	a.win.Focus()
	time.AfterFunc(40*time.Millisecond, func() {
		if !a.quitting.Load() {
			a.win.Update(func() { a.win.SetOpacity(1) })
		}
	})
}

// refreshLAN looks up the LAN address again, off the main thread, and
// passes a change on to the panel and the supervisor.
func (a *app) refreshLAN() {
	lan := lanAddress()
	a.mu.Lock()
	changed := lan != a.lan
	a.lan = lan
	a.mu.Unlock()
	if !changed {
		return
	}
	a.win.Update(func() { a.panel.SetLANAddress(lan) })
	a.applyCore()
}

// drawTray sets the icon and tooltip for the current view, language and
// menu-bar theme, and runs the breathing animation when it applies.
func (a *app) drawTray() {
	if a.tray == nil {
		return
	}
	loc := mygo.App.Locale()
	a.tps.Target(a.view, time.Now())
	ic := trayicon.For(a.view, loc, a.tray.Dark(), a.tps.Shown())
	tip := trayicon.ToolTip(a.view, loc)
	if !a.haveShown || ic != a.shown {
		a.setIcon(ic)
	}
	if !a.haveShown || tip != a.shownTip {
		a.tray.SetToolTip(tip)
	}
	a.shown, a.shownTip, a.haveShown = ic, tip, true
	a.syncBreathing()
	a.syncSpeed()
}

// syncSpeed starts or stops the ticker of the speed the icon shows
// (trayicon.Speed): it eases the number toward the live speed, when the
// user wants the icon animated and the system does not ask for less
// motion, and ends the hold after a request; it runs only while a speed
// shows.
func (a *app) syncSpeed() {
	a.mu.Lock()
	animate := a.st.AnimateIcon
	a.mu.Unlock()
	a.tps.Ease = animate && !mygo.Theme.Preferences().ReduceMotion
	want := a.tps.Active()
	switch {
	case want && a.stopSpeed == nil:
		stop := make(chan struct{})
		a.stopSpeed = stop
		go func() {
			tk := time.NewTicker(trayicon.SpeedTick)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case now := <-tk.C:
					if a.quitting.Load() {
						return
					}
					a.win.Update(func() {
						select {
						case <-stop:
							return
						default:
						}
						a.tps.Step(now)
						a.drawTray()
					})
				}
			}
		}()
	case !want && a.stopSpeed != nil:
		close(a.stopSpeed)
		a.stopSpeed = nil
	}
}

func (a *app) setIcon(ic trayicon.Icon) {
	if a.stopBreath != nil {
		ic = trayicon.Frame(ic, a.step)
	}
	if err := a.tray.SetIcon(trayicon.PNG(ic), false); err != nil {
		log.Println("tray icon:", err)
	}
}

// syncBreathing starts or stops the ticker that fades the bar while the
// engine generates. It runs only when the user turned it on and the system
// does not ask for less motion.
func (a *app) syncBreathing() {
	a.mu.Lock()
	on := a.st.AnimateIcon
	a.mu.Unlock()
	want := on && trayicon.Breathing(a.view) && !mygo.Theme.Preferences().ReduceMotion
	switch {
	case want && a.stopBreath == nil:
		stop := make(chan struct{})
		a.stopBreath, a.step = stop, 0
		go func() {
			tk := time.NewTicker(trayicon.BreathInterval)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					if a.quitting.Load() {
						return
					}
					a.win.Update(func() {
						select {
						case <-stop:
							return
						default:
						}
						a.step = (a.step + 1) % trayicon.BreathSteps
						a.setIcon(a.shown)
					})
				}
			}
		}()
	case !want && a.stopBreath != nil:
		a.stopBreathing()
		a.setIcon(a.shown) // back to the plain bar
	}
}

func (a *app) stopBreathing() {
	if a.stopBreath != nil {
		close(a.stopBreath)
		a.stopBreath = nil
	}
}
