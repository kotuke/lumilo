// Package panel draws Lumilo's menu-bar panel: the main screen with the
// server, memory, speed and agents cards, and the settings screen. It only
// draws and reports what the user asked for; the app wires the actions to
// the supervisor and the system.
package panel

import (
	"log"
	"maps"
	"slices"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/locales"
)

// Width is the panel width in points.
const Width = 372

// Prefs are the user's settings as the settings screen edits them.
type Prefs struct {
	Access       engine.Access
	Port         int
	APIKey       string // required for AccessNetwork
	AllowedHosts []string
	IdleRelease  time.Duration // 0 = never
	ModelUpdates bool
	Language     string // "" = as in the system, else a catalog tag
	Terminal     string // bundle id of the terminal that runs agents
	OpenAtLogin  bool
	AnimateIcon  bool
	// Contexts is the context window chosen for each model, in tokens, by
	// Model.ID; a missing one is the engine's choice.
	Contexts map[string]int
	// Options are the vision and draft choices of each model, by Model.ID;
	// a missing one is the engine's defaults.
	Options map[string]engine.ModelOptions
}

// Actions are what the panel asks the app to do. They are called on the
// UI thread and must return at once.
type Actions interface {
	Start()
	StartOn(port int)
	Stop()
	ForceStop()
	Restart()
	KillExternal()
	Release()
	Restore()
	SelectModel(id string)
	// Download downloads a model, by the name ParseModel accepted, with
	// the options o (NoVision: for text only, which the app also stores
	// for the model). An engine that downloads apart from the server
	// (View.DownloadApart) shows it in View.Downloads; another one starts
	// the server with the model, which it downloads first.
	Download(id string, o engine.ModelOptions)
	// CancelDownload stops the download of id; DismissDownload removes a
	// failed or finished one from the list.
	CancelDownload(id string)
	DismissDownload(id string)
	// PlanRemove counts what deleting the installed model id frees; the
	// count comes in View.Removal. CancelRemove forgets it when the user
	// does not delete; RemoveModel deletes the model from the disk.
	PlanRemove(id string)
	CancelRemove()
	RemoveModel(id string)
	// ServeTextOnly turns image input off for the model id, stores that,
	// and starts the server.
	ServeTextOnly(id string)
	// ParseModel checks a model name typed to download, in the engine's
	// syntax.
	ParseModel(spec string) (engine.Model, error)
	// SearchModels looks up Hugging Face repositories for the text typed in
	// "Add model…", off the UI thread. The answer comes through
	// Panel.SetModelHits; a failed search answers with no repositories.
	// A new search drops the one still running.
	SearchModels(text string)
	// ModelVariants looks at a repository ("owner/repo"): its variants the
	// engine runs, or its MLX quantization, and what they need; the
	// answer comes through Panel.SetModelInfo.
	ModelVariants(repo string)
	// CancelModelSearch drops the lookups still running.
	CancelModelSearch()
	// SwitchEngine runs another engine; the panel confirms first when our
	// server runs.
	SwitchEngine(id string)
	Install()
	Update()
	CheckUpdate()
	RunAgent(id string)
	Recheck() // detect the engine and Homebrew again

	OpenChat()
	OpenLog()
	OpenURL(url string)
	Copy(text string)
	// ToggleTheme switches the panel between the light and the dark theme.
	ToggleTheme()
	Quit() // quit Lumilo (the panel confirms first when needed)

	// SetPrefs stores changed settings. Settings that the server reads at
	// start take effect on Restart; the panel shows the restart banner.
	SetPrefs(p Prefs)
	// NewAPIKey returns a fresh random key.
	NewAPIKey() string
	// PortFree reports whether the port can be used by the server.
	PortFree(port int) bool
}

type screen int

const (
	screenMain screen = iota
	screenSettings
)

type dialog int

const (
	dlgNone dialog = iota
	dlgQuitBusy
	dlgQuitUpdating
	dlgUpdate
	dlgKill
	dlgSwitch // switching the engine stops our server
	dlgRemove // deleting an installed model from the disk: Panel.removeID
)

