package llamacpp

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/gguf"
)

// ServeCommand builds "llama-server -m <gguf> ..." (flags checked against
// "llama-server --help" of 0.7.0).
//
//   - An installed model is a path (Model.ID, see models.go), passed as -m:
//     the server never contacts the hub for it. A model to download is
//     "owner/repo[:quant]" (ParseModel), passed as -hf: the server
//     downloads it into the hub cache, mmproj included, before it loads it,
//     and prints nothing about the download while its stderr is not a
//     terminal (checked live with unsloth/SmolLM2-135M-Instruct-GGUF:Q2_K;
//     "load_model: loading model '<owner/repo:quant>'" comes after it).
//   - --offline (help: "forces use of cache, prevents network access") is
//     added when ModelUpdates is off.
//   - ContextTokens is -c (help: "size of the prompt context (default: 0,
//     0 = loaded from model)"). Without it --fit may shrink the context to
//     fit free memory; with it --fit leaves the context alone.
//   - A single mmproj-*.gguf beside the model is passed as --mmproj, as -hf
//     would do (help: "mmproj is also downloaded automatically"). NoVision
//     leaves it out, and with -hf adds --no-mmproj (help: "to disable, add
//     --no-mmproj").
//   - Draft: see draftArgs.
//   - --metrics: /metrics is off by default (README "/metrics").
//   - The API key goes into LLAMA_API_KEY (help: "--api-key KEY ... (env:
//     LLAMA_API_KEY)"; fixture failures/apikey_*: 401 without it).
//   - Without a key, --cors-origins localhost keeps other web pages away
//     (the default is "*"; the server warns about it, fixture serve.stderr).
//   - IdleRelease is --sleep-idle-seconds: the model and KV cache leave
//     memory after that many idle seconds and the next task loads them back
//     (README "Sleeping on Idle").
//   - AllowedHosts has no counterpart: llama-server does not check Host.
//   - Defaults kept: --jinja on, -ngl auto (all layers on Metal), --fit on
//     (shrinks the context to fit free memory), --slots on, --parallel auto
//     (4 slots, unified KV), --ui on.
func (l *LlamaCpp) ServeCommand(cfg engine.Config) (argv []string, env []string) {
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	model := "-m"
	if !filepath.IsAbs(cfg.Model) {
		model = "-hf"
	}
	served := alias(cfg.Model)
	argv = []string{l.binPath(),
		model, cfg.Model,
		"--alias", served,
		"--host", host,
		"--port", strconv.Itoa(cfg.Port),
		"--metrics",
	}
	switch mm := sidecarFor(cfg.Model, "mmproj"); {
	case cfg.NoVision && model == "-hf":
		argv = append(argv, "--no-mmproj")
	case mm != "" && !cfg.NoVision:
		argv = append(argv, "--mmproj", mm)
	}
	argv = append(argv, draftArgs(cfg)...)
	if !cfg.ModelUpdates {
		argv = append(argv, "--offline")
	}
	if cfg.ContextTokens > 0 {
		argv = append(argv, "-c", strconv.Itoa(cfg.ContextTokens))
	}
	if cfg.APIKey == "" {
		argv = append(argv, "--cors-origins", "localhost")
	}
	if s := sleepSeconds(cfg.IdleRelease); s > 0 {
		argv = append(argv, "--sleep-idle-seconds", strconv.Itoa(s))
	}
	l.remember(cfg.Port, cfg.IdleRelease, served)

	env = environ()
	if cfg.APIKey != "" {
		env = append(env, "LLAMA_API_KEY="+cfg.APIKey)
	}
	return argv, env
}

// alias is the name the API serves model by (--alias, "set model name
// aliases ... (to be used by API)", help of 0.8.0), for the agents to ask
// for: the GGUF file's name without ".gguf" and a split part's
// "-00001-of-0000n", else the -hf name without its owner
// ("Qwen3.8-27B-GGUF:UD-Q4_K_M"). Without it /v1/models names the model by
// its full path (fixture ready.v1models).
func alias(model string) string {
	if !filepath.IsAbs(model) {
		_, name, ok := strings.Cut(model, "/")
		if !ok {
			return model
		}
		return name
	}
	name := gguf.Stem(filepath.Base(model))
	if m := splitRe.FindStringSubmatch(filepath.Base(model)); m != nil {
		name = m[1]
	}
	return name
}

