package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemove(t *testing.T) {
	hub := t.TempDir()
	flat := t.TempDir()
	repo := filepath.Join(hub, "models--acme--Tiny-1B-GGUF")
	link := func(rev, name, blob string, content []byte) string {
		b := filepath.Join(repo, "blobs", blob)
		write(t, b, content, 0o644)
		p := filepath.Join(repo, "snapshots", rev, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(strings.Repeat("../", 2+strings.Count(name, "/"))+"blobs/"+blob, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write(t, filepath.Join(repo, "refs", "main"), []byte("r"), 0o644)
	q4 := link("r", "Tiny-1B-Q4_K_M.gguf", "q4", textModel("llama"))
	// An older snapshot holds Q4 too, another blob: listed once Q4 of r
	// went, unless it goes with it.
	q4old := link("old", "Tiny-1B-Q4_K_M.gguf", "q4old", append(textModel("llama"), 0))
	q8 := link("r", "Tiny-1B-Q8_0.gguf", "q8", textModel("llama"))
	mm := link("r", "mmproj-Tiny-1B-F16.gguf", "mm", ggufFile([]kv{{"general.architecture", "clip"}, {"general.type", "mmproj"}}, 10))
	split1 := link("r", "Q8/Tiny-1B-Q8-00001-of-00002.gguf", "s1", textModel("llama"))
	split2 := link("r", "Q8/Tiny-1B-Q8-00002-of-00002.gguf", "s2", make([]byte, 50))
	gemma := filepath.Join(flat, "gemma-Q4.gguf")
	write(t, gemma, textModel("gemma3"), 0o644)
	other := filepath.Join(flat, "notes.gguf.txt")
	write(t, other, []byte("x"), 0o644)

	e := New()
	e.hubDirs, e.flatDirs = []string{hub}, []string{flat}
	ctx := context.Background()
	gone := func(p string) bool { _, err := os.Lstat(p); return os.IsNotExist(err) }

	// Not a listed model: a path that is not one is never deleted.
	for _, id := range []string{other, filepath.Join(repo, "blobs", "q4"), split2, mm} {
		if err := e.Remove(ctx, id); err == nil || gone(id) {
			t.Errorf("Remove(%s) = %v", id, err)
		}
	}

	// Q4 has a neighbour: the vision projector stays for Q8.
	plan, err := e.PlanRemove(ctx, q4)
	if err != nil || plan.Bytes != int64(2*len(textModel("llama"))+1) || !plan.Shared {
		t.Errorf("plan of Q4 = %+v, %v", plan, err)
	}
	if err := e.Remove(ctx, q4); err != nil {
		t.Fatal(err)
	}
	if !gone(q4) || !gone(filepath.Join(repo, "blobs", "q4")) || gone(mm) {
		t.Error("Q4: its file stayed, or the shared projector went")
	}
	if !gone(q4old) || !gone(filepath.Join(repo, "blobs", "q4old")) || !gone(filepath.Join(repo, "snapshots", "old")) {
		t.Error("Q4 of the older snapshot stayed")
	}

	// A split model: every part goes; the folder of its parts too.
	if err := e.Remove(ctx, split1); err != nil {
		t.Fatal(err)
	}
	if !gone(split2) || !gone(filepath.Join(repo, "blobs", "s2")) || !gone(filepath.Dir(split1)) {
		t.Error("split: a part stayed")
	}

	// The last model of the snapshot takes its projector, then the whole
	// repository goes.
	if err := e.Remove(ctx, q8); err != nil {
		t.Fatal(err)
	}
	if !gone(mm) || !gone(repo) {
		t.Error("the last model left its projector or its repository")
	}

	// A flat file goes alone.
	plan, _ = e.PlanRemove(ctx, gemma)
	if plan.Shared || plan.Bytes != int64(len(textModel("gemma3"))) {
		t.Errorf("flat plan %+v", plan)
	}
	if err := e.Remove(ctx, gemma); err != nil || !gone(gemma) || gone(other) {
		t.Errorf("flat: %v", err)
	}
	if ms, _ := e.Models(ctx); len(ms) != 0 {
		t.Errorf("models left: %+v", ms)
	}
}
