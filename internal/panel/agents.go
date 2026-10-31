package panel

import (
	"github.com/egoist/mygo/ui"
)

// maxAgents is how many agent chips keep their measured width.
const maxAgents = 8

// Agents card.
func (p *Panel) agentsCard(c *ui.Context, f *frame, s *mainState) {
	t, m, v := f.pal, f.m, p.view
	card(c, f, 8).Children(func() {
		txt(c, m.AgentsTitle(), 12.5).FontWeight(600)
		ui.Row(c).Wrap().Gap(6).Children(func() {
			for i, a := range v.Agents {
				logo := f.brands.svg(a.Logo, 0)
				off, title := s.agentsOff, s.agentTitle
				if have := v.Snapshot.ContextTokens; !off && a.MinContext > 0 && have > 0 && have < a.MinContext {
					off, title = true, m.AgentNeedsContext(a.Name, ctxName(f.loc, a.MinContext), ctxName(f.loc, have))
				}
				// A disabled button is drawn at half opacity (MyGo
				// paint.go), the mockup's chips are not: when off, the chip
				// is a plain row under a disabled button of its size.
				var b ui.Element
				if off {
					b = ui.Row(c).Justify(ui.Center).AlignItems(ui.Center).TextColor(t.muted)
				} else {
					id := a.ID
					b = btn(c).Label(a.Name).TextColor(t.text).OnClick(func() { p.act.RunAgent(id) })
					if title != "" {
						b.Tooltip(title)
					}
				}
				b.Key(a.ID).Height(26).Radius(13).Background(t.chip).PaddingStart(8).PaddingEnd(10).Gap(6)
				if i < maxAgents {
					extra := float32(8 + 10)
					if logo != nil {
						extra += 13 + 6
					}
					growAuto(c, b, &p.agentTxt[i], &p.agentW[i], extra)
				} else {
					b.Grow(1)
				}
				ink := t.text
				if a.Color != "" {
					ink = ui.Hex(a.Color)
				}
				b.Children(func() {
					if logo != nil {
						l := ui.Icon(c, logo).Size(13, 13).TextColor(ink)
						if off {
							l.Opacity(0.45)
						}
					}
					name := txt(c, a.Name, 12).NoWrap()
					if i < maxAgents {
						name.Bind(&p.agentTxt[i])
					}
					if off {
						cover := ui.ButtonBase(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Disabled(true).
							Cursor(ui.CursorDefault).Label(a.Name)
						if title != "" {
							cover.Tooltip(title).Description(title)
						}
					}
				})
			}
		})
	})
}