// sleepSeconds is d in whole seconds, rounded up; 0 means never (the
// server's default is -1, disabled).
func sleepSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(d.Seconds()))
}

// draftArgs turns on speculative decoding (help: "--spec-type ...
// comma-separated list of types of speculative decoding to use (default:
// none)"): "mtp" uses the model's own MTP layers (draft-mtp); a path is a
// DFlash draft GGUF beside the model, loaded with -md ("--spec-draft-model,
// -md ... draft model for speculative decoding") as draft-dflash. Anything
// else, "" and engine.DraftOff included, is the default: none.
func draftArgs(cfg engine.Config) []string {
	switch {
	case cfg.Draft == draftMTP:
		return []string{"--spec-type", "draft-mtp"}
	case filepath.IsAbs(cfg.Draft) && filepath.IsAbs(cfg.Model) && sidecarFor(cfg.Model, "dflash") == cfg.Draft:
		return []string{"-md", cfg.Draft, "--spec-type", "draft-dflash"}
	}
	return nil
}

// sidecarFor returns the only file of the kind (gguf.Sidecar) in the
// model's directory, or "" when there is none or more than one.
func sidecarFor(model, kind string) string {
	if !filepath.IsAbs(model) {
		return ""
	}
	dir := filepath.Dir(model)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	found := ""
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") && gguf.Sidecar(e.Name()) == kind {
			if found != "" {
				return ""
			}
			found = filepath.Join(dir, e.Name())
		}
	}
	return found
}

// StopPolicy (checked live, testdata/lifecycle/stop_* and notes):
//   - Idle, one SIGINT or SIGTERM ends the server in 0.06 s with exit 0.
//   - During a non-streamed request one SIGINT or SIGTERM never finished in
//     60 s: the HTTP thread pool waits for the request. A streamed one ended
//     after 30 s (the SSE ping interval).
//   - A second SIGINT prints "Received second interrupt, terminating
//     immediately." and calls exit(); Metal's teardown then may abort
//     (exit 134, GGML_ASSERT in ggml-metal-device.m). So the group gets
//     SIGKILL 3 s after it.
//
// llama-server does not fork without router mode (no children in the live
// runs), so the group is the one process.
func (l *LlamaCpp) StopPolicy() engine.StopPolicy {
	return engine.StopPolicy{
		Graceful: syscall.SIGINT,
		Force:    syscall.SIGINT,
		KillWait: 3 * time.Second,
	}
}

var (
	// Log lines are "M.SS.mmm.uuu L tag  func: message", the time since
	// start and the level I/W/E/D/T (fixture serve.stderr). Older builds
	// and the argument parser print without the prefix.
	prefixRe  = regexp.MustCompile(`^\d+\.\d{2}\.\d{3}\.\d{3} ([IWEDT]) `)
	loadingRe = regexp.MustCompile(`load_model: loading model '(.*)'$`)
	bindRe    = regexp.MustCompile(`(?i)couldn't bind .*port: (\d+)`)
)

// splitLine returns the level ("" when absent) and the message.
func splitLine(line string) (level, body string) {
	body = strings.TrimSpace(line)
	if m := prefixRe.FindStringSubmatch(body); m != nil {
		return m[1], body[len(m[0]):]
	}
	return "", body
}

// ParseLine reads one line of llama-server output. Everything goes to
// stderr (stdout stayed empty in every live run), so the stream is ignored.
//
//	srv    load_model: loading model '<path>'      Loading
//	srv  llama_server: model loaded                Ready
//	srv  llama_server: listening on http://h:p     Ready (0.7.0 prints it after "model loaded")
//	main: server is listening on ...               Ready (older builds)
//	slot launch_slot_: id  0 | task 4 | processing task   RequestStart
//	slot      release: id  0 | task 4 | stop processing   RequestDone
//	srv    operator(): ...cleaning up before exit...      Stopping
//	Received second interrupt, terminating immediately.  Stopping
//	level E, "error: ..." (argument parser), GGML_ASSERT  Error
//
// After a sleep the server prints "loading model" again but no "model
// loaded"; the supervisor reads Ready only while loading, so that is fine.
func (l *LlamaCpp) ParseLine(line string, _ bool) (engine.Event, bool) {
	level, body := splitLine(line)
	ev := engine.Event{Line: body}
	switch {
	case body == "":
		return engine.Event{}, false
	case loadingRe.MatchString(body):
		ev.Kind, ev.Model = engine.EventLoading, loadingRe.FindStringSubmatch(body)[1]
	case strings.HasSuffix(body, ": model loaded"),
		strings.Contains(body, ": listening on http"),
		strings.Contains(body, "server is listening on"):
		ev.Kind = engine.EventReady
	case strings.Contains(body, "| processing task"):
		ev.Kind = engine.EventRequestStart
	case strings.Contains(body, "| stop processing"):
		ev.Kind = engine.EventRequestDone
	case strings.Contains(body, "cleaning up before exit"),
		strings.HasPrefix(body, "Received second interrupt"):
		ev.Kind = engine.EventStopping
	case isErrorLine(level, body):
		ev.Kind = engine.EventError
	default:
		return engine.Event{}, false
	}
	return ev, true
}

