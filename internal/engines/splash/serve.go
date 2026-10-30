package splash

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// ServeCommand builds "splash serve ..." (flags: ref §1.1, checked against
// "splash serve --help"). The API key goes only into SPLASH_API_KEY, which
// the launcher hands to the server (ref §1.1); a stale one in our own
// environment is dropped by environ.
func (s *Splash) ServeCommand(cfg engine.Config) (argv []string, env []string) {
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	argv = []string{s.binPath(), "serve",
		"--model", cfg.Model,
		"--port", strconv.Itoa(cfg.Port),
		"--host", host,
	}
	if !cfg.ModelUpdates {
		argv = append(argv, "--offline")
	}
	for _, h := range cfg.AllowedHosts {
		if h = strings.TrimSpace(h); h != "" {
			argv = append(argv, "--allowed-host", h)
		}
	}
	argv = append(argv, "--idle-release", idleReleaseArg(cfg.IdleRelease))
	if n := cfg.ContextTokens; n > 0 {
		argv = append(argv, "--max-context", contextArg(n))
	}
	// "skip vision preparation and loading; image and PDF input is
	// refused" (install/launcher.py:415-419). The selection is installed
	// apart, under models/.selections (install/models.py selection_link,
	// lines 200-212), from the files the Hub cache already holds. Splash
	// has no flag that turns its DFlash2 draft off, so Draft is ignored.
	// A model installed only for text (models.go textOnlyLink) is found
	// only with the flag, whatever NoVision says.
	if cfg.NoVision || s.textOnly(cfg.Model) {
		argv = append(argv, "--language-only")
	}

	env = environ()
	if cfg.APIKey != "" {
		env = append(env, "SPLASH_API_KEY="+cfg.APIKey)
	}
	return argv, env
}

// textOnly reports whether model is installed for text only: its
// language-only selection link exists and its plain one does not.
func (s *Splash) textOnly(model string) bool {
	if _, err := os.Lstat(plainLink(s.modelsDir, model)); err == nil {
		return false
	}
	_, err := os.Lstat(textOnlyLink(s.modelsDir, model))
	return err == nil
}

// idleReleaseArg spells --idle-release: "off" disables the automatic unload
// (0 is rejected by Splash), anything else is a positive number of seconds
// (server/serve_options.py parse_idle_release accepts "600", "30m", "off").
func idleReleaseArg(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

// contextArg spells --max-context: "256K" for a whole number of K (1024
// tokens), else the count; capped at Splash's limit (parse_max_context in
// server/serve_options.py: "a token count with an optional K or M suffix,
// up to 1010000"). Without the flag Splash serves the native window when
// memory holds it.
func contextArg(n int) string {
	n = min(n, maxContext)
	if n%1024 == 0 {
		return strconv.Itoa(n/1024) + "K"
	}
	return strconv.Itoa(n)
}

// StopPolicy: the first SIGINT starts a graceful stop (engine gets up to
// 15 s, ref §2.3). A second SIGINT SIGKILLs the engine child at once
// (server.py:1852-1856) and the server then closes within about 2 s
// (server_close, server.py:1625), so 5 s before SIGKILL of the group is
// enough. SIGTERM is not used: the server ignores it during cleanup.
func (s *Splash) StopPolicy() engine.StopPolicy {
	return engine.StopPolicy{
		Graceful: syscall.SIGINT,
		Force:    syscall.SIGINT,
		KillWait: 5 * time.Second,
	}
}

var (
	// Server lines are "HH:MM:SS message" (server/diagnostics.py).
	stampRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2} `)
	// Launcher and argparse errors have no timestamp.
	errorLineRe = regexp.MustCompile(`^(?:splash(?: serve)?: )?error: .`)
	// The server prints its own fatal and engine failures like this.
	serverErrorRe = regexp.MustCompile(`^(?:Error|Engine failed|Engine restart failed|Engine stopped|Template error) · .`)
	// install/hub.py download prints, before snapshot_download, only when
	// files are missing from the cache:
	// "Fetching 3 file(s), 21.24 GB, from owner/repo@0123456789ab; cached files are reused."
	fetchRe = regexp.MustCompile(`^Fetching \d+ file\(s\), ([0-9.]+) GB, from `)
)

// ParseLine reads one line of "splash serve" output (ref §2.2). Stream does
// not matter: stderr also carries many timestamped lines that are not errors.
func (s *Splash) ParseLine(line string, _ bool) (engine.Event, bool) {
	body := strings.TrimSpace(line)
	body = stampRe.ReplaceAllString(body, "")
	ev := engine.Event{Line: body}
	switch {
	case strings.HasPrefix(body, "Loading · "):
		ev.Kind, ev.Model = engine.EventLoading, strings.TrimPrefix(body, "Loading · ")
	case strings.HasPrefix(body, "Ready · "):
		// Ready · <model> · context 256K · [language only · ] http://host:port
		ev.Kind = engine.EventReady
		ev.Model, _, _ = strings.Cut(strings.TrimPrefix(body, "Ready · "), " · ")
	case strings.HasPrefix(body, "Started · "):
		ev.Kind = engine.EventRequestStart
	case strings.HasPrefix(body, "Done · "), strings.HasPrefix(body, "Cancelled · "):
		ev.Kind = engine.EventRequestDone
	case strings.HasPrefix(body, "Stopping · "):
		ev.Kind = engine.EventStopping
	case fetchRe.MatchString(body):
		ev.Kind = engine.EventDownloading
		if gb, err := strconv.ParseFloat(fetchRe.FindStringSubmatch(body)[1], 64); err == nil {
			ev.Bytes = int64(gb * 1e9)
		}
	case serverErrorRe.MatchString(body), errorLineRe.MatchString(body):
		ev.Kind = engine.EventError
	default:
		return engine.Event{}, false
	}
	return ev, true
}

// Failure texts. Each list is matched case-insensitively as substrings.
var (
	// A refusal of the model's vision alone (install/models.py VisionError):
	// install/upstream.py inspect_target appends "; use --language-only to
	// serve text only" to each one, e.g. "the GGUF repository has no BF16,
	// F32 or F16 vision projector (no GGUF named mmproj)" or "<repo> has no
	// vision tower" (testdata/failures/no_vision.*).
	visionPatterns = []string{"use --language-only"}
	modelPatterns  = []string{
		"hf_hub_offline is set",                 // install/hub.py:271-272
		"cannot resolve",                        // install/hub.py:309-316
		"model download or verification failed", // install/launcher.py:141
		"no single gguf matches",                // install/upstream.py:100
		"select a gguf with",                    // install/upstream.py:115-119
		"is not installed in",                   // install/models.py main (verify)
		"is a splash package",                   // install/models.py refuse_package
		"model not found",
		"repository not found",
	}
	// Memory: strings of the closed native engine (found with `strings
	// engine/splash`, ref §4 row "Insufficient memory at start").
	memoryPatterns = []string{
		"not enough free memory to start",
		"memory did not become available within the resource wait limit",
		"macos is short of memory",
		"the memory plan cannot hold",
		"hard budget cannot fit",
		"it leaves no memory for context",
		"floors of the memory plan require",
		"engine memory budget exceeded",
		"insufficient memory",
	}
)

