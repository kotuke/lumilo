package tensorfold

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// Probe requests are short: the supervisor probes every second.
const probeTimeout = 400 * time.Millisecond

// errNoRelease is Release's answer: see Release.
var errNoRelease = errors.New("tensorfold: the server cannot unload the model on request; it unloads by itself after --idle-unload")

// portMemo is what the engine keeps per port between calls: what the
// server was started with, which no route reports.
type portMemo struct {
	model string        // Model.ID ("owner/repo"); the server reports only the repository's name
	idle  time.Duration // IdleRelease
	// For AgentCommand: the name the server serves the model by (the
	// repository's name, /v1/models), its context window (from /memory, 0
	// until a probe read it) and whether it reads images (--vision).
	served  string
	context int
	vision  bool
	// efforts are the reasoning efforts /v1/models lists for the model
	// ("supported_efforts", fixture models.json of the 27B); asked once
	// per server, the first time a probe is authorized (asked).
	efforts []string
	asked   bool
	// The live decode clock (liveDecode): the time spent while tokens
	// were being generated, summed between probes, and the last probe's
	// time and tokens in flight.
	decoding time.Duration
	lastAt   time.Time
	lastRun  int64
}

func (tf *TensorFold) memo(port int) *portMemo {
	m := tf.ports[port]
	if m == nil {
		m = &portMemo{}
		tf.ports[port] = m
	}
	return m
}

// remember records what a server on port was started with: model, the
// name it serves it by, its idle unload and whether it reads images.
func (tf *TensorFold) remember(port int, model, served string, idle time.Duration, vision bool) {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	m := tf.memo(port)
	m.model, m.served, m.idle, m.vision = model, served, idle, vision
	m.efforts, m.asked = nil, false
	m.decoding, m.lastAt, m.lastRun = 0, time.Time{}, 0
}

// liveDecode advances the decode clock of the server on port to now, with
// running tokens in flight, and returns it. TensorFold adds a request's
// tokens and decode time to generation_tokens_total and
// request_decode_seconds_sum only when it ends (fixture metrics-busy.txt:
// 0 and 0 with 1029 generated); generation_tokens_running counts the
// tokens of the requests in flight. So the clock runs over every probe
// interval in which tokens were in flight at either end: wall time while
// the server generates, which with several requests at once is their
// common time and makes the speed theirs together. It never goes back,
// so the speed meter sees no reset when a request ends.
func (tf *TensorFold) liveDecode(port int, now time.Time, running int64) time.Duration {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	m := tf.memo(port)
	if !m.lastAt.IsZero() && (m.lastRun > 0 || running > 0) && now.After(m.lastAt) {
		m.decoding += now.Sub(m.lastAt)
	}
	m.lastAt, m.lastRun = now, running
	return m.decoding
}

// servedName is the name "tensorfold serve" serves model by without
// --name: the repository's name ("Qwen3.5-2B-MLX-4bit" of
// mlx-community/Qwen3.5-2B-MLX-4bit, fixture models.json).
func servedName(model string) string {
	if _, name, ok := strings.Cut(model, "/"); ok {
		return name
	}
	return model
}

// healthDoc is GET /health (api.md "Health"). With keys configured the
// body is only {"status": "ok"}.
type healthDoc struct {
	Status string `json:"status"`
	Model  string `json:"model"`
}

// memoryDoc is the part of GET /memory we read (fixture memory.json).
type memoryDoc struct {
	ContextWindow int `json:"context_window"`
	Process       struct {
		Footprint int64 `json:"physical_footprint_bytes"`
	} `json:"process"`
	// Device is there only while the engine is resident (fixture
	// memory-unloaded.json has none).
	Device *struct {
		Active int64 `json:"active_bytes"`
	} `json:"device"`
}

