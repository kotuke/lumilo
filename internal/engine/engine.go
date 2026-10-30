// Package engine defines what Lumilo needs from a local LLM engine.
//
// The panel, the tray icon and the supervisor only talk to these interfaces
// (Engine and its Installer). Everything engine-specific — command lines,
// the install method, lock files, log formats, metric names, error texts,
// agents — lives in an implementation such as internal/engines/splash;
// what implementations share is in internal/engines/enginekit.
package engine

import (
	"context"
	"io"
	"syscall"
	"time"
)

// Engine is one local LLM server program, for example Splash or llama.cpp.
// Methods must be safe to call from any goroutine. Methods that take a
// context may block on the network or the file system; the others must not.
type Engine interface {
	// Info is what the engine is: its name, logo, capabilities, models to
	// suggest and agents. It never changes.
	Info() Info
	// Installer installs and updates the engine. Lumilo installs and
	// updates engines only through it.
	Installer() Installer

	// Models lists the models installed locally that the engine can serve.
	Models(ctx context.Context) ([]Model, error)
	// ParseModel checks a model name the user typed to download, in the
	// engine's syntax (e.g. "owner/repo:VARIANT"), and splits it for
	// display. The Model's ID is what Config.Model takes for the download.
	ParseModel(spec string) (Model, error)

	// ServeCommand builds the server command for cfg. env is the complete
	// environment of the child. Secrets go into env, never into argv.
	// argv[0] is an absolute path. cfg.Model is a Model.ID from Models or
	// from ParseModel; with ModelUpdates on, the server downloads a model
	// it does not have yet.
	ServeCommand(cfg Config) (argv []string, env []string)
	// StopPolicy says how to stop the server's process group.
	StopPolicy() StopPolicy
	// ParseLine turns one line of the server's output into an event.
	// ok is false for lines that mean nothing to the supervisor.
	ParseLine(line string, stderr bool) (ev Event, ok bool)
	// ClassifyFailure explains why the server exited (or failed to start)
	// from its exit code and the last lines of its output.
	ClassifyFailure(code int, tail []string) Failure
	// WatchDownload starts to measure the download of model (a Model.ID
	// from ParseModel) by a server about to start. The meter it returns
	// reports the bytes fetched since WatchDownload was called; it may
	// block on the file system for a moment. nil = the engine has no way
	// to tell.
	WatchDownload(model string) (meter func() int64)

	// FindRunning lists live servers of this engine on this computer,
	// whoever started them.
	FindRunning() ([]Running, error)
	// Probe reads the server's state. It never returns an error: an
	// unreachable server is a Snapshot with Reachable false. Probes use
	// short timeouts (a loading server may accept and never answer).
	Probe(ctx context.Context, t Target) Snapshot
	// Release unloads the model weights without stopping the server.
	Release(ctx context.Context, t Target) error
	// Restore loads released weights back.
	Restore(ctx context.Context, t Target) error
	// ChatURL is the address of the engine's chat page, or "".
	ChatURL(t Target) string

	// AgentCommand builds the command that starts an agent against the
	// server at t, to run in Terminal. env holds only the variables to set
	// on top of the terminal's own; an empty value unsets the variable.
	// Secrets go into env, never argv.
	AgentCommand(agent string, t Target) (argv []string, env []string)
}

// Downloader is an Engine that can download a model without serving it.
// Lumilo downloads models through it, apart from the server: the server
// keeps running its model meanwhile. An engine without it downloads a model
// on the server's start (Config.ModelUpdates, WatchDownload).
type Downloader interface {
	// Download fetches the model req names and installs it where Models
	// finds it. progress reports the bytes fetched so far and the bytes the
	// download fetches in all (0 = not known yet); it is called from the
	// download's goroutine, often, and must return at once. id is the
	// Model.ID Models lists for it (llama.cpp: the path of the GGUF file).
	//
	// A cancelled ctx stops the download and returns ctx's error; what it
	// fetched stays until DropPartial, for a later Download of the model to
	// go on from (a file the engine cannot resume it may drop at once). A failure the engine can explain is
	// a Failure (errors.As); any other error is the line to show.
	Download(ctx context.Context, req DownloadRequest, progress func(done, total int64)) (id string, err error)
	// DropPartial removes what an unfinished Download of model, started at
	// since, left behind: partly fetched files. It may block on the file
	// system for a moment.
	DropPartial(model string, since time.Time)
}