// Panel holds the panel's own state between frames. It is used only on
// the UI thread.
type Panel struct {
	act  Actions
	view core.View
	pref Prefs
	// OnResize is called from View when the content height changes. It is
	// not called for Measure, which returns the height instead.
	OnResize func(height int)

	lanHost   string           // SetLANAddress
	memory    int64            // SetMemory: the computer's memory, bytes; 0 = unknown
	terminals []TerminalChoice // SetTerminals

	// now is the clock of every duration the panel shows.
	now func() time.Time

	// When things happened, as SetView saw them: View carries no times.
	seen     bool
	viewAt   time.Time // the current View arrived
	phaseAt  time.Time // its phase began
	servedAt time.Time // the server began to answer
	loadAt   time.Time // the last model load began

	screen     screen
	modelsOpen bool
	// enginesOpen is the engine list under the header.
	enginesOpen bool
	dlg         dialog
	// updToggled: the model-updates chip changed the setting, so the card
	// says it applies on restart while the server runs with the old one.
	updToggled bool

	// The model name being typed to download ("Add model…"): adding is set
	// while the field shows; addBad when the engine refused the name.
	addingModel bool
	addText     string
	addBad      bool
	// add is the completion of the name being typed (addmodel.go).
	add addState
	// switchTo is the engine whose switch waits for the user to confirm
	// that the server stops; "" = none.
	switchTo string
	// removeID is the model whose deletion waits for the user to confirm
	// (dlgRemove); "" = none.
	removeID string

	// Speed chart: the bar under the pointer or the focus, the bar that
	// holds the chart's tab stop, and when the live bar started growing:
	// the request in progress began, as SetView saw it.
	hoverBar int
	barSel   int // -1 = the last bar
	barFocus bool
	bars     [20]ui.Handle
	liveAt   time.Time

	// Agent chips: their names as the last frame laid them out, to give
	// each chip its content width as the basis it grows from.
	agentTxt [maxAgents]ui.Handle
	agentW   [maxAgents]float32
	// The access modes' names, for the same.
	segTxt [2]ui.Handle
	segW   [2]float32

	// Choices that wait for the server: "When idle" and "Quit after update".
	updateWhenIdle  bool
	quitAfterUpdate bool

	set settingsState

	gen     int      // bumped by SetView and SetPrefs
	shape   shapeKey // what the last frame's layout was built from
	content ui.Handle
	// Each screen's own height as its last frame laid it out (0 until
	// then), and the last height seen above 0: the window slides between
	// the two heights with the screens.
	pageH    [2]ui.Handle
	pageLast [2]float32
	cardH    ui.Handle
	headH    ui.Handle
	titleH   ui.Handle // the header's room for the title and the update chip
	chipText bool      // the last frame's update chip showed its version
	setHeadH ui.Handle // the header of Settings
	// maxH is the most the window may take, the visible screen's height
	// (SetMaxHeight); 0 = no limit. A taller screen scrolls under its
	// header.
	maxH     int
	popup    ui.Handle
	dialog   ui.Handle
	height   int
	sizer    *ui.Tester
	measured bool // the frame being built is Measure's
	// jump shows the screen at once in the next frame of the window, as
	// after Reset: the panel hid, so nothing should slide when it shows.
	jump bool
	// The download: the sizes of the models "Add model…" started, by ID;
	// its speed in bytes a second (0 = unknown), averaged over the samples
	// of DownloadBytes of the last dlWindow.
	dlTotal   map[string]int64
	dlRate    float64
	dlSamples []dlSample
	// dlKey is the model whose bytes dlSamples hold, dlLast its last count.
	dlKey  string
	dlLast int64
	// notice is the last notice of the View (core.Notice), shown until
	// noticeUntil.
	notice      core.Notice
	noticeUntil time.Time
	// brands are the logos of the View, decoded once (logos.go).
	brands brands
}

// New makes a panel that reports to act.
func New(act Actions) *Panel {
	return &Panel{act: act, now: time.Now, hoverBar: -1, barSel: -1, add: addState{sel: -1},
		terminals: []TerminalChoice{{ID: "com.apple.Terminal", Name: "Terminal"}}}
}

// SetMemory gives the computer's memory in bytes, against which the
// suggested models are measured.
func (p *Panel) SetMemory(bytes int64) { p.memory = bytes }

// SetMaxHeight gives the most the window may be tall, in points: the
// height of the screen's work area that shows the panel, less its
// margin. A screen taller than that scrolls under its header, which
// stays; 0 = no limit.
func (p *Panel) SetMaxHeight(h int) {
	if h != p.maxH {
		p.maxH = h
		p.gen++
	}
}

