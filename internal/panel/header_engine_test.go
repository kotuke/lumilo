package panel

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/locales"
)

// headerTitle is the engine button of the header: the name and version.
const headerTitle = "Splash 1.4.0"

// openEngines opens the engine list of the header.
func (b *bench) openEngines(title string) {
	b.t.Helper()
	b.click(title)
	b.tt.Frame()
	if !b.p.enginesOpen {
		b.t.Fatalf("the engine list did not open: %q", b.tt.Texts())
	}
}

// headerBoards are the boards of the header's engine switch.
func headerBoards() []board {
	var out []board
	// The open list in light and dark, in English, Russian and Arabic.
	for _, lang := range []string{"en", "ru", "ar"} {
		out = append(out, board{name: "header-engines-" + lang, lang: lang,
			build: func(b *bench) {
				b.show(withPhase(core.PhaseReady), time.Hour)
				b.openEngines(headerTitle)
			},
			check: func(b *bench) {
				b.want("Splash", "llama.cpp", "1.4.0", "0.7.0")
				// The rows start at the start of the line: the left, or the right in RTL.
				row, ok := b.tt.Find("llama.cpp")
				if !ok || row.X < 0 || row.X+row.W > Width {
					b.t.Fatalf("the list is off the panel: %+v (shown %v)", row, ok)
				}
				if rtl := format.RTL(b.lang); rtl != (row.X+row.W/2 > Width/2) {
					b.t.Errorf("rtl %v, row at %+v", rtl, row)
				}
			}})
	}
	out = append(out,
		// The server runs: choosing another engine asks first, in a dialog.
		board{name: "header-engines-confirm", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.openEngines(headerTitle)
			b.click("llama.cpp")
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("Switching to llama.cpp stops the server. Requests in progress are cut off", "Cancel", "Switch")
			b.notCalled("SwitchEngine llamacpp")
			if b.p.enginesOpen {
				b.t.Error("the list stays open under the dialog")
			}
			b.click("Switch")
			b.called("SwitchEngine llamacpp")
			b.none("Cancel")
			if b.p.dlg != dlgNone || b.p.switchTo != "" {
				b.t.Errorf("dialog %v, switchTo %q", b.p.dlg, b.p.switchTo)
			}
		}},
		board{name: "header-engines-cancel", build: func(b *bench) {
			b.show(withPhase(core.PhaseGenerating), time.Hour)
			b.openEngines(headerTitle)
			b.click("llama.cpp")
			b.tt.Frame()
		}, check: func(b *bench) {
			b.click("Cancel")
			b.none("Switch")
			b.notCalled("SwitchEngine llamacpp")
			if b.p.switchTo != "" {
				b.t.Errorf("switchTo %q", b.p.switchTo)
			}
		}},
		// No server of ours: the switch needs no question.
		board{name: "header-engines-stopped", build: func(b *bench) {
			b.show(withPhase(core.PhaseStopped), 0)
			b.openEngines(headerTitle)
		}, check: func(b *bench) {
			b.click("llama.cpp")
			b.called("SwitchEngine llamacpp")
			b.none("Switch", "Cancel")
			if b.p.enginesOpen {
				b.t.Error("the list stays open")
			}
		}},
		// The engine that is not installed leads to its install: the switch
		// is the same call as in Settings.
		board{name: "header-engines-choose-current", build: func(b *bench) {
			b.show(withPhase(core.PhaseStopped), 0)
			b.openEngines(headerTitle)
		}, check: func(b *bench) {
			b.click("Splash")
			b.notCalled("SwitchEngine splash")
			if b.p.enginesOpen {
				b.t.Error("the list stays open")
			}
		}},
		// Esc closes the list, a second click on the button too.
		board{name: "header-engines-esc", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.openEngines(headerTitle)
		}, check: func(b *bench) {
			b.tt.Key(0, ui.KeyEscape)
			b.tt.Frame()
			if b.p.enginesOpen {
				b.t.Error("Esc did not close the list")
			}
			b.none("Not installed")
			b.openEngines(headerTitle)
			b.click(headerTitle)
			b.tt.Frame()
			if b.p.enginesOpen {
				b.t.Error("the button did not close the list")
			}
		}},
		// Another popup or a dialog closes the list.
		board{name: "header-engines-exclusive", build: func(b *bench) {
			b.show(withPhase(core.PhaseGenerating), time.Hour)
			b.click("OrcaCyber-Nano-1.0")
			b.tt.Frame()
		}, check: func(b *bench) {
			if b.p.enginesOpen || !b.p.modelsOpen {
				b.t.Errorf("engines %v, models %v", b.p.enginesOpen, b.p.modelsOpen)
			}
			b.click(headerTitle)
			b.tt.Frame()
			if !b.p.enginesOpen || b.p.modelsOpen {
				b.t.Errorf("engines %v, models %v", b.p.enginesOpen, b.p.modelsOpen)
			}
			b.click("Quit")
			b.tt.Frame()
			if b.p.enginesOpen || b.p.dlg != dlgQuitBusy {
				b.t.Errorf("engines %v, dialog %v", b.p.enginesOpen, b.p.dlg)
			}
		}},
		board{name: "header-engines-reset", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.openEngines(headerTitle)
		}, check: func(b *bench) {
			b.p.Reset()
			b.tt.Frame()
			b.none("Not installed")
			b.click("Settings")
			b.click("Splash")
			b.click("llama.cpp")
			b.want("Switch")
			b.p.Reset()
			b.tt.Frame()
			if b.p.switchTo != "" {
				b.t.Errorf("switchTo %q", b.p.switchTo)
			}
		}},
		// Settings and the header share the confirmation; Settings still
		// asks under its list, not in a dialog.
		board{name: "settings-engines-no-dialog", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click("Settings")
			b.click("llama.cpp")
		}, check: func(b *bench) {
			if b.p.dlg != dlgNone || b.p.switchTo != "llamacpp" {
				b.t.Errorf("dialog %v, switchTo %q", b.p.dlg, b.p.switchTo)
			}
		}},
	)
	return out
}

