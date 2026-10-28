// Package trayicon draws Lumilo's menu-bar icon: a memory bar on the left
// and, on the right, the engine name over a value (memory, speed) or a
// state glyph.
//
// The icon is drawn by the same package ui as the panel, without a window,
// through ui.Tester. The Tester frame is opaque, so the icon is drawn on
// black and on white and the alpha is recovered from the difference.
package trayicon

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Sizes in points (menu-bar icons are 18 pt tall) and the render scale.
const (
	height   = 18
	maxWidth = 80
	scale    = 2
	segments = 8
)

// Tone picks a colour from the palette of the menu-bar theme.
type Tone uint8

const (
	ToneInk   Tone = iota // text, active glyphs, filled segments
	ToneMuted             // stopped, not installed
	ToneWarn              // loading, unresponsive, installing, updating
	ToneErr               // errors
)

// Glyph is the symbol that stands in for the value text.
type Glyph uint8

const (
	GlyphNone Glyph = iota // the value text is shown
	GlyphPower
	GlyphLoader
	GlyphZzz
	GlyphAlert
	GlyphDownload
)

// Icon is everything the icon shows. It is comparable and used as the
// cache key, so it holds no colours: they come from Dark and the tones.
type Icon struct {
	Label string // engine name, upper case
	Text  string // value text; used when Glyph is GlyphNone
	Glyph Glyph
	// GlyphTone colours the glyph. Text is always ToneInk.
	GlyphTone Tone
	// Lit is the number of lit segments from the bottom, 0 to 8.
	Lit int
	// BarTone colours the lit segments.
	BarTone Tone
	// Dim fades the bar by this many percent (0 to 100): the breathing frames.
	Dim uint8
	// Dark is true for a dark menu bar.
	Dark bool
}

// palette holds the colours of one menu-bar theme (tray-spec section 2).
type palette struct {
	ink, label, empty, muted, warn, err ui.Color
}

func paletteFor(dark bool) palette {
	if dark {
		return palette{
			ink: ui.RGB(0xF3, 0xF4, 0xF6), label: ui.RGBA(255, 255, 255, 0.78),
			empty: ui.RGBA(255, 255, 255, 0.22), muted: ui.RGB(0x9C, 0xA3, 0xAF),
			warn: ui.RGB(0xE0, 0xB4, 0x50), err: ui.RGB(0xE0, 0x6C, 0x60),
		}
	}
	return palette{
		ink: ui.RGB(0x1D, 0x1D, 0x1F), label: ui.RGBA(0, 0, 0, 0.62),
		empty: ui.RGBA(0, 0, 0, 0.18), muted: ui.RGB(0x5F, 0x63, 0x6B),
		warn: ui.RGB(0xA8, 0x68, 0x0F), err: ui.RGB(0xC0, 0x44, 0x3A),
	}
}

func (p palette) tone(t Tone) ui.Color {
	switch t {
	case ToneMuted:
		return p.muted
	case ToneWarn:
		return p.warn
	case ToneErr:
		return p.err
	}
	return p.ink
}

// Tabler icons (outline), as in the approved mockup.
var glyphPaths = map[Glyph]string{
	GlyphPower:    "M7 6a7.75 7.75 0 1 0 10 0M12 4l0 8",
	GlyphLoader:   "M12 3a9 9 0 1 0 9 9",
	GlyphZzz:      "M4 12h6l-6 8h6M14 4h6l-6 8h6",
	GlyphAlert:    "M12 9v4M10.363 3.591l-8.106 13.534a1.914 1.914 0 0 0 1.636 2.871h16.214a1.914 1.914 0 0 0 1.636 -2.87l-8.106 -13.536a1.914 1.914 0 0 0 -3.274 0zM12 16h.01",
	GlyphDownload: "M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2 -2v-2M7 11l5 5l5 -5M12 4l0 12",
}

var glyphSVG = func() map[Glyph]*ui.SVG {
	m := map[Glyph]*ui.SVG{}
	for g, d := range glyphPaths {
		m[g] = ui.MustParseSVG(fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="1 1 22 22" `+
			`fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" `+
			`stroke-linejoin="round"><path d="%s"/></svg>`, d))
	}
	return m
}()

