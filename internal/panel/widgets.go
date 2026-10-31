package panel

import (
	"github.com/egoist/mygo/plugins/glass"

	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

const mono = "monospace"

// txt is a text in the given size, in the mockup's default line height.
func txt(c *ui.Context, s string, size float32) ui.Element {
	return ui.Text(c, s).FontSize(size).FixedLineHeight(lineHeight(c.Locale(), size, s, false))
}

// monoTxt is a monospaced text, as the mockup's ui-monospace.
func monoTxt(c *ui.Context, s string, size float32) ui.Element {
	return ui.Text(c, s).FontSize(size).Font(mono).FixedLineHeight(lineHeight(c.Locale(), size, s, true)).
		LetterSpacing(monoTracking * size)
}

// monoTracking evens out the system monospaced font's width with the
// mockup's: Core Text sets it 1.6 % of the size wider per character than
// the browser does, which wraps the error lines earlier.
const monoTracking = -0.016

// Line heights of the browser's "normal" at the sizes the panel uses, in
// whole pixels as the mockup lays its lines out: the system font, its
// monospaced kind, and text with Arabic, Chinese or Japanese letters, whose
// fallback fonts have taller lines. Text otherwise takes 1.18 times the
// size, never less than its font's ascent and descent (MyGo
// internal/text/layout.go: line.Height), so at 11 and 11.5 it stays a
// fraction of a pixel taller than in the mockup.
var (
	lineSizes  = [...]float32{10, 10.5, 11, 11.5, 12, 12.5, 13, 14, 15, 17}
	lineLatin  = [...]float32{12, 12, 13, 13, 15, 15, 16, 17, 18, 20}
	lineMono   = [...]float32{11, 12, 13, 14, 14, 15, 15, 16, 18, 20}
	lineArabic = [...]float32{13, 15, 15, 16, 16, 17, 17, 18, 20, 22}
	lineHan    = [...]float32{14, 15, 16, 16, 17, 17, 18, 20, 21, 24}
	lineKana   = [...]float32{15, 15, 17, 17, 18, 19, 20, 21, 23, 26}
)

// lang is the language of the frame being built: the browser picks the
// fonts of Chinese and Japanese text, and so their line height, by the
// page's language.
func lineHeight(lang string, size float32, s string, monospaced bool) float32 {
	i := 0
	for i < len(lineSizes) && lineSizes[i] < size {
		i++
	}
	if i == len(lineSizes) || lineSizes[i] != size {
		return float32(int(size*1.18 + 0.5))
	}
	if monospaced {
		return lineMono[i]
	}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			if lang == "ar" {
				return lineLatin[i]
			}
			if strings.HasPrefix(lang, "ja") {
				return lineKana[i]
			}
			if strings.HasPrefix(lang, "zh") {
				return lineHan[i]
			}
			return lineLatin[i]
		case unicode.Is(unicode.Arabic, r):
			return lineArabic[i]
		}
	}
	return lineLatin[i]
}

// ico is a 24×24 icon drawn size×size with the given stroke width.
func ico(c *ui.Context, name string, stroke, size float32) ui.Element {
	return ui.Icon(c, icon(name, stroke)).Size(size, size)
}

// choiceLead is the leading column of a row of a single-choice list, as
// the check column of a macOS menu: a check in the text colour on the chosen
// row, an empty column of the same width on the others, so the names align.
// The column leads in either direction of the text, so the check is at the
// right in right-to-left; the glyph is the same both ways.
func choiceLead(c *ui.Context, f *frame, on bool) {
	col := ui.Box(c).Size(choiceLeadWidth, 16).Shrink(0).Center()
	if on {
		col.Children(func() { ico(c, "check", 2, 13).TextColor(f.pal.text) })
	}
}

// choiceLeadWidth is the width of choiceLead.
const choiceLeadWidth = 16

// mirror flips an icon that points along the line in right-to-left text.
func mirror(e ui.Element, rtl bool) ui.Element {
	if rtl {
		return e.Scale(-1, 1)
	}
	return e
}

// btn is a button without a look, as the mockup's <button>: a centered
// row that takes the focus.
func btn(c *ui.Context) ui.Element {
	return ui.ButtonBase(c).Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer)
}