// Probe fetches /health, /memory and /metrics at once.
//
//   - /health is public. 200 {"status": "ok"} while the model is resident;
//     503 {"status": "unavailable"} after an idle unload, until a request
//     reloads it (fixtures health-unloaded.json, keyed/health-unloaded.json);
//     503 {"status": "draining"} while it stops (keyed/health-draining.json).
//     The server listens only once the model is loaded (failures/port_busy:
//     the model loaded, then "cannot listen"), so there is no loading
//     answer.
//   - /memory and /metrics need the key when there is one (401 without it,
//     keyed/health.txt) and answer while the model is unloaded too.
//   - Counters (metrics-after.txt): requests_running and _waiting;
//     request_latency_seconds_count counts finished requests;
//     generation_tokens_total, prompt_tokens_total and the sums of
//     request_decode_seconds and request_prefill_seconds move when a
//     request ends; time_to_first_token_seconds is a histogram. The output
//     tokens include those in flight and the decode time is the live
//     clock of liveDecode, so the speed shows while a request runs.
//     MemoryUsed is process_footprint_bytes, the process's physical
//     footprint with its Metal buffers.
func (tf *TensorFold) Probe(ctx context.Context, t engine.Target) engine.Snapshot {
	var health, memory, metrics enginekit.Reply
	var wg sync.WaitGroup
	for path, dst := range map[string]*enginekit.Reply{"/health": &health, "/memory": &memory, "/metrics": &metrics} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*dst = tf.request(ctx, t, http.MethodGet, path, nil, probeTimeout)
		}()
	}
	wg.Wait()

	snap := engine.Snapshot{At: time.Now()}
	var h healthDoc
	if health.Code != http.StatusOK && health.Code != http.StatusServiceUnavailable || json.Unmarshal(health.Body, &h) != nil {
		return snap
	}
	snap.Reachable = true
	switch h.Status {
	case "ok":
		snap.Ready = true
	case "unavailable":
		snap.Ready, snap.Released = true, true
	default: // "draining": shutting down
	}
	snap.Authorized = memory.Code != http.StatusUnauthorized && metrics.Code != http.StatusUnauthorized
	tf.mu.Lock()
	m := *tf.memo(t.Port)
	tf.mu.Unlock()
	snap.Model, snap.IdleRelease = m.model, m.idle
	if snap.Model == "" {
		snap.Model = h.Model // a server we did not start nor find: its served name
	}
	if h.Model != "" {
		tf.mu.Lock()
		tf.memo(t.Port).served = h.Model // without a key /health names it
		tf.mu.Unlock()
	}
	if !snap.Authorized {
		return snap
	}

	var md memoryDoc
	if memory.Code == http.StatusOK && json.Unmarshal(memory.Body, &md) == nil {
		snap.ContextTokens = md.ContextWindow
		snap.MemoryUsed = md.Process.Footprint
		tf.mu.Lock()
		tf.memo(t.Port).context = md.ContextWindow
		tf.mu.Unlock()
		if md.Device == nil && h.Status == "unavailable" {
			snap.Released = true
		}
	}
	if !m.asked && snap.Ready {
		tf.askEfforts(ctx, t)
	}
	if metrics.Code == http.StatusOK {
		p := enginekit.ParseProm(metrics.Body)
		snap.Active = int(p.Num("tensorfold:requests_running"))
		snap.Queued = int(p.Num("tensorfold:requests_waiting"))
		snap.Completed = p.Num("tensorfold:request_latency_seconds_count")
		running := p.Num("tensorfold:generation_tokens_running")
		snap.OutputTokens = p.Num("tensorfold:generation_tokens_total") + running
		snap.DecodeTime = tf.liveDecode(t.Port, snap.At, running)
		snap.PrefillTokens = p.Num("tensorfold:prompt_tokens_total")
		snap.PrefillTime = p.Seconds("tensorfold:request_prefill_seconds_sum")
		snap.FirstToken = histMedian(p.Labeled["tensorfold:time_to_first_token_seconds_bucket"])
		if f := p.Num("tensorfold:process_footprint_bytes"); f > 0 {
			snap.MemoryUsed = f
		}
	}
	return snap
}

