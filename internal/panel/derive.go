package panel

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
)

// hangAfter is how long a ready server is silent before the supervisor
// calls it unresponsive (core.hangAfter); "No response for" counts from
// then.
const hangAfter = 12 * time.Second

type anim int

const (
	animNone anim = iota
	animSpin
	animPulse
)

type mainKind int

const (
	mainNone mainKind = iota
	mainStart
	mainStartOn
	mainStop
	mainRestart
	mainKill
	mainStopping
)

type errKind int

const (
	errPort errKind = iota
	errModel
	errMemory
	errExit
	errNoKey    // the network access has no API key to serve with
	errNoVision // the model's image input cannot be prepared: serve text only
)

// mainState is the main screen worked out from the View, as the mockup's
// renderVals works out its `s`: which blocks show and how they look.
type mainState struct {
	phase core.Phase

	pill, sub             string
	pillText, pillBg, dot ui.Color
	pillIcon              string
	pillAnim              anim
	pillFlip              bool // mirrored in RTL
	statusOnly            bool // install states: the status is read, not shown
	served                bool
	isInstall             bool
	isNotInstalled        bool
	isNoInstaller         bool
	isInstalling          bool
	isInstallFailed       bool
	isLoading, isError    bool
	isExternal, isHang    bool
	updAvail, isUpdating  bool
	updFailed             bool
	warnOn                bool
	warnText              string
	isProgress            bool
	progressText, cmd     string
	progressLeft          string // the time left of a download, "~6 min" (progressText)
	showHead, showPill    bool
	hasModel, showChat    bool
	chatOff               bool
	hasControls           bool
	showAgents            bool
	modelCanOpen          bool
	modelTitle            string
	canRelease, canLoad   bool
	restoring             bool // weights are coming back: Load shows, off
	idle                  string
	agentsOff             bool
	agentTitle            string
	main                  mainKind
	showMain              bool
	act                   string
	actBg, actText        ui.Color
	quit                  dialog
	err                   errKind
	addrLock              bool
	suggest               bool // no models: the suggestions replace the model button
	downloading           bool
}

// modelCanOpen reports whether the model list opens: only where the model
// can be changed.
func modelCanOpen(v core.View) bool {
	if v.Update == core.UpdateUpdating || len(v.Models) == 0 {
		return false
	}
	switch v.Phase {
	case core.PhaseStopped, core.PhaseReady, core.PhaseGenerating, core.PhaseReleased, core.PhaseError:
		return true
	}
	return false
}

// suggesting reports whether the engine has no model at all and nothing is
// being downloaded: the server card offers models to download instead of
// the model button and Start.
func (p *Panel) suggesting() bool {
	v := p.view
	if len(v.Models) > 0 || v.Download != "" || v.Update == core.UpdateUpdating {
		return false
	}
	return v.Phase == core.PhaseStopped || v.Phase == core.PhaseError
}

func isInstallPhase(ph core.Phase) bool {
	switch ph {
	case core.PhaseNotInstalled, core.PhaseNoInstaller, core.PhaseInstalling, core.PhaseInstallFailed:
		return true
	}
	return false
}

// failKind maps the engine's failure to the mockup's error kinds.
func failKind(k engine.FailureKind) errKind {
	switch k {
	case engine.FailPortBusy, engine.FailAlreadyServing:
		return errPort
	case engine.FailModel:
		return errModel
	case engine.FailMemory:
		return errMemory
	case engine.FailNoKey:
		return errNoKey
	case engine.FailNoVision:
		return errNoVision
	}
	return errExit
}

// downloadTotal is the size of the download in bytes; 0 = not known.
func (p *Panel) downloadTotal() int64 {
	v := p.view
	total := p.dlTotal[v.Download]
	for _, sg := range v.Suggestions {
		if sg.ID == v.Download {
			total = sg.DownloadBytes
		}
	}
	if total <= 0 {
		total = v.DownloadTotal
	}
	return total
}

