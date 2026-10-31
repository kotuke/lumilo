package trayicon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/engines/splash"
	"github.com/kotuke/lumilo/internal/engines/tensorfold"
)

// LUMILO_TRAY_DIR=… saves the icon of each main state in both menu-bar
// themes at 4x, with the engines' own logos, for the README
// (scripts/readme-images.py).
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
	clear(logos.m) // a test before may have set an engine without its logo
	defer func() { scale = 2; clear(cache.m); clear(logos.m) }()
	splashLogo := splash.New().Info().Logo
	llamaLogo := llamacpp.New().Info().Logo
	tfLogo := tensorfold.New().Info().Logo
	states := map[string]core.Phase{
		"generating": core.PhaseGenerating, "ready": core.PhaseReady, "loading": core.PhaseLoading,
		"released": core.PhaseReleased, "stopped": core.PhaseStopped, "error": core.PhaseError,
	}
	views := map[string]core.View{}
	for name, ph := range states {
		sp := view(ph)
		sp.Logo = splashLogo
		views[name] = sp
		// The same under llama.cpp, as the README's hero shows it:
		// llama-server counts a request's tokens when it ends, so a
		// generating icon shows memory.
		llama := view(ph)
		llama.Engine, llama.Speed.Current, llama.Snapshot.MemoryUsed = "llama.cpp", 0, 20_600_000_000
		llama.Logo = llamaLogo
		views[name+"-llamacpp"] = llama
		// And under TensorFold, which the hero's middle panel runs: its
		// speed is live.
		tf := view(ph)
		tf.Engine, tf.Speed.Current, tf.Snapshot.MemoryUsed = "TensorFold", 47, 22_400_000_000
		tf.Logo = tfLogo
		views[name+"-tensorfold"] = tf
	}
	for name, v := range views {
		for theme, dark := range map[string]bool{"dark": true, "light": false} {
			b := Shot(For(v, "en", liveTPS(v)), dark)
			if err := os.WriteFile(filepath.Join(dir, name+"-"+theme+".png"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
