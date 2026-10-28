package panel

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
)

// Server card.
func (p *Panel) serverCard(c *ui.Context, f *frame, s *mainState) {
	t, m, v := f.pal, f.m, p.view
	crd := card(c, f, 10).Bind(&p.cardH)
	crd.Children(func() {
		if s.statusOnly {
			// Install states show no pill: screen readers still hear it.
			txt(c, s.pill, 11).Role(ui.RoleStatus).Absolute().Size(1, 1).Opacity(0)
		}
		if s.showHead {
			ui.Row(c).Gap(8).Children(func() {
				switch {
				case s.hasModel:
					p.modelButton(c, f, s, crd)
				case s.suggest:
					txt(c, m.SuggestTitle(), 14).FontWeight(600).Grow(1).MinWidth(0)
				case s.isNotInstalled || s.isNoInstaller:
					ui.Row(c).Grow(1).Gap(10).Children(func() {
						title := m.InstTitle(v.Engine)
						// The install texts and this logo are Homebrew's
						// (engine.InstallHomebrew), the one install method
						// so far; the names and links come from
						// View.Install.
						if s.isNoInstaller {
							title = m.BrewTitle()
							ui.Box(c).Size(32, 32).Radius(8).Background(t.brewTile).Center().Shrink(0).Children(func() {
								ico(c, "logo-homebrew", 0, 20).TextColor(homebrewInk)
							})
						}
						txt(c, title, 14).FontWeight(600)
					})
				}
				if s.showPill {
					p.pill(c, f, s)
				}
			})
		}
		if s.suggest {
			p.suggestions(c, f)
		}
		if s.isProgress {
			ui.Column(c).Gap(6).Children(func() {
				if pct := p.downloadPercent(); s.downloading && pct >= 0 {
					p.downloadBar(c, f, s.progressText, pct)
				} else {
					sweep(c, f, s.progressText)
				}
				txt(c, s.progressText, 11.5).TextColor(t.muted)
				if s.cmd != "" {
					command(c, f, s.cmd)
				}
			})
		}
		if s.isNotInstalled {
			ui.Column(c).Gap(10).Children(func() {
				txt(c, m.InstText(v.Engine, format.MB(f.loc, v.Install.Size)), 12).LineHeight(1.4).TextColor(t.soft)
				if v.Install.InstallCommand != "" {
					command(c, f, v.Install.InstallCommand)
				}
				btn(c).Height(30).Radius(8).Background(t.startBg).TextColor(t.startText).Gap(7).OnClick(p.act.Install).Children(func() {
					ico(c, "download", 2, 14)
					txt(c, m.InstBtn(), 12.5).FontWeight(600)
				})
				txt(c, m.InstNote(), 11.5).TextColor(t.muted).TextAlign(ui.Center)
			})
		}
		if s.isNoInstaller {
			pre := v.Install.Prerequisite
			ui.Column(c).Gap(10).Children(func() {
				txt(c, m.BrewText(v.Engine), 12).LineHeight(1.4).TextColor(t.soft)
				ui.Column(c).Gap(6).Children(func() {
					btn(c).Height(30).Radius(8).Background(t.chip).TextColor(t.text).Gap(7).
						OnClick(func() { p.act.OpenURL(pre.URL) }).Children(func() {
						txt(c, m.OpenBrew(pre.Host), 12.5).FontWeight(600)
						mirror(ico(c, "external-link", 2, 14), f.rtl)
					})
					btn(c).Height(30).Radius(8).Border(1, t.outline).TextColor(t.text).OnClick(p.act.Recheck).Children(func() {
						txt(c, m.Recheck(), 12.5).FontWeight(500)
					})
				})
				if pre.Arch != "" && pre.OS != "" {
					txt(c, m.BrewReq(pre.Arch, pre.OS), 11.5).TextColor(t.muted).TextAlign(ui.Center)
				}
			})
		}
		if s.isError {
			p.cardError(c, f, s)
		}
		if s.isInstallFailed {
			p.errorBlock(c, f, m.InstFailTitle(v.Engine), v.UpdateError, "", []errButton{
				{text: m.ActRetry(), fill: true, icon: "refresh", fn: p.act.Install},
				{text: m.Log(), icon: "file-text", fn: p.act.OpenLog},
			})
		}
		if s.isExternal {
			txt(c, m.ExtNote(), 11.5).LineHeight(1.4).TextColor(t.soft).Background(t.chip).Radius(8).Padding(8, 10)
		}
		if s.warnOn {
			ui.Column(c).AlignItems(ui.Start).Gap(8).Background(t.warnBg).Radius(8).Padding(8, 10).Role(ui.RoleStatus).Children(func() {
				txt(c, s.warnText, 11.5).LineHeight(1.4).TextColor(t.warnText)
				btn(c).Height(22).Radius(6).Background(t.warnBtn).TextColor(t.warnText).PaddingX(8).OnClick(p.act.ForceStop).Children(func() {
					txt(c, m.ActForce(), 12).FontWeight(500).NoWrap()
				})
			})
		}
		if s.hasControls {
			p.controls(c, f, s)
		}
	})
}

