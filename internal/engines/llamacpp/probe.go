package llamacpp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// Probe requests are short: the supervisor probes every second.
const probeTimeout = 400 * time.Millisecond

// errNoRelease is Release's answer: see Release.
var errNoRelease = errors.New("llama.cpp: the server cannot unload the model on request; it sleeps by itself after --sleep-idle-seconds")

// portMemo is what the engine keeps per port between calls.
type portMemo struct {
	idle time.Duration // IdleRelease of the last ServeCommand or FindRunning
	// For AgentCommand: the name the API serves the model by (--alias, else
	// the -m path or -hf name, /v1/models), the context of a slot and the
	// image input, from /props.
	served    string
	context   int
	vision    bool
	pid       int   // the server process, found by port; 0 = unknown
	predicted int64 // tokens_predicted_total at the last probe
	completed int64 // synthesised request counter, see countCompleted
}

func (l *LlamaCpp) memo(port int) *portMemo {
	m := l.ports[port]
	if m == nil {
		m = &portMemo{predicted: -1}
		l.ports[port] = m
	}
	return m
}

// remember records the sleep setting a server on port was started with
// (/props does not report it) and the name its API serves the model by.
func (l *LlamaCpp) remember(port int, idle time.Duration, served string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.memo(port)
	m.idle, m.served = idle, served
}

// countCompleted synthesises Snapshot.Completed. llama-server has no
// request counter: /metrics adds a request's tokens to
// tokens_predicted_total only when it finishes (fixture generating*: 0
// during the first request, after_gen1: 600). So every probe that sees the
// counter grow counts one finished request; requests that finish within
// the same probe interval count as one. A counter that went down is a new
// server on the port.
func (l *LlamaCpp) countCompleted(port int, predicted int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.memo(port)
	switch {
	case m.predicted < 0 || predicted < m.predicted:
		m.completed = 0
		if predicted > 0 {
			m.completed = 1
		}
	case predicted > m.predicted:
		m.completed++
	}
	m.predicted = predicted
	return m.completed
}

// propsDoc is the part of GET /props we read (README "GET /props").
type propsDoc struct {
	ModelPath                 string `json:"model_path"`
	IsSleeping                bool   `json:"is_sleeping"`
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
	Modalities struct {
		Vision bool `json:"vision"`
	} `json:"modalities"`
}

// Probe fetches /health, /props and /metrics at once. It never asks /slots:
// a GET /slots wakes a sleeping server and keeps it awake (fixture notes;
// README lists only /health, /props, /models and /metrics as exempt).
//
//   - /health is public: 503 "Loading model" while loading, 200 after;
//     also 200 while asleep.
//   - /props and /metrics need the key (401 without it). /metrics answers
//     501 when the server runs without --metrics (a server started
//     elsewhere): then there are no counters.
func (l *LlamaCpp) Probe(ctx context.Context, t engine.Target) engine.Snapshot {
	var health, props, metrics enginekit.Reply
	var wg sync.WaitGroup
	for path, dst := range map[string]*enginekit.Reply{"/health": &health, "/props": &props, "/metrics": &metrics} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*dst = l.request(ctx, t, http.MethodGet, path, nil, probeTimeout)
		}()
	}
	wg.Wait()

	snap := engine.Snapshot{At: time.Now()}
	if health.Code != http.StatusOK && health.Code != http.StatusServiceUnavailable {
		return snap
	}
	snap.Reachable = true
	snap.Ready = health.Code == http.StatusOK
	snap.Authorized = props.Code != http.StatusUnauthorized && metrics.Code != http.StatusUnauthorized
	l.mu.Lock()
	snap.IdleRelease = l.memo(t.Port).idle
	l.mu.Unlock()
	if !snap.Authorized || !snap.Ready {
		return snap
	}

	var p propsDoc
	if props.Code == http.StatusOK && json.Unmarshal(props.Body, &p) == nil {
		snap.Model = p.ModelPath
		// n_ctx of a slot; with the unified KV (the default) each slot may
		// use the whole context.
		snap.ContextTokens = p.DefaultGenerationSettings.NCtx
		snap.Released = p.IsSleeping
		l.mu.Lock()
		m := l.memo(t.Port)
		m.context, m.vision = snap.ContextTokens, p.Modalities.Vision
		if m.served == "" {
			m.served = p.ModelPath // no alias: the API names it by its path
		}
		l.mu.Unlock()
	}
	if metrics.Code == http.StatusOK {
		m := enginekit.ParseProm(metrics.Body)
		snap.Active = int(m.Num("llamacpp:requests_processing"))
		snap.Queued = int(m.Num("llamacpp:requests_deferred"))
		snap.OutputTokens = m.Num("llamacpp:tokens_predicted_total")
		snap.DecodeTime = m.Seconds("llamacpp:tokens_predicted_seconds_total")
		snap.PrefillTokens = m.Num("llamacpp:prompt_tokens_total")
		snap.PrefillTime = m.Seconds("llamacpp:prompt_seconds_total")
		snap.Completed = l.countCompleted(t.Port, snap.OutputTokens)
		// No time-to-first-token metric: FirstToken stays 0.
	}
	// llama.cpp reports no memory: use the server's resident size. While
	// asleep that is what is left after the weights went.
	snap.MemoryUsed = l.residentBytes(t.Port)
	return snap
}

// Release cannot be done: llama-server (single model, as Lumilo runs it)
// has no route that puts it to sleep; it sleeps only after
// --sleep-idle-seconds without tasks. Router mode has POST /models/unload,
// but it stops a child server, which is not this contract.
func (l *LlamaCpp) Release(ctx context.Context, t engine.Target) error {
	return errNoRelease
}

// Restore wakes a sleeping server: any task loads the model back (README
// "Sleeping on Idle"; live: 0.28 s for the 0.6B model). This sends the
// smallest one, a one-token completion, and returns when it is answered.
// Side effect: tokens_predicted_total grows by one, which the speed meter
// ignores (it skips one-token requests).
func (l *LlamaCpp) Restore(ctx context.Context, t engine.Target) error {
	body := []byte(`{"prompt":"a","max_tokens":1}`)
	r := l.request(ctx, t, http.MethodPost, "/v1/completions", body, 120*time.Second)
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
	return fmt.Errorf("llama.cpp: POST /v1/completions: HTTP %d: %s", r.Code, msg)
}

// request is enginekit.Request with the engine's client.
func (l *LlamaCpp) request(ctx context.Context, t engine.Target, method, path string, body []byte, timeout time.Duration) enginekit.Reply {
	return enginekit.Request(ctx, l.client, t, method, path, body, timeout)
}
