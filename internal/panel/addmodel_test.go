package panel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engines/splash"
	"github.com/kotuke/lumilo/internal/hf"
)

var sampleHits = []hf.Repo{
	{ID: "unsloth/Qwen3.8-27B-GGUF", Downloads: 6009562, Filter: "gguf", Likes: 5366},
	{ID: "ggml-org/Qwen3.8-27B-GGUF", Downloads: 2266925, Filter: "gguf"},
	{ID: "lmstudio-community/Qwen3.8-27B-MLX-4bit", Downloads: 3586440, Filter: "mlx"},
}

// The variants of unsloth/Qwen3.8-27B-GGUF, 4-bit first, as hf lists them.
var sampleVariants = []hf.Variant{
	{Tag: "UD-Q4_K_M", Bytes: 16_464_440_224, Parts: 1},
	{Tag: "UD-IQ4_XS", Bytes: 14_252_845_984, Parts: 1},
	{Tag: "UD-Q4_K_XL", Bytes: 17_559_178_144, Parts: 1},
	{Tag: "Q8_0", Bytes: 29_047_086_048, Parts: 1},
	{Tag: "BF16", Bytes: 54_657_735_616, Parts: 2},
}

// openAdd opens the "Add model…" field: under the suggestions when there is
// no model, else in the model list.
func (b *bench) openAdd(popup bool) {
	b.t.Helper()
	if popup {
		b.show(withPhase(core.PhaseStopped), 0)
		b.click("OrcaCyber-Nano-1.0")
	} else {
		b.show(noModels(withPhase(core.PhaseStopped)), 0)
	}
	b.click(b.m.AddModel())
	b.tt.Frame()
}

// typeText types s, as the user does.
func (b *bench) typeText(s string) {
	b.t.Helper()
	b.tt.Type(s)
	b.tt.Frame()
}

// searched makes the search settle: the pause passes and the app answers.
func (b *bench) searched(text string, hits []hf.Repo) {
	b.t.Helper()
	b.tt.Advance(searchDelay)
	b.called("SearchModels " + text)
	b.p.SetModelHits(text, hits)
	b.tt.Frame()
}

// variants makes the look at repo settle: the hub answers with the GGUF
// variants vs and a vision projector, as for a multimodal Qwen.
func (b *bench) variants(repo string, vs []hf.Variant) {
	b.t.Helper()
	b.p.SetModelInfo(repo, hf.RepoInfo{ID: repo, GGUF: len(vs) > 0, Variants: vs, Vision: 928_000_000})
}

func (b *bench) text() string { return b.p.addText }

