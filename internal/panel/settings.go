package panel

import (
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
)

// settingsState is what the settings screen keeps between frames: the
// values its controls are bound to, the port being typed, the host being
// added.
type settingsState struct {
	access     int
	idle       time.Duration
	lang       string
	term       string // bundle id
	updates    bool
	login      bool
	blink      bool
	portText   string
	portBad    bool // the text is not a port number
	busy, free int  // the port is in use; free is one that is not (0 = none found)
	focusPort  bool
	// revealCtx, set by the context chip, brings the context row into
	// view and makes it blink (ctxBlink) from flashCtx, the frame that
	// first showed it, on the UI's clock, which its colours follow.
	revealCtx bool
	flashCtx  time.Time
	portFocus bool // the port field had the focus in the last frame
	epoch     int  // bumped by reset: popups of the selects start closed
	adding    bool
	hostText  string
	hostBad   bool
	opened    Prefs // the settings when the screen opened
	// The update check asked for on this screen: its result shows under
	// the version until the screen closes. Until a View with another
	// CheckSeq than checkSeq comes, the check counts as running.
	checkAsked bool
	checkSeq   int
	// The context select of the selected model: ctx is its value in tokens
	// (0 = auto, ctxOtherOpt = "Other…"); ctxOther shows the field for
	// another size, typed in K into ctxText; ctxBad when it is no size.
	ctx      int
	ctxModel string // the model ctx belongs to
	ctxSeen  int    // its stored size as the last frame saw it
	ctxOther bool
	ctxText  string
	ctxBad   bool
}

// ctxOtherOpt is the "Other…" option of the context select.
const ctxOtherOpt = -1

// Context sizes the select offers, in tokens (K = 1024), as the engines
// take them; only those up to the model's native window show.
var ctxOptions = []int{32 << 10, 64 << 10, 128 << 10, 256 << 10}

// ctxName is a context size as the select and the notes write it: "32K".
func ctxName(loc string, tokens int) string {
	if tokens%1024 == 0 {
		return format.Number(loc, float64(tokens/1024), 0) + "K"
	}
	return format.Number(loc, float64(tokens)/1024, 1) + "K"
}

// sync binds the controls to pr. The port text follows only a port that
// changed, so a half-typed number survives an unrelated update.
func (s *settingsState) sync(pr Prefs) {
	s.access = int(pr.Access)
	s.idle = pr.IdleRelease
	s.lang = pr.Language
	s.term = pr.Terminal
	s.updates, s.login, s.blink = pr.ModelUpdates, pr.OpenAtLogin, pr.AnimateIcon
	if n, err := strconv.Atoi(strings.TrimSpace(s.portText)); err != nil || n != pr.Port {
		if !s.portBad {
			s.portText = strconv.Itoa(pr.Port)
		}
	}
}

func (s *settingsState) reset(pr Prefs) {
	s.portBad, s.busy, s.free = false, 0, 0
	s.portText = strconv.Itoa(pr.Port)
	s.adding, s.hostText, s.hostBad, s.focusPort, s.portFocus = false, "", false, false, false
	s.flashCtx, s.revealCtx = time.Time{}, false
	s.checkAsked, s.checkSeq = false, 0
	s.ctxOther, s.ctxText, s.ctxBad, s.ctxModel = false, "", false, ""
	s.epoch++
	s.sync(pr)
}

// Options of the idle select, in the mockup's order.
var idleOptions = []time.Duration{5 * time.Minute, 10 * time.Minute, 30 * time.Minute, 0}

// Languages of the language select, in the mockup's order: "" follows the
// system.
var languages = []string{"", "en", "de", "es", "fr", "ru", "ar", "zh-Hans", "ja"}

// languageName is the language's name in itself, as the mockup writes it:
// 中文 for Simplified Chinese.
func languageName(tag string) string {
	if tag == "zh-Hans" {
		tag = "zh"
	}
	return format.LanguageName(tag)
}

