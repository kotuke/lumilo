package engine

import "testing"

// The draft an engine always runs counts in the estimate: the model's own
// when its drafts are known, the engine's default size when they are not
// (a repository still to download), and nothing for a model known to have
// none.
func TestEstimateDraft(t *testing.T) {
	mc := MemoryCaps{Reserve: 100, DefaultDraft: 1_300}
	known := Model{SizeBytes: 10_000, Features: Features{Drafts: []Draft{{ID: "d", SizeBytes: 900, Always: true}}}}
	unknown := Model{SizeBytes: 10_000, Features: Features{DraftsUnknown: true}}
	none := Model{SizeBytes: 10_000}
	for _, c := range []struct {
		name  string
		m     Model
		cfg   Config
		mc    MemoryCaps
		draft int64
	}{
		{"known", known, Config{}, mc, 900},
		{"unknown", unknown, Config{}, mc, 1_300},
		{"unknown, off asked", unknown, Config{Draft: DraftOff}, mc, 1_300}, // the engine runs it anyway
		{"none", none, Config{}, mc, 0},
		{"unknown, engine without one", unknown, Config{}, MemoryCaps{Reserve: 100}, 0},
	} {
		n := Estimate(c.m, c.cfg, c.mc)
		if n.Draft != c.draft || n.Total() != 10_000+c.mc.Reserve+c.draft {
			t.Errorf("%s: %+v", c.name, n)
		}
	}
	// Judge counts it too: 10,000 + 100 + 1,300 does not fit in 11,000.
	if _, fit := Judge(unknown, Config{}, mc, 11_000); fit != FitsNot {
		t.Errorf("unknown draft fits: %v", fit)
	}
	if _, fit := Judge(none, Config{}, mc, 11_000); fit != Fits {
		t.Errorf("no draft does not fit: %v", fit)
	}
}
