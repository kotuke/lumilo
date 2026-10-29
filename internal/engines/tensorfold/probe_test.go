package tensorfold

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// fixtureServer replays /health, /memory and /metrics from files of
// testdata: health is "code file".
func fixtureServer(t *testing.T, key string, health string, healthCode int, memory, metrics string) int {
	t.Helper()
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		if key == "" {
			return h
		}
		return keyed(key, h)
	}
	return serve(t, map[string]http.HandlerFunc{
		"/health":  reply(t, healthCode, "testdata/"+health),
		"/memory":  wrap(reply(t, 200, "testdata/"+memory)),
		"/metrics": wrap(reply(t, 200, "testdata/"+metrics)),
	})
}

func TestProbeIdle(t *testing.T) {
	tf := newTest(t)
	port := fixtureServer(t, "", "health-idle.json", 200, "memory.json", "metrics-after.txt")
	tf.remember(port, fixtureRepo, "Qwen3.5-2B-MLX-4bit", 30*time.Second, false)
	s := tf.Probe(context.Background(), engine.Target{Port: port})
	if !s.Reachable || !s.Ready || s.Released || !s.Authorized || s.Broken {
		t.Fatalf("snap = %+v", s)
	}
	if s.Model != fixtureRepo || s.ContextTokens != 262144 || s.IdleRelease != 30*time.Second {
		t.Errorf("model %q, context %d, idle %v", s.Model, s.ContextTokens, s.IdleRelease)
	}
	// metrics-after.txt: one request of 22 prompt and 1500 output tokens.
	if s.Completed != 1 || s.OutputTokens != 1500 || s.PrefillTokens != 22 || s.Active != 0 || s.Queued != 0 {
		t.Errorf("counters = %+v", s)
	}
	// Decode time is Lumilo's live clock (liveDecode): nothing ran while
	// it watched.
	if s.DecodeTime != 0 || s.PrefillTime != 21416*time.Microsecond {
		t.Errorf("times: decode %v, prefill %v", s.DecodeTime, s.PrefillTime)
	}
	// The one TTFT (0.022 s) is in the 0.01–0.05 bucket: the median is
	// its middle by interpolation, 0.03 s.
	if s.FirstToken != 30*time.Millisecond {
		t.Errorf("first token = %v", s.FirstToken)
	}
	if s.MemoryUsed != 4995025656 {
		t.Errorf("memory = %d", s.MemoryUsed)
	}
}

func TestProbeBusy(t *testing.T) {
	tf := newTest(t)
	port := fixtureServer(t, "", "health-idle.json", 200, "memory.json", "metrics-busy.txt")
	s := tf.Probe(context.Background(), engine.Target{Port: port})
	// The 1029 tokens in flight count already.
	if s.Active != 1 || s.OutputTokens != 1029 || s.Completed != 0 {
		t.Errorf("busy = %+v", s)
	}
	// Not started by Lumilo nor found: the served name.
	if s.Model != "Qwen3.5-2B-MLX-4bit" {
		t.Errorf("model = %q", s.Model)
	}
}

func TestProbeUnloaded(t *testing.T) {
	tf := newTest(t)
	port := fixtureServer(t, "", "health-unloaded.json", 503, "memory-unloaded.json", "metrics-unloaded.txt")
	s := tf.Probe(context.Background(), engine.Target{Port: port})
	if !s.Reachable || !s.Ready || !s.Released || !s.Authorized {
		t.Fatalf("snap = %+v", s)
	}
	// The counters survive the unload; the footprint is what is left.
	if s.Completed != 1 || s.OutputTokens != 1500 || s.MemoryUsed != 258671696 {
		t.Errorf("unloaded = %+v", s)
	}
}

func TestProbeKeyed(t *testing.T) {
	tf := newTest(t)
	port := fixtureServer(t, "test-key-123", "keyed/health-unloaded.json", 503, "keyed/memory-unloaded.json", "keyed/metrics-unloaded.txt")
	s := tf.Probe(context.Background(), engine.Target{Port: port, APIKey: "test-key-123"})
	if !s.Reachable || !s.Released || !s.Authorized || s.ContextTokens != 262144 {
		t.Errorf("keyed = %+v", s)
	}
	s = tf.Probe(context.Background(), engine.Target{Port: port, APIKey: "wrong"})
	if !s.Reachable || s.Authorized || s.Completed != 0 {
		t.Errorf("wrong key = %+v", s)
	}
}

func TestProbeDrainingAndDown(t *testing.T) {
	tf := newTest(t)
	port := fixtureServer(t, "", "keyed/health-draining.json", 503, "memory.json", "metrics-after.txt")
	s := tf.Probe(context.Background(), engine.Target{Port: port})
	if !s.Reachable || s.Ready || s.Released {
		t.Errorf("draining = %+v", s)
	}
	s = tf.Probe(context.Background(), engine.Target{Port: closedPort(t)})
	if s.Reachable {
		t.Errorf("closed port = %+v", s)
	}
}

func TestHistMedian(t *testing.T) {
	for _, c := range []struct {
		b    map[string]float64
		want time.Duration
	}{
		{map[string]float64{}, 0},
		{map[string]float64{"0.1": 0, "+Inf": 0}, 0},
		{map[string]float64{"0.1": 2, "0.5": 4, "+Inf": 4}, 100 * time.Millisecond},
		{map[string]float64{"0.1": 1, "0.5": 3, "+Inf": 4}, 300 * time.Millisecond},
		{map[string]float64{"0.1": 0, "0.5": 0, "+Inf": 4}, 500 * time.Millisecond},
	} {
		if got := histMedian(c.b); got != c.want {
			t.Errorf("%v: %v, want %v", c.b, got, c.want)
		}
	}
}

