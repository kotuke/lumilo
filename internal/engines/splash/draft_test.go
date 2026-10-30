package splash

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A model whose draft blobs are gone from the hub cache gets the draft
// back at the commit its model.json records, not at main, and the cache's
// refs/main stays as it was.
func TestFetchDraftAtRecordedCommit(t *testing.T) {
	const (
		repo   = "incoai/Qwen3.8-27B-DFlash2"
		commit = "015e795645c74b1a0eeef3b570031fb62e769bc5"
	)
	config, weights := []byte(`{"model_type":"dflash"}`), []byte("draft weights")
	ws := sha256.Sum256(weights)
	blobID := gitBlobID(config)

	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/api/models/" + repo + "/revision/" + commit:
			fmt.Fprintf(w, `{"sha":%q,"siblings":[
				{"rfilename":"README.md","size":3,"blobId":"%s"},
				{"rfilename":"config.json","size":%d,"blobId":"%s"},
				{"rfilename":"model.safetensors","size":%d,"lfs":{"sha256":"%s","size":%d}}]}`,
				commit, gitBlobID([]byte("abc")), len(config), blobID, len(weights), hex.EncodeToString(ws[:]), len(weights))
		case "/" + repo + "/resolve/" + commit + "/config.json":
			w.Write(config)
		case "/" + repo + "/resolve/" + commit + "/model.safetensors":
			w.Write(weights)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cache := t.TempDir()
	snap := filepath.Join(cache, "models--incoai--Qwen3.8-27B-DFlash2", "snapshots", commit)
	// What another program leaves: the repository's folder without blobs.
	if err := os.MkdirAll(filepath.Join(cache, "models--incoai--Qwen3.8-27B-DFlash2", "trees"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "orcarouter", "OrcaCyber-Nano-1.0:Mix-15.7G")
	writeManifest(t, link, map[string]any{
		"files": map[string]any{
			"target/model.gguf":       map[string]any{"bytes": 1, "path": filepath.Join(cache, "elsewhere.gguf")},
			"draft/config.json":       map[string]any{"bytes": len(config), "path": filepath.Join(snap, "config.json")},
			"draft/model.safetensors": map[string]any{"bytes": len(weights), "path": filepath.Join(snap, "model.safetensors")},
		},
		"sources": map[string]any{"draft": map[string]any{"repo": repo, "revision": commit}},
	})

	d, ok := missingDraft(link)
	if !ok || d.repo != repo || d.commit != commit || d.cache != cache || len(d.files) != 2 {
		t.Fatalf("missingDraft = %+v, %v; want the draft at %s in %s", d, ok, commit, cache)
	}
	s := &Splash{hubURL: srv.URL}
	if err := s.fetchDraft(context.Background(), d, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{"config.json": config, "model.safetensors": weights} {
		got, err := os.ReadFile(filepath.Join(snap, name))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(snap, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md fetched: only the files the assembly links are needed")
	}
	if _, err := os.Stat(filepath.Join(cache, "models--incoai--Qwen3.8-27B-DFlash2", "refs", "main")); !os.IsNotExist(err) {
		t.Errorf("refs/main written (%v): the commit is not main's", err)
	}
	for _, p := range asked {
		if filepath.Base(filepath.Dir(p)) == "main" || filepath.Base(p) == "main" {
			t.Errorf("asked %s: the draft must come at its recorded commit", p)
		}
	}
	if _, ok := missingDraft(link); ok {
		t.Error("missingDraft still reports the draft after fetchDraft")
	}
}

// A model whose draft is in the cache, or that has none, needs nothing.
func TestMissingDraftNone(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "models--a--d", "snapshots", "0123456789012345678901234567890123456789", "config.json")
	if err := os.MkdirAll(filepath.Dir(present), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(present, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	withDraft := filepath.Join(dir, "with")
	writeManifest(t, withDraft, map[string]any{
		"files":   map[string]any{"draft/config.json": map[string]any{"bytes": 2, "path": present}},
		"sources": map[string]any{"draft": map[string]any{"repo": "a/d", "revision": "0123456789012345678901234567890123456789"}},
	})
	without := filepath.Join(dir, "without")
	writeManifest(t, without, map[string]any{"files": map[string]any{}, "sources": map[string]any{}})
	for _, link := range []string{withDraft, without, filepath.Join(dir, "absent")} {
		if d, ok := missingDraft(link); ok {
			t.Errorf("missingDraft(%s) = %+v, want nothing missing", link, d)
		}
	}
}

func writeManifest(t *testing.T, dir string, m map[string]any) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitBlobID is the Git blob id of data, as the hub reports it.
func gitBlobID(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