// Failure texts, matched case-insensitively as substrings.
var (
	// Memory comes first: an allocation failure during load is followed by
	// the generic "failed to load model". Texts from ggml-metal and
	// llama.cpp sources; not reproduced live (with --fit on, the default,
	// the context shrinks to fit instead). A live run with -c 4000000
	// --fit off was SIGKILLed after 44 s without a line (failures/huge_ctx).
	memoryPatterns = []string{
		"failed to allocate",
		"unable to allocate",
		"out of memory",
		"outofmemory",
		"insufficient memory",
		"not enough memory",
	}
	modelPatterns = []string{
		"failed to open gguf file",                       // failures/no_model
		"invalid magic characters",                       // failures/not_gguf
		"exactly one out metadata, path_model, and file", // failures/offline_missing (-hf, not cached)
		"unknown model architecture",                     // a GGUF llama.cpp cannot run
		"failed to download",                             // -hf
		"failed to load model",                           // the generic tail of all of these
		"error loading model",
	}
)

// ClassifyFailure explains an exit from its code and output tail. Exit 0
// is a clean stop. Every live failure exited 1 (fixtures failures/).
func (l *LlamaCpp) ClassifyFailure(code int, tail []string) engine.Failure {
	if code == 0 {
		return engine.Failure{}
	}
	lines := make([]string, 0, len(tail))
	for _, t := range tail {
		if t = strings.TrimRight(t, "\r\n \t"); t != "" {
			lines = append(lines, t)
		}
	}
	f := engine.Failure{Kind: engine.FailExit, Code: code}

	// "couldn't bind HTTP server socket, hostname: H, port: P" is printed
	// both for a held port and for an impossible one (--port 99999). A
	// port held by another llama-server reads the same, so this is never
	// FailAlreadyServing; the supervisor's rescan finds that server.
	for _, line := range lines {
		if m := bindRe.FindStringSubmatch(line); m != nil {
			if p, err := strconv.Atoi(m[1]); err == nil && p >= 1 && p <= 65535 {
				f.Kind = engine.FailPortBusy
			}
			f.Line = line
			return f
		}
	}
	if line := findLine(lines, memoryPatterns...); line != "" {
		return engine.Failure{Kind: engine.FailMemory, Code: code, Line: line}
	}
	if line := findLine(lines, modelPatterns...); line != "" {
		return engine.Failure{Kind: engine.FailModel, Code: code, Line: line}
	}
	f.Line = firstErrorLine(lines)
	if f.Line == "" && len(lines) > 0 {
		f.Line = lines[len(lines)-1]
	}
	return f
}

// findLine returns the first line containing any of the lowercase patterns.
// Patterns are tried in order, so an earlier one wins over a later one
// that matches an earlier line.
func findLine(lines []string, patterns ...string) string {
	for _, p := range patterns {
		for _, l := range lines {
			if strings.Contains(strings.ToLower(l), p) {
				return l
			}
		}
	}
	return ""
}

// isErrorLine: level E, an "error: ..." line of the argument parser, or a
// failed ggml assertion ("src/ggml-metal/ggml-metal-device.m:1041:
// GGML_ASSERT(...) failed", printed without a prefix before an abort).
func isErrorLine(level, body string) bool {
	return level == "E" || level == "" && (strings.HasPrefix(body, "error: ") || strings.Contains(body, "GGML_ASSERT("))
}

// firstErrorLine is the first error line (isErrorLine).
func firstErrorLine(lines []string) string {
	for _, l := range lines {
		if isErrorLine(splitLine(l)) {
			return l
		}
	}
	return ""
}
