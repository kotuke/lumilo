package splash

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// removeFixture lays out Splash's models directory as Splash 1.4.1 does:
// two models of one family share its DFlash2 draft and a metadata entry;
// one of them has a text-only selection too.
type removeFixture struct {
	e                *Splash
	root, hub        string
	arex, arexText   string // assemblies of bart/AREX-GGUF:Q4
	nano             string // assembly of orc/Nano:Mix
	arexRepo, draft  string // hub repository folders
	metaArex, shared string // .metadata entries
}

func newRemoveFixture(t *testing.T) *removeFixture {
	t.Helper()
	base := t.TempDir()
	f := &removeFixture{root: filepath.Join(base, "models"), hub: filepath.Join(base, "hub")}
	f.e = New()
	f.e.modelsDir, f.e.runtimeDir, f.e.hubDirs = f.root, filepath.Join(base, "runtime"), []string{f.hub}

	hubFile := func(repo, commit, name, data string) string {
		dir := filepath.Join(f.hub, "models--"+strings.ReplaceAll(repo, "/", "--"))
		sum := strings.Repeat(string(rune('a'+len(data)%26)), 4) + name
		write(t, filepath.Join(dir, "blobs", sum), data, 0o644)
		write(t, filepath.Join(dir, "refs", "main"), commit, 0o644)
		link := filepath.Join(dir, "snapshots", commit, name)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../blobs/"+sum, link); err != nil {
			t.Fatal(err)
		}
		return link
	}
	arexGGUF := hubFile("bart/AREX-GGUF", "c1", "AREX-Q4.gguf", strings.Repeat("w", 1000))
	// A newer snapshot links the same blob (llama.cpp downloaded it later).
	newer := filepath.Join(filepath.Dir(filepath.Dir(arexGGUF)), "c9", "AREX-Q4.gguf")
	if err := os.MkdirAll(filepath.Dir(newer), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mustReadlink(t, arexGGUF), newer); err != nil {
		t.Fatal(err)
	}
	nanoGGUF := hubFile("orc/Nano", "c2", "Nano-Mix.gguf", strings.Repeat("n", 700))
	draft := hubFile("incoai/Q-DFlash2", "d1", "model.safetensors", strings.Repeat("d", 300))
	f.arexRepo, f.draft = filepath.Dir(filepath.Dir(filepath.Dir(arexGGUF))), filepath.Dir(filepath.Dir(filepath.Dir(draft)))

	meta := filepath.Join(f.root, ".metadata")
	f.metaArex, f.shared = filepath.Join(meta, "m1"), filepath.Join(meta, "m2")
	write(t, filepath.Join(f.metaArex, "config.json"), "{}", 0o644)
	write(t, filepath.Join(f.shared, "tokenizer.json"), "{}", 0o644)

	assembly := func(hash, model string, files map[string]string) string {
		dir := filepath.Join(f.root, ".resolved", hash)
		recs := map[string]any{}
		for name, src := range files {
			recs[name] = map[string]any{"bytes": 1, "path": src}
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(src, p); err != nil {
				t.Fatal(err)
			}
		}
		data, _ := json.Marshal(map[string]any{"model": model, "files": recs})
		write(t, filepath.Join(dir, "model.json"), string(data), 0o644)
		return dir
	}
	link := func(l, target string) {
		if err := os.MkdirAll(filepath.Dir(l), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, l); err != nil {
			t.Fatal(err)
		}
	}
	f.arex = assembly("a1", "bart/AREX-GGUF:Q4", map[string]string{
		"target/AREX-Q4.gguf": arexGGUF, "config.json": filepath.Join(f.metaArex, "config.json"),
		"tokenizer/tokenizer.json": filepath.Join(f.shared, "tokenizer.json"), "draft/model.safetensors": draft})
	f.arexText = assembly("a2", "bart/AREX-GGUF:Q4", map[string]string{"target/AREX-Q4.gguf": arexGGUF})
	f.nano = assembly("b1", "orc/Nano:Mix", map[string]string{
		"target/Nano-Mix.gguf": nanoGGUF, "tokenizer/tokenizer.json": filepath.Join(f.shared, "tokenizer.json"),
		"draft/model.safetensors": draft})
	link(plainLink(f.root, "bart/AREX-GGUF:Q4"), f.arex)
	link(textOnlyLink(f.root, "bart/AREX-GGUF:Q4"), f.arexText)
	link(plainLink(f.root, "orc/Nano:Mix"), f.nano)
	return f
}