func (p *Panel) openSettings(focusPort bool) {
	p.screen = screenSettings
	p.modelsOpen, p.enginesOpen, p.dlg, p.switchTo = false, false, dlgNone, ""
	p.set.opened = p.pref
	p.set.opened.AllowedHosts = slices.Clone(p.pref.AllowedHosts)
	p.set.opened.Contexts = maps.Clone(p.pref.Contexts)
	p.set.opened.Options = maps.Clone(p.pref.Options)
	p.set.reset(p.pref)
	p.set.focusPort = focusPort
	p.checkPort(p.pref.Port)
}

// ours reports whether our own server holds the port: it is not busy then.
func (p *Panel) ours(port int) bool {
	return p.running() && p.view.Port == port
}

// running reports whether our own server process runs (a foreign one does
// not count: the settings do not apply to it).
func (p *Panel) running() bool {
	switch p.view.Phase {
	case core.PhaseLoading, core.PhaseReady, core.PhaseGenerating, core.PhaseReleased, core.PhaseRestoring,
		core.PhaseUnresponsive, core.PhaseStopping:
		return true
	}
	return false
}

// checkPort asks whether port can be used and finds a free one if not.
func (p *Panel) checkPort(port int) {
	s := &p.set
	s.busy, s.free = 0, 0
	v := p.view
	switch {
	case p.ours(port):
		return
	case v.Phase == core.PhaseError && failKind(v.Failure.Kind) == errPort && v.Port == port:
		s.busy, s.free = port, v.FreePort
	case !p.act.PortFree(port):
		s.busy = port
	default:
		return
	}
	if s.free == 0 {
		for q := port + 1; q <= min(65535, port+50); q++ {
			if p.ours(q) || p.act.PortFree(q) {
				s.free = q
				break
			}
		}
	}
}

// commit changes the settings and tells the app. An open server needs a
// key, so one is made when the access opens.
func (p *Panel) commit(change func(pr *Prefs)) {
	pr := p.pref
	pr.AllowedHosts = slices.Clone(pr.AllowedHosts)
	pr.Contexts = maps.Clone(pr.Contexts)
	pr.Options = maps.Clone(pr.Options)
	change(&pr)
	if pr.Access != engine.AccessLocal && pr.APIKey == "" {
		pr.APIKey = p.act.NewAPIKey()
	}
	p.pref = pr
	p.gen++
	p.act.SetPrefs(pr)
}

// serverDiff reports whether two settings start the server with model
// differently.
func serverDiff(a, b Prefs, model string) bool {
	return a.Access != b.Access || a.Port != b.Port || a.APIKey != b.APIKey ||
		a.IdleRelease != b.IdleRelease || a.ModelUpdates != b.ModelUpdates ||
		!slices.Equal(a.AllowedHosts, b.AllowedHosts) || a.Contexts[model] != b.Contexts[model] ||
		a.Options[model] != b.Options[model]
}

// settings builds the settings screen.
func (p *Panel) settings(c *ui.Context, f *frame) {
	m := f.m
	p.applyChoices()

	ui.Row(c).Gap(6).Padding(2, 2, 0, 2).Bind(&p.setHeadH).Children(func() {
		arrow := "arrow-left"
		if f.rtl {
			arrow = "arrow-right"
		}
		iconBtn(c, f, m.Back(), arrow, false, func() { p.screen = screenMain })
		txt(c, m.SettingsTitle(), 14).FontWeight(600)
	})
	p.body(c, screenSettings, func() { p.settingsBody(c, f) })
}