// Adopter is an engine that installs its own copy of models from files
// already on disk for another engine (or another program): Splash
// assembles a GGUF from the Hugging Face hub cache, where llama.cpp reads
// it as it is.
type Adopter interface {
	// Adoptable lists those models, not the installed ones. A Model.ID is
	// what Download takes to install it; SizeBytes is what is on disk.
	Adoptable(ctx context.Context) ([]Model, error)
}

// Remover is an Engine that can delete an installed model from the disk.
// The caller makes sure no server of the engine runs the model and none
// of its downloads runs.
type Remover interface {
	// PlanRemove says what Remove(id) would delete, without deleting
	// anything. id is a Model.ID that Models lists. It may block on the
	// file system for a moment.
	PlanRemove(ctx context.Context, id string) (RemovePlan, error)
	// Remove deletes the model's files, and the files that came with it
	// (its vision projector, its draft), unless another installed model
	// uses them. Models no longer lists it afterwards.
	Remove(ctx context.Context, id string) error
}

// RemovePlan is what deleting a model frees.
type RemovePlan struct {
	// Bytes are what the deleted files take on the disk. Files another
	// installed model shares are not deleted and not counted.
	Bytes int64
	// Shared: some of the files are in a Hugging Face hub cache, which
	// other programs on this computer may read too.
	Shared bool
}

// DownloadRequest is a model to download.
type DownloadRequest struct {
	Model string // a Model.ID from ParseModel
	// NoVision downloads the model for text only: without its vision
	// projector (Config.NoVision).
	NoVision bool
}

// Info is the fixed description of an engine.
type Info struct {
	// ID is a stable lower-case identifier, e.g. "splash".
	ID string
	// Name is the display name, e.g. "Splash".
	Name string
	// Logo is the engine's brand mark in its own colours.
	Logo Logo
	// Caps says which of Lumilo's controls the engine supports.
	Caps Caps
	// Suggestions are models to offer for download when none is installed,
	// in the order to show them.
	Suggestions []Suggestion
	// ModelSearch says which Hugging Face repositories the engine can run,
	// for the completion of a name typed to download. A repository of GGUF
	// files takes a variant after ":" (OWNER/REPO:VARIANT) in every
	// engine so far; see Engine.ParseModel.
	ModelSearch SearchSpec
	// Agents lists the coding agents the engine can set up
	// (Engine.AgentCommand).
	Agents []Agent
}

// Installer installs and updates an engine: a package manager (Homebrew),
// a Python tool installer (pip, uv), a downloaded binary. Detect must not
// block for long: it looks at fixed paths and may ask the engine's own
// executable for its version.
type Installer interface {
	// Describe says how the engine is installed, for the install screen.
	Describe() InstallInfo
	// Detect reports whether the engine and the installer's prerequisite
	// are present.
	Detect(ctx context.Context) Install
	// Install installs the engine; Upgrade updates it to the newest
	// version. Both write their output to log (may be nil) and wait. last
	// is the last line of that output, which explains a failure (and,
	// with err nil, a run that changed nothing); err is non-nil when the
	// run failed.
	Install(ctx context.Context, log io.Writer) (last string, err error)
	Upgrade(ctx context.Context, log io.Writer) (last string, err error)
	// Latest asks the release feed for the newest version, the one Upgrade
	// would install.
	Latest(ctx context.Context) (string, error)
	// ReleaseNotesURL is the page that describes a version.
	ReleaseNotesURL(version string) string
}

// InstallInfo describes an Installer for the install screen.
type InstallInfo struct {
	// Method is the installer's kind; the panel words its texts by it.
	Method InstallMethod
	// Size is the approximate size of the engine on disk after install.
	Size int64
	// InstallCommand and UpgradeCommand are the command lines the
	// installer runs, shown while it runs; "" shows none.
	InstallCommand, UpgradeCommand string
	// Prerequisite is the program the installer needs, which the user
	// installs by hand; the zero value for none.
	Prerequisite Prerequisite
}

// InstallMethod is how an engine is installed.
type InstallMethod int

const (
	// InstallHomebrew: a Homebrew formula (enginekit.Homebrew).
	InstallHomebrew InstallMethod = iota
)

// Prerequisite is a program that must be installed before the installer
// can work, e.g. Homebrew.
type Prerequisite struct {
	Name string // e.g. "Homebrew"
	URL  string // where its instructions are, e.g. "https://brew.sh"
	Host string // URL's host as shown on its button, e.g. "brew.sh"
	// Arch and OS are what it runs on, e.g. "Apple silicon" and
	// "macOS 26.4", for "Requires {arch} and {os} or later"; "" = not shown.
	Arch, OS string
}

