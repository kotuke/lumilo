package panel

import (
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
)

// A select's list opens next to its button, its end 6 DIPs past the
// button's end, in either direction of the text: as NSPopUpButton's.
func TestSelectOpensAtItsButton(t *testing.T) {
	for _, lang := range []string{"en", "ar"} {
		b := newBench(t, lang)
		b.show(withPhase(core.PhaseReady), time.Hour)
		b.click(b.m.Settings())
		trig, ok := b.tt.Find(b.m.Language())
		if !ok {
			t.Fatalf("%s: no %q", lang, b.m.Language())
		}
		b.clickEnd(b.m.Language())
		b.tt.Frame()
		item, ok := b.tt.Find(format.LanguageName("ja"))
		if !ok {
			t.Fatalf("%s: the list did not open: %q", lang, b.tt.Texts())
		}
		// The list lies above the row, within a row's height of it, and
		// its items within the panel at the row's end.
		if gap := trig.Y - (item.Y + item.H); gap < 0 || gap > 120 {
			t.Errorf("%s: the item %+v is %v above the row %+v", lang, item, gap, trig)
		}
		if item.X < 0 || item.X+item.W > Width {
			t.Errorf("%s: the item %+v is off the panel", lang, item)
		}
		end := item.X + item.W
		if format.RTL(lang) {
			end = Width - item.X
		}
		if end < Width/2 {
			t.Errorf("%s: the list opens at the row's start, far from its button: %+v", lang, item)
		}
	}
}
