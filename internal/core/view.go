// Package core runs one engine's server and turns its state into a View
// for the panel and the tray icon. It knows nothing about any particular
// engine: all engine details come through engine.Engine.
package core

import (
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// Phase is the server state the panel shows.
type Phase int

const (
	PhaseStopped      Phase = iota
	PhaseLoading            // our server is loading the model
	PhaseReady              // serving, idle
	PhaseGenerating         // serving, at least one request in progress
	PhaseReleased           // serving, weights unloaded
	PhaseRestoring          // serving, weights being loaded back: not ready yet
	PhaseError              // our server exited or failed to start
	PhaseExternal           // a live server that Lumilo did not start
	PhaseUnresponsive       // our server is alive but stopped answering
	PhaseStopping           // our server is shutting down
	PhaseNotInstalled       // the engine is not installed, its installer is ready
	// PhaseNoInstaller: neither the engine nor its installer's
	// prerequisite (engine.InstallInfo.Prerequisite, e.g. Homebrew) is
	// installed; the user installs the prerequisite first.
	PhaseNoInstaller
	PhaseInstalling    // the installer is installing the engine
	PhaseInstallFailed // the install failed
)

// Served reports whether a server answers requests in this phase.
func (p Phase) Served() bool {
	return p == PhaseReady || p == PhaseGenerating || p == PhaseReleased || p == PhaseRestoring || p == PhaseExternal
}

// UpdatePhase is the state of an engine update, shown over any phase.
type UpdatePhase int

const (
	UpdateNone      UpdatePhase = iota
	UpdateAvailable             // a newer version exists
	UpdateUpdating              // the installer is upgrading the engine
	UpdateFailed                // the last update failed; the old version still runs
)

// UpdateReason says why an update failed when the installer did not:
// View.UpdateError then holds its last line, which is no error.
type UpdateReason int

const (
	UpdateReasonNone UpdateReason = iota // the installer failed: see View.UpdateError
	// UpdateReasonNoNewer: the installer ran fine and the installed version
	// did not change. It has no newer version yet: a Homebrew formula lags
	// behind the release feed.
	UpdateReasonNoNewer
)

// NoticeKind is what a Notice reports.
type NoticeKind int

const (
	NoticeNone NoticeKind = iota
	// NoticeAgentUnavailable: RunAgent found no server to point the agent
	// at: ours is stopping, not answering, failed to start, or an update
	// runs. Text is "".
	NoticeAgentUnavailable
	// NoticeTerminalFailed: the terminal did not open the agent. Text is
	// the error, in English, for the log or a detail line.
	NoticeTerminalFailed
	// NoticeKeyNotStored: the API key the agent needs is not in the
	// Keychain (it could not be written), so the agent was not started.
	// Text is the error.
	NoticeKeyNotStored
	// NoticeRemoveFailed: a model was not deleted (Supervisor.RemoveModel):
	// it could not be then, or the engine failed. Text is why, in English.
	NoticeRemoveFailed
)

// Notice is a one-off message about a command that did not do what was
// asked, for the panel to show for a moment. Seq grows with every new
// notice, so the panel can tell a new one from the one it showed.
type Notice struct {
	Kind NoticeKind
	Text string
	Seq  int
}

// CheckResult is how the last update check the user asked for ended.
type CheckResult int

const (
	CheckNone      CheckResult = iota // no check asked for yet, or one runs
	CheckUpToDate                     // the installed version is the newest
	CheckAvailable                    // a newer version exists: View.Latest
	CheckFailed                       // the feed did not answer, or not with a version
)

// View is everything the panel and the tray icon draw. It is a value:
// the supervisor publishes a fresh copy on every change.
type View struct {
	Engine   string // engine display name, e.g. "Splash"
	EngineID string
	Logo     engine.Logo
	Caps     engine.Caps
	// Install says how the engine is installed: method, size, commands,
	// prerequisite (engine.Installer.Describe).
	Install engine.InstallInfo
	// NotesURL is the page about Latest, "" when no newer version is known.
	NotesURL string
	// Engines are all the engines to choose from, the running one among
	// them; Switching is set while a switch stops our server.
	Engines   []EngineInfo
	Switching bool
	// Suggestions are models to offer for download (engine.Suggestions).
	Suggestions []engine.Suggestion
	// DownloadApart: the engine downloads models apart from the server
	// (engine.Downloader, Supervisor.DownloadModel): Downloads, not
	// Download, report them.
	DownloadApart bool
	// Download is the model a download run fetches and loads (Supervisor
	// .Download), "" when none. Downloading is set while it fetches files
	// (the run is loading and the model is not loading yet).
	// DownloadTotal is what the engine said it fetches, 0 when unknown;
	// DownloadBytes is what it fetched so far (engine.Engine.WatchDownload),
	// 0 when unknown.
	//
	// That is the download of an engine without engine.Downloader; the
	// others download apart from the server: Downloads.
	Download      string
	Downloading   bool
	DownloadTotal int64
	DownloadBytes int64
	// Downloads are the download jobs (Supervisor.DownloadModel), in the
	// order they began: the running one, failed ones and finished ones
	// until dismissed.
	Downloads []DownloadJob
	// CanRemove: the engine deletes installed models (engine.Remover,
	// Supervisor.RemoveModel). Removal is the deletion being counted or
	// run.
	CanRemove bool
	Removal   Removal

	Version     string // installed version, e.g. "1.4.0"
	Latest      string // newest known version; "" when unknown
	Phase       Phase
	Update      UpdatePhase
	UpdateError string // last line of a failed installer run (update or install)
	// UpdateReason says why the update failed when brew did not (Update is
	// UpdateFailed): e.g. Homebrew has no newer version yet.
	UpdateReason UpdateReason
	// Notice is the last notice, e.g. an agent that could not start; see
	// Notice.Seq.
	Notice Notice

	// Checking is set while an update check the user asked for runs
	// (Supervisor.CheckUpdate); the daily check does not set it.
	Checking bool
	// Check is the result of the last check the user asked for, with the
	// version found in Latest; the daily check leaves it alone.
	Check CheckResult
	// CheckSeq counts the checks the user asked for, so the panel can tell
	// the result of its own check from an older one.
	CheckSeq int

	Failure engine.Failure // why the server stopped (PhaseError)
	// FreePort is a free port to offer when Failure.Kind is FailPortBusy.
	FreePort int

	Model  engine.Model   // the selected model
	Models []engine.Model // models the user can pick

	Port     int           // the port the server uses (or will use)
	Address  string        // "host:port" shown in the address chip
	Access   engine.Access // of our server; AccessLocal for an external one
	External *engine.Running

	Snapshot engine.Snapshot // the last probe; zero when not served
	Speed    Speed

	// IdleLeft is the time until the server unloads the weights for being
	// idle; 0 when not counting down.
	IdleLeft time.Duration
	// StoppingLong is set when a stop has taken longer than expected.
	StoppingLong bool
	// LastLine is the last line of the server output, for error blocks.
	LastLine string
	// LogPath is the file with the server's output.
	LogPath string
	// ChatURL is the engine's chat page for the running server, or "".
	ChatURL string
	// Agents are the agents the panel offers.
	Agents []engine.Agent
	// PendingRestart is set when our running server uses other settings
	// than the current ones; Restart applies them.
	PendingRestart bool
}

// EngineInfo is one engine of the engine list.
type EngineInfo struct {
	ID, Name  string
	Logo      engine.Logo
	Installed bool
	Version   string // "" when not installed
	// Install says how the engine is installed, for a switch to one that
	// is not: its size, its prerequisite.
	Install engine.InstallInfo
}

// Describe fills the parts of v that come from e alone: name, logo,
// capabilities, install, agents, suggestions. The supervisor builds its
// views with it; tests of the panel use it for views of their own.
func Describe(v *View, e engine.Engine) {
	info := e.Info()
	v.Engine, v.EngineID = info.Name, info.ID
	v.Logo, v.Caps = info.Logo, info.Caps
	v.Install = e.Installer().Describe()
	v.Agents = info.Agents
	v.Suggestions = info.Suggestions
	_, v.DownloadApart = e.(engine.Downloader)
	_, v.CanRemove = e.(engine.Remover)
}

// Speed is the generation speed the panel and the tray show.
type Speed struct {
	// History holds tokens per second of the last finished requests,
	// oldest first, at most 20.
	History []float64
	// Average is the mean of the last 10 entries of History; 0 when empty.
	Average float64
	// Current is the live speed of the request in progress; 0 when none.
	Current float64
	// FirstToken is the typical time to the first token.
	FirstToken time.Duration
	// Read is the prompt reading speed, tokens per second.
	Read float64
	// LastRequest is when the last request finished; zero when unknown.
	LastRequest time.Time
}
