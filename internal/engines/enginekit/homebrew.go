package enginekit

import (
	"context"
	"errors"
	"io"

	"github.com/kotuke/lumilo/internal/engine"
)

// The Homebrew requirements of the "Homebrew needed" screen: what the
// mockup shows (Homebrew's own installer asks for more recent systems over
// time).
var homebrewNeeds = engine.Prerequisite{
	Name: "Homebrew",
	URL:  "https://brew.sh",
	Host: "brew.sh",
	Arch: "Apple silicon",
	OS:   "macOS 26.4",
}

// Runner runs brew at brewPath with args, writes its output to log and
// returns its last output line (RunBrew).
type Runner func(ctx context.Context, brewPath string, args []string, log io.Writer) (last string, err error)

// Homebrew is an engine.Installer for an engine installed by a Homebrew
// formula. Set its fields before use; it is read-only after that, so it is
// safe for concurrent use. Tests replace Bins, Brews and Run.
type Homebrew struct {
	// Formula installs the engine, e.g. "incoai/tap/splash".
	Formula string
	// Size is the approximate size on disk after install.
	Size int64
	// Bins are where the engine's executable may be, in order
	// (StdBins(name) for one Homebrew links).
	Bins []string
	// Brews are where brew may be; nil = StdBins("brew").
	Brews []string
	// Version reads the version of the executable bin; "" when unknown.
	// nil = always "".
	Version func(ctx context.Context, bin string) string
	// Feed asks the release feed for the newest version (GitHubLatest,
	// FormulaLatest).
	Feed func(ctx context.Context) (string, error)
	// Notes is the release notes page of a version; nil = none.
	Notes func(version string) string
	// Run runs brew; nil = RunBrew.
	Run Runner
}

var _ engine.Installer = (*Homebrew)(nil)

// Describe: installed with "brew install Formula", which needs Homebrew.
func (h *Homebrew) Describe() engine.InstallInfo {
	return engine.InstallInfo{
		Method:         engine.InstallHomebrew,
		Size:           h.Size,
		InstallCommand: "brew install " + h.Formula,
		UpgradeCommand: "brew upgrade " + h.Formula,
		Prerequisite:   homebrewNeeds,
	}
}

func (h *Homebrew) brews() []string {
	if h.Brews != nil {
		return h.Brews
	}
	return StdBins("brew")
}

// Detect looks for the executable and for brew at fixed paths; PATH is
// never consulted.
func (h *Homebrew) Detect(ctx context.Context) engine.Install {
	in := engine.Install{Ready: FirstExecutable(h.brews()) != ""}
	if p := FirstExecutable(h.Bins); p != "" {
		in.Installed, in.Path = true, p
		if h.Version != nil {
			in.Version = h.Version(ctx, p)
		}
	}
	return in
}

// Bin is the executable to run: the first of Bins that exists, else the
// first of Bins, where an install puts it.
func (h *Homebrew) Bin() string {
	if p := FirstExecutable(h.Bins); p != "" {
		return p
	}
	if len(h.Bins) == 0 {
		return ""
	}
	return h.Bins[0]
}

// errNoBrew is an install or upgrade without brew.
var errNoBrew = errors.New("Homebrew is not installed")

// Install runs "brew install Formula".
func (h *Homebrew) Install(ctx context.Context, log io.Writer) (string, error) {
	return h.brew(ctx, "install", log)
}

// Upgrade runs "brew upgrade Formula".
func (h *Homebrew) Upgrade(ctx context.Context, log io.Writer) (string, error) {
	return h.brew(ctx, "upgrade", log)
}

func (h *Homebrew) brew(ctx context.Context, verb string, log io.Writer) (string, error) {
	brew := FirstExecutable(h.brews())
	if brew == "" {
		return "", errNoBrew
	}
	run := h.Run
	if run == nil {
		run = RunBrew
	}
	return run(ctx, brew, []string{verb, h.Formula}, log)
}

// Latest asks Feed.
func (h *Homebrew) Latest(ctx context.Context) (string, error) {
	if h.Feed == nil {
		return "", errors.New("no release feed")
	}
	return h.Feed(ctx)
}

// ReleaseNotesURL asks Notes.
func (h *Homebrew) ReleaseNotesURL(v string) string {
	if h.Notes == nil {
		return ""
	}
	return h.Notes(v)
}
