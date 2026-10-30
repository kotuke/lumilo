package trayicon

import (
	"math"
	"time"

	"github.com/kotuke/lumilo/internal/core"
)

const (
	// SpeedTick is how often Speed steps while a speed shows: ten frames
	// a second.
	SpeedTick = 100 * time.Millisecond
	// SpeedHold is how long the icon keeps the last speed after the
	// server stops generating. An agent works in turns: between two
	// answers it runs its tools (a shell command, codemode) and the server
	// idles, so without a hold the icon would flip to memory and back.
	SpeedHold = 15 * time.Second
	// speedEase is the time constant of the easing: the number covers 86%
	// of a change in two of them, about a second.
	speedEase = 500 * time.Millisecond
)

// Speed is the speed the menu-bar icon shows: the live speed of the
// generation (core.Speed.Current, itself over the last three seconds),
// eased toward at SpeedTick when Ease is set, and held for SpeedHold
// after the generation stops. The zero value shows none. Not safe for
// concurrent use: the app keeps it on the main thread.
type Speed struct {
	// Ease: move the shown number toward the live speed in steps (the
	// user wants the icon animated and the system does not ask for less
	// motion); else it jumps.
	Ease bool

	target, shown float64
	last          float64   // the last live speed seen
	held          time.Time // the hold ends then; zero = none
	at            time.Time // the last Step
}

// Target sets the speed to show from the view v at now: the live speed
// while the server generates; the last one while a request is in progress
// without a speed of its own yet (the server reads a long prompt: 40 s for
// 25,000 tokens of Qwen3.8-27B with nothing cached), and until the hold
// ends while it is ready or answers outside Lumilo; else none (the icon
// shows memory).
func (s *Speed) Target(v core.View, now time.Time) {
	switch v.Phase {
	case core.PhaseGenerating:
		if cur := v.Speed.Current; cur > 0 {
			s.last, s.held = cur, now.Add(SpeedHold)
			s.set(cur)
			return
		}
		if s.last > 0 && v.Snapshot.Active > 0 {
			s.held = now.Add(SpeedHold) // the hold starts over when it ends
			s.set(s.last)
			return
		}
		fallthrough
	case core.PhaseReady, core.PhaseExternal:
		if !s.held.IsZero() && now.Before(s.held) {
			s.set(s.last)
			return
		}
	}
	s.target, s.shown, s.held = 0, 0, time.Time{}
}

// set aims at t; the first speed after none, or any without easing, shows
// at once.
func (s *Speed) set(t float64) {
	s.target = t
	if !s.Ease || s.shown <= 0 {
		s.shown = t
	}
}

// Step moves the shown speed toward the target by the time since the last
// step, exponentially with time constant speedEase.
func (s *Speed) Step(now time.Time) {
	dt := now.Sub(s.at)
	s.at = now
	if !s.Ease || s.shown <= 0 || s.target <= 0 || dt <= 0 || dt > time.Second {
		s.shown = s.target
		return
	}
	s.shown += (s.target - s.shown) * (1 - math.Exp(-dt.Seconds()/speedEase.Seconds()))
}

// Shown is the speed the icon shows now; 0 for none.
func (s *Speed) Shown() float64 { return s.shown }

// Active reports whether a speed shows, so the ticker has work: easing
// toward it, or ending the hold.
func (s *Speed) Active() bool { return s.target > 0 }
