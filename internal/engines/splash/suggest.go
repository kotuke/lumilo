package splash

import (
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// modelSearch is what "splash serve --model" takes: a GGUF repository, whose
// variant follows ":", or an MLX one (install/upstream.py inspect_target:
// ":VARIANT" or a repository without safetensors is a GGUF, else MLX). Splash
// runs the architectures of install/gguf.py ARCHITECTURES (the GGUF's
// general.architecture) and of install/families.py FAMILIES (the MLX
// config's model_type, the GGUF's with "_text" cut off).
//
// Variants: Splash loads quantized GGUFs of the types in install/gguf.py
// QUANTIZED_TYPES (lines 545-569: Q2_K…Q6_K, Q8_0, Q4_0/1, Q5_0/1, IQ1_S…
// IQ4_XS, MXFP4, NVFP4, Q2_0, PQ2_0) and refuses a whole BF16, F16 or F32
// model (require_loadable, lines 903-925: "Splash loads quantized GGUFs, not
// whole-BF16 or F16 models"); ggml's ternary TQ1_0/TQ2_0 and Q1_0 are not
// among its types. MLX checkpoints load "as affine 2, 3, 4, 5, 6 or 8 bits
// in groups of 32, 64 or 128 ..., or as mxfp4 or nvfp4", and unquantized
// ones are refused (strings of the native engine/splash 1.4.1).
func modelSearch() engine.SearchSpec {
	return engine.SearchSpec{
		Sources: []engine.SearchSource{
			{Filter: "gguf", Architectures: []string{"qwen35", "qwen35moe", "qwen4exp", "flint"}},
			{Filter: "mlx", Architectures: []string{"qwen3_5", "qwen3_5_text", "qwen3_5_moe", "qwen3_5_moe_text", "qwen4_exp", "qwen4_exp_text", "flint"}},
		},
		RefuseGGUF: []string{"BF16", "F16", "F32", "TQ1_0", "TQ2_0", "Q1_0"},
		MLX:        &engine.MLXQuants{Bits: []int{2, 3, 4, 5, 6, 8}, Groups: []int{32, 64, 128}, Modes: []string{"mxfp4", "nvfp4"}},
	}
}

// suggestions are the families Splash serves (install/families.py
// FAMILIES) that fit a Mac with 32 GB or more, as 4-bit GGUF variants of
// Unsloth's repositories; "splash serve --help" names :UD-Q4_K_M as its
// example variant. Ling 3.0 Flash is not offered: its architecture
// (bailingmoe3) is not a Splash family, and install/upstream.py refuses a
// model whose family has no DFlash2 draft.
//
// Sizes are from the Hugging Face API (api/models/<repo>?blobs=true,
// 2026-10-27). The download is the target GGUF, its BF16 or F16 vision
// projector (mmproj, about 0.9 GB) and the family's DFlash2 draft, which
// Splash also loads.
func suggestions() []engine.Suggestion {
	return []engine.Suggestion{
		{
			ID: "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", Name: "Qwen3.8-27B",
			Maker: "Qwen", MakerLogo: enginekit.QwenLogo,
			Params: 27_000_000_000,
			// 16.46 GB GGUF + 0.93 GB mmproj + 3.85 GB incoai/Qwen3.8-27B-DFlash2.
			DownloadBytes: 21_240_000_000, MemoryBytes: 21_240_000_000,
		},
		{
			ID: "unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M", Name: "Qwen3.6-35B-A3B",
			Maker: "Qwen", MakerLogo: enginekit.QwenLogo,
			Params: 36_000_000_000, Active: 3_000_000_000,
			// 22.13 GB GGUF + 0.90 GB mmproj + 1.05 GB incoai/Qwen3.6-35B-A3B-DFlash2.
			DownloadBytes: 24_080_000_000, MemoryBytes: 24_080_000_000,
		},
	}
}
