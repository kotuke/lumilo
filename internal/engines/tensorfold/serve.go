package tensorfold

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// ServeCommand builds "tensorfold serve REPO ..." (flags of "serve MODEL
// --help" and capabilities of 1.1.9):
//
//   - The model is the repository of a cached checkpoint (Model.ID). The
//     server never downloads, so ModelUpdates has nothing to change.
//   - ContextTokens is --context ("Bound prompt plus reply tokens; the
//     model's window and memory checks still apply", README). Without it
//     the server would pick one of its own, 32,768 for Qwen3.8-27B
//     whatever the memory (README; metal.zig openQwen27 "o.context orelse
//     32768"), too small for Claude Code and Pi: Lumilo then passes the
//     largest that fits (autoContext).
//   - IdleRelease is --idle-unload SECONDS: "the engine closes after N s
//     idle and the next request reloads it (a full model load)" (fixture
//     keyed/idle.stdout). Only requests that use the engine count as
//     activity: under GET /health and /metrics once a second it unloaded
//     after 10 s (fixture keyed/idle-poll.txt).
//   - AllowedHosts are --allowed-host NAME, repeated (api.md).
//   - The API key goes into TENSORFOLD_API_KEY ("Both servers accept ...
//     comma-separated TENSORFOLD_API_KEY", api.md); the log names it
//     "key=env-1" (fixture keyed/). A key with a comma would become two.
//   - Vision: --vision for a model whose images the engine reads
//     (checkpoint.visionOK), unless NoVision. Without it the server
//     answers image parts with HTTP 400 (api.md).
//   - Draft: a drafter repository of the model's Features is --drafter;
//     "" is the default drafter (the first of TensorFold.drafters), as the
//     README's recipe serves Qwen3.8-27B "--drafter
//     z-lab/Qwen3.8-27B-DFlash2" ("without it decoding is plain"); DraftOff
//     is --no-drafts ("Produce the plain reference"). A model that takes
//     no drafter keeps the server's own drafts with "".
func (tf *TensorFold) ServeCommand(cfg engine.Config) (argv []string, env []string) {
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	argv = []string{tf.binPath(), "serve", cfg.Model,
		"--host", host,
		"--port", strconv.Itoa(cfg.Port),
	}
	ctx := cfg.ContextTokens
	if ctx <= 0 {
		ctx = tf.autoContext(cfg)
	}
	if ctx > 0 {
		argv = append(argv, "--context", strconv.Itoa(ctx))
	}
	if s := idleSeconds(cfg.IdleRelease); s > 0 {
		argv = append(argv, "--idle-unload", strconv.Itoa(s))
	}
	for _, h := range cfg.AllowedHosts {
		if h = strings.TrimSpace(h); h != "" {
			argv = append(argv, "--allowed-host", h)
		}
	}
	cp, known := tf.checkpointOf(cfg.Model)
	vision := !cfg.NoVision && known && cp.visionOK()
	if vision {
		argv = append(argv, "--vision")
	}
	switch {
	case cfg.Draft == engine.DraftOff:
		argv = append(argv, "--no-drafts")
	case cfg.Draft != "":
		argv = append(argv, "--drafter", cfg.Draft)
	case known && cp.drafterOK():
		_, name, _ := strings.Cut(cfg.Model, "/")
		if ds := tf.drafters(name); len(ds) > 0 {
			argv = append(argv, "--drafter", ds[0].ID)
		}
	}
	tf.forget(cfg.Port)
	tf.remember(cfg.Port, cfg.Model, servedName(cfg.Model), cfg.IdleRelease, vision)

	env = environ()
	if cfg.APIKey != "" {
		env = append(env, "TENSORFOLD_API_KEY="+cfg.APIKey)
	}
	return argv, env
}

// autoContexts are the windows autoContext tries, largest first: the
// sizes of the context select of Settings.
var autoContexts = []int{256 << 10, 128 << 10, 64 << 10, 32 << 10}

// autoContext is the largest of autoContexts, up to the model's own
// window, that the memory estimate (engine.Judge with caps, which follows
// the server's own plan) fits in this computer's memory, with the image
// input and the draft of cfg; 0 when none does or the model or the memory
// is unknown, which leaves the choice to the server. On 48 GiB,
// Qwen3.8-27B with its drafter gets 64K with images and 128K without.
func (tf *TensorFold) autoContext(cfg engine.Config) int {
	md, ok := tf.modelOf(cfg.Model)
	if !ok {
		return 0
	}
	return fitContext(md, cfg, tf.info.Caps.Memory, tf.ram())
}