// command is an installer's command line, selectable, left to right.
func command(c *ui.Context, f *frame, cmd string) {
	t := f.pal
	monoTxt(c, cmd, 11.5).TextColor(t.soft).Background(t.chip).Radius(6).Padding(6, 8).
		Direction(ui.LeftToRight).TextAlign(ui.Start).Selectable()
}

func (p *Panel) cardError(c *ui.Context, f *frame, s *mainState) {
	m, v := f.m, p.view
	var title string
	switch s.err {
	case errPort:
		title = m.ErrTitlePort(v.Port)
	case errModel:
		// Only an offline run asks for the model-updates switch; online
		// the download itself failed.
		title = m.ErrTitleModelOnline()
		if v.Failure.Offline {
			title = m.ErrTitleModel()
		}
	case errNoVision:
		title = m.ErrTitleNoVision()
	case errMemory:
		var waited time.Duration
		if !p.loadAt.IsZero() && p.phaseAt.After(p.loadAt) {
			waited = p.phaseAt.Sub(p.loadAt)
		}
		title = m.ErrTitleMemory(v.Engine, format.Duration(f.loc, waited))
	case errNoKey:
		title = m.ErrTitleNoKey()
	default:
		title = m.ErrTitleExit(v.Failure.Code)
	}
	logBtn := func(fill bool) errButton {
		return errButton{text: m.Log(), fill: fill, icon: "file-text", fn: p.act.OpenLog}
	}
	var buttons []errButton
	switch s.err {
	case errPort:
		buttons = []errButton{{text: m.ErrActPort(), fill: true, fn: func() { p.openSettings(true) }}, logBtn(false)}
	case errModel:
		buttons = []errButton{{text: m.ErrActModel(), fill: true, fn: func() {
			if s.modelCanOpen {
				p.modelsOpen, p.enginesOpen, p.dlg = true, false, dlgNone
			}
		}}, logBtn(false)}
	case errNoKey:
		buttons = []errButton{{text: m.ErrActNoKey(), fill: true, icon: "settings", fn: func() { p.openSettings(false) }}, logBtn(false)}
	case errNoVision:
		id := v.Model.ID
		buttons = []errButton{{text: m.ErrActTextOnly(), fill: true, fn: func() { p.act.ServeTextOnly(id) }}, logBtn(false)}
	default:
		buttons = []errButton{logBtn(true)}
	}
	p.errorBlock(c, f, title, v.Failure.Line, "", buttons)
}

// pulseHalf is half the pill's pulse (sp-pulse, 1.6 s).
const pulseHalf = 800 * time.Millisecond

// pill is the status pill with its icon, which spins or pulses.
func (p *Panel) pill(c *ui.Context, f *frame, s *mainState) {
	ui.Row(c).Gap(6).Padding(3, 8).Radius(999).Background(s.pillBg).TextColor(s.pillText).
		Role(ui.RoleStatus).Shrink(0).Children(func() {
		if s.pillAnim == animSpin && !f.still {
			spinner(c, f, s.pillIcon, 2.4, 12, s.dot)
			txt(c, s.pill, 11).FontWeight(600).NoWrap()
			return
		}
		e := ico(c, s.pillIcon, 2.4, 12).TextColor(s.dot).Shrink(0)
		sx := float32(1)
		if s.pillFlip && f.rtl {
			sx = -1
		}
		e.Scale(sx, 1)
		if s.pillAnim == animPulse && !f.still {
			// sp-pulse: opacity 1 → .35 and scale 1 → .7 at the middle,
			// ease-in-out both ways, 1.6 s. Two poses, each half of it:
			// the view builds again at each half to head for the other,
			// and the frames between only paint.
			now := c.Now()
			at := time.Duration(now.UnixNano()) % (2 * pulseHalf)
			pose := ui.Pose{Scale: 1}
			if at < pulseHalf {
				pose = ui.Pose{Scale: 0.7, Fade: 0.65}
			}
			e.AnimatePose(pose, ui.Animation{Duration: pulseHalf, Ease: ui.EaseInOut})
			c.After(pulseHalf - at%pulseHalf)
		}
		txt(c, s.pill, 11).FontWeight(600).NoWrap()
	})
}

