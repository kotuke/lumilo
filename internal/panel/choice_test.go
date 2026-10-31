package panel

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/format"
)

// The lists with one choice show it as a macOS menu does: a check in the
// text colour in a leading column of a fixed width, which the other rows
// keep empty. These helpers read the pixels of that column.

// itemPitch is the distance between the items of a select's list: 30 high,
// no gap.
const itemPitch = 30

func (b *bench) palette() palette { return paletteFor(b.dark, false) }

func pixel(img *image.RGBA, x, y int) ui.Color {
	p := img.RGBAAt(x, y)
	return ui.Color{R: p.R, G: p.G, B: p.B, A: 255}
}

func dist(a, c ui.Color) int {
	d := func(x, y uint8) int { return int(math.Abs(float64(x) - float64(y))) }
	return d(a.R, c.R) + d(a.G, c.G) + d(a.B, c.B)
}

// ink looks in the box for the pixel farthest from the colour of the box's
// top-left corner, which is the background there: the ink of what is drawn
// in it, and whether anything is.
func (b *bench) ink(x0, y0, w, h float32) (ink, bg ui.Color, drawn bool) {
	b.t.Helper()
	img := b.tt.Image()
	bg = pixel(img, int(x0), int(y0))
	far, n := 0, 0
	for y := int(y0); y < int(y0+h); y++ {
		for x := int(x0); x < int(x0+w); x++ {
			if d := dist(pixel(img, x, y), bg); d > 90 {
				n++
				if d > far {
					far, ink = d, pixel(img, x, y)
				}
			}
		}
	}
	return ink, bg, n >= 6
}

// leadAt reads the leading column of 16×16 at x0 around the line cy.
func (b *bench) leadAt(x0, cy float32) (ink ui.Color, drawn bool) {
	ink, _, drawn = b.ink(x0, cy-8, choiceLeadWidth, 16)
	return ink, drawn
}

// rowLead is the column of a row of a button's whole width, as an engine
// row or a model row, whose padding is 8: at the row's start.
func (b *bench) rowLead(r ui.Rect) (float32, float32) {
	x := r.X + 8
	if format.RTL(b.lang) {
		x = r.X + r.W - 8 - choiceLeadWidth
	}
	return x, r.Y + r.H/2
}

// textLead is the column of an item of a select, found by the text
// beside it: gap before the text, in the direction of the text.
func (b *bench) textLead(r ui.Rect, gap float32) (float32, float32) {
	x := r.X - gap - choiceLeadWidth
	if format.RTL(b.lang) {
		x = r.X + r.W + gap
	}
	return x, r.Y + r.H/2
}

// wantChoice fails unless the column shows a check, in the text colour
// and not in green, when on, and nothing otherwise.
func (b *bench) wantChoice(what string, x, cy float32, on bool) {
	b.t.Helper()
	ink, drawn := b.leadAt(x, cy)
	if drawn != on {
		b.t.Errorf("%s: check drawn %v, want %v (column at %.0f,%.0f)", what, drawn, on, x, cy)
		return
	}
	if !on {
		return
	}
	if want := b.palette().text; dist(ink, want) > 150 {
		b.t.Errorf("%s: the check is %v, not the text colour %v", what, ink, want)
	}
	if int(ink.G) > int(ink.R)+30 && int(ink.G) > int(ink.B)+30 {
		b.t.Errorf("%s: the check is green: %v", what, ink)
	}
	// The column is the check's side of the line: the check must lie
	// within the box of the column, which the scan already limits, and
	// the box must be at the start of the row.
}

// wantRows checks the leading columns of rows found by their button: the
// ones in on are checked, those in off are not.
func (b *bench) wantRows(on string, off ...string) {
	b.t.Helper()
	find := func(s string) ui.Rect {
		r, ok := b.tt.Find(s)
		if !ok {
			b.t.Fatalf("no row %q in %q", s, b.tt.Texts())
		}
		return r
	}
	x, cy := b.rowLead(find(on))
	b.wantChoice(on, x, cy, true)
	for _, s := range off {
		x, cy := b.rowLead(find(s))
		b.wantChoice(s, x, cy, false)
	}
}