// SetLANAddress gives the computer's address on the local network, which
// the settings hint shows for the "Network" access.
func (p *Panel) SetLANAddress(host string) { p.lanHost = host }

// TerminalChoice is a terminal the settings offer: its bundle id, which the
// settings store, and its name.
type TerminalChoice struct{ ID, Name string }

// SetTerminals gives the terminals installed on this computer, for the
// terminal select of the settings.
func (p *Panel) SetTerminals(list []TerminalChoice) { p.terminals = slices.Clone(list) }

// SetView shows a new supervisor view. Call it on the UI thread
// (inside Window.Update).
func (p *Panel) SetView(v core.View) {
	now := p.now()
	old := p.view
	first := !p.seen
	p.seen = true
	if first || v.Phase != old.Phase {
		p.phaseAt = now
	}
	if v.Phase.Served() && (first || !old.Phase.Served()) {
		p.servedAt = now
	}
	if v.Phase == core.PhaseLoading && (first || old.Phase != core.PhaseLoading) {
		p.loadAt = now
	}
	// A request began: generating began, or a request ended and the next
	// one runs.
	if v.Phase == core.PhaseGenerating && (first || old.Phase != core.PhaseGenerating ||
		len(v.Speed.History) != len(old.Speed.History) || !v.Speed.LastRequest.Equal(old.Speed.LastRequest)) {
		p.liveAt = now
	}
	// The download's speed: how fast DownloadBytes grew over the last
	// dlWindow. The engine writes in bursts; the average keeps the speed
	// and the time left from jumping with them.
	// It is the server's download run, or the running download job: one
	// runs at a time.
	key, done := "", int64(0)
	switch {
	case v.Download != "" && v.Downloading:
		key, done = v.Download, v.DownloadBytes
	default:
		if j, ok := runningJob(v); ok {
			key, done = j.Model.ID, j.Done
		}
	}
	switch {
	case key == "" || key != p.dlKey:
		p.dlRate, p.dlSamples = 0, nil
		if key != "" {
			p.dlRate = p.addSample(now, done)
		}
	case done != p.dlLast || len(p.dlSamples) == 0:
		p.dlRate = p.addSample(now, done)
	}
	p.dlKey, p.dlLast = key, done
	// A new notice shows for a moment; one the panel saw before it opened
	// is old news.
	if v.Notice.Seq != old.Notice.Seq && !first && v.Notice.Kind != core.NoticeNone {
		p.notice, p.noticeUntil = v.Notice, now.Add(noticeFor)
		if v.Notice.Kind == core.NoticeRemoveFailed {
			p.modelsOpen = false // the list would cover it
		}
	}
	if old.PendingRestart && !v.PendingRestart {
		p.updToggled = false // restarted, from the card or Settings
	}
	p.viewAt = now
	p.view = v
	p.gen++

	// A dialog for a state that is gone closes.
	switch p.dlg {
	case dlgQuitBusy, dlgUpdate:
		if v.Phase != core.PhaseGenerating || v.Update == core.UpdateUpdating {
			p.dlg = dlgNone
		}
	case dlgQuitUpdating:
		if v.Update != core.UpdateUpdating {
			p.dlg = dlgNone
		}
	case dlgKill:
		if v.Phase != core.PhaseExternal {
			p.dlg = dlgNone
		}
	case dlgRemove:
		// The model went, or it cannot be deleted now.
		if _, ok := p.removable(p.removeID); !ok || p.removeBlock(nil, p.removeID) != "" {
			p.closeRemove()
		}
	}
	if v.EngineID != old.EngineID {
		p.add.reset(p.act) // its repositories were found for the other engine
	}
	if !modelCanOpen(v) {
		p.modelsOpen = false
		if !p.suggesting() {
			p.addingModel = false
		}
	}
	if p.switchTo != "" && (v.EngineID == p.switchTo || !p.running() && p.engineInstalled(p.switchTo)) {
		p.switchTo = "" // done, or nothing left to stop: no question
	}
	if p.switchTo == "" && p.dlg == dlgSwitch {
		p.dlg = dlgNone
	}
	if !p.switcherOn() {
		p.enginesOpen = false
	}
	// A check the user started in Settings before it was opened this
	// time: its result shows too.
	if v.Checking && p.screen == screenSettings && !p.set.checkAsked {
		p.set.checkAsked, p.set.checkSeq = true, -1
	}

	// Choices made in a dialog that wait for the server.
	if p.updateWhenIdle && v.Phase != core.PhaseGenerating {
		p.updateWhenIdle = false
		if v.Update == core.UpdateAvailable || v.Update == core.UpdateFailed {
			p.act.Update()
		}
	}
	if p.quitAfterUpdate && v.Update != core.UpdateUpdating {
		p.quitAfterUpdate = false
		p.act.Quit()
	}
}

