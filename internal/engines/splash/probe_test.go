package splash

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

const fixtureModel = "orcarouter/OrcaCyber-Nano-1.0:Mix-15.7G"

func probeState(t *testing.T, state string) engine.Snapshot {
	t.Helper()
	port := lifecycleServer(t, state)
	return New().Probe(context.Background(), engine.Target{Port: port})
}

func near(a, b, tol time.Duration) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestProbeReadyIdle(t *testing.T) {
	for _, state := range []string{"ready", "loading010"} {
		s := probeState(t, state)
		if !s.Reachable || !s.Ready || !s.Authorized || s.Broken || s.Released {
			t.Fatalf("%s: flags %+v", state, s)
		}
		if s.Model != fixtureModel || s.ContextTokens != 262144 {
			t.Errorf("%s: model %q context %d", state, s.Model, s.ContextTokens)
		}
		if s.MemoryUsed != 19687587840 || s.MemoryLimit != 39127154688 {
			t.Errorf("%s: memory %d / %d", state, s.MemoryUsed, s.MemoryLimit)
		}
		if s.DiskUsed != 0 || s.DiskLimit != 34359738368 {
			t.Errorf("%s: disk %d / %d", state, s.DiskUsed, s.DiskLimit)
		}
		if s.Pressure != engine.PressureWarning {
			t.Errorf("%s: pressure %v", state, s.Pressure)
		}
		if s.IdleRelease != 600*time.Second {
			t.Errorf("%s: idle release %v", state, s.IdleRelease)
		}
		if s.Active != 0 || s.Queued != 0 || s.Completed != 0 || s.OutputTokens != 0 || s.FirstToken != 0 {
			t.Errorf("%s: activity %+v", state, s)
		}
	}
}

// Active is scheduler prefilling+decoding: 1 through the whole generation
// (generating0..7) and 0 when idle (ready, after_gen*, released).
func TestProbeActiveDuringGeneration(t *testing.T) {
	var lastTokens int64
	for i := 0; i < 8; i++ {
		s := probeState(t, fmt.Sprintf("generating%d", i))
		if s.Active != 1 || s.Queued != 0 || !s.Ready {
			t.Errorf("generating%d: active %d queued %d ready %v", i, s.Active, s.Queued, s.Ready)
		}
		if s.OutputTokens <= lastTokens {
			t.Errorf("generating%d: output tokens %d did not grow from %d", i, s.OutputTokens, lastTokens)
		}
		lastTokens = s.OutputTokens
		if s.DecodeTime <= 0 {
			t.Errorf("generating%d: decode time %v", i, s.DecodeTime)
		}
	}
	for _, state := range []string{"ready", "after_gen1", "after_gen2", "after_gen3", "after_gen4", "after_gen5", "released"} {
		if s := probeState(t, state); s.Active != 0 || s.Queued != 0 {
			t.Errorf("%s: active %d queued %d, want idle", state, s.Active, s.Queued)
		}
	}
}

func TestProbeAfterGeneration(t *testing.T) {
	s := probeState(t, "after_gen5")
	if s.Completed != 5 || s.OutputTokens != 2528 || s.PrefillTokens != 154 {
		t.Errorf("counters %d %d %d", s.Completed, s.OutputTokens, s.PrefillTokens)
	}
	if !near(s.DecodeTime, 48613349260*time.Nanosecond, time.Microsecond) {
		t.Errorf("decode time %v", s.DecodeTime)
	}
	if !near(s.PrefillTime, 456796541*time.Nanosecond, time.Microsecond) {
		t.Errorf("prefill time %v", s.PrefillTime)
	}
	if !near(s.FirstToken, 80556167*time.Nanosecond, time.Microsecond) {
		t.Errorf("first token %v", s.FirstToken)
	}
	// Speed as the panel computes it: the fixture says 52.0 tok/s.
	if tps := float64(s.OutputTokens) / s.DecodeTime.Seconds(); tps < 51.99 || tps > 52.01 {
		t.Errorf("tok/s %.3f", tps)
	}
}

func TestProbeReleased(t *testing.T) {
	s := probeState(t, "released")
	if !s.Released || !s.Ready || s.Pressure != engine.PressureNormal {
		t.Errorf("released state %+v", s)
	}
	if s.MemoryUsed != 2052521984 {
		t.Errorf("memory used %d", s.MemoryUsed)
	}
	if r := probeState(t, "after_gen5"); r.Released {
		t.Error("after_gen5 reported released")
	}
}

