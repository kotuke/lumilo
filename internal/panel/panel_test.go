package panel

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/llamacpp"
	"github.com/kotuke/lumilo/internal/engines/splash"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/locales"
)

// recorder is a fake Actions that writes down every call.
type recorder struct {
	calls []string
	prefs []Prefs
	busy  map[int]bool
	keys  int
	eng   engine.Engine // ParseModel asks it; nil = Splash
	// search and variants stand for the lookups of the app, which answer
	// later through SetModelHits and SetModelVariants.
	search, variants func(string)
}

func (r *recorder) add(s string, a ...any) { r.calls = append(r.calls, fmt.Sprintf(s, a...)) }

func (r *recorder) Download(id string, o engine.ModelOptions) {
	if o.NoVision {
		r.add("Download %s text-only", id)
		return
	}
	r.add("Download %s", id)
}
func (r *recorder) CancelDownload(id string)  { r.add("CancelDownload %s", id) }
func (r *recorder) DismissDownload(id string) { r.add("DismissDownload %s", id) }
func (r *recorder) PauseDownload(id string)   { r.add("PauseDownload %s", id) }
func (r *recorder) PlanRemove(id string)      { r.add("PlanRemove %s", id) }
func (r *recorder) CancelRemove()             { r.add("CancelRemove") }
func (r *recorder) RemoveModel(id string)     { r.add("RemoveModel %s", id) }
func (r *recorder) ServeTextOnly(id string)   { r.add("ServeTextOnly %s", id) }
func (r *recorder) SwitchEngine(id string)    { r.add("SwitchEngine %s", id) }
func (r *recorder) SearchModels(text string) {
	r.add("SearchModels %s", text)
	if r.search != nil {
		r.search(text)
	}
}
func (r *recorder) ModelVariants(repo string) {
	r.add("ModelVariants %s", repo)
	if r.variants != nil {
		r.variants(repo)
	}
}
func (r *recorder) CancelModelSearch() { r.add("CancelModelSearch") }
func (r *recorder) ParseModel(spec string) (engine.Model, error) {
	if r.eng == nil {
		return splash.New().ParseModel(spec)
	}
	return r.eng.ParseModel(spec)
}

func (r *recorder) Start()                { r.add("Start") }
func (r *recorder) StartOn(port int)      { r.add("StartOn %d", port) }
func (r *recorder) Stop()                 { r.add("Stop") }
func (r *recorder) ForceStop()            { r.add("ForceStop") }
func (r *recorder) Restart()              { r.add("Restart") }
func (r *recorder) KillExternal()         { r.add("KillExternal") }
func (r *recorder) Release()              { r.add("Release") }
func (r *recorder) Restore()              { r.add("Restore") }
func (r *recorder) SelectModel(id string) { r.add("SelectModel %s", id) }
func (r *recorder) Install()              { r.add("Install") }
func (r *recorder) Update()               { r.add("Update") }
func (r *recorder) CheckUpdate()          { r.add("CheckUpdate") }
func (r *recorder) RunAgent(id string)    { r.add("RunAgent %s", id) }
func (r *recorder) Recheck()              { r.add("Recheck") }
func (r *recorder) OpenChat()             { r.add("OpenChat") }
func (r *recorder) OpenLog()              { r.add("OpenLog") }
func (r *recorder) OpenURL(url string)    { r.add("OpenURL %s", url) }
func (r *recorder) Copy(text string)      { r.add("Copy %s", text) }
func (r *recorder) Quit()                 { r.add("Quit") }
func (r *recorder) CheckAppUpdate()       { r.add("CheckAppUpdate") }
func (r *recorder) DownloadAppUpdate()    { r.add("DownloadAppUpdate") }
func (r *recorder) RestartToUpdate()      { r.add("RestartToUpdate") }
func (r *recorder) ToggleTheme()          { r.add("ToggleTheme") }
func (r *recorder) SetPrefs(p Prefs)      { r.add("SetPrefs"); r.prefs = append(r.prefs, p) }
func (r *recorder) NewAPIKey() string {
	r.keys++
	r.add("NewAPIKey")
	return fmt.Sprintf("sk-lumilo-%028x", r.keys)
}
func (r *recorder) PortFree(port int) bool { return !r.busy[port] }

func (r *recorder) has(call string) bool { return slices.Contains(r.calls, call) }

var t0 = time.Date(2026, 10, 26, 14, 20, 0, 0, time.UTC)

var sampleModel = engine.Model{
	ID: "orcarouter/OrcaCyber-Nano-1.0:Mix-15.7G", Name: "OrcaCyber-Nano-1.0", Owner: "orcarouter",
	Variant: "Mix-15.7G", SizeBytes: 15_700_000_000,
}

// The mockup's sample speeds, oldest first.
var sampleHistory = []float64{31, 36, 40, 38, 35, 42, 39, 37, 33, 36, 41, 38, 40, 37, 39, 36, 38, 40, 37, 39}

// sample is a ready server with the mockup's sample numbers.
func sample() core.View {
	v := core.View{
		Engine: "Splash", EngineID: "splash", Version: "1.4.0",
		Phase: core.PhaseReady,
		Model: sampleModel, Models: []engine.Model{sampleModel},
		Port: 8000, Address: "127.0.0.1:8000", Access: engine.AccessLocal,
		Snapshot: engine.Snapshot{
			Reachable: true, Ready: true, Authorized: true, Model: sampleModel.ID,
			MemoryUsed: 17_200_000_000, MemoryLimit: 38_000_000_000,
			DiskUsed: 6_100_000_000, DiskLimit: 32_000_000_000, IdleRelease: 10 * time.Minute,
		},
		Speed: core.Speed{
			History: slices.Clone(sampleHistory), Average: 38, FirstToken: 800 * time.Millisecond, Read: 1240,
			LastRequest: t0,
		},
		IdleLeft: 7 * time.Minute,
		LogPath:  "/tmp/splash.log",
		ChatURL:  "http://127.0.0.1:8000/",
	}
	core.Describe(&v, splash.New())
	v.Engines = engineList()
	return v
}

// bare is v with Splash's own parts: name, logo, formula, agents.
func bare(v core.View) core.View {
	agents := v.Agents
	core.Describe(&v, splash.New())
	v.Agents = agents
	v.Engines = engineList("splash")
	return v
}

// engineList is the engine list with Splash 1.4.0 and llama.cpp 0.7.0, or
// llama.cpp not installed when notInstalled names it.
func engineList(notInstalled ...string) []core.EngineInfo {
	var list []core.EngineInfo
	for _, e := range []engine.Engine{splash.New(), llamacpp.New()} {
		info := e.Info()
		list = append(list, core.EngineInfo{ID: info.ID, Name: info.Name, Logo: info.Logo, Installed: true,
			Install: e.Installer().Describe()})
	}
	list[0].Version, list[1].Version = "1.4.0", "0.7.0"
	for i := range list {
		if slices.Contains(notInstalled, list[i].ID) {
			list[i].Installed, list[i].Version = false, ""
		}
	}
	return list
}