// wantBadge checks the logos of engine rows found by their button: the
// one of on carries the check badge in the accent, those of off do not.
func (b *bench) wantBadge(on string, off ...string) {
	b.t.Helper()
	accent := b.palette().trackOn
	badge := func(s string) bool {
		r, ok := b.tt.Find(s)
		if !ok {
			b.t.Fatalf("no row %q in %q", s, b.tt.Texts())
		}
		// The badge sits over the logo's bottom end corner: 8 + 26 from
		// the row's start.
		x := r.X + 8 + 26 - 11
		if format.RTL(b.lang) {
			x = r.X + r.W - 8 - 26 - 3
		}
		img := b.tt.Image()
		n := 0
		for y := int(r.Y + r.H/2); y < int(r.Y+r.H); y++ {
			for xx := int(x); xx < int(x+14); xx++ {
				if dist(pixel(img, xx, y), accent) < 40 {
					n++
				}
			}
		}
		return n >= 12
	}
	if !badge(on) {
		b.t.Errorf("%s: no check badge on the logo", on)
	}
	for _, s := range off {
		if badge(s) {
			b.t.Errorf("%s: a check badge on a row not chosen", s)
		}
	}
}

// secondModel is another model of the list, in the other row.
var secondModel = engine.Model{ID: "mlx-community/Qwen3-4B-4bit", Name: "Qwen3-4B", Owner: "mlx-community",
	Variant: "4bit", SizeBytes: 2_300_000_000}

// twoModels lists v's model and secondModel and picks the one at sel.
func twoModels(v core.View, sel int) core.View {
	first := v.Model
	v.Models = []engine.Model{first, secondModel}
	if sel == 1 {
		v.Model = secondModel
		v.Snapshot.Model = secondModel.ID
	}
	return v
}

// popModels opens the model list of the view shown.
func (b *bench) popModels() {
	b.t.Helper()
	b.click(b.p.view.Model.Name)
	b.tt.Frame()
	if !b.p.modelsOpen {
		b.t.Fatalf("the model list did not open: %q", b.tt.Texts())
	}
}

// modelLeads checks the columns of the two rows of the model list, found
// by the second line of each: the variant and the size, which the header
// does not show. Their gap to the column is the row's 10.
func (b *bench) modelLeads(sel int) {
	b.t.Helper()
	subs := []string{"Mix-15.7G · 15.7 GB", "4bit · 2.3 GB"}
	for i, s := range subs {
		s = localSub(b.lang, i)
		r, ok := b.tt.Find(s)
		if !ok {
			b.t.Fatalf("no row line %q in %q", s, b.tt.Texts())
		}
		x, cy := b.textLead(r, 10)
		b.wantChoice(s, x, cy, i == sel)
	}
}

// localSub is the second line of the model row i in lang.
func localSub(lang string, i int) string {
	models := []engine.Model{sampleModel, secondModel}
	md := models[i]
	return md.Variant + " · " + format.GB(lang, md.SizeBytes)
}

// splashTwo is Splash with two models, the first with the eye and the
// draft it always runs.
func splashTwo(sel int) core.View {
	v := orcaFeatures(withPhase(core.PhaseStopped))
	v.Model.Variant = sampleModel.Variant
	v.Models[0] = v.Model
	return twoModels(v, sel)
}

// llamaTwo is llama.cpp with two models, the first with the eye and the
// MTP and DFlash drafts.
func llamaTwo(sel int) core.View {
	v := llamaFeatures()
	v.Phase = core.PhaseStopped
	v.Model.Variant = sampleModel.Variant
	v.Models[0] = v.Model
	v.Models = v.Models[:1]
	return twoModels(v, sel)
}

// toggleInk is the ink of the eye or the bolt of the model row, and the
// background under it.
func (b *bench) toggleInk(label string) (ink, bg ui.Color) {
	b.t.Helper()
	r, ok := b.tt.Find(label)
	if !ok {
		b.t.Fatalf("no toggle %q in %q", label, b.tt.Texts())
	}
	ink, bg, drawn := b.ink(r.X, r.Y, r.W, r.H)
	if !drawn {
		b.t.Fatalf("toggle %q draws nothing", label)
	}
	return ink, bg
}

// wantOn fails unless the toggle reads as on: 3:1 against its background
// (WCAG 1.4.11). It returns the ratio.
func (b *bench) wantOn(label string) float64 {
	b.t.Helper()
	ink, bg := b.toggleInk(label)
	r := contrast(ink, bg)
	if r < 3 {
		b.t.Errorf("%q on: contrast %.2f, want 3 or more (ink %v on %v)", label, r, ink, bg)
	}
	return r
}

