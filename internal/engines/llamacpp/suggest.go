package llamacpp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// The Hugging Face name syntax of -hf ("-hf, -hfr, --hf-repo <user>/<model>[:quant]",
// help): repository parts as the hub allows them, a quant tag without "/".
var (
	hfPartRe  = regexp.MustCompile(`^[A-Za-z0-9_](?:[A-Za-z0-9._-]*[A-Za-z0-9_])?$`)
	hfQuantRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ParseModel checks "owner/repo[:quant]", what -hf takes. Without a quant
// llama-server picks Q4_K_M, or the first GGUF of the repository (help).
func (l *LlamaCpp) ParseModel(spec string) (engine.Model, error) {
	spec = strings.TrimSpace(spec)
	repoID, quant, hasQuant := strings.Cut(spec, ":")
	owner, repo, ok := strings.Cut(repoID, "/")
	if !ok || !hfPartRe.MatchString(owner) || !hfPartRe.MatchString(repo) ||
		(hasQuant && !hfQuantRe.MatchString(quant)) {
		return engine.Model{}, fmt.Errorf("llama.cpp: %q is not OWNER/REPO[:QUANT]", spec)
	}
	return engine.Model{ID: spec, Name: repo, Owner: owner, Variant: quant}, nil
}

// suggestions are 4-bit GGUF repositories, in the order the user chose.
// Each tag resolves on the hub (GET huggingface.co/v2/<repo>/manifests/<tag>,
// the route -hf asks, answered 200 with the GGUF layer on 2026-10-27);
// sizes are from api/models/<repo>?blobs=true. -hf also fetches the
// repository's mmproj, about 0.9 GB for the Qwen models.
func suggestions() []engine.Suggestion {
	return []engine.Suggestion{
		{
			ID: "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", Name: "Qwen3.8-27B",
			Maker: "Qwen", MakerLogo: enginekit.QwenLogo,
			Params: 27_000_000_000,
			// 16.46 GB GGUF + 0.93 GB mmproj-F16.
			DownloadBytes: 17_390_000_000, MemoryBytes: 17_390_000_000,
		},
		{
			ID: "unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M", Name: "Qwen3.6-35B-A3B",
			Maker: "Qwen", MakerLogo: enginekit.QwenLogo,
			Params: 36_000_000_000, Active: 3_000_000_000,
			// 22.13 GB GGUF + 0.90 GB mmproj-F16.
			DownloadBytes: 23_030_000_000, MemoryBytes: 23_030_000_000,
		},
	}
}

// modelSearch is what -hf takes: GGUF repositories. llama.cpp runs many
// architectures and the hub does not say which this build knows, so none is
// filtered out.
//
// llama.cpp loads every GGUF type of ggml 0.27 ("llama-quantize --help"
// lists Q1_0 to BF16, TQ1_0 and TQ2_0 among them); Prism ML's PQ2_0, a type
// of Splash's (install/gguf.py TENSOR_TYPES 142), is not one of ggml's.
func modelSearch() engine.SearchSpec {
	return engine.SearchSpec{Sources: []engine.SearchSource{{Filter: "gguf"}}, RefuseGGUF: []string{"PQ2_0"}}
}
