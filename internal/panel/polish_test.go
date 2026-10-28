package panel

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/locales"
)

// The subtitle of a foreign server shows its whole uptime in every
// language: it is not cut by the ellipsis.
func TestExternalSubtitleFits(t *testing.T) {
	for _, lang := range []string{"en", "ru", "de", "fr", "es", "ja", "zh-Hans", "ar"} {
		m := locales.In(format.Tag(lang))
		for _, d := range []time.Duration{59 * time.Second, 23*time.Hour + 59*time.Minute, 12*24*time.Hour + 23*time.Hour} {
			sub := m.SubExternalShort(format.ShortDuration(lang, d))
			b := newBench(t, lang)
			b.show(withPhase(core.PhaseExternal), d)
			r, ok := b.tt.Find(sub)
			if !ok {
				t.Fatalf("%s: no %q", lang, sub)
			}
			var w float32
			ui.NewTester(func(c *ui.Context) { w, _ = c.MeasureText(0, ui.Span{Text: sub, Size: 11}) }, 100, 100)
			if w > r.W+0.5 {
				t.Errorf("%s %v: %q needs %v, has %v", lang, d, sub, w, r.W)
			}
		}
	}
}

// The header subtitle stays on one line in every language, for the longest
// times it shows, and does not push the buttons off the header.
//
// SingleLine makes the height check hold by itself: it ends a text that is
// too long in an ellipsis. With it removed (checked by hand) Ready,
// Generating, Released and Restoring still fit in all 8 languages; the
// statuses with a longer text (Loading and Stopped-ago in ru and de, "Started
// in another app" everywhere) wrap, and so end in an ellipsis here.
func TestSubtitleOneLine(t *testing.T) {
	for _, lang := range []string{"en", "ru", "de", "fr", "es", "ja", "zh-Hans", "ar"} {
		for _, d := range []time.Duration{59 * time.Second, 23*time.Hour + 59*time.Minute, 12*24*time.Hour + 23*time.Hour} {
			m := locales.In(format.Tag(lang))
			short := format.ShortDuration(lang, d)
			cases := []struct {
				ph   core.Phase
				want string
			}{
				{core.PhaseLoading, m.SubLoading(short)},
				{core.PhaseReady, m.SubReady(short)},
				{core.PhaseGenerating, m.SubGenerating(short)},
				{core.PhaseReleased, m.SubReleased(short)},
				{core.PhaseRestoring, m.SubReady(short)},
				{core.PhaseError, m.SubError(short)},
				{core.PhaseExternal, m.SubExternalShort(short)},
			}
			// One line of the same size, from a subtitle that is short.
			ref := newBench(t, lang)
			ref.show(withPhase(core.PhaseStopped), time.Hour)
			one, ok := ref.tt.Find(m.SubStopped())
			if !ok {
				t.Fatalf("%s: no %q", lang, m.SubStopped())
			}
			for _, c := range cases {
				b := newBench(t, lang)
				b.show(withPhase(c.ph), d)
				r, ok := b.tt.Find(c.want)
				if !ok {
					t.Errorf("%s %v %v: no %q in %q", lang, c.ph, d, c.want, b.tt.Texts())
					continue
				}
				if r.H > one.H+0.5 {
					t.Errorf("%s %v %v: %q is %v tall, one line is %v", lang, c.ph, d, c.want, r.H, one.H)
				}
				quit, ok := b.tt.Find(m.Quit())
				if !ok || quit.X < 0 || quit.X+quit.W > Width {
					t.Errorf("%s %v %v: the Quit button is off the panel: %+v", lang, c.ph, d, quit)
				}
			}
		}
	}
}