// settingsBody is the settings under their header: the cards.
func (p *Panel) settingsBody(c *ui.Context, f *frame) {
	t, m := f.pal, f.m
	s := &p.set
	p.restartBanner(c, f)
	p.engineCard(c, f)

	// Access.
	open := p.pref.Access != engine.AccessLocal
	card(c, f, 10).Children(func() {
		txt(c, m.Access(), 12.5).FontWeight(600)
		seg := ui.SegmentedBase(c.Key("access"), &s.access, 2)
		seg.Track.Gap(2).Background(t.seg).Radius(8).Padding(2).Label(m.Access()).Children(func() {
			for i, name := range []string{m.ModesLocal(), m.ModesNetwork()} {
				e := seg.Segment(i).Height(26).PaddingX(8).Radius(6).Justify(ui.Center).
					Cursor(ui.CursorPointer).Label(name)
				growAuto(c, e, &p.segTxt[i], &p.segW[i], 8+8)
				if i == s.access {
					e.Background(t.segOn).TextColor(t.strong).Shadow(0, 0, 0, 1, t.segShadow)
				} else {
					e.TextColor(t.muted)
				}
				e.Children(func() { txt(c, name, 12).FontWeight(500).NoWrap().Bind(&p.segTxt[i]) })
			}
		})
		addr := net.JoinHostPort(p.listenHost(), strconv.Itoa(p.pref.Port))
		var hint, warn string
		switch p.pref.Access {
		case engine.AccessNetwork:
			hint, warn = m.HintNetwork(addr), m.WarnNetwork()
		default:
			hint = m.HintLocal(addr)
		}
		txt(c, hint, 11.5).TextColor(t.muted)
		if warn != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Start).Background(t.warnBg).Radius(8).Padding(8, 10).Children(func() {
				ico(c, "alert-triangle", 2, 15).TextColor(t.warn).Margin(1, 0, 0, 0).Shrink(0)
				txt(c, warn, 11.5).TextColor(t.warnText).Grow(1).Shrink(1)
			})
		}
		p.portRow(c, f)
		if open {
			p.keyRow(c, f)
			if p.view.Caps.AllowedHosts {
				p.hostsRow(c, f)
			}
		}
	})

	// Model.
	card(c, f, 6).Children(func() {
		txt(c, m.Model(), 12.5).FontWeight(600).Padding(0, 0, 2, 0)
		p.contextRow(c, f)
		p.featureRows(c, f)
		opts := idleOptions
		if !slices.Contains(opts, p.pref.IdleRelease) {
			opts = append(slices.Clone(opts[:len(opts)-1]), p.pref.IdleRelease, 0)
			slices.SortFunc(opts[:len(opts)-1], func(a, b time.Duration) int { return int(a - b) })
		}
		dropdown(c, f, [2]any{"idle", s.epoch}, m.IdleUnload(), &s.idle, opts, func(d time.Duration) string {
			if d == 0 {
				return m.SettingsIdleNever()
			}
			return m.SettingsIdleAfter(format.Duration(f.loc, d))
		}, 200)
		if p.view.Caps.ModelUpdates {
			switchRow(c, f, "updates", m.CheckModelUpdates(), &s.updates)
		}
		p.modelNeed(c, f)
	})

	// App.
	card(c, f, 6).Children(func() {
		txt(c, m.App(), 12.5).FontWeight(600).Padding(0, 0, 2, 0)
		ui.Row(c).Gap(8).MinHeight(28).Children(func() {
			txt(c, m.EngineVersion(p.view.Engine), 12.5).Grow(1).MinWidth(0)
			if p.view.Version != "" {
				txt(c, p.view.Version, 12.5).TextColor(t.muted).Direction(ui.LeftToRight).NoWrap()
			}
			checking := p.checking()
			b := btn(c).Height(22).Radius(6).Background(t.chip).TextColor(t.text).PaddingX(8).Gap(5).
				Label(m.CheckUpdates()).Disabled(checking).OnClick(p.checkUpdate)
			if checking {
				b.TextColor(t.muted)
			}
			b.Children(func() {
				if checking {
					spinner(c, f, "loader-2", 2.4, 12, t.muted)
				}
				txt(c, m.CheckUpdates(), 12).NoWrap()
			})
		})
		// The result of the check in place of the note, until the screen
		// closes or another check starts.
		note, tone := m.UpdatesNote(), t.muted
		switch p.checkResult() {
		case core.CheckUpToDate:
			note, tone = m.CheckLatest(p.view.Version), t.okText
		case core.CheckAvailable:
			note, tone = m.CheckAvail(p.view.Latest), t.text
		case core.CheckFailed:
			note, tone = m.CheckFailed(), t.err
		}
		if p.checkResult() == core.CheckAvailable && p.view.Update == core.UpdateAvailable {
			// The version found, and the update itself. While a request
			// runs the main screen asks first, as its own Update does.
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 4, 0).Children(func() {
				txt(c, note, 11.5).TextColor(tone).Grow(1).MinWidth(0).Role(ui.RoleStatus)
				btn(c).Height(22).Radius(6).Background(t.fillBg).TextColor(t.fillText).PaddingX(10).Shrink(0).
					OnClick(func() {
						p.screen = screenMain
						p.doUpdate()
					}).Children(func() { txt(c, m.UpdBtn(), 12).FontWeight(600).NoWrap() })
			})
		} else {
			txt(c, note, 11.5).TextColor(tone).Padding(0, 0, 4, 0).Role(ui.RoleStatus)
		}
		dropdown(c, f, [2]any{"lang", s.epoch}, m.Language(), &s.lang, languages, func(tag string) string {
			if tag == "" {
				return m.System()
			}
			return languageName(tag)
		}, 220)
		dropdown(c, f, [2]any{"term", s.epoch}, m.Terminal(), &s.term, p.terminalIDs(), p.terminalName, 220)
		switchRow(c, f, "login", m.Login(), &s.login)
		switchRow(c, f, "blink", m.Blink(), &s.blink)
	})
}

