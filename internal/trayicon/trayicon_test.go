package trayicon

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/engines/splash"
	"github.com/kotuke/lumilo/internal/engines/tensorfold"
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

// Every state of every engine draws: right height, sane width,
// transparent corner, and only black ink, as a template image must.
func TestEveryState(t *testing.T) {
	dir := os.Getenv("LUMILO_ICON_DIR")
	for _, name := range []string{"Splash", "llama.cpp"} {
		for _, p := range phases {
			v := view(p)
			v.Engine = name
			data := PNG(For(v, "en", liveTPS(v)))
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			w, h := img.Bounds().Dx(), img.Bounds().Dy()
			if h != height*scale || w < markSize*scale || w > maxWidth*scale {
				t.Errorf("%s phase %d: size %dx%d", name, p, w, h)
			}
			if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
				t.Errorf("%s phase %d: corner alpha %d", name, p, a)
			}
			if !blackOnly(img) {
				t.Errorf("%s phase %d: not a template image", name, p)
			}
			if dir != "" {
				file := fmt.Sprintf("%s-%02d.png", strings.ToLower(name), p)
				_ = os.WriteFile(filepath.Join(dir, file), data, 0o644)
			}
		}
	}
}

func blackOnly(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA); c.A > 0 && (c.R|c.G|c.B) != 0 {
				return false
			}
		}
	}
	return true
}

func TestStates(t *testing.T) {
	want := func(name string, got, want Icon) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	v := view(core.PhaseReady)
	want("ready", For(v, "en", liveTPS(v)), Icon{Mark: MarkSpark, Engine: "Splash", Text: "17 GB"})
	want("external", For(view(core.PhaseExternal), "en", 0), Icon{Mark: MarkSpark, Engine: "Splash", Text: "17 GB"})
	g := view(core.PhaseGenerating)
	want("generating", For(g, "en", liveTPS(g)), Icon{Mark: MarkSpark, Engine: "Splash", Text: "41 T/S"})
	g.Speed.Current = 0
	want("generating without speed", For(g, "en", liveTPS(g)), Icon{Mark: MarkSpark, Engine: "Splash", Text: "17 GB"})
	want("loading", For(view(core.PhaseLoading), "en", 0), Icon{Mark: MarkLoader, Faded: true, Engine: "Splash"})
	want("error", For(view(core.PhaseError), "en", 0), Icon{Mark: MarkAlert, Engine: "Splash"})
	want("unresponsive", For(view(core.PhaseUnresponsive), "en", 0), Icon{Mark: MarkAlert, Engine: "Splash"})
	// Weights on their way back look like loading, not like ready or released.
	want("restoring", For(view(core.PhaseRestoring), "en", 0), Icon{Mark: MarkLoader, Faded: true, Engine: "Splash"})
	if got, want := ToolTip(view(core.PhaseRestoring), "en"), ToolTip(view(core.PhaseLoading), "en"); got != want {
		t.Errorf("restoring tooltip %q, loading %q", got, want)
	}
	want("released", For(view(core.PhaseReleased), "en", 0), Icon{Mark: MarkHollow, Faded: true, Engine: "Splash"})
	want("stopped", For(view(core.PhaseStopped), "en", 0), Icon{Mark: MarkSpark, Engine: "Splash", Faded: true})
	want("no brew", For(view(core.PhaseNoInstaller), "en", 0), Icon{Mark: MarkDownload, Faded: true, Engine: "Splash"})
	// A running update overrides any state.
	u := view(core.PhaseReady)
	u.Update = core.UpdateUpdating
	want("updating", For(u, "en", liveTPS(u)), Icon{Mark: MarkLoader, Faded: true, Engine: "Splash"})
	// An available update changes nothing.
	u.Update = core.UpdateAvailable
	want("update available", For(u, "en", liveTPS(u)), Icon{Mark: MarkSpark, Engine: "Splash", Text: "17 GB"})
}

