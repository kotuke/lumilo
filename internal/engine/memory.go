package engine

// MemoryCaps are the numbers of an engine that Estimate needs.
type MemoryCaps struct {
	// KVBytesPerElement is what one K or V element of the KV cache takes:
	// 2 for llama.cpp's default f16 cache, 1 + 4/256 for Splash's int8 cache
	// with an f32 scale per 256 elements.
	KVBytesPerElement float64
	// Reserve is what the engine needs beyond the weights and the KV cache:
	// compute buffers, batch state, pipelines.
	Reserve int64
	// ShrinksContext: with ContextTokens 0 the engine serves a shorter
	// context than the native window when memory does not hold it (Splash's
	// memory plan, llama.cpp's --fit).
	ShrinksContext bool
	// DefaultDraft is what the draft the engine always runs takes when its
	// size is not known yet (a model still to download, whose Features say
	// DraftsUnknown); 0 = no such draft. Estimate counts it.
	DefaultDraft int64
}

// MinFitContext is the context Estimate judges a model by when the engine
// shrinks the context to fit: a model that does not hold this much is of
// no use for coding agents.
const MinFitContext = 32 * 1024

// Need is what a model takes in memory, by part.
type Need struct {
	Weights int64 // the model's own weights (Model.SizeBytes)
	Vision  int64 // the vision projector, when loaded
	Draft   int64 // the speculative decoding draft, when one runs
	KV      int64 // the KV cache for Context tokens
	Reserve int64 // the engine's buffers (MemoryCaps.Reserve)
	Context int   // the context the KV cache is sized for; 0 = unknown
}

// Total is everything together.
func (n Need) Total() int64 { return n.Weights + n.Vision + n.Draft + n.KV + n.Reserve }

// ActiveDraft is the draft cfg runs with m: the chosen one, else one the
// engine always runs; nil for none.
func ActiveDraft(m Model, draft string) *Draft {
	for i, d := range m.Features.Drafts {
		if d.Always || (draft != "" && draft != DraftOff && d.ID == draft) {
			return &m.Features.Drafts[i]
		}
	}
	return nil
}

// Estimate is what m takes with the options of cfg: its weights, its vision
// projector unless NoVision, its draft (the engine's default one when the
// drafts are not known yet), the KV cache of the context
// (ContextTokens, else the native window) and the engine's reserve.
//
// The KV cache is KVElements (attention layers × KV heads × (key + value
// length)) × bytes per element × tokens. Layers of linear attention keep a
// fixed state instead and count as none. For OrcaCyber-Nano-1.0 under
// Splash: 16 × 4 × 512 × (1 + 4/256) = 33,280 bytes per token, the
// kv_page_bytes / kv_page_tokens of its /status (1,064,960 / 32).
func Estimate(m Model, cfg Config, mc MemoryCaps) Need {
	n := Need{Weights: m.SizeBytes, Reserve: mc.Reserve, Context: cfg.ContextTokens}
	if n.Context <= 0 {
		n.Context = m.ContextLength
	}
	if m.Features.Vision && !cfg.NoVision {
		n.Vision = m.Features.VisionBytes
	}
	if d := ActiveDraft(m, cfg.Draft); d != nil {
		n.Draft = d.SizeBytes
	} else if m.Features.DraftsUnknown {
		// The engine runs its draft whatever cfg.Draft says.
		n.Draft = mc.DefaultDraft
	}
	if m.KVElements > 0 && n.Context > 0 {
		n.KV = int64(float64(m.KVElements) * mc.KVBytesPerElement * float64(n.Context))
	}
	return n
}

// Budget is the memory the engine may use: what it reports (Snapshot.
// MemoryLimit), else three quarters of the computer's memory, about what
// Metal recommends as the GPU's working set on Apple silicon
// (recommendedMaxWorkingSetSize: 40.2 GB of 48 GiB, Splash's /status).
func Budget(ram, reported int64) int64 {
	if reported > 0 {
		return reported
	}
	return ram / 4 * 3
}

// Fit is how a model fits in a budget.
type Fit int

const (
	FitUnknown  Fit = iota // no budget or no size known
	Fits                   // it fits with the context asked for
	FitsShorter            // it fits only with a shorter context, which the engine chooses
	FitsNot                // it does not fit
)

// Judge tells how m fits in budget with cfg: the need at the context asked
// for, and whether it fits. With ContextTokens 0 an engine that shrinks the
// context fits a model that holds MinFitContext tokens.
func Judge(m Model, cfg Config, mc MemoryCaps, budget int64) (Need, Fit) {
	need := Estimate(m, cfg, mc)
	switch {
	case budget <= 0 || need.Weights <= 0:
		return need, FitUnknown
	case need.Total() <= budget:
		return need, Fits
	case cfg.ContextTokens <= 0 && mc.ShrinksContext:
		small := cfg
		small.ContextTokens = min(MinFitContext, max(m.ContextLength, 0))
		if small.ContextTokens == 0 {
			small.ContextTokens = MinFitContext
		}
		if Estimate(m, small, mc).Total() <= budget {
			return need, FitsShorter
		}
	}
	return need, FitsNot
}
