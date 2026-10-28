package panel

import (
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
)

func TestTerminalSelect(t *testing.T) {
	b := newBench(t, "en")
	b.p.SetTerminals([]TerminalChoice{
		{"com.apple.Terminal", "Terminal"}, {"com.mitchellh.ghostty", "Ghostty"}, {"com.superlogical.rex", "Rex"}})
	pr := defaultPrefs()
	pr.Terminal = "com.apple.Terminal"
	b.p.SetPrefs(pr)
	b.show(withPhase(core.PhaseReady), time.Hour)
	b.click("Settings")
	b.want("Terminal", "Language")
	b.none("Ghostty", "Rex")
	b.clickEnd("Terminal")
	b.want("Ghostty", "Rex")
	b.click("Ghostty")
	if n := len(b.rec.prefs); n == 0 || b.rec.prefs[n-1].Terminal != "com.mitchellh.ghostty" {
		t.Fatalf("terminal stored %+v", b.rec.prefs)
	}
}

// A saved terminal that is no longer installed shows as Terminal, the one
// agents start in then, and is not overwritten until the user chooses.
func TestTerminalGone(t *testing.T) {
	b := newBench(t, "en")
	pr := defaultPrefs()
	pr.Terminal = "com.mitchellh.ghostty"
	b.p.SetPrefs(pr)
	b.show(withPhase(core.PhaseReady), time.Hour)
	b.click("Settings")
	b.want("Terminal")
	b.none("Ghostty")
	for _, p := range b.rec.prefs {
		if p.Terminal != "com.mitchellh.ghostty" {
			t.Errorf("the saved choice was overwritten: %+v", p)
		}
	}
}