// SetPrefs shows the current settings.
func (p *Panel) SetPrefs(pr Prefs) {
	pr.AllowedHosts = slices.Clone(pr.AllowedHosts)
	pr.Contexts = maps.Clone(pr.Contexts)
	pr.Options = maps.Clone(pr.Options)
	p.pref = pr
	p.set.sync(pr)
	p.gen++
}

// Reset returns to the main screen and closes popups and dialogs; the
// app calls it when the panel hides.
func (p *Panel) Reset() {
	p.screen = screenMain
	p.jump = true
	p.modelsOpen, p.enginesOpen = false, false
	p.dlg = dlgNone
	p.addingModel, p.addText, p.addBad, p.switchTo = false, "", false, ""
	p.add.reset(p.act)
	p.hoverBar, p.barSel, p.barFocus = -1, -1, false
	p.set.reset(p.pref)
}

// Measure returns the content height for the given theme without a
// window, so the app can size the window before showing it.
func (p *Panel) Measure(dark bool) (h int) {
	defer func() {
		p.measured = false
		if r := recover(); r != nil {
			log.Println("panel: measure:", r)
			p.sizer = nil
			h = p.height
		}
	}()
	p.measured = true // NewTester builds a frame at once
	if p.sizer == nil {
		p.sizer = ui.NewTester(p.View, Width, 2000)
	}
	loc := i18n.Locale()
	p.sizer.SetDark(dark)
	p.sizer.SetLocale(loc)
	if format.RTL(loc) {
		p.sizer.SetDirection(ui.RightToLeft)
	} else {
		p.sizer.SetDirection(ui.LeftToRight)
	}
	p.sizer.SetPreferences(ui.Preferences{ReduceMotion: true})
	p.measured = true
	// The height is known once a frame has been laid out.
	p.sizer.Frame()
	p.sizer.Frame()
	return p.height
}

// frame is what one build of the view needs to know.
type frame struct {
	pal   palette
	m     locales.Messages
	loc   string
	rtl   bool
	still bool // no motion: Reduce Motion, or measuring
	now   time.Time
	// brands are the panel's decoded logos.
	brands *brands
}

