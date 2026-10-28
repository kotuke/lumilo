package llamacpp

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

func TestParseModel(t *testing.T) {
	e := New()
	m, err := e.ParseModel("  unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M ")
	if err != nil || !reflect.DeepEqual(m, engine.Model{ID: "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", Name: "Qwen3.8-27B-GGUF",
		Owner: "unsloth", Variant: "UD-Q4_K_M"}) {
		t.Errorf("got %+v, %v", m, err)
	}
	if m, err := e.ParseModel("ggml-org/Qwen3-0.6B-GGUF"); err != nil || m.Variant != "" || m.Name != "Qwen3-0.6B-GGUF" {
		t.Errorf("no quant: %+v, %v", m, err)
	}
	for _, bad := range []string{"", "qwen", "/repo", "owner/", "a/b/c", "a/b:", "a/b:x/y", "a b/c", "/abs/model.gguf"} {
		if _, err := e.ParseModel(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A model to download goes to -hf, without --offline when updates are on,
// and the context to -c.
func TestServeCommandDownloadAndContext(t *testing.T) {
	e := New()
	argv, _ := e.ServeCommand(engine.Config{Model: "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", Port: 18300,
		ModelUpdates: true, ContextTokens: 65536})
	want := []string{e.binPath(), "-hf", "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", "--host", "127.0.0.1", "--port", "18300",
		"--metrics", "-c", "65536", "--cors-origins", "localhost"}
	if !slices.Equal(argv, want) {
		t.Errorf("argv\n got %q\nwant %q", argv, want)
	}
	argv, _ = e.ServeCommand(engine.Config{Model: "/m/x.gguf", Port: 18300})
	if slices.Contains(argv, "-c") || !slices.Contains(argv, "--offline") || argv[1] != "-m" {
		t.Errorf("auto context, offline: %q", argv)
	}
}

func TestSuggestions(t *testing.T) {
	e := New()
	got := e.Info().Suggestions
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
		if _, err := e.ParseModel(s.ID); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
		if !strings.HasPrefix(string(s.MakerLogo), "<svg") || s.Params == 0 || s.DownloadBytes == 0 || s.MemoryBytes == 0 {
			t.Errorf("%+v", s)
		}
	}
	if want := []string{"Qwen3.8-27B", "Qwen3.6-35B-A3B"}; !slices.Equal(names, want) {
		t.Errorf("order %q", names)
	}
}

func TestModelSearch(t *testing.T) {
	spec := New().Info().ModelSearch
	if len(spec.Sources) != 1 || spec.Sources[0].Filter != "gguf" || len(spec.Sources[0].Architectures) != 0 {
		t.Errorf("%+v", spec)
	}
}
