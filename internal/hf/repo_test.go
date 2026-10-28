package hf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Recorded on 2026-10-27 besides the files of hf_test.go:
//
//	tree-ggml-org-qwen3.8-27b.json, tree-orcarouter-orcacyber-nano-1.0.json,
//	tree-lmstudio-qwen3.8-27b-mlx-4bit.json   /api/models/<repo>/tree/main?recursive=true
//	header-*.gguf   the first bytes of a GGUF (a ranged GET of
//	                /<repo>/resolve/main/<file>), cut after the name of the
//	                first tokenizer key
//	config-*.json   /<repo>/resolve/main/config.json

// repoHub serves trees, headers and configs by path.
func repoHub(t *testing.T, routes map[string]string) (*Client, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path+" "+r.Header.Get("Range"))
		file, ok := routes[r.URL.Path]
		if !ok {
			http.Error(w, "no route", http.StatusNotFound)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Error(err)
		}
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}, &asked
}

// Splash's refusals (install/gguf.py QUANTIZED_TYPES, UNQUANTIZED_TYPES).
var splashRules = Rules{
	RefuseGGUF: []string{"BF16", "F16", "F32", "TQ1_0", "TQ2_0", "Q1_0"},
	MLX:        &MLXRules{Bits: []int{2, 3, 4, 5, 6, 8}, Groups: []int{32, 64, 128}, Modes: []string{"mxfp4", "nvfp4"}},
}

func TestRepoGGUF(t *testing.T) {
	c, asked := repoHub(t, map[string]string{
		"/api/models/unsloth/Qwen3.8-27B-GGUF/tree/main":                    "tree-unsloth-qwen3.8-27b.json",
		"/unsloth/Qwen3.8-27B-GGUF/resolve/main/Qwen3.8-27B-UD-Q4_K_M.gguf": "header-unsloth-qwen3.8-27b-ud-q4_k_m.gguf",
	})
	info, err := c.Repo(context.Background(), "unsloth/Qwen3.8-27B-GGUF", splashRules)
	if err != nil {
		t.Fatal(err)
	}
	tags := tagsOfVariants(info.Variants)
	// BF16 (a split in BF16/) is what Splash refuses: "Splash loads
	// quantized GGUFs, not whole-BF16 or F16 models".
	if slices.Contains(tags, "BF16") || info.Refused != 1 || len(info.Variants) != 24 || tags[0] != "UD-Q4_K_M" {
		t.Errorf("tags %v, refused %d", tags, info.Refused)
	}
	// The header of the first variant: 16 attention layers of 4 KV heads
	// of 256 (Splash's /status for the same family), one MTP layer.
	if !info.GGUF || info.Context != 262144 || info.KVElements != 32768 || info.NextN != 1 {
		t.Errorf("info %+v", info)
	}
	if info.Vision != 931146432 { // mmproj-BF16.gguf before mmproj-F16.gguf
		t.Errorf("vision %d", info.Vision)
	}
	if len(*asked) != 2 || !strings.HasSuffix((*asked)[1], "bytes=0-524287") {
		t.Errorf("requests %q", *asked)
	}
	// llama.cpp loads them all.
	if info, _ := c.Repo(context.Background(), "unsloth/Qwen3.8-27B-GGUF", Rules{RefuseGGUF: []string{"PQ2_0"}}); len(info.Variants) != 25 {
		t.Errorf("llama.cpp: %d variants", len(info.Variants))
	}
}

