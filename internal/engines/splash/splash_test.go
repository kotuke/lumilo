package splash

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func envValue(env []string, name string) (string, bool) {
	val, found := "", false
	for _, kv := range env { // the last duplicate wins, as in exec
		if k, v, _ := strings.Cut(kv, "="); k == name {
			val, found = v, true
		}
	}
	return val, found
}

func TestInfo(t *testing.T) {
	e := New()
	info := e.Info()
	if info.ID != "splash" || info.Name != "Splash" {
		t.Errorf("identity %q %q", info.ID, info.Name)
	}
	if logo := info.Logo; logo.Kind != engine.LogoPNG || !logo.Tile || !bytes.HasPrefix(logo.Dark, []byte("\x89PNG")) ||
		!bytes.Equal(logo.Dark, logo.Light) {
		t.Errorf("logo kind %v tile %v", logo.Kind, logo.Tile)
	}
	if c := info.Caps; !c.Release || !c.AllowedHosts || !c.LiveSpeed || !c.DiskCache || c.MaxContext != 1_010_000 ||
		c.Memory.DefaultDraft != 1_300_000_000 || !c.Memory.ShrinksContext {
		t.Errorf("caps %+v", c)
	}
	if len(info.Suggestions) != 2 || info.Suggestions[0].Name != "Qwen3.8-27B" {
		t.Errorf("suggestions %+v", info.Suggestions)
	}
	if len(info.ModelSearch.Sources) != 2 || info.ModelSearch.MLX == nil {
		t.Errorf("model search %+v", info.ModelSearch)
	}
	var ids []string
	for _, a := range info.Agents {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != "claude,codex,opencode,pi" {
		t.Errorf("agents %v", ids)
	}
	p := e.StopPolicy()
	if p.Graceful != syscall.SIGINT || p.Force != syscall.SIGINT || p.KillWait != 5*time.Second {
		t.Errorf("stop policy %+v", p)
	}
}

func TestInstaller(t *testing.T) {
	e := New()
	in := e.Installer()
	d := in.Describe()
	if d.Method != engine.InstallHomebrew || d.Size != 270_000_000 || d.InstallCommand != "brew install incoai/tap/splash" ||
		d.UpgradeCommand != "brew upgrade incoai/tap/splash" || d.Prerequisite.Name != "Homebrew" {
		t.Errorf("describe %+v", d)
	}
	if got := in.ReleaseNotesURL("1.4.1"); got != "https://github.com/incoai/splash/releases/tag/1.4.1" {
		t.Errorf("release url %q", got)
	}
	if got := in.ReleaseNotesURL("v1.4.1"); !strings.HasSuffix(got, "/tag/1.4.1") {
		t.Errorf("release url with v: %q", got)
	}
	// The newest version is the latest GitHub release.
	port := serve(t, map[string]http.HandlerFunc{"/latest": func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"1.4.1"}`))
	}})
	e.releaseURL = "http://127.0.0.1:" + strconv.Itoa(port) + "/latest"
	if v, err := in.Latest(context.Background()); err != nil || v != "1.4.1" {
		t.Errorf("latest %q, %v", v, err)
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	script := "#!/bin/sh\necho 'Splash 9.9.9'\n"
	write(t, filepath.Join(root, "Cellar/splash/1.2.3/bin/splash"), script, 0o755)
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin/splash")
	if err := os.Symlink("../Cellar/splash/1.2.3/bin/splash", link); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "bin/brew"), "#!/bin/sh\n", 0o755)

	e := New()
	e.inst.Bins = []string{filepath.Join(root, "missing/splash"), link}
	e.inst.Brews = []string{filepath.Join(root, "bin/brew")}
	inst := e.Installer()

	// release.json wins, so the script's 9.9.9 proves no process ran.
	write(t, filepath.Join(root, "Cellar/splash/1.2.3/libexec/release.json"), `{"version":"1.2.3","binary_sha256":"x"}`, 0o644)
	in := inst.Detect(context.Background())
	if !in.Installed || in.Version != "1.2.3" || in.Path != link || !in.Ready {
		t.Errorf("with release.json: %+v", in)
	}

	// Without it, "splash --version" is run with an explicit environment.
	if err := os.Remove(filepath.Join(root, "Cellar/splash/1.2.3/libexec/release.json")); err != nil {
		t.Fatal(err)
	}
	if in = inst.Detect(context.Background()); in.Version != "9.9.9" {
		t.Errorf("fallback: %+v", in)
	}

	// A source checkout prints no version.
	write(t, filepath.Join(root, "Cellar/splash/1.2.3/bin/splash"), "#!/bin/sh\necho 'Splash (source checkout)'\n", 0o755)
	if in = inst.Detect(context.Background()); !in.Installed || in.Version != "" {
		t.Errorf("source checkout: %+v", in)
	}

	e.inst.Bins, e.inst.Brews = []string{filepath.Join(root, "nope")}, []string{filepath.Join(root, "nobrew")}
	if in = inst.Detect(context.Background()); in.Installed || in.Ready || in.Path != "" || in.Version != "" {
		t.Errorf("nothing installed: %+v", in)
	}
}

func TestModels(t *testing.T) {
	root := t.TempDir()
	assembly := func(hash, model string, files map[string][2]any) string {
		dir := filepath.Join(root, ".resolved", hash)
		recs := map[string]any{}
		for name, f := range files {
			recs[name] = map[string]any{"bytes": f[0], "path": f[1]}
		}
		data, _ := json.Marshal(map[string]any{"model": model, "files": recs})
		write(t, filepath.Join(dir, "model.json"), string(data), 0o644)
		return dir
	}
	link := func(name, target string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Weights that are there: an assembly whose weights are gone is not
	// servable.
	hf := filepath.Join(root, "hf")
	write(t, filepath.Join(hf, "m.gguf"), "gguf", 0o644)
	write(t, filepath.Join(hf, "x"), "mlx", 0o644)
	// The real layout: config.json and tokenizer/config.json share a source
	// file; draft/ is the shared DFlash2 draft.
	good := assembly("a1", "orcarouter/Nano-1.0:Mix-15.7G", map[string][2]any{
		"config.json":             {651, "/m/config.json"},
		"tokenizer/config.json":   {651, "/m/config.json"},
		"target/m.gguf":           {15000, filepath.Join(hf, "m.gguf")},
		"vision/mmproj.gguf":      {900, "/hf/mmproj.gguf"},
		"draft/model.safetensors": {3848817896, "/hf/draft.safetensors"},
	})
	link("orcarouter/Nano-1.0:Mix-15.7G", good)
	link("acme/Plain-7B", assembly("a2", "acme/Plain-7B", map[string][2]any{"target/model.safetensors": {42, filepath.Join(hf, "x")}}))
	link("acme/Gone-7B", assembly("a4", "acme/Gone-7B", map[string][2]any{"target/model.safetensors": {42, filepath.Join(hf, "deleted")}}))
	link("incoai/Qwen3.8-27B-DFlash2", assembly("a3", "incoai/Qwen3.8-27B-DFlash2", map[string][2]any{"x": {1, "/y"}}))

	pkg := filepath.Join(root, ".resolved", "pkg")
	write(t, filepath.Join(pkg, "manifest.json"), `{"format":{"name":"splash-packed-q4"}}`, 0o644)
	link("old/Package-9B", pkg)
	link("broken/Dangling-1B", filepath.Join(root, "does-not-exist"))
	link("acme/.prepare-leftover", good)
	link("acme/Bad Name", good)
	write(t, filepath.Join(root, "acme/plain-dir-not-a-link/model.json"), `{"files":{}}`, 0o644)
	write(t, filepath.Join(root, ".install.lock"), "", 0o644)
	link(".selections/abc", good)

	models, err := listModels(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []engine.Model{
		{ID: "acme/Plain-7B", Name: "Plain-7B", Owner: "acme", SizeBytes: 42},
		// The size is the weights alone (target/); the vision projector and
		// the draft, in memory as Splash keeps it, are features.
		{ID: "orcarouter/Nano-1.0:Mix-15.7G", Name: "Nano-1.0", Owner: "orcarouter", Variant: "Mix-15.7G", SizeBytes: 15000,
			Features: engine.Features{Vision: true, VisionBytes: 900, Drafts: []engine.Draft{{
				Kind: engine.DraftDFlash, Name: "DFlash2", ID: "DFlash2", SizeBytes: draftMemory(3848817896), Always: true}}}},
	}
	if len(models) != len(want) {
		t.Fatalf("models %+v", models)
	}
	for i := range want {
		if !reflect.DeepEqual(models[i], want[i]) {
			t.Errorf("model %d: %+v, want %+v", i, models[i], want[i])
		}
	}

	if got, err := listModels(context.Background(), filepath.Join(root, "absent")); err != nil || len(got) != 0 {
		t.Errorf("missing dir: %v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := listModels(ctx, root); err == nil {
		t.Error("cancelled context ignored")
	}
}

func TestServeCommand(t *testing.T) {
	t.Setenv("SPLASH_API_KEY", "stale-key")
	t.Setenv("HF_HUB_OFFLINE", "1")
	t.Setenv("SPLASH_PORT", "9999")
	t.Setenv("PATH", "/custom/bin")
	e := New()
	cfg := engine.Config{
		Model: fixtureModel, Port: 8123, Host: "0.0.0.0", Access: engine.AccessNetwork,
		APIKey: "s3cret-key", AllowedHosts: []string{"mymac.local", "", "10.0.0.5"},
		IdleRelease: 30 * time.Minute, ModelUpdates: false,
	}
	argv, env := e.ServeCommand(cfg)
	want := []string{e.binPath(), "serve", "--model", fixtureModel, "--port", "8123", "--host", "0.0.0.0",
		"--offline", "--allowed-host", "mymac.local", "--allowed-host", "10.0.0.5", "--idle-release", "1800"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv\n got %q\nwant %q", argv, want)
	}
	if !filepath.IsAbs(argv[0]) {
		t.Errorf("argv[0] %q is not absolute", argv[0])
	}
	for _, a := range argv {
		if strings.Contains(a, "s3cret-key") || a == "--api-key" {
			t.Errorf("key in argv: %q", argv)
		}
	}
	if v, _ := envValue(env, "SPLASH_API_KEY"); v != "s3cret-key" {
		t.Errorf("env key %q", v)
	}
	if v, ok := envValue(env, "HF_HUB_OFFLINE"); ok {
		t.Errorf("HF_HUB_OFFLINE leaked: %q", v)
	}
	if v, ok := envValue(env, "SPLASH_PORT"); ok {
		t.Errorf("SPLASH_PORT leaked: %q", v)
	}
	path, _ := envValue(env, "PATH")
	if !strings.HasPrefix(path, "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin") || !strings.Contains(path, "/custom/bin") {
		t.Errorf("PATH %q", path)
	}
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("PATH entries: %d", n)
	}

	// No key: no key in env, even though the parent had one. Updates on:
	// no --offline. IdleRelease 0 is "off"; no host falls back to loopback.
	argv, env = e.ServeCommand(engine.Config{Model: "a/b", Port: 8000, ModelUpdates: true})
	if _, ok := envValue(env, "SPLASH_API_KEY"); ok {
		t.Error("stale parent key reached the server")
	}
	line := strings.Join(argv, " ")
	if strings.Contains(line, "--offline") || !strings.Contains(line, "--idle-release off") || !strings.Contains(line, "--host 127.0.0.1") {
		t.Errorf("argv %q", line)
	}
}

func TestIdleReleaseArg(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "off", -time.Second: "off", 10 * time.Minute: "600",
		90 * time.Second: "90", 500 * time.Millisecond: "0.5", 2 * time.Hour: "7200",
	} {
		if got := idleReleaseArg(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestParseLineFixtures(t *testing.T) {
	e := New()
	read := func(path string) []string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}

	type want struct {
		kind  engine.EventKind
		model string
		line  string
	}
	cases := map[int]want{ // by line index of lifecycle/serve.stdout.txt
		1: {engine.EventLoading, fixtureModel, "Loading · " + fixtureModel},
		3: {engine.EventReady, fixtureModel, "Ready · " + fixtureModel + " · context 256K · http://127.0.0.1:8000"},
		4: {engine.EventRequestStart, "", "Started · input 82 · cached 0 · effort absent · thinking on"},
		5: {engine.EventRequestDone, "", "Done · input 82 · cached 0 · output 1,200 · TTFT 0.2s · 55.7 tok/s · 2.6 tok/cycle"},
	}
	var starts, dones, stops int
	for i, l := range read(filepath.Join("testdata", "lifecycle", "serve.stdout.txt")) {
		ev, ok := e.ParseLine(l, false)
		if w, listed := cases[i]; listed {
			if !ok || ev.Kind != w.kind || ev.Model != w.model || ev.Line != w.line {
				t.Errorf("line %d %q -> %+v, %v", i, l, ev, ok)
			}
		}
		if ok {
			switch ev.Kind {
			case engine.EventRequestStart:
				starts++
			case engine.EventRequestDone:
				dones++
			case engine.EventStopping:
				stops++
				if ev.Line != "Stopping · releasing engine resources" {
					t.Errorf("stopping line %q", ev.Line)
				}
			}
		} else if i != 0 && i != 2 {
			t.Errorf("line %d %q ignored", i, l)
		}
	}
	if starts != 5 || dones != 5 || stops != 1 {
		t.Errorf("starts %d dones %d stops %d", starts, dones, stops)
	}
	// The launcher's "Splash model ... is already installed" and the "Chat
	// template" line are noise.
	for _, l := range []string{
		"Splash model a/b is already installed in /x",
		"21:58:45 Chat template · patched to render later system messages in place",
		"", "   ", "21:58:45",
		"22:00:00 Engine restarted",
		"22:00:00 Warning · --allowed-origin '*' without --api-key lets every web page use it",
	} {
		if ev, ok := e.ParseLine(l, false); ok {
			t.Errorf("%q parsed as %+v", l, ev)
		}
	}

	// Engine chatter on stderr is not an error, whatever the stream.
	for i, l := range read(filepath.Join("testdata", "lifecycle", "serve.stderr.txt")) {
		if ev, ok := e.ParseLine(l, true); ok {
			t.Errorf("serve.stderr line %d %q parsed as %+v", i, l, ev)
		}
	}

	errs := map[string]string{
		"error: cannot bind 127.0.0.1:8002: [Errno 48] Address already in use":                "error: cannot bind 127.0.0.1:8002: [Errno 48] Address already in use",
		"error: Splash is already serving (PID 1, model a/b, port 8000); stop it with Ctrl+C": "error: Splash is already serving (PID 1, model a/b, port 8000); stop it with Ctrl+C",
		"splash serve: error: argument --port: port must be between 1 and 65535":              "splash serve: error: argument --port: port must be between 1 and 65535",
		"splash: error: --cache-dir needs --persistent-cache":                                 "splash: error: --cache-dir needs --persistent-cache",
		"12:01:02 Error · native ReadyEvent timed out":                                        "Error · native ReadyEvent timed out",
		"12:01:02 Error · unable to start HTTP server: [Errno 48] Address already in use":     "Error · unable to start HTTP server: [Errno 48] Address already in use",
		"12:01:02 Engine stopped · the inference engine failed 3 times in a row":              "Engine stopped · the inference engine failed 3 times in a row",
		"12:01:02 error: not enough free memory to start":                                     "error: not enough free memory to start",
	}
	for l, wantLine := range errs {
		ev, ok := e.ParseLine(l, true)
		if !ok || ev.Kind != engine.EventError || ev.Line != wantLine {
			t.Errorf("%q -> %+v, %v", l, ev, ok)
		}
	}
	for _, name := range []string{"port_busy", "no_model_offline", "bad_port"} {
		_, lines := readTail(t, filepath.Join("testdata", "failures", name+".stderr.txt"))
		found := false
		for _, l := range lines {
			if ev, ok := e.ParseLine(l, true); ok && ev.Kind == engine.EventError {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no error event in %q", name, lines)
		}
	}
}

func TestClassifyFailureFixtures(t *testing.T) {
	e := New()
	dir := filepath.Join("testdata", "failures")

	check := func(name string, code int, tail []string, kind engine.FailureKind, line string) {
		t.Helper()
		f := e.ClassifyFailure(code, tail)
		if f.Kind != kind || f.Code != code || f.Line != line {
			t.Errorf("%s: %+v, want kind %v code %d line %q", name, f, kind, code, line)
		}
	}

	code, tail := readTail(t, filepath.Join(dir, "port_busy.stderr.txt"))
	check("port_busy", code, tail, engine.FailPortBusy, "error: cannot bind 127.0.0.1:8002: [Errno 48] Address already in use")

	code, tail = readTail(t, filepath.Join(dir, "no_model_offline.stderr.txt"))
	check("no_model_offline", code, tail, engine.FailModel,
		"error: cannot resolve orcarouter/NoSuchModel-1.0: HF_HUB_OFFLINE is set; neither this installation nor the Hub cache records a commit for the default branch")
	if f := e.ClassifyFailure(code, tail); f.Repo != "orcarouter/NoSuchModel-1.0" {
		t.Errorf("no_model_offline: Repo = %q, want the target's repository", f.Repo)
	}

	// The target installs from the cache, its drafter is gone: the panel
	// names the drafter, not the "installing … from the Hub cache" notice.
	code, tail = readTail(t, filepath.Join(dir, "draft_offline.stderr.txt"))
	check("draft_offline", code, tail, engine.FailModel,
		"error: cannot resolve incoai/Qwen3.8-27B-DFlash2: HF_HUB_OFFLINE is set; the Hub cache has no snapshot of 015e795645c74b1a0eeef3b570031fb62e769bc5")
	if f := e.ClassifyFailure(code, tail); f.Repo != "incoai/Qwen3.8-27B-DFlash2" {
		t.Errorf("draft_offline: Repo = %q, want the drafter's repository", f.Repo)
	}

	code, tail = readTail(t, filepath.Join(dir, "bad_port.stderr.txt"))
	if code != 2 {
		t.Fatalf("bad_port exit %d", code)
	}
	check("bad_port", code, tail, engine.FailExit, "splash serve: error: argument --port: port must be between 1 and 65535")

	// second_serve.txt: "# exit N", "## stdout", "## stderr" sections.
	data, err := os.ReadFile(filepath.Join("testdata", "lifecycle", "second_serve.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, _ := strings.Cut(string(data), "## stderr\n")
	check("second_serve", 1, strings.Split(strings.TrimSpace(stderr), "\n"), engine.FailAlreadyServing,
		"error: Splash is already serving (PID 14340, model "+fixtureModel+", port 8000); stop it with Ctrl+C first")

	// A clean stop is not a failure, whatever the log says.
	for _, name := range []string{filepath.Join(dir, "apikey.stderr.txt"), filepath.Join("testdata", "lifecycle", "serve.stderr.txt")} {
		code, tail = readTail(t, name)
		if f := e.ClassifyFailure(code, tail); f.Kind != engine.FailNone || f.Line != "" {
			t.Errorf("%s: clean stop classified %+v", name, f)
		}
		// The same noisy log with a crash code is a plain exit with its last line.
		if f := e.ClassifyFailure(1, tail); f.Kind != engine.FailExit || f.Code != 1 || f.Line != tail[len(tail)-1] {
			t.Errorf("%s: crash classified %+v", name, f)
		}
	}
}

func TestClassifyFailureTexts(t *testing.T) {
	e := New()
	const (
		wait    = "21:58:00 Waiting for sufficient available memory to start; the macOS reserve remains protected..."
		timeout = "22:08:00 Error · native ReadyEvent timed out"
		eof     = "22:08:00 Error · native protocol reached EOF"
	)
	cases := []struct {
		name string
		tail []string
		kind engine.FailureKind
		line string
	}{
		{"server bind race", []string{"12:00:00 Error · unable to start HTTP server: [Errno 48] Address already in use"}, engine.FailPortBusy,
			"12:00:00 Error · unable to start HTTP server: [Errno 48] Address already in use"},
		{"permission", []string{"error: cannot bind 127.0.0.1:80: [Errno 13] Permission denied"}, engine.FailExit,
			"error: cannot bind 127.0.0.1:80: [Errno 13] Permission denied"},
		{"already serving without owner", []string{"error: Splash is already serving; stop it with Ctrl+C first"}, engine.FailAlreadyServing,
			"error: Splash is already serving; stop it with Ctrl+C first"},
		{"download failed", []string{"error: model download or verification failed"}, engine.FailModel, "error: model download or verification failed"},
		{"variant", []string{"error: no single GGUF matches :Q9 (files in the repository root: a.gguf, b.gguf)"}, engine.FailModel,
			"error: no single GGUF matches :Q9 (files in the repository root: a.gguf, b.gguf)"},
		{"not enough memory", []string{wait, "error: not enough free memory to start (812 MiB reclaimable, 4096 MiB protected for macOS, system pressure critical): close other applications"},
			engine.FailMemory, "error: not enough free memory to start (812 MiB reclaimable, 4096 MiB protected for macOS, system pressure critical): close other applications"},
		{"plan", []string{"12:00:00 error: the memory plan cannot hold the model beside its state staging", eof}, engine.FailMemory,
			"12:00:00 error: the memory plan cannot hold the model beside its state staging"},
		{"memory-bound startup timeout", []string{wait, "12:00:01 Memory: growth paused", timeout}, engine.FailMemory, timeout},
		{"plain startup timeout", []string{"12:00:01 Weights loaded in 5.21 s.", timeout}, engine.FailExit, timeout},
		{"engine died", []string{"12:00:01 Weights loaded in 5.21 s.", eof}, engine.FailExit, eof},
		{"empty", nil, engine.FailExit, ""},
		{"last line", []string{"Traceback (most recent call last):", "  File x", "KeyError: 'a'"}, engine.FailExit, "KeyError: 'a'"},
		{"first error wins", []string{"error: first thing", "error: second thing"}, engine.FailExit, "error: first thing"},
	}
	for _, c := range cases {
		f := e.ClassifyFailure(1, c.tail)
		if f.Kind != c.kind || f.Line != c.line || f.Code != 1 {
			t.Errorf("%s: %+v, want kind %v line %q", c.name, f, c.kind, c.line)
		}
	}
	if f := e.ClassifyFailure(137, []string{"killed"}); f.Kind != engine.FailExit || f.Code != 137 {
		t.Errorf("signal exit: %+v", f)
	}
}

func TestFindRunning(t *testing.T) {
	dir := t.TempDir()
	e := New()
	e.runtimeDir = dir
	live := filepath.Join(dir, "serve-8000.lock")
	write(t, live, `{"pid": 14340, "model": "`+fixtureModel+`", "port": 8000}`, 0o644)
	write(t, filepath.Join(dir, "serve-8001.lock"), `{"pid": 99, "model": "x/y", "port": 8001}`, 0o644) // stale
	write(t, filepath.Join(dir, "serve-8002.lock"), ``, 0o644)                                          // stale, empty
	write(t, filepath.Join(dir, "serve.lock"), ``, 0o644)                                               // not per port
	write(t, filepath.Join(dir, "serve-abc.lock"), `{"pid": 1}`, 0o644)
	held := func(name string) {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
	}
	held("serve-8000.lock")
	held("serve.lock")
	held("serve-abc.lock")
	// Held but the owner has not written its record yet: skipped.
	write(t, filepath.Join(dir, "serve-8003.lock"), ``, 0o644)
	held("serve-8003.lock")

	got, err := e.FindRunning()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (engine.Running{PID: 14340, Port: 8000, Model: fixtureModel}) {
		t.Errorf("running %+v", got)
	}
	// Probing must not leave a lock behind: the stale files can be taken.
	f, _ := os.Open(filepath.Join(dir, "serve-8001.lock"))
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Errorf("stale lock stayed held: %v", err)
	}
	f.Close()

	// A record whose JSON lacks the port falls back to the file name.
	write(t, filepath.Join(dir, "serve-8004.lock"), `{"pid": 7, "model": "m/n"}`, 0o644)
	held("serve-8004.lock")
	got, _ = e.FindRunning()
	if len(got) != 2 || got[1] != (engine.Running{PID: 7, Port: 8004, Model: "m/n"}) {
		t.Errorf("running %+v", got)
	}

	e.runtimeDir = filepath.Join(dir, "absent")
	if got, err := e.FindRunning(); err != nil || len(got) != 0 {
		t.Errorf("missing dir: %v, %v", got, err)
	}
}

func TestAgents(t *testing.T) {
	e := New()
	agents := e.Info().Agents
	var ids []string
	for _, a := range agents {
		ids = append(ids, a.ID)
		if !strings.HasPrefix(string(a.Logo), "<svg") || a.Name == "" {
			t.Errorf("agent %+v", a.ID)
		}
		wantColor := ""
		if a.ID == "claude" {
			wantColor = "#D97757"
		}
		if a.Color != wantColor {
			t.Errorf("%s color %q", a.ID, a.Color)
		}
	}
	if strings.Join(ids, ",") != "claude,codex,opencode,pi" {
		t.Errorf("agents %v", ids)
	}

	t.Setenv("SPLASH_API_KEY", "stale")
	t.Setenv("SPLASH_PORT", "1111")
	argv, env := e.AgentCommand("codex", engine.Target{Port: 8123})
	if len(argv) != 2 || argv[0] != e.binPath() || argv[1] != "codex" {
		t.Errorf("argv %q", argv)
	}
	if v, _ := envValue(env, "SPLASH_PORT"); v != "8123" {
		t.Errorf("SPLASH_PORT %q", v)
	}
	if v, ok := envValue(env, "SPLASH_API_KEY"); !ok || v != "" {
		t.Errorf("SPLASH_API_KEY %q, want an empty value (unset in the shell)", v)
	}
	if _, ok := envValue(env, "PATH"); ok {
		t.Error("PATH must come from the Terminal's login shell")
	}

	argv, env = e.AgentCommand("claude", engine.Target{Port: 8000, APIKey: "k"})
	for _, a := range argv {
		if strings.Contains(a, "k") && a != "claude" && !strings.Contains(a, "splash") {
			t.Errorf("suspicious argv %q", argv)
		}
	}
	if v, _ := envValue(env, "SPLASH_API_KEY"); v != "k" {
		t.Errorf("key %q", v)
	}
	if argv, env := e.AgentCommand("hermes", engine.Target{Port: 1}); argv != nil || env != nil {
		t.Error("hermes must not be offered")
	}
}

// WatchDownload watches the whole hub cache: the draft comes from another
// repository than the model.
func TestWatchDownload(t *testing.T) {
	e := New()
	hub := t.TempDir()
	e.hubDirs = []string{hub}
	write(t, filepath.Join(hub, "models--unsloth--Qwen3.8-27B-GGUF/blobs/old"), "already here", 0o644)
	meter := e.WatchDownload("unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M")
	if meter == nil || meter() != 0 {
		t.Fatal("no meter")
	}
	write(t, filepath.Join(hub, "models--unsloth--Qwen3.8-27B-GGUF/blobs/a.1234.incomplete"), "12345", 0o644)
	write(t, filepath.Join(hub, "models--incoai--Qwen3.8-27B-DFlash2/blobs/b.5678.incomplete"), "123", 0o644)
	if got := meter(); got != 8 {
		t.Errorf("meter %d, want 8", got)
	}
}
