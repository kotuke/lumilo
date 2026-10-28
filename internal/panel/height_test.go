package panel

import (
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
)

func TestPlanHeight(t *testing.T) {
	t.Parallel()
	main := func(page float32) heightPlan {
		return heightPlan{row: 900, screen: screenMain, page: [2]float32{page, 0}, last: [2]float32{page, 800}}
	}
	for _, tc := range []struct {
		name string
		in   heightPlan
		want int
	}{
		{"first frame: the row", heightPlan{row: 500.4}, 502},
		{"the screen shown, not the row", main(600), 602},
		{"settings", heightPlan{row: 900, screen: screenSettings, page: [2]float32{0, 800}, last: [2]float32{600, 800}}, 802},
		{"slide halfway", heightPlan{row: 800, sliding: true, at: 0.5, last: [2]float32{600, 800}}, 702},
		{"slide before Settings is known", heightPlan{row: 800, sliding: true, at: 0.5, last: [2]float32{600, 0}}, 802},
		{"a popup below the screen", func() heightPlan { in := main(600); in.overlay = 700; return in }(), 710},
		{"a popup within it", func() heightPlan { in := main(600); in.overlay = 500; return in }(), 602},
		{"the screen's limit", func() heightPlan { in := main(1037); in.max = 800; return in }(), 800},
		{"the limit over a popup", func() heightPlan { in := main(600); in.overlay = 900; in.max = 800; return in }(), 800},
	} {
		if got := planHeight(tc.in); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A screen taller than the window may be scrolls under its header, which
// stays: Settings with the network access, on a short screen.
func TestMaxHeightScrolls(t *testing.T) {
	for _, s := range []screen{screenMain, screenSettings} {
		b := newBench(t, "en")
		pr := defaultPrefs()
		pr.Access, pr.APIKey = engine.AccessNetwork, "sk-lumilo-0123456789abcdef"
		b.p.SetPrefs(pr)
		b.show(withPhase(core.PhaseGenerating), time.Hour)
		if s == screenSettings {
			b.click(b.m.Settings())
		}
		full := b.p.Measure(false)
		b.p.SetMaxHeight(full - 200)
		b.tt.Frame()
		b.tt.Frame()
		if b.p.height != full-200 {
			t.Errorf("screen %d: height %d, want %d", s, b.p.height, full-200)
		}
		head := b.m.SettingsTitle()
		if s == screenMain {
			head = "Splash 1.4.0"
		}
		if r, ok := b.tt.Find(head); !ok || r.Y > 40 {
			t.Errorf("screen %d: the header %q is not at the top: %v %v", s, head, r, ok)
		}
		b.p.SetMaxHeight(0)
		b.tt.Frame()
		b.tt.Frame()
		if b.p.height != full {
			t.Errorf("screen %d: without a limit %d, want %d", s, b.p.height, full)
		}
	}
}