// modelsDoc is the part of GET /v1/models we read (fixture models.json):
// "reasoning": {"default_enabled": true, "supported_efforts": ["xhigh",
// "medium", "low", "none"]} for Qwen3.8-27B; Qwen3.5-2B lists none.
type modelsDoc struct {
	Data []struct {
		Reasoning struct {
			Efforts []string `json:"supported_efforts"`
		} `json:"reasoning"`
	} `json:"data"`
}

// askEfforts reads the reasoning efforts of the server at t, for the
// agents (enginekit.AgentServer.Efforts), once: a failed ask is not
// repeated until the next start.
func (tf *TensorFold) askEfforts(ctx context.Context, t engine.Target) {
	r := tf.request(ctx, t, http.MethodGet, "/v1/models", nil, probeTimeout)
	var d modelsDoc
	ok := r.Code == http.StatusOK && json.Unmarshal(r.Body, &d) == nil
	tf.mu.Lock()
	defer tf.mu.Unlock()
	m := tf.memo(t.Port)
	m.asked = true
	if ok && len(d.Data) > 0 {
		m.efforts = d.Data[0].Reasoning.Efforts
	}
}

// histMedian is the median of a Prometheus histogram given as its
// cumulative buckets by "le", interpolated within the bucket that holds
// it; 0 when it has no samples. The last finite bound stands for +Inf.
func histMedian(buckets map[string]float64) time.Duration {
	type bucket struct{ le, n float64 }
	var bs []bucket
	total := buckets["+Inf"]
	for le, n := range buckets {
		if v, err := strconv.ParseFloat(le, 64); err == nil && !math.IsInf(v, 0) {
			bs = append(bs, bucket{v, n})
		}
	}
	if total <= 0 || len(bs) == 0 {
		return 0
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].le < bs[j].le })
	half := total / 2
	lo, below := 0.0, 0.0
	for _, b := range bs {
		if b.n >= half {
			frac := 1.0
			if b.n > below {
				frac = (half - below) / (b.n - below)
			}
			return time.Duration((lo + frac*(b.le-lo)) * float64(time.Second))
		}
		lo, below = b.le, b.n
	}
	return time.Duration(bs[len(bs)-1].le * float64(time.Second))
}

// Release cannot be done: the server has no route that unloads the model
// (api.md route table); it unloads only after --idle-unload without
// requests (idle_unload.zig).
func (tf *TensorFold) Release(context.Context, engine.Target) error {
	return errNoRelease
}

// Restore reloads an unloaded model: any request does ("the next request
// reloads it"). This sends the smallest one, a one-token completion, and
// returns when it is answered: 0.51 s for Qwen3.5-2B, logged as "idle
// reload: the model is resident again" (fixture lifecycle/serve.stdout).
// Side effect: the counters count one request of one token, which the
// speed meter ignores (it skips one-token requests).
func (tf *TensorFold) Restore(ctx context.Context, t engine.Target) error {
	body := []byte(`{"prompt":"a","max_tokens":1}`)
	r := tf.request(ctx, t, http.MethodPost, "/v1/completions", body, 120*time.Second)
	if r.Err != nil && r.Code == 0 {
		return r.Err
	}
	if r.Code == http.StatusOK {
		return nil
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := ""
	if json.Unmarshal(r.Body, &e) == nil {
		msg = e.Error.Message
	}
	if msg == "" {
		msg = strings.TrimSpace(string(r.Body))
	}
	return fmt.Errorf("tensorfold: POST /v1/completions: HTTP %d: %s", r.Code, msg)
}

// request is enginekit.Request with the engine's client.
func (tf *TensorFold) request(ctx context.Context, t engine.Target, method, path string, body []byte, timeout time.Duration) enginekit.Reply {
	return enginekit.Request(ctx, tf.client, t, method, path, body, timeout)
}
