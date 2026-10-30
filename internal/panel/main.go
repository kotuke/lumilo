package panel

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
)

// main builds the main screen, block by block in the mockup's order.
func (p *Panel) main(c *ui.Context, f *frame) {
	s := p.derive(c, f)
	// The supervisor publishes a View only when something changes: the
	// times the screen shows run on the panel's own clock.
	if d := p.clockTick(f, s); d > 0 {
		c.After(d)
	}
	p.header(c, f, &s)
	p.body(c, screenMain, func() {
		p.noticeBlock(c, f)
		if s.updAvail {
			p.updateNote(c, f)
		}
		if s.updFailed {
			v := p.view
			line, note := v.UpdateError, f.m.KeepOld(v.Version)
			if v.UpdateReason == core.UpdateReasonNoNewer {
				// brew ran fine: its last line is no error.
				line, note = "", f.m.UpdNoNewer()
			}
			p.errorBlock(c, f, f.m.UpdFailTitle(v.Engine, v.Latest), line, note, []errButton{
				{text: f.m.ActRetry(), fill: true, icon: "refresh", fn: p.doUpdate},
				{text: f.m.Log(), icon: "file-text", fn: p.act.OpenLog},
			})
		}
		p.serverCard(c, f, &s)
		if s.served {
			p.memoryCard(c, f, &s)
			p.speedCard(c, f, &s)
		}
		if s.showAgents {
			p.agentsCard(c, f, &s)
		}
	})
}

// clockTick is how long until a time the main screen shows changes its
// text, 0 when none runs: the next second while it counts seconds, else
// the next minute.
func (p *Panel) clockTick(f *frame, s mainState) time.Duration {
	v := p.view
	tick := time.Duration(0)
	take := func(d time.Duration) {
		if d > 0 && (tick == 0 || d < tick) {
			tick = d
		}
	}
	from := func(at time.Time) {
		if at.IsZero() {
			return
		}
		e := max(0, f.now.Sub(at))
		if e < time.Minute {
			take(time.Second - e%time.Second)
		} else {
			take(time.Minute - e%time.Minute)
		}
	}
	switch s.phase {
	case core.PhaseLoading, core.PhaseError, core.PhaseExternal:
		from(p.phaseAt)
	case core.PhaseReady, core.PhaseGenerating, core.PhaseReleased, core.PhaseRestoring:
		from(p.servedAt)
	case core.PhaseUnresponsive:
		take(time.Second) // "No response for", in seconds
	}
	if left := v.IdleLeft - f.now.Sub(p.viewAt); v.IdleLeft > 0 && left > 0 {
		// The countdown: whole minutes rounded up, the last in seconds.
		unit := time.Minute
		if left <= time.Minute {
			unit = time.Second
		}
		if r := left % unit; r > 0 {
			take(r)
		} else {
			take(unit)
		}
	}
	if s.served && !v.Speed.LastRequest.IsZero() {
		from(v.Speed.LastRequest) // "Last request … ago"
	}
	if p.noticeUntil.After(f.now) {
		take(p.noticeUntil.Sub(f.now))
	}
	return tick
}

// noticeFor is how long a notice shows.
const noticeFor = 5 * time.Second

// noticeBlock shows the View's last notice for a moment: what a command did
// not do, and the error under it when there is one.
func (p *Panel) noticeBlock(c *ui.Context, f *frame) {
	n := p.notice
	if !p.noticeUntil.After(f.now) || n.Kind == core.NoticeNone {
		return
	}
	t, m := f.pal, f.m
	var title string
	switch n.Kind {
	case core.NoticeAgentUnavailable:
		title = m.NoticeAgentUnavailable()
	case core.NoticeTerminalFailed:
		title = m.NoticeTerminalFailed()
	case core.NoticeKeyNotStored:
		title = m.NoticeKeyNotStored()
	case core.NoticeRemoveFailed:
		title = m.RmFailed()
	default:
		return
	}
	ui.Row(c).Key("notice").Gap(8).AlignItems(ui.Start).Background(t.warnBg).Radius(8).Padding(8, 12).
		Role(ui.RoleStatus).Children(func() {
		ico(c, "alert-triangle", 2, 15).TextColor(t.warn).Margin(1, 0, 0, 0).Shrink(0)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(3).Children(func() {
			txt(c, title, 11.5).LineHeight(1.4).TextColor(t.warnText)
			if n.Text != "" {
				monoTxt(c, n.Text, 11).TextColor(t.soft).Direction(ui.LeftToRight).TextAlign(ui.Start).AlignSelf(ui.Start)
			}
		})
	})
}

