package trayicon

import (
	"math"
	"strings"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
)

// BreathSteps frames make one breathing cycle; BreathInterval is the time
// between frames. A cycle takes two seconds, as in the mockup.
const (
	BreathSteps    = 8
	BreathInterval = 250 * time.Millisecond
	breathDepth    = 55 // the bar fades to 45 % opacity
)

// For maps the supervisor's view to an icon. loc is the app locale
// (mygo.App.Locale()); dark is Tray.Dark().
//
//	state                          bar (bottom up)    value
//	stopped, stopping              0 lit              power, muted
//	loading, installing, updating, restoring  2 lit, warn  loader, warn
//	ready, external                memory fraction    memory "17 GB"
//	generating                     memory fraction    speed "41 T/S"; memory until a speed is known
//	released                       0 lit              zzz, ink
//	unresponsive                   2 lit, warn        alert, warn
//	error, install failed          2 lit, err         alert, err
//	not installed, no Homebrew     0 lit              download, muted
//
// An update that is running overrides the state: loading look, no text.
func For(v core.View, loc string, dark bool) Icon {
	ic := Icon{Label: strings.ToUpper(v.Engine), Dark: dark}
	busy := func() { ic.Glyph, ic.GlyphTone, ic.Lit, ic.BarTone = GlyphLoader, ToneWarn, 2, ToneWarn }
	fail := func(g Glyph, t Tone) { ic.Glyph, ic.GlyphTone, ic.Lit, ic.BarTone = g, t, 2, t }
	idle := func(g Glyph, t Tone) { ic.Glyph, ic.GlyphTone = g, t }
	memory := func() {
		ic.Text = format.TrayGB(loc, v.Snapshot.MemoryUsed)
		ic.Lit = LitSegments(v.Snapshot.MemoryUsed, v.Snapshot.MemoryLimit)
	}

	if v.Update == core.UpdateUpdating {
		busy()
		return ic
	}
	switch v.Phase {
	case core.PhaseStopped, core.PhaseStopping:
		idle(GlyphPower, ToneMuted)
	case core.PhaseLoading, core.PhaseInstalling, core.PhaseRestoring:
		busy()
	case core.PhaseReady, core.PhaseExternal:
		memory()
	case core.PhaseGenerating:
		memory()
		if cur := v.Speed.Current; cur > 0 {
			ic.Text = format.TrayTPS(loc, cur)
		}
	case core.PhaseReleased:
		idle(GlyphZzz, ToneInk)
	case core.PhaseUnresponsive:
		fail(GlyphAlert, ToneWarn)
	case core.PhaseError, core.PhaseInstallFailed:
		fail(GlyphAlert, ToneErr)
	case core.PhaseNotInstalled, core.PhaseNoInstaller:
		idle(GlyphDownload, ToneMuted)
	}
	return ic
}

// LitSegments is how many of the 8 segments a memory fraction lights:
// segment i is lit when (i + 0.5) / 8 <= used / limit.
func LitSegments(used, limit int64) int {
	if limit <= 0 || used <= 0 {
		return 0
	}
	f := float64(used) / float64(limit)
	n := 0
	for i := range segments {
		if (float64(i)+0.5)/segments <= f {
			n++
		}
	}
	return n
}

// Breathing reports whether the bar should breathe in this view.
func Breathing(v core.View) bool {
	return v.Phase == core.PhaseGenerating && v.Update != core.UpdateUpdating
}

// Frame returns ic with the bar faded for breathing step i.
func Frame(ic Icon, i int) Icon {
	phase := 2 * math.Pi * float64(((i%BreathSteps)+BreathSteps)%BreathSteps) / BreathSteps
	ic.Dim = uint8(math.Round(breathDepth * (1 - math.Cos(phase)) / 2))
	return ic
}

// ToolTip is "Lumilo — <Engine> <state>" in the language loc.
func ToolTip(v core.View, loc string) string {
	m := format.Messages(loc)
	var state string
	switch {
	case v.Update == core.UpdateUpdating:
		state = m.PillUpdating()
	default:
		switch v.Phase {
		case core.PhaseStopped:
			state = m.PillStopped()
		case core.PhaseLoading, core.PhaseRestoring:
			state = m.PillLoading()
		case core.PhaseReady:
			state = m.PillReady()
		case core.PhaseGenerating:
			state = m.PillGenerating()
		case core.PhaseReleased:
			state = m.PillReleased()
		case core.PhaseError:
			state = m.PillError()
		case core.PhaseInstallFailed:
			state = m.PillInstallFailed()
		case core.PhaseExternal:
			state = m.PillExternal()
		case core.PhaseUnresponsive:
			state = m.PillUnresponsive()
		case core.PhaseStopping:
			state = m.PillStopping()
		case core.PhaseNotInstalled:
			state = m.PillNotInstalled()
		case core.PhaseNoInstaller:
			state = m.PillNoBrew()
		case core.PhaseInstalling:
			state = m.PillInstalling()
		}
	}
	tip := "Lumilo"
	if v.Engine != "" {
		tip += " — " + v.Engine
	}
	if state != "" {
		tip += " " + state
	}
	return tip
}
