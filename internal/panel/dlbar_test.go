package panel

import (
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
)

// downloadView is a download run of the model with the known size total and
// done bytes fetched.
func downloadView(total, done int64) core.View {
	v := withPhase(core.PhaseLoading)
	v.Models = nil
	m := engine.Model{ID: "unsloth/Other-GGUF:Q8_0", Owner: "unsloth", Name: "Other-GGUF", Variant: "Q8_0"}
	v.Model, v.Download, v.Downloading, v.DownloadTotal, v.DownloadBytes = m, m.ID, true, total, done
	return v
}

// The bar of a download fills the segments of the fetched part; it sweeps
// only while the size, or the first byte, is not known.
func TestDownloadBarFraction(t *testing.T) {
	for _, tc := range []struct {
		total, done int64
		pct         float64
		segments    int
	}{
		{0, 0, -1, 0},
		{10e9, 0, -1, 0},
		{0, 3e9, -1, 0},
		{10e9, 1, 0.00000001, 1},
		{10e9, 1e9, 10, 3},
		{10e9, 4e9, 40, 12},
		{10e9, 5e9, 50, 15},
		{10e9, 10e9, 100, 30},
		{10e9, 11e9, 100, 30},
	} {
		b := newBench(t, "en")
		b.show(downloadView(tc.total, tc.done), 0)
		pct := b.p.downloadPercent()
		if tc.pct < 0 && pct >= 0 || tc.pct >= 0 && (pct <= 0 || pct > 100) {
			t.Errorf("%d of %d: %v %%", tc.done, tc.total, pct)
			continue
		}
		if tc.pct < 0 {
			continue
		}
		n := 0
		for i := range 30 {
			if filled(i, 30, pct) {
				n++
			}
		}
		if n != tc.segments {
			t.Errorf("%d of %d: %d segments, want %d", tc.done, tc.total, n, tc.segments)
		}
	}
}

// The speed is that of the last seconds, not of the last burst.
func TestDownloadSpeedIsAveraged(t *testing.T) {
	p := New(&recorder{})
	t0 := time.Unix(1000, 0)
	at := func(s int, bytes int64) float64 { return p.addSample(t0.Add(time.Duration(s)*time.Second), bytes) }
	if r := at(0, 1_000_000_000); r != 0 {
		t.Errorf("one sample: %v", r)
	}
	at(1, 1_100_000_000) // a burst
	// A trickle after it: the last step alone is 1 MB/s.
	r := at(2, 1_101_000_000)
	if r < 40e6 || r > 60e6 {
		t.Errorf("average %v B/s, want about 50 MB/s", r)
	}
	// Long after, the old bursts are out of the window; one sample before it
	// stays as the start of the span.
	r = at(30, 1_201_000_000)
	if want := 100e6 / 28; r < want*0.99 || r > want*1.01 {
		t.Errorf("average %v B/s, want %v", r, want)
	}
	if len(p.dlSamples) > 3 {
		t.Errorf("%d samples kept", len(p.dlSamples))
	}
	// A count that starts over starts the average over.
	if r := at(31, 500_000_000); r != 0 || len(p.dlSamples) != 1 {
		t.Errorf("restart: %v, %d samples", r, len(p.dlSamples))
	}
}
