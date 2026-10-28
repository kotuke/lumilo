package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

const testCommit = "13ac06624948e4d8589cb4b6f90100837342fcb8"

// fakeHub answers the requests Download makes: the revision with
// checksums, the manifest of a tag (only to a llama.cpp User-Agent, as the
// hub does) and the files at the commit, with Range.
type fakeHub struct {
	files    map[string][]byte
	manifest map[string][2]string // tag -> model, projector
	bad      map[string]bool      // files served with other content
	mu       sync.Mutex
	gets     []string // "file range"
}

func (h *fakeHub) start(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const repo = "/acme/Tiny-1B-GGUF"
		switch {
		case r.URL.Path == "/api/models"+repo+"/revision/main":
			type lfs struct {
				SHA256 string `json:"sha256"`
				Size   int    `json:"size"`
			}
			type sib struct {
				Name string `json:"rfilename"`
				Size int    `json:"size"`
				LFS  *lfs   `json:"lfs,omitempty"`
			}
			out := struct {
				SHA      string `json:"sha"`
				Siblings []sib  `json:"siblings"`
			}{SHA: testCommit}
			out.Siblings = append(out.Siblings, sib{Name: "README.md", Size: 3})
			for name, data := range h.files {
				s := sha256.Sum256(data)
				out.Siblings = append(out.Siblings, sib{name, len(data), &lfs{hex.EncodeToString(s[:]), len(data)}})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(r.URL.Path, "/v2"+repo+"/manifests/"):
			m, ok := h.manifest[strings.TrimPrefix(r.URL.Path, "/v2"+repo+"/manifests/")]
			if !ok || r.Header.Get("User-Agent") != "llama-cpp" {
				http.Error(w, `{"error":"not a valid quantization scheme"}`, http.StatusBadRequest)
				return
			}
			body := map[string]any{"ggufFile": map[string]string{"rfilename": m[0]}}
			if m[1] != "" {
				body["mmprojFile"] = map[string]string{"rfilename": m[1]}
			}
			_ = json.NewEncoder(w).Encode(body)
		case strings.HasPrefix(r.URL.Path, repo+"/resolve/"+testCommit+"/"):
			name := strings.TrimPrefix(r.URL.Path, repo+"/resolve/"+testCommit+"/")
			data, ok := h.files[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if h.bad[name] {
				data = []byte(strings.Repeat("?", len(data)))
			}
			h.mu.Lock()
			h.gets = append(h.gets, name+" "+r.Header.Get("Range"))
			h.mu.Unlock()
			from := 0
			if rg := r.Header.Get("Range"); rg != "" {
				fmt.Sscanf(rg, "bytes=%d-", &from)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(data)-1, len(data)))
				w.WriteHeader(http.StatusPartialContent)
			}
			_, _ = w.Write(data[from:])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// dlEngine is an engine whose hub is h and whose only cache is a fresh
// directory.
func dlEngine(t *testing.T, h *fakeHub) (*LlamaCpp, string) {
	l := newEngine()
	cache := t.TempDir()
	l.hubDirs, l.flatDirs, l.dlDir = []string{cache}, nil, cache
	l.hubURL = h.start(t).URL
	return l, cache
}

func tinyHub() *fakeHub {
	model := textModel("llama")
	return &fakeHub{
		files: map[string][]byte{
			"Tiny-1B-Q4_K_M.gguf":     model,
			"Tiny-1B-Q8_0.gguf":       append(append([]byte{}, model...), 0, 0),
			"mmproj-Tiny-1B-F16.gguf": ggufFile([]kv{{"general.architecture", "clip"}, {"general.type", "mmproj"}}, 10),
		},
		manifest: map[string][2]string{"Q4_K_M": {"Tiny-1B-Q4_K_M.gguf", "mmproj-Tiny-1B-F16.gguf"}},
	}
}

func TestDownload(t *testing.T) {
	h := tinyHub()
	l, cache := dlEngine(t, h)
	var last [2]int64
	id, err := l.Download(context.Background(), engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:Q4_K_M"},
		func(done, total int64) { last = [2]int64{done, total} })
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cache, "models--acme--Tiny-1B-GGUF")
	if want := filepath.Join(root, "snapshots", testCommit, "Tiny-1B-Q4_K_M.gguf"); id != want {
		t.Errorf("id = %s, want %s", id, want)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "refs", "main")); string(b) != testCommit {
		t.Errorf("refs/main = %q", b)
	}
	want := int64(len(h.files["Tiny-1B-Q4_K_M.gguf"]) + len(h.files["mmproj-Tiny-1B-F16.gguf"]))
	if last != [2]int64{want, want} {
		t.Errorf("progress = %v, want %d of %d", last, want, want)
	}
	// Models lists it by that path, with the projector beside it.
	models, err := l.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != id || !models[0].Features.Vision || models[0].Variant != "Q4_K_M" {
		t.Fatalf("models = %+v", models)
	}
	if len(h.gets) != 2 {
		t.Errorf("fetched %q, want the model and the projector", h.gets)
	}
}

func TestDownloadNoVisionAndFallback(t *testing.T) {
	h := tinyHub()
	l, cache := dlEngine(t, h)
	// Q8_0 has no manifest here: the file names decide.
	id, err := l.Download(context.Background(), engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:Q8_0", NoVision: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(id) != "Tiny-1B-Q8_0.gguf" {
		t.Errorf("id = %s", id)
	}
	if len(h.gets) != 1 || !strings.HasPrefix(h.gets[0], "Tiny-1B-Q8_0.gguf") {
		t.Errorf("fetched %q, want the model alone", h.gets)
	}
	if _, err := os.Lstat(filepath.Join(cache, "models--acme--Tiny-1B-GGUF", "snapshots", testCommit, "mmproj-Tiny-1B-F16.gguf")); !os.IsNotExist(err) {
		t.Error("the projector was linked")
	}
}