// TestRepoDrafts: ggml-org's repository carries the DFlash draft and the MTP
// head for llama.cpp beside the models; OrcaRouter's names its draft after
// the model. Neither is a variant.
func TestRepoDrafts(t *testing.T) {
	c, _ := repoHub(t, map[string]string{
		"/api/models/ggml-org/Qwen3.8-27B-GGUF/tree/main":                                        "tree-ggml-org-qwen3.8-27b.json",
		"/api/models/orcarouter/OrcaCyber-Nano-1.0/tree/main":                                    "tree-orcarouter-orcacyber-nano-1.0.json",
		"/orcarouter/OrcaCyber-Nano-1.0/resolve/main/OrcaCyber-Nano-1.0-AI-Defense-Mix-13G.gguf": "header-orcacyber-nano-1.0-mix-15.7g.gguf",
	})
	info, err := c.Repo(context.Background(), "ggml-org/Qwen3.8-27B-GGUF", Rules{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tagsOfVariants(info.Variants); !slices.Equal(got, []string{"Q4_K_M", "Q8_0", "BF16"}) {
		t.Errorf("tags %v", got)
	}
	if info.DFlashFile != "dflash-Qwen3.8-27B-Q4_0.gguf" || info.DFlash != 1094346080 {
		t.Errorf("dflash %q %d", info.DFlashFile, info.DFlash)
	}
	info, err = c.Repo(context.Background(), "orcarouter/OrcaCyber-Nano-1.0", splashRules)
	if err != nil {
		t.Fatal(err)
	}
	// The tags past the shared words: the one Splash was given.
	if got := tagsOfVariants(info.Variants); !slices.Equal(got, []string{"Mix-13G", "Mix-15.7G"}) || info.KVElements != 32768 {
		t.Errorf("tags %v, %+v", got, info)
	}
	// The repository is gated: its files answer 401 without a token. The
	// tags keep a word before a number all the same; the KV cache is
	// unknown.
	gated, _ := repoHub(t, map[string]string{
		"/api/models/orcarouter/OrcaCyber-Nano-1.0/tree/main": "tree-orcarouter-orcacyber-nano-1.0.json",
	})
	info, err = gated.Repo(context.Background(), "orcarouter/OrcaCyber-Nano-1.0", splashRules)
	if got := tagsOfVariants(info.Variants); err != nil || !slices.Equal(got, []string{"Mix-13G", "Mix-15.7G"}) || info.KVElements != 0 {
		t.Errorf("gated: tags %v, %+v, %v", got, info, err)
	}
}

func TestRepoMLX(t *testing.T) {
	c, _ := repoHub(t, map[string]string{
		"/api/models/a/four/tree/main":     "tree-lmstudio-qwen3.8-27b-mlx-4bit.json",
		"/a/four/resolve/main/config.json": "config-lmstudio-qwen3.8-27b-mlx-4bit.json",
		"/api/models/a/fp8/tree/main":      "tree-lmstudio-qwen3.8-27b-mlx-4bit.json",
		"/a/fp8/resolve/main/config.json":  "config-unhinged-qwen3.8-27b-mlx-mxfp8.json",
		"/api/models/a/bf16/tree/main":     "tree-lmstudio-qwen3.8-27b-mlx-4bit.json",
		"/a/bf16/resolve/main/config.json": "config-mlx-community-qwen3.8-27b-bf16.json",
	})
	for repo, want := range map[string]struct {
		quant string
		bad   bool
	}{
		"a/four": {"affine 4-bit g64", false},
		"a/fp8":  {"mxfp8", true},
		"a/bf16": {"none", true},
	} {
		info, err := c.Repo(context.Background(), repo, splashRules)
		if err != nil {
			t.Fatal(repo, err)
		}
		if info.GGUF || info.Quant != want.quant || info.Unsupported != want.bad || info.Weights == 0 {
			t.Errorf("%s: %+v", repo, info)
		}
		if repo == "a/four" && (info.KVElements != 32768 || info.Context != 262144) {
			t.Errorf("%s: shape %+v", repo, info)
		}
		// llama.cpp has no MLX rules: nothing is refused.
		if info, _ := c.Repo(context.Background(), repo, Rules{}); info.Unsupported {
			t.Errorf("%s refused without rules", repo)
		}
	}
}

func TestQuantOf(t *testing.T) {
	for tag, want := range map[string]string{
		"UD-Q4_K_M": "Q4_K", "Q4_K_S": "Q4_K", "UD-Q3_K_XL": "Q3_K", "Q8_0": "Q8_0", "Q4_0": "Q4_0",
		"IQ2_XXS": "IQ2_XXS", "IQ3_M": "IQ3_S", "UD-IQ2_M": "IQ2_S", "IQ4_NL": "IQ4_NL", "MXFP4_MOE": "MXFP4",
		"BF16": "BF16", "F16": "F16", "TQ1_0": "TQ1_0", "PQ2_0": "PQ2_0", "Mix-15.7G": "", "GSQ-RCO-IQ2_S-mtp": "IQ2_S",
		"IQ2_XXS-2.56bpw": "IQ2_XXS", "MTP-Q4_K_P": "Q4_K", "NVFP4": "NVFP4",
	} {
		if got := QuantOf(tag); got != want {
			t.Errorf("QuantOf(%q) = %q, want %q", tag, got, want)
		}
	}
}
