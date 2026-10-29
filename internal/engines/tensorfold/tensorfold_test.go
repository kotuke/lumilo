package tensorfold

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

func TestInfo(t *testing.T) {
	info := New().Info()
	if info.ID != "tensorfold" || info.Name != "TensorFold" || len(info.Logo.Dark) == 0 || info.Logo.Kind != engine.LogoPNG {
		t.Errorf("info = %+v", info)
	}
	c := info.Caps
	if c.Chat || c.Release || !c.AllowedHosts || !c.LiveSpeed || c.DiskCache || c.MaxContext != 0 {
		t.Errorf("caps = %+v", c)
	}
	if c.Memory.KVBytesPerElement != 3.25 || c.Memory.Reserve != 4_000_000_000 || !c.Memory.ShrinksContext || c.Memory.Share != 0.7 {
		t.Errorf("memory caps = %+v", c.Memory)
	}
	var ids []string
	for _, a := range info.Agents {
		ids = append(ids, a.ID)
	}
	if !slices.Equal(ids, []string{"claude", "codex", "opencode", "pi"}) {
		t.Errorf("agents = %v", ids)
	}
	if New().ChatURL(engine.Target{Port: 8080}) != "" {
		t.Error("ChatURL: the server has no chat page")
	}
	if New().WatchDownload(fixtureRepo) != nil {
		t.Error("WatchDownload: the server never downloads")
	}
}

func TestInstaller(t *testing.T) {
	inst := New().Installer()
	d := inst.Describe()
	if d.Method != engine.InstallHomebrew || d.InstallCommand != "brew install ashhart/tensorfold/tensorfold" ||
		d.UpgradeCommand != "brew upgrade ashhart/tensorfold/tensorfold" {
		t.Errorf("describe = %+v", d)
	}
	if got := inst.ReleaseNotesURL("v1.1.9_1"); got != "https://github.com/ashhart/TensorFold/releases/tag/v1.1.9" {
		t.Errorf("notes = %q", got)
	}
}

