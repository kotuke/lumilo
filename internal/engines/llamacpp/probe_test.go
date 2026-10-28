package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// newEngine returns an engine whose ps sees nothing, so probes of
// fixtures do not depend on the processes of this computer.
func newEngine() *LlamaCpp {
	e := New()
	e.ps = psListing()
	e.psPID = func(int) ([]byte, error) { return nil, nil }
	return e
}

// Every recorded reply parses: JSON bodies as JSON, /metrics as
// Prometheus text with the llamacpp: names Probe reads.
func TestFixturesParse(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*", "*.*.txt"))
	n := 0
	for _, path := range files {
		first, _ := os.ReadFile(path)
		if !strings.HasPrefix(string(first), "# HTTP ") || strings.HasSuffix(path, ".response.txt") {
			continue // logs and completion responses
		}
		n++
		f := readFixture(t, path)
		switch {
		case f.code == 0:
			if !strings.Contains(f.body, "Connection refused") {
				t.Errorf("%s: no reply, body %q", path, f.body)
			}
		case strings.HasPrefix(f.ctype, "application/json"):
			var v any
			if err := json.Unmarshal([]byte(f.body), &v); err != nil {
				t.Errorf("%s: %v", path, err)
			}
		case strings.HasPrefix(f.ctype, "text/plain; version=0.0.4"):
			m := enginekit.ParseProm([]byte(f.body)).Plain
			for _, name := range []string{"llamacpp:tokens_predicted_total", "llamacpp:tokens_predicted_seconds_total",
				"llamacpp:prompt_tokens_total", "llamacpp:prompt_seconds_total", "llamacpp:requests_processing", "llamacpp:requests_deferred"} {
				if _, ok := m[name]; !ok {
					t.Errorf("%s: no %s", path, name)
				}
			}
		default:
			t.Errorf("%s: content type %q", path, f.ctype)
		}
	}
	if n < 100 {
		t.Errorf("only %d fixtures", n)
	}
}

func TestProbeLoading(t *testing.T) {
	e := newEngine()
	// loading000: not listening yet.
	if s := e.Probe(context.Background(), engine.Target{Port: newStateServer(t, "lifecycle", "loading000").port}); s.Reachable || s.Ready {
		t.Errorf("loading000: %+v", s)
	}
	// loading002..007: /health, /props and /metrics answer 503 "Loading model".
	for i := 2; i <= 7; i++ {
		s := e.Probe(context.Background(), engine.Target{Port: newStateServer(t, "lifecycle", fmt.Sprintf("loading%03d", i)).port})
		if !s.Reachable || s.Ready || !s.Authorized || s.Model != "" || s.Completed != 0 {
			t.Errorf("loading%03d: %+v", i, s)
		}
	}
	s := e.Probe(context.Background(), engine.Target{Port: newStateServer(t, "lifecycle", "loading008").port})
	if !s.Reachable || !s.Ready || s.Model != fixtureModel || s.ContextTokens != 8192 {
		t.Errorf("loading008: %+v", s)
	}
	if s := e.Probe(context.Background(), engine.Target{Port: closedPort(t)}); s.Reachable || s.At.IsZero() {
		t.Errorf("closed port: %+v", s)
	}
}