// fitContext is autoContext for the model md on a computer of ram bytes.
func fitContext(md engine.Model, cfg engine.Config, mc engine.MemoryCaps, ram int64) int {
	if ram <= 0 {
		return 0
	}
	budget := engine.Budget(ram, 0, mc.Share)
	for _, c := range autoContexts {
		if md.ContextLength > 0 && c > md.ContextLength {
			continue
		}
		try := cfg
		try.ContextTokens = c
		if _, fit := engine.Judge(md, try, mc, budget); fit == engine.Fits {
			return c
		}
	}
	return 0
}

// modelOf is the installed checkpoint repo as Models lists it, with its
// drafters; ok is false when it is not installed.
func (tf *TensorFold) modelOf(repo string) (engine.Model, bool) {
	fams := tf.families(context.Background())
	for _, d := range tf.hubDirs {
		snap := pickSnapshot(filepath.Join(d, enginekit.RepoFolder(repo)))
		if snap == "" {
			continue
		}
		s := snapshot{repo: repo, dir: snap}
		m, cp, ok := s.model(fams)
		if !ok {
			continue
		}
		if cp.drafterOK() {
			m.Features.Drafts = tf.drafters(m.Name)
		}
		return m, true
	}
	return engine.Model{}, false
}

// physicalMemory is the computer's memory in bytes (sysctl hw.memsize, a
// little-endian uint64), 0 when unknown. syscall.Sysctl drops one trailing
// NUL byte, which hw.memsize has for every size below 2^56.
func physicalMemory() int64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil || len(s) == 0 || len(s) > 8 {
		return 0
	}
	var b [8]byte
	copy(b[:], s)
	return int64(binary.LittleEndian.Uint64(b[:]))
}

// idleSeconds is d in whole seconds, rounded up; 0 means never (the
// server's default: "0 (off)", cli.zig).
func idleSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(d.Seconds()))
}

// checkpointOf reads the config.json of the cached checkpoint repo.
func (tf *TensorFold) checkpointOf(repo string) (checkpoint, bool) {
	for _, d := range tf.hubDirs {
		snap := pickSnapshot(filepath.Join(d, enginekit.RepoFolder(repo)))
		if snap == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(snap, "config.json"))
		if err != nil {
			continue
		}
		if cp, err := parseCheckpoint(data); err == nil {
			return cp, true
		}
	}
	return checkpoint{}, false
}

// StopPolicy (checked live, fixtures lifecycle/ and keyed/):
//   - Idle, SIGINT ends the server in 0.12 s with exit 0: "shutting down
//     (SIGINT): draining", "drained", "stopped".
//   - A running request may finish first: the drain waits up to 20 s
//     ("1 request(s) in progress may finish within 20.0 s"); a stream of
//     3000 tokens held it 10 s.
//   - A second SIGINT ends the requests "at the next round boundary"
//     (finish=cancelled): the server was gone 2.6 s after it.
//
// The server does not fork: the group is the one process. SIGKILL comes
// 10 s after the second signal.
func (tf *TensorFold) StopPolicy() engine.StopPolicy {
	return engine.StopPolicy{
		Graceful: syscall.SIGINT,
		Force:    syscall.SIGINT,
		KillWait: 10 * time.Second,
	}
}

var (
	// "[tensorfold] serving NAME at http://H:P/v1 (sampling: …; loaded in 1.8s)"
	servingRe = regexp.MustCompile(`^serving (\S+) at http://`)
	listenRe  = regexp.MustCompile(`cannot listen on \S+ \(AddressInUse\)`)
)

// tag is the prefix of the server's own log lines on stdout.
const tag = "[tensorfold] "

