package panel

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
)

const addHint = "Type a model's name to search Hugging Face, or paste owner/repo:variant"

// Picking a variant leaves a summary of the model under the field, with its
// size and a Download button that does what Enter does.
func TestAddModelSummaryDownloads(t *testing.T) {
	for _, popup := range []bool{false, true} {
		b := newBench(t, "en")
		b.openAdd(popup)
		b.typeText("unsloth/Qwen3.8-27B-GGUF:")
		b.p.SetModelVariants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
		b.tt.Frame()
		b.none("Download Qwen3.8-27B-GGUF")
		b.click("UD-IQ4_XS")
		b.tt.Frame()
		if got := b.text(); got != "unsloth/Qwen3.8-27B-GGUF:UD-IQ4_XS" {
			t.Fatalf("popup %v: the pick put %q", popup, got)
		}
		// The summary is there at once, the list and the hint are gone, and
		// the field keeps the focus.
		b.want("Qwen3.8-27B-GGUF", "unsloth · UD-IQ4_XS · 14.3 GB", "Download Qwen3.8-27B-GGUF", "Needs about")
		b.none("BF16", addHint)
		if !b.tt.Focused("Add model…") {
			t.Errorf("popup %v: the field lost the focus", popup)
		}
		b.notCalled("Download unsloth/Qwen3.8-27B-GGUF:UD-IQ4_XS")
		b.click("Download Qwen3.8-27B-GGUF")
		b.called("Download unsloth/Qwen3.8-27B-GGUF:UD-IQ4_XS")
		// The field closes; the model list stays open, where the download
		// shows (Splash downloads apart from the server).
		if b.p.addingModel || b.p.modelsOpen != popup || b.p.addText != "" {
			t.Errorf("popup %v: the field stays open after the download, or the list closed", popup)
		}
		if got := b.p.dlTotal["unsloth/Qwen3.8-27B-GGUF:UD-IQ4_XS"]; got != 14_252_845_984 {
			t.Errorf("popup %v: the progress counts toward %d", popup, got)
		}
	}
}

// A complete name that is pasted, and not picked, gets the summary too.
func TestAddModelSummaryOfPastedName(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(true)
	b.typeText("unsloth/Qwen3.8-27B-GGUF:BF16")
	b.p.SetModelVariants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
	b.tt.Frame()
	b.want("54.7 GB", "more than the", "Download Qwen3.8-27B-GGUF")
	b.none("UD-Q4_K_M")
}

// What is not a name the engine accepts has no button; an empty or short
// field has the hint, a longer one does not.
func TestAddModelNoSummaryWithoutAName(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.want(addHint)
	b.typeText("q")
	b.want(addHint)
	b.typeText("wen not a model")
	b.none(addHint)
	if _, ok := b.p.addChosen(nil); ok {
		t.Error("a summary for text that is no name")
	}
	b.tt.Key(ui.Cmd, ui.KeyA)
	b.typeText("unsloth/Qwen3.8-27B-GGUF:")
	b.none(addHint, "Download Qwen3.8-27B-GGUF")
	b.tt.Key(ui.Cmd, ui.KeyA)
	b.typeText("not a:model")
	if _, ok := b.p.addChosen(nil); ok {
		t.Error("a summary for text that is no name")
	}
}

// While the server cannot take a download Enter does nothing, and the
// button, if it is still there, is off.
func TestAddModelSummaryBlocked(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(true)
	b.typeText("unsloth/Qwen3.8-27B-GGUF:BF16")
	b.p.SetModelVariants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
	b.tt.Frame()
	b.p.view.Switching = true
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	b.notCalled("Download unsloth/Qwen3.8-27B-GGUF:BF16")
	if !b.p.downloadBlocked() || !b.p.addingModel {
		t.Error("the field should stay open, the download blocked")
	}
	b.p.view = withPhase(core.PhaseStopped)
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	b.called("Download unsloth/Qwen3.8-27B-GGUF:BF16")
}