// sweep is the loading bar: a block of 6 lit segments runs to the end of
// the bar and back while the work has no measurable progress, in frames
// that only paint; it stands still in the middle, as in the mockup, when
// motion is reduced. label is what it waits for.
func sweep(c *ui.Context, f *frame, label string) ui.Element {
	t, rtl, still := f.pal, f.rtl, f.still
	return ui.Box(c).Height(8).Role(ui.RoleProgress).Label(label).Draw(func(pt *ui.Painter, r ui.Rect) {
		start := 9
		if !still {
			ph := float32(time.Duration(pt.Now().UnixNano())%sweepPeriod) / float32(sweepPeriod)
			start = int(ui.EaseInOut(min(ph*2, 2-ph*2))*(30-sweepLen) + 0.5)
			pt.AnimationFrame()
		}
		paintSegments(pt, r, rtl, 2, func(i int) ui.Color {
			if i >= start && i < start+sweepLen {
				return t.warnBar
			}
			return t.segEmpty
		})
	})
}

// downloadBar is the loading bar of a download whose size is known: the
// segments up to the fetched part are filled, in the colour of the running
// block, and nothing sweeps. label is the progress text.
func (p *Panel) downloadBar(c *ui.Context, f *frame, label string, pct float64) {
	t := f.pal
	bar := segments(c, f, 8, 2, func(i int) ui.Color {
		if filled(i, 30, pct) {
			return t.warnBar
		}
		return t.segEmpty
	})
	total := p.downloadTotal()
	bar.Role(ui.RoleProgress).Label(label).Range(0, float64(total), float64(min(p.view.DownloadBytes, total)))
}

// modelButton shows the model and opens the list of models.
func (p *Panel) modelButton(c *ui.Context, f *frame, s *mainState, crd ui.Element) {
	t, m, v := f.pal, f.m, p.view
	name := v.Model.Name
	if name == "" {
		name = v.Model.ID
	}
	if name == "" {
		name = m.ErrActModel()
	}
	var sub []string
	for _, x := range []string{v.Model.Owner, v.Model.Variant} {
		if x != "" {
			sub = append(sub, x)
		}
	}
	b := ui.ButtonBase(c).Grow(1).MinWidth(0).Height(32).Gap(8).Justify(ui.Start).PaddingEnd(6).TextColor(t.text).
		Role(ui.RolePopUpButton).Label(name).Expanded(p.modelsOpen && s.modelCanOpen)
	if s.modelTitle != "" {
		b.Tooltip(s.modelTitle)
	}
	if s.modelCanOpen {
		b.Cursor(ui.CursorPointer).OnClick(func() {
			p.modelsOpen = !p.modelsOpen
			p.enginesOpen = false
			p.dlg = dlgNone
			p.addingModel, p.addText, p.addBad = false, "", false
		})
	}
	b.Children(func() {
		ui.Column(c).MinWidth(0).Shrink(1).Children(func() {
			txt(c, name, 14).FontWeight(600).SingleLine()
			if len(sub) > 0 {
				// Left to right, under the start of the name's line.
				e := txt(c, strings.Join(sub, " · "), 11.5).TextColor(t.muted).SingleLine().Direction(ui.LeftToRight)
				if f.rtl {
					e.AlignSelf(ui.End)
				}
			}
		})
		if s.modelCanOpen {
			ico(c, "chevron-down", 2, 14).TextColor(t.muted).Shrink(0)
		}
	})
	if !s.modelCanOpen {
		return
	}
	ui.PopoverBase(c, b, &p.modelsOpen, func(pop ui.Element) {
		// Inside the card at top 50 and start 6, as the mockup places it.
		pop.AttachTo(crd, ui.AnchorTopStart, ui.AnchorTopStart).Top(51)
		if f.rtl {
			pop.Left(-7)
		} else {
			pop.Left(7)
		}
		popSurface(pop.Bind(&p.popup).Width(330).Padding(6), f).Role(ui.RolePopup)
		// 2 pt between rows, as in the other lists, so a highlighted row
		// never touches the next one.
		list := ui.Column(c).Gap(2)
		if len(v.Models)+len(v.Downloads) > 6 {
			list = ui.Scroll(c).MaxHeight(6*42 - 2).Gap(2)
		}
		list.Children(func() {
			for _, md := range v.Models {
				p.modelRow(c, f, md)
			}
			// Models on their way: not to be picked yet.
			for _, j := range v.Downloads {
				if j.State != core.DownloadDone {
					p.jobRow(c, f, j, nil, true)
				}
			}
		})
		ui.Box(c).Height(2)
		p.addModel(c, f, true)
		txt(c, m.PopNote(), 11).TextColor(t.muted).Padding(6, 8, 4, 8)
	})
}