// ParseLine reads one line of server output (fixtures lifecycle/,
// keyed/). The log goes to stdout with the "[tensorfold] " prefix;
// stderr gets "info: …" and "warning: …" notes, which are no errors (a
// jpeg-turbo warning comes on every start), and the line that explains
// a failed start, "tensorfold: …".
//
//	serving NAME at http://H:P/v1 (… loaded in 1.8s)   Ready
//	idle reload: the model is resident again            Ready (after an idle unload)
//	done <id> prompt=… tokens=… finish=…                RequestDone
//	shutting down (SIGINT): draining …                  Stopping
//	tensorfold: …, tensorfold serve: error: …           Error
//
// No line marks a request's start: the access-log line of a POST comes
// when its reply is sent, after "done" for a non-streamed one.
func (tf *TensorFold) ParseLine(line string, stderr bool) (engine.Event, bool) {
	body := strings.TrimSpace(line)
	if body == "" {
		return engine.Event{}, false
	}
	if msg, ok := strings.CutPrefix(body, tag); ok {
		ev := engine.Event{Line: msg}
		switch {
		case servingRe.MatchString(msg):
			ev.Kind, ev.Model = engine.EventReady, servingRe.FindStringSubmatch(msg)[1]
		case strings.HasPrefix(msg, "idle reload:"):
			ev.Kind = engine.EventReady
		case strings.HasPrefix(msg, "done "):
			ev.Kind = engine.EventRequestDone
		case strings.HasPrefix(msg, "shutting down"):
			ev.Kind = engine.EventStopping
		default:
			return engine.Event{}, false
		}
		return ev, true
	}
	if isErrorLine(body) {
		return engine.Event{Kind: engine.EventError, Line: body}, true
	}
	return engine.Event{}, false
}

// isErrorLine: what the server prints when it cannot start, "tensorfold:
// …" (fixtures failures/*.stderr) or the argument parser's "tensorfold
// serve: error: …".
func isErrorLine(body string) bool {
	return strings.HasPrefix(body, "tensorfold: ") || strings.HasPrefix(body, "tensorfold serve: error: ")
}

// Failure texts, matched case-insensitively as substrings.
var (
	// "tensorfold: --parallel 1 needs 13.0 GiB for its streams and
	// 512-row prompt scratch at a 131072-token context; 11.8 GiB remains
	// beside the weights and an 8 GiB margin: lower --parallel or
	// --context" (failures/no_memory: Qwen3.8-27B with images at 131K on
	// 48 GiB); the others are words of the native engine's sources
	// (lockstep.zig "OutOfDeviceMemory", lane hosts failing with
	// "OutOfMemory") and of "tensorfold info"'s fit check.
	memoryPatterns = []string{
		"lower --parallel or --context",
		"outofmemory",
		"outofdevicememory",
		"out of memory",
		"does not fit",
		"not enough memory",
	}
	modelPatterns = []string{
		"is not in the hugging face cache", // failures/no_model, offline_missing
		"no registered zig family",         // a model type it does not serve
	}
	// "--vision: the native engine reads images for GLM-5.3-Flash, … only,
	// not qwen3_5 checkpoints" (lifecycle/vision.stderr).
	visionPatterns = []string{"--vision: "}
)

// ClassifyFailure explains an exit from its code and output tail. Exit 0
// is a clean stop. A refused start exits 1 with one "tensorfold: …" line;
// a bad flag exits 2 with the usage and "tensorfold serve: error: …"
// (fixtures failures/).
func (tf *TensorFold) ClassifyFailure(code int, tail []string) engine.Failure {
	if code == 0 {
		return engine.Failure{}
	}
	var lines []string
	for _, t := range tail {
		if t = strings.TrimRight(t, "\r\n \t"); t != "" {
			lines = append(lines, t)
		}
	}
	// "tensorfold: cannot listen on 127.0.0.1:18090 (AddressInUse)", after
	// the model loaded (failures/port_busy). Another TensorFold on the
	// port reads the same; the supervisor's rescan finds it.
	for _, l := range lines {
		if listenRe.MatchString(l) {
			return engine.Failure{Kind: engine.FailPortBusy, Code: code, Line: l}
		}
	}
	errs := errorLines(lines)
	if len(errs) == 0 {
		errs = lines // a crash may print something else
	}
	for _, c := range []struct {
		kind     engine.FailureKind
		patterns []string
	}{
		{engine.FailNoVision, visionPatterns},
		{engine.FailMemory, memoryPatterns},
		{engine.FailModel, modelPatterns},
	} {
		if l := findLine(errs, c.patterns...); l != "" {
			return engine.Failure{Kind: c.kind, Code: code, Line: l}
		}
	}
	f := engine.Failure{Kind: engine.FailExit, Code: code}
	if e := errorLines(lines); len(e) > 0 {
		f.Line = e[0]
	} else if len(lines) > 0 {
		f.Line = lines[len(lines)-1]
	}
	return f
}

// errorLines are the error lines of lines (isErrorLine).
func errorLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if isErrorLine(strings.TrimSpace(l)) {
			out = append(out, l)
		}
	}
	return out
}

// findLine returns the first line containing any of the lowercase
// patterns, trying the patterns in order.
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