// One port through the recorded life: two single requests, then three in
// parallel. Completed is synthesised from tokens_predicted_total.
func TestProbeSequence(t *testing.T) {
	e := newEngine()
	srv := newStateServer(t, "lifecycle", "ready")
	probe := func(state string) engine.Snapshot {
		srv.state = state
		return e.Probe(context.Background(), engine.Target{Port: srv.port})
	}

	s := probe("ready")
	if !s.Reachable || !s.Ready || !s.Authorized || s.Released || s.Broken {
		t.Fatalf("ready: %+v", s)
	}
	if s.Model != fixtureModel || s.ContextTokens != 8192 || s.Active != 0 || s.Queued != 0 || s.Completed != 0 || s.OutputTokens != 0 {
		t.Errorf("ready: %+v", s)
	}
	if s.MemoryLimit != 0 || s.DiskUsed != 0 || s.FirstToken != 0 || s.IdleRelease != 0 {
		t.Errorf("ready: values llama.cpp does not report: %+v", s)
	}

	// While the first request runs its tokens are not counted yet; the
	// prompt is (18 tokens in 0.125 s).
	for i := 0; i <= 6; i++ {
		s = probe(fmt.Sprintf("generating%d", i))
		if s.Active != 1 || s.Queued != 0 || s.OutputTokens != 0 || s.Completed != 0 {
			t.Errorf("generating%d: %+v", i, s)
		}
		if s.PrefillTokens != 18 || s.PrefillTime != 124773*time.Microsecond {
			t.Errorf("generating%d: prefill %d in %v", i, s.PrefillTokens, s.PrefillTime)
		}
	}
	s = probe("after_gen1")
	if s.Active != 0 || s.Completed != 1 || s.OutputTokens != 600 || s.DecodeTime != 1673400*time.Microsecond {
		t.Errorf("after_gen1: %+v", s)
	}
	if s = probe("after_gen1"); s.Completed != 1 {
		t.Errorf("after_gen1 again: %d", s.Completed)
	}
	if s = probe("after_gen2"); s.Completed != 2 || s.OutputTokens != 1200 {
		t.Errorf("after_gen2: %+v", s)
	}
	if s = probe("parallel"); s.Active != 3 || s.Completed != 2 {
		t.Errorf("parallel: %+v", s)
	}
	// Three requests finished between two probes count as one.
	if s = probe("after_parallel"); s.Active != 0 || s.Completed != 3 || s.OutputTokens != 2100 {
		t.Errorf("after_parallel: %+v", s)
	}
	// A new server on the port: the counter starts again.
	if s = probe("ready"); s.Completed != 0 {
		t.Errorf("restart: %+v", s)
	}
	if s = probe("after_gen2"); s.Completed != 1 {
		t.Errorf("after restart: %+v", s)
	}
}

func TestProbeSleep(t *testing.T) {
	e := newEngine()
	srv := newStateServer(t, "lifecycle", "sleep_ready")
	t0 := engine.Target{Port: srv.port}
	if s := e.Probe(context.Background(), t0); s.Released || !s.Ready || s.ContextTokens != 4096 {
		t.Errorf("sleep_ready: %+v", s)
	}
	srv.state = "sleeping"
	s := e.Probe(context.Background(), t0)
	if !s.Released || !s.Ready || !s.Reachable || s.Active != 0 || s.OutputTokens != 8 {
		t.Errorf("sleeping: %+v", s)
	}
	srv.state = "woken"
	if s := e.Probe(context.Background(), t0); s.Released {
		t.Errorf("woken: %+v", s)
	}
}

// The sleep setting a server was started with reaches the snapshot, asleep
// or awake, so the panel can count down and the supervisor can tell when
// the server falls asleep; "never" (0) leaves no countdown.
func TestProbeIdleRelease(t *testing.T) {
	e := newEngine()
	srv := newStateServer(t, "lifecycle", "sleep_ready")
	cfg := engine.Config{Model: "/m/Tiny-Q4_K_M.gguf", Port: srv.port, IdleRelease: 5 * time.Minute}
	argv, _ := e.ServeCommand(cfg)
	if i := slices.Index(argv, "--sleep-idle-seconds"); i < 0 || argv[i+1] != "300" {
		t.Fatalf("argv %q: no --sleep-idle-seconds 300", argv)
	}
	t0 := engine.Target{Port: srv.port}
	if s := e.Probe(context.Background(), t0); s.IdleRelease != 5*time.Minute || s.Released {
		t.Errorf("awake: %+v", s)
	}
	srv.state = "sleeping"
	if s := e.Probe(context.Background(), t0); s.IdleRelease != 5*time.Minute || !s.Released {
		t.Errorf("asleep: %+v", s)
	}
	cfg.IdleRelease = 0 // Settings: Never
	argv, _ = e.ServeCommand(cfg)
	if slices.Contains(argv, "--sleep-idle-seconds") {
		t.Errorf("argv %q has --sleep-idle-seconds for Never", argv)
	}
	if s := e.Probe(context.Background(), t0); s.IdleRelease != 0 || !s.Released {
		t.Errorf("never: %+v", s)
	}
}

