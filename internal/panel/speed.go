package panel

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
)

// Speed card.
func (p *Panel) speedCard(c *ui.Context, f *frame, s *mainState) {
	t, m, v := f.pal, f.m, p.view
	sp := v.Speed
	// The bar of the request in progress needs counters that grow while it
	// runs (Caps.LiveSpeed); without them the chart waits for its end.
	generating := v.Phase == core.PhaseGenerating && v.Caps.LiveSpeed
	// A foreign server may have answered before Lumilo saw it: its counters
	// say so, and "No requests yet" would be false.
	answered := v.Phase == core.PhaseExternal && v.Snapshot.Completed > 0
	if len(sp.History) == 0 && !generating && !answered {
		// No request has finished: there is nothing to average or draw. The
		// card is a title and one muted line until the first answer; the
		// first generation brings the full card with its live bar.
		p.hoverBar, p.barSel = -1, -1
		card(c, f, 4).Children(func() {
			txt(c, m.SpeedTitle(), 12.5).FontWeight(600)
			txt(c, m.SpeedEmpty(), 11.5).TextColor(t.muted).LineHeight(1.4)
			if v.Phase == core.PhaseReleased || (v.Phase == core.PhaseExternal && v.Snapshot.Released) {
				txt(c, m.LoadsOnFirst(), 11.5).TextColor(t.muted)
			}
		})
		return
	}
	series := append([]float64(nil), sp.History...)
	current := -1
	if generating {
		series = append(series, sp.Current)
		current = len(series) - 1
	}
	if len(series) > 20 {
		current -= len(series) - 20
		series = series[len(series)-20:]
	}
	dim := v.Phase == core.PhaseReleased || (v.Phase == core.PhaseExternal && v.Snapshot.Released)
	n := len(series)
	if p.barSel >= n {
		p.barSel = -1
	}
	if p.hoverBar >= n {
		p.hoverBar = -1
	}
	isCur := func(i int) bool { return i == current }
	tps := func(x float64) string { return format.TokPerSec(f.loc, x) }
	el := card(c, f, 8).Key("speed-card")
	if current >= 0 {
		// The live bar and its number follow the speed with a spring, so
		// they glide where the probes step (once a second); at once with
		// Reduce Motion.
		series[current] = float64(el.Spring("live", float32(series[current])))
	}
	barText := func(i int) string {
		if isCur(i) {
			return m.HoverCur() + " " + tps(series[i])
		}
		return m.HoverReq() + " " + tps(series[i])
	}

	el.Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			txt(c, m.SpeedTitle(), 12.5).FontWeight(600).Grow(1)
			if generating && v.Snapshot.Queued > 0 {
				txt(c, m.Queue(v.Snapshot.Queued), 11).FontWeight(600).TextColor(t.warnText).Background(t.warnBg).
					Radius(999).Padding(3, 8).NoWrap()
			}
		})
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			avg := "—"
			if sp.Average > 0 {
				avg = tps(sp.Average)
			}
			txt(c, avg, 17).FontWeight(650).NoWrap()
			txt(c, m.AvgNote(), 11.5).TextColor(t.muted).Grow(1)
			if h := p.hoverBar; h >= 0 && h < n {
				col := t.text
				if isCur(h) {
					col = t.okText
				}
				txt(c, barText(h), 11.5).FontWeight(600).TextColor(col).NoWrap()
			}
		})
		p.chart(c, f, series, current, dim, barText)
		var foot1, foot2 string
		if dim {
			if !sp.LastRequest.IsZero() {
				foot1 = m.LastReq(format.Duration(f.loc, f.now.Sub(sp.LastRequest)))
			}
			foot2 = m.LoadsOnFirst()
		} else {
			if sp.FirstToken > 0 {
				foot1 = m.FirstToken(format.Seconds(sp.FirstToken))
			}
			if sp.Read > 0 {
				foot2 = m.PromptSpeed(tps(sp.Read))
			}
		}
		if foot1 != "" || foot2 != "" {
			ui.Row(c).Wrap().AlignItems(ui.Baseline).GapX(6).GapY(2).TextColor(t.muted).Children(func() {
				txt(c, foot1, 11.5).Grow(1).NoWrap()
				if foot2 != "" {
					txt(c, foot2, 11.5).NoWrap()
				}
			})
		}
	})
}