// Install is what Detect found.
type Install struct {
	Installed bool
	Version   string // e.g. "1.4.0"; "" when not installed
	Path      string // absolute path of the engine's executable
	// Ready: the installer's prerequisite is present, so Install can run.
	// When it is not, the user installs it first (InstallInfo.Prerequisite).
	Ready bool
}

// Model is one locally installed model.
type Model struct {
	ID        string // what the engine's --model takes, e.g. "orcarouter/OrcaCyber-Nano-1.0:Mix-15.7G"
	Name      string // e.g. "OrcaCyber-Nano-1.0"
	Owner     string // e.g. "orcarouter"
	Variant   string // e.g. "Mix-15.7G"; may be ""
	SizeBytes int64  // size on disk; 0 when unknown
	// ContextLength is the model's native context window in tokens (the
	// one it was trained for); 0 when unknown.
	ContextLength int
	// KVElements is how many K and V elements one token of context takes
	// in the KV cache, all attention layers together (gguf.Meta.KVElements);
	// 0 when unknown.
	KVElements int64
	// Features are the vision tower and the drafts the engine can load
	// with the model.
	Features Features
}

// SizeBytes of a Model, a Suggestion's DownloadBytes and a variant's
// size in "Add model…" all mean the same: the model's own weights (every
// part of a split GGUF, or the safetensors shards of an MLX checkpoint),
// without its vision projector and drafts. Features lists those apart, and
// Estimate adds everything up.

// Features are what a model can load besides its text weights.
type Features struct {
	// Vision: the model has a vision projector the engine loads, unless
	// Config.NoVision; VisionBytes is its size.
	Vision      bool
	VisionBytes int64
	// NoJPEG: the engine refuses JPEG images on this computer (a decoder
	// it needs is missing); other images serve.
	NoJPEG bool
	// Drafts are the speculative decoding drafts the engine can run with
	// the model, in the order to offer them.
	Drafts []Draft
	// DraftsUnknown: the model is not installed yet (a repository on the
	// hub), so its drafts are not known; Estimate counts the draft the
	// engine always runs at its default size (MemoryCaps.DefaultDraft).
	DraftsUnknown bool
}

// DraftKind is how a draft proposes tokens.
type DraftKind int

const (
	// DraftDFlash is a DFlash draft model: Splash's DFlash2, llama.cpp's
	// draft-dflash.
	DraftDFlash DraftKind = iota
	// DraftMTP is the model's own multi-token prediction layers.
	DraftMTP
	// DraftModel is a smaller model of the same family.
	DraftModel
)

// Draft is a speculative decoding draft.
type Draft struct {
	Kind DraftKind
	// Name is what to show, e.g. "incoai/Qwen3.8-27B-DFlash2" or "MTP".
	Name string
	// ID is what Config.Draft takes to choose it.
	ID string
	// SizeBytes is what the draft adds in memory; 0 for layers that are
	// part of the model's weights.
	SizeBytes int64
	// Always: the engine always runs it and has no way to turn it off;
	// Config.Draft is ignored.
	Always bool
	// Default: the engine runs it when Config.Draft is "" (its default);
	// DraftOff turns it off.
	Default bool
}

// DraftOff is the Config.Draft that turns speculative decoding off.
const DraftOff = "off"

// LogoKind is the image format of a Logo.
type LogoKind int

const (
	LogoSVG LogoKind = iota
	LogoPNG
)

// Logo is a brand mark in its own colours, not a currentColor glyph.
type Logo struct {
	Kind LogoKind
	// Dark is the image for the dark theme, Light for the light one; they
	// may be the same.
	Dark, Light []byte
	// Tile is set when the image is a whole square tile with its own
	// background, drawn edge to edge; otherwise it is a mark that sits on
	// the panel's own tile.
	Tile bool
	// Bare: the image is a mark of an odd shape, drawn across the whole
	// tile without the panel's tile under it.
	Bare bool
}

