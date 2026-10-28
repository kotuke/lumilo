//go:build live

// Live test of a download run, the context size and an engine switch with
// llama.cpp:
//
//	CGO_ENABLED=0 go test -tags live -run TestLiveLlamaCpp -v ./internal/core/
//
// It needs llama.cpp and ggml-org/Qwen3-0.6B-GGUF:Q8_0 in the Hugging Face
// cache (the "download" then only resolves the cached file). It uses port
// 18300 and never touches a server it did not start.
package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/engines/splash"
)

func TestLiveLlamaCpp(t *testing.T) {
	const port = 18300
	views := make(chan core.View, 256)
	sup := core.New(core.Options{
		Engine:  llamacpp.New(),
		Engines: []engine.Engine{llamacpp.New(), splash.New()},
		Settings: core.Settings{
			Config:   engine.Config{Port: port, Access: engine.AccessLocal},
			Contexts: map[core.ModelKey]int{{Engine: "llamacpp", Model: "ggml-org/Qwen3-0.6B-GGUF:Q8_0"}: 8192},
		},
		LogDir: t.TempDir(),
		OnChange: func(v core.View) {
			select {
			case views <- v:
			default:
			}
		},
		OnModel: func(e, m string) { t.Logf("OnModel %s %s", e, m) },
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
	wait("detect", 30*time.Second, func(v core.View) bool { return v.Version != "" })

	sup.Download("ggml-org/Qwen3-0.6B-GGUF:Q8_0")
	v := wait("download run", 10*time.Second, func(v core.View) bool { return v.Phase == core.PhaseLoading })
	t.Logf("loading: download %q downloading %v model %+v", v.Download, v.Downloading, v.Model)
	v = wait("ready", 120*time.Second, func(v core.View) bool { return v.Phase == core.PhaseReady && v.Download == "" })
	if !strings.HasSuffix(v.Model.ID, "Qwen3-0.6B-Q8_0.gguf") || v.PendingRestart {
		t.Errorf("after download: model %q pending %v", v.Model.ID, v.PendingRestart)
	}
	if v.Snapshot.ContextTokens != 8192 {
		t.Errorf("context %d, want 8192 (-c)", v.Snapshot.ContextTokens)
	}
	t.Logf("ready: model %s context %d native %d", v.Model.ID, v.Snapshot.ContextTokens, v.Model.ContextLength)

	sup.SwitchEngine("splash", "")
	v = wait("switch", 60*time.Second, func(v core.View) bool { return v.EngineID == "splash" && !v.Switching })
	t.Logf("after switch: phase %d version %s", v.Phase, v.Version)
}