// downloadPercent is how much of the download is fetched, in percent;
// -1 = not known: the size is not, or no byte arrived yet.
func (p *Panel) downloadPercent() float64 {
	total, done := p.downloadTotal(), p.view.DownloadBytes
	if total <= 0 || done <= 0 {
		return -1
	}
	return min(float64(done)/float64(total)*100, 100)
}

// downloadText is the progress of a model download: "6.1 of 21.2 GB ·
// 45 MB/s" and the time left, "~6 min", while View.DownloadBytes grows
// toward a known size, else the size alone and no time.
//
// The size is the suggestion's, or the variant's the field took
// (dlTotal), else what the engine said it fetches (View.DownloadTotal).
// The speed is that of DownloadBytes, the bytes fetched so far, between
// the Views that changed it.
func (p *Panel) downloadText(f *frame) (text, left string) {
	return p.progressText(f, p.downloadTotal(), p.view.DownloadBytes)
}

// progressText is downloadText of a download of total bytes (0 = not
// known) that fetched done, at the speed the panel measured (dlRate).
// left is "" until the speed is known.
func (p *Panel) progressText(f *frame, total, done int64) (text, left string) {
	m := f.m
	if total > 0 && done > 0 && done < total {
		of := m.UsedOf(format.Number(f.loc, float64(done)/1e9, 1), format.GB(f.loc, total))
		if p.dlRate <= 0 {
			return of, "" // the speed is not known yet
		}
		d := time.Duration(float64(total-done) / p.dlRate * float64(time.Second))
		// A countdown rounds up: "~2 min" while more than one minute is left.
		if d >= time.Minute {
			d = (d + time.Minute - 1).Truncate(time.Minute)
		}
		return of + " · " + m.DlSpeed(format.MB(f.loc, int64(p.dlRate))), m.DlLeft(format.ShortDuration(f.loc, d))
	}
	switch {
	case total > 0:
		return m.DownloadingSize(format.GB(f.loc, total)), ""
	case done > 0:
		return m.DownloadingSize(format.GB(f.loc, done)), ""
	}
	return m.Downloading(), ""
}

// progressLine is a download's progress text and its time left on one
// line, " · " between them: the text gives way (an ellipsis) before the
// time does, so the time left always shows.
func progressLine(c *ui.Context, f *frame, text, left string, size float32) {
	t := f.pal
	ui.Row(c).MinWidth(0).Role(ui.RoleStatus).Label(joinLeft(text, left)).Children(func() {
		txt(c, text, size).TextColor(t.muted).SingleLine().Shrink(1).MinWidth(0)
		if left != "" {
			txt(c, " · "+left, size).TextColor(t.muted).NoWrap().Shrink(0)
		}
	})
}

// joinLeft is a progress text with its time left, as one string.
func joinLeft(text, left string) string {
	if left == "" {
		return text
	}
	return text + " · " + left
}

