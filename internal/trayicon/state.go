package trayicon

import (
	"math"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
)

// BreathSteps frames make one breathing cycle; BreathInterval is the time
// between frames. A cycle takes two seconds, as in the mockup.
const (
	BreathSteps    = 8
	BreathInterval = 250 * time.Millisecond
	breathDepth    = 55 // the mark fades to 45 % opacity
)

// For maps the supervisor's view to an icon. loc is the app locale
// (mygo.App.Locale()); tps is the speed to show (Speed.Shown), 0 for none.
//
// The mark is the engine's logo (SetLogo), Lumilo's spark in a ring when
// the logo cannot be read; a state shows as its opacity and a badge:
//
//	state                                       logo                      Lumilo's mark       value
//	stopped, stopping                           faded                     spark, faded        —
//	loading, installing, updating, restoring    faded; pulses (Frame)     loader              —
//	ready, external                             full                      spark               speed "41 T/S" while tps > 0 (held), else memory "17 GB"
//	generating                                  full; breathes (Frame)    spark, breathing    speed "41 T/S"; memory until a speed is known
//	released                                    faded, pause badge        hollow spark        —
//	unresponsive, error, install failed         full, "!" badge           alert               —
//	not installed, no Homebrew                  faded, download badge     download, faded     —
//
// An update that is running overrides the state: loading look, no text.
func For(v core.View, loc string, tps float64) (ic Icon) {
	SetLogo(v.Engine, v.Logo)
	ic.Engine = v.Engine
	if v.Update == core.UpdateUpdating {
		ic.Mark, ic.Faded = MarkLoader, true
		return ic
	}
	switch v.Phase {
	case core.PhaseStopped, core.PhaseStopping:
		ic.Faded = true
	case core.PhaseLoading, core.PhaseInstalling, core.PhaseRestoring:
		ic.Mark, ic.Faded = MarkLoader, true
	case core.PhaseReady, core.PhaseExternal, core.PhaseGenerating:
		ic.Text = format.TrayGB(loc, v.Snapshot.MemoryUsed)
		if tps > 0 {
			ic.Text = format.TrayTPS(loc, tps)
		}
	case core.PhaseReleased:
		ic.Mark, ic.Faded = MarkHollow, true
	case core.PhaseUnresponsive, core.PhaseError, core.PhaseInstallFailed:
		ic.Mark = MarkAlert
	case core.PhaseNotInstalled, core.PhaseNoInstaller:
		ic.Mark, ic.Faded = MarkDownload, true
	}
	return ic
}

// Breathing reports whether the mark should breathe in this view: while
// the engine answers, and while it loads (the loading logo pulses).
func Breathing(v core.View) bool {
	if v.Update == core.UpdateUpdating {
		return true
	}
	switch v.Phase {
	case core.PhaseGenerating, core.PhaseLoading, core.PhaseInstalling, core.PhaseRestoring:
		return true
	}
	return false
}

// Frame returns ic faded for breathing step i. A loading logo, faded when
// still, pulses from full instead.
func Frame(ic Icon, i int) Icon {
	phase := 2 * math.Pi * float64(((i%BreathSteps)+BreathSteps)%BreathSteps) / BreathSteps
	ic.Dim = uint8(math.Round(breathDepth * (1 - math.Cos(phase)) / 2))
	if ic.Mark == MarkLoader {
		ic.Faded = false
	}
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