// restoringServer is the "released" server with /status changed to say that
// the weights are coming back. No real reply of that moment was recorded
// (the Splash fixtures have restoring false throughout), so it is built from
// the real released /status body with only the weights flags replaced.
func restoringServer(t *testing.T, flags string) int {
	t.Helper()
	routes := map[string]http.HandlerFunc{}
	for _, ep := range []string{"health", "ready", "status", "metrics"} {
		f := readFixture(t, filepath.Join("testdata", "lifecycle", "released."+ep+".txt"))
		if ep == "status" {
			const was = `"released":true,"restoring":false`
			if !strings.Contains(f.body, was) {
				t.Fatal("released.status.txt has no weights flags to replace")
			}
			f.body = strings.Replace(f.body, was, flags, 1)
		}
		routes["/"+ep] = replay(f)
	}
	return serve(t, routes)
}

// Weights on their way back: Restoring is set and Released is not, whether
// /status keeps released true meanwhile or not.
func TestProbeRestoring(t *testing.T) {
	for _, flags := range []string{
		`"released":true,"restoring":true`,
		`"released":false,"restoring":true`,
	} {
		s := New().Probe(context.Background(), engine.Target{Port: restoringServer(t, flags)})
		if !s.Reachable || !s.Authorized || !s.Restoring || s.Released {
			t.Errorf("%s: %+v", flags, s)
		}
		if s.Model != fixtureModel || s.MemoryLimit != 39127154688 {
			t.Errorf("%s: model %q, limit %d", flags, s.Model, s.MemoryLimit)
		}
	}
	if s := probeState(t, "released"); s.Restoring {
		t.Error("released reports restoring")
	}
	if s := probeState(t, "ready"); s.Restoring {
		t.Error("ready reports restoring")
	}
}

// Loading and stopped servers: the recorded fixtures say no reply at all.
func TestProbeUnreachable(t *testing.T) {
	for _, state := range []string{"loading000", "after_stop"} {
		if f := readFixture(t, filepath.Join("testdata", "lifecycle", state+".health.txt")); f.code != 0 {
			t.Fatalf("%s fixture has a reply", state)
		}
	}
	s := New().Probe(context.Background(), engine.Target{Port: closedPort(t)})
	if s.Reachable || s.Ready || s.Authorized {
		t.Errorf("closed port: %+v", s)
	}
	if s.At.IsZero() {
		t.Error("At not set")
	}
}

// A loading server accepts the connection and never answers.
func TestProbeHangReturnsFast(t *testing.T) {
	hang := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}
	port := serve(t, map[string]http.HandlerFunc{"/": hang})
	start := time.Now()
	s := New().Probe(context.Background(), engine.Target{Port: port})
	if el := time.Since(start); el > 700*time.Millisecond {
		t.Errorf("probe took %v", el)
	}
	if s.Reachable {
		t.Errorf("hanging server reported reachable: %+v", s)
	}
}

// With a key, /health and /ready are open and /status, /metrics answer 401.
func TestProbeKey(t *testing.T) {
	dir := filepath.Join("testdata", "failures")
	nokey := func(name string) fixture { return readFixture(t, filepath.Join(dir, "apikey_nokey."+name+".txt")) }
	// apikey_withkey.status.txt was recorded cut short (HTTP 200, partial
	// body), so the authorized reply is the full lifecycle one.
	if c := readFixture(t, filepath.Join(dir, "apikey_withkey.status.txt")).code; c != 200 {
		t.Fatalf("with-key status fixture is HTTP %d", c)
	}
	withKey := readFixture(t, filepath.Join("testdata", "lifecycle", "ready.status.txt"))
	const key = "secret-key"
	guarded := func(name string, ok fixture) http.HandlerFunc {
		deny := nokey(name)
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer "+key {
				replay(ok)(w, r)
				return
			}
			replay(deny)(w, r)
		}
	}
	port := serve(t, map[string]http.HandlerFunc{
		"/health":  replay(nokey("health")),
		"/ready":   replay(nokey("ready")),
		"/status":  guarded("status", withKey),
		"/metrics": guarded("metrics", fixture{code: 404, body: "not recorded"}),
	})

	s := New().Probe(context.Background(), engine.Target{Port: port})
	if !s.Reachable || !s.Ready || s.Authorized {
		t.Errorf("no key: %+v", s)
	}
	if s.Model != "" || s.MemoryLimit != 0 || s.Completed != 0 || s.Broken {
		t.Errorf("no key: metrics should stay zero: %+v", s)
	}

	s = New().Probe(context.Background(), engine.Target{Port: port, APIKey: "wrong"})
	if !s.Reachable || s.Authorized {
		t.Errorf("wrong key: %+v", s)
	}

	s = New().Probe(context.Background(), engine.Target{Port: port, APIKey: key})
	if !s.Reachable || !s.Authorized || s.Model != fixtureModel || s.ContextTokens != 262144 {
		t.Errorf("right key: %+v", s)
	}
}

