package tensorfold

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

const testCommit = "93760be4f1f69842a46bc13dbdc0f19e291392a3"

// fakeHub answers the requests Download makes: the revision with
// checksums and the files at the commit. A *.safetensors or *.gguf file is
// an LFS file (sha256); the others are plain Git files (blobId).
type fakeHub struct {
	repos map[string]map[string][]byte // "owner/repo" -> path -> content
	mu    sync.Mutex
	gets  []string // "repo/path"
}

func (h *fakeHub) start(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for repo, files := range h.repos {
			switch {
			case r.URL.Path == "/api/models/"+repo+"/revision/main":
				type lfs struct {
					SHA256 string `json:"sha256"`
					Size   int    `json:"size"`
				}
				type sib struct {
					Name   string `json:"rfilename"`
					Size   int    `json:"size"`
					BlobID string `json:"blobId"`
					LFS    *lfs   `json:"lfs,omitempty"`
				}
				out := struct {
					SHA      string `json:"sha"`
					Siblings []sib  `json:"siblings"`
				}{SHA: testCommit}
				for name, data := range files {
					g := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(data), data)))
					s := sib{Name: name, Size: len(data), BlobID: hex.EncodeToString(g[:])}
					if strings.HasSuffix(name, ".safetensors") || strings.HasSuffix(name, ".gguf") {
						sum := sha256.Sum256(data)
						s.LFS = &lfs{hex.EncodeToString(sum[:]), len(data)}
					}
					out.Siblings = append(out.Siblings, s)
				}
				_ = json.NewEncoder(w).Encode(out)
				return
			case strings.HasPrefix(r.URL.Path, "/"+repo+"/resolve/"+testCommit+"/"):
				name := strings.TrimPrefix(r.URL.Path, "/"+repo+"/resolve/"+testCommit+"/")
				data, ok := files[name]
				if !ok {
					http.NotFound(w, r)
					return
				}
				h.mu.Lock()
				h.gets = append(h.gets, repo+"/"+name)
				h.mu.Unlock()
				_, _ = w.Write(data)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func dlEngine(t *testing.T, h *fakeHub) (*TensorFold, string) {
	cache := t.TempDir()
	tf := newTest(t, cache)
	tf.hubURL = h.start(t)
	return tf, cache
}

func TestDownload(t *testing.T) {
	h := &fakeHub{repos: map[string]map[string][]byte{
		fixtureRepo: {
			"config.json":            []byte(config(t, "qwen3.5-2b-mlx-4bit")),
			"tokenizer.json":         []byte(`{"tok":1}`),
			"model.safetensors":      []byte("weights!"),
			"extra/notes.txt":        []byte("left out: not in the root"),
			"Qwen3.5-2B-Q4_K_M.gguf": []byte("left out: a GGUF"),
		},
	}}
	tf, cache := dlEngine(t, h)
	var last [2]int64
	id, err := tf.Download(context.Background(), engine.DownloadRequest{Model: fixtureRepo},
		func(done, total int64) { last = [2]int64{done, total} })
	if err != nil {
		t.Fatal(err)
	}
	if id != fixtureRepo {
		t.Errorf("id = %s", id)
	}
	slices.Sort(h.gets)
	want := []string{fixtureRepo + "/config.json", fixtureRepo + "/config.json", fixtureRepo + "/model.safetensors", fixtureRepo + "/tokenizer.json"}
	if !slices.Equal(h.gets, want) {
		t.Errorf("fetched %q, want %q (config.json first for the check)", h.gets, want)
	}
	n := int64(len(h.repos[fixtureRepo]["config.json"]) + len(`{"tok":1}`) + len("weights!"))
	if last != [2]int64{n, n} {
		t.Errorf("progress = %v, want %d", last, n)
	}
	root := filepath.Join(cache, "models--mlx-community--Qwen3.5-2B-MLX-4bit")
	if b, _ := os.ReadFile(filepath.Join(root, "refs", "main")); string(b) != testCommit {
		t.Errorf("refs/main = %q", b)
	}
	models, err := tf.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != fixtureRepo || models[0].SizeBytes != int64(len("weights!")) {
		t.Fatalf("models = %+v, %v", models, err)
	}
}