func TestVersion(t *testing.T) {
	// The formula links bin/tensorfold to the keg's libexec/bin/tensorfold-native.
	dir := t.TempDir()
	keg := filepath.Join(dir, "Cellar", "tensorfold", "1.1.9", "libexec", "bin", "tensorfold-native")
	if err := os.MkdirAll(filepath.Dir(keg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keg, []byte("#!/bin/sh\necho tensorfold-native 9.9.9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin", "tensorfold")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(keg, link); err != nil {
		t.Fatal(err)
	}
	if v := version(context.Background(), link); v != "1.1.9" {
		t.Errorf("keg version = %q", v)
	}
	// Outside a keg: "tensorfold --version".
	loose := filepath.Join(t.TempDir(), "tensorfold")
	os.WriteFile(loose, []byte("#!/bin/sh\necho tensorfold-native 9.9.9\n"), 0o755)
	if v := version(context.Background(), loose); v != "9.9.9" {
		t.Errorf("--version = %q", v)
	}
}

// families reads the installed executable's capabilities, once per
// executable, and falls back to families.go when it cannot.
func TestFamiliesFromExecutable(t *testing.T) {
	tf := newTest(t)
	bin := tf.inst.Bins[0]
	if f := tf.families(context.Background()); len(f) != len(families) {
		t.Errorf("no executable: %d families", len(f))
	}
	caps, _ := filepath.Abs("testdata/capabilities.json")
	script := "#!/bin/sh\necho run >> " + bin + ".runs\ncat '" + caps + "' | sed 's/\"lfm2_moe\"/\"lfm3\"/'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f := tf.families(context.Background())
	tf.families(context.Background())
	if _, ok := f["lfm3"]; !ok || f["lfm2_moe"] != nil {
		t.Errorf("families not from the executable: %v", f)
	}
	if runs, _ := os.ReadFile(bin + ".runs"); strings.Count(string(runs), "run") != 1 {
		t.Errorf("ran %q", runs)
	}
	cp := checkpoint{ModelType: "lfm3", Format: "mlx-q4g64"}
	if cp.supported(f) != "" {
		t.Errorf("lfm3 refused: %s", cp.supported(f))
	}
}

func TestCapabilitiesFamilies(t *testing.T) {
	// capabilitiesFamilies of the fixture is families.go.
	fromCaps, err := capabilitiesFamilies([]byte(readFile(t, "testdata/capabilities.json")))
	if err != nil {
		t.Fatal(err)
	}
	for name, formats := range fromCaps {
		got := slices.Clone(families[name])
		sort.Strings(got)
		sort.Strings(formats)
		if !slices.Equal(got, formats) {
			t.Errorf("%s: families.go %v, capabilities %v", name, got, formats)
		}
	}
	if len(fromCaps) != len(families) {
		t.Errorf("%d families from capabilities, %d in families.go", len(fromCaps), len(families))
	}
	// families.go is the fixture's list, less what Lumilo cannot serve.
	var caps struct {
		Families map[string][]string `json:"families"`
	}
	if err := json.Unmarshal([]byte(readFile(t, "testdata/capabilities.json")), &caps); err != nil {
		t.Fatal(err)
	}
	for name, formats := range caps.Families {
		if name == "fish_omni" || name == "glm_moe_dsa" {
			if _, ok := families[name]; ok {
				t.Errorf("%s is listed", name)
			}
			continue
		}
		var mlx []string
		for _, f := range formats {
			if strings.HasPrefix(f, "mlx-") || f == "bf16" {
				mlx = append(mlx, f)
			}
		}
		got := slices.Clone(families[name])
		sort.Strings(got)
		sort.Strings(mlx)
		if !slices.Equal(got, mlx) {
			t.Errorf("%s: %v, capabilities %v", name, got, mlx)
		}
	}
	for name := range families {
		if _, ok := caps.Families[name]; !ok {
			t.Errorf("%s is not in the capabilities", name)
		}
	}
}

func TestParseCheckpoint(t *testing.T) {
	for _, c := range []struct {
		name                        string
		typ, format                 string
		supported, vision, drafters bool
		drafter, dense27            bool
	}{
		{"qwen3.5-2b-mlx-4bit", "qwen3_5", "mlx-q4g64", true, false, false, false, false},
		{"qwen3.8-27b-mlx-4bit", "qwen3_5", "mlx-q4g64", true, true, true, false, true},
		{"nemotron-lightning-mlx-4bit", "nemotron_h", "mlx-q4g64", true, false, false, false, false},
		{"lfm3-1.7b-mlx-4bit", "lfm3", "mlx-q4g64", false, false, false, false, false},
		{"qwen3.8-27b-dflash2", "qwen3", "bf16", false, false, false, true, false},
	} {
		cp, err := parseCheckpoint([]byte(config(t, c.name)))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if cp.ModelType != c.typ || cp.Format != c.format || (cp.supported(families) == "") != c.supported ||
			cp.visionOK() != c.vision || cp.drafterOK() != c.drafters || cp.Drafter != c.drafter || cp.Dense27 != c.dense27 {
			t.Errorf("%s: %+v supported=%q vision=%v drafters=%v", c.name, cp, cp.supported(families), cp.visionOK(), cp.drafterOK())
		}
	}
	// The 2B has an MTP layer (text_config.mtp_num_hidden_layers).
	if cp, _ := parseCheckpoint([]byte(config(t, "qwen3.5-2b-mlx-4bit"))); cp.MTP != 1 {
		t.Errorf("MTP = %d", cp.MTP)
	}
	// A family's own formats: lfm2_moe reads group 64 only.
	cp, _ := parseCheckpoint([]byte(`{"model_type":"lfm2_moe","quantization":{"bits":4,"group_size":32}}`))
	if cp.supported(families) == "" {
		t.Error("lfm2_moe g32 accepted")
	}
	cp, _ = parseCheckpoint([]byte(`{"model_type":"qwen3_5","quantization":{"bits":4,"group_size":64,"mode":"mxfp4"}}`))
	if cp.Format != "mxfp4" || cp.supported(families) == "" {
		t.Errorf("mxfp4: %+v", cp)
	}
	if _, err := parseCheckpoint([]byte("not json")); err == nil {
		t.Error("bad JSON accepted")
	}
}

func TestModels(t *testing.T) {
	dir := t.TempDir()
	idx := `{"weight_map":{"a":"model-00001-of-00002.safetensors","b":"model-00002-of-00002.safetensors"}}`
	putRepo(t, dir, "TensorFold/Qwen3.8-27B-MLX-4bit", "c27",
		hubFile{"config.json", config(t, "qwen3.8-27b-mlx-4bit")},
		hubFile{"model.safetensors.index.json", idx},
		hubFile{"model-00001-of-00002.safetensors", "1234567890"},
		hubFile{"model-00002-of-00002.safetensors", "12345"})
	putRepo(t, dir, "z-lab/Qwen3.8-27B-DFlash2", "cdf",
		hubFile{"config.json", config(t, "qwen3.8-27b-dflash2")},
		hubFile{"model.safetensors", strings.Repeat("d", 1280)})
	// Splash's DFlash2 of the same model: offered after the recipe's.
	putRepo(t, dir, "incoai/Qwen3.8-27B-DFlash2", "cin",
		hubFile{"config.json", config(t, "qwen3.8-27b-dflash2")},
		hubFile{"model.safetensors", strings.Repeat("d", 128)})
	putRepo(t, dir, fixtureRepo, "c2",
		hubFile{"config.json", config(t, "qwen3.5-2b-mlx-4bit")},
		hubFile{"model.safetensors", "12"})
	// Not listed: a family it does not serve, a shard missing, no weights.
	putRepo(t, dir, "LiquidAI/LFM3-1.7B-MLX-4bit", "cl",
		hubFile{"config.json", config(t, "lfm3-1.7b-mlx-4bit")}, hubFile{"model.safetensors", "1"})
	putRepo(t, dir, "a/Partial-MLX", "cp",
		hubFile{"config.json", config(t, "qwen3.5-2b-mlx-4bit")}, hubFile{"model.safetensors.index.json", idx},
		hubFile{"model-00001-of-00002.safetensors", "1"})
	putRepo(t, dir, "a/NoWeights", "cn", hubFile{"config.json", config(t, "qwen3.5-2b-mlx-4bit")})
	// A download broken off before the index: one shard of three.
	putRepo(t, dir, "a/FirstShard-MLX", "cf",
		hubFile{"config.json", config(t, "qwen3.5-2b-mlx-4bit")}, hubFile{"model-00001-of-00003.safetensors", "1"})

	tf := newTest(t, dir)
	ms, err := tf.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	if want := []string{fixtureRepo, "TensorFold/Qwen3.8-27B-MLX-4bit"}; !slices.Equal(ids, want) {
		t.Fatalf("models = %v, want %v", ids, want)
	}
	small, big := ms[0], ms[1]
	if small.Name != "Qwen3.5-2B-MLX-4bit" || small.Owner != "mlx-community" || small.SizeBytes != 2 ||
		small.ContextLength != 262144 || small.KVElements != 6*2*512 || small.Features.Vision || len(small.Features.Drafts) != 0 {
		t.Errorf("2B = %+v", small)
	}
	if big.SizeBytes != 15 || !big.Features.Vision {
		t.Errorf("27B = %+v", big)
	}
	// 1280 bytes of bf16 take 576 in memory (drafterMemory).
	want := []engine.Draft{
		{Kind: engine.DraftDFlash, Name: "z-lab/Qwen3.8-27B-DFlash2", ID: "z-lab/Qwen3.8-27B-DFlash2", SizeBytes: 576, Default: true},
		{Kind: engine.DraftDFlash, Name: "incoai/Qwen3.8-27B-DFlash2", ID: "incoai/Qwen3.8-27B-DFlash2", SizeBytes: 57},
	}
	if !slices.Equal(big.Features.Drafts, want) {
		t.Errorf("27B drafts = %+v", big.Features.Drafts)
	}
}

func TestServeCommand(t *testing.T) {
	dir := t.TempDir()
	putRepo(t, dir, "TensorFold/Qwen3.8-27B-MLX-4bit", "c27",
		hubFile{"config.json", config(t, "qwen3.8-27b-mlx-4bit")}, hubFile{"model.safetensors", "1"})
	putRepo(t, dir, fixtureRepo, "c2",
		hubFile{"config.json", config(t, "qwen3.5-2b-mlx-4bit")}, hubFile{"model.safetensors", "1"})
	putRepo(t, dir, "z-lab/Qwen3.8-27B-DFlash2", "cdf",
		hubFile{"config.json", config(t, "qwen3.8-27b-dflash2")}, hubFile{"model.safetensors", "1"})
	tf := newTest(t, dir)
	t.Setenv("TENSORFOLD_API_KEY", "stale")
	t.Setenv("TENSORFOLD_REQUEST_LOG", "1")

	argv, env := tf.ServeCommand(engine.Config{Model: fixtureRepo, Port: 8000, Host: "127.0.0.1"})
	want := []string{tf.binPath(), "serve", fixtureRepo, "--host", "127.0.0.1", "--port", "8000"}
	if !slices.Equal(argv, want) {
		t.Errorf("argv = %q", argv)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "TENSORFOLD_API_KEY=") || strings.HasPrefix(e, "TENSORFOLD_REQUEST_LOG=") {
			t.Errorf("env keeps %s", e)
		}
	}

	argv, env = tf.ServeCommand(engine.Config{Model: "TensorFold/Qwen3.8-27B-MLX-4bit", Port: 8001, Host: "0.0.0.0",
		APIKey: "k", AllowedHosts: []string{"mymac.local", " "}, IdleRelease: 90*time.Second + time.Millisecond,
		ContextTokens: 65536, Draft: "z-lab/Qwen3.8-27B-DFlash2"})
	want = []string{tf.binPath(), "serve", "TensorFold/Qwen3.8-27B-MLX-4bit", "--host", "0.0.0.0", "--port", "8001",
		"--context", "65536", "--idle-unload", "91", "--allowed-host", "mymac.local", "--vision",
		"--drafter", "z-lab/Qwen3.8-27B-DFlash2"}
	if !slices.Equal(argv, want) {
		t.Errorf("argv = %q", argv)
	}
	if !slices.Contains(env, "TENSORFOLD_API_KEY=k") {
		t.Error("no key in env")
	}
	if slices.Contains(argv, "k") {
		t.Error("key in argv")
	}

	argv, _ = tf.ServeCommand(engine.Config{Model: "TensorFold/Qwen3.8-27B-MLX-4bit", Port: 8001, NoVision: true, Draft: engine.DraftOff})
	if slices.Contains(argv, "--vision") || !slices.Contains(argv, "--no-drafts") || slices.Contains(argv, "--drafter") {
		t.Errorf("argv = %q", argv)
	}
	// "": the default drafter, the cached one.
	argv, _ = tf.ServeCommand(engine.Config{Model: "TensorFold/Qwen3.8-27B-MLX-4bit", Port: 8001})
	if i := slices.Index(argv, "--drafter"); i < 0 || argv[i+1] != "z-lab/Qwen3.8-27B-DFlash2" {
		t.Errorf("default draft: argv = %q", argv)
	}
	// A model that takes no drafter gets none.
	argv, _ = tf.ServeCommand(engine.Config{Model: fixtureRepo, Port: 8001})
	if slices.Contains(argv, "--drafter") || slices.Contains(argv, "--no-drafts") {
		t.Errorf("2B: argv = %q", argv)
	}
}

func TestStopPolicy(t *testing.T) {
	p := New().StopPolicy()
	if p.Graceful != syscall.SIGINT || p.Force != syscall.SIGINT || p.KillWait != 10*time.Second {
		t.Errorf("policy = %+v", p)
	}
}

func TestParseLineFixtures(t *testing.T) {
	tf := New()
	kinds := func(path string, stderr bool) []engine.EventKind {
		var out []engine.EventKind
		for _, l := range lines(t, path) {
			if ev, ok := tf.ParseLine(l, stderr); ok {
				out = append(out, ev.Kind)
			}
		}
		return out
	}
	// Start, a request, idle unload, the reloading request, SIGINT.
	got := kinds("testdata/lifecycle/serve.stdout", false)
	want := []engine.EventKind{engine.EventReady, engine.EventRequestDone, engine.EventReady, engine.EventRequestDone,
		engine.EventStopping, engine.EventStopping, engine.EventStopping}
	if !slices.Equal(got, want) {
		t.Errorf("serve.stdout: %v, want %v", got, want)
	}
	// "info: … kept wired" and the jpeg-turbo warning are no errors.
	for _, f := range []string{"testdata/lifecycle/serve.stderr", "testdata/lifecycle/stop_twice.stderr"} {
		if k := kinds(f, true); len(k) != 0 {
			t.Errorf("%s: %v", f, k)
		}
	}
	got = kinds("testdata/lifecycle/stop_twice.stdout", false)
	want = []engine.EventKind{engine.EventReady, engine.EventStopping, engine.EventStopping, engine.EventRequestDone, engine.EventStopping}
	if !slices.Equal(got, want) {
		t.Errorf("stop_twice.stdout: %v, want %v", got, want)
	}

	ev, ok := tf.ParseLine("[tensorfold] serving Qwen3.5-2B-MLX-4bit at http://127.0.0.1:18080/v1 (sampling: greedy; loaded in 1.8s)", false)
	if !ok || ev.Kind != engine.EventReady || ev.Model != "Qwen3.5-2B-MLX-4bit" {
		t.Errorf("serving: %+v", ev)
	}
	ev, ok = tf.ParseLine("tensorfold: nobody/X is not in the Hugging Face cache; the native engine does not download", true)
	if !ok || ev.Kind != engine.EventError {
		t.Errorf("error: %+v %v", ev, ok)
	}
	if _, ok := tf.ParseLine(`[tensorfold] 127.0.0.1 "GET /health HTTP/1.1" 200 -`, false); ok {
		t.Error("an access-log line is an event")
	}
}

func TestClassifyFailureFixtures(t *testing.T) {
	tf := New()
	for _, c := range []struct {
		name string
		code int
		kind engine.FailureKind
		line string
	}{
		{"failures/port_busy", 1, engine.FailPortBusy, "tensorfold: cannot listen on 127.0.0.1:18090 (AddressInUse)"},
		{"failures/no_model", 1, engine.FailModel, "tensorfold: nobody/NoSuchModel-MLX-4bit is not in the Hugging Face cache"},
		{"failures/offline_missing", 1, engine.FailModel, "tensorfold: mlx-community/Qwen3.5-0.8B-MLX-4bit is not in the Hugging Face cache"},
		{"failures/bad_drafter", 1, engine.FailModel, "tensorfold: nobody/NoDrafter is not in the Hugging Face cache"},
		{"failures/huge_context", 1, engine.FailExit, "tensorfold: --context must fit"},
		{"failures/bad_port", 2, engine.FailExit, "tensorfold serve: error: argument --port: invalid port"},
		{"failures/no_memory", 1, engine.FailMemory, "tensorfold: --parallel 1 needs 13.0 GiB"},
		{"lifecycle/vision", 1, engine.FailNoVision, "tensorfold: --vision: the native engine reads images for"},
	} {
		tail := append(lines(t, "testdata/"+c.name+".stdout"), lines(t, "testdata/"+c.name+".stderr")...)
		f := tf.ClassifyFailure(c.code, tail)
		if f.Kind != c.kind || f.Code != c.code || !strings.HasPrefix(f.Line, c.line) {
			t.Errorf("%s: %+v", c.name, f)
		}
	}
	if f := tf.ClassifyFailure(0, []string{"tensorfold: x"}); f.Kind != engine.FailNone {
		t.Errorf("exit 0: %+v", f)
	}
	if f := tf.ClassifyFailure(1, []string{"tensorfold: engine open failed: OutOfMemory"}); f.Kind != engine.FailMemory {
		t.Errorf("memory: %+v", f)
	}
	if f := tf.ClassifyFailure(134, []string{"something", "Abort trap"}); f.Kind != engine.FailExit || f.Line != "Abort trap" {
		t.Errorf("crash: %+v", f)
	}
}

func TestParseModel(t *testing.T) {
	tf := New()
	m, err := tf.ParseModel(" " + fixtureRepo + " ")
	if err != nil || m.ID != fixtureRepo || m.Owner != "mlx-community" || m.Name != "Qwen3.5-2B-MLX-4bit" || m.Variant != "" {
		t.Errorf("%+v %v", m, err)
	}
	for _, bad := range []string{"", "repo", "a/b/c", "a/b:Q4", "a/b@main", "/b", "a/"} {
		if _, err := tf.ParseModel(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSuggestionsAndSearch(t *testing.T) {
	info := New().Info()
	if len(info.Suggestions) != 1 || info.Suggestions[0].ID != "TensorFold/Qwen3.8-27B-MLX-4bit" {
		t.Errorf("suggestions = %+v", info.Suggestions)
	}
	for _, s := range info.Suggestions {
		if _, err := New().ParseModel(s.ID); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
	}
	s := info.ModelSearch
	if len(s.Sources) != 1 || s.Sources[0].Filter != "mlx" || !slices.Contains(s.Sources[0].Architectures, "qwen3_5") ||
		slices.Contains(s.Sources[0].Architectures, "lfm3") {
		t.Errorf("search = %+v", s)
	}
	if s.MLX == nil || !slices.Equal(s.MLX.Bits, []int{2, 3, 4, 5, 6, 8}) || !slices.Equal(s.MLX.Groups, []int{32, 64, 128}) {
		t.Errorf("mlx = %+v", s.MLX)
	}
}

// An agent gets the model by the name the server serves it by, its image
// input and the context a probe read; a server Lumilo does not know gives
// no command.
func TestAgentCommand(t *testing.T) {
	dir := t.TempDir()
	putRepo(t, dir, "TensorFold/Qwen3.8-27B-MLX-4bit", "c27",
		hubFile{"config.json", config(t, "qwen3.8-27b-mlx-4bit")}, hubFile{"model.safetensors", "1"})
	tf := newTest(t, dir)
	if argv, _ := tf.AgentCommand("claude", engine.Target{Port: 8000}); argv != nil {
		t.Errorf("unknown server: %q", argv)
	}
	tf.ServeCommand(engine.Config{Model: "TensorFold/Qwen3.8-27B-MLX-4bit", Port: 8000})
	tf.mu.Lock()
	tf.memo(8000).context = 65536 // what /memory reported
	tf.mu.Unlock()
	argv, env := tf.AgentCommand("claude", engine.Target{Port: 8000, APIKey: "k"})
	if !slices.Contains(argv, "Qwen3.8-27B-MLX-4bit") || !slices.Contains(env, "ANTHROPIC_AUTH_TOKEN=k") ||
		!slices.Contains(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=65536") {
		t.Errorf("claude: %q %q", argv, env)
	}
	_, env = tf.AgentCommand("opencode", engine.Target{Port: 8000})
	if !strings.Contains(strings.Join(env, " "), `"input":["text","image"]`) {
		t.Errorf("opencode: no image input in %q", env)
	}
}

// The estimate agrees with the server's own plan on 48 GiB (caps): with
// the drafter, 131K without images fits, 131K with images does not, 32K
// with images fits.
func TestMemoryPlan(t *testing.T) {
	mc := caps().Memory
	budget := engine.Budget(48<<30, 0, mc.Share)
	md := engine.Model{SizeBytes: 16_054_541_349, ContextLength: 262144, KVElements: 32768,
		Features: engine.Features{Vision: true, VisionBytes: visionBytes,
			Drafts: []engine.Draft{{Kind: engine.DraftDFlash, ID: "z-lab/Qwen3.8-27B-DFlash2", SizeBytes: drafterMemory(3_848_817_896), Default: true}}}}
	for _, c := range []struct {
		ctx      int
		noVision bool
		want     engine.Fit
	}{
		{131072, true, engine.Fits},
		{131072, false, engine.FitsNot},
		{32768, false, engine.Fits},
	} {
		need, fit := engine.Judge(md, engine.Config{ContextTokens: c.ctx, NoVision: c.noVision}, mc, budget)
		if fit != c.want {
			t.Errorf("ctx %d, no vision %v: %v (%.1f GB of %.1f GB)", c.ctx, c.noVision, fit, float64(need.Total())/1e9, float64(budget)/1e9)
		}
	}
}

// The fixture's JPEG decoder is Homebrew's 3.2.1, not the 3.1.4.1 the
// server needs: JPEG images are refused, and a vision model says so.
func TestCapabilitiesNoJPEG(t *testing.T) {
	caps := []byte(readFile(t, "testdata/capabilities.json"))
	if !capabilitiesNoJPEG(caps) {
		t.Error("JPEG usable")
	}
	if capabilitiesNoJPEG([]byte(`{"optional_libraries": [{"name": "libjpeg-turbo", "usable": true}]}`)) ||
		capabilitiesNoJPEG([]byte(`{}`)) {
		t.Error("JPEG refused without a reason")
	}
}
