package panel

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
)

// engineCard is the engine choice: every engine with its logo and its
// version or "Not installed", the running one checked. Choosing one while
// our server runs asks first, since the switch stops it.
func (p *Panel) engineCard(c *ui.Context, f *frame) {
	t, m, v := f.pal, f.m, p.view
	if len(v.Engines) == 0 {
		return
	}
	busy := p.engineBusy()
	ui.Column(c).Gap(6).Padding(6, 0, 0, 0).Children(func() {
		ui.Column(c).Gap(2).Margin(0, -6).Role(ui.RoleRadioGroup).Label(m.Engine()).Children(func() {
			for _, e := range v.Engines {
				p.engineRow(c, f, e, false, busy)
			}
		})
		if p.switchTo != "" {
			ui.Column(c).Gap(8).Background(t.warnBg).Radius(8).Padding(8, 10).Role(ui.RoleStatus).Children(func() {
				note, act := p.switchTexts(f, p.switchTo)
				txt(c, note, 11.5).LineHeight(1.4).TextColor(t.warnText)
				ui.Row(c).Gap(6).Justify(ui.End).Children(func() {
					btn(c).Height(24).Radius(12).Border(1, t.outline).TextColor(t.text).PaddingX(10).
						OnClick(func() { p.switchTo = "" }).Children(func() { txt(c, m.CancelBtn(), 12).NoWrap() })
					to := p.switchTo
					btn(c).Height(24).Radius(12).Background(t.warnBtn).TextColor(t.warnText).PaddingX(10).
						OnClick(func() { p.switchEngine(to) }).Children(func() {
						txt(c, act, 12).FontWeight(600).NoWrap()
					})
				})
			})
		} else {
			note(c, f, m.EngineNote())
		}
	})
}