// llamaView is a ready llama.cpp server with a GGUF model.
func llamaView() core.View {
	v := sample()
	core.Describe(&v, llamacpp.New())
	v.Version = "0.7.0"
	v.LogPath = "/tmp/llamacpp.log"
	m := engine.Model{ID: "/hub/models--ggml-org--Qwen3-0.6B-GGUF/snapshots/c/Qwen3-0.6B-Q8_0.gguf",
		Name: "Qwen3-0.6B-GGUF", Owner: "ggml-org", Variant: "Q8_0", SizeBytes: 639_000_000, ContextLength: 40960}
	v.Model, v.Models = m, []engine.Model{m}
	v.Snapshot.Model, v.Snapshot.MemoryLimit, v.Snapshot.DiskUsed, v.Snapshot.DiskLimit = m.ID, 0, 0, 0
	v.Snapshot.MemoryUsed = 1_100_000_000
	v.Engines = engineList()
	return v
}

func defaultPrefs() Prefs {
	return Prefs{Access: engine.AccessLocal, Port: 8000, IdleRelease: 10 * time.Minute,
		AllowedHosts: []string{"computer.local"}, OpenAtLogin: true}
}

// bench is one panel in a tester, with a clock of its own.
type bench struct {
	t     *testing.T
	p     *Panel
	tt    *ui.Tester
	rec   *recorder
	clock time.Time
	m     locales.Messages
	lang  string
	dark  bool // the dark theme, for the checks that read colours
}

func newBench(t *testing.T, lang string) *bench {
	t.Helper()
	rec := &recorder{busy: map[int]bool{}}
	b := &bench{t: t, rec: rec, clock: t0, lang: lang, m: locales.In(format.Tag(lang))}
	b.p = New(rec)
	b.p.now = func() time.Time { return b.clock }
	b.p.SetMemory(48 << 30)
	b.p.SetLANAddress("192.168.1.20")
	b.p.SetPrefs(defaultPrefs())
	b.tt = ui.NewTester(b.p.View, Width, 1400)
	b.tt.SetLocale(lang)
	if format.RTL(lang) {
		b.tt.SetDirection(ui.RightToLeft)
	}
	return b
}

// show publishes v as if it had been on screen for d.
func (b *bench) show(v core.View, d time.Duration) {
	b.p.SetView(v)
	b.clock = b.clock.Add(d)
	b.p.SetView(v)
	b.tt.Frame()
}

func (b *bench) want(texts ...string) {
	b.t.Helper()
	for _, s := range texts {
		if !b.tt.HasText(s) {
			b.t.Errorf("no %q in %q", s, b.tt.Texts())
		}
	}
}

func (b *bench) none(texts ...string) {
	b.t.Helper()
	for _, s := range texts {
		if b.tt.HasText(s) {
			b.t.Errorf("unexpected %q", s)
		}
	}
}

// click clicks the element showing s. A click that changes the screen
// waits until the screens have slid.
func (b *bench) click(s string) {
	b.t.Helper()
	before := b.p.screen
	if err := b.tt.Click(s); err != nil {
		b.t.Fatalf("click %q: %v in %q", s, err, b.tt.Texts())
	}
	if b.p.screen != before {
		b.settle()
	}
}

// settle lets the screens slide to their place.
func (b *bench) settle() { b.tt.Advance(slideAnimation.Duration + 100*time.Millisecond) }

// clickNext clicks the button after the one labelled s in the same row,
// as the confirm button of a dialog after its Cancel.
func (b *bench) clickNext(s string) {
	b.t.Helper()
	r, ok := b.tt.Find(s)
	if !ok {
		b.t.Fatalf("no %q", s)
	}
	x := r.X + r.W + 6 + 14 + 12
	if format.RTL(b.lang) {
		x = r.X - 6 - 14 - 12
	}
	b.tt.ClickAt(x, r.Y+r.H/2)
}

// clickEnd clicks the control at the end of the settings row labelled s,
// as the port field.
func (b *bench) clickEnd(s string) {
	b.t.Helper()
	r, ok := b.tt.Find(s)
	if !ok {
		b.t.Fatalf("no %q", s)
	}
	x := float32(Width - 10 - 1 - 12 - 20)
	if format.RTL(b.lang) {
		x = 10 + 1 + 12 + 20
	}
	b.tt.ClickAt(x, r.Y+r.H/2)
}

func (b *bench) called(calls ...string) {
	b.t.Helper()
	for _, c := range calls {
		if !b.rec.has(c) {
			b.t.Errorf("no call %q in %q", c, b.rec.calls)
		}
	}
}

func (b *bench) notCalled(calls ...string) {
	b.t.Helper()
	for _, c := range calls {
		if b.rec.has(c) {
			b.t.Errorf("unexpected call %q in %q", c, b.rec.calls)
		}
	}
}

// board is one mockup board (canvas.json) built from a View.
type board struct {
	name string
	lang string
	// build puts the panel in the board's state.
	build func(b *bench)
	// check asserts what the board shows and what its controls do.
	check func(b *bench)
}

// slide clicks s, which changes the screen, and lets the screens slide
// for d.
func (b *bench) slide(s string, d time.Duration) {
	b.t.Helper()
	if err := b.tt.Click(s); err != nil {
		b.t.Fatalf("click %q: %v", s, err)
	}
	b.tt.Advance(d)
}

// checked opens Settings, checks for updates and shows the result.
func (b *bench) checked(res core.CheckResult) {
	b.t.Helper()
	v := withPhase(core.PhaseReady)
	b.show(v, time.Hour)
	b.click(b.m.Settings())
	b.click(b.m.CheckUpdates())
	v.Checking, v.CheckSeq = true, 1
	b.show(v, 0)
	v.Checking, v.Check = false, res
	if res == core.CheckAvailable {
		v.Latest, v.Update = "1.4.1", core.UpdateAvailable
	}
	b.show(v, 0)
}

func withPhase(ph core.Phase) core.View {
	v := sample()
	v.Phase = ph
	switch ph {
	case core.PhaseGenerating:
		v.Snapshot.MemoryUsed, v.Snapshot.DiskUsed = 21_400_000_000, 6_400_000_000
		v.Snapshot.Active, v.Snapshot.Queued = 1, 2
		v.Speed.Current = 41
	case core.PhaseReleased:
		v.Snapshot.MemoryUsed, v.Snapshot.DiskUsed, v.Snapshot.Released = 900_000_000, 6_400_000_000, true
	case core.PhaseRestoring:
		v.Snapshot.MemoryUsed, v.Snapshot.DiskUsed, v.Snapshot.Restoring = 9_800_000_000, 6_400_000_000, true
	case core.PhaseReady, core.PhaseExternal:
	default:
		v.Snapshot, v.IdleLeft, v.ChatURL = engine.Snapshot{}, 0, ""
	}
	return v
}

func failed(kind engine.FailureKind, code int, line string) core.View {
	v := withPhase(core.PhaseError)
	v.Failure = engine.Failure{Kind: kind, Code: code, Line: line}
	if kind == engine.FailPortBusy {
		v.FreePort = 8001
	}
	return v
}

