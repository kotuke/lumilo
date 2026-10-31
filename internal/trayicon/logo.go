package trayicon

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/engine"
)

// logoSize is the side, in pixels, of the square the logo is read at;
// logoHeight and logoMaxWidth bound it in the icon, in points.
const (
	logoSize     = 256
	logoHeight   = 15.5
	logoMaxWidth = 26
)

// logoMark is an engine's logo as a template mask and its aspect ratio.
type logoMark struct {
	bitmap *ui.Bitmap
	aspect float32 // width / height
}

// size is the mark's size in the icon: as tall as the ring, unless that
// makes it wider than logoMaxWidth.
func (l *logoMark) size() (w, h float32) {
	w, h = logoHeight*l.aspect, logoHeight
	if w > logoMaxWidth {
		w, h = logoMaxWidth, logoMaxWidth/l.aspect
	}
	return w, h
}

// logos keeps the engines' logos as template masks by engine name: black
// with the logo's shape in alpha, cropped to the shape.
var logos = struct {
	sync.Mutex
	m map[string]*logoMark
}{m: map[string]*logoMark{}}

// SetLogo makes the template mask of the logo of the engine name once;
// For then shows it in place of Lumilo's spark (Icon.Engine).
func SetLogo(name string, l engine.Logo) {
	logos.Lock()
	_, ok := logos.m[name]
	logos.Unlock()
	if ok || name == "" {
		return
	}
	mask := logoMask(l)
	logos.Lock()
	logos.m[name] = mask
	logos.Unlock()
}

func logoFor(name string) *logoMark {
	logos.Lock()
	defer logos.Unlock()
	return logos.m[name]
}

// logoMask reads the logo in its own colours and keeps its silhouette:
// the alpha of a mark (llama.cpp, TensorFold) or how far a pixel is from
// the background of a tile (Splash: a light mark on coral). A soft edge
// (TensorFold's glow) is cut to a crisp one, and Logo.Cuts are cut out.
// nil when the logo cannot be read.
func logoMask(l engine.Logo) *logoMark {
	data := l.Light
	if len(data) == 0 {
		data = l.Dark
	}
	var src ui.ImageSource
	switch l.Kind {
	case engine.LogoPNG:
		bm, err := ui.DecodeBitmap(data)
		if err != nil {
			return nil
		}
		src = bm
	default:
		svg, err := ui.ParseSVG(data)
		if err != nil {
			return nil
		}
		src = svg
	}
	draw := func(src ui.ImageSource, bg ui.Color) *image.RGBA {
		t := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Fill().Background(bg).Children(func() {
				ui.Image(c, src).Size(logoSize, logoSize)
			})
		}, logoSize, logoSize)
		t.Frame()
		return t.Image()
	}
	img := unmatte(draw(src, ui.RGB(0, 0, 0)), draw(src, ui.RGB(255, 255, 255)))

	alpha := image.NewAlpha(img.Rect)
	if l.Tile {
		bg := img.NRGBAAt(2, 2)
		far := 0.0
		dist := func(c color.NRGBA) float64 {
			dr, dg, db := float64(c.R)-float64(bg.R), float64(c.G)-float64(bg.G), float64(c.B)-float64(bg.B)
			return math.Sqrt(dr*dr+dg*dg+db*db) * float64(c.A) / 255
		}
		for i := 0; i < len(img.Pix); i += 4 {
			far = max(far, dist(color.NRGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}))
		}
		if far == 0 {
			return nil
		}
		for i := 0; i < len(img.Pix); i += 4 {
			d := dist(color.NRGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]})
			alpha.Pix[i/4] = uint8(clamp(int(math.Round(d * 255 / far))))
		}
	} else {
		for i := 3; i < len(img.Pix); i += 4 {
			a := int(img.Pix[i])
			if l.Bare {
				// The glow around the shape goes; its edge stays smooth.
				a = clamp((a - 96) * 4)
			}
			alpha.Pix[i/4] = uint8(a)
		}
	}
	if cuts, err := ui.ParseSVG(l.Cuts); len(l.Cuts) > 0 && err == nil {
		cut := unmatte(draw(cuts, ui.RGB(0, 0, 0)), draw(cuts, ui.RGB(255, 255, 255)))
		for i := range alpha.Pix {
			alpha.Pix[i] = uint8(int(alpha.Pix[i]) * (255 - int(cut.Pix[i*4+3])) / 255)
		}
	}

	// Crop to the shape: render draws it as tall as the ring of Lumilo's
	// mark, or as wide as logoMaxWidth.
	b := image.Rectangle{}
	for y := 0; y < logoSize; y++ {
		for x := 0; x < logoSize; x++ {
			if alpha.AlphaAt(x, y).A > 24 {
				b = b.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if b.Empty() {
		return nil
	}
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			out.SetNRGBA(x, y, color.NRGBA{A: alpha.AlphaAt(b.Min.X+x, b.Min.Y+y).A})
		}
	}
	return &logoMark{ui.NewBitmap(out), float32(b.Dx()) / float32(b.Dy())}
}

