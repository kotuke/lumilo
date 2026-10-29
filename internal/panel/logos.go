package panel

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/engine"
)

// brands keeps the images that come as bytes in the View (engine, maker
// and agent logos), decoded once by their bytes: MyGo keeps a bitmap on
// the GPU while it is used, and a bitmap made in every frame would be a
// new texture each time. Each Panel has its own, used on its UI thread.
type brands struct {
	bitmaps map[string]*ui.Bitmap
	svgs    map[string]*ui.SVG // by stroke and content
}

// image is data as an image in its own colours, nil when it cannot be read.
func (b *brands) image(data []byte, kind engine.LogoKind) ui.ImageSource {
	if len(data) == 0 {
		return nil
	}
	if kind == engine.LogoPNG {
		if b.bitmaps == nil {
			b.bitmaps = map[string]*ui.Bitmap{}
		}
		bm, ok := b.bitmaps[string(data)]
		if !ok {
			var err error
			if bm, err = ui.DecodeBitmap(data); err != nil {
				bm = nil
			}
			b.bitmaps[string(data)] = bm
		}
		if bm == nil {
			return nil
		}
		return bm
	}
	if s := b.svg(data, 0); s != nil {
		return s
	}
	return nil
}

// svg parses an SVG logo with its first stroke-width="2" set to stroke
// (0 keeps it); nil when it cannot be read.
func (b *brands) svg(data []byte, stroke float32) *ui.SVG {
	if len(data) == 0 {
		return nil
	}
	if b.svgs == nil {
		b.svgs = map[string]*ui.SVG{}
	}
	k := strconv.FormatFloat(float64(stroke), 'f', -1, 32) + "\x00" + string(data)
	if s, ok := b.svgs[k]; ok {
		return s
	}
	s, err := ui.ParseSVG(withStroke(data, stroke))
	if err != nil {
		s = nil
	}
	b.svgs[k] = s
	return s
}

// logo is the engine logo for the theme.
func (b *brands) logo(l engine.Logo, dark bool) ui.ImageSource {
	data := l.Dark
	if !dark && len(l.Light) > 0 {
		data = l.Light
	}
	return b.image(data, l.Kind)
}

// engineTile is the engine's logo as a rounded square of size DIPs: a tile
// logo fills it, a mark sits on the panel's tint, a bare mark takes the
// square without it. marginEnd is the space
// after it.
func engineTile(c *ui.Context, f *frame, l engine.Logo, size, marginEnd float32) {
	src := f.brands.logo(l, c.Theme().Dark)
	radius := size * 7 / 24
	if l.Tile && src != nil {
		ui.Image(c, src).Size(size, size).Fit(ui.Cover).Radius(radius).MarginEnd(marginEnd).Shrink(0)
		return
	}
	if l.Bare && src != nil {
		ui.Image(c, src).Size(size, size).MarginEnd(marginEnd).Shrink(0)
		return
	}
	ui.Box(c).Size(size, size).Radius(radius).Background(f.pal.tint).MarginEnd(marginEnd).Center().Shrink(0).
		Children(func() {
			if src != nil {
				ui.Image(c, src).Size(size*0.6, size*0.6)
			}
		})
}
