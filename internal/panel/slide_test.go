package panel

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
)

// tallSettings makes Settings the taller screen.
func tallSettings(b *bench) {
	pr := defaultPrefs()
	pr.Access, pr.APIKey = engine.AccessNetwork, "sk-splash-0000000000003f9a"
	b.p.SetPrefs(pr)
}

// slideFrames clicks s and plays the slide in 16 ms frames, resizing the
// tester's window to the height the panel reports, as the app does. It
// returns every height the panel reported through OnResize, the click's
// frame and every frame after it included.
func slideFrames(t *testing.T, b *bench, s string) []int {
	t.Helper()
	var reported []int
	b.p.OnResize = func(h int) { reported = append(reported, h) }
	if err := b.tt.Click(s); err != nil {
		t.Fatalf("click %q: %v", s, err)
	}
	for el := time.Duration(0); el < slideAnimation.Duration+200*time.Millisecond; el += 16 * time.Millisecond {
		b.tt.Advance(16 * time.Millisecond)
		b.tt.SetSize(Width, b.p.height)
		b.tt.Frame()
	}
	return reported
}

// The window height follows the slide: it goes from one screen's height to
// the other's in steady steps, with no jump to the taller screen's height at
// the start or to the shorter one's at the end.
func TestSlideHeight(t *testing.T) {
	for _, lang := range []string{"en", "ar"} {
		b := newBench(t, lang)
		tallSettings(b)
		b.show(withPhase(core.PhaseStopped), time.Hour)
		mainH := b.p.height

		up := slideFrames(t, b, b.m.Settings())
		if len(up) == 0 {
			t.Fatalf("%s: no height reported", lang)
		}
		settingsH := up[len(up)-1]
		if settingsH <= mainH || b.tt.FramePending() {
			t.Fatalf("%s: main %d, settings %d, pending %v", lang, mainH, settingsH, b.tt.FramePending())
		}
		prev, steps := mainH, 0
		for i, h := range up {
			if h < prev || h > settingsH {
				t.Fatalf("%s: slide in, frame %d: height %d after %d (main %d, settings %d): %v", lang, i, h, prev, mainH, settingsH, up)
			}
			if h > prev {
				steps++
			}
			prev = h
		}
		if steps < 6 {
			t.Errorf("%s: the height changed in %d steps only: %v", lang, steps, up)
		}
		if up[0] > mainH+(settingsH-mainH)/4 || b.p.height != settingsH {
			t.Errorf("%s: the height jumps at the start: %v", lang, up)
		}

		down := slideFrames(t, b, b.m.Back())
		if len(down) == 0 {
			t.Fatalf("%s: no height reported on the way back", lang)
		}
		if end := down[len(down)-1]; end != mainH {
			t.Errorf("%s: back: height %d, want %d", lang, end, mainH)
		}
		prev = settingsH
		for i, h := range down {
			if h > prev || h < mainH {
				t.Fatalf("%s: slide back, frame %d: height %d after %d: %v", lang, i, h, prev, down)
			}
			prev = h
		}
		if down[0] < settingsH-(settingsH-mainH)/4 {
			t.Errorf("%s: the height jumps at the start of the way back: %v", lang, down)
		}
	}
}

// With Reduce Motion the window takes the new screen's height at once.
func TestSlideHeightReduceMotion(t *testing.T) {
	b := newBench(t, "en")
	b.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tallSettings(b)
	b.show(withPhase(core.PhaseStopped), time.Hour)
	mainH := b.p.height
	var got []int
	b.p.OnResize = func(h int) { got = append(got, h) }
	if err := b.tt.Click(b.m.Settings()); err != nil {
		t.Fatal(err)
	}
	b.tt.Frame()
	b.tt.Frame()
	if len(got) == 0 || got[len(got)-1] <= mainH {
		t.Fatalf("reported heights %v, main %d", got, mainH)
	}
	for _, h := range got {
		if h != got[len(got)-1] {
			t.Errorf("heights on the way: %v", got)
		}
	}
}

// After Reset the panel shows the main screen at its own height at once.
func TestSlideHeightAfterReset(t *testing.T) {
	b := newBench(t, "en")
	tallSettings(b)
	b.show(withPhase(core.PhaseStopped), time.Hour)
	mainH := b.p.height
	b.click(b.m.Settings())
	if b.p.height <= mainH {
		t.Fatalf("Settings %d, main %d", b.p.height, mainH)
	}
	b.p.Reset()
	b.p.Measure(false)
	var got []int
	b.p.OnResize = func(h int) { got = append(got, h) }
	b.tt.Frame()
	b.tt.Frame()
	if b.p.height != mainH || b.tt.FramePending() {
		t.Errorf("height %d after Reset, want %d; pending %v; reported %v", b.p.height, mainH, b.tt.FramePending(), got)
	}
}