func mustReadlink(t *testing.T, p string) string {
	t.Helper()
	l, err := os.Readlink(p)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func gone(p string) bool {
	_, err := os.Lstat(p)
	return os.IsNotExist(err)
}

func TestRemove(t *testing.T) {
	f := newRemoveFixture(t)
	ctx := context.Background()

	plan, err := f.e.PlanRemove(ctx, "bart/AREX-GGUF:Q4")
	if err != nil {
		t.Fatal(err)
	}
	size := func(p string) int64 { st, _ := os.Stat(p); return st.Size() }
	want := 1000 + size(filepath.Join(f.arex, "model.json")) + size(filepath.Join(f.arexText, "model.json")) +
		size(filepath.Join(f.metaArex, "config.json")) + size(filepath.Join(f.arexRepo, "refs", "main"))
	if plan.Bytes != want || !plan.Shared {
		t.Errorf("plan %+v, want %d bytes: the AREX weights and its own small files, not the shared draft", plan, want)
	}
	if err := f.e.Remove(ctx, "bart/AREX-GGUF:Q4"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{plainLink(f.root, "bart/AREX-GGUF:Q4"), textOnlyLink(f.root, "bart/AREX-GGUF:Q4"),
		filepath.Join(f.root, "bart"), f.arex, f.arexText, f.metaArex, f.arexRepo} {
		if !gone(p) {
			t.Errorf("%s stayed", p)
		}
	}
	for _, p := range []string{plainLink(f.root, "orc/Nano:Mix"), f.nano, f.shared, f.draft,
		filepath.Join(f.draft, "snapshots", "d1", "model.safetensors"), filepath.Join(f.root, ".selections")} {
		if gone(p) {
			t.Errorf("%s went", p)
		}
	}
	models, err := f.e.Models(ctx)
	if err != nil || len(models) != 1 || models[0].ID != "orc/Nano:Mix" {
		t.Errorf("models %+v, %v", models, err)
	}

	// The last model of the family takes the draft and the metadata.
	if err := f.e.Remove(ctx, "orc/Nano:Mix"); err != nil {
		t.Fatal(err)
	}
	if !gone(f.draft) || !gone(f.shared) || !gone(f.nano) {
		t.Error("the draft, the metadata or the assembly stayed")
	}
}

func TestRemoveRefuses(t *testing.T) {
	f := newRemoveFixture(t)
	ctx := context.Background()
	if err := f.e.Remove(ctx, "bart/AREX-GGUF:Q8"); err == nil {
		t.Error("a model that is not installed was accepted")
	}

	// A server serves Nano: its assembly is held, nothing goes.
	rec, err := os.Open(filepath.Join(f.nano, "model.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(rec.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	if err := f.e.Remove(ctx, "orc/Nano:Mix"); err == nil || gone(plainLink(f.root, "orc/Nano:Mix")) {
		t.Errorf("held: %v", err)
	}
	// It counts all the same: our server, which holds it now, stops
	// before the deletion.
	if plan, err := f.e.PlanRemove(ctx, "orc/Nano:Mix"); err != nil || plan.Bytes == 0 {
		t.Errorf("held plan: %+v, %v", plan, err)
	}
	rec.Close()

	// Another installation holds the models directory.
	lock, err := os.OpenFile(filepath.Join(f.root, ".install.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := f.e.Remove(ctx, "orc/Nano:Mix"); err == nil || gone(f.nano) {
		t.Errorf("locked: %v", err)
	}
}
