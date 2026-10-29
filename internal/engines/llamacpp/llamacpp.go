// Package llamacpp implements engine.Engine for llama.cpp's llama-server
// (github.com/ggml-org/llama.cpp), installed through the llama.cpp
// Homebrew formula of homebrew-core.
//
// Facts are about llama.cpp 0.7.0 (build 11669, commit 620d2225c) and are
// cited as "help" (llama-server --help), "README" (tools/server/README.md at
// tag v0.7.0) or "fixture" (testdata/, recorded from a live server with
// ggml-org/Qwen3-0.6B-GGUF:Q8_0 on 2026-10-27).
package llamacpp

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
	formula = "llama.cpp"
	// Cellar sizes after "brew install llama.cpp" on macOS 27 arm64:
	// llama.cpp 0.7.0 20.1 MB, ggml 0.27.0 8.2 MB, libomp 23.1.4 1.8 MB,
	// and the openssl@4 dependency (25 MB), which was already installed here.
	installSize = 56_000_000

	// The formula's stable version is what "brew upgrade llama.cpp" installs.
	// GitHub releases are not: every commit to master is a "bNNNNN"
	// prerelease, and the "vX.Y.Z" releases reach the formula later.
	formulaURL     = "https://formulae.brew.sh/api/formula/llama.cpp.json"
	releasePageURL = "https://github.com/ggml-org/llama.cpp/releases/tag/v"
)

// The logos are the square icons of the llama.cpp brand assets
// (github.com/ggml-org/llama.brand, icon/icon-dark.svg and icon-light.svg).
// Licence: CC BY-NC 4.0 plus BRAND-USAGE.md, which allows redistribution in
// software to identify llama.cpp, including rescaling and single-colour
// rendering; attribution is "naming this repository as the source in a
// README, credits file, or equivalent".
//
//go:embed logos/*.svg
var logos embed.FS

// LlamaCpp is the llama.cpp engine. The zero value is not usable; call New.
// Paths are read-only after New; the probe memo has its own lock.
type LlamaCpp struct {
	info       engine.Info
	inst       *enginekit.Homebrew
	hubDirs    []string // Hugging Face hub caches (models--owner--repo/...)
	flatDirs   []string // directories of plain .gguf files
	formulaURL string
	client     *http.Client
	ps         func() ([]byte, error)        // "ps" of all processes, see running.go
	psPID      func(int) ([]byte, error)     // "ps" of one process
	listeners  func(port int) ([]int, error) // PIDs listening on the TCP port, see running.go

	// Download (download.go): the hub to ask ("" = huggingface.co) and the
	// hub cache to download into.
	hubURL string
	dlDir  string

	mu    sync.Mutex
	ports map[int]*portMemo // what Probe and ServeCommand remember per port
}

var _ engine.Engine = (*LlamaCpp)(nil)

// New returns the llama.cpp engine with its standard paths.
func New() *LlamaCpp {
	home, _ := os.UserHomeDir()
	l := &LlamaCpp{
		hubDirs:    hubDirs(home),
		flatDirs:   flatDirs(home),
		formulaURL: formulaURL,
		dlDir:      enginekit.HubCaches(home)[0],
		client:     enginekit.LocalClient(),
		ps:         runPS,
		psPID:      runPSPID,
		listeners:  runLsof,
		ports:      map[int]*portMemo{},
	}
	l.info = engine.Info{ID: "llamacpp", Name: "llama.cpp", Logo: logo, Caps: caps(),
		Suggestions: suggestions(), ModelSearch: modelSearch(), Agents: enginekit.Agents()}
	l.inst = &enginekit.Homebrew{
		Formula: formula,
		Size:    installSize,
		Bins:    enginekit.StdBins("llama-server"),
		Version: version,
		Feed:    func(ctx context.Context) (string, error) { return enginekit.FormulaLatest(ctx, l.formulaURL) },
		Notes:   releaseNotesURL,
	}
	return l
}

// hubDirs are the Hugging Face hub caches that llama-server reads: with
// -hf it downloads into the hub layout, and "llama-server -cl" lists models
// from ~/.cache/huggingface/hub (checked live). The strings of
// libllama-common name HF_HUB_CACHE, HF_HOME and XDG_CACHE_HOME; the
// standard precedence of huggingface_hub is used. LLAMA_CACHE is named by
// the router docs ("Cached models (controlled by the LLAMA_CACHE
// environment variable)") without its layout, so it is read both as a hub
// cache and as a flat directory.
func hubDirs(home string) []string {
	return enginekit.HubCaches(home, os.Getenv("LLAMA_CACHE"))
}

// flatDirs hold .gguf files directly: LLAMA_CACHE and the cache of older
// llama.cpp builds, ~/Library/Caches/llama.cpp, which stored -hf downloads
// as <owner>_<repo>_<file>.gguf (README, GET /models example).
func flatDirs(home string) []string {
	return enginekit.Uniq([]string{os.Getenv("LLAMA_CACHE"), filepath.Join(home, "Library", "Caches", "llama.cpp")})
}