// View builds the panel for one frame.
func (p *Panel) View(c *ui.Context) {
	loc := c.Locale()
	if loc == "" {
		loc = "en"
	}
	f := &frame{
		pal:    paletteFor(c.Theme().Dark, p.screen == screenSettings),
		m:      format.Messages(loc),
		loc:    loc,
		rtl:    c.Direction() == ui.RightToLeft,
		still:  c.Preferences().ReduceMotion || p.measured,
		now:    p.now(),
		brands: &p.brands,
	}
	t := *c.Theme()
	t.Focus = f.pal.focus
	c.SetTheme(&t)

	root := ui.Column(c).Fill().Background(f.pal.panel).Border(1, f.pal.border).Radius(14).
		TextColor(f.pal.text).FontFeatures("tnum")

	// Settings slides in from the end of the line as the main screen goes
	// out at its start, and back, as a navigation push and pop. at goes
	// from 0, the main screen, to 1, Settings. Reduce Motion and Measure
	// (whose Tester asks for less motion) switch at once, as does the
	// first frame after Reset.
	if p.jump && !p.measured {
		c.DisableAnimations()
		p.jump = false
	}
	target := float32(0)
	if p.screen == screenSettings {
		target = 1
	}
	at := root.AnimateTo("screen", target, slideAnimation)
	sliding := !root.Settled("screen")
	if sliding {
		// The screen going away must not draw past the rounded panel.
		root.Clip()
	}
	root.Children(func() {
		// While they slide, both screens lie side by side in this row, which
		// is as tall as the taller one. The window is not: it goes from one
		// screen's height to the other's as the screens slide (below). The
		// row keeps its size inside the shorter window; the panel clips it.
		ui.Row(c).Bind(&p.content).AlignItems(ui.Start).Shrink(0).Children(func() {
			dx := -at * pageWidth
			if f.rtl {
				dx = -dx // the row starts at the right, Settings lies left of it
			}
			if sliding || p.screen == screenMain {
				p.page(c, f, screenMain, sliding, dx)
			}
			if sliding || p.screen == screenSettings {
				p.page(c, f, screenSettings, sliding, dx)
			}
		})
	})
	if p.screen == screenMain {
		p.dialogs(c, f)
	}

	// The height below is read from the last frame's layout. When what the
	// layout is built from changed, look again once this frame is laid out.
	if k := p.shapeOf(c, f, sliding); k != p.shape {
		p.shape = k
		c.AnimationFrame()
	}
	for i := range p.pageH {
		if b := p.pageH[i].Bounds(c).H; b > 0 {
			p.pageLast[i] = b
		}
	}
	// The window is as tall as what it shows, "Add model…" and its answers
	// included: the app resizes it after the frame that needs it
	// (OnResize), so no frame shows stretched.
	in := heightPlan{
		row: p.content.Bounds(c).H, screen: p.screen, sliding: sliding, at: at,
		last: p.pageLast, max: p.maxH,
	}
	in.page[p.screen] = p.pageH[p.screen].Bounds(c).H
	// Overlays must fit in the window too. The model list belongs 51 DIPs
	// below the server card's top; where the window is too short, the list
	// moves, so its own place says nothing: count from the card.
	if p.screen == screenMain && p.modelsOpen {
		if pop, crd := p.popup.Bounds(c), p.cardH.Bounds(c); pop.H > 0 {
			in.overlay = crd.Y + 51 + pop.H
		}
	}
	if p.screen == screenMain && p.enginesOpen {
		if pop, hd := p.popup.Bounds(c), p.headH.Bounds(c); pop.H > 0 {
			in.overlay = hd.Y + hd.H + engineListGap + pop.H
		}
	}
	if p.screen == screenMain && p.dlg != dlgNone {
		if d := p.dialog.Bounds(c); d.H > 0 {
			in.overlay = max(in.overlay, 45+d.H, d.Y+d.H)
		}
	}
	h := planHeight(in)
	if h > 2 && h != p.height {
		p.height = h
		c.AnimationFrame()
		if p.OnResize != nil && !p.measured {
			p.OnResize(h)
		}
	}
}

// heightPlan is what the window's height is worked out from: the layout of
// the last frame, and what the panel shows now.
type heightPlan struct {
	row     float32    // the row of the screens, as tall as the taller one
	page    [2]float32 // the screen shown, as laid out (0 = not yet)
	last    [2]float32 // the last height above 0 of each screen
	screen  screen
	sliding bool
	at      float32 // the slide's progress, from the main screen (0) to Settings (1)
	overlay float32 // the bottom of the popup or dialog shown, from the panel's top; 0 = none
	max     int     // the most the window may take (the visible screen); 0 = no limit
}

// planHeight is the window's height for in, its border included.
func planHeight(in heightPlan) int {
	const border = 2 // the root's
	h := int(in.row+0.5) + border
	if cur := in.page[in.screen]; !in.sliding && cur > 0 {
		// Once the slide ended the row may still hold both screens: the
		// height is that of the screen shown.
		h = int(cur+0.5) + border
	}
	if in.sliding {
		// The window follows the slide: from the height of the screen going
		// out to that of the screen coming in, with the slide's own
		// progress. Until both heights are known the row's height, the
		// taller one, stands in.
		if from, to := in.last[screenMain], in.last[screenSettings]; from > 0 && to > 0 {
			h = int(from+(to-from)*in.at+0.5) + border
		} else {
			h = max(h, int(in.last[screenMain]+0.5)+border)
		}
	}
	if in.overlay > 0 {
		h = max(h, int(in.overlay+0.5)+overlayMargin)
	}
	if in.max > 0 {
		h = min(h, in.max)
	}
	return h
}

// overlayMargin is the room under a popup or dialog to the window's edge.
const overlayMargin = 10

