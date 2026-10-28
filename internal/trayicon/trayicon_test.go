package trayicon

import (
	"bytes"
	"fmt"
	"image/png"
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
				ic := For(v, "en", dark)
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
	if ic := For(v, "en", false); ic.Text != "17 GB" || ic.Lit != 4 || ic.Label != "SPLASH" || ic.Glyph != GlyphNone {
		t.Errorf("ready: %+v", ic)
	}
	if ic := For(view(core.PhaseExternal), "en", false); ic.Text != "17 GB" || ic.Lit != 4 {
		t.Errorf("external: %+v", ic)
	}
	g := view(core.PhaseGenerating)
	if ic := For(g, "en", false); ic.Text != "41 T/S" || ic.Lit != 4 {
		t.Errorf("generating: %+v", ic)
	}
	g.Speed.Current = 0
	if ic := For(g, "en", false); ic.Text != "17 GB" {
		t.Errorf("generating without speed: %+v", ic)
	}
	if ic := For(view(core.PhaseLoading), "en", false); ic.Glyph != GlyphLoader || ic.Lit != 2 || ic.BarTone != ToneWarn {
		t.Errorf("loading: %+v", ic)
	}
	if ic := For(view(core.PhaseError), "en", false); ic.Glyph != GlyphAlert || ic.GlyphTone != ToneErr || ic.Lit != 2 {
		t.Errorf("error: %+v", ic)
	}
	// Weights on their way back look like loading, not like ready or zzz.
	if ic := For(view(core.PhaseRestoring), "en", false); ic.Glyph != GlyphLoader || ic.Lit != 2 || ic.BarTone != ToneWarn || ic.Text != "" {
		t.Errorf("restoring: %+v", ic)
	}
	if got, want := ToolTip(view(core.PhaseRestoring), "en"), ToolTip(view(core.PhaseLoading), "en"); got != want {
		t.Errorf("restoring tooltip %q, loading %q", got, want)
	}
	if ic := For(view(core.PhaseReleased), "en", false); ic.Glyph != GlyphZzz || ic.Lit != 0 || ic.GlyphTone != ToneInk {
		t.Errorf("released: %+v", ic)
	}
	if ic := For(view(core.PhaseStopped), "en", false); ic.Glyph != GlyphPower || ic.Lit != 0 || ic.GlyphTone != ToneMuted {
		t.Errorf("stopped: %+v", ic)
	}
	if ic := For(view(core.PhaseNoInstaller), "en", false); ic.Glyph != GlyphDownload {
		t.Errorf("no brew: %+v", ic)
	}
	// A running update overrides any state.
	u := view(core.PhaseReady)
	u.Update = core.UpdateUpdating
	if ic := For(u, "en", true); ic.Glyph != GlyphLoader || ic.Text != "" || ic.Lit != 2 || ic.BarTone != ToneWarn {
		t.Errorf("updating: %+v", ic)
	}
	// An available update changes nothing.
	u.Update = core.UpdateAvailable
	if ic := For(u, "en", true); ic.Text != "17 GB" {
		t.Errorf("update available: %+v", ic)
	}
}

func TestLocaleUnits(t *testing.T) {
	if ic := For(view(core.PhaseReady), "ru", false); !strings.Contains(ic.Text, "ГБ") {
		t.Errorf("ru: %q", ic.Text)
	}
	if ic := For(view(core.PhaseGenerating), "ru", false); !strings.Contains(ic.Text, "Т/С") {
		t.Errorf("ru: %q", ic.Text)
	}
	// Arabic keeps Latin digits and is not isolated: the icon reads left to right.
	if ic := For(view(core.PhaseReady), "ar", false); !strings.HasPrefix(ic.Text, "17") {
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
	ic := For(view(core.PhaseReady), "en", false)
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
	ic := For(view(core.PhaseGenerating), "en", true)
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
		img, err := png.Decode(bytes.NewReader(PNG(For(v, "en", false))))
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
		const barEnd = 2*scale + 9 // padding 1 pt, bar 4.5 pt
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