func (p *Panel) doUpdate() {
	if p.view.Phase == core.PhaseGenerating {
		p.openDialog(dlgUpdate)
		return
	}
	p.dlg = dlgNone
	p.act.Update()
}

func (p *Panel) openDialog(d dialog) {
	p.dlg = d
	p.modelsOpen, p.enginesOpen = false, false
}

func (p *Panel) quit(s *mainState) {
	if s.quit == dlgNone {
		p.act.Quit()
		return
	}
	if p.dlg == s.quit {
		p.dlg = dlgNone
		return
	}
	p.openDialog(s.quit)
}

// Header: engine tile, name and version (the engine choice), update chip,
// sub line, buttons.
func (p *Panel) header(c *ui.Context, f *frame, s *mainState) {
	t, m, v := f.pal, f.m, p.view
	hdr := ui.Row(c).Gap(6).Padding(2, 2, 0, 2).Bind(&p.headH)
	hdr.Children(func() {
		engineTile(c, f, v.Logo, 24, 3)
		ui.Column(c).Grow(1).MinWidth(0).Bind(&p.titleH).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				spans := []ui.Span{{Text: v.Engine, Size: 14, Weight: 600}}
				if v.Version != "" {
					// The mockup sets the version at 14 too, but digits are as tall
					// as capitals and next to the lowercase name they read larger:
					// 13 pt makes the version no larger than the name.
					spans = append(spans, ui.Span{Text: " " + format.Isolate(f.loc, v.Version), Size: versionSize, Weight: 400, Color: t.muted})
				}
				// The title ends in an ellipsis rather than run under what
				// follows it, should a language leave it too little room.
				title := func() ui.Element {
					return ui.RichText(c, spans...).FixedLineHeight(lineHeight(f.loc, 14, "", false)).SingleLine()
				}
				// The update chip shows the version it brings where it fits
				// beside the whole title, else only its arrow: the installed
				// version is not cut for it.
				chipText := true
				if room := p.titleH.Bounds(c).W; s.updAvail && room > 0 {
					w, _ := c.MeasureText(0, spans...)
					if p.switcherOn() {
						w += 3 + 13 // the chevron and its gap
					}
					cw, _ := c.MeasureText(0, ui.Span{Text: v.Latest, Size: 11, Weight: 600})
					chipText = w+6+5+11+3+cw+7 <= room
				}
				if chipText != p.chipText {
					p.chipText = chipText
					c.AnimationFrame() // the layout changes: measure again
				}
				if p.switcherOn() {
					p.engineSwitch(c, f, hdr, title, strings.TrimSpace(v.Engine+" "+v.Version))
				} else {
					title()
				}
				if s.updAvail {
					label := m.Avail(v.Latest)
					chip := btn(c).Radius(999).Background(t.chip).TextColor(t.text).PaddingY(2).Shrink(0).
						Label(label).Tooltip(label).OnClick(p.doUpdate)
					if chipText {
						chip.PaddingStart(5).PaddingEnd(7).Gap(3)
					} else {
						chip.PaddingX(4)
					}
					chip.Children(func() {
						ico(c, "arrow-up", 2.6, 11)
						if chipText {
							txt(c, v.Latest, 11).FontWeight(600).NoWrap().Direction(ui.LeftToRight)
						}
					})
				}
			})
			// One line: the short durations make it fit; if a language still
			// runs over, it ends in an ellipsis and does not push the buttons.
			txt(c, s.sub, 11).TextColor(t.muted).SingleLine()
		})
		if s.showChat {
			b := iconBtn(c, f, m.Chat(), "message", false, p.act.OpenChat)
			if s.chatOff {
				// Disabled already halves the opacity (MyGo paint.go): 0.3 in
				// all, clearly off beside the other buttons; the tooltip says
				// why.
				b.Disabled(true).Opacity(0.6).Cursor(ui.CursorDefault).Tooltip(m.ChatOff()).Description(m.ChatOff())
			}
		}
		iconBtn(c, f, m.Log(), "file-text", false, p.act.OpenLog)
		sb := iconBtn(c, f, m.Settings(), "settings", false, func() { p.openSettings(false) })
		if p.appReady() {
			// A new version of Lumilo waits for a restart, which Settings
			// offers: a dot on the button says so.
			sb.Description(m.AppUpdReady(p.app.st.Version)).DrawOver(func(pt *ui.Painter, r ui.Rect) {
				x := r.X + r.W - 7
				if f.rtl {
					x = r.X + 1
				}
				pt.Fill(ui.Rect{X: x, Y: r.Y + 1, W: 6, H: 6}, t.ok, 3)
			})
		}
		// The icon shows the theme the button switches to, as in Kvoto.
		if c.Theme().Dark {
			iconBtn(c, f, m.ThemeLight(), "sun", false, p.act.ToggleTheme)
		} else {
			iconBtn(c, f, m.ThemeDark(), "moon", false, p.act.ToggleTheme)
		}
		q := iconBtn(c, f, m.Quit(), "logout", true, func() { p.quit(s) })
		q.MarginStart(6).Expanded(p.dlg == dlgQuitBusy || p.dlg == dlgQuitUpdating)
	})
}