func TestProbeAPIKey(t *testing.T) {
	e := newEngine()
	for label, authorized := range map[string]bool{"nokey": false, "wrongkey": false, "withkey": true} {
		routes := map[string]http.HandlerFunc{"/slots": noSlots(t)}
		for _, ep := range []string{"health", "props", "metrics"} {
			routes["/"+ep] = replay(readFixture(t, filepath.Join("testdata", "failures", "apikey_"+label+"."+ep+".txt")))
		}
		s := e.Probe(context.Background(), engine.Target{Port: serve(t, routes)})
		if !s.Reachable || !s.Ready || s.Authorized != authorized {
			t.Errorf("%s: %+v", label, s)
		}
		if authorized && (s.Model != fixtureModel || s.ContextTokens != 4096) {
			t.Errorf("%s: %+v", label, s)
		}
		if !authorized && (s.Model != "" || s.MemoryUsed != 0) {
			t.Errorf("%s: read past a 401: %+v", label, s)
		}
	}
	// The key is sent as a bearer token.
	var got string
	port := serve(t, map[string]http.HandlerFunc{"/health": func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}})
	e.Probe(context.Background(), engine.Target{Port: port, APIKey: "k1"})
	if got != "Bearer k1" {
		t.Errorf("authorization %q", got)
	}
}

// A server started without --metrics answers 501 there: no counters.
func TestProbeNoMetrics(t *testing.T) {
	e := newEngine()
	routes := map[string]http.HandlerFunc{"/slots": noSlots(t)}
	for _, ep := range []string{"health", "props"} {
		routes["/"+ep] = replay(readFixture(t, filepath.Join("testdata", "lifecycle", "after_gen1."+ep+".txt")))
	}
	routes["/metrics"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, `{"error":{"code":501,"message":"This server does not support metrics endpoint. Start it with `+"`--metrics`"+`","type":"not_supported_error"}}`)
	}
	s := e.Probe(context.Background(), engine.Target{Port: serve(t, routes)})
	if !s.Ready || !s.Authorized || s.Model != fixtureModel || s.OutputTokens != 0 || s.Completed != 0 {
		t.Errorf("%+v", s)
	}
}

func TestProbeMemory(t *testing.T) {
	srv := newStateServer(t, "lifecycle", "ready")
	p := strconv.Itoa(srv.port)
	e := newEngine()
	var full, single int
	e.ps = func() ([]byte, error) {
		full++
		return []byte("  1 0 100 /sbin/launchd\n 42 1 4606176 /opt/homebrew/bin/llama-server -m /m.gguf --port " + p + "\n"), nil
	}
	e.psPID = func(pid int) ([]byte, error) {
		single++
		if pid != 42 {
			t.Errorf("ps -p %d", pid)
		}
		return []byte(" 42 1 4700000 /opt/homebrew/bin/llama-server -m /m.gguf --port " + p + "\n"), nil
	}
	tg := engine.Target{Port: srv.port}
	if s := e.Probe(context.Background(), tg); s.MemoryUsed != 4606176*1024 {
		t.Errorf("first: %d", s.MemoryUsed)
	}
	if s := e.Probe(context.Background(), tg); s.MemoryUsed != 4700000*1024 {
		t.Errorf("second: %d", s.MemoryUsed)
	}
	if full != 1 || single != 1 {
		t.Errorf("ps runs: %d full, %d single", full, single)
	}
	// The process is gone: the next probe looks again and finds nothing.
	e.psPID = func(int) ([]byte, error) { return nil, fmt.Errorf("exit status 1") }
	e.ps = psListing("  1 0 100 /sbin/launchd")
	if s := e.Probe(context.Background(), tg); s.MemoryUsed != 0 {
		t.Errorf("gone: %d", s.MemoryUsed)
	}
}

func TestReleaseRestore(t *testing.T) {
	e := newEngine()
	if err := e.Release(context.Background(), engine.Target{Port: 1}); err == nil || err != errNoRelease {
		t.Errorf("release: %v", err)
	}
	var body, auth string
	port := serve(t, map[string]http.HandlerFunc{"/v1/completions": func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, auth = string(b), r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}})
	if err := e.Restore(context.Background(), engine.Target{Port: port, APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if body != `{"prompt":"a","max_tokens":1}` || auth != "Bearer k" {
		t.Errorf("body %q auth %q", body, auth)
	}
	fail := serve(t, map[string]http.HandlerFunc{"/v1/completions": func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid API Key","type":"authentication_error","code":401}}`)
	}})
	if err := e.Restore(context.Background(), engine.Target{Port: fail}); err == nil || !strings.Contains(err.Error(), "HTTP 401: Invalid API Key") {
		t.Errorf("restore error: %v", err)
	}
	if err := e.Restore(context.Background(), engine.Target{Port: closedPort(t)}); err == nil {
		t.Error("closed port: no error")
	}
}
