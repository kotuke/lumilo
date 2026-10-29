// Package splash implements engine.Engine for Splash (github.com/incoai/splash),
// installed through the incoai/tap/splash Homebrew formula.
//
// Facts about Splash 1.4.0 are cited as "ref §n" (.notes/splash-reference.md)
// or by file in /opt/homebrew/opt/splash/libexec.
package splash

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

const (
	formula     = "incoai/tap/splash"
	installSize = 270_000_000

	latestReleaseURL = "https://api.github.com/repos/incoai/splash/releases/latest"
	// Release tags are plain versions without a "v" (ref §6).
	releasePageURL = "https://github.com/incoai/splash/releases/tag/"
)

//go:embed logos/*.png
var logos embed.FS

// Splash is the Splash engine. The zero value is not usable; call New.
// Its fields are read-only after New, so it is safe for concurrent use.
type Splash struct {
	info       engine.Info
	inst       *enginekit.Homebrew
	modelsDir  string   // ~/Library/Application Support/Splash/models
	runtimeDir string   // ~/Library/Application Support/Splash/runtime
	hubDirs    []string // Hugging Face hub caches the downloads go to
	libexecDir string   // Splash's program files; "" = beside the launcher (download.go)
	releaseURL string
	client     *http.Client
}

var _ engine.Engine = (*Splash)(nil)

// New returns the Splash engine with its standard paths.
func New() *Splash {
	home, _ := os.UserHomeDir()
	support := filepath.Join(home, "Library", "Application Support", "Splash")
	s := &Splash{
		modelsDir:  filepath.Join(support, "models"),
		runtimeDir: filepath.Join(support, "runtime"),
		// install/hub.py downloads with huggingface_hub into its standard
		// cache (constants.HF_HUB_CACHE).
		hubDirs:    enginekit.HubCaches(home),
		releaseURL: latestReleaseURL,
		// Splash answers "Connection: close".
		client: enginekit.LocalClient(),
	}
	s.info = engine.Info{ID: "splash", Name: "Splash", Logo: logo, Caps: caps(),
		Suggestions: suggestions(), ModelSearch: modelSearch(), Agents: agents()}
	s.inst = &enginekit.Homebrew{
		Formula: formula,
		Size:    installSize,
		Bins:    enginekit.StdBins("splash"),
		Version: version,
		Feed:    func(ctx context.Context) (string, error) { return enginekit.GitHubLatest(ctx, s.releaseURL) },
		Notes:   releaseNotesURL,
	}
	return s
}

// Info: Splash with its logo, controls, suggestions and agents.
func (s *Splash) Info() engine.Info { return s.info }

// Installer is Homebrew with the incoai/tap/splash formula. The newest
// version is the latest GitHub release (the tap follows it).
func (s *Splash) Installer() engine.Installer { return s.inst }

// logo is the avatar of the incoai organisation on GitHub (user id
// 282140180, avatars.githubusercontent.com/u/282140180?s=256): white
// layers on a coral square, the same in both themes.
var logo = func() engine.Logo {
	png := enginekit.MustFile(logos, "logos/splash.png")
	return engine.Logo{Kind: engine.LogoPNG, Dark: png, Light: png, Tile: true}
}()

// maxContext is the largest --max-context (serve_options.py
// MAX_YARN_CONTEXT_TOKENS, "up to 1010000" in "splash serve --help").
const maxContext = 1_010_000

// caps: Splash releases weights on request (POST /v1/memory/release, see
// probe.go), checks the Host header against --allowed-host (ref §1.1),
// counts output tokens while it makes them and keeps conversations on
// disk (probe.go).
//
// Memory, from /status of OrcaCyber-Nano-1.0 (testdata/lifecycle): the KV
// cache is int8 with an f32 scale per 256 elements (identity.kv.format
// "int8", memory_plan.model.kv_elements_per_scale 256), 1 + 4/256 bytes per
// element; the reserve is fixed_runtime_bytes less the three weights
// (19,993,572,224 - 15,598,206,976 - 1,266,647,040 - 930,250,752 =
// 2,198,467,456: batch state, prefill and decode buffers, pipelines, the
// runtime's own reserve); the memory plan serves a shorter context when the
// native one does not fit.
func caps() engine.Caps {
	// VisionNeedsProjector: a GGUF repository of a vision family without a
	// BF16, F32 or F16 mmproj is refused unless --language-only
	// (install/upstream.py select_vision; testdata/failures/no_vision.*).
	return engine.Caps{Release: true, AllowedHosts: true, LiveSpeed: true, DiskCache: true, MaxContext: maxContext,
		VisionNeedsProjector: true,
		Memory: engine.MemoryCaps{KVBytesPerElement: 1 + 4.0/256, Reserve: 2_200_000_000, ShrinksContext: true,
			// The DFlash2 draft of a model to download, before its family
			// is known: Qwen3.8-27B's takes 1.27 GB (draft_weights_bytes).
			DefaultDraft: 1_300_000_000}}
}

// ChatURL is the web UI at the root: we never pass --no-webui.
func (s *Splash) ChatURL(t engine.Target) string { return enginekit.ChatURL(t.Port) }

// releaseNotesURL is the GitHub release page of version v.
func releaseNotesURL(v string) string {
	return releasePageURL + url.PathEscape(strings.TrimPrefix(strings.TrimSpace(v), "v"))
}

// binPath is the launcher to run: the first that exists, else the default.
func (s *Splash) binPath() string { return s.inst.Bin() }

// WatchDownload measures a download by the growth of the hub cache: Splash
// prints the size of each fetch ("Fetching N file(s), X GB", ParseLine)
// but no progress without a terminal, as huggingface_hub hides its bars
// then. The whole cache is watched: the model's DFlash2 draft comes from
// another repository, whose name only Splash knows.
func (s *Splash) WatchDownload(string) func() int64 {
	return enginekit.HubMeter(s.hubDirs)
}

var versionRe = regexp.MustCompile(`^Splash (\d\S*)`)

// version reads libexec/release.json beside the resolved launcher (no
// process; ref §6 shows {"version": "1.4.0", ...}) and falls back to
// "splash --version", which prints "Splash 1.4.0". A source checkout has
// neither, and gets "".
func version(ctx context.Context, bin string) string {
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		// <prefix>/Cellar/splash/<v>/bin/splash -> <v>/libexec/release.json
		data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(real)), "libexec", "release.json"))
		if err == nil {
			var rel struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &rel) == nil && rel.Version != "" {
				return rel.Version
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = environ()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if m := versionRe.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
		return m[1]
	}
	return ""
}

// environ is the child environment (enginekit.Environ) without the
// variables that would change what we ask for: a stray key, port or
// offline switch.
func environ() []string {
	return enginekit.Environ(func(name string) bool {
		return name == "SPLASH_API_KEY" || name == "SPLASH_PORT" || name == "HF_HUB_OFFLINE"
	})
}
