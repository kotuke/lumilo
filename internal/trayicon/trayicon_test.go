package trayicon

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
)

var phases = []core.Phase{
	core.PhaseStopped, core.PhaseLoading, core.PhaseReady, core.PhaseGenerating,
	core.PhaseReleased, core.PhaseRestoring, core.PhaseError, core.PhaseExternal, core.PhaseUnresponsive,
	core.PhaseStopping, core.PhaseNotInstalled, core.PhaseNoInstaller, core.PhaseInstalling,
	core.PhaseInstallFailed,
}

func view(p core.Phase) core.View {
	return core.View{
		Engine: "Splash", Phase: p,
		Snapshot: engine.Snapshot{MemoryUsed: 17_200_000_000, MemoryLimit: 38_000_000_000},
		Speed:    core.Speed{Current: 41.3},
	}
}

func decode(t *testing.T, data []byte) (w, h int, cornerAlpha uint32) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, a := img.At(0, 0).RGBA()
	return img.Bounds().Dx(), img.Bounds().Dy(), a
}

// Every state of every engine draws in both themes: right height, sane
// width, transparent corner.
func TestEveryState(t *testing.T) {
	dir := os.Getenv("LUMILO_ICON_DIR")
	for _, name := range []string{"Splash", "llama.cpp"} {
		for _, p := range phases {
			for _, dark := range []bool{false, true} {
				v := view(p)
				v.Engine = name
				ic := For(v, "en", dark, liveTPS(v))
				data := PNG(ic)
				w, h, a := decode(t, data)
				if h != height*scale || w < 20 || w > maxWidth*scale {
					t.Errorf("%s phase %d dark=%v: size %dx%d", name, p, dark, w, h)
				}
				if a != 0 {
					t.Errorf("%s phase %d dark=%v: corner alpha %d", name, p, dark, a)
				}
				if dir != "" {
					file := fmt.Sprintf("%s-%02d-%s.png", strings.ToLower(name), p, map[bool]string{false: "light", true: "dark"}[dark])
					_ = os.WriteFile(filepath.Join(dir, file), data, 0o644)
				}
			}
		}
	}
}

func TestStates(t *testing.T) {
	v := view(core.PhaseReady)
	if ic := For(v, "en", false, liveTPS(v)); ic.Text != "17 GB" || ic.Lit != 4 || ic.Label != "SPLASH" || ic.Glyph != GlyphNone {
		t.Errorf("ready: %+v", ic)
	}
	if ic := For(view(core.PhaseExternal), "en", false, 0); ic.Text != "17 GB" || ic.Lit != 4 {
		t.Errorf("external: %+v", ic)
	}
	g := view(core.PhaseGenerating)
	if ic := For(g, "en", false, liveTPS(g)); ic.Text != "41 T/S" || ic.Lit != 4 {
		t.Errorf("generating: %+v", ic)
	}
	g.Speed.Current = 0
	if ic := For(g, "en", false, liveTPS(g)); ic.Text != "17 GB" {
		t.Errorf("generating without speed: %+v", ic)
	}
	if ic := For(view(core.PhaseLoading), "en", false, 0); ic.Glyph != GlyphLoader || ic.Lit != 2 || ic.BarTone != ToneWarn {
		t.Errorf("loading: %+v", ic)
	}
	if ic := For(view(core.PhaseError), "en", false, 0); ic.Glyph != GlyphAlert || ic.GlyphTone != ToneErr || ic.Lit != 2 {
		t.Errorf("error: %+v", ic)
	}
	// Weights on their way back look like loading, not like ready or zzz.
	if ic := For(view(core.PhaseRestoring), "en", false, 0); ic.Glyph != GlyphLoader || ic.Lit != 2 || ic.BarTone != ToneWarn || ic.Text != "" {
		t.Errorf("restoring: %+v", ic)
	}
	if got, want := ToolTip(view(core.PhaseRestoring), "en"), ToolTip(view(core.PhaseLoading), "en"); got != want {
		t.Errorf("restoring tooltip %q, loading %q", got, want)
	}
	if ic := For(view(core.PhaseReleased), "en", false, 0); ic.Glyph != GlyphZzz || ic.Lit != 0 || ic.GlyphTone != ToneInk {
		t.Errorf("released: %+v", ic)
	}
	if ic := For(view(core.PhaseStopped), "en", false, 0); ic.Glyph != GlyphPower || ic.Lit != 0 || ic.GlyphTone != ToneMuted {
		t.Errorf("stopped: %+v", ic)
	}
	if ic := For(view(core.PhaseNoInstaller), "en", false, 0); ic.Glyph != GlyphDownload {
		t.Errorf("no brew: %+v", ic)
	}
	// A running update overrides any state.
	u := view(core.PhaseReady)
	u.Update = core.UpdateUpdating
	if ic := For(u, "en", true, liveTPS(u)); ic.Glyph != GlyphLoader || ic.Text != "" || ic.Lit != 2 || ic.BarTone != ToneWarn {
		t.Errorf("updating: %+v", ic)
	}
	// An available update changes nothing.
	u.Update = core.UpdateAvailable
	if ic := For(u, "en", true, liveTPS(u)); ic.Text != "17 GB" {
		t.Errorf("update available: %+v", ic)
	}
}

