package panel

import (
	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/internal/hf"
)

// budget is the memory the engine may use: what it reports while it runs,
// else three quarters of the Mac's memory (engine.Budget).
func (p *Panel) budget() int64 {
	return engine.Budget(p.memory, p.view.Snapshot.MemoryLimit, p.view.Caps.Memory.Share)
}

// judge is engine.Judge with the engine's numbers and the budget.
func (p *Panel) judge(md engine.Model, cfg engine.Config) (engine.Need, engine.Fit) {
	return engine.Judge(md, cfg, p.view.Caps.Memory, p.budget())
}

// repoModel is a repository seen on the hub as a Model to judge: weights of
// that many bytes and its vision projector. Its drafts are not known until
// it is installed: engine.Estimate counts the one the engine always runs
// (MemoryCaps.DefaultDraft).
func repoModel(info hf.RepoInfo, weights int64) engine.Model {
	md := engine.Model{SizeBytes: weights, ContextLength: info.Context, KVElements: info.KVElements}
	md.Features.DraftsUnknown = true
	if info.Vision > 0 {
		md.Features.Vision, md.Features.VisionBytes = true, info.Vision
	}
	return md
}

// needNote is the line about what a model needs in memory: muted when it
// fits, a warning when it does not, and a note when the engine will serve
// a shorter context than the native one.
func (p *Panel) needNote(c *ui.Context, f *frame, md engine.Model, cfg engine.Config) {
	t, m := f.pal, f.m
	need, fit := p.judge(md, cfg)
	if fit == engine.FitUnknown {
		return
	}
	size, total := format.GB(f.loc, need.Total()), format.GB(f.loc, p.budget())
	switch fit {
	case engine.FitsNot:
		ui.Row(c).Gap(4).AlignItems(ui.Start).TextColor(t.warnText).Role(ui.RoleStatus).Children(func() {
			ico(c, "alert-triangle", 2, 11).Shrink(0).Margin(2, 0, 0, 0)
			txt(c, m.MemTooMuch(size, total), 11).LineHeight(1.35).Grow(1).Shrink(1)
		})
	case engine.FitsShorter:
		txt(c, m.MemShorter(size, total), 11).TextColor(t.warnText).LineHeight(1.35)
	default:
		// Close to the budget (90 % or more): a warning before it is over.
		tone := t.muted
		if b := p.budget(); b > 0 && need.Total()*10 >= b*9 {
			tone = t.warnText
		}
		txt(c, m.MemNeeds(size), 11).TextColor(tone).LineHeight(1.35)
	}
}

// modelNeed is the memory estimate of the selected model with its
// settings, the last line of the Model card.
func (p *Panel) modelNeed(c *ui.Context, f *frame) {
	v := p.view
	md := v.Model
	if md.ID == "" || len(v.Models) == 0 || v.Phase == core.PhaseExternal || md.SizeBytes <= 0 {
		return
	}
	opt := p.pref.Options[md.ID]
	rowSep(c, f)
	ui.Column(c).Padding(8, 0).Children(func() {
		p.needNote(c, f, md, engine.Config{ContextTokens: p.pref.Contexts[md.ID], NoVision: opt.NoVision, Draft: opt.Draft})
	})
}