// addModel is "Add model…" and, once clicked, the field for a model name
// that the engine downloads on start. inPopup lays it out as a row of the
// model list; otherwise it is a link under the suggestions.
func (p *Panel) addModel(c *ui.Context, f *frame, inPopup bool) {
	t, m, v := f.pal, f.m, p.view
	if !p.addingModel {
		b := ui.ButtonBase(c).Key("add-model").Gap(8).Cursor(ui.CursorPointer).TextColor(t.soft).Label(m.AddModel())
		if inPopup {
			b.Height(34).PaddingX(8).Radius(8).Justify(ui.Start).Role(ui.RoleMenuItem)
			if b.Hovered() || b.FocusVisible() {
				b.Background(t.rowHover)
			}
		} else {
			b.Height(28).AlignSelf(ui.Start)
		}
		b.OnClick(func() {
			p.addingModel, p.addText, p.addBad = true, "", false
			p.add.reset(p.act)
		}).Children(func() {
			ico(c, "plus", 2.2, 14)
			txt(c, m.AddModel(), 12.5).NoWrap()
		})
		return
	}
	example := "owner/repo:variant"
	if len(v.Suggestions) > 0 {
		example = v.Suggestions[0].ID
	}
	p.addField(c, f, inPopup, example)
}

// submitModel downloads the typed model when the engine accepts its name.
func (p *Panel) submitModel() {
	if p.downloadBlocked() {
		return
	}
	md, err := p.act.ParseModel(strings.TrimSpace(p.addText))
	if err != nil {
		p.addBad = true
		return
	}
	// The variant's size, which the progress counts toward.
	var o engine.ModelOptions
	if repo, tag, _ := splitName(p.addText); repo != "" && strings.EqualFold(repo, p.add.varsFor) {
		for _, vr := range p.add.vars {
			if strings.EqualFold(vr.Tag, tag) {
				if p.dlTotal == nil {
					p.dlTotal = map[string]int64{}
				}
				p.dlTotal[md.ID] = vr.Bytes
			}
		}
		// A repository without a vision projector: an engine that refuses
		// such a model with vision gets it for text only from the start.
		o.NoVision = p.add.full && textOnlyRepo(p.add.info) && p.view.Caps.VisionNeedsProjector
	}
	p.addingModel, p.addText, p.addBad = false, "", false
	// A download apart from the server shows in the model list, which
	// stays open; one on the server's start shows in the server card.
	if !p.view.DownloadApart {
		p.modelsOpen = false
	}
	p.add.reset(p.act)
	p.act.Download(md.ID, o)
}

// suggestions replace the model button when the engine has no model: the
// engine's suggested models with their makers' logos, sizes and a
// download button each, then "Add model…".
func (p *Panel) suggestions(c *ui.Context, f *frame) {
	t, m, v := f.pal, f.m, p.view
	ui.Column(c).Gap(2).Children(func() {
		txt(c, m.SuggestNote(), 11.5).TextColor(t.muted).LineHeight(1.4).Padding(0, 0, 4, 0)
		// One download is the suggested one: the first that fits.
		first := -1
		for i, sg := range v.Suggestions {
			if first < 0 && p.fits(sg.MemoryBytes) {
				first = i
			}
		}
		for i, sg := range v.Suggestions {
			if j, ok := jobFor(v, sg.ID); ok {
				p.jobRow(c, f, j, sg.MakerLogo, false)
				continue
			}
			p.suggestionRow(c, f, sg, i == first)
		}
		// A typed model on its way.
		for _, j := range v.Downloads {
			if j.State != core.DownloadDone && !slices.ContainsFunc(v.Suggestions, func(sg engine.Suggestion) bool { return sg.ID == j.Model.ID }) {
				p.jobRow(c, f, j, nil, false)
			}
		}
		p.addModel(c, f, false)
	})
}

