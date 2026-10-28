package panel

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
)

// frameBench runs the main screen in v for a second of 60 Hz display
// frames per op and reports how many times the view was built.
func frameBench(b *testing.B, v core.View) {
	bn := newBench(&testing.T{}, "en")
	builds := 0
	bn.tt = ui.NewTester(func(c *ui.Context) { builds++; bn.p.View(c) }, Width, 1400)
	bn.show(v, time.Hour)
	bn.tt.Advance(time.Second)
	builds = 0
	b.ReportAllocs()
	for b.Loop() {
		for range 60 {
			bn.tt.Advance(time.Second / 60)
		}
	}
	b.ReportMetric(float64(builds)/float64(b.N), "builds/s")
}

// The spinner, the pulse, the sweep and the live bar move in frames that
// only paint: the view builds about once a second, for the clock.
func TestAnimationsOnlyPaint(t *testing.T) {
	for _, ph := range []core.Phase{core.PhaseLoading, core.PhaseGenerating} {
		bn := newBench(t, "en")
		builds := 0
		bn.tt = ui.NewTester(func(c *ui.Context) { builds++; bn.p.View(c) }, Width, 1400)
		bn.show(withPhase(ph), time.Minute)
		bn.tt.Advance(time.Second)
		builds = 0
		for range 60 {
			bn.tt.Advance(time.Second / 60)
		}
		if builds > 3 {
			t.Errorf("%v: %d builds in 1 s", ph, builds)
		}
	}
}

// BenchmarkGeneratingSecond: the pill pulses and the live bar grows.
func BenchmarkGeneratingSecond(b *testing.B) { frameBench(b, withPhase(core.PhaseGenerating)) }

// BenchmarkLoadingSecond: the loading bar sweeps and the pill spins.
func BenchmarkLoadingSecond(b *testing.B) { frameBench(b, withPhase(core.PhaseLoading)) }