func boards() []board {
	en := locales.In("en")
	return []board{
		{name: "stopped", build: func(b *bench) { b.show(withPhase(core.PhaseStopped), 0) },
			check: func(b *bench) {
				b.want("Splash 1.4.0", "Server not running", "Stopped", "OrcaCyber-Nano-1.0", "orcarouter · Mix-15.7G",
					"Model updates off", "127.0.0.1:8000", "Start", "Launch agent")
				b.none("Memory", "Generation speed")
				b.click("Start")
				b.click("Claude")
				b.click("Copy address 127.0.0.1:8000")
				b.called("Start", "RunAgent claude", "Copy 127.0.0.1:8000")
				// The chat is off while no server answers.
				b.click("Open chat")
				b.notCalled("OpenChat")
				// The model list opens and picks.
				b.click("OrcaCyber-Nano-1.0")
				b.want(en.PopNote(), "Mix-15.7G · 15.7 GB")
			}},
		{name: "models-open", build: func(b *bench) {
			v := withPhase(core.PhaseStopped)
			other := engine.Model{ID: "orcarouter/OrcaCyber-Mini-1.0:Q4", Name: "OrcaCyber-Mini-1.0", Owner: "orcarouter",
				Variant: "Q4", SizeBytes: 4_200_000_000}
			v.Models = append(v.Models, other)
			b.show(v, 0)
			b.click("OrcaCyber-Nano-1.0")
		}, check: func(b *bench) {
			b.want(locales.In("en").PopNote(), "Q4 · 4.2 GB")
			b.click("OrcaCyber-Mini-1.0")
			b.called("SelectModel orcarouter/OrcaCyber-Mini-1.0:Q4")
			b.none(locales.In("en").PopNote())
		}},
		{name: "loading", build: func(b *bench) { b.show(withPhase(core.PhaseLoading), 24*time.Second) },
			check: func(b *bench) {
				b.want("Loading", "Started 24 s ago", en.LoadingText(), "Cancel")
				b.click("Cancel")
				b.called("Stop")
			}},
		{name: "ready", build: func(b *bench) { b.show(withPhase(core.PhaseReady), 2*time.Hour+14*time.Minute) },
			check: func(b *bench) {
				b.want("Ready", "Running for 2 h 14 min", "Unloads after 7 minutes idle", "Memory", "17.2 GB", "of 38 GB",
					"Conversations on disk", "6.1 of 32 GB", "Generation speed", "38 tok/s", "on average",
					"First token 0.8 s", "Prompt processing 1,240 tok/s", "Unload", "Stop")
				b.none("Almost full")
				b.click("Unload")
				b.click("Stop")
				b.click("Open chat")
				b.click("Log")
				b.click("Settings")
				b.click("Back")
				// Nothing to lose: Quit needs no confirmation.
				b.click("Quit")
				b.called("Release", "Stop", "OpenChat", "OpenLog", "Quit")
			}},
		{name: "memory-high", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Snapshot.Pressure = engine.PressureCritical
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			// The bar is at 45 %: the system's pressure is the cause.
			b.want("Memory pressure is high")
			b.none("Almost full")
		}},
		{name: "memory-full", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Snapshot.MemoryUsed = 35_000_000_000
			b.show(v, time.Hour)
		}, check: func(b *bench) { b.want("Almost full") }},
		// llama.cpp reports no budget: the bar fills against three quarters
		// of the Mac's memory.
		{name: "llamacpp-memory-bar", build: func(b *bench) { b.show(llamaView(), time.Hour) },
			check: func(b *bench) { b.want("1.1 GB", "of 38.7 GB") }},
		{name: "memory-warning", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Snapshot.Pressure = engine.PressureWarning
			b.show(v, time.Hour)
		}, check: func(b *bench) { b.none("Almost full") }},
		{name: "generating", build: func(b *bench) { b.show(withPhase(core.PhaseGenerating), 2*time.Hour+14*time.Minute) },
			check: func(b *bench) {
				b.want("Generating", "Running for 2 h 14 min", "21.4 GB", "6.4 of 32 GB", "2 queued", "Stop")
				b.none("Unload", "Load", "Unloads after 7 minutes idle")
				// Hover and the arrows show a bar's speed beside the average:
				// the text shows once more than the bars' own labels.
				labels := map[string]int{"This request 41 tok/s": 1, "Request 39 tok/s": 3, "Request 36 tok/s": 3}
				shown := func(s string) bool {
					n := 0
					for _, x := range b.tt.Texts() {
						if x == s {
							n++
						}
					}
					return n == labels[s]+1
				}
				r, ok := b.tt.Find("This request 41 tok/s")
				if !ok {
					b.t.Fatalf("no current bar in %q", b.tt.Texts())
				}
				b.tt.Move(r.X+r.W/2, r.Y+r.H/2)
				if !shown("This request 41 tok/s") {
					b.t.Error("hover shows no speed")
				}
				b.tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
				b.tt.Move(1, 1)
				for _, k := range []struct {
					key  ui.Key
					want string
				}{{ui.KeyLeft, "Request 39 tok/s"}, {ui.KeyHome, "Request 36 tok/s"}, {ui.KeyEnd, "This request 41 tok/s"}} {
					b.tt.Key(0, k.key)
					if !shown(k.want) {
						b.t.Errorf("after %v: no %q beside the average", k.key, k.want)
					}
				}
			}},
		{name: "released", build: func(b *bench) { b.show(withPhase(core.PhaseReleased), 3*time.Hour+2*time.Minute) },
			check: func(b *bench) {
				b.want("Unloaded", "Running for 3 h 2 min", "0.9 GB", "Load", "Loads on first request",
					"Last request 3 hours 2 minutes ago")
				b.click("Load")
				b.called("Restore")
			}},
		{name: "restoring", build: func(b *bench) { b.show(withPhase(core.PhaseRestoring), 3*time.Hour+2*time.Minute) },
			check: func(b *bench) {
				// The weights are coming back: loading, not ready; the memory
				// card stays and Load is off.
				b.want("Loading", "Running for 3 h 2 min", "Memory", "9.8 GB", "Load", "Stop")
				b.none("Ready", "Unloaded", "Unload", en.LoadingText())
				if err := b.tt.Click("Load"); err == nil {
					b.notCalled("Restore")
				}
				b.click("Stop")
				b.called("Stop")
			}},
		{name: "speed-empty", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Speed = core.Speed{}
			b.show(v, 5*time.Minute)
		}, check: func(b *bench) {
			// No request has finished: one line instead of the average and the chart.
			b.want("Ready", "Generation speed", en.SpeedEmpty(), "Memory")
			b.none("on average", "First token", "Prompt processing")
			v := withPhase(core.PhaseGenerating)
			v.Speed = core.Speed{Current: 41, Tokens: 312}
			b.show(v, 0)
			// The first generation brings the card with its live bar, led by
			// the live speed: nothing to set it against yet.
			b.want("Generating", "41 tok/s", "now", "Tokens: 312", "This request 41 tok/s")
			b.none(en.SpeedEmpty(), "on average", "↑", "↓")
			// With an average, the live speed says how it stands to it.
			// The first request ended and the next one runs: its live speed
			// glides there, its tokens count from its own.
			v.Speed = core.Speed{History: []float64{40}, Average: 40, Current: 44, Tokens: 1}
			b.show(v, time.Second)
			b.tt.Advance(5 * time.Second)
			b.want("44 tok/s", "now", "↑ 10%", "Tokens: 1")
		}},
		{name: "speed-empty-external", build: func(b *bench) {
			v := withPhase(core.PhaseExternal)
			v.External = &engine.Running{PID: 4242, Port: 8080, Model: sampleModel.ID}
			v.Port, v.Address = 8080, "127.0.0.1:8080"
			v.Speed = core.Speed{}
			b.show(v, time.Hour)
			// A new foreign server has answered nothing: the one-line card.
			b.want(en.SpeedEmpty())
			v.Snapshot.Completed = 7
			b.show(v, 0)
		}, check: func(b *bench) {
			// One that has answered before Lumilo looked: no false claim.
			b.none(en.SpeedEmpty())
			b.want("Generation speed", "on average")
		}},
		{name: "speed-empty-released", build: func(b *bench) {
			v := withPhase(core.PhaseReleased)
			v.Speed = core.Speed{}
			b.show(v, 5*time.Minute)
		}, check: func(b *bench) {
			b.want("Unloaded", en.SpeedEmpty(), "Loads on first request")
			b.none("on average", "Last request")
		}},
		{name: "error-port", build: func(b *bench) {
			b.show(failed(engine.FailPortBusy, 1, "error: cannot bind 127.0.0.1:8000: [Errno 48] Address already in use"), 3*time.Minute)
		}, check: func(b *bench) {
			b.want("Error", "Stopped 3 min ago", "Port 8000 is in use by another app",
				"error: cannot bind 127.0.0.1:8000: [Errno 48] Address already in use", "Change port", "Start on 8001")
			b.click("Start on 8001")
			b.called("StartOn 8001")
			b.click("Change port")
			b.want("Settings", "Port 8000 is in use. 8001 is free", "Use 8001")
			b.click("Use 8001")
			if n := len(b.rec.prefs); n == 0 || b.rec.prefs[n-1].Port != 8001 {
				b.t.Errorf("Use 8001 stored %+v", b.rec.prefs)
			}
		}},
		{name: "error-model", build: func(b *bench) {
			v := failed(engine.FailModel, 1, "cannot resolve orcarouter/OrcaCyber-Nano-1.0: HF_HUB_OFFLINE is set")
			v.Failure.Offline = true
			b.show(v, time.Minute)
		}, check: func(b *bench) {
			b.want(en.ErrTitleModel(), "Choose model", "Restart")
			b.click("Choose model")
			b.want(en.PopNote())
			b.click("Restart")
			b.called("Restart")
		}},
		{name: "error-memory", build: func(b *bench) {
			b.show(withPhase(core.PhaseLoading), 10*time.Minute)
			b.show(failed(engine.FailMemory, 1, ""), 0)
		}, check: func(b *bench) {
			b.want("The model ran out of memory: Splash waited 10 minutes. Close other apps and start again", "Restart")
			b.click("Log")
			b.called("OpenLog")
		}},
		{name: "error-exit", build: func(b *bench) { b.show(failed(engine.FailExit, 1, ""), time.Minute) },
			check: func(b *bench) {
				b.want("Process exited with code 1", "Restart")
				b.click("Claude")
				b.notCalled("RunAgent claude")
			}},
		{name: "external", build: func(b *bench) {
			v := withPhase(core.PhaseExternal)
			v.External = &engine.Running{PID: 4242, Port: 8080, Model: sampleModel.ID}
			v.Port, v.Address = 8080, "127.0.0.1:8080"
			b.show(v, 2*time.Hour+14*time.Minute)
		}, check: func(b *bench) {
			b.want("Running", "Another app · 2 h 14 min", en.ExtNote(), "127.0.0.1:8080", "End process")
			b.none("Model updates off", "Unloads after 7 minutes idle")
			b.click("End process")
			b.want(en.KillTitle("Splash"), en.KillNote())
			b.clickNext("Cancel")
			b.called("KillExternal")
		}},
		{name: "unresponsive", build: func(b *bench) { b.show(withPhase(core.PhaseUnresponsive), 33*time.Second) },
			check: func(b *bench) {
				b.want("Not responding", "No response for 45 s",
					"The server has not responded for 45 seconds and the queue is stuck. It may be hung", "Force quit")
				b.none("Stop", "Start")
				b.click("Force quit")
				b.called("ForceStop")
			}},
		{name: "stopping", build: func(b *bench) { b.show(withPhase(core.PhaseStopping), time.Second) },
			check: func(b *bench) {
				b.want("Freeing memory", "Stopping")
				b.none("Force quit")
			}},
		{name: "stopping-long", build: func(b *bench) {
			v := withPhase(core.PhaseStopping)
			v.StoppingLong = true
			b.show(v, 15*time.Second)
		}, check: func(b *bench) {
			b.want(en.StopLong(), "Force quit")
			b.click("Force quit")
			b.called("ForceStop")
		}},
		{name: "quit-confirm", build: func(b *bench) {
			b.show(withPhase(core.PhaseGenerating), time.Minute)
			b.click("Quit")
		}, check: func(b *bench) {
			// Active and queued requests are cut: 1 + 2.
			b.want("Quit Lumilo?", "3 requests will be interrupted", "Cancel")
			b.notCalled("Quit")
			if !b.tt.Focused("Cancel") {
				b.t.Error("Cancel has no focus")
			}
			b.tt.Key(0, ui.KeyEscape)
			b.none("Quit Lumilo?")
			b.click("Quit")
			b.clickNext("Cancel")
			b.called("Quit")
		}},
		{name: "update-available", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Update, v.Latest = core.UpdateAvailable, "1.4.1"
			v.NotesURL = splash.New().Installer().ReleaseNotesURL(v.Latest)
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			b.want("1.4.1", "Splash 1.4.1 is ready to install. The server will restart", "Update", "What’s new")
			b.click("What’s new")
			b.called("OpenURL https://github.com/incoai/splash/releases/tag/1.4.1")
			b.click("Version 1.4.1 available")
			b.called("Update")
		}},
		{name: "updating", build: func(b *bench) {
			v := withPhase(core.PhaseStopped)
			v.Update, v.Latest = core.UpdateUpdating, "1.4.1"
			b.show(v, time.Minute)
		}, check: func(b *bench) {
			b.want("Updating", "Updating Splash to 1.4.1", en.UpdText(), "brew upgrade incoai/tap/splash")
			b.none("Start", "Launch agent", "Model updates off")
			b.click("Quit")
			b.want("Splash is still updating", "Quit when the update finishes?")
			b.click("Quit after update")
			b.notCalled("Quit")
			v := withPhase(core.PhaseStopped)
			v.Latest = "1.4.1"
			b.p.SetView(v)
			b.called("Quit")
		}},
		{name: "update-failed", build: func(b *bench) {
			v := withPhase(core.PhaseReady)
			v.Update, v.Latest = core.UpdateFailed, "1.4.1"
			v.UpdateError = `Error: Failed to download resource "splash" (Could not resolve host: github.com)`
			b.show(v, time.Hour)
		}, check: func(b *bench) {
			b.want("Could not update Splash to 1.4.1", "The previous version 1.4.0 is still running",
				`Error: Failed to download resource "splash" (Could not resolve host: github.com)`, "Retry")
			b.click("Retry")
			b.called("Update")
		}},
		{name: "update-confirm", build: func(b *bench) {
			v := withPhase(core.PhaseGenerating)
			v.Update, v.Latest = core.UpdateAvailable, "1.4.1"
			b.show(v, time.Hour)
			b.click("Update")
		}, check: func(b *bench) {
			b.want("Update now?", "When idle")
			b.notCalled("Update")
			b.click("When idle")
			b.notCalled("Update")
			v := withPhase(core.PhaseReady)
			v.Update, v.Latest = core.UpdateAvailable, "1.4.1"
			b.p.SetView(v)
			b.called("Update")
		}},
		{name: "not-installed", build: func(b *bench) {
			v := bare(core.View{Phase: core.PhaseNotInstalled, Latest: "1.4.1", Agents: splash.New().Info().Agents})
			b.show(v, 0)
		}, check: func(b *bench) {
			b.want("Splash is not installed", "Install Splash to run models",
				"Splash is installed with Homebrew. It takes about a minute and needs about 270 MB of disk space",
				"brew install incoai/tap/splash", "Install", en.InstNote())
			b.none("Launch agent", "Open chat")
			b.click("Install")
			b.called("Install")
		}},
		{name: "no-brew", build: func(b *bench) {
			b.show(bare(core.View{Phase: core.PhaseNoInstaller}), 0)
		}, check: func(b *bench) {
			b.want("Homebrew needed", en.BrewText("Splash"), "Open brew.sh", "Check again",
				"Requires Apple silicon and macOS 26.4 or later")
			b.click("Open brew.sh")
			b.click("Check again")
			b.called("OpenURL https://brew.sh", "Recheck")
		}},
		{name: "installing", build: func(b *bench) {
			b.show(bare(core.View{Phase: core.PhaseInstalling, Latest: "1.4.1"}), 20*time.Second)
		}, check: func(b *bench) {
			b.want("Installing Splash 1.4.1", "Homebrew is downloading and installing Splash", "brew install incoai/tap/splash")
		}},
		{name: "install-failed", build: func(b *bench) {
			b.show(bare(core.View{Phase: core.PhaseInstallFailed,
				UpdateError: `Error: Failed to download resource "splash" (Could not resolve host: github.com)`}), 0)
		}, check: func(b *bench) {
			b.want("Could not install Splash", "Install Splash to run models", "Retry", "Log")
			b.click("Retry")
			b.click("Log")
			b.called("Install", "OpenLog")
		}},
		{name: "kill-confirm", build: func(b *bench) {
			v := withPhase(core.PhaseExternal)
			v.Port, v.Address = 8080, "127.0.0.1:8080"
			b.show(v, time.Hour)
			b.click("End process")
		}, check: func(b *bench) {
			b.want("End the Splash process of another app?", "The app that started it will lose the server")
			b.click("Cancel")
			b.none("End the Splash process of another app?")
			b.notCalled("KillExternal")
		}},
		{name: "settings-local", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click("Settings")
		}, check: func(b *bench) {
			b.want("Server access", "This computer", "Network",
				"The server listens on 127.0.0.1:8000. Only apps on this computer can reach it", "Port",
				"Unload when idle", "After 10 minutes", "Check for model updates", "Splash version", "1.4.0",
				"Check for updates", en.UpdatesNote(), "Language", "System default", "Launch at login",
				"Animate the icon while responding")
			b.none("API key", "Allowed host names")
			b.click("Check for updates")
			b.called("CheckUpdate")
			b.click("Check for model updates")
			if n := len(b.rec.prefs); n == 0 || !b.rec.prefs[n-1].ModelUpdates {
				b.t.Errorf("the switch stored %+v", b.rec.prefs)
			}
			b.click("Back")
			b.want("Generation speed")
		}},
		{name: "settings-network", build: func(b *bench) {
			pr := defaultPrefs()
			pr.Access, pr.APIKey = engine.AccessNetwork, "sk-splash-0000000000003f9a"
			b.p.SetPrefs(pr)
			v := withPhase(core.PhaseReady)
			v.Access, v.Address = engine.AccessNetwork, "192.168.1.20:8000"
			b.show(v, time.Hour)
			// The chip shows the address other devices use, and copies it.
			b.want("Copy address 192.168.1.20:8000")
			b.click("Copy address 192.168.1.20:8000")
			b.called("Copy 192.168.1.20:8000")
			b.click("Settings")
		}, check: func(b *bench) {
			b.want("This computer", "Network", en.HintNetwork("192.168.1.20:8000"), en.WarnNetwork(),
				"API key", "sk-splash-••••3f9a", en.KeyNote(), "Allowed host names", "computer.local", "Add")
			b.none("Local network", "All addresses")
			b.click("Copy key")
			b.called("Copy sk-splash-0000000000003f9a")
			b.click("Create new key")
			b.called("NewAPIKey")
			b.click("Add")
			b.tt.Type("tunnel.example.com")
			b.tt.Key(0, ui.KeyEnter)
			if n := len(b.rec.prefs); n == 0 || !slices.Contains(b.rec.prefs[n-1].AllowedHosts, "tunnel.example.com") {
				b.t.Errorf("the host was not added: %+v", b.rec.prefs)
			}
			b.tt.Frame()
			b.want("tunnel.example.com")
			b.click("Remove computer.local")
			if n := len(b.rec.prefs); slices.Contains(b.rec.prefs[n-1].AllowedHosts, "computer.local") {
				b.t.Errorf("the host was not removed: %+v", b.rec.prefs[n-1])
			}
		}},
		// No LAN address is known: the hint falls back to every interface.
		{name: "settings-network-no-lan", build: func(b *bench) {
			b.p.SetLANAddress("")
			pr := defaultPrefs()
			pr.Access, pr.APIKey = engine.AccessNetwork, "sk-splash-0000000000003f9a"
			pr.AllowedHosts = []string{"computer.local", "llm.example.com"}
			b.p.SetPrefs(pr)
			v := withPhase(core.PhaseReady)
			v.Access, v.Address = engine.AccessNetwork, "0.0.0.0:8000"
			b.show(v, time.Hour)
			b.click("Settings")
		}, check: func(b *bench) {
			b.want(en.HintNetwork("0.0.0.0:8000"), en.WarnNetwork(), "llm.example.com")
			// Back to this computer only: no key row.
			b.click("This computer")
			if n := len(b.rec.prefs); n == 0 || b.rec.prefs[n-1].Access != engine.AccessLocal {
				b.t.Errorf("access stored %+v", b.rec.prefs)
			}
			b.tt.Frame()
			b.want(en.HintLocal("127.0.0.1:8000"))
			b.none("API key")
		}},
		{name: "settings-access-key", build: func(b *bench) {
			b.show(withPhase(core.PhaseStopped), 0)
			b.click("Settings")
			b.click("Network")
		}, check: func(b *bench) {
			// Opening the server makes a key at once.
			b.called("NewAPIKey")
			if n := len(b.rec.prefs); n == 0 || b.rec.prefs[n-1].APIKey == "" || b.rec.prefs[n-1].Access != engine.AccessNetwork {
				b.t.Errorf("stored %+v", b.rec.prefs)
			}
		}},
		{name: "settings-restart", build: func(b *bench) {
			b.rec.busy[8002] = true
			v := withPhase(core.PhaseReady)
			v.PendingRestart = true
			b.show(v, time.Hour)
			b.click("Settings")
			b.clickEnd("Port")
			b.tt.Key(ui.Cmd, ui.KeyA)
			b.tt.Type("8002")
			b.tt.Key(0, ui.KeyEnter)
		}, check: func(b *bench) {
			// Typing stores nothing on the way: neither 8, 80 nor 800.
			for _, pr := range b.rec.prefs {
				if pr.Port != 8000 && pr.Port != 8003 {
					b.t.Errorf("stored port %d while typing", pr.Port)
				}
			}
			b.want("Changes apply after the server restarts", "Restart", "Port 8002 is in use. 8003 is free", "Use 8003")
			b.click("Use 8003")
			if n := len(b.rec.prefs); n == 0 || b.rec.prefs[n-1].Port != 8003 {
				b.t.Fatalf("port stored %+v", b.rec.prefs)
			}
			b.tt.Frame()
			b.want("Running agents will lose their connection")
			b.click("Restart")
			b.called("Restart")
		}},
		{name: "settings-stopped", build: func(b *bench) {
			b.show(withPhase(core.PhaseStopped), 0)
			b.click("Settings")
			b.click("After 10 minutes")
			b.click("After 30 minutes")
		}, check: func(b *bench) {
			b.want("Applies when the server starts")
			b.none("Restart", "Changes apply after the server restarts")
			if n := len(b.rec.prefs); n == 0 || b.rec.prefs[n-1].IdleRelease != 30*time.Minute {
				b.t.Errorf("idle stored %+v", b.rec.prefs)
			}
		}},
		// Halfway through the slide to Settings, and back.
		{name: "slide-to-settings", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.slide("Settings", 90*time.Millisecond)
		}, check: func(b *bench) { b.want("Generation speed", "Server access") }},
		{name: "slide-to-settings-ar", lang: "ar", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.slide(b.m.Settings(), 90*time.Millisecond)
		}, check: func(b *bench) { b.want(b.m.SpeedTitle(), b.m.Access()) }},
		{name: "slide-to-main", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click("Settings")
			b.slide("Back", 90*time.Millisecond)
		}, check: func(b *bench) { b.want("Generation speed", "Server access") }},
		// The update check: running, and its three results.
		{name: "settings-checking", build: func(b *bench) {
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click("Settings")
			b.click("Check for updates")
		}, check: func(b *bench) {
			b.called("CheckUpdate")
			b.want(en.UpdatesNote())
			b.click("Check for updates")
			if n := strings.Count(strings.Join(b.rec.calls, "\n"), "CheckUpdate"); n != 1 {
				b.t.Errorf("the disabled button checked %d times", n)
			}
		}},
		{name: "settings-check-latest", build: func(b *bench) { b.checked(core.CheckUpToDate) },
			check: func(b *bench) { b.want("You have the latest version, 1.4.0"); b.none(en.UpdatesNote()) }},
		{name: "settings-check-available", build: func(b *bench) { b.checked(core.CheckAvailable) },
			check: func(b *bench) { b.want("Version 1.4.1 is available") }},
		{name: "settings-check-failed", build: func(b *bench) { b.checked(core.CheckFailed) },
			check: func(b *bench) { b.want("Couldn’t check for updates") }},
		{name: "settings-check-latest-ar", lang: "ar", build: func(b *bench) { b.checked(core.CheckUpToDate) },
			check: func(b *bench) { b.want(b.m.CheckLatest("1.4.0")) }},
		// French has the longest check button label: it must fit the version row.
		{name: "settings-check-latest-fr", lang: "fr", build: func(b *bench) { b.checked(core.CheckUpToDate) },
			check: func(b *bench) { b.want(b.m.CheckLatest("1.4.0"), b.m.CheckUpdates()) }},
		{name: "settings-light-network", build: func(b *bench) {
			pr := defaultPrefs()
			pr.Access, pr.APIKey = engine.AccessNetwork, "sk-splash-0000000000003f9a"
			b.p.SetPrefs(pr)
			b.show(withPhase(core.PhaseReady), time.Hour)
			b.click("Settings")
		}, check: func(b *bench) { b.want("API key") }},
	}
}

