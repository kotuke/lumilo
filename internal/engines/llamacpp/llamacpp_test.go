package llamacpp

import (
	"context"
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
	"github.com/kotuke/lumilo/internal/engines/enginekit"
	"github.com/kotuke/lumilo/internal/gguf"
)

func TestInfo(t *testing.T) {
	e := New()
	info := e.Info()
	if info.ID != "llamacpp" || info.Name != "llama.cpp" {
		t.Errorf("identity %q %q", info.ID, info.Name)
	}
	logo := info.Logo
	if logo.Kind != engine.LogoSVG || logo.Tile ||
		!strings.Contains(string(logo.Dark), `fill="#FAFAFA"`) || !strings.Contains(string(logo.Light), `fill="#111111"`) {
		t.Errorf("logo %+v", logo)
	}
	if c := info.Caps; c.Release || c.AllowedHosts || c.LiveSpeed || c.Memory.DefaultDraft != 0 || !c.Memory.ShrinksContext {
		t.Errorf("caps %+v", c)
	}
	if len(info.Suggestions) != 2 || len(info.ModelSearch.Sources) != 1 || info.ModelSearch.MLX != nil {
		t.Errorf("suggestions %+v, search %+v", info.Suggestions, info.ModelSearch)
	}
	if len(info.Agents) != 4 || info.Agents[0].ID != "claude" || info.Agents[3].ID != "pi" {
		t.Errorf("agents %d", len(info.Agents))
	}
	p := e.StopPolicy()
	if p.Graceful != syscall.SIGINT || p.Force != syscall.SIGINT || p.KillWait != 3*time.Second {
		t.Errorf("stop policy %+v", p)
	}
	if got := e.ChatURL(engine.Target{Port: 18201}); got != "http://127.0.0.1:18201/" {
		t.Errorf("chat url %q", got)
	}
	if argv, env := e.AgentCommand("claude", engine.Target{Port: 1}); argv != nil || env != nil {
		t.Error("agent command")
	}
}