// terminalIDs are the bundle ids of the terminals the select lists.
func (p *Panel) terminalIDs() []string {
	ids := make([]string, len(p.terminals))
	for i, t := range p.terminals {
		ids[i] = t.ID
	}
	return ids
}

// terminalName is the name the select shows for a bundle id. A terminal
// that is no longer installed shows as the first one, Terminal: that is the
// one agents start in then.
func (p *Panel) terminalName(id string) string {
	for _, t := range p.terminals {
		if t.ID == id {
			return t.Name
		}
	}
	if len(p.terminals) > 0 {
		return p.terminals[0].Name
	}
	return id
}

// checkUpdate asks for an update check, whose result the screen shows.
func (p *Panel) checkUpdate() {
	p.set.checkAsked, p.set.checkSeq = true, p.view.CheckSeq
	p.act.CheckUpdate()
}

// checking reports whether an update check runs: the supervisor says so,
// or the one asked for here has not reached the view yet.
func (p *Panel) checking() bool {
	s := &p.set
	return p.view.Checking || s.checkAsked && p.view.CheckSeq == s.checkSeq
}

// checkResult is the result of the check asked for on this screen; none
// while it runs or when none was asked for.
func (p *Panel) checkResult() core.CheckResult {
	if !p.set.checkAsked || p.checking() {
		return core.CheckNone
	}
	return p.view.Check
}

// applyChoices stores what the controls chose since the last frame.
func (p *Panel) applyChoices() {
	s, pr := &p.set, p.pref
	if engine.Access(s.access) != pr.Access || s.idle != pr.IdleRelease || s.lang != pr.Language || s.term != pr.Terminal ||
		s.updates != pr.ModelUpdates || s.login != pr.OpenAtLogin || s.blink != pr.AnimateIcon {
		p.commit(func(pr *Prefs) {
			pr.Access = engine.Access(s.access)
			pr.IdleRelease, pr.Language, pr.Terminal = s.idle, s.lang, s.term
			pr.ModelUpdates, pr.OpenAtLogin, pr.AnimateIcon = s.updates, s.login, s.blink
		})
	}
}