// Caps are what an engine supports of Lumilo's controls. The panel hides
// a control the engine does not support.
type Caps struct {
	// Chat: the server has a chat page (Engine.ChatURL).
	Chat bool
	// ModelUpdates: the server contacts the model hub on start unless
	// Config.ModelUpdates is off (it downloads or refreshes a model then),
	// so the setting changes something.
	ModelUpdates bool
	// Release: Release unloads the weights on request.
	Release bool
	// AllowedHosts: the server checks the HTTP Host header and accepts the
	// extra names of Config.AllowedHosts.
	AllowedHosts bool
	// LiveSpeed: the output counters of Snapshot grow while a request
	// runs, so the speed of the request in progress can be measured.
	LiveSpeed bool
	// DiskCache: the engine keeps conversation state on disk
	// (Snapshot.DiskUsed and DiskLimit).
	DiskCache bool
	// MaxContext is the largest Config.ContextTokens the engine accepts;
	// 0 = no limit of its own.
	MaxContext int
	// VisionNeedsProjector: the engine refuses a GGUF model of a vision
	// family whose repository has no vision projector (mmproj) unless the
	// model is served for text only (Config.NoVision). A download of such
	// a repository is made text only up front.
	VisionNeedsProjector bool
	// Memory is how the engine uses memory, for Estimate.
	Memory MemoryCaps
}

// Suggestion is a model to offer for download. It holds names and
// numbers only: the panel words them in the user's language.
type Suggestion struct {
	ID   string // what Config.Model takes to download it
	Name string // the model's name, e.g. "Qwen3.8-27B"
	// Maker is who made the model, e.g. "Qwen"; MakerLogo is its logo, an
	// SVG in its own colours.
	Maker     string
	MakerLogo []byte
	Params    int64 // parameters in all
	Active    int64 // parameters active per token (mixture of experts); 0 = all
	// DownloadBytes is about what the download fetches; MemoryBytes about
	// what the weights take in memory.
	DownloadBytes int64
	MemoryBytes   int64
}

// SearchSpec is where to look for models to download.
type SearchSpec struct {
	// Sources are the kinds of repository the engine can run; the hub is
	// searched for each, and the results are merged by downloads.
	Sources []SearchSource
	// RefuseGGUF are the GGUF tensor types the engine does not load, by
	// ggml's names as hf.QuantOf reads them from a variant tag ("BF16",
	// "TQ1_0"); such variants are not offered.
	RefuseGGUF []string
	// MLX, when set, are the MLX quantizations the engine loads; an MLX
	// checkpoint with another one, or none, is refused.
	MLX *MLXQuants
}

// MLXQuants are MLX quantizations, as config.json's "quantization" names
// them: affine (the default mode) with Bits and Groups, or a mode of Modes.
type MLXQuants struct {
	Bits   []int
	Groups []int
	Modes  []string
}

// SearchSource is one kind of repository.
type SearchSource struct {
	// Filter is the Hugging Face tag of the repositories, e.g. "gguf"
	// (the GGUF files of llama.cpp) or "mlx".
	Filter string
	// Architectures are the ones the engine can run, as the hub names them:
	// gguf.architecture for "gguf" repositories, config.model_type for the
	// others. Empty = any; a repository whose architecture the hub does not
	// say is kept.
	Architectures []string
}

// Access is who may connect to the server.
type Access int

const (
	AccessLocal   Access = iota // this computer only (127.0.0.1)
	AccessNetwork               // every interface (0.0.0.0): local network, VPN, forwarded ports
)

// Config is how Lumilo wants the server to run.
type Config struct {
	Model        string        // Model.ID
	Port         int           // e.g. 8000
	Host         string        // bind address resolved from Access, e.g. "127.0.0.1"
	Access       Access        //
	APIKey       string        // "" for none; required for AccessNetwork
	AllowedHosts []string      // extra Host names accepted, e.g. "mymac.local"
	IdleRelease  time.Duration // unload weights after this long idle; 0 = never
	ModelUpdates bool          // false = offline, never contact the model hub
	// ContextTokens is the context window to serve, in tokens; 0 = the
	// engine's choice (the model's native window where memory holds it).
	ContextTokens int
	// NoVision serves text only when the model has a vision projector.
	// The zero value loads it.
	NoVision bool
	// Draft chooses the speculative decoding draft: a Draft.ID of the
	// model's Features, DraftOff, or "" for the engine's default (Splash:
	// its DFlash2 draft; llama.cpp: none).
	Draft string
}

// ModelOptions are the choices of one model beyond its context: the zero
// value is the engine's default. Stored per model like the context.
type ModelOptions struct {
	NoVision bool   `json:"no_vision,omitempty"` // Config.NoVision
	Draft    string `json:"draft,omitempty"`     // Config.Draft
}

