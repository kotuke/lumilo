// Package tensorfold implements engine.Engine for TensorFold's native
// server (github.com/ashhart/TensorFold), installed through the
// ashhart/tensorfold Homebrew tap.
//
// Facts are about TensorFold 1.1.9 (tensorfold-native, Metal, Apple M5
// Max) and are cited as "README" (README.md at the 1.1.9 tag), "api.md"
// (docs/api.md), "help" ("tensorfold --help", "serve MODEL --help"),
// "capabilities" ("tensorfold capabilities --json",
// testdata/capabilities.json) or "fixture" (testdata/, recorded from live
// servers with mlx-community/Qwen3.5-2B-MLX-4bit on 2026-10-29).
package tensorfold

import (
	"context"
	"embed"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

const (
	formula = "ashhart/tensorfold/tensorfold"
	// "brew install ashhart/tensorfold/tensorfold" of 1.1.9: 22 files,
	// 17.1 MB; no dependencies.
	installSize = 17_100_000

	// The tap's formula is what "brew upgrade" installs. GitHub's latest
	// release is not a safe feed: the repository also publishes the
	// desktop app's betas (v1.0.0-beta.9).
	formulaURL     = "https://raw.githubusercontent.com/ashhart/homebrew-tensorfold/HEAD/Formula/tensorfold.rb"
	releasePageURL = "https://github.com/ashhart/TensorFold/releases/tag/v"
)

// The logo is the mark of the repository's README, assets/tensorfold-logo.png
// (1254 px, transparent), cut to the mark, centred in a square and scaled
// to 256 px. The repository is Apache-2.0, which grants no trademark rights
// (§6); the mark is used only to identify TensorFold, as
// THIRD_PARTY_NOTICES says.
//
//go:embed logos/tensorfold.png
var logos embed.FS

// TensorFold is the TensorFold engine. The zero value is not usable; call
// New. Paths are read-only after New; the port memo has its own lock.
type TensorFold struct {
	info       engine.Info
	inst       *enginekit.Homebrew
	hubDirs    []string // Hugging Face hub caches the server reads
	formulaURL string
	client     *http.Client
	ps         func() ([]byte, error)        // "ps" of all processes, see running.go
	listeners  func(port int) ([]int, error) // PIDs listening on the TCP port

	// Download (download.go): the hub to ask ("" = huggingface.co) and the
	// hub cache to download into.
	hubURL string
	dlDir  string

	mu    sync.Mutex
	ports map[int]*portMemo // what ServeCommand and FindRunning remember per port
	// The families of the installed executable (families.go), and the
	// executable and its time they were read for.
	fams   map[string][]string
	famKey string
	noJPEG bool
}

var _ engine.Engine = (*TensorFold)(nil)

// New returns the TensorFold engine with its standard paths.
func New() *TensorFold {
	home, _ := os.UserHomeDir()
	tf := &TensorFold{
		hubDirs:    enginekit.HubCaches(home),
		formulaURL: formulaURL,
		dlDir:      enginekit.HubCaches(home)[0],
		client:     enginekit.LocalClient(),
		ps:         runPS,
		listeners:  runLsof,
		ports:      map[int]*portMemo{},
	}
	tf.info = engine.Info{ID: "tensorfold", Name: "TensorFold", Logo: logo, Caps: caps(),
		Suggestions: suggestions(), ModelSearch: modelSearch(), Agents: enginekit.Agents()}
	tf.inst = &enginekit.Homebrew{
		Formula: formula,
		Size:    installSize,
		// The formula links tensorfold-native and, to the same file,
		// tensorfold.
		Bins:    enginekit.StdBins("tensorfold"),
		Version: version,
		Feed:    func(ctx context.Context) (string, error) { return enginekit.TapFormulaLatest(ctx, tf.formulaURL) },
		Notes:   releaseNotesURL,
	}
	return tf
}

// Info: TensorFold with its logo, controls, suggestions and agents
// (agents.go).
func (tf *TensorFold) Info() engine.Info { return tf.info }

// Installer is Homebrew with the formula of the ashhart/tensorfold tap. The
// newest version is the tap's formula.
func (tf *TensorFold) Installer() engine.Installer { return tf.inst }

// logo is the folded mesh on a transparent ground, the same in both
// themes. It is wide (1170 × 684 px of mark), so it is drawn bare, across
// the whole tile, not on the panel's tile.
var logo = func() engine.Logo {
	png := enginekit.MustFile(logos, "logos/tensorfold.png")
	return engine.Logo{Kind: engine.LogoPNG, Dark: png, Light: png, Bare: true}
}()

// caps:
//   - no Chat: there is no chat page (ChatURL).
//   - no ModelUpdates: the server never contacts the hub (WatchDownload);
//     Lumilo downloads models, so the setting would change nothing.
//   - no Release: the server has no route that unloads the model (api.md
//     route table); it unloads by itself after --idle-unload (serve.go).
//   - AllowedHosts: on a loopback bind it answers a foreign Host header
//     with 403 unless --allowed-host names it (api.md "Browser request
//     headers"; fixture keyed/health.txt).
//   - LiveSpeed: the tokens in flight (generation_tokens_running) and a
//     decode clock of Lumilo's own (probe.go liveDecode) grow while a
//     request runs.
//   - no conversation cache on disk: --spill-gib and --learn-conversations
//     are off by default.
//   - no context limit of its own: --context is bounded by the model's
//     window ("--context must fit the Qwen checkpoint's positive context
//     window", fixture failures/huge_context).
//
// Memory, as the server plans it (Qwen3.8-27B with its DFlash2 drafter on
// 48 GiB, checked live 2026-10-29): it keeps "inside 70% of 48 GiB of RAM"
// (Share) and an 8 GiB margin, and a stream of a 131,072-token context
// takes 13.0 GiB with its 512-row prompt scratch and the drafter's cache
// ("--parallel 1 needs 13.0 GiB for its streams and 512-row prompt
// scratch at a 131072-token context; 11.8 GiB remains beside the weights
// and an 8 GiB margin"): 3.25 bytes per KV element of the model's 32,768
// a token (KVBytesPerElement), where the bf16 cache alone is 2. The image
// tower takes 1.79 GiB more (models.go visionBytes), the drafter 1.6 GiB
// (drafterMemory). The reserve is fitted
// to the runs: 131K served without images, refused with them, and 32K
// with images served three streams. Without --context the server picks
// the context (Qwen3.8-27B: 32,768, README) and fits it to memory
// (/memory "context_fitted"). The retained-prompt cache
// (--prompt-cache-gib) takes what is left ("2.5 GiB from 2.5 GiB free"),
// so it is not counted.
func caps() engine.Caps {
	return engine.Caps{AllowedHosts: true, LiveSpeed: true,
		Memory: engine.MemoryCaps{KVBytesPerElement: 3.25, Reserve: 4_000_000_000, ShrinksContext: true, Share: 0.7}}
}

// ChatURL: the server has no chat page; "/" answers the health JSON
// (api.md "Health"), and --dashboard, off by default, shows metrics only.
func (tf *TensorFold) ChatURL(engine.Target) string { return "" }

// releaseNotesURL is the GitHub release of version v; tags are "v1.1.9".
func releaseNotesURL(v string) string {
	return releasePageURL + url.PathEscape(enginekit.DropRevision(strings.TrimPrefix(strings.TrimSpace(v), "v")))
}

// binPath is the server to run: the first that exists, else the default.
func (tf *TensorFold) binPath() string { return tf.inst.Bin() }

// WatchDownload: the server never downloads ("the native engine does not
// download: run tensorfold pull … first", fixture failures/no_model);
// Lumilo downloads through Download.
func (tf *TensorFold) WatchDownload(string) func() int64 { return nil }

var (
	// <prefix>/Cellar/tensorfold/<version>/libexec/bin/tensorfold-native
	cellarRe = regexp.MustCompile(`/Cellar/tensorfold/([0-9][^/]*)/`)
	// "tensorfold-native 1.1.9" (the formula's test).
	versionRe = regexp.MustCompile(`(?m)^tensorfold-native (\S+)`)
)

// version reads the keg name from the resolved symlink (no process),
// without a Homebrew revision. It falls back to "tensorfold --version".
func version(ctx context.Context, bin string) string {
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		if m := cellarRe.FindStringSubmatch(real); m != nil {
			return enginekit.DropRevision(m[1])
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
	if m := versionRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

// dropEnv reports whether an inherited variable must not reach the server
// (capabilities "env"): TENSORFOLD_API_KEY adds keys to --api-key (api.md
// "API keys") and TENSORFOLD_REQUEST_LOG would log request bodies. The hub
// variables stay: the server reads the cache Models lists.
func dropEnv(name string) bool {
	return name == "TENSORFOLD_API_KEY" || name == "TENSORFOLD_REQUEST_LOG"
}

// environ is the child environment (enginekit.Environ) without the
// variables of dropEnv.
func environ() []string { return enginekit.Environ(dropEnv) }