// listenHost is the address shown for the access chosen: for the network
// the computer's LAN address, which other devices use, when it is known.
func (p *Panel) listenHost() string {
	if p.pref.Access == engine.AccessNetwork {
		if p.lanHost != "" {
			return p.lanHost
		}
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func (p *Panel) restartBanner(c *ui.Context, f *frame) {
	t, m, v := f.pal, f.m, p.view
	if !v.PendingRestart && !serverDiff(p.set.opened, p.pref, v.Model.ID) {
		return
	}
	running := p.running()
	text := m.RestartStopped()
	if running {
		text = m.RestartNote()
	}
	// As settings-spec: the address changed, or the server asked for a
	// restart for a reason the panel cannot see.
	agents := running && v.Phase.Served() && (v.PendingRestart || p.pref.Port != v.Port || p.pref.Access != v.Access)
	ui.Row(c).Gap(8).Background(t.chip).Radius(8).Padding(8, 12).Role(ui.RoleStatus).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			txt(c, text, 11.5).TextColor(t.text)
			if agents {
				txt(c, m.RestartAgents(), 11.5).TextColor(t.muted)
			}
		})
		if running {
			btn(c).Height(24).Radius(6).Border(1, t.border).Background(t.btn).TextColor(t.text).PaddingX(10).Shrink(0).
				OnClick(func() {
					p.set.opened = p.pref
					p.set.opened.AllowedHosts = slices.Clone(p.pref.AllowedHosts)
					p.set.opened.Contexts = maps.Clone(p.pref.Contexts)
					p.set.opened.Options = maps.Clone(p.pref.Options)
					p.act.Restart()
				}).Children(func() { txt(c, m.Restart(), 12).NoWrap() })
		}
	})
}

// portRow is the port field, its check and "Use <free>".
func (p *Panel) portRow(c *ui.Context, f *frame) {
	t, m := f.pal, f.m
	s := &p.set
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).MinHeight(28).Children(func() {
			txt(c, m.Port(), 12.5).Grow(1)
			border := t.portBorder
			if s.portBad || s.busy != 0 {
				border = t.err
			}
			in := ui.TextInputBase(c.Key("port"), &s.portText).Width(72).Height(26).Radius(8).Border(1, border).
				Background(t.field).TextColor(t.text).FontSize(12.5).PaddingX(8).TextAlign(ui.End).Label(m.Port())
			if s.busy != 0 {
				in.Error(m.PortBusy(s.busy, s.free))
			}
			if s.focusPort && !p.measured {
				s.focusPort = false
				in.Focus()
			}
			// Typing only clears the old verdict; the port is checked and
			// stored on Enter or when the field loses the focus, so 8001
			// is not stored as 8, 80 and 800 on the way.
			if in.Changed() {
				s.portBad, s.busy, s.free = false, 0, 0
			}
			focused := in.Focused()
			if in.Submitted() || (s.portFocus && !focused && !p.measured) {
				p.portTyped()
			}
			if !p.measured {
				s.portFocus = focused
			}
		})
		if s.busy != 0 {
			ui.Row(c).Gap(8).Children(func() {
				msg := m.PortBusy(s.busy, s.free)
				txt(c, msg, 11.5).TextColor(t.err).Grow(1).Role(ui.RoleStatus)
				if s.free != 0 {
					free := s.free
					btn(c).Height(22).Radius(6).Background(t.chip).TextColor(t.text).PaddingX(10).Shrink(0).
						OnClick(func() {
							s.portText, s.portBad, s.busy, s.free = strconv.Itoa(free), false, 0, 0
							p.commit(func(pr *Prefs) { pr.Port = free })
						}).Children(func() { txt(c, m.PortTake(free), 12).NoWrap() })
				}
			})
		}
	})
}

// portTyped checks the typed port and stores it when it is a port number
// that can be used.
func (p *Panel) portTyped() {
	s := &p.set
	n, err := strconv.Atoi(strings.TrimSpace(s.portText))
	s.portBad = err != nil || n < 1 || n > 65535
	s.busy, s.free = 0, 0
	if s.portBad {
		return
	}
	p.checkPort(n)
	if s.busy == 0 && n != p.pref.Port {
		p.commit(func(pr *Prefs) { pr.Port = n })
	}
}