// Info: llama.cpp with its logo, controls, suggestions and agents
// (agents.go).
func (l *LlamaCpp) Info() engine.Info { return l.info }

// Installer is Homebrew with the llama.cpp formula of homebrew-core. The
// newest version is the formula's (formulae.brew.sh), not GitHub's.
func (l *LlamaCpp) Installer() engine.Installer { return l.inst }

// logo is the square mark on a transparent ground: icon-dark.svg is drawn
// in #FAFAFA for dark backgrounds, icon-light.svg in #111111 for light ones.
var logo = engine.Logo{Kind: engine.LogoSVG,
	Dark: enginekit.MustFile(logos, "logos/icon-dark.svg"), Light: enginekit.MustFile(logos, "logos/icon-light.svg")}

// caps: no Release (see probe.go; idle unload is --sleep-idle-seconds), no
// Host check (llama-server serves any Host header, serve.go), no live
// speed (llamacpp:tokens_predicted_total and _seconds_total move only when
// a request ends: a streamed 1500-token request read 0 for 6 s, then 1500,
// checked live on 2026-10-27) and no conversation cache on disk. -c has no
// limit of its own: the model's and the memory's limits apply.
//
// Memory: the KV cache is f16 unless -ctk/-ctv say otherwise (help:
// "--cache-type-k TYPE ... (default: f16)"), 2 bytes per element; the
// 4 slots share one cache of -c tokens (--kv-unified, the default with
// --parallel auto). The reserve is the compute buffers, about 1 GB for a
// 27B model on Metal. --fit (on by default) shrinks the context to fit.
func caps() engine.Caps {
	return engine.Caps{Memory: engine.MemoryCaps{KVBytesPerElement: 2, Reserve: 1_000_000_000, ShrinksContext: true}}
}

// ChatURL is the built-in web UI at the root (--ui is on by default, help).
// It is served gzip-only: a client without "Accept-Encoding: gzip" gets 415,
// every browser sends it (fixture notes).
func (l *LlamaCpp) ChatURL(t engine.Target) string { return enginekit.ChatURL(t.Port) }

// releaseNotesURL is the GitHub release of version v; tags are "v0.7.0".
func releaseNotesURL(v string) string {
	return releasePageURL + url.PathEscape(enginekit.DropRevision(strings.TrimPrefix(strings.TrimSpace(v), "v")))
}

// binPath is the server to run: the first that exists, else the default.
func (l *LlamaCpp) binPath() string { return l.inst.Bin() }

// WatchDownload measures a download by the growth of the model's
// repository in the hub caches: llama-server prints nothing about a
// download while its stderr is not a terminal, and its options have no
// switch for it (--log-* set verbosity, colours and prefixes only, help).
// It writes the file as blobs/<sha256>.downloadInProgress and grows it as
// it fetches (enginekit/testdata/hub-llamacpp.txt). A model that is no
// repository (a path) has nothing to watch.
func (l *LlamaCpp) WatchDownload(model string) func() int64 {
	repo, _, _ := strings.Cut(model, ":")
	if strings.Count(repo, "/") != 1 || strings.HasPrefix(repo, "/") {
		return nil
	}
	return enginekit.HubMeter(l.hubDirs, repo)
}

var (
	// <prefix>/Cellar/llama.cpp/<version>/bin/llama-server
	cellarRe = regexp.MustCompile(`/Cellar/llama\.cpp/([0-9][^/]*)/bin/[^/]+$`)
	// "version: 0.7.0 (build 11669, commit 620d2225c)" (0.7.0 prints
	// "Version:"); older builds print "version: 6123 (abcdef0)".
	versionRe = regexp.MustCompile(`(?im)^version: (\S+)`)
)

// version reads the keg name from the resolved symlink (no process),
// without a Homebrew revision ("0.7.0_1" -> "0.7.0"). It falls back to
// "llama-server --version", which writes to stderr.
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	if m := versionRe.FindStringSubmatch(string(out)); m != nil {
		return m[1]
	}
	return ""
}

// dropEnv reports whether an inherited variable must not reach the server:
// every LLAMA_ARG_* is a flag in disguise (help: "(env: LLAMA_ARG_PORT)"),
// LLAMA_API_KEY is --api-key and HF_HUB_OFFLINE would change downloads.
func dropEnv(name string) bool {
	return name == "LLAMA_API_KEY" || name == "HF_HUB_OFFLINE" || strings.HasPrefix(name, "LLAMA_ARG_")
}

// environ is the child environment (enginekit.Environ) without the
// variables of dropEnv.
func environ() []string { return enginekit.Environ(dropEnv) }