// chartTop is the speed the full chart height stands for: the mockup's 50
// tok/s, raised in steps of 10 for faster engines.
func chartTop(series []float64) float64 {
	top := 0.0
	for _, x := range series {
		top = max(top, x)
	}
	if top <= 50 {
		return 50
	}
	return math.Ceil(top*1.2/10) * 10
}

// chart is the 20-bar speed chart. It reads left to right in every
// language, and so do its keys.
func (p *Panel) chart(c *ui.Context, f *frame, series []float64, current int, dim bool, barText func(int) string) {
	t, m := f.pal, f.m
	n := len(series)
	top := chartTop(series)
	hover := -1
	group := ui.Row(c).Direction(ui.LeftToRight).Height(40).Gap(3).AlignItems(ui.End).Role(ui.RoleGroup)
	if n > 0 {
		lo, hi := series[0], series[0]
		for _, x := range series {
			lo, hi = min(lo, x), max(hi, x)
		}
		group.Label(m.ChartLabel(n, format.Number(f.loc, lo, 1), format.TokPerSec(f.loc, hi)))
	}
	// The live bar grows from the time the request began (liveFrom), in
	// the Draw of its cell: the frames that grow it only paint. The Draw
	// reads the window's clock, the panel its own: built is the window's
	// time as this frame is built, when the bar had grown for grown.
	built, grown := c.Now(), f.now.Sub(p.liveAt)
	sel := p.barSel
	if sel < 0 {
		sel = n - 1
	}
	group.Children(func() {
		for i := range 20 - n {
			ui.Box(c).Key(-1 - i).Grow(1).Basis(0).Height(40)
		}
		for i, x := range series {
			h := float32(math.Round(x/top*100)) / 100 * 40
			bg := t.barLo
			if !dim && i >= n-10 {
				bg = t.barHi
			}
			if i == current {
				bg = t.ok
			}
			cell := ui.Column(c).Key(i).Grow(1).Basis(0).Height(40).Justify(ui.End).Bind(&p.bars[i]).Label(barText(i))
			if i == sel {
				cell.Focusable()
				move := func(to int) func() {
					return func() {
						to = max(0, min(n-1, to))
						p.barSel, p.hoverBar, p.barFocus = to, to, true
					}
				}
				cell.OnShortcut(0, ui.KeyLeft, move(i-1)).OnShortcut(0, ui.KeyRight, move(i+1)).
					OnShortcut(0, ui.KeyHome, move(0)).OnShortcut(0, ui.KeyEnd, move(n-1))
			}
			if cell.Hovered() || cell.Focused() {
				hover = i
				if i != current {
					bg = t.strong
				}
			}
			if i == current {
				cell.Draw(liveBar(x/top, bg, t.strong, f.still, built, grown))
				continue
			}
			cell.Children(func() {
				ui.Box(c).Height(max(h, 0)).Radius(2).Background(bg)
			})
		}
	})
	if p.barFocus && !p.measured && sel >= 0 && sel < n {
		p.barFocus = false
		p.bars[sel].Focus(c)
	}
	if hover != p.hoverBar && !p.measured {
		p.hoverBar = hover
		c.AnimationFrame()
	}
}

// The live bar grows from 6 % to its height in liveSteps steps of
// liveStep, then again.
const (
	liveStep  = 100 * time.Millisecond
	liveSteps = 60
)

// liveBar paints the bar of the request in progress at share of the
// chart's height, with a 2 DIP top edge in edge. It has grown for grown
// at the window's time built; it stands at its height while the frame
// has no motion.
func liveBar(share float64, fill, edge ui.Color, still bool, built time.Time, grown time.Duration) func(*ui.Painter, ui.Rect) {
	return func(pt *ui.Painter, r ui.Rect) {
		phase := 1.0
		if !still {
			d := max(0, grown+pt.Now().Sub(built))
			phase = float64(int(d/liveStep)%liveSteps) / liveSteps
			pt.After(liveStep - d%liveStep)
		}
		h := float32(max(6, math.Round(phase*share*100))) / 100 * r.H
		bar := ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}
		pt.Fill(bar, edge, 2)
		pt.Fill(ui.Rect{X: bar.X, Y: bar.Y + 2, W: bar.W, H: max(0, bar.H-2)}, fill, 2)
	}
}
