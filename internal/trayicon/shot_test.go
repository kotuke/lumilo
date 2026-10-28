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
	for name, ph := range states {
		for theme, dark := range map[string]bool{"dark": true, "light": false} {
			b := PNG(For(view(ph), "en", dark))
			if err := os.WriteFile(filepath.Join(dir, name+"-"+theme+".png"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