// langBoards are the language boards: the generating panel, the settings
// with the language list open and the settings in the network access.
func langBoards() []board {
	var out []board
	for _, lang := range []string{"zh-Hans", "ar", "en", "de", "ja", "fr", "es", "ru"} {
		out = append(out, board{name: "main-" + lang, lang: lang,
			build: func(b *bench) { b.show(withPhase(core.PhaseGenerating), 2*time.Hour+14*time.Minute) },
			check: func(b *bench) {
				m := b.m
				b.want(m.PillGenerating(), m.SpeedTitle(), m.Memory(), m.AgentsTitle(), m.ActStop(),
					m.SubGenerating(format.ShortDuration(lang, 2*time.Hour+14*time.Minute)),
					format.TokPerSec(lang, 38), format.GB(lang, 21_400_000_000))
			}})
		out = append(out, board{name: "settings-" + lang, lang: lang,
			build: func(b *bench) {
				b.show(withPhase(core.PhaseReady), time.Hour)
				b.click(b.m.Settings())
				b.click(b.m.System())
			},
			check: func(b *bench) {
				b.want(b.m.SettingsTitle(), b.m.System(), "中文", "العربية", "Русский", "English", "Deutsch",
					"日本語", "Français", "Español")
				b.click("Deutsch")
				if n := len(b.rec.prefs); n == 0 || b.rec.prefs[n-1].Language != "de" {
					b.t.Errorf("language stored %+v", b.rec.prefs)
				}
			}})
		out = append(out, board{name: "settings-network-" + lang, lang: lang,
			build: func(b *bench) {
				pr := defaultPrefs()
				pr.Access, pr.APIKey = engine.AccessNetwork, "sk-splash-0000000000003f9a"
				b.p.SetPrefs(pr)
				v := withPhase(core.PhaseReady)
				v.Access, v.Address = engine.AccessNetwork, "192.168.1.20:8000"
				b.show(v, time.Hour)
				b.click(b.m.Settings())
			},
			check: func(b *bench) {
				m := b.m
				b.want(m.ModesLocal(), m.ModesNetwork(), m.HintNetwork("192.168.1.20:8000"), m.WarnNetwork(), m.ApiKey())
			}})
	}
	return out
}