// maskKey shows a key's prefix and its last four characters:
// sk-splash-••••3f9a.
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	prefix := ""
	if i := strings.LastIndex(key, "-"); i >= 0 && i < len(key)-4 {
		prefix = key[:i+1]
	}
	tail := key
	if len(key) > 4 {
		tail = key[len(key)-4:]
	}
	return prefix + "••••" + tail
}

func (p *Panel) keyRow(c *ui.Context, f *frame) {
	t, m := f.pal, f.m
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(6).MinHeight(28).Children(func() {
			txt(c, m.ApiKey(), 12.5).Grow(1)
			monoTxt(c, maskKey(p.pref.APIKey), 12).TextColor(t.soft).Direction(ui.LeftToRight).NoWrap()
			small := func(label, name string, fn func()) {
				btn(c).Size(26, 26).Radius(8).Background(t.chip).TextColor(t.soft).Label(label).Tooltip(label).
					OnClick(fn).Children(func() { ico(c, name, 2, 14) })
			}
			small(m.CopyKey(), "copy", func() { p.act.Copy(p.pref.APIKey) })
			small(m.NewKey(), "refresh", func() {
				key := p.act.NewAPIKey()
				p.commit(func(pr *Prefs) { pr.APIKey = key })
			})
		})
		txt(c, m.KeyNote(), 11.5).TextColor(t.muted)
	})
}