func (p *Panel) derive(c *ui.Context, f *frame) mainState {
	v, t, m := p.view, f.pal, f.m
	dur := func(d time.Duration) string { return format.Duration(f.loc, d) }
	// The subtitle is one line beside five buttons: its durations are short
	// ("2 h 14 min"); the notes in the cards have room for the words.
	short := func(d time.Duration) string { return format.ShortDuration(f.loc, d) }
	since := func(at time.Time) string {
		if at.IsZero() {
			return short(0)
		}
		return short(f.now.Sub(at))
	}
	neutral := func(s *mainState) { s.actBg, s.actText = t.chip, t.text }
	start := func(s *mainState) { s.actBg, s.actText = t.startBg, t.startText }

	s := mainState{phase: v.Phase, err: failKind(v.Failure.Kind)}
	switch v.Phase {
	case core.PhaseStopped:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon = m.PillStopped(), t.muted, t.idleBg, t.muted, "square"
		s.sub = m.SubStopped()
		s.main, s.act = mainStart, m.ActStart()
		start(&s)
	case core.PhaseLoading:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon, s.pillAnim = m.PillLoading(), t.warnText, t.warnBg, t.warnBar, "loader-2", animSpin
		s.sub = m.SubLoading(since(p.phaseAt))
		s.main, s.act, s.isLoading = mainStop, m.ActCancel(), true
		neutral(&s)
	case core.PhaseReady:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon = m.PillReady(), t.okText, t.okBg, t.okBar, "circle-check"
		s.sub = m.SubReady(since(p.servedAt))
		s.main, s.act, s.served, s.canRelease = mainStop, m.ActStop(), true, v.Caps.Release
		if left := v.IdleLeft - f.now.Sub(p.viewAt); v.IdleLeft > 0 && left > 0 {
			if left >= time.Minute { // a countdown reads in whole minutes, rounded up
				left = (left + time.Minute - 1).Truncate(time.Minute)
			}
			s.idle = m.IdleNote(dur(left))
		}
		neutral(&s)
	case core.PhaseGenerating:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon, s.pillAnim = m.PillGenerating(), t.okText, t.okBg, t.okBar, "message-dots", animPulse
		s.pillFlip = true
		s.sub = m.SubGenerating(since(p.servedAt))
		s.main, s.act, s.served = mainStop, m.ActStop(), true
		neutral(&s)
	case core.PhaseReleased:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon = m.PillReleased(), t.soft, t.idleBg, t.muted, "zzz"
		s.sub = m.SubReleased(since(p.servedAt))
		s.main, s.act, s.served, s.canLoad = mainStop, m.ActStop(), true, true
		neutral(&s)
	case core.PhaseRestoring:
		// The weights are coming back: the server is not ready yet. The
		// memory card stays, with Load off, while the pill loads.
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon, s.pillAnim = m.PillLoading(), t.warnText, t.warnBg, t.warnBar, "loader-2", animSpin
		s.sub = m.SubReady(since(p.servedAt))
		s.main, s.act, s.served, s.restoring = mainStop, m.ActStop(), true, true
		neutral(&s)
	case core.PhaseError:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon = m.PillError(), t.err, t.errBg, t.errBar, "alert-circle"
		s.sub = m.SubError(since(p.phaseAt))
		s.isError, s.agentsOff = true, true
		if s.err == errPort && v.FreePort > 0 {
			s.main, s.act = mainStartOn, m.ActStartOn(v.FreePort)
			start(&s)
		} else {
			s.main, s.act = mainRestart, m.ActRestart()
			neutral(&s)
		}
	case core.PhaseExternal:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon = m.PillExternal(), t.okText, t.okBg, t.okBar, "external-link"
		if v.Snapshot.Released {
			// Asleep: the weights are out of memory, as our own Unloaded.
			s.pill, s.pillText, s.pillBg, s.dot = m.PillReleased(), t.soft, t.idleBg, t.muted
		}
		s.pillFlip = true
		// The short form leaves room for the uptime beside the buttons in
		// every language.
		s.sub = m.SubExternalShort(since(p.phaseAt))
		s.main, s.act, s.served, s.isExternal = mainKill, m.ActKill(), true, true
		// The mockup offers Unload on a foreign server too (A20), and the
		// supervisor can release its weights.
		s.canRelease, s.canLoad = !v.Snapshot.Released && v.Caps.Release, v.Snapshot.Released
		neutral(&s)
	case core.PhaseUnresponsive:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon = m.PillUnresponsive(), t.warnText, t.warnBg, t.warnBar, "alert-triangle"
		s.sub = m.SubUnresponsive(short(f.now.Sub(p.phaseAt) + hangAfter))
		s.isHang, s.agentsOff = true, true
	case core.PhaseStopping:
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon = m.PillStopping(), t.soft, t.idleBg, t.muted, "clock"
		s.sub = m.SubStopping()
		s.main, s.act, s.actBg, s.actText = mainStopping, m.ActStopping(), t.chip, t.muted
		s.agentsOff = true
	case core.PhaseNotInstalled:
		s.pill, s.pillIcon = m.PillNotInstalled(), "download"
		s.sub = m.SubNotInstalled(v.Engine)
		s.isInstall, s.isNotInstalled = true, true
	case core.PhaseNoInstaller:
		s.pill, s.pillIcon = m.PillNoBrew(), "package"
		s.sub = m.SubNoBrew(v.Engine)
		s.isInstall, s.isNoInstaller = true, true
	case core.PhaseInstalling:
		s.pill, s.pillIcon = m.PillInstalling(), "loader-2"
		s.sub = strings.TrimSpace(m.SubInstalling(v.Engine, v.Latest))
		s.isInstall, s.isInstalling = true, true
	case core.PhaseInstallFailed:
		s.pill, s.pillIcon = m.PillInstallFailed(), "alert-circle"
		s.sub = m.SubNotInstalled(v.Engine)
		s.isInstall, s.isInstallFailed = true, true
	}
	if s.isInstall {
		s.agentsOff = true
		s.statusOnly = true
	}

	// The update runs over any server state, except during an install.
	upd := v.Update
	if s.isInstall {
		upd = core.UpdateNone
	}
	s.updAvail = upd == core.UpdateAvailable && v.Latest != ""
	s.isUpdating = upd == core.UpdateUpdating
	s.updFailed = upd == core.UpdateFailed
	if s.isUpdating {
		s.pill, s.pillText, s.pillBg, s.dot, s.pillIcon, s.pillAnim = m.PillUpdating(), t.warnText, t.warnBg, t.warnBar, "loader-2", animSpin
		s.pillFlip = false
		s.sub = strings.TrimSpace(m.SubUpdating(v.Engine, v.Latest))
		s.isLoading, s.isError, s.isExternal, s.isHang = false, false, false, false
		s.canRelease, s.canLoad, s.served, s.idle = false, false, false, ""
		s.main = mainNone
	}

	stopLong := v.Phase == core.PhaseStopping && v.StoppingLong && !s.isUpdating
	s.warnOn = s.isHang || stopLong
	if stopLong {
		s.warnText = m.StopLong()
	} else if s.isHang {
		s.warnText = m.HangNote(dur(f.now.Sub(p.phaseAt) + hangAfter))
	}

	s.isProgress = s.isLoading || s.isUpdating || s.isInstalling
	switch {
	case s.isUpdating:
		s.progressText = m.UpdText()
		s.cmd = v.Install.UpgradeCommand
	case s.isInstalling:
		s.progressText = m.InstProgress(v.Engine)
		s.cmd = v.Install.InstallCommand
	default:
		s.progressText = m.LoadingText()
		if v.Downloading {
			s.downloading = true
			s.pill = m.PillDownloading()
			s.progressText, s.progressLeft = p.downloadText(f)
		}
	}

	s.showHead = !(s.isInstalling || s.isInstallFailed)
	s.showPill = !s.isInstall
	s.suggest = !s.isInstall && !s.isUpdating && p.suggesting()
	s.hasModel = !s.isInstall && !s.suggest
	s.showChat = !s.isInstall && v.Caps.Chat
	s.chatOff = !s.served
	s.hasControls = !s.isInstall && !s.isUpdating && !s.suggest
	s.showAgents = !s.isInstall && !s.isUpdating && !s.suggest && len(v.Agents) > 0
	// The model-updates chip toggles a panel setting; it says nothing of a
	// foreign server.
	// A download runs online; an engine that never contacts the hub
	// (Caps.ModelUpdates off) has no use for it.
	s.modelCanOpen = s.hasModel && modelCanOpen(v)
	if v.Phase == core.PhaseExternal && !s.isUpdating {
		s.modelTitle = m.ModelLocked()
	}
	s.showMain = s.main != mainNone && !s.isHang && !s.suggest

	switch v.Phase {
	case core.PhaseStopped, core.PhaseReleased:
		s.agentTitle = m.AgentStart()
	case core.PhaseError:
		s.agentTitle = m.AgentOffError()
	case core.PhaseUnresponsive:
		s.agentTitle = m.AgentOffUnresponsive()
	case core.PhaseStopping:
		s.agentTitle = m.AgentOffStopping()
	}

	switch {
	case s.isUpdating:
		s.quit = dlgQuitUpdating
	case v.Phase == core.PhaseGenerating:
		s.quit = dlgQuitBusy
	}
	s.addrLock = !s.isExternal && v.Phase != core.PhaseExternal && v.Access != engine.AccessLocal
	return s
}
