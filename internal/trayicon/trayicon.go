// Package trayicon draws Lumilo's menu-bar icon: the app's mark, a spark
// in a ring, and the value next to it (memory, speed).
//
// The icon is a template image: black with alpha, so macOS tints it for
// the menu bar's appearance, for a selected item and for Increase
// Contrast, as it does its own icons. States change the mark's shape, not
// its colour.
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
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Sizes in points (menu-bar icons are 18 pt tall).
const (
	height   = 18
	maxWidth = 80
	markSize = 16
	textSize = 13
	faded    = 0.4 // the opacity of a faded mark
)

// scale is the render scale; the README images raise it.
var scale = 2

// Mark is the shape of the app's mark for a state.
type Mark uint8

const (
	MarkSpark    Mark = iota // a spark in a ring
	MarkLoader               // a faint spark, an arc on the ring
	MarkHollow               // the spark as an outline: weights released
	MarkAlert                // "!" in the ring
	MarkDownload             // an arrow in the ring
)

// Icon is everything the icon shows. It is comparable and used as the
// cache key.
type Icon struct {
	Mark Mark
	// Faded draws the mark at 40 %: nothing runs, or it loads.
	Faded bool
	// Engine names the engine whose logo (SetLogo) is the mark; empty, or
	// a logo that cannot be read, draws Lumilo's spark in a ring.
	Engine string
	// Text is the value next to the mark; empty for none.
	Text string
	// Dim fades the mark (Lumilo's: its spark) by this many percent (0 to
	// 100): the breathing frames.
	Dim uint8
}

// The mark on a 24 x 24 grid, after AppIcon.icon: the sphere is the ring
// and the four-point spark keeps its curves and its size inside it. The
// view box crops the grid to the ring, so the ring is as tall as the
// system's own icons (about 15.5 pt of 16).
const (
	ring  = `<circle cx="12" cy="12" r="8.7" stroke-width="1.9"/>`
	spark = "M12 5.3C12.49 10.17 13.83 11.51 18.7 12C13.83 12.49 12.49 13.83 12 18.7" +
		"C11.51 13.83 10.17 12.49 5.3 12C10.17 11.51 11.51 10.17 12 5.3Z"
	arc   = `<path d="M12 3.3a8.7 8.7 0 0 1 8.7 8.7" stroke-width="1.9" stroke-linecap="round"/>`
	alert = `<path d="M12 7v6.5M12 16.6v.1" stroke-width="2.2" stroke-linecap="round"/>`
	arrow = `<path d="M12 7.5v8M8.8 12.6l3.2 3.2l3.2-3.2" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/>`
)

// markSVG is the SVG of the mark of ic, with fills and strokes in
// currentColor.
func markSVG(ic Icon) string {
	sparkOpacity := 1 - float32(ic.Dim)/100
	var b strings.Builder
	switch ic.Mark {
	case MarkSpark:
		b.WriteString(ring)
		fmt.Fprintf(&b, `<path d="%s" fill="currentColor" stroke="none" opacity="%.2f"/>`, spark, sparkOpacity)
	case MarkLoader:
		fmt.Fprintf(&b, `<g opacity="0.3">%s</g>%s`, ring, arc)
		fmt.Fprintf(&b, `<path d="%s" fill="currentColor" stroke="none" opacity="%.2f"/>`, spark, faded)
	case MarkHollow:
		b.WriteString(ring)
		fmt.Fprintf(&b, `<path d="%s" stroke-width="1.4" stroke-linejoin="round"/>`, spark)
	case MarkAlert:
		b.WriteString(ring + alert)
	case MarkDownload:
		b.WriteString(ring + arrow)
	}
	return b.String()
}

// marks keeps parsed marks by their SVG: a breathing cycle and the states
// make a few dozen.
var marks = struct {
	sync.Mutex
	m map[string]*ui.SVG
}{m: map[string]*ui.SVG{}}

func parseMark(body string) *ui.SVG {
	marks.Lock()
	defer marks.Unlock()
	if svg, ok := marks.m[body]; ok {
		return svg
	}
	svg := ui.MustParseSVG(fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="2.2 2.2 19.6 19.6" `+
		`fill="none" stroke="currentColor">%s</svg>`, body))
	marks.m[body] = svg
	return svg
}

// cache keeps encoded icons by content, so the same state is drawn once.
var cache = struct {
	sync.Mutex
	m map[Icon][]byte
}{m: map[Icon][]byte{}}

const cacheMax = 256

// PNG returns the icon as a template PNG at @2x: black with alpha, 36
// pixels tall, as wide as its content. Identical icons are drawn once.
func PNG(ic Icon) []byte {
	cache.Lock()
	b, ok := cache.m[ic]
	cache.Unlock()
	if ok {
		return b
	}
	b = encode(draw(ic))

	cache.Lock()
	if len(cache.m) >= cacheMax {
		clear(cache.m)
	}
	cache.m[ic] = b
	cache.Unlock()
	return b
}

// Shot returns the icon as macOS shows the template in a light or a dark
// menu bar: in the label colour, black or white at 85 %. For the README
// and the demo, which have no menu bar to tint it.
func Shot(ic Icon, dark bool) []byte {
	img := draw(ic)
	var v uint8
	if dark {
		v = 255
	}
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
		img.Pix[i+3] = uint8(int(img.Pix[i+3]) * 85 / 100)
	}
	return encode(img)
}

func draw(ic Icon) *image.NRGBA {
	img := unmatte(render(ic, ui.RGB(0, 0, 0)), render(ic, ui.RGB(255, 255, 255)))
	return img.SubImage(image.Rect(0, 0, contentWidth(img), img.Bounds().Dy())).(*image.NRGBA)
}

func encode(img *image.NRGBA) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func render(ic Icon, bg ui.Color) *image.RGBA {
	ink := ui.RGB(0, 0, 0)
	mark := parseMark(markSVG(ic))
	logo := logoFor(ic.Engine)
	view := func(c *ui.Context) {
		// The icon stays left to right in every language.
		ui.Row(c).Direction(ui.LeftToRight).Fill().Background(bg).
			AlignItems(ui.Center).Gap(4).PaddingX(1).Children(func() {
			if logo != nil {
				opacity := 1 - float32(ic.Dim)/100
				if ic.Faded {
					opacity = faded
				}
				bm, w, h := logo.image(ic.Mark, opacity)
				ui.Image(c, bm).Size(w, h)
			} else {
				m := ui.Icon(c, mark).Size(markSize, markSize).TextColor(ink)
				// The loader has faint parts of its own.
				if ic.Faded && ic.Mark != MarkLoader {
					m.Opacity(faded)
				}
			}
			if ic.Text != "" {
				// Regular and on the baseline of the menu-bar clock, 1 pt
				// above the icon's centre; tabular figures keep the width
				// while the speed eases.
				ui.Text(c, ic.Text).FontSize(textSize).FontFeatures("tnum").
					TextColor(ink).NoWrap().Margin(0, 0, 2, 0)
			}
		})
	}
	t := ui.NewTester(view, maxWidth, height)
	t.SetScale(float32(scale))
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
