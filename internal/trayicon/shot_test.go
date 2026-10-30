package trayicon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kotuke/lumilo/internal/core"
)

// LUMILO_TRAY_DIR=… saves the icon of each main state in both menu-bar
// themes at 4x, for the README (scripts/readme-images.py).
func TestTrayShot(t *testing.T) {
	dir := os.Getenv("LUMILO_TRAY_DIR")
	if dir == "" {
		t.Skip("LUMILO_TRAY_DIR is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scale = 4
	clear(cache.m)
	defer func() { scale = 2; clear(cache.m) }()
	states := map[string]core.Phase{
		"generating": core.PhaseGenerating, "ready": core.PhaseReady, "loading": core.PhaseLoading,
		"released": core.PhaseReleased, "stopped": core.PhaseStopped, "error": core.PhaseError,
	}
	views := map[string]core.View{}
	for name, ph := range states {
		views[name] = view(ph)
		// The same under llama.cpp, as the README's hero shows it:
		// llama-server counts a request's tokens when it ends, so a
		// generating icon shows memory.
		llama := view(ph)
		llama.Engine, llama.Speed.Current, llama.Snapshot.MemoryUsed = "llama.cpp", 0, 20_600_000_000
		views[name+"-llamacpp"] = llama
	}
	for name, v := range views {
		for theme, dark := range map[string]bool{"dark": true, "light": false} {
			b := PNG(For(v, "en", dark, liveTPS(v)))
			if err := os.WriteFile(filepath.Join(dir, name+"-"+theme+".png"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