func TestInstaller(t *testing.T) {
	e := New()
	in := e.Installer()
	d := in.Describe()
	if d.Method != engine.InstallHomebrew || d.Size != 56_000_000 || d.InstallCommand != "brew install llama.cpp" ||
		d.UpgradeCommand != "brew upgrade llama.cpp" || d.Prerequisite.Host != "brew.sh" {
		t.Errorf("describe %+v", d)
	}
	for v, want := range map[string]string{
		"0.7.0":   "https://github.com/ggml-org/llama.cpp/releases/tag/v0.7.0",
		"v0.7.1":  "https://github.com/ggml-org/llama.cpp/releases/tag/v0.7.1",
		"0.7.0_1": "https://github.com/ggml-org/llama.cpp/releases/tag/v0.7.0",
	} {
		if got := in.ReleaseNotesURL(v); got != want {
			t.Errorf("release url %q: %q", v, got)
		}
	}
	// The newest version is the formula's.
	port := serve(t, map[string]http.HandlerFunc{
		"/f": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"versions":{"stable":"0.7.2"},"revision":0}`))
		},
	})
	e.formulaURL = "http://127.0.0.1:" + strconv.Itoa(port) + "/f"
	if v, err := in.Latest(context.Background()); err != nil || v != "0.7.2" {
		t.Errorf("latest %q, %v", v, err)
	}
}

// WatchDownload watches the model's repository in the hub caches; a path
// has none.
func TestWatchDownload(t *testing.T) {
	e := New()
	hub := t.TempDir()
	e.hubDirs = []string{hub}
	meter := e.WatchDownload("ggml-org/Qwen3-0.6B-GGUF:Q8_0")
	if meter == nil || meter() != 0 {
		t.Fatal("no meter")
	}
	write(t, filepath.Join(hub, "models--ggml-org--Qwen3-0.6B-GGUF/blobs/abc.downloadInProgress"), []byte("12345"), 0o644)
	write(t, filepath.Join(hub, "models--other--Repo-GGUF/blobs/def"), []byte("123"), 0o644)
	if got := meter(); got != 5 {
		t.Errorf("meter %d, want 5", got)
	}
	if e.WatchDownload("/models/x.gguf") != nil {
		t.Error("a path has a meter")
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	// The bottle's layout; the script would print another version.
	script := "#!/bin/sh\necho 'version: 9.9.9 (build 1, commit x)' >&2\n"
	write(t, filepath.Join(root, "Cellar/llama.cpp/0.7.0_1/bin/llama-server"), []byte(script), 0o755)
	link := filepath.Join(root, "bin/llama-server")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../Cellar/llama.cpp/0.7.0_1/bin/llama-server", link); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "bin/brew"), []byte("#!/bin/sh\n"), 0o755)

	e := New()
	e.inst.Bins = []string{filepath.Join(root, "missing"), link}
	e.inst.Brews = []string{filepath.Join(root, "bin/brew")}
	inst := e.Installer()
	in := inst.Detect(context.Background())
	if !in.Installed || in.Version != "0.7.0" || in.Path != link || !in.Ready {
		t.Errorf("keg: %+v", in)
	}

	// Outside a Cellar the server is asked; 0.7.0 writes "Version: ..." to
	// stderr, older builds "version: 6123 (abc)".
	plain := filepath.Join(root, "other/llama-server")
	for out, want := range map[string]string{
		"Version: 0.7.0 (build 11669, commit 620d2225c)\nbuilt with AppleClang": "0.7.0",
		"version: 6123 (abcdef0)":  "6123",
		"llama.cpp (source build)": "",
	} {
		write(t, plain, []byte("#!/bin/sh\necho '"+out+"' >&2\n"), 0o755)
		e.inst.Bins = []string{plain}
		if in = inst.Detect(context.Background()); !in.Installed || in.Version != want {
			t.Errorf("%q: %+v", out, in)
		}
	}

	e.inst.Bins, e.inst.Brews = []string{filepath.Join(root, "nope")}, []string{filepath.Join(root, "nobrew")}
	if in = inst.Detect(context.Background()); in.Installed || in.Ready || in.Version != "" {
		t.Errorf("nothing installed: %+v", in)
	}
}

func TestModels(t *testing.T) {
	hub := t.TempDir()
	flat := t.TempDir()
	repo := func(owner, name string) string { return filepath.Join(hub, "models--"+owner+"--"+name) }
	// A snapshot file is a symlink into blobs/, as huggingface_hub lays it out.
	blob := 0
	file := func(repoDir, rev, name string, content []byte) string {
		blob++
		b := filepath.Join(repoDir, "blobs", strconv.Itoa(blob))
		write(t, b, content, 0o644)
		p := filepath.Join(repoDir, "snapshots", rev, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(b, p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	tiny := repo("acme", "Tiny-1B-GGUF")
	write(t, filepath.Join(tiny, "refs", "main"), []byte("rev2\n"), 0o644)
	file(tiny, "rev1", "Tiny-1B-Q4_K_M.gguf", textModel("llama")) // older snapshot: shadowed
	wantTiny := file(tiny, "rev2", "Tiny-1B-Q4_K_M.gguf", textModel("llama"))
	file(tiny, "rev2", "mmproj-Tiny-1B-F16.gguf", ggufFile([]kv{{"general.architecture", "clip"}, {"general.type", "mmproj"}}, 10))
	file(tiny, "rev2", "README.md", []byte("# Tiny"))
	file(tiny, "rev1", "Tiny-1B-Q8_0.gguf", textModel("llama")) // only in an old snapshot: listed

	big := repo("acme", "Big-70B-GGUF")
	part1 := file(big, "r", "Big-70B-Q4_K_M-00001-of-00002.gguf", textModel("qwen3"))
	file(big, "r", "Big-70B-Q4_K_M-00003-of-00002.gguf", textModel("qwen3")) // not a first part: never listed
	file(big, "r", "Big-70B-Q4_K_M-00002-of-00002.gguf", make([]byte, 5000))
	file(big, "r", "Big-70B-Q8_0-00001-of-00003.gguf", textModel("qwen3")) // part 3 missing
	file(big, "r", "Big-70B-Q8_0-00002-of-00003.gguf", make([]byte, 10))

	// Not text models: speech (no context length), embeddings, a LoRA, junk.
	file(repo("handy-computer", "gigaam-v3-e2e-rnnt-gguf"), "f7", "gigaam-v3-e2e-rnnt-Q8_0.gguf",
		ggufFile([]kv{{"general.architecture", "gigaam"}, {"general.name", "GigaAM v3"}, {"gigaam.block_count", uint32(16)}}, 100))
	file(repo("acme", "Embed-GGUF"), "e", "embed.gguf", textModel("nomic-bert"))
	file(repo("acme", "Lora-GGUF"), "l", "lora.gguf", ggufFile([]kv{{"general.architecture", "llama"}, {"general.type", "adapter"}, {"llama.context_length", uint32(1)}}, 0))
	file(repo("acme", "Junk-GGUF"), "j", "junk.gguf", []byte("this is not a gguf file\n"))
	// A dangling snapshot link (a download in progress) and a dataset.
	if err := os.MkdirAll(filepath.Join(repo("acme", "Partial-GGUF"), "snapshots", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(hub, "nowhere"), filepath.Join(repo("acme", "Partial-GGUF"), "snapshots", "p", "x.gguf")); err != nil {
		t.Fatal(err)
	}
	file(filepath.Join(hub, "datasets--acme--data"), "d", "data.gguf", textModel("llama"))
	write(t, filepath.Join(hub, "CACHEDIR.TAG"), []byte("Signature"), 0o644)

	// The flat cache of older llama.cpp builds.
	write(t, filepath.Join(flat, "ggml-org_gemma-3-4b-it-GGUF_gemma-3-4b-it-Q4_K_M.gguf"), textModel("gemma3"), 0o644)
	write(t, filepath.Join(flat, "ggml-org_gemma-3-4b-it-GGUF_mmproj-model-f16.gguf"), textModel("gemma3"), 0o644)
	write(t, filepath.Join(flat, "notes.txt"), []byte("x"), 0o644)

	e := New()
	e.hubDirs = []string{hub, filepath.Join(hub, "absent")}
	e.flatDirs = []string{flat, filepath.Join(flat, "absent")}
	models, err := e.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	size := func(arch string) int64 { return int64(len(textModel(arch))) }
	// Flat models have no owner, so they sort first.
	want := []engine.Model{
		{ID: filepath.Join(flat, "ggml-org_gemma-3-4b-it-GGUF_gemma-3-4b-it-Q4_K_M.gguf"), Name: "ggml-org_gemma-3-4b-it-GGUF_gemma-3-4b-it-Q4_K_M", SizeBytes: size("gemma3"), ContextLength: 4096,
			Features: engine.Features{Vision: true, VisionBytes: size("gemma3")}},
		{ID: part1, Name: "Big-70B-GGUF", Owner: "acme", Variant: "Q4_K_M", SizeBytes: size("qwen3") + 5000, ContextLength: 4096},
		{ID: wantTiny, Name: "Tiny-1B-GGUF", Owner: "acme", Variant: "Q4_K_M", SizeBytes: size("llama"), ContextLength: 4096,
			Features: engine.Features{Vision: true, VisionBytes: int64(len(ggufFile([]kv{{"general.architecture", "clip"}, {"general.type", "mmproj"}}, 10)))}},
		{ID: filepath.Join(tiny, "snapshots/rev1/Tiny-1B-Q8_0.gguf"), Name: "Tiny-1B-GGUF", Owner: "acme", Variant: "Q8_0", SizeBytes: size("llama"), ContextLength: 4096},
	}
	if len(models) != len(want) {
		for _, m := range models {
			t.Logf("got %+v", m)
		}
		t.Fatalf("%d models, want %d", len(models), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(models[i], want[i]) {
			t.Errorf("model %d:\n got %+v\nwant %+v", i, models[i], want[i])
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Models(ctx); err == nil {
		t.Error("cancelled context ignored")
	}
}

func TestVariantOf(t *testing.T) {
	for _, c := range [][4]string{
		{"Qwen3-0.6B-GGUF", "Qwen3-0.6B-Q8_0.gguf", "Qwen3-0.6B", "Q8_0"},
		// general.name "OrcaCyber Nano 1.0 AI Defense" is the longer prefix:
		// the tag Splash's list shows for the same file.
		{"OrcaCyber-Nano-1.0", "OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf", "OrcaCyber-Nano-1.0-AI-Defense", "Mix-15.7G"},
		{"OrcaCyber-Nano-1.0", "OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf", "", "AI-Defense-Mix-15.7G"},
		{"gemma-3-4b-it-GGUF", "gemma-3-4b-it-Q4_K_M-00001-of-00002.gguf", "Gemma 3 4b It", "Q4_K_M"},
		{"Some-GGUF", "other-name.Q5_K_S.gguf", "", "other-name.Q5_K_S"},
		{"Model-gguf", "model.gguf", "", "model"},
	} {
		if got := variantOf(c[0], c[1], strings.Join(strings.Fields(c[2]), "-")); got != c[3] {
			t.Errorf("%s/%s: %q, want %q", c[0], c[1], got, c[3])
		}
	}
}

func TestReadHeader(t *testing.T) {
	dir := t.TempDir()
	// The reader stops at the context length and never reaches the huge
	// (here: truncated, so unreadable) vocabulary that follows.
	data := textModel("qwen35")
	cut := ggufFile([]kv{
		{"general.architecture", "qwen35"},
		{"general.languages", []string{"en", "ru"}},
		{"qwen35.rope.dimension_sections", []uint32{1, 2, 3}},
		{"qwen35.context_length", uint32(262144)},
		{"tokenizer.ggml.tokens", []string{"a"}},
	}, 0)
	write(t, filepath.Join(dir, "ok.gguf"), data, 0o644)
	write(t, filepath.Join(dir, "cut.gguf"), cut[:len(cut)-12], 0o644)
	for _, name := range []string{"ok.gguf", "cut.gguf"} {
		h, err := gguf.ReadFile(filepath.Join(dir, name))
		if err != nil || h.Arch != "qwen35" || !h.HasContext {
			t.Errorf("%s: %+v, %v", name, h, err)
		}
	}
	// A tokenizer key before the context length ends the search: not a
	// text model by this test.
	write(t, filepath.Join(dir, "order.gguf"), ggufFile([]kv{{"general.architecture", "llama"}, {"tokenizer.ggml.model", "gpt2"}, {"llama.context_length", uint32(1)}}, 0), 0o644)
	if h, _ := gguf.ReadFile(filepath.Join(dir, "order.gguf")); h.TextModel() {
		t.Error("order.gguf")
	}
	for name, content := range map[string][]byte{
		"empty.gguf": nil, "v1.gguf": append([]byte("GGUF\x01\x00\x00\x00"), make([]byte, 16)...),
		"long.gguf": append(ggufFile(nil, 0)[:16], []byte("\x01\x00\x00\x00\x00\x00\x00\x00\xff\xff\xff\xff\xff\xff\xff\x7f")...),
	} {
		write(t, filepath.Join(dir, name), content, 0o644)
		if _, err := gguf.ReadFile(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestServeCommand(t *testing.T) {
	t.Setenv("LLAMA_API_KEY", "stale-key")
	t.Setenv("LLAMA_ARG_PORT", "9999")
	t.Setenv("LLAMA_ARG_MODEL", "/stale.gguf")
	t.Setenv("HF_HUB_OFFLINE", "1")
	t.Setenv("HF_TOKEN", "hf-token-kept")
	t.Setenv("PATH", "/custom/bin")
	dir := t.TempDir()
	model := filepath.Join(dir, "Tiny-Q4_K_M.gguf")
	write(t, model, textModel("llama"), 0o644)
	write(t, filepath.Join(dir, "mmproj-Tiny-F16.gguf"), []byte("x"), 0o644)

	e := New()
	cfg := engine.Config{
		Model: model, Port: 8123, Host: "0.0.0.0", Access: engine.AccessNetwork,
		APIKey: "s3cret-key", AllowedHosts: []string{"mymac.local"},
		IdleRelease: 90*time.Second + 500*time.Millisecond, ModelUpdates: false,
	}
	argv, env := e.ServeCommand(cfg)
	want := []string{e.binPath(), "-m", model, "--alias", "Tiny-Q4_K_M", "--host", "0.0.0.0", "--port", "8123", "--metrics",
		"--mmproj", filepath.Join(dir, "mmproj-Tiny-F16.gguf"), "--offline", "--sleep-idle-seconds", "91"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv\n got %q\nwant %q", argv, want)
	}
	if !filepath.IsAbs(argv[0]) {
		t.Errorf("argv[0] %q", argv[0])
	}
	// The key never reaches argv, where ps would show it.
	for _, a := range argv {
		if strings.Contains(a, "s3cret") || strings.HasPrefix(a, "--api-key") {
			t.Errorf("key in argv: %q", argv)
		}
	}
	if v, _ := envValue(env, "LLAMA_API_KEY"); v != "s3cret-key" {
		t.Errorf("env key %q", v)
	}
	for _, name := range []string{"LLAMA_ARG_PORT", "LLAMA_ARG_MODEL", "HF_HUB_OFFLINE"} {
		if v, ok := envValue(env, name); ok {
			t.Errorf("%s leaked: %q", name, v)
		}
	}
	if v, _ := envValue(env, "HF_TOKEN"); v != "hf-token-kept" {
		t.Errorf("HF_TOKEN %q", v)
	}
	path, _ := envValue(env, "PATH")
	if !strings.HasPrefix(path, enginekit.SystemPath) || !strings.HasSuffix(path, ":/custom/bin") {
		t.Errorf("PATH %q", path)
	}
	// Probe reports the sleep setting it was started with.
	if s := e.Probe(context.Background(), engine.Target{Port: closedPort(t)}); s.IdleRelease != 0 {
		t.Errorf("idle of an unknown port: %v", s.IdleRelease)
	}
	e.mu.Lock()
	idle := e.memo(8123).idle
	e.mu.Unlock()
	if idle != cfg.IdleRelease {
		t.Errorf("remembered idle %v", idle)
	}

	// No key: none in env although the parent had one; browsers of other
	// origins are kept out. Updates on: no --offline. Two projectors: none.
	write(t, filepath.Join(dir, "mmproj-Tiny-F32.gguf"), []byte("x"), 0o644)
	argv, env = e.ServeCommand(engine.Config{Model: model, Port: 8000, ModelUpdates: true})
	if _, ok := envValue(env, "LLAMA_API_KEY"); ok {
		t.Error("stale parent key reached the server")
	}
	line := strings.Join(argv, " ")
	if strings.Contains(line, "--offline") || strings.Contains(line, "--mmproj") || strings.Contains(line, "--sleep-idle-seconds") ||
		!strings.Contains(line, "--host 127.0.0.1") || !strings.HasSuffix(line, "--metrics --cors-origins localhost") {
		t.Errorf("argv %q", line)
	}
}

func TestSleepSeconds(t *testing.T) {
	for d, want := range map[time.Duration]int{0: 0, -time.Second: 0, time.Millisecond: 1, 10 * time.Minute: 600, 90500 * time.Millisecond: 91} {
		if got := sleepSeconds(d); got != want {
			t.Errorf("%v: %d, want %d", d, got, want)
		}
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestParseLineFixtures(t *testing.T) {
	e := New()
	count := func(path string) map[engine.EventKind][]engine.Event {
		got := map[engine.EventKind][]engine.Event{}
		for _, l := range readLines(t, path) {
			if ev, ok := e.ParseLine(l, true); ok {
				got[ev.Kind] = append(got[ev.Kind], ev)
			}
		}
		return got
	}

	serve := count(filepath.Join("testdata", "lifecycle", "serve.stderr.txt"))
	if ev := serve[engine.EventLoading]; len(ev) != 1 || ev[0].Model != fixtureModel || ev[0].Line != "srv    load_model: loading model '"+fixtureModel+"'" {
		t.Errorf("loading %+v", ev)
	}
	if ev := serve[engine.EventReady]; len(ev) != 2 || ev[0].Line != "srv  llama_server: model loaded" ||
		ev[1].Line != "srv  llama_server: listening on http://127.0.0.1:18201" {
		t.Errorf("ready %+v", ev)
	}
	// Two single requests and three parallel ones.
	if n, m := len(serve[engine.EventRequestStart]), len(serve[engine.EventRequestDone]); n != 5 || m != 5 {
		t.Errorf("requests: %d started, %d done", n, m)
	}
	if ev := serve[engine.EventStopping]; len(ev) != 1 {
		t.Errorf("stopping %+v", ev)
	}
	if ev := serve[engine.EventError]; len(ev) != 0 {
		t.Errorf("errors in a clean run: %+v", ev)
	}

	// After a sleep the model is loaded again without a "model loaded" line.
	sleep := count(filepath.Join("testdata", "lifecycle", "sleep.stderr.txt"))
	if len(sleep[engine.EventLoading]) != 3 || len(sleep[engine.EventReady]) != 2 {
		t.Errorf("sleep: %d loading, %d ready", len(sleep[engine.EventLoading]), len(sleep[engine.EventReady]))
	}
	// The download run prints the -hf name, not a path.
	dl := count(filepath.Join("testdata", "lifecycle", "download.stderr.txt"))
	if ev := dl[engine.EventLoading]; len(ev) != 1 || ev[0].Model != "ggml-org/Qwen3-0.6B-GGUF:Q8_0" {
		t.Errorf("download %+v", ev)
	}
	stop := count(filepath.Join("testdata", "lifecycle", "stop_gen.stderr.txt"))
	if ev := stop[engine.EventStopping]; len(ev) != 2 || ev[1].Line != "Received second interrupt, terminating immediately." {
		t.Errorf("stop_gen %+v", ev)
	}

	for _, l := range []string{
		"", "   ", "0.00.268.094 W load: control-looking token: 128247 '</s>' was not control-type; this is probably a bug in the model. its type will be overridden",
		"0.02.266.110 I slot print_timing: id  0 | task 4 | prompt eval time =     125.02 ms /    18 tokens",
		"0.00.074.001 W srv  llama_server: security: no API key is set and CORS allows all origins",
		"0.03.400.537 I srv  handle_sleep: server is entering sleeping state",
		"built with AppleClang 21.0.0.21000334 for Darwin arm64",
	} {
		if ev, ok := e.ParseLine(l, true); ok {
			t.Errorf("%q parsed as %+v", l, ev)
		}
	}
	// Older builds: no prefix.
	if ev, ok := e.ParseLine("main: server is listening on http://127.0.0.1:8080 - starting the main loop", false); !ok || ev.Kind != engine.EventReady {
		t.Errorf("old ready: %+v", ev)
	}

	for _, name := range []string{"port_busy", "no_model", "bad_flag", "bad_port", "offline_missing", "not_gguf", "second_serve"} {
		_, lines := readTail(t, filepath.Join("testdata", "failures", name+".stderr.txt"))
		found := false
		for _, l := range lines {
			if ev, ok := e.ParseLine(l, true); ok && ev.Kind == engine.EventError {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no error event", name)
		}
	}
}

func TestClassifyFailureFixtures(t *testing.T) {
	e := New()
	cases := []struct {
		name string
		kind engine.FailureKind
		line string // suffix of the line
	}{
		{"port_busy", engine.FailPortBusy, "E srv         start: couldn't bind HTTP server socket, hostname: 127.0.0.1, port: 18205"},
		{"second_serve", engine.FailPortBusy, "couldn't bind HTTP server socket, hostname: 127.0.0.1, port: 18210"},
		{"bad_port", engine.FailExit, "couldn't bind HTTP server socket, hostname: 127.0.0.1, port: 99999"},
		{"no_model", engine.FailModel, "E gguf_init_from_file: failed to open GGUF file '/nonexistent/model.gguf' (No such file or directory)"},
		{"not_gguf", engine.FailModel, "E gguf_init_from_reader: invalid magic characters: 'this', expected 'GGUF'"},
		{"offline_missing", engine.FailModel, "E llama_model_load_from_file_impl: exactly one out metadata, path_model, and file must be defined"},
		{"bad_flag", engine.FailExit, "error: invalid argument: --no-such-flag"},
		// SIGKILLed while allocating: no line tells why.
		{"huge_ctx", engine.FailExit, "n_ctx_seq (4000000) > n_ctx_train (40960) -- possible training context overflow"},
	}
	for _, c := range cases {
		code, tail := readTail(t, filepath.Join("testdata", "failures", c.name+".stderr.txt"))
		f := e.ClassifyFailure(code, tail)
		if f.Kind != c.kind || f.Code != code || !strings.HasSuffix(f.Line, c.line) {
			t.Errorf("%s: %+v, want kind %v line …%q", c.name, f, c.kind, c.line)
		}
	}
	if code, _ := readTail(t, filepath.Join("testdata", "failures", "huge_ctx.stderr.txt")); code != 137 {
		t.Errorf("huge_ctx exit %d", code)
	}
	code, tail := readTail(t, filepath.Join("testdata", "lifecycle", "serve.stderr.txt"))
	if f := e.ClassifyFailure(code, tail); code != 0 || f != (engine.Failure{}) {
		t.Errorf("clean stop: %d %+v", code, f)
	}
	code, tail = readTail(t, filepath.Join("testdata", "lifecycle", "stop_gen.stderr.txt"))
	if f := e.ClassifyFailure(code, tail); code != 134 || f.Kind != engine.FailExit || !strings.Contains(f.Line, "GGML_ASSERT") {
		t.Errorf("forced stop: %d %+v", code, f)
	}
}

func TestClassifyFailureTexts(t *testing.T) {
	e := New()
	const load = "0.00.069.035 I srv    load_model: loading model '/m.gguf'"
	cases := []struct {
		name string
		tail []string
		kind engine.FailureKind
		line string
	}{
		{"metal buffer", []string{load, "0.01.000.000 E ggml_metal_buffer_init: error: failed to allocate buffer, size = 30000.00 MiB",
			"0.01.000.001 E llama_model_load: error loading model: unable to allocate Metal buffer", "0.01.000.002 E srv    load_model: failed to load model, '/m.gguf'"},
			engine.FailMemory, "0.01.000.000 E ggml_metal_buffer_init: error: failed to allocate buffer, size = 30000.00 MiB"},
		{"oom", []string{"E ggml_metal_graph_compute: command buffer 0 failed with status 5", "error: Insufficient Memory (00000008:kIOGPUCommandBufferCallbackErrorOutOfMemory)"},
			engine.FailMemory, "error: Insufficient Memory (00000008:kIOGPUCommandBufferCallbackErrorOutOfMemory)"},
		{"arch", []string{load, "0.00.1 E llama_model_load: error loading model: error loading model architecture: unknown model architecture: 'gigaam'"},
			engine.FailModel, "0.00.1 E llama_model_load: error loading model: error loading model architecture: unknown model architecture: 'gigaam'"},
		{"empty", nil, engine.FailExit, ""},
		{"last line", []string{"a", "b"}, engine.FailExit, "b"},
		{"first error wins", []string{"0.00.000.001 I x", "0.00.000.002 E first", "0.00.000.003 E second"}, engine.FailExit, "0.00.000.002 E first"},
	}
	for _, c := range cases {
		f := e.ClassifyFailure(1, c.tail)
		if f.Kind != c.kind || f.Line != c.line || f.Code != 1 {
			t.Errorf("%s: %+v, want kind %v line %q", c.name, f, c.kind, c.line)
		}
	}
}

// psListing builds "ps -axww -o pid=,ppid=,rss=,args=" output.
func psListing(lines ...string) func() ([]byte, error) {
	return func() ([]byte, error) { return []byte(strings.Join(lines, "\n") + "\n"), nil }
}

func TestFindRunning(t *testing.T) {
	healthy := func(code int) int {
		return serve(t, map[string]http.HandlerFunc{"/health": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }})
	}
	ready, loading, other := healthy(200), healthy(503), healthy(404)
	dead := closedPort(t)
	p := strconv.Itoa
	e := New()
	// Who listens where: each port is held by the process that names it,
	// except where the test says otherwise.
	owner := map[int][]int{ready: {100}, loading: {200}, other: {400}}
	e.listeners = func(port int) ([]int, error) { return owner[port], nil }
	e.ps = psListing(
		"    1     0  28720 /sbin/launchd",
		// A path with spaces: ps joins argv with spaces.
		"  100     1 4606176 /opt/homebrew/bin/llama-server -m /Users/user/Library/Application Support/Lumilo/m 1.gguf --port "+p(ready)+" --metrics --sleep-idle-seconds 600",
		"  200     1  90000 /usr/local/bin/llama-server -hf ggml-org/Qwen3-0.6B-GGUF:Q8_0 --port="+p(loading),
		"  300     1  10000 llama-server --port "+p(dead)+" -m /x.gguf",                    // not answering
		"  400     1  10000 /opt/homebrew/bin/llama-server --port "+p(other)+" -m /y.gguf", // answers, but not like llama-server
		"  500     1  10000 /opt/homebrew/bin/llama-server --host /tmp/l.sock -m /z.gguf",  // Unix socket
		"  600   200  10000 /usr/local/bin/llama-server -m /child.gguf --port "+p(ready),   // a router's child
		"  700     1  10000 /usr/bin/python3 /opt/homebrew/bin/llama-server --port "+p(ready),
		"  bad lines are skipped",
	)
	got, err := e.FindRunning()
	if err != nil {
		t.Fatal(err)
	}
	want := []engine.Running{
		{PID: 100, Port: ready, Model: "/Users/user/Library/Application Support/Lumilo/m 1.gguf"},
		{PID: 200, Port: loading, Model: "ggml-org/Qwen3-0.6B-GGUF:Q8_0"},
	}
	if want[0].Port > want[1].Port {
		want[0], want[1] = want[1], want[0]
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("running %+v\nwant %+v", got, want)
	}
	e.mu.Lock()
	m := *e.memo(ready)
	e.mu.Unlock()
	if m.pid != 100 || m.idle != 10*time.Minute {
		t.Errorf("memo %+v", m)
	}

	// Without --port: the default ports are tried.
	saved := defaultPorts
	defer func() { defaultPorts = saved }()
	defaultPorts = []int{dead, ready}
	e.ps = psListing("  800     1  10 /opt/homebrew/bin/llama-server")
	owner[ready] = []int{800}
	if got, _ := e.FindRunning(); len(got) != 1 || got[0] != (engine.Running{PID: 800, Port: ready}) {
		t.Errorf("default port: %+v", got)
	}
	// The default port answers, but another process listens there (e.g.
	// a server started with LLAMA_ARG_PORT): 800 is not that server.
	owner[ready] = []int{900}
	if got, _ := e.FindRunning(); len(got) != 0 {
		t.Errorf("adopted a port another process holds: %+v", got)
	}
	// Nor is a process whose --port another process holds.
	e.ps = psListing("  100     1 10 /opt/homebrew/bin/llama-server --port " + p(ready))
	if got, _ := e.FindRunning(); len(got) != 0 {
		t.Errorf("adopted a port another process holds: %+v", got)
	}
	// lsof failing ties nothing.
	owner[ready] = []int{100}
	e.listeners = func(int) ([]int, error) { return nil, os.ErrPermission }
	if got, _ := e.FindRunning(); len(got) != 0 {
		t.Errorf("adopted without lsof: %+v", got)
	}
	e.ps = func() ([]byte, error) { return nil, os.ErrPermission }
	if _, err := e.FindRunning(); err == nil {
		t.Error("ps error ignored")
	}
}

// An agent asks for the model by its --alias.
func TestAgentCommand(t *testing.T) {
	e := New()
	if argv, _ := e.AgentCommand("codex", engine.Target{Port: 9000}); argv != nil {
		t.Errorf("unknown server: %q", argv)
	}
	e.ServeCommand(engine.Config{Model: "/m/Qwen3-0.6B-Q8_0.gguf", Port: 9000})
	argv, _ := e.AgentCommand("codex", engine.Target{Port: 9000})
	if !strings.Contains(strings.Join(argv, " "), `-c model="Qwen3-0.6B-Q8_0"`) {
		t.Errorf("codex: %q", argv)
	}
}