func TestLocaleUnits(t *testing.T) {
	if ic := For(view(core.PhaseReady), "ru", false, 0); !strings.Contains(ic.Text, "ГБ") {
		t.Errorf("ru: %q", ic.Text)
	}
	if ic := For(view(core.PhaseGenerating), "ru", false, liveTPS(view(core.PhaseGenerating))); !strings.Contains(ic.Text, "Т/С") {
		t.Errorf("ru: %q", ic.Text)
	}
	// Arabic keeps Latin digits and is not isolated: the icon reads left to right.
	if ic := For(view(core.PhaseReady), "ar", false, 0); !strings.HasPrefix(ic.Text, "17") {
		t.Errorf("ar: %q", ic.Text)
	}
}

func TestLitSegments(t *testing.T) {
	const limit = 38_000_000_000
	for _, c := range []struct {
		used int64
		want int
	}{{0, 0}, {1, 0}, {2_400_000_000, 1}, {17_200_000_000, 4}, {21_400_000_000, 5}, {38_000_000_000, 8}, {90_000_000_000, 8}} {
		if got := LitSegments(c.used, limit); got != c.want {
			t.Errorf("used %d: got %d, want %d", c.used, got, c.want)
		}
	}
	if LitSegments(5, 0) != 0 {
		t.Error("no limit must light nothing")
	}
}

func TestToolTip(t *testing.T) {
	if got := ToolTip(view(core.PhaseGenerating), "en"); got != "Lumilo — Splash Responding" && !strings.HasPrefix(got, "Lumilo — Splash ") {
		t.Errorf("tooltip %q", got)
	}
	tips := map[string]bool{}
	for _, p := range phases {
		tip := ToolTip(view(p), "en")
		if !strings.HasPrefix(tip, "Lumilo — Splash ") || len(tip) <= len("Lumilo — Splash ") {
			t.Errorf("phase %d: %q", p, tip)
		}
		tips[tip] = true
	}
	if len(tips) < 11 {
		t.Errorf("too few distinct tooltips: %d", len(tips))
	}
	u := view(core.PhaseReady)
	u.Update = core.UpdateUpdating
	if ToolTip(u, "en") == ToolTip(view(core.PhaseReady), "en") {
		t.Error("updating must change the tooltip")
	}
}

func TestCacheAndLight(t *testing.T) {
	ic := For(view(core.PhaseReady), "en", false, 0)
	a, b := PNG(ic), PNG(ic)
	if &a[0] != &b[0] {
		t.Error("identical icons must come from the cache")
	}
	ic.Dark = true
	if bytes.Equal(a, PNG(ic)) {
		t.Error("themes must differ")
	}
}

func TestBreathing(t *testing.T) {
	ic := For(view(core.PhaseGenerating), "en", true, liveTPS(view(core.PhaseGenerating)))
	seen := map[uint8]bool{}
	for i := range BreathSteps {
		seen[Frame(ic, i).Dim] = true
	}
	if Frame(ic, 0).Dim != 0 || Frame(ic, BreathSteps/2).Dim != breathDepth || len(seen) < 4 {
		t.Errorf("frames: %v", seen)
	}
	if Frame(ic, BreathSteps).Dim != Frame(ic, 0).Dim {
		t.Error("cycle must wrap")
	}
	if bytes.Equal(PNG(Frame(ic, 0)), PNG(Frame(ic, BreathSteps/2))) {
		t.Error("a dimmed bar must look different")
	}
	if !Breathing(view(core.PhaseGenerating)) || Breathing(view(core.PhaseReady)) {
		t.Error("Breathing must follow the generating phase")
	}
	if BreathSteps*BreathInterval != 2*time.Second {
		t.Error("cycle must take 2 s")
	}
}