// paramsText is a parameter count in billions: "27B", "5.1B".
func paramsText(loc string, n int64) string {
	return format.Number(loc, float64(n)/1e9, 1) + "B"
}

// suggestionRow is a suggested model; primary marks the download the
// panel suggests first, the others are secondary.
func (p *Panel) suggestionRow(c *ui.Context, f *frame, sg engine.Suggestion, primary bool) {
	t, m := f.pal, f.m
	fits := p.fits(sg.MemoryBytes)
	params := m.ParamsDense(paramsText(f.loc, sg.Params))
	if sg.Active > 0 {
		params = m.ParamsMoe(paramsText(f.loc, sg.Params), paramsText(f.loc, sg.Active))
	}
	ui.Row(c).Key(sg.ID).Gap(10).MinHeight(46).PaddingY(4).Children(func() {
		if src := f.brands.image(sg.MakerLogo, engine.LogoSVG); src != nil {
			ui.Image(c, src).Size(22, 22).Shrink(0).Label(sg.Maker)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			txt(c, sg.Name, 12.5).FontWeight(600).SingleLine().Direction(ui.LeftToRight).AlignSelf(ui.Start)
			txt(c, params+" · "+format.GB(f.loc, sg.DownloadBytes), 11).TextColor(t.muted).SingleLine()
			if !fits {
				ui.Row(c).Gap(4).TextColor(t.warnText).Children(func() {
					ico(c, "alert-triangle", 2, 11).Shrink(0)
					txt(c, m.NeedsMemory(format.GB(f.loc, sg.MemoryBytes), p.ramText(f)), 11).Grow(1).Shrink(1)
				})
			}
		})
		id := sg.ID
		b := btn(c).Height(26).Radius(7).PaddingStart(8).PaddingEnd(10).Gap(5).Shrink(0).
			Label(m.Download() + " " + sg.Name)
		if reason := p.downloadBlock(&m); reason != "" {
			b.Disabled(true).Cursor(ui.CursorDefault).Opacity(0.6).Tooltip(reason).Description(reason)
		} else {
			b.OnClick(func() { p.act.Download(id, engine.ModelOptions{}) })
		}
		switch {
		case primary:
			b.Background(t.startBg).TextColor(t.startText)
		case fits:
			b.Background(t.chip).TextColor(t.text)
		default:
			// It does not fit: secondary, and quieter still.
			b.Border(1, t.border).TextColor(t.soft)
		}
		b.Children(func() {
			ico(c, "download", 2.2, 13)
			txt(c, m.Download(), 12).FontWeight(600).NoWrap()
		})
	})
}

func (p *Panel) modelRow(c *ui.Context, f *frame, md engine.Model) {
	t, m, v := f.pal, f.m, p.view
	sel := md.ID == v.Model.ID
	name := modelName(md)
	var sub []string
	if md.Variant != "" {
		sub = append(sub, md.Variant)
	}
	if md.SizeBytes > 0 {
		sub = append(sub, format.GB(f.loc, md.SizeBytes))
	}
	// Its files are being deleted: it shows so, and is not to be picked.
	removing := v.Removal.Running && v.Removal.Model == md.ID
	row := ui.ButtonBase(c).Key(md.ID).Height(40).PaddingX(8).Radius(8).Gap(10).
		Role(ui.RoleMenuItemRadio).Checked(sel).Label(name)
	if sel || !removing && (row.Hovered() || row.FocusVisible()) {
		row.Background(t.rowHover)
	}
	if removing {
		row.Cursor(ui.CursorDefault).Opacity(0.6).Description(m.RmDeleting())
	} else {
		row.Cursor(ui.CursorPointer).OnClick(func() {
			p.modelsOpen = false
			if !sel {
				p.act.SelectModel(md.ID)
			}
		})
	}
	row.Children(func() {
		choiceLead(c, f, sel)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			txt(c, name, 12.5).FontWeight(600).SingleLine()
			if removing {
				ui.Row(c).Gap(4).AlignItems(ui.Center).TextColor(t.muted).Children(func() {
					ico(c, "loader-2", 2, 11).Shrink(0)
					txt(c, m.RmDeleting(), 11).SingleLine()
				})
				return
			}
			fresh := downloaded(v, md.ID)
			if len(sub) > 0 || fresh {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if fresh {
						downloadedTag(c, f)
					}
					if len(sub) > 0 {
						txt(c, strings.Join(sub, " · "), 11).TextColor(t.muted).SingleLine().Shrink(1).MinWidth(0)
					}
				})
			}
		})
		if removing {
			return
		}
		p.featureToggles(c, f, md)
		if v.CanRemove {
			p.removeButton(c, f, md)
		}
	})
}