// wantOff fails unless the toggle is clearly dimmer than on, which has the
// contrast ratio on, and still shows.
func (b *bench) wantOff(label string, on float64) {
	b.t.Helper()
	ink, bg := b.toggleInk(label)
	r := contrast(ink, bg)
	if r > on*0.5 || r < 1.2 {
		b.t.Errorf("%q off: contrast %.2f beside %.2f of on (ink %v on %v)", label, r, on, ink, bg)
	}
}

func choiceBoards() []board {
	var out []board
	for _, lang := range []string{"en", "ru", "ar"} {
		for sel, name := range []string{"first", "second"} {
			out = append(out, board{name: "model-popup-selected-" + name + "-" + lang, lang: lang,
				build: func(b *bench) {
					b.show(splashTwo(sel), 0)
					b.popModels()
				},
				check: func(b *bench) { b.modelLeads(sel) }})
		}
		// The header's engine list and the engine card of Settings.
		out = append(out, board{name: "engines-header-checked-" + lang, lang: lang,
			build: func(b *bench) {
				b.show(withPhase(core.PhaseReady), time.Hour)
				b.openEngines(headerTitle)
			},
			check: func(b *bench) { b.wantBadge("Splash", "llama.cpp") }})
		out = append(out, board{name: "engines-settings-checked-" + lang, lang: lang,
			build: func(b *bench) {
				b.show(withPhase(core.PhaseReady), time.Hour)
				b.click(b.m.Settings())
			},
			check: func(b *bench) { b.wantBadge("Splash", "llama.cpp") }})
		// Language: the first item, "System default", is chosen.
		out = append(out, board{name: "select-language-checked-" + lang, lang: lang,
			build: func(b *bench) {
				b.show(withPhase(core.PhaseReady), time.Hour)
				b.click(b.m.Settings())
				b.clickEnd(b.m.Language())
				b.tt.Frame()
			},
			check: func(b *bench) { b.selectLeads("English", 1, 0) }})
		// Idle unload: 10 minutes, the second of four.
		out = append(out, board{name: "select-idle-checked-" + lang, lang: lang,
			build: func(b *bench) {
				b.show(withPhase(core.PhaseReady), time.Hour)
				b.click(b.m.Settings())
				b.clickEnd(b.m.IdleUnload())
				b.tt.Frame()
			},
			check: func(b *bench) {
				b.selectLeads(b.m.SettingsIdleAfter(format.Duration(lang, 5*time.Minute)), 0, 1)
			}})
	}
	out = append(out,
		// Context size: Auto, the first of the list, is chosen.
		board{name: "select-context-checked", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click(b.m.Settings())
			b.clickEnd(b.m.Context())
			b.tt.Frame()
		}, check: func(b *bench) { b.selectLeads(ctxName("en", 32<<10), 1, 0) }},
		board{name: "select-terminal-checked", build: func(b *bench) {
			b.p.SetTerminals([]TerminalChoice{
				{"com.apple.Terminal", "Terminal"}, {"com.mitchellh.ghostty", "Ghostty"}, {"com.superlogical.rex", "Rex"}})
			pr := defaultPrefs()
			pr.Terminal = "com.mitchellh.ghostty"
			b.p.SetPrefs(pr)
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click(b.m.Settings())
			b.clickEnd(b.m.Terminal())
			b.tt.Frame()
		}, check: func(b *bench) { b.selectLeads("Rex", 2, 1) }},
		// Speculative decoding of llama.cpp: Off is chosen.
		board{name: "select-draft-checked", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(llamaFeatures(), 0)
			b.click(b.m.Settings())
			b.clickEnd(b.m.Draft())
			b.tt.Frame()
		}, check: func(b *bench) { b.selectLeads("MTP", 1, 0) }},
		board{name: "select-draft-mtp-checked", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := llamaFeatures()
			pr := defaultPrefs()
			pr.Options = map[string]engine.ModelOptions{v.Model.ID: {Draft: "mtp"}}
			b.p.SetPrefs(pr)
			b.show(v, 0)
			b.click(b.m.Settings())
			b.clickEnd(b.m.Draft())
			b.tt.Frame()
		}, check: func(b *bench) { b.selectLeads("OrcaCyber-Nano-1.0-AI-Defense-DFlash2-Q8_0", 2, 1) }},

		// The eye and the bolt: on in the row's ink, off clearly dimmer.
		// Splash's draft always runs: it reads as on, though it cannot be
		// turned off.
		board{name: "model-popup-splash-draft", build: func(b *bench) {
			b.show(splashTwo(0), 0)
			b.popModels()
		}, check: func(b *bench) {
			b.modelLeads(0)
			eye := b.wantOn("Image input: on")
			bolt := b.wantOn("Speculative decoding: on")
			if math.Abs(eye-bolt) > eye*0.15 {
				b.t.Errorf("the fixed draft is %.2f, the eye %.2f", bolt, eye)
			}
			// No hover and no pointer: the pointer rests on it.
			r, _ := b.tt.Find("Speculative decoding: on")
			beside := pixel(b.tt.Image(), int(r.X+r.W+3), int(r.Y+r.H/2))
			b.tt.Move(r.X+r.W/2, r.Y+r.H/2)
			b.tt.Frame()
			if got := pixel(b.tt.Image(), int(r.X+r.W+3), int(r.Y+r.H/2)); got != beside {
				b.t.Errorf("row bg beside the bolt changed on hover: %v to %v", beside, got)
			}
			if got := pixel(b.tt.Image(), int(r.X+2), int(r.Y+2)); got != beside {
				b.t.Errorf("the fixed draft has a hover face: %v, row %v", got, beside)
			}
			if c := b.tt.Cursor(); c != ui.CursorDefault {
				b.t.Errorf("cursor %v over the fixed draft", c)
			}
			b.tt.Move(1, 1)
			b.tt.Frame()
		}},
		board{name: "model-popup-llamacpp-draft-off", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			b.show(llamaTwo(0), 0)
			b.popModels()
		}, check: func(b *bench) {
			b.modelLeads(0)
			eye := b.wantOn("Image input: on")
			b.wantOff("Speculative decoding: off", eye)
		}},
		board{name: "model-popup-llamacpp-draft-on", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := llamaTwo(1)
			pr := defaultPrefs()
			pr.Options = map[string]engine.ModelOptions{v.Models[0].ID: {Draft: "mtp"}}
			b.p.SetPrefs(pr)
			b.show(v, 0)
			b.popModels()
		}, check: func(b *bench) {
			b.modelLeads(1)
			b.wantOn("Speculative decoding: on")
			b.wantOn("Image input: on")
		}},
		board{name: "model-popup-vision-off", build: func(b *bench) {
			b.rec.eng = llamacpp.New()
			v := llamaTwo(0)
			pr := defaultPrefs()
			pr.Options = map[string]engine.ModelOptions{v.Models[0].ID: {NoVision: true, Draft: "mtp"}}
			b.p.SetPrefs(pr)
			b.show(v, 0)
			b.popModels()
		}, check: func(b *bench) {
			draft := b.wantOn("Speculative decoding: on")
			b.wantOff("Image input: off", draft)
		}},
	)
	return out
}

// selectLeads checks the columns of an open select's list. The item with
// the text beside is at index at and is not the chosen one; the chosen item
// is at index chosen. The trigger shows the chosen text too, so the chosen
// item is found by the other one: items are itemPitch apart, and the
// column keeps its place at their start.
func (b *bench) selectLeads(beside string, at, chosen int) {
	b.t.Helper()
	r, ok := b.tt.Find(beside)
	if !ok {
		b.t.Fatalf("no item %q in %q", beside, b.tt.Texts())
	}
	x, cy := b.textLead(r, 8)
	b.wantChoice(beside, x, cy, false)
	b.wantChoice("the chosen item", x, cy+float32(chosen-at)*itemPitch, true)
}

// TestChoiceBothThemes runs the boards of the choices in the dark theme
// too (TestBoards runs them in the light one).
func TestChoiceBothThemes(t *testing.T) {
	for _, bd := range choiceBoards() {
		t.Run(bd.name, func(t *testing.T) {
			lang := bd.lang
			if lang == "" {
				lang = "en"
			}
			b := newBench(t, lang)
			b.dark = true
			b.tt.SetDark(true)
			bd.build(b)
			bd.check(b)
		})
	}
}