// The label sits where Kvoto's labels do (same 7 pt bold label over a 10 pt
// value in an 18 pt box, centred), so it has the same cap height and
// baseline next to Kvoto's icon in the menu bar. At @2x the label's ink
// starts in the top two pixel rows, the value's ink ends in the bottom two,
// and the bar is centred (a third of the height clear above and below).
func TestLabelPosition(t *testing.T) {
	v := view(core.PhaseReady)
	for _, name := range []string{"Splash", "llama.cpp"} {
		v.Engine = name
		img, err := png.Decode(bytes.NewReader(PNG(For(v, "en", false, liveTPS(v)))))
		if err != nil {
			t.Fatal(err)
		}
		inkRows := func(x0, x1 int, min uint32) (first, last int) {
			first, last = -1, -1
			for y := 0; y < img.Bounds().Dy(); y++ {
				for x := x0; x < x1; x++ {
					if _, _, _, a := img.At(x, y).RGBA(); a > min {
						if first < 0 {
							first = y
						}
						last = y
						break
					}
				}
			}
			return first, last
		}
		barEnd := 2*scale + 9 // padding 1 pt, bar 4.5 pt
		bTop, bBot := inkRows(0, barEnd, 0)
		tTop, tBot := inkRows(barEnd+6, img.Bounds().Dx(), 0x4000)
		h := img.Bounds().Dy()
		if tTop > 2 || tBot < h-3 {
			t.Errorf("%s: text ink rows %d..%d of %d", name, tTop, tBot, h)
		}
		if bTop < 2 || bTop > 4 || h-1-bBot < 2 || h-1-bBot > 4 {
			t.Errorf("%s: bar rows %d..%d of %d, want it centred", name, bTop, bBot, h)
		}
	}
}

// liveTPS is the speed the app shows for v without a hold: the live one
// while generating.
func liveTPS(v core.View) float64 {
	if v.Phase == core.PhaseGenerating {
		return v.Speed.Current
	}
	return 0
}

// Speed holds the last speed after a generation, eases toward a new one
// in steps, and shows none once the hold is over or the server stops.
func TestSpeedHoldAndEase(t *testing.T) {
	t0 := time.Unix(1000, 0)
	gen := view(core.PhaseGenerating)
	gen.Speed.Current = 80
	ready := view(core.PhaseReady)
	var s Speed
	s.Ease = true
	s.Target(gen, t0)
	s.Step(t0)
	if s.Shown() != 80 || !s.Active() {
		t.Fatalf("first speed: %v", s.Shown())
	}
	// A faster stretch: the number moves toward 120, not at once.
	gen.Speed.Current = 120
	s.Target(gen, t0.Add(100*time.Millisecond))
	s.Step(t0.Add(100 * time.Millisecond))
	if got := s.Shown(); got <= 80 || got >= 100 {
		t.Errorf("after one step: %v", got)
	}
	for i := 2; i <= 30; i++ {
		s.Step(t0.Add(time.Duration(i) * 100 * time.Millisecond))
	}
	if got := s.Shown(); math.Abs(got-120) > 1 {
		t.Errorf("after 3 s: %v", got)
	}
	// The agent runs a tool: the server is ready, the speed holds.
	at := t0.Add(5 * time.Second)
	s.Target(ready, at)
	if s.Shown() < 119 || !s.Active() {
		t.Errorf("held: %v", s.Shown())
	}
	if ic := For(ready, "en", false, s.Shown()); ic.Text != "120 T/S" {
		t.Errorf("held icon %q", ic.Text)
	}
	// The next request reads a long prompt: longer than the hold, with no
	// speed of its own, the last one stays.
	reading := view(core.PhaseGenerating)
	reading.Speed.Current, reading.Snapshot.Active = 0, 1
	s.Target(reading, at.Add(SpeedHold+40*time.Second))
	if s.Shown() < 119 {
		t.Errorf("while reading a prompt: %v", s.Shown())
	}
	at = at.Add(SpeedHold + 40*time.Second)
	s.Target(ready, at.Add(SpeedHold+time.Second))
	if s.Shown() != 0 || s.Active() {
		t.Errorf("after the hold: %v", s.Shown())
	}
	// Stopped: none at once.
	s.Target(gen, at)
	s.Target(view(core.PhaseStopped), at.Add(time.Second))
	if s.Shown() != 0 {
		t.Errorf("stopped: %v", s.Shown())
	}
	// Without easing a change shows at once.
	s = Speed{}
	gen.Speed.Current = 50
	s.Target(gen, t0)
	gen.Speed.Current = 90
	s.Target(gen, t0.Add(time.Second))
	if s.Shown() != 90 {
		t.Errorf("no easing: %v", s.Shown())
	}
}