// featureRows are the model's vision and draft choices in the Model card,
// under the context: a switch for each the model has. A draft the engine
// always runs is a switch that is on and disabled, with the reason as its
// tooltip; several drafts are a select. Changes need a restart, which the
// restart banner offers.
func (p *Panel) featureRows(c *ui.Context, f *frame) {
	m, v := f.m, p.view
	md := v.Model
	if md.ID == "" || len(v.Models) == 0 || v.Phase == core.PhaseExternal {
		return
	}
	opt := p.pref.Options[md.ID]
	if md.Features.Vision {
		on := !opt.NoVision
		was := on
		tip := m.VisionTip(format.GB(f.loc, md.Features.VisionBytes))
		if md.Features.NoJPEG {
			tip += ". " + m.VisionNoJPEG()
		}
		featureSwitch(c, f, "vision:"+md.ID, m.Vision(), tip, &on, false)
		if on != was {
			p.setOptions(md.ID, func(o *engine.ModelOptions) { o.NoVision = !on })
		}
	}
	drafts := md.Features.Drafts
	switch {
	case len(drafts) == 0:
	case drafts[0].Always:
		on := true
		featureSwitch(c, f, "draft:"+md.ID, m.Draft(), m.DraftAlways(v.Engine, drafts[0].Name), &on, true)
	case len(drafts) == 1:
		on := engine.ActiveDraft(md, opt.Draft) != nil
		was := on
		featureSwitch(c, f, "draft:"+md.ID, m.Draft(), p.draftTip(f, drafts[0]), &on, false)
		if on != was {
			var d *engine.Draft
			if on {
				d = &drafts[0]
			}
			choice := draftChoice(drafts, d)
			p.setOptions(md.ID, func(o *engine.ModelOptions) { o.Draft = choice })
		}
	default:
		off := draftChoice(drafts, nil)
		opts := []string{off}
		for _, d := range drafts {
			opts = append(opts, d.ID)
		}
		sel, was := off, off
		if d := engine.ActiveDraft(md, opt.Draft); d != nil {
			sel, was = d.ID, d.ID
		}
		dropdown(c, f, [2]any{"draft", md.ID}, m.Draft(), &sel, opts, func(id string) string {
			for _, d := range drafts {
				if d.ID == id {
					return draftName(d)
				}
			}
			return m.DraftOff()
		}, 200)
		if sel != was {
			var pick *engine.Draft
			for i := range drafts {
				if drafts[i].ID == sel {
					pick = &drafts[i]
				}
			}
			choice := draftChoice(drafts, pick)
			p.setOptions(md.ID, func(o *engine.ModelOptions) { o.Draft = choice })
		}
	}
}

// draftChoice is the Config.Draft that runs d of drafts, or none when d is
// nil: "" for the engine's default draft, DraftOff to turn a default draft
// off, else the draft's ID.
func draftChoice(drafts []engine.Draft, d *engine.Draft) string {
	if d != nil {
		if d.Default {
			return ""
		}
		return d.ID
	}
	for _, x := range drafts {
		if x.Default {
			return engine.DraftOff
		}
	}
	return ""
}

// draftName is how a draft is named in a select: "MTP", or its file.
func draftName(d engine.Draft) string {
	if d.Kind == engine.DraftMTP {
		return "MTP"
	}
	return d.Name
}

func (p *Panel) draftTip(f *frame, d engine.Draft) string {
	m := f.m
	if d.Kind == engine.DraftMTP {
		return m.DraftMTPTip()
	}
	return m.DraftModelTip(d.Name, format.GB(f.loc, d.SizeBytes))
}

// setOptions changes the vision and draft choices of model.
func (p *Panel) setOptions(model string, change func(o *engine.ModelOptions)) {
	p.commit(func(pr *Prefs) {
		o := pr.Options[model]
		change(&o)
		if o == (engine.ModelOptions{}) {
			delete(pr.Options, model)
			return
		}
		if pr.Options == nil {
			pr.Options = map[string]engine.ModelOptions{}
		}
		pr.Options[model] = o
	})
}

// featureSwitch is switchRow with a tooltip; without an icon, as the other
// rows of the card. A disabled one only shows its state.
func featureSwitch(c *ui.Context, f *frame, key, label, tip string, on *bool, disabled bool) {
	rowSep(c, f)
	row := ui.SwitchBase(c.Key(key), on).Gap(8).MinHeight(rowHeight).Label(label).Description(tip).Tooltip(tip).FocusRing(false)
	if disabled {
		row.Disabled(true).Cursor(ui.CursorDefault)
	} else {
		row.Cursor(ui.CursorPointer)
	}
	ring := row.FocusVisible()
	row.Children(func() {
		txt(c, label, 12.5).Grow(1)
		switchTrack(c, f, *on, disabled, ring)
	})
}

