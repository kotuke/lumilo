package panel

import (
	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
)

// Memory card: 30-segment memory bar, Unload or Load, disk bar.
func (p *Panel) memoryCard(c *ui.Context, f *frame, s *mainState) {
	t, m, v := f.pal, f.m, p.view
	sn := v.Snapshot
	// The bar fills against what the engine may use: what it reports, else
	// three quarters of the Mac's memory (llama.cpp reports none).
	limit := p.budget()
	pct := percent(sn.MemoryUsed, limit)
	// Red with a label when memory is nearly exhausted or the system runs
	// short of it; yellow, without a label, for the system's "warning"
	// pressure, which macOS reports often (decision of the user,
	// 2026-10-27).
	full := pct >= 90
	pressed := sn.Pressure >= engine.PressureCritical
	memFill := t.barHi
	switch {
	case full || pressed:
		memFill = t.errBar
	case sn.Pressure == engine.PressureWarning:
		memFill = t.warnBar
	}
	card(c, f, 6).Children(func() {
		// The idle note sits beside the title while both fit on the line,
		// else under it (Arabic).
		tw, _ := c.MeasureText(0, ui.Span{Text: m.Memory(), Size: 12.5, Weight: 600})
		iw, _ := c.MeasureText(0, ui.Span{Text: s.idle, Size: 11.5})
		beside := s.idle == "" || tw+6+iw <= cardInner
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			txt(c, m.Memory(), 12.5).FontWeight(600).Grow(1)
			if s.idle != "" && beside {
				txt(c, s.idle, 11.5).TextColor(t.muted)
			}
		})
		if !beside {
			txt(c, s.idle, 11.5).TextColor(t.muted).Margin(-4, 0, 0, 0)
		}
		if limit > 0 {
			bar := ui.Box(c).PaddingY(3)
			meter(bar, m.Memory(), m.UsedOf(format.Number(f.loc, float64(sn.MemoryUsed)/1e9, 1), format.GB(f.loc, limit)),
				sn.MemoryUsed, limit)
			bar.Children(func() {
				segments(c, f, 8, 2, func(i int) ui.Color {
					if filled(i, 30, pct) {
						return memFill
					}
					return t.segEmpty
				})
			})
		}
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			txt(c, format.GB(f.loc, sn.MemoryUsed), 17).FontWeight(650).NoWrap()
			if limit > 0 {
				txt(c, m.OfTotal(format.GB(f.loc, limit)), 11.5).TextColor(t.muted).NoWrap()
			} else {
				// No budget to measure against: the number stands as the
				// memory the server takes.
				txt(c, m.MemInUse(), 11.5).TextColor(t.muted).NoWrap()
			}
			if full || pressed {
				// Full is full; when the bar is not, the system's pressure is
				// the cause.
				label := m.Almost()
				if !full {
					label = m.MemPressure()
				}
				ui.Row(c).Gap(4).TextColor(t.err).Shrink(1).MinWidth(0).Children(func() {
					ico(c, "alert-triangle", 2, 12).Shrink(0)
					txt(c, label, 11.5).FontWeight(600).SingleLine()
				})
			}
			label, fn := "", func() {}
			switch {
			case s.canRelease:
				label, fn = m.Unload(), p.act.Release
			case s.canLoad:
				label, fn = m.Load(), p.act.Restore
			case s.restoring:
				label = m.Load() // the weights are coming back: nothing to click
			}
			if label != "" {
				b := btn(c).Height(22).Radius(6).Background(t.chip).TextColor(t.text).PaddingX(10).MarginStart(ui.Auto).
					AlignSelf(ui.Center).Shrink(0)
				if s.restoring {
					b.Disabled(true).Cursor(ui.CursorDefault)
				} else {
					b.OnClick(fn)
				}
				b.Children(func() { txt(c, label, 12).NoWrap() })
			}
		})
		if !v.Caps.DiskCache {
			return
		}
		ui.Column(c).Gap(5).Padding(6, 0, 0, 0).Children(func() {
			used := format.GB(f.loc, sn.DiskUsed)
			if sn.DiskLimit > 0 {
				used = m.UsedOf(format.Number(f.loc, float64(sn.DiskUsed)/1e9, 1), format.GB(f.loc, sn.DiskLimit))
			}
			ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
				txt(c, m.Disk(), 12).TextColor(t.soft).Grow(1)
				txt(c, used, 12).FontWeight(600).NoWrap()
			})
			dpct := percent(sn.DiskUsed, sn.DiskLimit)
			meter(segments(c, f, 4, 1.5, func(i int) ui.Color {
				if filled(i, 30, dpct) {
					return t.barHi
				}
				return t.segEmpty
			}), m.Disk(), used, sn.DiskUsed, sn.DiskLimit)
		})
	})
}

// cardInner is the width of a card's content: the screen's, less its
// padding and the card's.
const cardInner = pageWidth - 2*pagePad - 2*12

func percent(used, limit int64) float64 {
	if limit <= 0 || used <= 0 {
		return 0
	}
	return float64(used) / float64(limit) * 100
}
