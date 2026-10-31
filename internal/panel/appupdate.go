package panel

import (
	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/appupdate"
	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
)

// appUpdate is what the panel knows of Lumilo's own update.
type appUpdate struct {
	st      appupdate.State
	version string // the running version
	// release: the running bundle is a published release, whose updates
	// may download by themselves (the switch shows).
	release bool
	// The check asked for on the settings screen, as for the engine's.
	asked bool
	seq   int
	// restartLater: restart once the engine's install or update ends
	// (dlgRestartLater).
	restartLater bool
}

// SetAppInfo tells the running version of Lumilo and whether it is a
// published release.
func (p *Panel) SetAppInfo(version string, release bool) {
	p.app.version, p.app.release = version, release
}

// SetAppUpdate shows the state of Lumilo's update. Call it on the UI
// thread.
func (p *Panel) SetAppUpdate(s appupdate.State) { p.app.st = s }

// appReady reports whether a new version of Lumilo waits for a restart.
func (p *Panel) appReady() bool { return p.app.st.Phase == appupdate.PhaseReady }

// restartToUpdate restarts into the new version, after asking when that
// stops our server or a download. An engine being installed or updated is
// never cut off: the restart waits for it to end.
func (p *Panel) restartToUpdate() {
	if p.brewBusy() {
		p.screen = screenMain
		p.openDialog(dlgRestartLater)
		return
	}
	if p.restartStops() {
		p.screen = screenMain
		p.openDialog(dlgRestartUpdate)
		return
	}
	p.act.RestartToUpdate()
}

// brewBusy reports whether Homebrew installs or updates the engine, or an
// engine switch runs: a restart would cut them off.
func (p *Panel) brewBusy() bool {
	v := p.view
	return v.Switching || v.Phase == core.PhaseInstalling || v.Update == core.UpdateUpdating
}

// restartWhenIdle restarts into the new version once the engine's install
// or update the user waited for ends. SetView calls it.
func (p *Panel) restartWhenIdle() {
	if p.app.restartLater && !p.brewBusy() {
		p.app.restartLater = false
		p.act.RestartToUpdate()
	}
}

// restartStops reports whether a restart stops our server or a download.
func (p *Panel) restartStops() bool {
	switch p.view.Phase {
	case core.PhaseLoading, core.PhaseReady, core.PhaseGenerating, core.PhaseReleased, core.PhaseRestoring:
		return true
	}
	for _, j := range p.view.Downloads {
		if j.State == core.DownloadRunning || j.State == core.DownloadQueued {
			return true
		}
	}
	return false
}

func (p *Panel) appChecking() bool {
	a := &p.app
	return a.asked && a.st.CheckSeq == a.seq
}

func (p *Panel) appCheck() {
	p.app.asked, p.app.seq = true, p.app.st.CheckSeq
	p.act.CheckAppUpdate()
}

// appRows are Lumilo's version and update in the App card: the version
// with a button for the next step, a line that says where the update is,
// and the switch of automatic downloads.
func (p *Panel) appRows(c *ui.Context, f *frame, s *settingsState) {
	t, m, a := f.pal, f.m, &p.app
	st := a.st
	// Under the name: the version, or what the update does now.
	sub, tone := a.version, t.muted
	switch {
	case !st.Enabled && a.version != "":
		sub = a.version + " · " + m.AppUpdOff()
	case !st.Enabled:
		sub = m.AppUpdOff()
	case st.Phase == appupdate.PhaseReady:
		sub, tone = m.AppUpdReady(st.Version), t.text
	case st.Phase == appupdate.PhaseDownloading:
		sub = m.AppUpdDownloading(st.Version)
		if st.Total > 0 {
			sub += " · " + m.UsedOf(format.Number(f.loc, float64(st.Done)/1e6, 0), format.MB(f.loc, st.Total))
		}
	case st.Phase == appupdate.PhaseFailed:
		sub, tone = m.AppUpdFailed(st.Version), t.err
	case st.Phase == appupdate.PhaseAvailable:
		sub, tone = m.CheckAvail(st.Version), t.text
	case a.asked && !p.appChecking():
		switch st.Check {
		case appupdate.CheckUpToDate:
			sub = m.CheckLatest(a.version)
		case appupdate.CheckFailed:
			sub, tone = m.CheckFailed(), t.err
		}
	}
	updateRow(c, f, "Lumilo", sub, tone, func() {
		if !st.Enabled {
			return
		}
		switch st.Phase {
		case appupdate.PhaseReady:
			fillBtn(c, f, "app-restart", m.AppUpdRestart(), p.restartToUpdate)
		case appupdate.PhaseAvailable:
			fillBtn(c, f, "app-install", m.UpdBtn(), p.act.DownloadAppUpdate)
		case appupdate.PhaseFailed:
			fillBtn(c, f, "app-retry", m.UpdBtn(), p.act.DownloadAppUpdate)
		case appupdate.PhaseDownloading:
		default:
			checkBtn(c, f, "app-check", m.EngineVersion("Lumilo"), p.appChecking(), p.appCheck)
		}
	})
	if st.Enabled && a.release {
		switchRow(c, f, "app-auto", m.AppUpdAuto(), &s.appAuto)
	}
}

// fillBtn is a small filled capsule button of a settings row.
func fillBtn(c *ui.Context, f *frame, key, label string, fn func()) {
	t := f.pal
	btn(c).Key(key).Height(24).Radius(12).Background(t.fillBg).TextColor(t.fillText).PaddingX(10).Shrink(0).
		Label(label).OnClick(fn).Children(func() { txt(c, label, 12.5).FontWeight(600).NoWrap() })
}