// A 27B-class model comes with its DFlash2 drafter, which Models then
// offers as the default draft; progress counts both.
func TestDownloadWithDrafter(t *testing.T) {
	model, draft := []byte("model weights"), []byte(strings.Repeat("d", 128))
	h := &fakeHub{repos: map[string]map[string][]byte{
		"TensorFold/Qwen3.8-27B-MLX-4bit": {
			"config.json":       []byte(config(t, "qwen3.8-27b-mlx-4bit")),
			"model.safetensors": model,
		},
		qwen27Drafter: {
			"config.json":       []byte(config(t, "qwen3.8-27b-dflash2")),
			"model.safetensors": draft,
		},
	}}
	tf, _ := dlEngine(t, h)
	var steps [][2]int64
	id, err := tf.Download(context.Background(), engine.DownloadRequest{Model: "TensorFold/Qwen3.8-27B-MLX-4bit"},
		func(done, total int64) { steps = append(steps, [2]int64{done, total}) })
	if err != nil {
		t.Fatal(err)
	}
	total := int64(len(h.repos["TensorFold/Qwen3.8-27B-MLX-4bit"]["config.json"]) + len(model) +
		len(h.repos[qwen27Drafter]["config.json"]) + len(draft))
	for i, s := range steps {
		if s[1] != total || (i > 0 && s[0] < steps[i-1][0]) {
			t.Fatalf("progress %v: total %d, not growing", steps, total)
		}
	}
	if last := steps[len(steps)-1]; last[0] != total {
		t.Errorf("last progress %v, want %d", last, total)
	}
	models, err := tf.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != id {
		t.Fatalf("models = %+v, %v", models, err)
	}
	if d := models[0].Features.Drafts; len(d) != 1 || d[0].ID != qwen27Drafter || !d[0].Default || d[0].SizeBytes != 57 {
		t.Errorf("drafts = %+v", d)
	}
	// A drafter alone is refused: it comes with its model.
	_, err = tf.Download(context.Background(), engine.DownloadRequest{Model: qwen27Drafter}, nil)
	var f engine.Failure
	if !errors.As(err, &f) || f.Kind != engine.FailModel || !strings.Contains(f.Line, "comes with its model") {
		t.Errorf("drafter alone: %v", err)
	}
}

func TestDownloadRefused(t *testing.T) {
	h := &fakeHub{repos: map[string]map[string][]byte{
		"LiquidAI/LFM3-1.7B-MLX-4bit": {
			"config.json":       []byte(config(t, "lfm3-1.7b-mlx-4bit")),
			"model.safetensors": []byte("weights"),
		},
		"acme/NoConfig": {"model.safetensors": []byte("w")},
	}}
	tf, _ := dlEngine(t, h)
	for model, want := range map[string]string{
		"LiquidAI/LFM3-1.7B-MLX-4bit": `does not serve model_type "lfm3"`,
		"acme/NoConfig":               "has no config.json",
		"acme/Missing":                "404",
		"not a repo":                  "is not OWNER/REPO",
	} {
		_, err := tf.Download(context.Background(), engine.DownloadRequest{Model: model}, nil)
		var f engine.Failure
		if !errors.As(err, &f) || f.Kind != engine.FailModel || !strings.Contains(f.Line, want) {
			t.Errorf("%s: %v, want FailModel with %q", model, err, want)
		}
	}
	for _, g := range h.gets {
		if strings.HasSuffix(g, ".safetensors") {
			t.Errorf("fetched %s of a refused model", g)
		}
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	putRepo(t, dir, "TensorFold/Qwen3.8-27B-MLX-4bit", "c27",
		hubFile{"config.json", config(t, "qwen3.8-27b-mlx-4bit")}, hubFile{"model.safetensors", "1234567890"})
	putRepo(t, dir, "TensorFold/Qwen3.8-27B-MLX-8bit", "c28",
		hubFile{"config.json", strings.Replace(config(t, "qwen3.8-27b-mlx-4bit"), `"bits": 4`, `"bits": 8`, 1)},
		hubFile{"model.safetensors", "12345678901234567890"})
	putRepo(t, dir, "z-lab/Qwen3.8-27B-DFlash2", "cdf",
		hubFile{"config.json", config(t, "qwen3.8-27b-dflash2")}, hubFile{"model.safetensors", strings.Repeat("d", 128)})
	putRepo(t, dir, fixtureRepo, "c2",
		hubFile{"config.json", config(t, "qwen3.5-2b-mlx-4bit")}, hubFile{"model.safetensors", "12"})
	tf := newTest(t, dir)
	ctx := context.Background()

	// The drafter is shared with the 8-bit model: it stays.
	plan, err := tf.PlanRemove(ctx, "TensorFold/Qwen3.8-27B-MLX-4bit")
	if err != nil {
		t.Fatal(err)
	}
	cfg := int64(len(config(t, "qwen3.8-27b-mlx-4bit")))
	// The config, the weights and refs/main ("c27"): the folder goes.
	if want := cfg + 10 + 3; plan.Bytes != want || !plan.Shared {
		t.Errorf("plan = %+v, want %d", plan, want)
	}
	if err := tf.Remove(ctx, "TensorFold/Qwen3.8-27B-MLX-4bit"); err != nil {
		t.Fatal(err)
	}
	// The last model it drafts for takes it along.
	if err := tf.Remove(ctx, "TensorFold/Qwen3.8-27B-MLX-8bit"); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"TensorFold/Qwen3.8-27B-MLX-4bit", "TensorFold/Qwen3.8-27B-MLX-8bit", "z-lab/Qwen3.8-27B-DFlash2"} {
		if _, err := os.Stat(filepath.Join(dir, "models--"+strings.ReplaceAll(repo, "/", "--"), "snapshots")); !os.IsNotExist(err) {
			t.Errorf("%s: snapshots left: %v", repo, err)
		}
	}
	models, _ := tf.Models(ctx)
	if len(models) != 1 || models[0].ID != fixtureRepo {
		t.Errorf("models = %+v", models)
	}
	if err := tf.Remove(ctx, "a/NotInstalled"); err == nil {
		t.Error("removed a model that is not installed")
	}
}