// A split GGUF in a folder of its own (BF16/…-00001-of-00002.gguf) is
// fetched whole and listed by the path of its first part.
func TestDownloadSplitInFolder(t *testing.T) {
	h := tinyHub()
	h.files["BF16/Tiny-1B-BF16-00001-of-00002.gguf"] = textModel("llama")
	h.files["BF16/Tiny-1B-BF16-00002-of-00002.gguf"] = []byte("second part")
	l, _ := dlEngine(t, h)
	id, err := l.Download(context.Background(), engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:BF16", NoVision: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(id) != "Tiny-1B-BF16-00001-of-00002.gguf" || filepath.Base(filepath.Dir(id)) != "BF16" {
		t.Errorf("id = %s", id)
	}
	models, err := l.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got *engine.Model
	for i := range models {
		if models[i].ID == id {
			got = &models[i]
		}
	}
	want := int64(len(h.files["BF16/Tiny-1B-BF16-00001-of-00002.gguf"]) + len("second part"))
	if got == nil || got.SizeBytes != want || got.Owner != "acme" {
		t.Fatalf("models = %+v, want %s of %d bytes", models, id, want)
	}
}

func TestDownloadFailures(t *testing.T) {
	h := tinyHub()
	l, _ := dlEngine(t, h)
	var f engine.Failure
	_, err := l.Download(context.Background(), engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:Q2_K"}, nil)
	if !errors.As(err, &f) || f.Kind != engine.FailModel {
		t.Errorf("unknown tag: %v, want FailModel", err)
	}
	_, err = l.Download(context.Background(), engine.DownloadRequest{Model: "acme/Nope-GGUF:Q4_K_M"}, nil)
	if !errors.As(err, &f) || f.Kind != engine.FailModel {
		t.Errorf("unknown repository: %v, want FailModel", err)
	}
	h.bad = map[string]bool{"Tiny-1B-Q4_K_M.gguf": true}
	_, err = l.Download(context.Background(), engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:Q4_K_M"}, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("bad content: %v, want a checksum mismatch", err)
	}
}

func TestDownloadResumeAndDrop(t *testing.T) {
	h := tinyHub()
	l, cache := dlEngine(t, h)
	data := h.files["Tiny-1B-Q4_K_M.gguf"]
	s := sha256.Sum256(data)
	blobs := filepath.Join(cache, "models--acme--Tiny-1B-GGUF", "blobs")
	part := filepath.Join(blobs, hex.EncodeToString(s[:])+".lumilo-part")
	write(t, part, data[:100], 0o644)

	// A cancelled download keeps the part; DropPartial removes it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Download(ctx, engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:Q4_K_M"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("part gone after cancel: %v", err)
	}

	// The next one resumes it.
	if _, err := l.Download(context.Background(), engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:Q4_K_M", NoVision: true}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.gets) != 1 || h.gets[0] != "Tiny-1B-Q4_K_M.gguf bytes=100-" {
		t.Errorf("fetched %q, want the rest from byte 100", h.gets)
	}

	write(t, filepath.Join(blobs, "deadbeef.lumilo-part"), []byte("x"), 0o644)
	l.DropPartial("acme/Tiny-1B-GGUF:Q4_K_M", time.Now())
	if left, _ := filepath.Glob(filepath.Join(blobs, "*.lumilo-part")); len(left) != 0 {
		t.Errorf("DropPartial left %q", left)
	}
}

// A download resumed after a quit counts the part it had from the first
// report on, and the blob it completes is checked: a part that was not
// the file's start fails the checksum, is dropped, and the next try
// fetches the file whole.
func TestDownloadResumeProgressAndChecksum(t *testing.T) {
	h := tinyHub()
	l, cache := dlEngine(t, h)
	data := h.files["Tiny-1B-Q4_K_M.gguf"]
	s := sha256.Sum256(data)
	blobs := filepath.Join(cache, "models--acme--Tiny-1B-GGUF", "blobs")
	part := filepath.Join(blobs, hex.EncodeToString(s[:])+".lumilo-part")
	req := engine.DownloadRequest{Model: "acme/Tiny-1B-GGUF:Q4_K_M", NoVision: true}

	write(t, part, data[:len(data)/2], 0o644)
	var first [2]int64
	got := false
	id, err := l.Download(context.Background(), req, func(done, total int64) {
		if !got {
			first, got = [2]int64{done, total}, true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := [2]int64{int64(len(data) / 2), int64(len(data))}; first != want {
		t.Errorf("first report %v, want %v", first, want)
	}
	if b, err := os.ReadFile(id); err != nil || string(b) != string(data) {
		t.Errorf("resumed file differs (%v)", err)
	}

	// Another part, not of this file: the end checks it.
	h.gets = nil
	_ = os.Remove(filepath.Join(blobs, hex.EncodeToString(s[:])))
	write(t, part, []byte(strings.Repeat("?", len(data)/2)), 0o644)
	if _, err := l.Download(context.Background(), req, nil); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("bad part: %v", err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Errorf("the bad part stayed: %v", err)
	}
	if _, err := l.Download(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.gets) != 2 || h.gets[1] != "Tiny-1B-Q4_K_M.gguf " {
		t.Errorf("fetched %q, want the rest of the bad part, then the whole file", h.gets)
	}
}
