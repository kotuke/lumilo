//go:build live

package llamacpp

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// TestLiveDownload downloads LUMILO_LIVE_DOWNLOAD_MODEL (default
// ggml-org/Qwen3-0.6B-GGUF:Q4_0, 429 MB) from huggingface.co into the hub
// cache LUMILO_LIVE_DOWNLOAD_DIR, which it needs: it never writes to the
// user's cache. Models must list the result. Then
// "HF_HUB_CACHE=<dir> llama-server -hf <model> --offline" serves it.
func TestLiveDownload(t *testing.T) {
	dir := os.Getenv("LUMILO_LIVE_DOWNLOAD_DIR")
	if dir == "" {
		t.Skip("LUMILO_LIVE_DOWNLOAD_DIR not set")
	}
	model := os.Getenv("LUMILO_LIVE_DOWNLOAD_MODEL")
	if model == "" {
		model = "ggml-org/Qwen3-0.6B-GGUF:Q4_0"
	}
	l := newEngine()
	l.hubDirs, l.flatDirs, l.dlDir = []string{dir}, nil, dir
	start := time.Now()
	last := time.Now()
	id, err := l.Download(context.Background(), engine.DownloadRequest{Model: model}, func(done, total int64) {
		if time.Since(last) > 5*time.Second {
			last = time.Now()
			t.Logf("%d of %d bytes", done, total)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("downloaded %s in %s: %s", model, time.Since(start).Round(time.Second), id)
	models, err := l.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.ID == id {
			t.Logf("listed: %s/%s:%s, %d bytes", m.Owner, m.Name, m.Variant, m.SizeBytes)
			return
		}
	}
	t.Fatalf("Models does not list %s: %+v", id, models)
}