func TestReleaseRestore(t *testing.T) {
	tf := newTest(t)
	if err := tf.Release(context.Background(), engine.Target{Port: 1}); err == nil {
		t.Error("Release worked")
	}
	var body string
	port := serve(t, map[string]http.HandlerFunc{
		"/v1/completions": keyed("k", func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			fmt.Fprint(w, `{"object": "text_completion"}`)
		}),
	})
	if err := tf.Restore(context.Background(), engine.Target{Port: port, APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"max_tokens":1`) {
		t.Errorf("body = %s", body)
	}
	err := tf.Restore(context.Background(), engine.Target{Port: port, APIKey: "bad"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401: invalid API key") {
		t.Errorf("err = %v", err)
	}
}

func TestFindRunning(t *testing.T) {
	tf := newTest(t)
	health := func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status": "ok"}`) }
	p1 := serve(t, map[string]http.HandlerFunc{"/health": health})
	p2 := serve(t, map[string]http.HandlerFunc{"/health": health})
	dead := closedPort(t)
	tf.ps = func() ([]byte, error) {
		return []byte(fmt.Sprintf(`  101 /opt/homebrew/bin/tensorfold serve %[4]s --port %[1]d --idle-unload 60 --vision
  102 tensorfold-native serve --no-drafts --port=%[2]d other/Model-MLX
  103 /opt/homebrew/bin/tensorfold serve a/b --port %[3]d
  104 /opt/homebrew/bin/tensorfold pull a/b
  105 /usr/bin/python3 tensorfold serve a/b --port %[1]d
  106 /opt/homebrew/bin/tensorfold serve a/c --port %[2]d
`, p1, p2, dead, fixtureRepo)), nil
	}
	tf.listeners = func(port int) ([]int, error) {
		switch port {
		case p1:
			return []int{101}, nil
		case p2:
			return []int{102}, nil
		}
		return nil, nil
	}
	got, err := tf.FindRunning()
	if err != nil {
		t.Fatal(err)
	}
	want := []engine.Running{{PID: 101, Port: p1, Model: fixtureRepo}, {PID: 102, Port: p2, Model: "other/Model-MLX"}}
	if p2 < p1 {
		want[0], want[1] = want[1], want[0]
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("running = %+v, want %+v", got, want)
	}
	if m := tf.ports[p1]; m.model != fixtureRepo || m.idle != time.Minute {
		t.Errorf("memo = %+v", m)
	}
}

// The model's reasoning efforts are asked once, for Pi's levels.
func TestProbeEfforts(t *testing.T) {
	tf := newTest(t)
	asks := 0
	port := serve(t, map[string]http.HandlerFunc{
		"/health":  reply(t, 200, "testdata/health-idle.json"),
		"/memory":  reply(t, 200, "testdata/memory.json"),
		"/metrics": reply(t, 200, "testdata/metrics-idle.txt"),
		"/v1/models": func(w http.ResponseWriter, r *http.Request) {
			asks++
			fmt.Fprint(w, `{"object": "list", "data": [{"id": "Qwen3.8-27B-MLX-4bit", "reasoning": {"default_enabled": true, "supported_efforts": ["xhigh", "medium", "low", "none"]}}]}`)
		},
	})
	tf.remember(port, "TensorFold/Qwen3.8-27B-MLX-4bit", "Qwen3.8-27B-MLX-4bit", 0, false)
	tf.Probe(context.Background(), engine.Target{Port: port})
	tf.Probe(context.Background(), engine.Target{Port: port})
	if asks != 1 || strings.Join(tf.ports[port].efforts, ",") != "xhigh,medium,low,none" {
		t.Errorf("asks %d, efforts %v", asks, tf.ports[port].efforts)
	}
	_, env := tf.AgentCommand("pi", engine.Target{Port: port})
	if len(env) == 0 {
		t.Fatal("no pi command")
	}
}

// The live decode clock runs over the probe intervals with tokens in
// flight at either end, never goes back, and starts over with a new start.
func TestLiveDecode(t *testing.T) {
	tf := newTest(t)
	t0 := time.Unix(1000, 0)
	sec := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	for _, c := range []struct {
		at      int
		running int64
		want    time.Duration
	}{
		{0, 0, 0},             // idle
		{1, 0, 0},             // idle
		{2, 300, time.Second}, // started within (1, 2]
		{3, 600, 2 * time.Second},
		{4, 0, 3 * time.Second},  // ended within (3, 4]
		{5, 0, 3 * time.Second},  // idle again
		{6, 50, 4 * time.Second}, // the next request
	} {
		if got := tf.liveDecode(8000, sec(c.at), c.running); got != c.want {
			t.Errorf("t=%d running=%d: %v, want %v", c.at, c.running, got, c.want)
		}
	}
	tf.remember(8000, fixtureRepo, "Qwen3.5-2B-MLX-4bit", 0, false)
	if got := tf.liveDecode(8000, sec(7), 10); got != 0 {
		t.Errorf("after a new start: %v", got)
	}
}
