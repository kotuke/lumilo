package panel

import (
	"github.com/egoist/mygo/ui"
)

// dialogs shows the confirmation over the panel: top 44, end 10, as in
// the mockup (plus the panel's 1 DIP border).
func (p *Panel) dialogs(c *ui.Context, f *frame) {
	if p.dlg == dlgNone {
		return
	}
	t, m, v := f.pal, f.m, p.view
	type dbtn struct {
		text   string
		bg, fg ui.Color
		weight int
		fn     func()
		off    bool // shown, but not yet to be clicked
	}
	var title, note string
	var buttons []dbtn
	cut := m.CutRequests(max(1, v.Snapshot.Active+v.Snapshot.Queued))
	switch p.dlg {
	case dlgQuitBusy:
		title, note = m.QuitTitle(), cut
		buttons = []dbtn{{m.QuitBtn(), t.errBtn, t.err, 600, func() { p.dlg = dlgNone; p.act.Quit() }, false}}
	case dlgQuitUpdating:
		title, note = m.QuitUpdTitle(v.Engine), m.QuitUpdNote()
		buttons = []dbtn{{m.QuitAfter(), t.fillBg, t.fillText, 600, func() { p.dlg = dlgNone; p.quitAfterUpdate = true }, false}}
	case dlgUpdate:
		title, note = m.UpdDlgTitle(), cut
		buttons = []dbtn{
			{m.UpdLater(), t.chip, t.text, 500, func() { p.dlg = dlgNone; p.updateWhenIdle = true }, false},
			{m.UpdBtn(), t.fillBg, t.fillText, 600, func() { p.dlg = dlgNone; p.act.Update() }, false},
		}
	case dlgKill:
		title, note = m.KillTitle(v.Engine), m.KillNote()
		buttons = []dbtn{{m.KillBtn(), t.errBtn, t.err, 600, func() { p.dlg = dlgNone; p.act.KillExternal() }, false}}
	case dlgSwitch:
		// The same words as the note under Settings' engine list.
		var act string
		title, act = p.switchTexts(f, p.switchTo)
		to := p.switchTo
		buttons = []dbtn{{act, t.warnBtn, t.warnText, 600, func() { p.switchEngine(to) }, false}}
	case dlgRemove:
		var ready bool
		title, note, ready = p.removeTexts(f)
		id := p.removeID
		buttons = []dbtn{{m.RmBtn(), t.errBtn, t.err, 600, func() {
			// The list shows the model being deleted.
			p.dlg, p.removeID, p.modelsOpen = dlgNone, "", true
			p.act.RemoveModel(id)
		}, !ready}}
	}
	cancel := func() {
		if p.dlg == dlgRemove {
			p.closeRemove()
		}
		p.dlg = dlgNone
		p.switchTo = ""
	}
	ui.Overlay(c, func() {
		back := ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Modal()
		back.Children(func() {
			d := ui.Column(c).Bind(&p.dialog).Absolute().Padding(12).Gap(12)
			if hd := p.headH.Bounds(c); p.dlg == dlgSwitch && hd.H > 0 {
				// It answers the engine list: where the list was, under the
				// engine's name.
				d.Top(hd.Y + hd.H + engineListGap).Width(330)
				if f.rtl {
					d.Right(Width - hd.X - hd.W + 7)
				} else {
					d.Left(hd.X + 7)
				}
			} else {
				d.Top(45).End(11).Width(320)
			}
			popSurface(d, f).Role(ui.RoleAlertDialog).Label(title).Description(note).
				Children(func() {
					ui.Column(c).Gap(3).Children(func() {
						txt(c, title, 12.5).FontWeight(600)
						if note != "" {
							txt(c, note, 11.5).TextColor(t.muted)
						}
					})
					ui.Row(c).Wrap().Justify(ui.End).Gap(6).Children(func() {
						btn(c).Height(26).Radius(8).Border(1, t.outline).TextColor(t.text).PaddingX(12).AutoFocus().
							OnClick(cancel).Children(func() { txt(c, m.CancelBtn(), 12).NoWrap() })
						for _, b := range buttons {
							e := btn(c).Height(26).Radius(8).Background(b.bg).TextColor(b.fg).PaddingX(12)
							if b.off {
								e.Disabled(true).Cursor(ui.CursorDefault).Opacity(0.6)
							} else {
								e.OnClick(b.fn)
							}
							e.Children(func() { txt(c, b.text, 12).FontWeight(b.weight).NoWrap() })
						}
					})
				})
			if back.OverlayShortcut(0, ui.KeyEscape) {
				cancel()
			}
		})
	})
}
