package splash

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// A loading server has bound its socket but not called listen(): connects
// hang instead of failing (ref §0.6), so every probe request is short.
const probeTimeout = 400 * time.Millisecond

// statusDoc is the part of GET /status we read (ref §3.3). /metrics lacks
// these fields: the weights state, the model and the context size.
type statusDoc struct {
	MaximumContextTokens float64 `json:"maximum_context_tokens"`
	Weights              struct {
		IdleReleaseSeconds json.RawMessage `json:"idle_release_seconds"`
		Released           bool            `json:"released"`
		Restoring          bool            `json:"restoring"`
	} `json:"weights"`
	Transport struct {
		Stopped bool `json:"stopped"`
	} `json:"transport"`
	Instance struct {
		Model string `json:"model"`
	} `json:"instance"`
	Admission struct {
		Waiting float64 `json:"waiting"`
	} `json:"admission"`
}

// Probe fetches /health, /ready, /status and /metrics at once and maps them
// to a Snapshot. Numbers come from /metrics where it has them; /status adds
// what only it has.
func (s *Splash) Probe(ctx context.Context, t engine.Target) engine.Snapshot {
	var health, ready, status, metrics enginekit.Reply
	var wg sync.WaitGroup
	for path, dst := range map[string]*enginekit.Reply{"/health": &health, "/ready": &ready, "/status": &status, "/metrics": &metrics} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*dst = s.request(ctx, t, http.MethodGet, path, nil, probeTimeout)
		}()
	}
	wg.Wait()

	snap := engine.Snapshot{At: time.Now()}
	if health.Code != http.StatusOK {
		return snap
	}
	snap.Reachable = true
	snap.Ready = ready.Code == http.StatusOK
	// /health and /ready are public. A 401 on the others means the key is
	// missing or wrong; then we know nothing more, and that is not an error.
	// A timeout there leaves Authorized true with zero numbers.
	snap.Authorized = status.Code != http.StatusUnauthorized && metrics.Code != http.StatusUnauthorized
	if !snap.Authorized {
		return snap
	}

	var st statusDoc
	if status.Code == http.StatusOK && json.Unmarshal(status.Body, &st) == nil {
		snap.Model = st.Instance.Model
		snap.ContextTokens = int(st.MaximumContextTokens)
		// While the weights come back (about 4 s) /status says restoring.
		// It may still say released then, so restoring wins.
		snap.Restoring = st.Weights.Restoring
		snap.Released = st.Weights.Released && !st.Weights.Restoring
		snap.IdleRelease = idleReleaseFromStatus(st.Weights.IdleReleaseSeconds)
		// The crash-loop stop (ref §2.3): the process lives, /ready is 503
		// and POSTs fail with engine_failed. /ready 503 alone also happens
		// while the engine recovers, so it is not enough.
		snap.Broken = st.Transport.Stopped && !snap.Ready
	}
	if metrics.Code == http.StatusOK {
		m := enginekit.ParseProm(metrics.Body)
		// Memory: memory_actual.current_bytes is what Metal holds right now
		// (19.7 GB loaded, 2.05 GB after a release in the fixtures);
		// memory_governor.limit_bytes is the budget it is held against.
		// charged_bytes would count reservations, not use.
		snap.MemoryUsed = m.Num("splash_memory_current_bytes")
		snap.MemoryLimit = m.Num("splash_memory_limit_bytes")
		snap.DiskUsed = m.Num("splash_disk_used_bytes")
		snap.DiskLimit = m.Num("splash_disk_limit_bytes")
		switch m.OneHot("splash_memory_pressure") {
		case "warning":
			snap.Pressure = engine.PressureWarning
		case "critical":
			snap.Pressure = engine.PressureCritical
		}
		// Active: scheduler.decoding is 1 and prefilling is 0 for the whole
		// of a generation in the fixtures, and both are 0 when idle.
		// transport_pending also is 1 then, but it counts everything queued
		// to the engine, so it is not used.
		snap.Active = int(m.Num("splash_scheduler_prefilling") + m.Num("splash_scheduler_decoding"))
		// Queued: requests in the scheduler's queue plus those held at
		// admission (for memory, concurrency or behind a refusal). /status
		// admission.waiting is that admission total.
		snap.Queued = int(m.Num("splash_scheduler_queued")) + int(st.Admission.Waiting)

		snap.Completed = m.Num("splash_requests_completed_total")
		snap.OutputTokens = m.Num("splash_decode_output_tokens_total")
		snap.DecodeTime = m.Millis("splash_decode_wall_milliseconds_total")
		snap.PrefillTokens = m.Num("splash_prefill_input_tokens_total")
		snap.PrefillTime = m.Millis("splash_prefill_wall_milliseconds_total")
		snap.FirstToken = m.Millis("splash_ttft_p50_milliseconds")
	}
	return snap
}

// idleReleaseFromStatus reads weights.idle_release_seconds. How "off" is
// spelled there is unverified (ref §9), so anything that is not a positive
// number means never.
func idleReleaseFromStatus(raw json.RawMessage) time.Duration {
	var secs float64
	if json.Unmarshal(raw, &secs) != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// Release is POST /v1/memory/release. 409 means the engine is busy.
func (s *Splash) Release(ctx context.Context, t engine.Target) error {
	return s.post(ctx, t, "/v1/memory/release", nil, 10*time.Second)
}

// Restore loads the weights back. Splash has no restore route (POST routes,
// server/server.py:205-222): the next request restores them (ref §3.5). So
// this sends the smallest request, a one-token completion, and returns when
// it is answered. The model field is left out, which means the served model
// (frontend.py:310-315). Side effect: it runs once, so the server prints
// Started/Done lines and its Completed counter grows by one.
func (s *Splash) Restore(ctx context.Context, t engine.Target) error {
	body := []byte(`{"prompt":"a","max_tokens":1}`)
	return s.post(ctx, t, "/v1/completions", body, 60*time.Second)
}

func (s *Splash) post(ctx context.Context, t engine.Target, path string, body []byte, timeout time.Duration) error {
	r := s.request(ctx, t, http.MethodPost, path, body, timeout)
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
	return fmt.Errorf("splash: POST %s: HTTP %d: %s", path, r.Code, msg)
}

// request is enginekit.Request with the engine's client.
func (s *Splash) request(ctx context.Context, t engine.Target, method, path string, body []byte, timeout time.Duration) enginekit.Reply {
	return enginekit.Request(ctx, s.client, t, method, path, body, timeout)
}