// Target is a running server to talk to.
type Target struct {
	Port   int
	APIKey string // "" when unknown or none
}

// StopPolicy is how to stop a server's process group.
type StopPolicy struct {
	Graceful syscall.Signal // first signal to the group, e.g. SIGINT
	Force    syscall.Signal // sent by "Force quit", e.g. a second SIGINT
	KillWait time.Duration  // after Force, SIGKILL the group if still alive
}

// Running is a live server found on this computer.
type Running struct {
	PID   int
	Port  int
	Model string // Model.ID as the server reports it
}

// Pressure is the system memory pressure as the engine reports it.
type Pressure int

const (
	PressureNormal Pressure = iota
	PressureWarning
	PressureCritical
)

// Snapshot is the state of a server at one moment, in engine-neutral terms.
// Counters are cumulative since the server started.
type Snapshot struct {
	At         time.Time
	Reachable  bool // the health endpoint answered
	Ready      bool // the server accepts requests
	Broken     bool // alive but unable to serve (needs a restart)
	Authorized bool // metrics were readable; false on a key mismatch

	Model         string
	ContextTokens int

	MemoryUsed  int64 // bytes the engine holds
	MemoryLimit int64 // the engine's memory budget, bytes
	DiskUsed    int64 // bytes of on-disk conversation state
	DiskLimit   int64
	Pressure    Pressure

	Released    bool          // weights are unloaded
	Restoring   bool          // weights are being loaded back; Released is false then
	IdleRelease time.Duration // the server's idle-unload setting; 0 = never

	Active int // requests being processed now
	Queued int // requests waiting

	Completed     int64         // requests finished
	OutputTokens  int64         // tokens generated
	DecodeTime    time.Duration // time spent generating
	PrefillTokens int64         // prompt tokens read
	PrefillTime   time.Duration // time spent reading prompts
	FirstToken    time.Duration // typical time to first token (median); 0 if unknown
}

// EventKind is what a line of server output means.
type EventKind int

const (
	EventLoading      EventKind = iota // started loading the model
	EventReady                         // ready to serve
	EventRequestStart                  // a request started
	EventRequestDone                   // a request finished
	EventStopping                      // shutting down
	EventError                         // an error line
	EventDownloading                   // started to download model files
)

// Event is a parsed line of server output.
type Event struct {
	Kind  EventKind
	Model string // for EventLoading and EventReady
	Line  string // the line without its timestamp
	// Bytes is what the download fetches, for EventDownloading; 0 when the
	// engine does not say. The EventDownloading of one run add up (a model
	// and its draft come from two repositories).
	Bytes int64
}

// FailureKind is why a server stopped or could not start.
type FailureKind int

const (
	FailNone           FailureKind = iota
	FailPortBusy                   // another program holds the port
	FailAlreadyServing             // another server of this engine holds the port
	FailModel                      // model files missing or not resolvable
	FailMemory                     // not enough memory for the model
	FailExit                       // anything else: exit code and log
	// FailNoKey: the network access needs an API key and there is none
	// (the Keychain could not be read, or holds none). Lumilo refuses to
	// serve every interface without a key; nothing was started.
	FailNoKey
	// FailNoVision: the model's image input cannot be prepared (its
	// repository has no vision projector the engine loads); served for
	// text only (Config.NoVision) it starts.
	FailNoVision
)

// Failure explains a stopped server or a failed download.
type Failure struct {
	Kind FailureKind
	Code int    // exit code
	Line string // the most telling output line, verbatim; may be ""
	// Offline: the server ran offline (Config.ModelUpdates off), so a
	// FailModel means files missing locally, not a failed download. The
	// supervisor sets it; engines leave it false.
	Offline bool
	// Repo is the hub repository whose files a FailModel misses, when the
	// engine names it (e.g. "incoai/Qwen3.8-27B-DFlash2", a draft of the
	// model); "" when it does not.
	Repo string
}

// Error makes a Failure an error, for Downloader.Download.
func (f Failure) Error() string {
	if f.Line != "" {
		return f.Line
	}
	return "engine failure"
}

// Agent is a coding agent the engine can point at its server.
type Agent struct {
	ID    string // e.g. "claude"; passed to AgentCommand
	Name  string // e.g. "Claude"
	Logo  []byte // SVG
	Color string // logo fill as "#RRGGBB"; "" = the text colour
	// MinContext is the smallest context window, in tokens, the agent
	// works in; 0 = any. The panel does not launch it against a server
	// that serves less.
	MinContext int
}
