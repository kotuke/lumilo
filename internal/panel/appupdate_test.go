package panel

import (
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/appupdate"
	"github.com/kotuke/lumilo/internal/core"
)

// appBench opens Settings with Lumilo's update in state st.
func appBench(t *testing.T, phase core.Phase, st appupdate.State, release bool) *bench {
	b := newBench(t, "en")
	b.p.SetAppInfo("0.1.0", release)
	b.p.SetAppUpdate(st)
	b.show(withPhase(phase), time.Hour)
	b.click("Settings")
	return b
}

func TestAppUpdateDisabled(t *testing.T) {
	b := appBench(t, core.PhaseStopped, appupdate.State{}, true)
	b.want("Lumilo", "0.1.0", b.m.AppUpdOff())
	b.none(b.m.AppUpdAuto())
}

func TestAppUpdateCheck(t *testing.T) {
	b := appBench(t, core.PhaseStopped, appupdate.State{Enabled: true}, true)
	b.want("Lumilo", "0.1.0", b.m.AppUpdAuto())
	// The engine's row comes first, in the engine card: Lumilo's button
	// is at the end of its own row.
	b.clickEnd("Lumilo")
	b.called("CheckAppUpdate")
	b.notCalled("CheckUpdate")
	b.p.SetAppUpdate(appupdate.State{Enabled: true, Check: appupdate.CheckUpToDate, CheckSeq: 1})
	b.tt.Frame()
	b.want("You have the latest version, 0.1.0")
}

func TestAppUpdateLocalBuildHasNoSwitch(t *testing.T) {
	b := appBench(t, core.PhaseStopped, appupdate.State{Enabled: true}, false)
	b.none(b.m.AppUpdAuto())
}

func TestAppUpdateAvailableInstalls(t *testing.T) {
	b := appBench(t, core.PhaseStopped, appupdate.State{Enabled: true, Phase: appupdate.PhaseAvailable, Version: "0.2.0"}, false)
	b.want("Version 0.2.0 is available")
	b.click("Update")
	b.called("DownloadAppUpdate")
}

// checks counts the texts of the "Check for updates" buttons shown.
func (b *bench) checks() int {
	n := 0
	for _, s := range b.tt.Texts() {
		if s == "Check for updates" {
			n++
		}
	}
	return n
}

func TestAppUpdateDownloading(t *testing.T) {
	idle := appBench(t, core.PhaseStopped, appupdate.State{Enabled: true}, true).checks()
	b := appBench(t, core.PhaseStopped, appupdate.State{Enabled: true, Phase: appupdate.PhaseDownloading,
		Version: "0.2.0", Done: 3_000_000, Total: 8_000_000}, true)
	b.want("Downloading version 0.2.0 · 3 of 8 MB")
	// Only the engine's row checks; Lumilo's has no button meanwhile.
	if n := b.checks(); n >= idle {
		t.Errorf("%d check texts while downloading, %d when idle", n, idle)
	}
	b.none(b.m.AppUpdRestart())
}

func TestAppUpdateReadyRestartsAtOnceWhenIdle(t *testing.T) {
	b := appBench(t, core.PhaseStopped, appupdate.State{Enabled: true, Phase: appupdate.PhaseReady, Version: "0.2.0"}, true)
	b.want(b.m.AppUpdReady("0.2.0"))
	b.click(b.m.AppUpdRestart())
	b.called("RestartToUpdate")
}

func TestAppUpdateRestartAsksWhileServing(t *testing.T) {
	b := appBench(t, core.PhaseReady, appupdate.State{Enabled: true, Phase: appupdate.PhaseReady, Version: "0.2.0"}, true)
	b.click(b.m.AppUpdRestart())
	b.notCalled("RestartToUpdate")
	b.want(b.m.RestartUpdTitle(), b.m.RestartUpdNote())
	b.clickNext("Cancel")
	b.called("RestartToUpdate")
}

func TestAppUpdateReadyMarksSettingsButton(t *testing.T) {
	dot := func(st appupdate.State) int {
		b := newBench(t, "en")
		b.p.SetAppUpdate(st)
		b.show(withPhase(core.PhaseStopped), time.Hour)
		r, ok := b.tt.Find("Settings")
		if !ok {
			t.Fatal("no Settings button")
		}
		// The corner of the button, where the dot goes, against the ok colour.
		img, n := b.tt.Image(), 0
		for y := int(r.Y); y < int(r.Y)+8; y++ {
			for x := int(r.X + r.W - 8); x < int(r.X+r.W); x++ {
				if dist(pixel(img, x, y), b.palette().ok) < 60 {
					n++
				}
			}
		}
		return n
	}
	if n := dot(appupdate.State{Enabled: true}); n > 0 {
		t.Errorf("a dot without an update: %d pixels", n)
	}
	if n := dot(appupdate.State{Enabled: true, Phase: appupdate.PhaseReady, Version: "0.2.0"}); n < 6 {
		t.Errorf("no dot while the update waits: %d pixels", n)
	}
}

func TestAppUpdateRestartWaitsForEngineUpdate(t *testing.T) {
	b := newBench(t, "en")
	b.p.SetAppUpdate(appupdate.State{Enabled: true, Phase: appupdate.PhaseReady, Version: "0.2.0"})
	v := withPhase(core.PhaseReady)
	v.Update = core.UpdateUpdating
	b.show(v, time.Hour)
	b.click("Settings")
	b.click(b.m.AppUpdRestart())
	b.want(b.m.QuitUpdTitle("Splash"), b.m.RestartUpdLater())
	b.click(b.m.AppUpdRestartLater())
	b.notCalled("RestartToUpdate")
	v.Update = core.UpdateNone
	b.show(v, time.Second)
	b.called("RestartToUpdate")
}