func TestLocaleUnits(t *testing.T) {
	if ic := For(view(core.PhaseReady), "ru", 0); !strings.Contains(ic.Text, "ГБ") {
		t.Errorf("ru: %q", ic.Text)
	}
	if ic := For(view(core.PhaseGenerating), "ru", liveTPS(view(core.PhaseGenerating))); !strings.Contains(ic.Text, "Т/С") {
		t.Errorf("ru: %q", ic.Text)
	}
	// Arabic keeps Latin digits and is not isolated: the icon reads left to right.
	if ic := For(view(core.PhaseReady), "ar", 0); !strings.HasPrefix(ic.Text, "17") {
		t.Errorf("ar: %q", ic.Text)
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

// Each state has its own look, and identical icons come from the cache.
func TestCacheAndLooks(t *testing.T) {
	ic := For(view(core.PhaseReady), "en", 0)
	a, b := PNG(ic), PNG(ic)
	if &a[0] != &b[0] {
		t.Error("identical icons must come from the cache")
	}
	seen := map[string]Icon{}
	for _, ic := range []Icon{{}, {Faded: true}, {Mark: MarkLoader}, {Mark: MarkHollow}, {Mark: MarkAlert}, {Mark: MarkDownload, Faded: true}} {
		if prev, ok := seen[string(PNG(ic))]; ok {
			t.Errorf("%+v looks like %+v", ic, prev)
		}
		seen[string(PNG(ic))] = ic
	}
	if bytes.Equal(Shot(ic, false), Shot(ic, true)) {
		t.Error("shots must differ by theme")
	}
}

func TestBreathing(t *testing.T) {
	ic := For(view(core.PhaseGenerating), "en", liveTPS(view(core.PhaseGenerating)))
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
		t.Error("a dimmed spark must look different")
	}
	if !Breathing(view(core.PhaseGenerating)) || !Breathing(view(core.PhaseLoading)) || Breathing(view(core.PhaseReady)) {
		t.Error("Breathing must follow the generating and loading phases")
	}
	// A loading mark is faded when still and pulses from full.
	if l := For(view(core.PhaseLoading), "en", 0); Frame(l, 0).Faded || Frame(l, 0).Dim != 0 {
		t.Errorf("loading frame: %+v", Frame(l, 0))
	}
	if BreathSteps*BreathInterval != 2*time.Second {
		t.Error("cycle must take 2 s")
	}
}

// The mark is centred in the 18 pt height: 1 pt clear above and below.
func TestMarkCentred(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG(Icon{})))
	if err != nil {
		t.Fatal(err)
	}
	first, last := -1, -1
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x4000 {
				if first < 0 {
					first = y
				}
				last = y
				break
			}
		}
	}
	h := img.Bounds().Dy()
	if top, bottom := first, h-1-last; top < scale || bottom < scale || top-bottom > 1 || bottom-top > 1 {
		t.Errorf("mark rows %d..%d of %d", first, last, h)
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
	if ic := For(ready, "en", s.Shown()); ic.Text != "120 T/S" {
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

// With an engine's logo every state still has its own look, the badges
// widen the icon, and the icon stays a template image.
func TestLogoStates(t *testing.T) {
	SetLogo("Square", engine.Logo{Kind: engine.LogoSVG,
		Light: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="20" rx="4"/></svg>`)})
	seen := map[string]core.Phase{}
	plain := 0
	for _, p := range []core.Phase{core.PhaseReady, core.PhaseStopped, core.PhaseReleased, core.PhaseError, core.PhaseNotInstalled} {
		ic := For(view(p), "en", 0)
		ic.Engine, ic.Text = "Square", ""
		img, err := png.Decode(bytes.NewReader(PNG(ic)))
		if err != nil {
			t.Fatal(err)
		}
		if !blackOnly(img) {
			t.Errorf("phase %d: not a template image", p)
		}
		if prev, ok := seen[string(PNG(ic))]; ok {
			t.Errorf("phase %d looks like phase %d", p, prev)
		}
		seen[string(PNG(ic))] = p
		if p == core.PhaseReady {
			plain = img.Bounds().Dx()
		} else if ic.Mark != MarkSpark && img.Bounds().Dx() <= plain {
			t.Errorf("phase %d: a badge must widen the icon", p)
		}
	}
	l := For(view(core.PhaseLoading), "en", 0)
	l.Engine = "Square"
	if bytes.Equal(PNG(Frame(l, 0)), PNG(l)) {
		t.Error("a pulsing logo must start from full")
	}
}

// A pulsing logo keeps its place: every frame of the loading pulse and the
// full logo of Ready cover the same pixels, so the icon does not jump when
// the pulse comes back to full.
func TestLogoPulseKeepsPlace(t *testing.T) {
	for name, l := range map[string]engine.Logo{
		"Splash": splash.New().Info().Logo, "llama.cpp": llamacpp.New().Info().Logo, "TensorFold": tensorfold.New().Info().Logo,
	} {
		key := "pulse-" + name
		SetLogo(key, l)
		ink := func(ic Icon) image.Rectangle {
			img, err := png.Decode(bytes.NewReader(PNG(ic)))
			if err != nil {
				t.Fatal(err)
			}
			r := image.Rectangle{}
			b := img.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if _, _, _, a := img.At(x, y).RGBA(); a > 0x0800 {
						r = r.Union(image.Rect(x, y, x+1, y+1))
					}
				}
			}
			return r
		}
		want := ink(Icon{Engine: key})
		loading := Icon{Engine: key, Mark: MarkLoader, Faded: true}
		if got := ink(loading); got != want {
			t.Errorf("%s still loading: ink %v, ready %v", name, got, want)
		}
		for i := range BreathSteps {
			if got := ink(Frame(loading, i)); got != want {
				t.Errorf("%s pulse frame %d: ink %v, ready %v", name, i, got, want)
			}
		}
	}
}