const (
	memoryWaitLine  = "waiting for sufficient available memory"
	readyTimeoutErr = "native readyevent timed out" // server/runtime.py:830
)

// ClassifyFailure explains an exit from its code and output tail (ref §4).
// Exit code 0 is a clean stop, FailNone. Everything unrecognised is
// FailExit, with the first error line as Line.
func (s *Splash) ClassifyFailure(code int, tail []string) engine.Failure {
	if code == 0 {
		return engine.Failure{}
	}
	lines := make([]string, 0, len(tail))
	for _, l := range tail {
		if l = strings.TrimRight(l, "\r\n \t"); l != "" {
			lines = append(lines, l)
		}
	}
	f := engine.Failure{Kind: engine.FailExit, Code: code}
	if code == 2 { // argparse: usage block, then "splash serve: error: ..."
		f.Line = firstErrorLine(lines)
		return f
	}

	// [Errno 48] is in the launcher's "cannot bind" and in the server's
	// "unable to start HTTP server" (server.py:1842-1844).
	if l := findLine(lines, "[errno 48]"); l != "" {
		return engine.Failure{Kind: engine.FailPortBusy, Code: code, Line: l}
	}
	if l := findLine(lines, "splash is already serving"); l != "" {
		return engine.Failure{Kind: engine.FailAlreadyServing, Code: code, Line: l}
	}
	// Before the model patterns: the launcher follows this refusal with
	// "model download or verification failed".
	if l := findLine(lines, visionPatterns...); l != "" {
		return engine.Failure{Kind: engine.FailNoVision, Code: code, Line: l}
	}
	if l := findLine(lines, modelPatterns...); l != "" {
		return engine.Failure{Kind: engine.FailModel, Code: code, Line: l}
	}
	if l := findLine(lines, memoryPatterns...); l != "" {
		return engine.Failure{Kind: engine.FailMemory, Code: code, Line: l}
	}
	// The 600 s startup deadline is memory-bound only when the engine said
	// it was waiting for memory.
	if findLine(lines, memoryWaitLine) != "" {
		if l := findLine(lines, readyTimeoutErr); l != "" {
			return engine.Failure{Kind: engine.FailMemory, Code: code, Line: l}
		}
	}
	f.Line = firstErrorLine(lines)
	if f.Line == "" && len(lines) > 0 {
		f.Line = lines[len(lines)-1]
	}
	return f
}

// findLine returns the first line containing any of the lowercase patterns,
// preferring error lines: a notice such as "Could not reach the Hub
// (HF_HUB_OFFLINE is set); installing … from the Hub cache" precedes the
// error that names what is missing (testdata/failures/draft_offline.*).
func findLine(lines []string, patterns ...string) string {
	var notice string
	for _, l := range lines {
		low := strings.ToLower(l)
		for _, p := range patterns {
			if !strings.Contains(low, p) {
				continue
			}
			if isErrorLine(l) {
				return l
			}
			if notice == "" {
				notice = l
			}
			break
		}
	}
	return notice
}

// isErrorLine reports an "error: ..." or "HH:MM:SS Error · ..." line.
func isErrorLine(l string) bool {
	b := stampRe.ReplaceAllString(l, "")
	return errorLineRe.MatchString(b) || serverErrorRe.MatchString(b)
}

// firstErrorLine is the first "error: ..." or "HH:MM:SS Error · ..." line.
func firstErrorLine(lines []string) string {
	for _, l := range lines {
		if isErrorLine(l) {
			return l
		}
	}
	return ""
}