// pageWidth is a screen's width: the panel's inside its border.
const pageWidth = Width - 2

// slideAnimation is how the screens slide.
var slideAnimation = ui.Animation{Duration: 280 * time.Millisecond, Ease: ui.EaseOut}

// page builds screen s in its own palette, moved by dx while the screens
// slide.
func (p *Panel) page(c *ui.Context, f *frame, s screen, sliding bool, dx float32) {
	pf := *f
	pf.pal = paletteFor(c.Theme().Dark, s == screenSettings)
	key := "main"
	if s == screenSettings {
		key = "settings"
	}
	col := ui.Column(c).Key(key).Bind(&p.pageH[s]).Width(pageWidth).Shrink(0).Padding(pagePad).Gap(pageGap)
	if sliding {
		col.Translate(dx, 0)
	}
	col.Children(func() {
		if s == screenSettings {
			p.settings(c, &pf)
		} else {
			p.main(c, &pf)
		}
	})
}

// A screen's padding, and the gap between its blocks.
const pagePad, pageGap = 10, 8

// body builds the part of screen s under its header. When the window may
// be shorter than the screen (SetMaxHeight), it scrolls there, and the
// header stays.
func (p *Panel) body(c *ui.Context, s screen, fn func()) {
	if p.maxH <= 0 {
		fn()
		return
	}
	head := p.headH
	if s == screenSettings {
		head = p.setHeadH
	}
	hh := head.Bounds(c).H
	if hh <= 0 {
		hh = 32 // until the header is laid out: about its height
	}
	// The panel's border, the screen's padding, the header and the gap
	// under it.
	room := float32(p.maxH) - 2 - 2*pagePad - hh - pageGap
	ui.Scroll(c).Key("body").MaxHeight(max(room, 120)).Gap(pageGap).Shrink(0).Children(fn)
}

// shapeKey is what the panel's layout depends on besides the clock.
type shapeKey struct {
	gen                 int
	screen              screen
	sliding             bool
	models, engines     bool
	overlay             float32
	dlg                 dialog
	adding, bad         bool
	addModel, addBad    bool
	addRows             int
	addLoad             bool
	addSum, addHint     bool
	switchTo            string
	ctxOther, ctxBad    bool
	busy                int
	notice              bool
	loc                 string
	rtl, dark, measured bool
}

func (p *Panel) shapeOf(c *ui.Context, f *frame, sliding bool) shapeKey {
	return shapeKey{gen: p.gen, screen: p.screen, sliding: sliding, models: p.modelsOpen, engines: p.enginesOpen, dlg: p.dlg,
		overlay: p.popup.Bounds(c).H + p.dialog.Bounds(c).H,
		adding:  p.set.adding, bad: p.set.portBad || p.set.hostBad, busy: p.set.busy, notice: p.noticeUntil.After(f.now),
		addModel: p.addingModel, addBad: p.addBad, addRows: p.add.rows, addLoad: p.add.loading, addSum: p.add.summary, addHint: p.add.hint, switchTo: p.switchTo, ctxOther: p.set.ctxOther, ctxBad: p.set.ctxBad,
		loc: f.loc, rtl: f.rtl, dark: c.Theme().Dark, measured: p.measured}
}

// dlWindow is how long the download's speed is averaged over.
const dlWindow = 10 * time.Second

// dlSample is the bytes a download had fetched at a time.
type dlSample struct {
	at    time.Time
	bytes int64
}

// addSample notes the bytes fetched at now and returns the speed over the
// last dlWindow in bytes a second; 0 = not known yet. One sample from before
// the window stays as the start of the span, so a stall does not lose the
// speed at once.
func (p *Panel) addSample(now time.Time, bytes int64) float64 {
	if n := len(p.dlSamples); n > 0 && bytes < p.dlSamples[n-1].bytes {
		p.dlSamples = nil // the count started over
	}
	p.dlSamples = append(p.dlSamples, dlSample{now, bytes})
	for len(p.dlSamples) > 2 && now.Sub(p.dlSamples[1].at) > dlWindow {
		p.dlSamples = p.dlSamples[1:]
	}
	first, last := p.dlSamples[0], p.dlSamples[len(p.dlSamples)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 || first.bytes <= 0 || last.bytes <= first.bytes {
		return 0
	}
	return float64(last.bytes-first.bytes) / dt
}
