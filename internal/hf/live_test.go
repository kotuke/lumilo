//go:build live

package hf

import (
	"context"
	"testing"
	"time"
)

// Run with: go test -tags live ./internal/hf -run Live -v
func TestLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var c Client
	repos, err := c.Search(ctx, Query{Text: "qwen3.8", Sources: []Source{
		{Filter: FilterGGUF, Architectures: []string{"qwen35", "qwen35moe", "qwen4exp", "flint"}},
		{Filter: "mlx", Architectures: []string{"qwen3_5", "qwen3_5_moe", "qwen4_exp", "flint"}},
	}})
	if err != nil || len(repos) == 0 {
		t.Fatalf("%v %v", repos, err)
	}
	for _, r := range repos {
		t.Logf("%+v", r)
	}
	vs, err := c.Variants(ctx, "unsloth/Qwen3.8-27B-GGUF")
	if err != nil || len(vs) == 0 {
		t.Fatalf("%v %v", vs, err)
	}
	t.Logf("%+v", vs[:3])
	for _, repo := range []string{"orcarouter/OrcaCyber-Nano-1.0", "lmstudio-community/Qwen3.8-27B-MLX-4bit"} {
		info, err := c.Repo(ctx, repo, Rules{RefuseGGUF: []string{"BF16", "F16", "F32"},
			MLX: &MLXRules{Bits: []int{4}, Groups: []int{64}}})
		if err != nil || (len(info.Variants) == 0 && info.Weights == 0) {
			t.Fatalf("%s: %+v %v", repo, info, err)
		}
		t.Logf("%+v", info)
	}
}
