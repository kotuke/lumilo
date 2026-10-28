//go:build live

package splash

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// TestLiveDownloadNoVision runs the installed Splash's own installer for a
// repository without a vision projector: it reads GGUF headers only, then
// refuses the model in seconds, and nothing is downloaded. With
// LUMILO_LIVE_SPLASH_DL=1 (it contacts the Hub).
func TestLiveDownloadNoVision(t *testing.T) {
	if os.Getenv("LUMILO_LIVE_SPLASH_DL") != "1" {
		t.Skip("LUMILO_LIVE_SPLASH_DL=1 runs it")
	}
	e := New()
	before, err := e.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	_, err = e.Download(ctx, engine.DownloadRequest{Model: arex}, func(done, total int64) { t.Logf("progress %d / %d", done, total) })
	var f engine.Failure
	if !errors.As(err, &f) || f.Kind != engine.FailNoVision {
		t.Fatalf("err %v (%+v)", err, f)
	}
	t.Logf("refused in %s: %s", time.Since(start).Round(time.Millisecond), f.Line)
	after, _ := e.Models(context.Background())
	if len(after) != len(before) {
		t.Errorf("models changed: %d -> %d", len(before), len(after))
	}
	for _, m := range after {
		t.Logf("listed %s (vision %v)", m.ID, m.Features.Vision)
	}
}

// TestLiveDownloadCancel starts the text-only install of the same
// repository, cancels it once bytes arrive and drops the partial files.
// With LUMILO_LIVE_SPLASH_CANCEL=1: it downloads for a few seconds.
func TestLiveDownloadCancel(t *testing.T) {
	if os.Getenv("LUMILO_LIVE_SPLASH_CANCEL") != "1" {
		t.Skip("LUMILO_LIVE_SPLASH_CANCEL=1 runs it")
	}
	e := New()
	since := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan [2]int64, 100)
	done := make(chan error, 1)
	go func() {
		_, err := e.Download(ctx, engine.DownloadRequest{Model: arex, NoVision: true}, func(d, total int64) {
			select {
			case got <- [2]int64{d, total}:
			default:
			}
		})
		done <- err
	}()
	timeout := time.After(3 * time.Minute)
wait:
	for {
		select {
		case p := <-got:
			t.Logf("progress %d / %d", p[0], p[1])
			if p[0] > 50<<20 && p[1] > 0 {
				break wait
			}
		case err := <-done:
			t.Fatalf("ended before cancel: %v", err)
		case <-timeout:
			t.Fatal("no bytes in 3 min")
		}
	}
	start := time.Now()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	t.Logf("stopped in %s", time.Since(start).Round(time.Millisecond))
	e.DropPartial(arex, since)
	for _, d := range e.hubDirs {
		parts, _ := filepath.Glob(filepath.Join(d, "models--BoldingBuilds--AREX-2-Abliterated-GGUF", "blobs", "*.incomplete"))
		if len(parts) > 0 {
			t.Errorf("partial files left: %v", parts)
		}
	}
	if _, err := os.Lstat(textOnlyLink(e.modelsDir, arex)); err == nil {
		t.Error("a cancelled install was published")
	}
}