// controls: the chip row and the main button.
func (p *Panel) controls(c *ui.Context, f *frame, s *mainState) {
	t, m, v := f.pal, f.m, p.view
	ui.Row(c).Gap(6).Wrap().Children(func() {
		if s.showUpdChip {
			p.updatesChip(c, f)
		}
		if v.Address != "" {
			label := m.CopyAddr() + " " + v.Address
			btn(c).Direction(ui.LeftToRight).Height(22).Radius(6).Background(t.chip).TextColor(t.soft).PaddingX(8).Gap(4).
				Label(label).Tooltip(m.CopyAddr()).OnClick(func() { p.act.Copy(v.Address) }).Children(func() {
				if s.addrLock {
					ico(c, "lock", 2, 12)
				}
				monoTxt(c, v.Address, 11.5).NoWrap()
			})
		}
	})
	if s.showUpdChip && p.updToggled && v.PendingRestart && p.running() {
		// The server read the choice when it started.
		ui.Row(c).Key("updates-restart").Gap(8).AlignItems(ui.Center).Role(ui.RoleStatus).Children(func() {
			txt(c, m.AppliesOnRestart(), 11.5).TextColor(t.muted).Grow(1).Shrink(1).MinWidth(0)
			r := btn(c).Key("updates-restart-btn").Height(22).Radius(6).Border(1, t.border).Background(t.btn).
				TextColor(t.text).PaddingX(8).Shrink(0)
			r.OnClick(func() {
				p.updToggled = false
				p.act.Restart()
			}).Children(func() { txt(c, m.Restart(), 11.5).NoWrap() })
		})
	}
	if !s.showMain {
		return
	}
	b := btn(c).Height(30).Radius(8).Background(s.actBg).TextColor(s.actText).Gap(7)
	switch s.main {
	case mainStart:
		b.OnClick(p.act.Start)
	case mainStartOn:
		port := v.FreePort
		b.OnClick(func() { p.act.StartOn(port) })
	case mainStop:
		b.OnClick(p.act.Stop)
	case mainRestart:
		b.OnClick(p.act.Restart)
	case mainKill:
		b.OnClick(func() { p.openDialog(dlgKill) }).Role(ui.RoleButton).Expanded(p.dlg == dlgKill)
	case mainStopping:
		b.Opacity(0.6).Cursor(ui.CursorDefault)
	}
	b.Children(func() {
		switch s.main {
		case mainStart, mainStartOn, mainRestart:
			ico(c, "player-play-filled", 0, 13)
		case mainStop, mainKill:
			ico(c, "player-stop-filled", 0, 12)
		}
		txt(c, s.act, 12.5).FontWeight(600).NoWrap()
	})
}

// updatesChip is the model-updates chip, a toggle for the setting of
// Settings → "Check for model updates": off, the server starts offline and
// the chip is muted; on, it checks the hub on start, with a refresh icon.
// The tooltip says what the state does and what a click does.
func (p *Panel) updatesChip(c *ui.Context, f *frame) {
	t, m := f.pal, f.m
	on := p.pref.ModelUpdates
	label, tip, ink := m.NoUpdates(), m.UpdatesOffTip(), t.muted
	if on {
		label, tip, ink = m.UpdatesOn(), m.UpdatesOnTip(), t.soft
	}
	b := btn(c).Key("updates-chip").Height(22).Radius(6).Background(t.chip).PaddingX(8).Gap(4).
		Role(ui.RoleToggleButton).Checked(on).Label(label).Description(tip).Tooltip(tip)
	if b.Hovered() || b.FocusVisible() {
		b.Background(t.rowHover)
		ink = t.text
	}
	b.TextColor(ink).OnClick(func() { p.toggleModelUpdates() }).Children(func() {
		if on {
			ico(c, "refresh", 2, 12)
		}
		txt(c, label, 11.5).NoWrap()
	})
}

// toggleModelUpdates flips the model-updates setting from the chip; the
// settings screen shows the same switch, so it follows.
func (p *Panel) toggleModelUpdates() {
	on := !p.pref.ModelUpdates
	p.set.updates = on
	p.updToggled = true
	p.commit(func(pr *Prefs) { pr.ModelUpdates = on })
}

// The loading bar's running block: its length in segments and the time of
// one run there and back.
const (
	sweepLen    = 6
	sweepPeriod = 2400 * time.Millisecond
)