// engineRow is one engine of a list, in Settings and in the header's popup
// (menu): its logo, name, version or "Not installed", and a check badge on
// the running one's logo, as System Settings marks a chosen picture; the
// same in both lists. A busy list only shows.
func (p *Panel) engineRow(c *ui.Context, f *frame, e core.EngineInfo, menu, busy bool) {
	t, m := f.pal, f.m
	on := e.ID == p.view.EngineID
	status := e.Version
	if !e.Installed {
		status = m.EngineNotInstalled()
	}
	role := ui.RoleRadio
	if menu {
		role = ui.RoleMenuItemRadio
	}
	row := ui.ButtonBase(c).Key("engine:" + e.ID).Height(40).PaddingX(8).Radius(8).Gap(10).
		Role(role).Checked(on).Label(e.Name).Description(status)
	if busy {
		row.Disabled(true).Cursor(ui.CursorDefault)
	} else {
		id := e.ID
		row.Cursor(ui.CursorPointer).OnClick(func() { p.chooseEngine(id) })
	}
	// The check marks the running engine, as in a menu; the tint is for
	// the pointer, the focus and an engine about to be switched to.
	if p.switchTo == e.ID || (!busy && (row.Hovered() || row.FocusVisible())) {
		row.Background(t.rowHover)
	}
	row.Children(func() {
		ui.Box(c).Size(26, 26).Shrink(0).Children(func() {
			engineTile(c, f, e.Logo, 26, 0)
			if on {
				ring := f.pal.card
				if menu {
					ring = f.pal.pop
				}
				checkBadge(c, f, ring)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			txt(c, e.Name, 12.5).FontWeight(600).SingleLine()
			st := txt(c, status, 11).TextColor(t.muted).SingleLine()
			if e.Installed {
				st.Direction(ui.LeftToRight).AlignSelf(ui.Start)
			}
		})
	})
}

// checkBadge is the mark of a chosen tile: a check in the accent's disc
// over its bottom end corner, cut from the tile by a ring of the colour
// under it (the card's, or the popup's).
func checkBadge(c *ui.Context, f *frame, ring ui.Color) {
	t := f.pal
	ui.Box(c).Absolute().Bottom(-3).End(-3).Size(14, 14).Radius(7).Background(t.trackOn).
		Border(1.5, ring).Center().Children(func() {
		ico(c, "check", 3.4, 9).TextColor(ui.Hex("#FFFFFF"))
	})
}

// engineName is the display name of the engine with ID id.
func (p *Panel) engineName(id string) string {
	for _, e := range p.view.Engines {
		if e.ID == id {
			return e.Name
		}
	}
	return id
}

// engineBusy reports whether the engine cannot be switched now: the
// server is being stopped, an engine is being installed or updated, or
// a switch runs.
func (p *Panel) engineBusy() bool {
	v := p.view
	return v.Switching || v.Phase == core.PhaseInstalling || v.Phase == core.PhaseStopping ||
		v.Update == core.UpdateUpdating
}

// switcherOn reports whether the header offers the engine choice.
func (p *Panel) switcherOn() bool { return len(p.view.Engines) > 1 && !p.engineBusy() }

// engineInfo is the engine with ID id of the engine list; ok is false for
// an engine the list lacks.
func (p *Panel) engineInfo(id string) (e core.EngineInfo, ok bool) {
	for _, e := range p.view.Engines {
		if e.ID == id {
			return e, true
		}
	}
	return core.EngineInfo{}, false
}

// engineInstalled reports whether the engine with ID id is installed.
func (p *Panel) engineInstalled(id string) bool {
	e, ok := p.engineInfo(id)
	return !ok || e.Installed
}

// switchTexts are the question and the button of a switch to the engine
// with ID id: one that is not installed says that Homebrew installs it and
// how much disk it takes, as the install screen does.
func (p *Panel) switchTexts(f *frame, id string) (note, act string) {
	m, name := f.m, p.engineName(id)
	e, _ := p.engineInfo(id)
	size := format.MB(f.loc, e.Install.Size)
	switch {
	case p.engineInstalled(id):
		return m.EngineSwitchNote(name), m.EngineSwitchBtn()
	case p.running():
		return m.EngineInstallStopNote(name, size), m.EngineInstallBtn()
	}
	return m.EngineInstallNote(name, size), m.EngineInstallBtn()
}

// chooseEngine switches at once, or asks first when our server runs or
// the engine is to be installed: under the list in Settings, in a dialog
// on the main screen.
func (p *Panel) chooseEngine(id string) {
	p.enginesOpen = false
	switch {
	case id == p.view.EngineID:
		p.switchTo = ""
	case p.running() || !p.engineInstalled(id):
		p.switchTo = id
		if p.screen == screenMain {
			p.openDialog(dlgSwitch)
		}
	default:
		p.switchEngine(id)
	}
}

// switchEngine switches and goes back to the main screen, which shows the
// new engine: its server, or its install.
func (p *Panel) switchEngine(id string) {
	p.switchTo, p.dlg = "", dlgNone
	p.act.SwitchEngine(id)
	p.screen = screenMain
}

// contextRow is the context select of the selected model: Auto, the sizes
// up to the model's native window, the size chosen if it is another one,
// and "Other…", which opens a field for any size in K. A size past the
// native window comes with a warning.
func (p *Panel) contextRow(c *ui.Context, f *frame) {
	t, m, v := f.pal, f.m, p.view
	s := &p.set
	model := v.Model.ID
	if model == "" || len(v.Models) == 0 || v.Phase == core.PhaseExternal {
		return
	}
	native := v.Model.ContextLength
	cur := p.pref.Contexts[model]
	switch {
	case s.ctxModel != model: // another model: its own size, no field
		s.ctxModel, s.ctxSeen, s.ctx, s.ctxOther, s.ctxText, s.ctxBad = model, cur, cur, false, "", false
	case s.ctxSeen != cur: // stored meanwhile, here or elsewhere
		s.ctxSeen = cur
		if !s.ctxOther {
			s.ctx = cur
		}
	}
	switch {
	case s.ctx == ctxOtherOpt && !s.ctxOther:
		s.ctxOther, s.ctxText, s.ctxBad = true, "", false
	case s.ctx != ctxOtherOpt && s.ctx != cur: // chosen in the select
		s.ctxOther = false
		p.setContext(model, s.ctx)
		cur = s.ctx
	}

	opts := []int{0}
	for _, o := range ctxOptions {
		if native == 0 || o <= native {
			opts = append(opts, o)
		}
	}
	if cur > 0 && !slices.Contains(opts, cur) {
		opts = append(opts, cur)
		slices.Sort(opts)
	}
	opts = append(opts, ctxOtherOpt)
	// Opened from the context chip, the row blinks twice in the accent's
	// tint (ctxBlink). It reaches 8 DIPs past the row on each side, as a
	// highlighted list row does.
	hl := ui.Column(c).Key("ctx-flash").Margin(-4, -8).Padding(4, 8).Radius(8)
	if s.revealCtx && !p.measured {
		s.revealCtx, s.flashCtx = false, c.Now()
		hl.ScrollIntoView()
	}
	tint := ui.Color{}
	if !s.flashCtx.IsZero() {
		lit, next := ctxBlink(c.Now().Sub(s.flashCtx))
		if lit {
			tint = t.focus.Alpha(0.16)
		}
		if next > 0 {
			c.After(next)
		}
	}
	hl.Background(hl.AnimateColor("tint", tint, ui.Animation{Duration: 200 * time.Millisecond, Ease: ui.EaseInOut}))
	hl.Children(func() {
		dropdown(c, f, [3]any{"ctx", s.epoch, model}, m.Context(), &s.ctx, opts, func(n int) string {
			switch n {
			case 0:
				return m.CtxAuto()
			case ctxOtherOpt:
				return m.CtxOther()
			}
			return ctxName(f.loc, n)
		}, 200)
	})

	if s.ctxOther {
		rowSep(c, f)
		ui.Row(c).Gap(8).MinHeight(rowHeight).Children(func() {
			txt(c, m.CtxOtherLabel(), 12.5).Grow(1).MinWidth(0)
			border := t.portBorder
			if s.ctxBad {
				border = t.err
			}
			in := ui.TextInputBase(c.Key("ctx-other"), &s.ctxText).Width(72).Height(24).Radius(7).Border(1, border).
				Background(t.field).TextColor(t.text).FontSize(12.5).PaddingX(8).TextAlign(ui.End).
				Label(m.CtxOtherLabel()).AutoFocus()
			if s.ctxBad {
				in.Error(m.CtxBad())
			}
			if in.Changed() {
				s.ctxBad = false
			}
			if in.Shortcut(0, ui.KeyEscape) {
				s.ctx, s.ctxOther, s.ctxText, s.ctxBad = cur, false, "", false
			}
			if in.Submitted() {
				p.contextTyped(model, cur)
			}
		})
		if s.ctxBad {
			note(c, f, m.CtxBad()).TextColor(t.err).Role(ui.RoleStatus)
		}
	}

	switch {
	case native > 0 && cur > native:
		warnBox(c, f, m.CtxWarn(ctxName(f.loc, native)))
	case native > 0:
		note(c, f, m.CtxNative(ctxName(f.loc, native)))
	}
}

// contextTyped stores the size typed in K, when it is one the engine takes.
func (p *Panel) contextTyped(model string, cur int) {
	s := &p.set
	k, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(s.ctxText)), "K")))
	limit := p.view.Caps.MaxContext
	if limit <= 0 {
		limit = 16 << 20 // no limit of the engine's own: far past any model
	}
	if err != nil || k < 1 || k*1024 > limit {
		s.ctxBad = true
		return
	}
	s.ctx, s.ctxOther, s.ctxText, s.ctxBad = k*1024, false, "", false
	if s.ctx != cur {
		p.setContext(model, s.ctx)
	}
}

// setContext stores the context size of model; 0 is the engine's choice.
func (p *Panel) setContext(model string, tokens int) {
	p.commit(func(pr *Prefs) {
		if tokens <= 0 {
			delete(pr.Contexts, model)
			return
		}
		if pr.Contexts == nil {
			pr.Contexts = map[string]int{}
		}
		pr.Contexts[model] = tokens
	})
}