// iconBtn is a round 28×28 header button, as the toolbar buttons of
// macOS 26: no frame, a tint under the pointer.
func iconBtn(c *ui.Context, f *frame, label, name string, flip bool, fn func()) ui.Element {
	t := f.pal
	b := btn(c).Size(28, 28).Radius(14).TextColor(t.soft).Label(label).Tooltip(label)
	if b.Hovered() || b.FocusVisible() {
		b.Background(t.rowHover)
	}
	if fn != nil {
		b.OnClick(fn)
	}
	b.Children(func() {
		e := ico(c, name, 2, 15)
		if flip {
			mirror(e, f.rtl)
		}
	})
	return b
}

// growAuto makes e grow from its content's width, as CSS's flex: 1 1
// auto: Grow alone starts it from none, which shares the space equally.
// name is the text whose width the last frame measured, last that width,
// extra the rest of e's content, gaps and padding.
func growAuto(c *ui.Context, e ui.Element, name *ui.Handle, last *float32, extra float32) {
	e.Grow(1)
	if w := name.Bounds(c).W; w > 0 {
		if w != *last {
			*last = w
			c.AnimationFrame()
		}
		e.Basis(w + extra)
	}
}

// card is a section of the panel: card background, hairline border.
func card(c *ui.Context, f *frame, gap float32) ui.Element {
	// Liquid Glass, as Kvoto's cards: the panel shows through, frosted.
	return ui.Column(c).Material(glass.Glass{}).Radius(14).Padding(10, 12).Gap(gap)
}

// popSurface is the look of every popup and dialog over the panel: the
// model and engine lists, the selects, the confirmations.
func popSurface(e ui.Element, f *frame) ui.Element {
	t := f.pal
	return e.Radius(12).Background(t.pop).Border(1, t.popBorder).Shadow(0, 16, 40, 0, t.popShadow).
		TextColor(t.text).FontFeatures("tnum")
}

// segments draws a 30-segment bar with gap 2: fill says the colour of
// segment i. Draw does not mirror, but the mockup's grid does, so the bar
// fills from the start of the line: the right in RTL.
func segments(c *ui.Context, f *frame, h, radius float32, fill func(i int) ui.Color) ui.Element {
	rtl := f.rtl
	return ui.Box(c).Height(h).Draw(func(pt *ui.Painter, r ui.Rect) { paintSegments(pt, r, rtl, radius, fill) })
}

// paintSegments paints the 30 segments of a bar in r.
func paintSegments(pt *ui.Painter, r ui.Rect, rtl bool, radius float32, fill func(i int) ui.Color) {
	const n, gap = 30, 2
	w := (r.W - gap*(n-1)) / n
	for i := range n {
		x := r.X + float32(i)*(w+gap)
		if rtl {
			x = r.X + r.W - float32(i+1)*w - float32(i)*gap
		}
		pt.Fill(ui.Rect{X: x, Y: r.Y, W: w, H: r.H}, fill(i), radius)
	}
}

// meter makes a bar read as a meter by assistive technology: its value
// of the whole, and the text that says it.
func meter(e ui.Element, label, value string, used, limit int64) ui.Element {
	e.Role(ui.RoleMeter).Label(label).Value(value)
	if limit > 0 {
		e.Range(0, float64(limit), float64(min(max(used, 0), limit)))
	}
	return e
}

// spinner is a loader icon that turns once a second, drawn in ink. It
// turns in frames that only paint (its Draw reads the clock), so a
// loading screen does not build the view at the display's rate. It
// stands still while the frame has no motion.
func spinner(c *ui.Context, f *frame, name string, stroke, size float32, ink ui.Color) ui.Element {
	svg, still := icon(name, stroke), f.still
	return ui.Box(c).Size(size, size).Shrink(0).Draw(func(pt *ui.Painter, r ui.Rect) {
		if still {
			pt.Icon(svg, r, ink)
			return
		}
		turn := float32(pt.Now().UnixMilli()%1000) * 360 / 1000
		pt.Transform(ui.Rotation(turn), r.X+r.W/2, r.Y+r.H/2, func() { pt.Icon(svg, r, ink) })
		pt.AnimationFrame()
	})
}

// filled is the mockup's fill rule: segment i of n is lit when its middle
// is within pct, and the first one for any use at all.
func filled(i, n int, pct float64) bool {
	return (float64(i)+0.5)/float64(n)*100 <= pct || (i == 0 && pct > 0)
}