// Where the server cannot be switched, the header shows no button.
func TestEngineSwitchDisabled(t *testing.T) {
	cases := []struct {
		name string
		v    func() core.View
	}{
		{"installing", func() core.View { return withPhase(core.PhaseInstalling) }},
		{"stopping", func() core.View { return withPhase(core.PhaseStopping) }},
		{"updating", func() core.View {
			v := withPhase(core.PhaseReady)
			v.Update = core.UpdateUpdating
			return v
		}},
		{"switching", func() core.View {
			v := withPhase(core.PhaseStopping)
			v.Switching = true
			return v
		}},
		{"one engine", func() core.View {
			v := withPhase(core.PhaseReady)
			v.Engines = v.Engines[:1]
			return v
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBench(t, "en")
			b.show(c.v(), time.Minute)
			// Not a button: a click on the name opens nothing.
			_ = b.tt.Click(headerTitle)
			b.tt.Frame()
			if b.p.enginesOpen || b.p.switcherOn() {
				t.Error("the engine list opened")
			}
			b.none("Not installed")
			// Settings disallows the same: its rows only show.
			if c.name != "one engine" && !b.p.engineBusy() {
				t.Error("Settings would allow the switch")
			}
		})
	}
	// A list that is open when the state turns busy closes.
	b := newBench(t, "en")
	b.show(withPhase(core.PhaseReady), time.Hour)
	b.openEngines(headerTitle)
	v := withPhase(core.PhaseReady)
	v.Switching = true
	b.show(v, 0)
	if b.p.enginesOpen {
		t.Error("the list stays open while switching")
	}
	// A dialog that waits for the answer goes with the server.
	b = newBench(t, "en")
	b.show(withPhase(core.PhaseReady), time.Hour)
	b.openEngines(headerTitle)
	b.click("llama.cpp")
	b.show(withPhase(core.PhaseStopped), 0)
	if b.p.dlg != dlgNone || b.p.switchTo != "" {
		t.Errorf("dialog %v, switchTo %q after the server stopped", b.p.dlg, b.p.switchTo)
	}
}

// The header fits the engine button, the update chip and the five icon
// buttons in every language, mirrored too, and the open list stays inside
// the window.
func TestHeaderEngineSwitchFits(t *testing.T) {
	for _, lang := range []string{"en", "ru", "de", "fr", "es", "ja", "zh-Hans", "ar"} {
		m := locales.In(format.Tag(lang))
		b := newBench(t, lang)
		b.p.OnResize = func(h int) { b.tt.SetSize(Width, h) }
		v := withPhase(core.PhaseGenerating)
		v.Update, v.Latest = core.UpdateAvailable, "1.4.1"
		b.show(v, 12*24*time.Hour+23*time.Hour)
		b.tt.SetSize(Width, b.p.height)
		b.tt.Frame()
		prevX := float32(-1)
		for _, label := range []string{m.Chat(), m.Log(), m.Settings(), m.ThemeLight(), m.Quit()} {
			if label == m.ThemeLight() {
				label = m.ThemeDark()
			}
			r, ok := b.tt.Find(label)
			if !ok {
				t.Errorf("%s: no button %q in %q", lang, label, b.tt.Texts())
				continue
			}
			if r.W < 25 || r.X < 0 || r.X+r.W > Width {
				t.Errorf("%s: %q is squeezed or off the panel: %+v", lang, label, r)
			}
			if prevX >= 0 && (r.X < prevX) != format.RTL(lang) {
				t.Errorf("%s: %q out of order: %+v", lang, label, r)
			}
			prevX = r.X
		}
		title, ok := b.tt.Find(headerTitle)
		if !ok {
			t.Errorf("%s: no engine name", lang)
			continue
		}
		chat, _ := b.tt.Find(m.Chat())
		if format.RTL(lang) {
			if title.X < chat.X+chat.W {
				t.Errorf("%s: the name %+v runs into the buttons %+v", lang, title, chat)
			}
		} else if title.X+title.W > chat.X {
			t.Errorf("%s: the name %+v runs into the buttons %+v", lang, title, chat)
		}
		// Open: every row inside the window.
		b.click("Splash 1.4.0")
		for range 3 {
			b.tt.Frame()
		}
		row, ok := b.tt.Find("llama.cpp")
		if !ok || row.Y+row.H > float32(b.p.height) {
			t.Errorf("%s: the list is cut off: %+v (shown %v), window %d", lang, row, ok, b.p.height)
		}
	}
}
