package panel

import (
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/locales"
)

// reviewBoards are the states the review of 27.10.2026 added or changed.
func reviewBoards() []board {
	en := locales.In("en")
	return []board{
		// A notice shows for a moment, with the error under its title.
		{name: "notice-terminal", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			b.show(v, time.Hour)
			v.Notice = core.Notice{Kind: core.NoticeTerminalFailed, Text: "exit status 1: open -b com.example.term", Seq: 1}
			b.show(v, 0)
		}, check: func(b *bench) {
			b.want(en.NoticeTerminalFailed(), "exit status 1: open -b com.example.term")
			b.clock = b.clock.Add(noticeFor)
			b.tt.Advance(noticeFor)
			b.tt.Frame()
			b.none(en.NoticeTerminalFailed())
		}},
		{name: "notice-agent", build: func(b *bench) {
			v := withPhase(core.PhaseStopping)
			b.show(v, time.Second)
			v.Notice = core.Notice{Kind: core.NoticeAgentUnavailable, Seq: 4}
			b.show(v, 0)
		}, check: func(b *bench) { b.want(en.NoticeAgentUnavailable()) }},
		// brew ran fine, but Homebrew's formula lags behind the release.
		{name: "update-no-newer", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Update, v.Latest, v.UpdateReason, v.UpdateError = core.UpdateFailed, "1.4.1", core.UpdateReasonNoNewer, "==> Upgrading splash"
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			b.want(en.UpdNoNewer())
			b.none("==> Upgrading splash")
		}},
		// The version a check found comes with its Update button.
		{name: "settings-check-update", build: func(b *bench) { b.checked(core.CheckAvailable) },
			check: func(b *bench) {
				b.want("Version 1.4.1 is available")
				b.click(en.UpdBtn())
				b.called("Update")
				if b.p.screen != screenMain {
					b.t.Error("Update did not go back to the main screen")
				}
			}},
		// While a request runs, the main screen asks first.
		{name: "settings-check-update-busy", build: func(b *bench) {
			b.checked(core.CheckAvailable)
			v := b.p.view
			v.Phase = core.PhaseGenerating
			b.show(v, 0)
		}, check: func(b *bench) {
			b.click(en.UpdBtn())
			b.notCalled("Update")
			if b.p.dlg != dlgUpdate {
				b.t.Errorf("dialog %v", b.p.dlg)
			}
		}},
		// The chat is off until the server answers, and says why.
		{name: "chat-off", build: func(b *bench) { b.show(withPhase(core.PhaseStopped), 0) },
			check: func(b *bench) {
				b.click(en.Chat())
				b.notCalled("OpenChat")
			}},
		// A switch to an engine that is not installed says that Homebrew
		// installs it, even with no server to stop.
		{name: "header-engines-install", build: func(b *bench) {
			v := withPhase(core.PhaseStopped)
			v.Engines = engineList("llamacpp")
			b.show(v, time.Hour)
			b.openEngines(headerTitle)
			b.click("llama.cpp")
		}, check: func(b *bench) {
			// It names what the install takes on disk: llama.cpp's 56 MB.
			b.want(en.EngineInstallNote("llama.cpp", "56 MB"), en.EngineInstallBtn())
			b.notCalled("SwitchEngine llamacpp")
			// The question stays while views come.
			b.show(b.p.view, time.Second)
			b.click(en.EngineInstallBtn())
			b.called("SwitchEngine llamacpp")
		}},
		{name: "settings-engines-install", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Engines = engineList("llamacpp")
			b.show(v, time.Hour)
			b.click("Settings")
			b.click("llama.cpp")
		}, check: func(b *bench) {
			b.want(en.EngineInstallStopNote("llama.cpp", "56 MB"), en.EngineInstallBtn())
			b.click(en.EngineInstallBtn())
			b.called("SwitchEngine llamacpp")
		}},
		// A download whose bytes grow toward a known size: how much, how
		// fast, how long.
		{name: "downloading-progress", build: func(b *bench) {
			v := withPhase(core.PhaseLoading)
			v.Models, v.Model = nil, engine.Model{}
			sg := v.Suggestions[0]
			v.Download, v.Downloading, v.DownloadBytes = sg.ID, true, 4_000_000_000
			b.show(v, 0)
			b.clock = b.clock.Add(10 * time.Second)
			v.DownloadBytes = 4_450_000_000 // 45 MB/s
			b.p.SetView(v)
			b.tt.Frame()
		}, check: func(b *bench) {
			total := b.p.view.Suggestions[0].DownloadBytes
			left := time.Duration(float64(total-4_450_000_000) / 45e6 * float64(time.Second))
			left = (left + time.Minute - 1).Truncate(time.Minute) // a countdown rounds up
			b.want("4.5 of 21.2 GB · 45 MB/s · " + en.DlLeft(format.ShortDuration("en", left)))
		}},
		// A download of a model no suggestion names: the size is the one the
		// engine said, the bytes so far are the engine's meter.
		{name: "downloading-engine-total", build: func(b *bench) {
			v := withPhase(core.PhaseLoading)
			v.Models = nil
			m := engine.Model{ID: "unsloth/Other-GGUF:Q8_0", Owner: "unsloth", Name: "Other-GGUF", Variant: "Q8_0"}
			v.Model, v.Download, v.Downloading, v.DownloadTotal, v.DownloadBytes = m, m.ID, true, 10_000_000_000, 1_000_000_000
			b.show(v, 0)
			b.clock = b.clock.Add(10 * time.Second)
			v.DownloadBytes = 2_000_000_000 // 100 MB/s
			b.p.SetView(v)
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("2 of 10 GB · 100 MB/s · " + en.DlLeft(format.ShortDuration("en", 2*time.Minute)))
		}},
		// A download of a known size at about 40 %: the bar fills, no sweep,
		// and the speed is the average of the last seconds.
		{name: "downloading-bar", build: func(b *bench) {
			b.show(downloadView(21_200_000_000, 7_000_000_000), 0)
			for _, d := range []int64{7_400_000_000, 7_500_000_000, 8_000_000_000} {
				b.clock = b.clock.Add(3 * time.Second)
				b.show(downloadView(21_200_000_000, d), 0)
			}
		}, check: func(b *bench) {
			if pct := b.p.downloadPercent(); pct < 37 || pct > 38 {
				b.t.Errorf("%v %%", pct)
			}
			b.want("8 of 21.2 GB · 111.1 MB/s")
		}},
		// Add model: a picked variant leaves the model's summary under the
		// field, with a Download button.
		{name: "add-model-summary", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("unsloth/Qwen3.8-27B-GGUF:")
			b.variants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
			b.tt.Frame()
			b.click("UD-IQ4_XS")
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("unsloth · UD-IQ4_XS · 14.3 GB", "Download Qwen3.8-27B-GGUF")
			b.click("Download Qwen3.8-27B-GGUF")
			b.called("Download unsloth/Qwen3.8-27B-GGUF:UD-IQ4_XS")
		}},
		// The same for a variant that does not fit in memory.
		{name: "add-model-summary-big", build: func(b *bench) {
			b.openAdd(true)
			b.typeText("unsloth/Qwen3.8-27B-GGUF:BF16")
			b.variants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
			b.tt.Frame()
		}, check: func(b *bench) {
			b.want("54.7 GB", "Download Qwen3.8-27B-GGUF")
		}},
		// The network access without a key: nothing started, Settings fixes it.
		{name: "error-no-key", build: func(b *bench) { b.show(failed(engine.FailNoKey, 0, ""), time.Minute) },
			check: func(b *bench) {
				b.want(en.ErrTitleNoKey(), en.ErrActNoKey())
				b.click(en.ErrActNoKey())
				if b.p.screen != screenSettings {
					b.t.Error("Open Settings did not open Settings")
				}
			}},
	}
}

// The times on the main screen run without a new View: the supervisor
// publishes only changes.
func TestClockRunsWithoutViews(t *testing.T) {
	b := newBench(t, "en")
	b.show(failed(engine.FailExit, 1, ""), 30*time.Second)
	b.want("Stopped 30 s ago")
	b.clock = b.clock.Add(31 * time.Second)
	b.tt.Advance(31 * time.Second)
	b.want("Stopped 1 min ago")

	b = newBench(t, "en")
	b.show(withPhase(core.PhaseExternal), 59*time.Minute+30*time.Second)
	b.clock = b.clock.Add(time.Minute)
	b.tt.Advance(time.Minute)
	b.want(b.m.SubExternalShort("1 h"))
}