// featureToggles are the eye and the bolt of a model row: image input and
// speculative decoding, each a toggle of the same choice as Settings →
// Model (it applies on Restart, which the restart banner offers). A draft
// the engine always runs only shows, with the reason as its tooltip. A
// click on one does not pick the row.
func (p *Panel) featureToggles(c *ui.Context, f *frame, md engine.Model) {
	m := f.m
	drafts := md.Features.Drafts
	if !md.Features.Vision && len(drafts) == 0 {
		return
	}
	opt := p.pref.Options[md.ID]
	ui.Row(c).Gap(2).Shrink(0).Children(func() {
		if md.Features.Vision {
			on := !opt.NoVision
			featureToggle(c, f, "vision:"+md.ID, "eye", m.Vision(), on, "", func() {
				p.setOptions(md.ID, func(o *engine.ModelOptions) { o.NoVision = on })
			})
		}
		switch {
		case len(drafts) == 0:
		case drafts[0].Always:
			featureToggle(c, f, "draft:"+md.ID, "bolt", m.Draft(), true, m.DraftAlways(p.view.Engine, drafts[0].Name), nil)
		default:
			// Several drafts: the toggle turns on the first, as the
			// select of Settings offers the others.
			on := engine.ActiveDraft(md, opt.Draft) != nil
			var d *engine.Draft
			if !on {
				d = &drafts[0]
			}
			choice := draftChoice(drafts, d)
			featureToggle(c, f, "draft:"+md.ID, "bolt", m.Draft(), on, "", func() {
				p.setOptions(md.ID, func(o *engine.ModelOptions) { o.Draft = choice })
			})
		}
	})
}

// featureOffOpacity is how much of the muted ink an off toggle keeps: clearly
// dimmer than the on one, whose ink is the row's own.
const featureOffOpacity = 0.45

// featureToggle is a 22×22 icon toggle named name, on or off: on in the
// row's ink, off in the muted ink at featureOffOpacity and struck through.
// fixed, when set, is a state that cannot change (a draft the engine always
// runs): it shows as on, with fixed as its tooltip, and has no hover, press
// or click; fn is the click otherwise. It is not Disabled: a disabled
// element draws at half opacity, as off, and shows no tooltip.
func featureToggle(c *ui.Context, f *frame, key, iconName, name string, on bool, fixed string, fn func()) {
	t, m := f.pal, f.m
	state := m.FeatureOff(name)
	if on {
		state = m.FeatureOn(name)
	}
	tip := state
	if fixed != "" {
		tip = fixed
	}
	ink := t.muted.Alpha(featureOffOpacity)
	if on {
		ink = t.soft
	}
	// The label says the state, as the tooltip does: "Image input: on".
	b := ui.ButtonBase(c).Key(key).Size(22, 22).Radius(6).Justify(ui.Center).AlignItems(ui.Center).
		Role(ui.RoleToggleButton).Checked(on).Label(state)
	if fn == nil {
		// Its click goes nowhere, and not to the row under it either.
		b.Cursor(ui.CursorDefault).Description(fixed)
	} else {
		b.Cursor(ui.CursorPointer).OnClick(fn)
		if b.Hovered() || b.FocusVisible() {
			b.Background(t.rowHover)
			if on {
				ink = t.text
			}
		}
	}
	b.Tooltip(tip).TextColor(ink).Children(func() {
		e := ico(c, iconName, 2, 13)
		if !on {
			// A stroke across the icon, as the system's "off" glyphs.
			e.DrawOver(func(pt *ui.Painter, r ui.Rect) {
				pt.StrokePolyline([][2]float32{{r.X + 1.5, r.Y + 1.5}, {r.X + r.W - 1.5, r.Y + r.H - 1.5}}, 1.6, ink)
			})
		}
	})
}
