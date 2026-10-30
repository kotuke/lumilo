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
		// Between the probes that time it (a long prompt read, a burst of a
		// draft model) the request has no speed: the bar and the number keep
		// the last one, as the menu-bar icon does.
		cur := sp.Current
		if cur > 0 {
			p.liveLast = cur
		} else if v.Snapshot.Active > 0 {
			cur = p.liveLast
		}
		series = append(series, cur)
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
	// While a request runs, the card leads with its live speed, how it
	// stands to the average and the tokens it has generated. The speed and
	// its bar follow the probes with a slow spring (liveSpring), so they
	// glide through the second between two probes rather than step at
	// each; at once with Reduce Motion. The live speed is a whole number,
	// as in the menu bar: its tenths only flicker. The tokens count on at
	// the live speed (tokenCount).
	live, tokens := 0.0, int64(0)
	if current >= 0 {
		series[current] = float64(el.Spring("live", float32(series[current]), liveSpring))
		live = series[current]
		tokens = p.tokenCount(c, f, sp.Tokens, live)
	}
	tpsLive := func(x float64) string { return m.SpeedTps(format.Number(f.loc, x, 0)) }
	barText := func(i int) string {
		if isCur(i) {
			return m.HoverCur() + " " + tpsLive(series[i])
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
			if live > 0 {
				txt(c, tpsLive(live), 17).FontWeight(650).TextColor(t.okText).FontFeatures("tnum").NoWrap()
				txt(c, m.LiveNote(), 11.5).TextColor(t.muted).NoWrap()
				// How the live speed stands to the average: "↑ 12 %".
				if d := live/sp.Average - 1; sp.Average > 0 && math.Abs(d) >= 0.005 {
					arrow, col := "↑", t.okText
					if d < 0 {
						arrow, col = "↓", t.warnText
					}
					txt(c, arrow+" "+format.Percent(f.loc, math.Abs(d)), 11.5).FontWeight(600).TextColor(col).FontFeatures("tnum").NoWrap()
				}
				ui.Box(c).Grow(1)
			} else {
				avg := "—"
				if sp.Average > 0 {
					avg = tps(sp.Average)
				}
				txt(c, avg, 17).FontWeight(650).NoWrap()
				txt(c, m.AvgNote(), 11.5).TextColor(t.muted).Grow(1)
			}
			if h := p.hoverBar; h >= 0 && h < n {
				col := t.text
				if isCur(h) {
					col = t.okText
				}
				txt(c, barText(h), 11.5).FontWeight(600).TextColor(col).NoWrap()
			} else if tokens > 0 {
				txt(c, m.LiveTokens(int(tokens)), 11.5).TextColor(t.muted).FontFeatures("tnum").NoWrap()
			}
		})
		p.chart(c, f, series, current, dim, sp.Average, barText)
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

// liveSpring is the spring of the live speed: about as slow as
// the menu-bar number's easing (trayicon.speedEase), which covers most of
// a change in the second until the next probe.
var liveSpring = ui.SpringOptions{Duration: 2 * time.Second}

// tokenCatchUp is about how long the shown token count takes to reach
// the count it runs toward.
const tokenCatchUp = 500 * time.Millisecond

// tokenCount is the token count to show for n, the count of the last
// probe, at the live speed rate. It counts on from n at rate since the
// probe (tokAt) and catches up with that estimate in about tokenCatchUp,
// never backwards: a counter that runs, not one that steps once a second
// as the probes do. With Reduce Motion it is n.
func (p *Panel) tokenCount(c *ui.Context, f *frame, n int64, rate float64) int64 {
	switch {
	case n <= 0:
		p.tokShown = 0
		return 0
	case p.measured: // a layout pass: no time goes by
		return int64(max(p.tokShown, float64(n)))
	case f.still || rate <= 0 || p.tokShown <= 0:
		p.tokShown, p.tokStep = float64(n), f.now
		if !f.still && rate > 0 {
			c.AnimationFrame()
		}
		return n
	}
	// Past a second and a half without a probe the estimate stops: the
	// server may have stalled.
	est := float64(n) + rate*min(f.now.Sub(p.tokAt).Seconds(), 1.5)
	dt := f.now.Sub(p.tokStep).Seconds()
	p.tokStep = f.now
	if dt > 0 && dt < 0.5 {
		p.tokShown = max(p.tokShown, p.tokShown+dt*(rate+(est-p.tokShown)/tokenCatchUp.Seconds()))
	}
	c.AnimationFrame()
	return int64(p.tokShown)
}

// chartRange is the speeds the chart's bottom and top stand for. The
// chart starts below the slowest bar, not at zero, so that requests of
// close speeds differ at a glance: of bars from 30 to 45 tok/s, the
// slowest stands at a third of the height. The range spans at least half
// the top speed, so that requests of about one speed do not read as wild
// swings; both ends are multiples of 5 tok/s, so they hold still while
// the bars change a little.
func chartRange(series []float64) (lo, hi float64) {
	if len(series) == 0 {
		return 0, 50
	}
	lo, hi = series[0], series[0]
	for _, x := range series {
		lo, hi = min(lo, x), max(hi, x)
	}
	hi = max(5, math.Ceil(hi*1.05/5)*5)
	lo = min(lo-(hi-lo)/2, hi/2)
	return max(0, math.Floor(lo/5)*5), hi
}

// chart is the 20-bar speed chart, with a dashed line at avg (none when
// 0). It reads left to right in every language, and so do its keys.
func (p *Panel) chart(c *ui.Context, f *frame, series []float64, current int, dim bool, avg float64, barText func(int) string) {
	t, m := f.pal, f.m
	n := len(series)
	hover := -1
	group := ui.Row(c).Key("speed-chart").Direction(ui.LeftToRight).Height(40).Gap(3).AlignItems(ui.End).Role(ui.RoleGroup)
	// The range glides as the live bar does, so a new slowest or fastest
	// request rescales the bars smoothly.
	lo, hi := chartRange(series)
	lo = float64(group.Spring("lo", float32(lo), liveSpring))
	hi = float64(group.Spring("hi", float32(hi), liveSpring))
	// height is how high the bar of speed x stands, from 2 DIP (seen)
	// to the chart's 40.
	height := func(x float64) float32 {
		return min(40, max(2, float32(math.Round((x-lo)/(hi-lo)*100))/100*40))
	}
	if n > 0 {
		lo, hi := series[0], series[0]
		for _, x := range series {
			lo, hi = min(lo, x), max(hi, x)
		}
		group.Label(m.ChartLabel(n, format.Number(f.loc, lo, 1), format.TokPerSec(f.loc, hi)))
	}
	if avg > 0 {
		// Over the bars, so that each reads above or below the average.
		ah := height(avg)
		line := t.muted
		group.DrawOver(func(pt *ui.Painter, r ui.Rect) {
			y := r.Y + r.H - ah
			for x := r.X; x < r.X+r.W; x += 6 {
				pt.Fill(ui.Rect{X: x, Y: y, W: min(3, r.X+r.W-x), H: 1}, line, 0)
			}
		})
	}
	sel := p.barSel
	if sel < 0 {
		sel = n - 1
	}
	group.Children(func() {
		for i := range 20 - n {
			ui.Box(c).Key(-1 - i).Grow(1).Basis(0).Height(40)
		}
		for i, x := range series {
			h := height(x)
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
