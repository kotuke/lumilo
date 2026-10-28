package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
)

// LUMILO_SHOT_DIR=… saves the slide to Settings at 2×, with the window as
// tall as the panel reports at each moment.
func TestShotSlide(t *testing.T) {
	dir := os.Getenv("LUMILO_SHOT_DIR")
	if dir == "" {
		t.Skip("LUMILO_SHOT_DIR is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{true, false} {
		b := newBench(t, "en")
		b.tt.SetDark(dark)
		tallSettings(b)
		b.show(withPhase(core.PhaseReady), time.Hour)
		if err := b.tt.Click(b.m.Settings()); err != nil {
			t.Fatal(err)
		}
		theme := map[bool]string{true: "dark", false: "light"}[dark]
		var elapsed time.Duration
		for i, at := range []time.Duration{0, 50 * time.Millisecond, 100 * time.Millisecond, 160 * time.Millisecond, 400 * time.Millisecond} {
			b.tt.Advance(at - elapsed)
			elapsed = at
			b.tt.Frame()
			b.tt.SetSize(Width, b.p.height)
			b.tt.SetScale(2)
			b.tt.Frame()
			save(t, b.tt, filepath.Join(dir, fmt.Sprintf("slide-%d-%dms-%dpt-%s.png", i, at/time.Millisecond, b.p.height, theme)))
		}
	}
}