// validHost reports whether s can be a host name: letters, digits, dots
// and hyphens.
func validHost(s string) bool {
	if s == "" || len(s) > 253 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, "..") {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

func (p *Panel) hostsRow(c *ui.Context, f *frame) {
	t, m := f.pal, f.m
	s := &p.set
	ui.Column(c).Gap(6).Children(func() {
		txt(c, m.Hosts(), 12.5)
		ui.Row(c).Wrap().Gap(6).Children(func() {
			for _, h := range p.pref.AllowedHosts {
				ui.Row(c).Key("host:" + h).Height(22).Radius(6).Background(t.chip).TextColor(t.text).
					PaddingStart(8).PaddingEnd(1).Gap(4).Children(func() {
					monoTxt(c, h, 12).Direction(ui.LeftToRight).NoWrap()
					label := m.Remove() + " " + format.Isolate(f.loc, h)
					host := h
					btn(c).Size(20, 20).Radius(5).TextColor(t.muted).Label(label).Tooltip(label).OnClick(func() {
						p.commit(func(pr *Prefs) {
							pr.AllowedHosts = slices.DeleteFunc(pr.AllowedHosts, func(x string) bool { return x == host })
						})
					}).Children(func() { ico(c, "x", 2.4, 12) })
				})
			}
			if s.adding {
				border := t.dash
				if s.hostBad {
					border = t.err
				}
				in := ui.TextInputBase(c.Key("host-new"), &s.hostText).Width(160).Height(22).Radius(6).
					Border(1, border).Background(t.field).TextColor(t.text).FontSize(12).Font(mono).PaddingX(8).
					Direction(ui.LeftToRight).Label(m.Add()).AutoFocus()
				if in.Changed() {
					s.hostBad = false
				}
				if in.OverlayShortcut(0, ui.KeyEscape) || in.Shortcut(0, ui.KeyEscape) {
					s.adding, s.hostText, s.hostBad = false, "", false
				}
				if in.Submitted() {
					h := strings.ToLower(strings.TrimSpace(s.hostText))
					switch {
					case !validHost(h):
						s.hostBad = true
					case slices.Contains(p.pref.AllowedHosts, h):
						s.adding, s.hostText = false, ""
					default:
						s.adding, s.hostText = false, ""
						p.commit(func(pr *Prefs) { pr.AllowedHosts = append(pr.AllowedHosts, h) })
					}
				}
			} else {
				btn(c).Height(22).Radius(6).Border(1, t.dash).BorderStyle(ui.BorderDashed).TextColor(t.soft).PaddingX(8).Gap(4).
					OnClick(func() { s.adding, s.hostText, s.hostBad = true, "", false }).Children(func() {
					ico(c, "plus", 2.4, 12)
					txt(c, m.Add(), 12).NoWrap()
				})
			}
		})
		txt(c, m.HostsNote(p.view.Engine), 11.5).TextColor(t.muted)
	})
}

// dropdown is a settings row with a select whose list opens upwards, its
// end 6 DIPs past the button's, as in the mockup. The list is width wide,
// or as wide as its longest item needs, up to dropdownMax; an item still
// too long ends in an ellipsis.
func dropdown[T comparable](c *ui.Context, f *frame, key any, label string, sel *T, opts []T, name func(T) string, width float32) {
	t := f.pal
	ui.Row(c).Gap(8).MinHeight(28).Children(func() {
		txt(c, label, 12.5).Grow(1)
		d := ui.SelectBase(c.Key(key), sel)
		d.Trigger.Height(26).Radius(8).Border(1, t.popBorder).Background(t.field).TextColor(t.text).
			PaddingStart(10).PaddingEnd(6).Gap(6).Cursor(ui.CursorPointer).Label(label).Children(func() {
			txt(c, name(*sel), 12.5).NoWrap()
			ico(c, "chevron-down", 2, 13).TextColor(t.muted)
		})
		d.Popup(func(pop ui.Element) {
			pop.AttachTo(d.Trigger, ui.AnchorTopEnd, ui.AnchorBottomEnd).Margin(0, 0, 6, 0)
			if f.rtl {
				pop.Left(-6)
			} else {
				pop.Left(6)
			}
			w := width
			for _, o := range opts {
				// The surface sets tabular figures, which MeasureText does
				// not: every digit is measured as a 0, as wide as they are.
				tw, _ := c.MeasureText(0, ui.Span{Text: tabular(name(o)), Size: 12.5})
				// The surface's padding and border, the item's padding,
				// its check column and the gap after it.
				w = max(w, tw+2*6+2+2*8+choiceLeadWidth+8+1)
			}
			w = min(w, dropdownMax)
			// 2 pt between rows, as in the engine and model lists, so a
			// highlighted row never touches the next one.
			popSurface(pop.Width(w).MinWidth(w).Padding(6).Gap(2), f)
			for _, o := range opts {
				on := o == *sel
				it := d.Item(o).Height(30).Radius(8).PaddingX(8).Gap(8).Cursor(ui.CursorPointer)
				if on || it.Highlighted() {
					it.Background(t.rowHover)
				}
				it.Children(func() {
					choiceLead(c, f, on)
					// The name sits at the start even when it reads the
					// other way, as العربية in an English list.
					txt(c, name(o), 12.5).SingleLine().Shrink(1).MinWidth(0)
				})
			}
		})
	})
}

// tabular is s with its digits as 0s, for measuring under tabular figures.
func tabular(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '1' && r <= '9' {
			return '0'
		}
		return r
	}, s)
}

// dropdownMax is the widest a dropdown list grows: the panel less the
// cards' margins.
const dropdownMax = Width - 2*16

// switchRow is a settings row that toggles as a whole, with the mockup's
// 32×20 switch at its end.
func switchRow(c *ui.Context, f *frame, key, label string, on *bool) {
	t := f.pal
	row := ui.SwitchBase(c.Key(key), on).Gap(8).MinHeight(28).Cursor(ui.CursorPointer).Label(label).FocusRing(false)
	ring := row.FocusVisible()
	row.Children(func() {
		txt(c, label, 12.5).Grow(1)
		track := ui.Row(c).Size(32, 20).Radius(10).Padding(0, 1).Shrink(0)
		if *on {
			track.Background(t.trackOn).Border(1, ui.Transparent).Justify(ui.End)
		} else {
			track.Background(t.trackOff).Border(1, t.trackOffBorder).Justify(ui.Start)
		}
		if ring {
			track.DrawOver(func(pt *ui.Painter, r ui.Rect) { pt.FocusRing(r, [4]float32{10, 10, 10, 10}) })
		}
		track.Children(func() { ui.Box(c).Size(16, 16).Radius(8).Background(t.knob) })
	})
}
