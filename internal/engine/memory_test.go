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

// A default draft runs with "" and stops with DraftOff; another draft of
// the model is chosen by its ID.
func TestActiveDraftDefault(t *testing.T) {
	m := Model{Features: Features{Drafts: []Draft{{ID: "a", SizeBytes: 7, Default: true}, {ID: "b", SizeBytes: 9}}}}
	for draft, want := range map[string]string{"": "a", DraftOff: "", "a": "a", "b": "b", "x": ""} {
		got := ""
		if d := ActiveDraft(m, draft); d != nil {
			got = d.ID
		}
		if got != want {
			t.Errorf("%q: %q, want %q", draft, got, want)
		}
	}
	if n := Estimate(Model{SizeBytes: 10, Features: m.Features}, Config{}, MemoryCaps{}); n.Draft != 7 {
		t.Errorf("default draft not counted: %+v", n)
	}
}

// The budget is the engine's report, else its share of the memory, else
// three quarters.
func TestBudget(t *testing.T) {
	var ram int64 = 48 << 30
	if b := Budget(ram, 30e9, 0.7); b != 30e9 {
		t.Errorf("reported: %d", b)
	}
	if b := Budget(ram, 0, 0.7); b != int64(float64(ram)*0.7) {
		t.Errorf("share: %d", b)
	}
	if b := Budget(ram, 0, 0); b != ram/4*3 {
		t.Errorf("default: %d", b)
	}
}