func allBoards() []board {
	all := append(append(boards(), engineBoards()...), headerBoards()...)
	all = append(append(all, idleBoards()...), featureBoards()...)
	all = append(append(all, reviewBoards()...), jobBoards()...)
	all = append(append(all, chipBoards()...), langBoards()...)
	all = append(append(append(all, removeBoards()...), readmeBoards()...), adoptBoards()...)
	return append(all, choiceBoards()...)
}

// The tests of panels in ui.Testers run one after another: the panel keeps
// no state across panels, but MyGo 0.6.3's Testers share a glyph atlas
// that -race finds written from two (internal/scene/atlas.go:157).
func TestBoards(t *testing.T) {
	for _, bd := range allBoards() {
		t.Run(bd.name, func(t *testing.T) {
			lang := bd.lang
			if lang == "" {
				lang = "en"
			}
			b := newBench(t, lang)
			bd.build(b)
			bd.check(b)
		})
	}
}

// LUMILO_SHOT_DIR=… saves every board in both themes at 2×, sized to the
// content as the window would be.
func TestShot(t *testing.T) {
	dir := os.Getenv("LUMILO_SHOT_DIR")
	if dir == "" {
		t.Skip("LUMILO_SHOT_DIR is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// LUMILO_SHOT_LANG sets the language of boards without their own,
	// to compare them with the Russian mockup.
	defLang := os.Getenv("LUMILO_SHOT_LANG")
	if defLang == "" {
		defLang = "en"
	}
	// LUMILO_SHOT_ONLY keeps the boards whose names start with one of its
	// comma-separated prefixes.
	only := os.Getenv("LUMILO_SHOT_ONLY")
	for _, bd := range allBoards() {
		if only != "" && !slices.ContainsFunc(strings.Split(only, ","), func(pre string) bool {
			return strings.HasPrefix(bd.name, pre)
		}) {
			continue
		}
		for _, dark := range []bool{true, false} {
			lang := bd.lang
			if lang == "" {
				lang = "en"
			}
			b := newBench(t, lang)
			b.tt.SetDark(dark)
			bd.build(b)
			if bd.lang == "" && defLang != "en" {
				// The build clicks English labels; switch after it.
				b.tt.SetLocale(defLang)
				b.tt.Frame()
			}
			b.tt.Frame()
			b.tt.SetSize(Width, b.p.height)
			b.tt.SetScale(2)
			b.tt.Frame()
			theme := map[bool]string{true: "dark", false: "light"}[dark]
			save(t, b.tt, filepath.Join(dir, bd.name+"-"+theme+".png"))
		}
	}
}

func save(t *testing.T, tt *ui.Tester, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

// Every phase, update and failure draws, also with Reduce Motion and in
// Arabic, and the view keeps no frames coming when nothing moves.
func TestMotionAndRTL(t *testing.T) {
	phases := []core.Phase{core.PhaseStopped, core.PhaseLoading, core.PhaseReady, core.PhaseGenerating,
		core.PhaseReleased, core.PhaseRestoring, core.PhaseError, core.PhaseExternal, core.PhaseUnresponsive, core.PhaseStopping,
		core.PhaseNotInstalled, core.PhaseNoInstaller, core.PhaseInstalling, core.PhaseInstallFailed}
	for _, lang := range []string{"en", "ar"} {
		for _, reduce := range []bool{false, true} {
			for _, ph := range phases {
				for _, upd := range []core.UpdatePhase{core.UpdateNone, core.UpdateAvailable, core.UpdateUpdating, core.UpdateFailed} {
					b := newBench(t, lang)
					b.tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
					v := withPhase(ph)
					v.Update, v.Latest = upd, "1.4.1"
					b.show(v, time.Minute)
					b.tt.Advance(700 * time.Millisecond)
					if b.p.height < 100 {
						t.Errorf("%s %v %v: height %d", lang, ph, upd, b.p.height)
					}
				}
			}
			b := newBench(t, lang)
			b.tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
			b.show(withPhase(core.PhaseReady), time.Minute)
			b.click(b.m.Settings())
			b.tt.Frame()
		}
	}
}

// Reduce Motion stops the spinner, the pulse and the live bar: frames a
// moment apart are the same, while without it they differ.
func TestReduceMotionStill(t *testing.T) {
	for _, ph := range []core.Phase{core.PhaseLoading, core.PhaseGenerating} {
		for _, reduce := range []bool{true, false} {
			b := newBench(t, "en")
			b.tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
			b.show(withPhase(ph), 30*time.Second)
			b.tt.Advance(10 * time.Millisecond)
			before := slices.Clone(b.tt.Image().Pix)
			// Past a half of the pulse, whose pose heads the other way at
			// each half.
			b.tt.Advance(pulseHalf + 100*time.Millisecond)
			same := slices.Equal(before, b.tt.Image().Pix)
			if same != reduce {
				t.Errorf("%v, reduce motion %v: frames the same %v", ph, reduce, same)
			}
		}
	}
}

// In Arabic the layout mirrors, numbers keep Latin digits and the chart
// still reads left to right.
func TestArabic(t *testing.T) {
	b := newBench(t, "ar")
	b.show(withPhase(core.PhaseGenerating), time.Hour)
	quit, ok := b.tt.Find(b.m.Quit())
	if !ok || quit.X > 100 {
		t.Errorf("the quit button is not at the left: %+v %v", quit, ok)
	}
	for _, s := range b.tt.Texts() {
		if strings.ContainsAny(s, "٠١٢٣٤٥٦٧٨٩") {
			t.Errorf("Arabic-Indic digits in %q", s)
		}
	}
	first, _ := b.tt.Find(b.m.HoverReq() + " " + format.TokPerSec("ar", 36))
	cur, _ := b.tt.Find(b.m.HoverCur() + " " + format.TokPerSec("ar", 41))
	if cur.X <= first.X {
		t.Errorf("the chart is mirrored: first %+v, current %+v", first, cur)
	}
}

// Measure lays the panel out without a window: more cards, more height.
func TestMeasure(t *testing.T) {
	b := newBench(t, "en")
	b.p.SetView(withPhase(core.PhaseStopped))
	stopped := b.p.Measure(true)
	b.p.SetView(withPhase(core.PhaseReady))
	ready := b.p.Measure(false)
	if stopped < 150 || ready <= stopped {
		t.Errorf("heights: stopped %d, ready %d", stopped, ready)
	}
	// A new view resizes the window in the frame after it.
	b.show(withPhase(core.PhaseStopped), 0)
	short := b.p.height
	b.show(withPhase(core.PhaseReady), 0)
	if b.p.height <= short {
		t.Errorf("height after ready %d, stopped %d", b.p.height, short)
	}
	var got []int
	b.p.OnResize = func(h int) { got = append(got, h) }
	b.p.SetView(withPhase(core.PhaseStopped))
	b.tt.Frame()
	if len(got) == 0 || got[len(got)-1] != stopped {
		t.Errorf("OnResize %v, measured %d", got, stopped)
	}
}

// Reset closes what is open and returns to the main screen.
func TestReset(t *testing.T) {
	b := newBench(t, "en")
	b.show(withPhase(core.PhaseGenerating), time.Minute)
	b.click("Quit")
	b.want("Quit Lumilo?")
	b.p.Reset()
	b.tt.Frame()
	b.none("Quit Lumilo?")
	b.click("Settings")
	b.p.Reset()
	b.tt.Frame()
	b.want("Generation speed")
}

// A free port typed in is stored when the field loses the focus.
func TestPortOnBlur(t *testing.T) {
	b := newBench(t, "en")
	b.show(withPhase(core.PhaseStopped), 0)
	b.click("Settings")
	b.clickEnd("Port")
	b.tt.Key(ui.Cmd, ui.KeyA)
	b.tt.Type("8005")
	if len(b.rec.prefs) != 0 {
		t.Fatalf("stored while typing: %+v", b.rec.prefs)
	}
	b.click("Language")
	if n := len(b.rec.prefs); n != 1 || b.rec.prefs[0].Port != 8005 {
		t.Errorf("stored %+v", b.rec.prefs)
	}
}

// With many models the open list still fits in a window sized to the
// panel, as the app sizes it.
func TestModelListFits(t *testing.T) {
	b := newBench(t, "en")
	b.p.OnResize = func(h int) { b.tt.SetSize(Width, h) }
	v := withPhase(core.PhaseStopped)
	for i := range 5 {
		v.Models = append(v.Models, engine.Model{ID: fmt.Sprintf("m%d", i), Name: fmt.Sprintf("Model %d", i), Variant: "Q4"})
	}
	b.show(v, 0)
	b.tt.SetSize(Width, b.p.height)
	b.tt.Frame()
	b.click("OrcaCyber-Nano-1.0")
	for range 3 {
		b.tt.Frame()
	}
	btn, _ := b.tt.Find("OrcaCyber-Nano-1.0")
	last, ok := b.tt.Find("Model 4")
	if !ok || last.Y < btn.Y+btn.H || last.Y+last.H > float32(b.p.height) {
		t.Errorf("last row %+v (shown %v), button %+v, window height %d", last, ok, btn, b.p.height)
	}
}

// Reset closes the select lists too.
func TestResetClosesSelects(t *testing.T) {
	b := newBench(t, "en")
	b.show(withPhase(core.PhaseReady), time.Minute)
	b.click("Settings")
	b.click("System default")
	b.want("Deutsch")
	b.p.Reset()
	b.tt.Frame()
	b.click("Settings")
	b.none("Deutsch")
}

func TestMaskKey(t *testing.T) {
	for in, want := range map[string]string{
		"sk-splash-0000000000003f9a": "sk-splash-••••3f9a",
		"abcdef":                     "••••cdef",
		"":                           "",
	} {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThemeButton(t *testing.T) {
	for _, dark := range []bool{false, true} {
		b := newBench(t, "en")
		b.tt.SetDark(dark)
		b.show(withPhase(core.PhaseStopped), time.Minute)
		label := "Dark theme"
		if dark {
			label = "Light theme"
		}
		b.click(label)
		b.called("ToggleTheme")
	}
}

// The screens slide: halfway, both show, the main screen going out at
// the start of the line and Settings coming from its end, and the window
// is between the two heights. Then only the screen shown is left.
func TestSlide(t *testing.T) {
	for _, lang := range []string{"en", "ar"} {
		b := newBench(t, lang)
		m := b.m
		pr := defaultPrefs()
		pr.Access, pr.APIKey = engine.AccessNetwork, "sk-splash-0000000000003f9a"
		b.p.SetPrefs(pr) // Settings is the taller screen
		b.show(withPhase(core.PhaseStopped), time.Hour)
		mainH := b.p.height
		// Find gives the boxes as the window shows them, clipped to it.
		speed, _ := b.tt.Find(m.SubStopped())
		b.slide(m.Settings(), 40*time.Millisecond)
		half, _ := b.tt.Find(m.SubStopped())
		access, ok := b.tt.Find(m.Access())
		if !ok || access.W == 0 || half.W == 0 || !b.tt.FramePending() {
			t.Fatalf("%s: not both halfway: main %+v, settings %+v, pending %v", lang, half, access, b.tt.FramePending())
		}
		out := half.X < speed.X || half.W < speed.W && half.X == 1
		in := access.X > 23
		if lang == "ar" {
			out = half.X > speed.X
			in = access.X+access.W < Width-23
		}
		if !out || !in {
			t.Errorf("%s: halfway the main screen is at %+v (was %+v), Settings at %+v", lang, half, speed, access)
		}
		b.tt.Frame()
		halfH := b.p.height
		b.settle()
		settingsH := b.p.height
		b.none(m.SubStopped())
		if halfH <= mainH || halfH >= settingsH || settingsH <= mainH || b.tt.FramePending() {
			t.Errorf("%s: heights: main %d, halfway %d, settings %d; pending %v", lang, mainH, halfH, settingsH, b.tt.FramePending())
		}
		if a, _ := b.tt.Find(m.Access()); a.X != 23 {
			t.Errorf("%s: Settings ends at %+v", lang, a)
		}
		// And back: the window shrinks while the screens slide.
		b.slide(m.Back(), 100*time.Millisecond)
		b.want(m.SubStopped(), m.Access())
		b.tt.Frame()
		if b.p.height <= mainH || b.p.height >= settingsH {
			t.Errorf("%s: sliding back: height %d, want between %d and %d", lang, b.p.height, mainH, settingsH)
		}
		b.settle()
		b.none(m.Access())
		if b.p.height != mainH {
			t.Errorf("%s: back on the main screen: height %d, want %d", lang, b.p.height, mainH)
		}
	}
}

// With Reduce Motion the screens switch at once.
func TestSlideReduceMotion(t *testing.T) {
	b := newBench(t, "en")
	b.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	b.show(withPhase(core.PhaseReady), time.Hour)
	if err := b.tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	b.tt.Frame()
	b.none("Generation speed")
	b.want("Server access")
	if b.tt.FramePending() {
		t.Error("frames pending after the switch")
	}
	b.click("Check for updates")
	b.click("Back")
	b.none("Server access")
}

// Reset, as the panel hides, shows the main screen at once.
func TestResetNoSlide(t *testing.T) {
	b := newBench(t, "en")
	b.show(withPhase(core.PhaseReady), time.Hour)
	b.click("Settings")
	b.p.Reset()
	b.p.Measure(false) // as the app does before it shows the panel
	b.tt.Frame()
	b.none("Server access")
	b.want("Generation speed")
	if b.tt.FramePending() {
		t.Error("the screens slide after Reset")
	}
}

// The check's result stays until Settings closes, and goes when another
// check starts; the loader stands still with Reduce Motion.
func TestCheckLine(t *testing.T) {
	b := newBench(t, "en")
	b.checked(core.CheckFailed)
	b.want("Couldn’t check for updates")
	b.click("Check for updates")
	b.none("Couldn’t check for updates")
	b.want("Updates are installed with Homebrew")
	v := b.p.view
	v.CheckSeq, v.Check = 2, core.CheckUpToDate
	b.show(v, 0)
	b.want("You have the latest version, 1.4.0")
	b.click("Back")
	b.click("Settings")
	b.none("You have the latest version, 1.4.0")
	b.want("Updates are installed with Homebrew")

	for _, reduce := range []bool{true, false} {
		b := newBench(t, "en")
		b.tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
		b.show(withPhase(core.PhaseStopped), time.Minute)
		b.click("Settings")
		b.click("Check for updates")
		b.tt.Advance(10 * time.Millisecond)
		before := slices.Clone(b.tt.Image().Pix)
		b.tt.Advance(300 * time.Millisecond)
		if same := slices.Equal(before, b.tt.Image().Pix); same != reduce {
			t.Errorf("reduce motion %v: frames the same %v", reduce, same)
		}
	}
}