func (b *bench) count(prefix string) int {
	n := 0
	for _, c := range b.rec.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestAddModelSearchWaitsForTheTypingToPause(t *testing.T) {
	for _, popup := range []bool{false, true} {
		b := newBench(t, "en")
		b.openAdd(popup)
		b.typeText("q")
		b.tt.Advance(time.Second)
		if n := b.count("SearchModels"); n != 0 {
			t.Fatalf("popup %v: a search for one letter: %q", popup, b.rec.calls)
		}
		b.typeText("w")
		b.tt.Advance(searchDelay - 10*time.Millisecond)
		b.notCalled("SearchModels qw")
		// More typing moves the pause on.
		b.typeText("e")
		b.tt.Advance(searchDelay - 10*time.Millisecond)
		b.notCalled("SearchModels qw", "SearchModels qwe")
		b.tt.Advance(10 * time.Millisecond)
		b.called("SearchModels qwe")
		if n := b.count("SearchModels"); n != 1 {
			t.Errorf("popup %v: %d searches: %q", popup, n, b.rec.calls)
		}
		// The view asks for no frames while it waits for the answer.
		if b.tt.FramePending() {
			t.Errorf("popup %v: frames keep coming", popup)
		}
		// The same text is not searched again; backspacing to two letters is.
		b.tt.Key(0, ui.KeyBackspace)
		b.tt.Advance(searchDelay)
		b.called("SearchModels qw")
		b.tt.Key(0, ui.KeyBackspace)
		b.tt.Advance(searchDelay)
		if b.count("SearchModels") != 2 || !b.rec.has("CancelModelSearch") {
			t.Errorf("popup %v: one letter should drop the search: %q", popup, b.rec.calls)
		}
	}
}

func TestAddModelResultsAndKeys(t *testing.T) {
	for _, popup := range []bool{false, true} {
		b := newBench(t, "en")
		b.openAdd(popup)
		b.typeText("qwen3.8")
		b.searched("qwen3.8", sampleHits)
		b.want("unsloth/Qwen3.8-27B-GGUF", "6,009,562 downloads", "ggml-org/Qwen3.8-27B-GGUF",
			"lmstudio-community/Qwen3.8-27B-MLX-4bit", "MLX · 3,586,440 downloads", "GGUF · 2,266,925 downloads")
		// Nothing is highlighted: Enter would download what is typed.
		if b.p.add.sel != -1 {
			t.Fatalf("popup %v: sel %d", popup, b.p.add.sel)
		}
		b.tt.Key(0, ui.KeyDown)
		b.tt.Key(0, ui.KeyDown)
		b.tt.Key(0, ui.KeyUp)
		if b.p.add.sel != 0 {
			t.Errorf("popup %v: after down, down, up: sel %d", popup, b.p.add.sel)
		}
		b.tt.Key(0, ui.KeyDown)
		b.tt.Key(0, ui.KeyDown)
		b.tt.Key(0, ui.KeyDown) // stops at the last row
		if b.p.add.sel != 2 {
			t.Errorf("popup %v: sel %d", popup, b.p.add.sel)
		}
		b.tt.Key(0, ui.KeyUp)
		b.tt.Key(0, ui.KeyUp)
		b.tt.Key(0, ui.KeyEnter)
		b.tt.Frame()
		// A GGUF repository gets ":" and lists its variants.
		if got := b.text(); got != "unsloth/Qwen3.8-27B-GGUF:" {
			t.Fatalf("popup %v: Enter put %q", popup, got)
		}
		b.called("ModelVariants unsloth/Qwen3.8-27B-GGUF")
		b.notCalled("Download unsloth/Qwen3.8-27B-GGUF")
		// The caret follows the text: the next letters come after the colon.
		b.typeText("UD")
		if got := b.text(); got != "unsloth/Qwen3.8-27B-GGUF:UD" {
			t.Errorf("popup %v: after typing %q", popup, got)
		}
	}
}

func TestAddModelTabTakesTheFirstRow(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("qwen3.8")
	b.searched("qwen3.8", sampleHits)
	b.tt.Key(0, ui.KeyTab)
	b.tt.Frame()
	if got := b.text(); got != "unsloth/Qwen3.8-27B-GGUF:" {
		t.Errorf("Tab put %q", got)
	}
	// With no list, Tab is not taken: the text stays.
	b.variants("unsloth/Qwen3.8-27B-GGUF", nil)
	b.tt.Frame()
	b.tt.Key(0, ui.KeyTab)
	if got := b.text(); got != "unsloth/Qwen3.8-27B-GGUF:" {
		t.Errorf("Tab without a list changed the text to %q", got)
	}
}

func TestAddModelClickTakesARow(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("qwen3.8")
	b.searched("qwen3.8", sampleHits)
	b.click("ggml-org/Qwen3.8-27B-GGUF")
	b.tt.Frame()
	if got := b.text(); got != "ggml-org/Qwen3.8-27B-GGUF:" {
		t.Fatalf("click put %q", got)
	}
	b.called("ModelVariants ggml-org/Qwen3.8-27B-GGUF")
	b.none("6,009,562 downloads")
	// The field keeps the focus: typing goes on.
	b.typeText("Q8")
	if got := b.text(); got != "ggml-org/Qwen3.8-27B-GGUF:Q8" {
		t.Errorf("typed after the click: %q", got)
	}
}

func TestAddModelEscapeClosesTheListFirst(t *testing.T) {
	for _, popup := range []bool{false, true} {
		b := newBench(t, "en")
		b.openAdd(popup)
		b.typeText("qwen3.8")
		b.searched("qwen3.8", sampleHits)
		b.tt.Key(0, ui.KeyEscape)
		b.tt.Frame()
		b.none("unsloth/Qwen3.8-27B-GGUF", "6,009,562 downloads")
		if !b.p.addingModel || b.text() != "qwen3.8" {
			t.Fatalf("popup %v: Esc closed the field (adding %v, text %q)", popup, b.p.addingModel, b.text())
		}
		// Typing opens the list again.
		b.typeText("x")
		b.searched("qwen3.8x", sampleHits)
		b.want("unsloth/Qwen3.8-27B-GGUF")
		b.tt.Key(0, ui.KeyEscape)
		b.tt.Frame()
		b.tt.Key(0, ui.KeyEscape)
		b.tt.Frame()
		if b.p.addingModel {
			t.Errorf("popup %v: the second Esc left the field open", popup)
		}
		if !b.rec.has("CancelModelSearch") {
			t.Errorf("popup %v: lookups kept running: %q", popup, b.rec.calls)
		}
	}
}

func TestAddModelEmptyAndFailedSearch(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("qwen3.8")
	b.searched("qwen3.8", sampleHits)
	b.want("unsloth/Qwen3.8-27B-GGUF")
	b.none(b.m.AddModelNothing())
	// The next search finds nothing: the field says so once it answered.
	b.typeText("x")
	b.none(b.m.AddModelNothing()) // still waiting for the pause
	b.searched("qwen3.8x", nil)
	b.none("unsloth/Qwen3.8-27B-GGUF", "6,009,562 downloads", "2,266,925 downloads", b.m.AddModelUnreachable())
	b.want(b.m.AddModelNothing())
	// One more letter: the note goes until that search answers, which
	// could not reach the hub.
	b.typeText("y")
	b.none(b.m.AddModelNothing())
	b.tt.Advance(searchDelay)
	b.called("SearchModels qwen3.8xy")
	b.p.SetModelSearchFailed("qwen3.8xy")
	b.tt.Frame()
	b.want(b.m.AddModelUnreachable())
	b.none(b.m.AddModelNothing())
	b.tt.Key(0, ui.KeyBackspace)
	b.tt.Frame()
	// The hint is for a field too short to search.
	b.none("Type a model's name to search Hugging Face, or paste owner/repo:variant")
	// Typing, and Enter, still work.
	b.tt.Key(ui.Cmd, ui.KeyA)
	b.typeText("unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M")
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	b.called("Download unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M")
}

func TestAddModelAnswerForOldTextIsDropped(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("qwen")
	b.tt.Advance(searchDelay)
	b.typeText("3")
	b.tt.Advance(searchDelay)
	b.p.SetModelHits("qwen", sampleHits) // too late: "qwen3" was asked since
	b.tt.Frame()
	b.none("unsloth/Qwen3.8-27B-GGUF")
	b.p.SetModelHits("qwen3", sampleHits)
	b.tt.Frame()
	b.want("unsloth/Qwen3.8-27B-GGUF")
}

func TestAddModelVariants(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	// A name with ":" asks for the variants at once, with no pause, and
	// shows that it waits.
	b.typeText("unsloth/Qwen3.8-27B-GGUF:")
	b.called("ModelVariants unsloth/Qwen3.8-27B-GGUF")
	b.notCalled("SearchModels unsloth/Qwen3.8-27B-GGUF:")
	b.want("Looking up variants…")
	b.variants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
	b.tt.Frame()
	b.none("Looking up variants…")
	b.want("UD-Q4_K_M", "16.5 GB", "UD-IQ4_XS", "14.3 GB", "Q8_0", "29 GB", "BF16", "54.7 GB")
	// 4-bit first.
	rows := b.p.add.rowsFor(b.text())
	if len(rows) != 5 || rows[0].variant.Tag != "UD-Q4_K_M" {
		t.Errorf("rows %+v", rows)
	}
	// Each variant says what it needs: its weights, the vision projector,
	// the draft Splash always runs and Splash's buffers (the sample knows no
	// KV cache). A big one warns: the Mac here has 48 GB, Splash may use
	// three quarters.
	b.want("Needs about 20.9 GB of memory", "Needs about 59.1 GB of memory, more than the 38.7 GB available")
	// What follows the colon filters, prefix first.
	b.typeText("q8")
	b.want("Q8_0")
	b.none("UD-Q4_K_M", "BF16")
	b.tt.Key(ui.Cmd, ui.KeyA)
	b.typeText("unsloth/Qwen3.8-27B-GGUF:UD-Q4")
	b.want("UD-Q4_K_M", "UD-Q4_K_XL")
	b.none("UD-IQ4_XS", "Q8_0")
	// Another repository asks again.
	b.tt.Key(ui.Cmd, ui.KeyA)
	b.typeText("ggml-org/Qwen3.8-27B-GGUF:")
	b.called("ModelVariants ggml-org/Qwen3.8-27B-GGUF")
	b.none("UD-Q4_K_M")
	// An answer for the repository left is dropped.
	b.variants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
	b.tt.Frame()
	b.none("UD-Q4_K_M")
}

func TestAddModelPickAVariant(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("unsloth/Qwen3.8-27B-GGUF:")
	b.variants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
	b.tt.Frame()
	b.tt.Key(0, ui.KeyDown)
	b.tt.Key(0, ui.KeyDown)
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	if got := b.text(); got != "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL" {
		t.Fatalf("Enter put %q", got)
	}
	// The pick is complete: the list is gone and Enter downloads.
	b.none("UD-IQ4_XS", "BF16")
	b.notCalled("Download unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL")
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	b.called("Download unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL")
	if b.p.addingModel {
		t.Error("the field stays open after the download")
	}
}

func TestAddModelVariantsOfAnotherCase(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("Unsloth/qwen3.8-27B-GGUF:")
	b.variants("Unsloth/qwen3.8-27B-GGUF", sampleVariants)
	b.tt.Frame()
	b.tt.Key(0, ui.KeyTab)
	b.tt.Frame()
	// The repository is as the user typed it.
	if got := b.text(); got != "Unsloth/qwen3.8-27B-GGUF:UD-Q4_K_M" {
		t.Errorf("got %q", got)
	}
}

// A repository without variants (an MLX one, for Splash) is complete.
func TestAddModelRepositoryWithoutVariants(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("qwen3.8")
	b.searched("qwen3.8", sampleHits)
	b.click("lmstudio-community/Qwen3.8-27B-MLX-4bit")
	b.tt.Frame()
	if got := b.text(); got != "lmstudio-community/Qwen3.8-27B-MLX-4bit" {
		t.Fatalf("got %q", got)
	}
	// Its quantization and size are looked up.
	b.called("ModelVariants lmstudio-community/Qwen3.8-27B-MLX-4bit")
	b.want("Looking up variants…")
	b.p.SetModelInfo("lmstudio-community/Qwen3.8-27B-MLX-4bit", hf.RepoInfo{ID: "lmstudio-community/Qwen3.8-27B-MLX-4bit",
		Weights: 15_200_000_000, Context: 262144, KVElements: 32768, Quant: "affine 4-bit g64"})
	b.tt.Frame()
	b.want("Needs about 27.4 GB of memory")
	b.tt.Advance(searchDelay)
	b.notCalled("SearchModels lmstudio-community/Qwen3.8-27B-MLX-4bit")
	b.none("3,586,440 downloads")
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	b.called("Download lmstudio-community/Qwen3.8-27B-MLX-4bit")
}

func TestAddModelClearsWhenTheFieldReopensOrThePanelHides(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("qwen3.8")
	b.searched("qwen3.8", sampleHits)
	b.p.Reset()
	b.tt.Frame()
	b.openAdd(false)
	b.none("unsloth/Qwen3.8-27B-GGUF")
	b.typeText("qw")
	b.tt.Advance(searchDelay)
	b.p.SetModelHits("qwen3.8", sampleHits) // an answer from before
	b.tt.Frame()
	b.none("unsloth/Qwen3.8-27B-GGUF")
	if b.count("CancelModelSearch") == 0 {
		t.Error("the lookup kept running")
	}
	// Switching the engine drops the repositories found for the other.
	b.p.SetModelHits("qw", sampleHits)
	b.tt.Frame()
	b.want("unsloth/Qwen3.8-27B-GGUF")
	v := llamaView()
	b.show(noModels(v), time.Minute)
	b.tt.Frame()
	if len(b.p.add.hits) != 0 {
		t.Error("hits survive an engine switch")
	}
}

// Measuring the height builds the view on its own tester and sends nothing.
func TestAddModelMeasureSendsNothing(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("qwen3.8")
	b.searched("qwen3.8", sampleHits)
	before := slices.Clone(b.rec.calls)
	h := b.p.height
	if got := b.p.Measure(true); got < 100 || got < h-4 {
		t.Errorf("measured %d, height %d", got, h)
	}
	if !slices.Equal(before, b.rec.calls) {
		t.Errorf("Measure called %q", b.rec.calls[len(before):])
	}
}

// The window is as tall as what "Add model…" shows: it grows with a longer
// list and shrinks with a shorter one, and every row of the list is in it.
// The app resizes it after the frame (OnResize), so no frame is stretched.
func TestAddModelHeightFollowsContent(t *testing.T) {
	var hits []hf.Repo
	for i := range hitRows {
		hits = append(hits, hf.Repo{ID: fmt.Sprintf("owner%d/Qwen3.8-27B-GGUF", i), Downloads: int64(1000 - i), Filter: "gguf"})
	}
	for _, popup := range []bool{false, true} {
		b := newBench(t, "en")
		var sizes []int
		b.p.OnResize = func(h int) { sizes = append(sizes, h) }
		b.openAdd(popup)
		b.tt.Frame()
		b.tt.Frame()
		open := b.p.height

		// fits checks that the row is inside the window.
		fits := func(row string) {
			t.Helper()
			r, ok := b.tt.Find(row)
			if !ok {
				t.Fatalf("popup %v: no %q in %q", popup, row, b.tt.Texts())
			}
			if bottom := int(r.Y + r.H); bottom > b.p.height {
				t.Errorf("popup %v: %q ends at %d, below the window's %d", popup, row, bottom, b.p.height)
			}
		}
		b.typeText("qw")
		b.searched("qw", hits)
		b.tt.Frame()
		fits(hits[hitRows-1].ID)
		long := b.p.height
		if long <= open {
			t.Errorf("popup %v: %d rows left the window at %d", popup, hitRows, long)
		}
		b.typeText("en3.8")
		b.searched("qwen3.8", hits[:1])
		b.tt.Frame()
		fits(hits[0].ID)
		if b.p.height >= long {
			t.Errorf("popup %v: one row kept the window at %d", popup, b.p.height)
		}
		// No room is kept: the window always matches the layout.
		if got := b.p.Measure(false); got != b.p.height {
			t.Errorf("popup %v: window %d, content %d", popup, b.p.height, got)
		}

		// Esc closes the list, then the field: the window shrinks to the
		// screen without it.
		b.tt.Key(0, ui.KeyEscape)
		b.tt.Frame()
		b.tt.Key(0, ui.KeyEscape)
		b.tt.Frame()
		b.tt.Frame()
		if b.p.height >= open {
			t.Errorf("popup %v: open at %d, closed at %d", popup, open, b.p.height)
		}
		if len(sizes) == 0 || sizes[len(sizes)-1] != b.p.height {
			t.Errorf("popup %v: resizes %v, height %d", popup, sizes, b.p.height)
		}
	}
}

// The window slides between the main screen, with the open field, and
// Settings without a jump either way.
func TestAddModelSlide(t *testing.T) {
	b := newBench(t, "en")
	var sizes []int
	b.p.OnResize = func(h int) { sizes = append(sizes, h) }
	b.openAdd(false)
	b.tt.Frame()
	open := b.p.height
	steady := func(from, to int, way string) {
		t.Helper()
		for range 25 {
			b.tt.Advance(16 * time.Millisecond)
		}
		prev := from
		for _, h := range sizes {
			if (to > from && h < prev) || (to < from && h > prev) {
				t.Errorf("%s: %d to %d by %v", way, from, to, sizes)
				break
			}
			prev = h
		}
		if b.p.height != to {
			t.Errorf("%s: ends at %d, want %d", way, b.p.height, to)
		}
		sizes = nil
	}
	sizes = nil
	b.click(b.m.Settings())
	steady(open, b.p.Measure(false), "to Settings")
	set := b.p.height
	b.click(b.m.Back())
	steady(set, open, "back")
}

// Arabic mirrors the layout; the names stay left to right.
func TestAddModelArabic(t *testing.T) {
	b := newBench(t, "ar")
	b.openAdd(false)
	b.typeText("qwen3.8")
	b.searched("qwen3.8", sampleHits)
	b.want("unsloth/Qwen3.8-27B-GGUF")
	b.tt.Key(0, ui.KeyDown)
	b.tt.Key(0, ui.KeyEnter)
	b.variants("unsloth/Qwen3.8-27B-GGUF", sampleVariants)
	b.tt.Frame()
	b.want("UD-Q4_K_M")
}

// The whole way with Splash: the typed text goes through the debounce to the
// lookup, which asks a hub that serves the responses recorded from
// huggingface.co (internal/hf/testdata), and the answers come back on the
// UI thread, as the app posts them.
func TestAddModelEndToEnd(t *testing.T) {
	serve := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("..", "hf", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var mu sync.Mutex
	var urls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		urls = append(urls, r.URL.String())
		mu.Unlock()
		switch {
		case r.URL.Path == "/api/models" && r.URL.Query().Get("filter") == "gguf":
			_, _ = w.Write(serve("search-gguf-siblings.json"))
		case r.URL.Path == "/api/models" && r.URL.Query().Get("filter") == "mlx":
			_, _ = w.Write(serve("search-mlx-arch.json"))
		case r.URL.Path == "/api/models/unsloth/Qwen3.8-27B-GGUF/tree/main":
			_, _ = w.Write(serve("tree-unsloth-qwen3.8-27b.json"))
		case r.URL.Path == "/unsloth/Qwen3.8-27B-GGUF/resolve/main/Qwen3.8-27B-UD-Q4_K_M.gguf":
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(serve("header-unsloth-qwen3.8-27b-ud-q4_k_m.gguf"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	b := newBench(t, "en")
	posted := make(chan func(), 8)
	look := &hf.Lookup{Client: &hf.Client{BaseURL: srv.URL}, Deliver: func(f func()) { posted <- f }}
	b.rec.search = func(text string) {
		var src []hf.Source
		for _, s := range splash.New().Info().ModelSearch.Sources {
			src = append(src, hf.Source{Filter: s.Filter, Architectures: s.Architectures})
		}
		look.Search(hf.Query{Text: text, Sources: src, Limit: 8}, func(rs []hf.Repo, err error) {
			if err != nil {
				rs = nil
			}
			b.p.SetModelHits(text, rs)
		})
	}
	b.rec.variants = func(repo string) {
		spec := splash.New().Info().ModelSearch
		rules := hf.Rules{RefuseGGUF: spec.RefuseGGUF}
		look.Repo(repo, rules, func(info hf.RepoInfo, err error) { b.p.SetModelInfo(repo, info) })
	}
	deliver := func() {
		t.Helper()
		select {
		case f := <-posted:
			f()
			b.tt.Frame()
		case <-time.After(5 * time.Second):
			t.Fatal("the lookup did not answer")
		}
	}

	b.openAdd(false)
	b.typeText("qwen3.8")
	b.tt.Advance(searchDelay)
	deliver()
	b.want("unsloth/Qwen3.8-27B-GGUF", "MLX · ")
	// The architectures Splash does not run are not offered.
	b.none("mlx-community/Qwen3.8-27B-MTP-4bit")
	rows := b.p.add.rowsFor(b.text())
	if len(rows) == 0 || rows[0].repo.ID != "ISTA-DASLab/Qwen3.8-Flash-Next-GSQ-RCO-GGUF" {
		t.Fatalf("rows %+v", rows)
	}

	b.click("unsloth/Qwen3.8-27B-GGUF")
	b.tt.Frame()
	deliver()
	// Splash refuses whole-BF16 models; the 256K KV cache of the header's
	// 16 attention layers counts.
	b.want("UD-Q4_K_M", "16.5 GB", "Needs about 29.6 GB of memory")
	b.none("mtp-Qwen3.8-27B-Q4_0", "imatrix_unsloth", "BF16")
	b.tt.Key(0, ui.KeyDown)
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	b.tt.Key(0, ui.KeyEnter)
	b.tt.Frame()
	b.called("Download unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M")
	mu.Lock()
	defer mu.Unlock()
	if len(urls) != 4 {
		t.Errorf("requests %q", urls)
	}
}

// A repository typed with ":" that has no variant the engine runs says so;
// one the hub could not be asked about says that instead.
func TestAddModelNoVariants(t *testing.T) {
	b := newBench(t, "en")
	b.openAdd(false)
	b.typeText("acme/Empty-GGUF:")
	b.called("ModelVariants acme/Empty-GGUF")
	b.want(b.m.AddModelLoading())
	b.p.SetModelInfo("acme/Empty-GGUF", hf.RepoInfo{ID: "acme/Empty-GGUF", GGUF: true, Refused: 3})
	b.tt.Frame()
	b.want(b.m.AddModelNoVariants(b.p.view.Engine))
	b.none(b.m.AddModelLoading(), b.m.AddModelNothing())

	b.tt.Key(ui.Cmd, ui.KeyA)
	b.typeText("acme/Gone-GGUF:")
	b.p.SetModelInfoFailed("acme/Gone-GGUF")
	b.tt.Frame()
	b.want(b.m.AddModelUnreachable())
	b.none(b.m.AddModelNoVariants(b.p.view.Engine))
}