// engineSwitch is the engine's name and version as a button, like the model
// button: it opens the list of engines under the header. title draws the
// name and version.
func (p *Panel) engineSwitch(c *ui.Context, f *frame, hdr ui.Element, title func() ui.Element, label string) {
	t := f.pal
	b := ui.ButtonBase(c).Key("engine-switch").Shrink(1).MinWidth(0).Gap(3).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).TextColor(t.text).Role(ui.RolePopUpButton).Label(label).Expanded(p.enginesOpen)
	b.OnClick(func() {
		p.enginesOpen = !p.enginesOpen
		p.modelsOpen, p.dlg = false, dlgNone
	})
	b.Children(func() {
		title().Shrink(1).MinWidth(0)
		ico(c, "chevron-down", 2, 13).TextColor(t.muted).Shrink(0)
	})
	ui.PopoverBase(c, b, &p.enginesOpen, func(pop ui.Element) {
		// Under the header, at the start of the cards, as the model list.
		pop.AttachTo(hdr, ui.AnchorBottomStart, ui.AnchorTopStart).Top(engineListGap)
		if f.rtl {
			pop.Left(-7)
		} else {
			pop.Left(7)
		}
		popSurface(pop.Bind(&p.popup).Width(330).Padding(6), f).Role(ui.RolePopup)
		// 2 pt between rows, as in the model list, so a highlighted row
		// never touches the next one.
		ui.Column(c).Gap(2).Children(func() {
			for _, e := range p.view.Engines {
				p.engineRow(c, f, e, true, false)
			}
		})
	})
}

// versionSize is the point size of the version in the header; the name is 14.
const versionSize = 13

// engineListGap is the space between the header and the list of engines.
const engineListGap = 6

func (p *Panel) updateNote(c *ui.Context, f *frame) {
	t, m, v := f.pal, f.m, p.view
	ui.Column(c).Background(t.chip).Radius(8).Padding(8, 12).Gap(8).Children(func() {
		txt(c, m.UpdNote(v.Engine, v.Latest), 11.5).LineHeight(1.4).TextColor(t.soft)
		ui.Row(c).Gap(10).Children(func() {
			btn(c).Height(22).Radius(6).Border(1, t.border).Background(t.btn).TextColor(t.text).PaddingX(10).
				OnClick(p.doUpdate).Children(func() { txt(c, m.UpdBtn(), 12).FontWeight(500).NoWrap() })
			btn(c).Height(22).Gap(4).TextColor(t.soft).OnClick(func() {
				if v.NotesURL != "" {
					p.act.OpenURL(v.NotesURL)
				}
			}).Children(func() {
				txt(c, m.WhatsNew(), 12).Underline().NoWrap()
				mirror(ico(c, "external-link", 2, 12), f.rtl)
			})
		})
	})
}

type errButton struct {
	text string
	fill bool
	icon string
	fn   func()
}

// errorBlock is the red block of the top error and the card error.
func (p *Panel) errorBlock(c *ui.Context, f *frame, title, line, note string, buttons []errButton) {
	t := f.pal
	ui.Column(c).Background(t.errBg).Radius(8).Padding(8, 10).Gap(6).Role(ui.RoleStatus).Children(func() {
		txt(c, title, 12.5).FontWeight(600).TextColor(t.err)
		if line != "" {
			monoTxt(c, line, 11.5).TextColor(t.soft).Direction(ui.LeftToRight).TextAlign(ui.Start).AlignSelf(ui.Start)
		}
		ui.Row(c).Gap(6).Padding(2, 0, 0, 0).Children(func() {
			for _, b := range buttons {
				e := btn(c).Height(22).Radius(6).Gap(5).TextColor(t.err).OnClick(b.fn)
				if b.icon != "" {
					e.PaddingStart(6).PaddingEnd(8)
				} else {
					e.PaddingX(8)
				}
				if b.fill {
					e.Background(t.errBtn)
				} else {
					e.Border(1, t.errLine)
				}
				e.Children(func() {
					if b.icon != "" {
						ico(c, b.icon, 2, 13)
					}
					txt(c, b.text, 12).FontWeight(500).NoWrap()
				})
			}
		})
		if note != "" {
			txt(c, note, 11).TextColor(t.muted)
		}
	})
}
