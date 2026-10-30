package panel

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// over is c over an opaque background.
func over(c, bg ui.Color) ui.Color {
	a := float64(c.A) / 255
	mix := func(f, b uint8) uint8 { return uint8(math.Round(float64(f)*a + float64(b)*(1-a))) }
	return ui.Color{R: mix(c.R, bg.R), G: mix(c.G, bg.G), B: mix(c.B, bg.B), A: 255}
}

// contrast is the WCAG contrast ratio of two opaque colours.
func contrast(a, b ui.Color) float64 {
	lum := func(c ui.Color) float64 {
		ch := func(v uint8) float64 {
			s := float64(v) / 255
			if s <= 0.04045 {
				return s / 12.92
			}
			return math.Pow((s+0.055)/1.055, 2.4)
		}
		return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
	}
	la, lb := lum(a), lum(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// The speed chart's bars, the older ones too, stand out from the card by
// 3:1 (WCAG 1.4.11, graphical objects) in the light theme.
func TestChartBarsContrast(t *testing.T) {
	t.Parallel()
	p := paletteFor(false, false)
	for name, bar := range map[string]ui.Color{"older": p.barLo, "recent": p.barHi} {
		if r := contrast(over(bar, p.card), p.card); r < 3 {
			t.Errorf("%s bars: %.2f:1 against the card", name, r)
		}
	}
	if contrast(over(p.barHi, p.card), over(p.barLo, p.card)) < 1.3 {
		t.Error("the recent bars do not read darker than the older ones")
	}
}

func TestChartRange(t *testing.T) {
	for _, c := range []struct {
		series []float64
		lo, hi float64
	}{
		{nil, 0, 50},
		// Close speeds: the slowest bar at a third of the height.
		{[]float64{30, 36, 45, 41}, 20, 50},
		// One speed: the range still spans half the top.
		{[]float64{40}, 20, 45},
		// Far apart: the chart starts at zero.
		{[]float64{8, 90}, 0, 95},
		// A request without a speed yet.
		{[]float64{0}, 0, 5},
	} {
		if lo, hi := chartRange(c.series); lo != c.lo || hi != c.hi {
			t.Errorf("chartRange(%v) = %v, %v; want %v, %v", c.series, lo, hi, c.lo, c.hi)
		}
	}
}
