package enginekit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hubFixture writes files into a hub cache under root: blobs by name
// ("a" → blobs/a with that many bytes as its content's length) and
// snapshot links ("c1/x.gguf" → "a").
func hubFixture(t *testing.T, root, repo string, blobs map[string]string, links map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, RepoFolder(repo))
	for name, data := range blobs {
		writeFile(t, filepath.Join(dir, "blobs", name), data)
	}
	for l, blob := range links {
		p := filepath.Join(dir, "snapshots", filepath.FromSlash(l))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		target := strings.Repeat("../", 1+strings.Count(l, "/")) + "blobs/" + blob
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, "refs", "main"), "c1")
	return dir
}

func writeFile(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestRemovalKeepsSharedBlob(t *testing.T) {
	root := t.TempDir()
	// Two variants; the vision projector is one blob that both
	// snapshots link.
	dir := hubFixture(t, root, "o/r", map[string]string{"q4": "1234", "q8": "12345678", "mm": "12"},
		map[string]string{"c1/m-Q4.gguf": "q4", "c1/mmproj.gguf": "mm", "c2/Q8/m-Q8.gguf": "q8", "c2/mmproj.gguf": "mm"})

	var r Removal
	for _, f := range []string{"c2/Q8/m-Q8.gguf", "c2/mmproj.gguf"} {
		if err := r.AddHubFile(filepath.Join(dir, "snapshots", filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := r.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Bytes != 8 || !plan.Shared {
		t.Errorf("plan %+v, want 8 bytes of q8 only, shared", plan)
	}
	if !exists(filepath.Join(dir, "blobs", "q8")) {
		t.Fatal("Plan deleted a file")
	}
	if err := r.Apply(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"blobs/q8", "snapshots/c2"} {
		if exists(filepath.Join(dir, p)) {
			t.Errorf("%s stayed", p)
		}
	}
	for _, p := range []string{"blobs/q4", "blobs/mm", "snapshots/c1/m-Q4.gguf", "snapshots/c1/mmproj.gguf", "refs/main"} {
		if !exists(filepath.Join(dir, p)) {
			t.Errorf("%s went", p)
		}
	}
}

func TestRemovalLastVariantTakesRepo(t *testing.T) {
	root := t.TempDir()
	dir := hubFixture(t, root, "o/r", map[string]string{"q4": "1234"}, map[string]string{"c1/m-Q4.gguf": "q4"})
	writeFile(t, filepath.Join(dir, ".no_exist", "c1", "mmproj.gguf"), "")

	var r Removal
	if err := r.AddHubFile(filepath.Join(dir, "snapshots", "c1", "m-Q4.gguf")); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(); err != nil {
		t.Fatal(err)
	}
	if exists(dir) {
		t.Errorf("the repository folder stayed")
	}
	if !exists(root) {
		t.Errorf("the cache went")
	}
}

func TestRemovalKeepsRepoWithPartialBlob(t *testing.T) {
	root := t.TempDir()
	// Another variant is half downloaded: its partial blob stays, and so
	// does the folder that holds it.
	dir := hubFixture(t, root, "o/r", map[string]string{"q4": "1234", "q8" + partSuffix: "12"},
		map[string]string{"c1/m-Q4.gguf": "q4"})

	var r Removal
	if err := r.AddHubFile(filepath.Join(dir, "snapshots", "c1", "m-Q4.gguf")); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "blobs", "q4")) || exists(filepath.Join(dir, "snapshots", "c1")) {
		t.Error("the variant stayed")
	}
	if !exists(filepath.Join(dir, "blobs", "q8"+partSuffix)) {
		t.Error("the partial blob went")
	}
}

func TestRemovalRefusesPathOutsideSnapshot(t *testing.T) {
	root := t.TempDir()
	dir := hubFixture(t, root, "o/r", map[string]string{"q4": "1234"}, map[string]string{"c1/m-Q4.gguf": "q4"})
	var r Removal
	for _, p := range []string{filepath.Join(dir, "blobs", "q4"), filepath.Join(dir, "snapshots", "c1"), filepath.Join(root, "x.gguf")} {
		if err := r.AddHubFile(p); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
}

func TestRemovalTreeAndFiles(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "asm")
	writeFile(t, filepath.Join(tree, "model.json"), "{}")
	writeFile(t, filepath.Join(root, "outside"), "123456")
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(tree, "link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "flat", "m.gguf"), "123")

	var r Removal
	if err := r.AddTree(tree); err != nil {
		t.Fatal(err)
	}
	r.AddFile(filepath.Join(root, "flat", "m.gguf"))
	r.AddFile(filepath.Join(root, "flat", "missing.gguf"))
	plan, _ := r.Plan()
	if plan.Bytes != 5 || plan.Shared {
		t.Errorf("plan %+v, want 5 bytes (model.json and m.gguf), not shared", plan)
	}
	if err := r.Apply(); err != nil {
		t.Fatal(err)
	}
	if exists(tree) || exists(filepath.Join(root, "flat", "m.gguf")) {
		t.Error("files stayed")
	}
	if !exists(filepath.Join(root, "outside")) || !exists(filepath.Join(root, "flat")) {
		t.Error("a link was followed, or a directory not asked for went")
	}
}