// A crash-looped engine: the process lives, /ready is 503, transport.stopped.
func TestProbeBroken(t *testing.T) {
	dir := filepath.Join("testdata", "lifecycle")
	status := readFixture(t, filepath.Join(dir, "ready.status.txt"))
	if !strings.Contains(status.body, `"stopped":false`) {
		t.Fatal("fixture lacks transport.stopped")
	}
	status.body = strings.Replace(status.body, `"stopped":false`, `"stopped":true`, 1)
	status.body = strings.Replace(status.body, `"ready":true`, `"ready":false`, 1)
	routes := map[string]http.HandlerFunc{
		"/health":  replay(readFixture(t, filepath.Join(dir, "ready.health.txt"))),
		"/ready":   replay(fixture{503, `{"status":"unavailable"}`}),
		"/status":  replay(status),
		"/metrics": replay(readFixture(t, filepath.Join(dir, "ready.metrics.txt"))),
	}
	s := New().Probe(context.Background(), engine.Target{Port: serve(t, routes)})
	if !s.Reachable || s.Ready || !s.Broken {
		t.Errorf("broken server: %+v", s)
	}

	// 503 without transport.stopped is a recovering engine, not broken.
	routes["/status"] = replay(readFixture(t, filepath.Join(dir, "ready.status.txt")))
	s = New().Probe(context.Background(), engine.Target{Port: serve(t, routes)})
	if s.Ready || s.Broken {
		t.Errorf("recovering server: %+v", s)
	}
}

func TestIdleReleaseFromStatus(t *testing.T) {
	cases := map[string]time.Duration{
		`600`: 600 * time.Second, `0`: 0, `null`: 0, `"off"`: 0, `-1`: 0, `1.5`: 1500 * time.Millisecond, ``: 0,
	}
	for in, want := range cases {
		if got := idleReleaseFromStatus([]byte(in)); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

func TestReleaseAndRestore(t *testing.T) {
	var got []string
	handler := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			got = append(got, fmt.Sprintf("%s %s auth=%q body=%s", r.Method, r.URL.Path, r.Header.Get("Authorization"), b))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	release := readFixture(t, filepath.Join("testdata", "lifecycle", "release.response.txt"))
	port := serve(t, map[string]http.HandlerFunc{
		"/v1/memory/release": handler(release.code, release.body),
		"/v1/completions":    handler(200, `{"choices":[]}`),
	})
	tg := engine.Target{Port: port, APIKey: "k"}
	e := New()
	if err := e.Release(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /v1/memory/release auth="Bearer k" body=`,
		`POST /v1/completions auth="Bearer k" body={"prompt":"a","max_tokens":1}`,
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("requests:\n got %q\nwant %q", got, want)
	}

	busy := serve(t, map[string]http.HandlerFunc{"/v1/memory/release": handler(409,
		`{"error":{"message":"the engine is serving requests or restoring its weights; it releases them only while idle","type":"invalid_request_error","code":"engine_busy"}}`)})
	err := e.Release(context.Background(), engine.Target{Port: busy})
	if err == nil || !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "only while idle") {
		t.Errorf("busy release: %v", err)
	}
	if err := e.Release(context.Background(), engine.Target{Port: closedPort(t)}); err == nil {
		t.Error("release to a closed port succeeded")
	}
}

func TestChatURL(t *testing.T) {
	if got := New().ChatURL(engine.Target{Port: 8123}); got != "http://127.0.0.1:8123/" {
		t.Errorf("chat url %q", got)
	}
}

func TestFixturesAreScrubbed(t *testing.T) {
	err := filepath.WalkDir("testdata", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(path)
		text := string(b)
		if strings.Contains(text, "/Users/kotuke") || strings.Contains(text, "kotuke") {
			t.Errorf("%s: personal path", path)
		}
		for _, part := range strings.Split(text, "/var/folders/")[1:] {
			if !strings.HasPrefix(part, "xx/T/") {
				t.Errorf("%s: unscrubbed temp path", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
