//go:build live

// Live test of TensorFold under the supervisor: detection, a download into
// the hub cache, a start with a context and an idle unload, a request, the
// unload and the reload:
//
//	CGO_ENABLED=0 go test -tags live -run TestLiveTensorFold -v ./internal/core/
//
// It needs TensorFold (brew install ashhart/tensorfold/tensorfold) and
// mlx-community/Qwen3.5-2B-MLX-4bit in the Hugging Face cache (the download
// then only checks the cached blobs). It uses port 18310 and never touches
// a server it did not start.
package core_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/tensorfold"
)

func TestLiveTensorFold(t *testing.T) {
	const (
		port  = 18310
		model = "mlx-community/Qwen3.5-2B-MLX-4bit"
	)
	views := make(chan core.View, 256)
	sup := core.New(core.Options{
		Engine:  tensorfold.New(),
		Engines: []engine.Engine{tensorfold.New()},
		Settings: core.Settings{
			Config:   engine.Config{Port: port, Access: engine.AccessLocal, Model: model, IdleRelease: 10 * time.Second},
			Contexts: map[core.ModelKey]int{{Engine: "tensorfold", Model: model}: 16384},
		},
		LogDir: t.TempDir(),
		OnChange: func(v core.View) {
			select {
			case views <- v:
			default:
			}
		},
	})
	defer sup.Close()
	wait := func(what string, d time.Duration, ok func(core.View) bool) core.View {
		t.Helper()
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if v := sup.View(); ok(v) {
				return v
			}
			time.Sleep(100 * time.Millisecond)
		}
		v := sup.View()
		t.Fatalf("timed out waiting for %s: phase %d, last line %q", what, v.Phase, v.LastLine)
		return v
	}
	v := wait("detect", 30*time.Second, func(v core.View) bool { return v.Version != "" })
	t.Logf("version %s, latest %q, models %d", v.Version, v.Latest, len(v.Models))

	// The model pulled by "tensorfold pull": Lumilo's download finds every
	// blob in place (same layout) and fetches nothing.
	sup.DownloadModel(model, engine.ModelOptions{})
	v = wait("download", 60*time.Second, func(v core.View) bool {
		return len(v.Downloads) > 0 && (v.Downloads[0].State == core.DownloadDone || v.Downloads[0].State == core.DownloadFailed)
	})
	if j := v.Downloads[0]; j.State != core.DownloadDone || j.Installed != model {
		t.Fatalf("download: %+v", j)
	}

	sup.SelectModel(model)
	sup.Start()
	v = wait("ready", 60*time.Second, func(v core.View) bool { return v.Phase == core.PhaseReady })
	if v.Model.ID != model || v.Snapshot.Model != model || v.Snapshot.ContextTokens != 16384 || v.Snapshot.IdleRelease != 10*time.Second {
		t.Errorf("ready: model %q snap %q context %d idle %v", v.Model.ID, v.Snapshot.Model, v.Snapshot.ContextTokens, v.Snapshot.IdleRelease)
	}
	t.Logf("ready: memory %d MB", v.Snapshot.MemoryUsed>>20)

	body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 200,
		"messages":             []map[string]string{{"role": "user", "content": "Count from 1 to 50."}},
		"chat_template_kwargs": map[string]bool{"enable_thinking": false}})
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat: HTTP %d", resp.StatusCode)
	}
	v = wait("speed", 10*time.Second, func(v core.View) bool { return len(v.Speed.History) > 0 })
	t.Logf("speed: %.1f tok/s, first token %v, read %.0f tok/s, completed %d",
		v.Speed.History[0], v.Speed.FirstToken, v.Speed.Read, v.Snapshot.Completed)

	v = wait("idle unload", 30*time.Second, func(v core.View) bool { return v.Phase == core.PhaseReleased })
	t.Logf("released: memory %d MB", v.Snapshot.MemoryUsed>>20)

	sup.Restore()
	v = wait("reload", 30*time.Second, func(v core.View) bool { return v.Phase == core.PhaseReady && !v.Snapshot.Released })
	t.Logf("reloaded: memory %d MB", v.Snapshot.MemoryUsed>>20)

	sup.Stop()
	wait("stop", 30*time.Second, func(v core.View) bool { return v.Phase == core.PhaseStopped })
}

// Live test of a download paused and resumed under the supervisor:
//
//	CGO_ENABLED=0 go test -tags live -run TestLiveTensorFoldPause -v ./internal/core/
//
// It downloads mlx-community/Qwen3.5-2B-MLX-4bit (1.7 GB) into the
// Hugging Face cache, pauses it about a third in, resumes it from the part
// it kept, and removes the model at the end. The model must not be in the
// cache before.
func TestLiveTensorFoldPause(t *testing.T) {
	const model = "mlx-community/Qwen3.5-2B-MLX-4bit"
	sup := core.New(core.Options{
		Engine:   tensorfold.New(),
		Engines:  []engine.Engine{tensorfold.New()},
		Settings: core.Settings{Config: engine.Config{Port: 18311, Access: engine.AccessLocal}},
		LogDir:   t.TempDir(),
	})
	defer sup.Close()
	wait := func(what string, d time.Duration, ok func(core.View) bool) core.View {
		t.Helper()
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if v := sup.View(); ok(v) {
				return v
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s: %+v", what, sup.View().Downloads)
		return core.View{}
	}
	v := wait("detect", 30*time.Second, func(v core.View) bool { return v.Version != "" })
	for _, m := range v.Models {
		if m.ID == model {
			t.Skip(model + " is installed already")
		}
	}
	job := func(v core.View) core.DownloadJob {
		for _, j := range v.Downloads {
			if j.Model.ID == model {
				return j
			}
		}
		return core.DownloadJob{State: -1}
	}

	sup.DownloadModel(model, engine.ModelOptions{})
	v = wait("a third", 5*time.Minute, func(v core.View) bool {
		j := job(v)
		return j.State == core.DownloadRunning && j.Total > 0 && j.Done*3 >= j.Total
	})
	at := job(v).Done
	sup.PauseDownload(model)
	v = wait("paused", 30*time.Second, func(v core.View) bool { return job(v).State == core.DownloadPaused })
	time.Sleep(2 * time.Second) // nothing moves while paused
	if j := job(sup.View()); j.State != core.DownloadPaused || j.Done < at {
		t.Fatalf("paused: %+v", j)
	}
	t.Logf("paused at %.2f of %.2f GB", float64(job(sup.View()).Done)/1e9, float64(job(sup.View()).Total)/1e9)

	start := time.Now()
	sup.DownloadModel(model, engine.ModelOptions{})
	v = wait("resumed", 30*time.Second, func(v core.View) bool { return job(v).State == core.DownloadRunning })
	if j := job(v); j.Done < at {
		t.Errorf("resumed from %d, paused at %d: the part was not kept", j.Done, at)
	}
	v = wait("done", 10*time.Minute, func(v core.View) bool { return job(v).State == core.DownloadDone })
	t.Logf("finished %.0f s after the resume", time.Since(start).Seconds())
	listed := false
	for _, m := range v.Models {
		listed = listed || m.ID == model
	}
	if !listed {
		t.Fatalf("not listed after the download")
	}

	sup.RemoveModel(model)
	wait("removed", time.Minute, func(v core.View) bool {
		for _, m := range v.Models {
			if m.ID == model {
				return false
			}
		}
		return !v.Removal.Running
	})
}