// cache keeps encoded icons by content, so the same state is drawn once.
var cache = struct {
	sync.Mutex
	m map[Icon][]byte
}{m: map[Icon][]byte{}}

const cacheMax = 256

// PNG returns the icon as a PNG at @2x: 36 pixels tall, as wide as its
// content. Identical icons are drawn once.
func PNG(ic Icon) []byte {
	cache.Lock()
	b, ok := cache.m[ic]
	cache.Unlock()
	if ok {
		return b
	}
	onBlack := render(ic, ui.RGB(0, 0, 0))
	onWhite := render(ic, ui.RGB(255, 255, 255))
	img := unmatte(onBlack, onWhite)
	img = img.SubImage(image.Rect(0, 0, contentWidth(img), img.Bounds().Dy())).(*image.NRGBA)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	b = buf.Bytes()

	cache.Lock()
	if len(cache.m) >= cacheMax {
		clear(cache.m)
	}
	cache.m[ic] = b
	cache.Unlock()
	return b
}

func render(ic Icon, bg ui.Color) *image.RGBA {
	p := paletteFor(ic.Dark)
	lit := p.tone(ic.BarTone)
	view := func(c *ui.Context) {
		// The icon stays left to right in every language.
		ui.Row(c).Direction(ui.LeftToRight).Fill().Background(bg).
			AlignItems(ui.Center).Gap(3).PaddingX(1).Children(func() {
			ui.Column(c).Reverse().Gap(0.5).Opacity(1 - float32(ic.Dim)/100).Children(func() {
				for i := range segments {
					fill := p.empty
					if i < ic.Lit {
						fill = lit
					}
					ui.Box(c).Size(4.5, 1.5).Radius(0.4).Background(fill)
				}
			})
			// The same box as Kvoto's icon (label 7, gap 1, value 10 = 18 pt),
			// so the label has the same size and baseline in the menu bar.
			ui.Column(c).AlignItems(ui.Start).Gap(1).Children(func() {
				ui.Text(c, ic.Label).FontSize(7).FontWeight(700).LetterSpacing(0.3).
					TextColor(p.label).FixedLineHeight(7).NoWrap()
				if svg := glyphSVG[ic.Glyph]; svg != nil {
					// 9 pt glyph in the 10 pt value line, 0.25 pt above its bottom.
					ui.Icon(c, svg).Size(9, 9).Margin(0.75, 0, 0.25, 0).TextColor(p.tone(ic.GlyphTone))
					return
				}
				ui.Text(c, ic.Text).FontSize(10).Bold().FontFeatures("tnum").
					TextColor(p.ink).FixedLineHeight(10).NoWrap()
			})
		})
	}
	t := ui.NewTester(view, maxWidth, height)
	t.SetScale(scale)
	t.Frame()
	return t.Image()
}

// unmatte recovers colour and alpha: where the frames on black and white
// agree a pixel is opaque, where they differ by 255 it is transparent.
func unmatte(b, w *image.RGBA) *image.NRGBA {
	r := b.Bounds()
	out := image.NewNRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			pb, pw := b.RGBAAt(x, y), w.RGBAAt(x, y)
			d := (int(pw.R) - int(pb.R) + int(pw.G) - int(pb.G) + int(pw.B) - int(pb.B)) / 3
			a := clamp(255 - d)
			if a == 0 {
				continue
			}
			c := func(v uint8) uint8 { return uint8(clamp(int(v) * 255 / a)) }
			out.SetNRGBA(x, y, color.NRGBA{R: c(pb.R), G: c(pb.G), B: c(pb.B), A: uint8(a)})
		}
	}
	return out
}

// contentWidth is the width up to the last opaque column plus one point,
// rounded up to a whole point.
func contentWidth(img *image.NRGBA) int {
	r := img.Bounds()
	w := 0
	for x := r.Min.X; x < r.Max.X; x++ {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if img.NRGBAAt(x, y).A > 0 {
				w = x + 1
				break
			}
		}
	}
	w += scale
	if w%scale != 0 {
		w += scale - w%scale
	}
	return min(w, r.Dx())
}

func clamp(v int) int { return max(0, min(255, v)) }
