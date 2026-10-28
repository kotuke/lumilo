package hf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The files of testdata are responses of huggingface.co recorded on
// 2026-10-27:
//
//	search-gguf.json      /api/models?search=qwen3.8&limit=8&sort=downloads&direction=-1&filter=gguf
//	search-gguf-arch.json the same with expand[]=downloads,likes,gguf and limit=24
//	search-mlx-arch.json  filter=mlx with expand[]=downloads,likes,config and limit=24
//	tree-…json            /api/models/unsloth/Qwen3.8-27B-GGUF/tree/main?recursive=true
//	search-gguf-siblings.json  search=qwen3.8, filter=gguf, limit=24, expand[]=downloads,likes,gguf,siblings
//	search-mmproj-siblings.json the same for search=mmproj, limit=40
//
// The expanded responses are cut to gguf.architecture, gguf.total,
// gguf.context_length and config.model_type: the hub adds each repository's
// whole chat template, 60 KB of text the client never reads.

// hub serves testdata files by request and counts the requests.
type hub struct {
	t    *testing.T
	mu   sync.Mutex
	reqs []*url.URL
	// routes maps a filter ("gguf", "mlx") to the file the search returns.
	routes map[string]string
	tree   string
}

func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.reqs = append(h.reqs, r.URL)
	h.mu.Unlock()
	var file string
	switch {
	case r.URL.Path == "/api/models":
		file = h.routes[r.URL.Query().Get("filter")]
	case strings.HasSuffix(r.URL.Path, "/tree/main"):
		if r.URL.Path != "/api/models/unsloth/Qwen3.8-27B-GGUF/tree/main" {
			http.Error(w, `{"error":"Invalid username or password."}`, http.StatusUnauthorized)
			return
		}
		file = h.tree
	}
	if file == "" {
		http.Error(w, "no route", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		h.t.Error(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func newHub(t *testing.T) (*hub, *Client) {
	t.Helper()
	h := &hub{t: t, routes: map[string]string{
		"gguf": "search-gguf.json",
		"mlx":  "search-mlx-arch.json",
	}, tree: "tree-unsloth-qwen3.8-27b.json"}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return h, &Client{BaseURL: srv.URL}
}

func ids(rs []Repo) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestSearchPlain(t *testing.T) {
	h, c := newHub(t)
	repos, err := c.Search(context.Background(), Query{Text: " qwen3.8 ", Sources: []Source{{Filter: FilterGGUF}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 8 || repos[0].ID != "ISTA-DASLab/Qwen3.8-Flash-Next-GSQ-RCO-GGUF" || repos[0].Downloads != 7296902 ||
		repos[1].ID != "unsloth/Qwen3.8-27B-GGUF" || !repos[1].GGUF() || repos[1].Likes != 5366 {
		t.Errorf("repos %+v", repos)
	}
	// The request is the one the hub documents, for three times the
	// repositories: the file filter drops some.
	q := h.reqs[0].Query()
	for k, want := range map[string]string{"search": "qwen3.8", "limit": "24", "sort": "downloads", "direction": "-1", "filter": "gguf"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if got := q["expand[]"]; !slices.Equal(got, []string{"downloads", "likes", "siblings", "gguf"}) {
		t.Errorf("expand %v", got)
	}
}

func TestSearchArchitectures(t *testing.T) {
	h, c := newHub(t)
	h.routes["gguf"] = "search-gguf-siblings.json"
	// Splash's families, as its install/gguf.py ARCHITECTURES names them.
	repos, err := c.Search(context.Background(), Query{Text: "qwen3.8", Limit: 8, Sources: []Source{
		{Filter: FilterGGUF, Architectures: []string{"qwen35", "qwen35moe", "qwen4exp", "flint"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The hub calls cdiamond/…NVFP4-MTP-GGUF "clip" after its mmproj, but
	// it holds a model too: kept, its architecture unknown.
	if len(repos) != 8 || repos[2].ID != "cdiamond/Qwen3.8-27B-iMatrix-NVFP4-MTP-GGUF" || repos[2].Architecture != "" ||
		repos[1].Architecture != "qwen35" {
		t.Errorf("repos %v", repos)
	}
	q := h.reqs[0].Query()
	if got := q["expand[]"]; !slices.Equal(got, []string{"downloads", "likes", "siblings", "gguf"}) || q.Get("limit") != "24" {
		t.Errorf("expand %v limit %s", got, q.Get("limit"))
	}
}

// TestSearchDropsProjectors: a search for "mmproj" (recorded with its file
// names) finds repositories of vision projectors alone, which no engine
// serves, beside models that come with one.
func TestSearchDropsProjectors(t *testing.T) {
	h, c := newHub(t)
	h.routes["gguf"] = "search-mmproj-siblings.json"
	repos, err := c.Search(context.Background(), Query{Text: "mmproj", Limit: 40, Sources: []Source{{Filter: FilterGGUF}}})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(repos)
	for _, id := range []string{"koboldcpp/mmproj", "HermiHg/Qwen3.8-27B-mmproj-Q5_K-MIX-GGUF", "AVert888/Qwen3.5-9B-mmproj-q8_0-GGUF",
		"ChaoticNeutrals/LLaVA-Llama-3-8B-mmproj-Updated"} {
		if slices.Contains(got, id) {
			t.Errorf("%s kept", id)
		}
	}
	for _, id := range []string{"concedo/llama-joycaption-beta-one-hf-llava-mmproj-gguf",
		"Youseff1987/Qwen3.6-35B-A3B-Claude-4.6-Opus-Reasoning-Distilled-GGUF-with-mmproj",
		"lucatarik/Qwen3.5-4B-heretic-i1-GGUF-mmproj", "EnderCrypt/Qwen3-VL-30B-A3B-Instruct-GGUF-mmproj"} {
		if !slices.Contains(got, id) {
			t.Errorf("%s dropped", id)
		}
	}
}

func TestHasModelGGUF(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  bool
	}{
		{[]string{"README.md", "mmproj-F16.gguf", "MTP/mtp-Qwen3.8-27B-Q4_0.gguf", "imatrix.gguf"}, false},
		{[]string{"dflash-Qwen3.8-27B-Q4_0.gguf", "Qwen3.6-27B-DFlash-Q4_K_M.gguf"}, false},
		{[]string{"bge-m3-embedding-Q8_0.gguf", "Qwen3-Reranker-4B-Q8_0.gguf"}, false},
		{[]string{"x/y/Model-Q4_K_M-00001-of-00002.gguf", "z/Model.gguf"}, false},
		{[]string{"Q8_0/Model-Q8_0-00001-of-00002.gguf"}, true},
		{[]string{"Qwen3.8-27B-GSQ-RCO-IQ2_S-mtp.gguf"}, true},
	} {
		if got := HasModelGGUF(c.files); got != c.want {
			t.Errorf("%v: %v", c.files, got)
		}
	}
}

func TestSearchTwoSources(t *testing.T) {
	h, c := newHub(t)
	h.routes["gguf"] = "search-gguf-arch.json"
	repos, err := c.Search(context.Background(), Query{Text: "qwen3.8", Limit: 12, Sources: []Source{
		{Filter: FilterGGUF, Architectures: []string{"qwen35", "qwen35moe", "qwen4exp", "flint"}},
		{Filter: "mlx", Architectures: []string{"qwen3_5", "qwen3_5_moe", "qwen4_exp", "flint"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 12 {
		t.Fatalf("%d repos: %v", len(repos), ids(repos))
	}
	for i := 1; i < len(repos); i++ {
		if repos[i].Downloads > repos[i-1].Downloads {
			t.Errorf("not by downloads: %v", ids(repos))
		}
	}
	// The MLX ones come without variants; the architectures not run are out.
	mlx := 0
	for _, r := range repos {
		if r.Filter == "mlx" {
			mlx++
			if r.GGUF() || (r.Architecture != "qwen3_5" && r.Architecture != "qwen4_exp") {
				t.Errorf("mlx repo %+v", r)
			}
		}
	}
	if mlx == 0 {
		t.Errorf("no mlx repository in %v", ids(repos))
	}
	for _, q := range h.reqs {
		if q.Query().Get("filter") == "mlx" && !slices.Equal(q.Query()["expand[]"], []string{"downloads", "likes", "siblings", "config"}) {
			t.Errorf("mlx expand %v", q.Query()["expand[]"])
		}
	}
}

func TestSearchKeepsUnknownArchitecture(t *testing.T) {
	_, c := newHub(t)
	// mradermacher/Qwen3.8-27B-OBLITERATED-GGUF has the mlx tag and no config.
	repos, err := c.Search(context.Background(), Query{Text: "qwen3.8", Limit: 30, Sources: []Source{
		{Filter: "mlx", Architectures: []string{"qwen3_5", "qwen4_exp"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(repos)
	if !slices.Contains(got, "mradermacher/Qwen3.8-27B-OBLITERATED-GGUF") {
		t.Errorf("unknown architecture dropped: %v", got)
	}
	for _, id := range []string{"mlx-community/Qwen3.8-27B-MTP-4bit", "naklitechie/Qwen3.8-27B-DFlash2-ternary-bonsai2"} {
		if slices.Contains(got, id) {
			t.Errorf("%s kept", id)
		}
	}
}

func TestSearchErrors(t *testing.T) {
	h, c := newHub(t)
	// One source down, the other answers: its repositories come.
	h.routes["mlx"] = ""
	repos, err := c.Search(context.Background(), Query{Text: "qwen", Sources: []Source{{Filter: FilterGGUF}, {Filter: "mlx"}}})
	if err != nil || len(repos) == 0 {
		t.Errorf("one source failed: %v, %d repos", err, len(repos))
	}
	// All down.
	if _, err := c.Search(context.Background(), Query{Text: "qwen", Sources: []Source{{Filter: "mlx"}}}); err == nil {
		t.Error("no error when the only source failed")
	}
	// Not JSON.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer bad.Close()
	if _, err := (&Client{BaseURL: bad.URL}).Search(context.Background(), Query{Text: "qwen", Sources: []Source{{Filter: FilterGGUF}}}); err == nil {
		t.Error("no error for a page that is not JSON")
	}
	// Nothing to ask.
	if r, err := c.Search(context.Background(), Query{Text: "  ", Sources: []Source{{Filter: FilterGGUF}}}); r != nil || err != nil {
		t.Errorf("blank text: %v %v", r, err)
	}
	// No network.
	dead := httptest.NewServer(nil)
	dead.Close()
	if _, err := (&Client{BaseURL: dead.URL}).Search(context.Background(), Query{Text: "qwen", Sources: []Source{{Filter: FilterGGUF}}}); err == nil {
		t.Error("no error when the hub is unreachable")
	}
}

func TestSearchCancel(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := (&Client{BaseURL: slow.URL}).Search(ctx, Query{Text: "qwen", Sources: []Source{{Filter: FilterGGUF}}})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("no error after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Search ignores the context")
	}
}

func tagsOfVariants(vs []Variant) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Tag)
	}
	return out
}

func TestVariantsRecorded(t *testing.T) {
	_, c := newHub(t)
	vs, err := c.Variants(context.Background(), "unsloth/Qwen3.8-27B-GGUF")
	if err != nil {
		t.Fatal(err)
	}
	tags := tagsOfVariants(vs)
	// 4-bit first, UD-Q4_K_M in front, the rest by size; mmproj, imatrix and MTP/ are not
	// variants; BF16/ is a split of two parts, summed.
	want := []string{"UD-Q4_K_M", "UD-IQ4_XS", "UD-Q4_K_S", "Q4_0", "Q4_1", "UD-Q4_K_XL"}
	if !slices.Equal(tags[:len(want)], want) {
		t.Errorf("tags %v", tags)
	}
	for _, bad := range []string{"mtp-Qwen3.8-27B-Q4_0", "BF16-00001-of-00002", "F16", "BF16-F16"} {
		if slices.Contains(tags, bad) {
			t.Errorf("%q listed in %v", bad, tags)
		}
	}
	by := map[string]Variant{}
	for _, v := range vs {
		by[v.Tag] = v
	}
	if v := by["UD-Q4_K_M"]; v.Bytes != 16464440224 || v.Parts != 1 {
		t.Errorf("UD-Q4_K_M %+v", v)
	}
	if v := by["BF16"]; v.Bytes != 49986159616+4671576000 || v.Parts != 2 {
		t.Errorf("BF16 %+v", v)
	}
	if len(vs) != 25 {
		t.Errorf("%d variants: %v", len(vs), tags)
	}
	// Past the 4-bit ones, smallest first.
	rest := vs[slices.IndexFunc(vs, func(v Variant) bool { return rank(v.Tag) == 2 }):]
	for i := 1; i < len(rest); i++ {
		if rest[i].Bytes < rest[i-1].Bytes {
			t.Errorf("not by size: %v", tagsOfVariants(rest))
			break
		}
	}
}

func TestVariantsErrors(t *testing.T) {
	_, c := newHub(t)
	if _, err := c.Variants(context.Background(), "nobody/nothing"); err == nil {
		t.Error("no error for a repository the hub refuses")
	}
	for _, bad := range []string{"", "owner", "owner/", "/repo", "a/b?c"} {
		if _, err := c.Variants(context.Background(), bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func f(path string, size int64) File { return File{Path: path, Size: size} }

func TestParseVariants(t *testing.T) {
	tests := []struct {
		name  string
		repo  string
		files []File
		want  []Variant
	}{
		{
			name: "split in folders, as Unsloth and Bartowski publish big models",
			repo: "inclusionAI/Ling-3.0-flash-GGUF",
			files: []File{
				f("Q4_K_M/Ling-3.0-flash-Q4_K_M-00001-of-00002.gguf", 50), f("Q4_K_M/Ling-3.0-flash-Q4_K_M-00002-of-00002.gguf", 27),
				f("Q8_0/Ling-3.0-flash-Q8_0-00001-of-00003.gguf", 40), f("Q8_0/Ling-3.0-flash-Q8_0-00002-of-00003.gguf", 40),
				f("Q8_0/Ling-3.0-flash-Q8_0-00003-of-00003.gguf", 40),
				f("README.md", 1),
			},
			want: []Variant{{"Q4_K_M", 77, 2}, {"Q8_0", 120, 3}},
		},
		{
			name: "an incomplete split is not offered",
			repo: "o/r-GGUF",
			files: []File{
				f("r-Q4_K_M.gguf", 4),
				f("r-Q8_0-00001-of-00002.gguf", 8),
			},
			want: []Variant{{"Q4_K_M", 4, 1}},
		},
		{
			name:  "one GGUF: the words after the repository's name",
			repo:  "ggml-org/Qwen3-0.6B-GGUF",
			files: []File{f("Qwen3-0.6B-Q8_0.gguf", 600), f("mmproj-Qwen3-0.6B-f16.gguf", 9)},
			want:  []Variant{{"Q8_0", 600, 1}},
		},
		{
			name:  "one GGUF named alone has no tag",
			repo:  "o/tiny-GGUF",
			files: []File{f("model.gguf", 5)},
			want:  nil,
		},
		{
			name:  "quantization after a dot, as TheBloke names it",
			repo:  "TheBloke/Llama-2-7B-Chat-GGUF",
			files: []File{f("llama-2-7b-chat.Q4_K_M.gguf", 4), f("llama-2-7b-chat.Q5_K_M.gguf", 5), f("llama-2-7b-chat.Q2_K.gguf", 2)},
			want:  []Variant{{"Q4_K_M", 4, 1}, {"Q2_K", 2, 1}, {"Q5_K_M", 5, 1}},
		},
		{
			name:  "a name that is the start of the others keeps all its words",
			repo:  "o/m-GGUF",
			files: []File{f("m.gguf", 9), f("m-Q8_0.gguf", 8)},
			want:  []Variant{{"m-Q8_0", 8, 1}, {"m", 9, 1}},
		},
		{
			name:  "folders other than splits, and deep ones, are not variants",
			repo:  "o/m-GGUF",
			files: []File{f("m-Q4_K_M.gguf", 4), f("old/m-Q5_K_M.gguf", 5), f("a/b/m-Q6_K-00001-of-00001.gguf", 6), f("m-Q8_0.gguf", 8)},
			want:  []Variant{{"Q4_K_M", 4, 1}, {"Q8_0", 8, 1}},
		},
		{name: "no GGUF", repo: "o/m", files: []File{f("model.safetensors", 1), f("config.json", 1)}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseVariants(tt.repo, tt.files); !slices.Equal(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
