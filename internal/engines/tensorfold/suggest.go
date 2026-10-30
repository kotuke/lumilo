package tensorfold

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/makers"
)

// hfPartRe is a repository part as the hub allows it.
var hfPartRe = regexp.MustCompile(`^[A-Za-z0-9_](?:[A-Za-z0-9._-]*[A-Za-z0-9_])?$`)

// ParseModel checks "owner/repo", what "tensorfold serve" and Download
// take. A checkpoint has no variants: one repository is one quantization.
// "@revision" pins a "tensorfold pull" (README), but serve takes the name
// without it, so it is refused here.
func (tf *TensorFold) ParseModel(spec string) (engine.Model, error) {
	spec = strings.TrimSpace(spec)
	owner, repo, ok := strings.Cut(spec, "/")
	if !ok || !hfPartRe.MatchString(owner) || !hfPartRe.MatchString(repo) {
		return engine.Model{}, fmt.Errorf("tensorfold: %q is not OWNER/REPO", spec)
	}
	return engine.Model{ID: spec, Name: repo, Owner: owner}, nil
}

// suggestions: Qwen3.8-27B in TensorFold's own 4-bit MLX checkpoint, the
// README's model for one Mac, which Download fetches with its DFlash2
// drafter. Sizes are from the Hugging Face API
// (api/models/<repo>/revision/main?blobs=true, 2026-10-29): the model 16.08
// GB (16.05 GB of safetensors), the drafter 3.85 GB of bf16, 1.73 GB in
// memory (drafterMemory).
func suggestions() []engine.Suggestion {
	return []engine.Suggestion{
		{
			ID: "TensorFold/Qwen3.8-27B-MLX-4bit", Name: "Qwen3.8-27B",
			Maker: "Qwen", MakerLogo: makers.Qwen,
			Params:        27_000_000_000,
			DownloadBytes: 19_930_000_000, MemoryBytes: 17_780_000_000,
		},
	}
}

// modelSearch: MLX checkpoints of the families the engine serves
// (families.go), with the affine quantizations some family reads; the
// family's own list is checked by Models and Download.
func modelSearch() engine.SearchSpec {
	var archs []string
	bits, groups := map[int]bool{}, map[int]bool{}
	for arch, formats := range families {
		archs = append(archs, arch)
		for _, f := range formats {
			var b, g int
			if _, err := fmt.Sscanf(f, "mlx-q%dg%d", &b, &g); err == nil {
				bits[b], groups[g] = true, true
			}
		}
	}
	sort.Strings(archs)
	return engine.SearchSpec{
		Sources: []engine.SearchSource{{Filter: "mlx", Architectures: archs}},
		MLX:     &engine.MLXQuants{Bits: sortedKeys(bits), Groups: sortedKeys(groups)},
	}
}

func sortedKeys(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
