package splash

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

func TestParseModel(t *testing.T) {
	e := New()
	m, err := e.ParseModel("orcarouter/OrcaCyber-Nano-1.0:Mix-15.7G")
	if err != nil || !reflect.DeepEqual(m, engine.Model{ID: "orcarouter/OrcaCyber-Nano-1.0:Mix-15.7G", Name: "OrcaCyber-Nano-1.0",
		Owner: "orcarouter", Variant: "Mix-15.7G"}) {
		t.Errorf("got %+v, %v", m, err)
	}
	for _, bad := range []string{"", "repo", "a/b/c", "a/b:", "a/b:-x", "-a/b", "a/b:x:y"} {
		if _, err := e.ParseModel(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestContextArg(t *testing.T) {
	e := New()
	for n, want := range map[int]string{32768: "32K", 262144: "256K", 100000: "100000", 2_000_000: "1010000"} {
		argv, _ := e.ServeCommand(engine.Config{Model: "a/b", Port: 18300, ContextTokens: n})
		i := slices.Index(argv, "--max-context")
		if i < 0 || argv[i+1] != want {
			t.Errorf("%d: %q", n, argv)
		}
	}
	argv, _ := e.ServeCommand(engine.Config{Model: "a/b", Port: 18300})
	if slices.Contains(argv, "--max-context") {
		t.Errorf("auto: %q", argv)
	}
}

func TestFetchingLine(t *testing.T) {
	ev, ok := New().ParseLine("Fetching 3 file(s), 21.24 GB, from unsloth/Qwen3.8-27B-GGUF@0123456789ab; cached files are reused.", false)
	if !ok || ev.Kind != engine.EventDownloading || ev.Bytes != 21_240_000_000 {
		t.Errorf("%+v %v", ev, ok)
	}
}

// The native window is max_position_embeddings of config.json beside
// model.json, in text_config or at the top.
func TestNativeContext(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"text": `{"text_config":{"max_position_embeddings":262144},"max_position_embeddings":4096}`,
		"top":  `{"max_position_embeddings":32768}`,
		"none": `{"model_type":"x"}`,
	} {
		_ = os.MkdirAll(filepath.Join(dir, name), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name, "config.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]int{"text": 262144, "top": 32768, "none": 0, "missing": 0} {
		if got := nativeContext(filepath.Join(dir, name)); got != want {
			t.Errorf("%s: %d", name, got)
		}
	}
}

func TestSuggestions(t *testing.T) {
	e := New()
	var names []string
	for _, s := range e.Info().Suggestions {
		names = append(names, s.Name)
		if _, err := e.ParseModel(s.ID); err != nil || !strings.HasPrefix(string(s.MakerLogo), "<svg") || s.DownloadBytes == 0 {
			t.Errorf("%+v: %v", s, err)
		}
	}
	// Ling 3.0 Flash is not a Splash family.
	if want := []string{"Qwen3.8-27B", "Qwen3.6-35B-A3B"}; !slices.Equal(names, want) {
		t.Errorf("order %q", names)
	}
}

// The search offers GGUF and MLX repositories of the architectures Splash
// runs, and the suggested models are among them.
func TestModelSearch(t *testing.T) {
	spec := New().Info().ModelSearch
	if len(spec.Sources) != 2 || spec.Sources[0].Filter != "gguf" || spec.Sources[1].Filter != "mlx" {
		t.Fatalf("%+v", spec)
	}
	for _, a := range []string{"qwen35", "qwen35moe"} { // Qwen3.8-27B, Qwen3.6-35B-A3B
		if !slices.Contains(spec.Sources[0].Architectures, a) {
			t.Errorf("%s not searched", a)
		}
	}
	if slices.Contains(spec.Sources[0].Architectures, "llama") {
		t.Error("llama is not a Splash family")
	}
}

// The GGUF architectures of the search are those of the installed Splash
// (install/gguf.py ARCHITECTURES), when it is installed.
func TestModelSearchMatchesInstalledSplash(t *testing.T) {
	src, err := os.ReadFile("/opt/homebrew/opt/splash/libexec/install/gguf.py")
	if err != nil {
		t.Skip("Splash is not installed")
	}
	_, table, ok := strings.Cut(string(src), "\nARCHITECTURES = {")
	if !ok {
		t.Skip("install/gguf.py has no ARCHITECTURES table")
	}
	table, _, _ = strings.Cut(table, "\n}\n")
	var want []string
	for _, m := range regexp.MustCompile(`(?m)^    "(\w+)": Architecture\(`).FindAllStringSubmatch(table, -1) {
		want = append(want, m[1])
	}
	got := slices.Clone(New().Info().ModelSearch.Sources[0].Architectures)
	slices.Sort(got)
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("searched %q, Splash runs %q", got, want)
	}
}