// The badge in the logo's bottom right corner, in points: a disc with the
// state's glyph cut out of it, and a gap cut out of the logo around it, as
// macOS draws badges on its own template icons.
const (
	badgeRadius = 4.0
	badgeGap    = 1.3
	badgeInset  = 2.5 // from the logo's right edge to the disc's centre
	badgeOut    = badgeRadius - badgeInset
	badgeK      = 12 // pixels per point of a composed mark
)

// badgeGlyph is the glyph cut out of the badge for a state, centred on 0, 0.
var badgeGlyph = map[Mark]string{
	MarkHollow:   "M-1.1 -1.8v3.6M1.1 -1.8v3.6",        // pause: weights released
	MarkAlert:    "M0 -2.1v2.4M0 1.7v.01",              // !
	MarkDownload: "M0 -2.2v3.9M-1.7 0l1.7 1.8l1.7-1.8", // download
}

// composed keeps a logo with a state's badge and opacity, drawn once.
var composed = struct {
	sync.Mutex
	m map[composedKey]*ui.Bitmap
}{m: map[composedKey]*ui.Bitmap{}}

type composedKey struct {
	logo    *logoMark
	mark    Mark
	opacity uint8 // percent
}

// image is the logo for the mark m and its size in points. A mark
// without a badge is the logo itself, which the icon fades with the
// element's opacity: the same pixels at every step of a pulse, so the logo
// does not move when it comes back to full. A mark with a badge is the
// logo at opacity (0 to 1) with the badge, composed once.
func (l *logoMark) image(m Mark, opacity float32) (bm *ui.Bitmap, w, h float32, fade float32) {
	w, h = l.size()
	glyph, badged := badgeGlyph[m]
	if !badged {
		return l.bitmap, w, h, opacity
	}
	cw := w + badgeOut
	k := composedKey{l, m, uint8(math.Round(float64(opacity) * 100))}
	composed.Lock()
	defer composed.Unlock()
	if bm, ok := composed.m[k]; ok {
		return bm, cw, h, 1
	}
	px := func(pt float32) int { return int(math.Ceil(float64(pt * badgeK))) }
	W, H := px(cw), px(h)
	layer := func(view func(c *ui.Context)) *image.NRGBA {
		draw := func(bg ui.Color) *image.RGBA {
			t := ui.NewTester(func(c *ui.Context) {
				ui.Row(c).Fill().Background(bg).Children(func() { view(c) })
			}, W, H)
			t.Frame()
			return t.Image()
		}
		return unmatte(draw(ui.RGB(0, 0, 0)), draw(ui.RGB(255, 255, 255)))
	}
	logo := layer(func(c *ui.Context) {
		ui.Image(c, l.bitmap).Size(w*badgeK, h*badgeK).Opacity(opacity)
	})
	out := image.NewNRGBA(image.Rect(0, 0, W, H))
	copy(out.Pix, logo.Pix)
	cx, cy := w-badgeInset, h-badgeRadius
	svg := func(body string) *ui.SVG {
		return ui.MustParseSVG(fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g">%s</svg>`, cw, h, body))
	}
	disc := fmt.Sprintf(`<circle cx="%g" cy="%g" r="%g" fill="#000"/>`, cx, cy, badgeRadius)
	gap := fmt.Sprintf(`<circle cx="%g" cy="%g" r="%g" fill="#000"/>`, cx, cy, badgeRadius+badgeGap)
	cut := fmt.Sprintf(`<path transform="translate(%g %g)" d="%s" fill="none" stroke="#000" stroke-width="1.1" `+
		`stroke-linecap="round" stroke-linejoin="round"/>`, cx, cy, glyph)
	full := func(body string) *image.NRGBA {
		s := svg(body)
		return layer(func(c *ui.Context) { ui.Icon(c, s).Size(cw*badgeK, h*badgeK) })
	}
	gapA, discA, cutA := full(gap), full(disc), full(cut)
	for i := 3; i < len(out.Pix); i += 4 {
		a := int(out.Pix[i]) * (255 - int(gapA.Pix[i])) / 255
		b := int(discA.Pix[i]) * (255 - int(cutA.Pix[i])) / 255
		out.Pix[i] = uint8(max(a, b))
	}
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2] = 0, 0, 0
	}
	bm = ui.NewBitmap(out)
	composed.m[k] = bm
	return bm, cw, h, 1
}
