package splash

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// header is a GGUF v3 header of a text model of arch, padded to size bytes.
func header(arch, name string, size int) []byte {
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { le(uint64(len(s))); b.WriteString(s) }
	kvs := [][2]string{{"general.architecture", arch}, {"general.type", "model"}, {"general.name", name}}
	b.WriteString("GGUF")
	le(uint32(3))
	le(uint64(0))
	le(uint64(len(kvs) + 1))
	for _, kv := range kvs {
		str(kv[0])
		le(uint32(8))
		str(kv[1])
	}
	str(arch + ".context_length")
	le(uint32(4))
	le(uint32(32768))
	if pad := size - b.Len(); pad > 0 {
		b.Write(make([]byte, pad))
	}
	return b.Bytes()
}

// snapshot writes files into the refs/main snapshot of OWNER/REPO in hub.
func snapshot(t *testing.T, hub, repo string, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(hub, "models--"+filepath.Dir(repo)+"--"+filepath.Base(repo))
	snap := filepath.Join(dir, "snapshots", "c0ffee")
	if err := os.MkdirAll(snap, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "refs", "main"), []byte("c0ffee"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(snap, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return snap
}

// install publishes an assembly of the GGUF at path as Splash's model id.
func install(t *testing.T, models, id, path string) {
	t.Helper()
	asm := filepath.Join(models, ".resolved", filepath.Base(id))
	if err := os.MkdirAll(asm, 0o755); err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(map[string]any{"model": id, "files": map[string]any{
		"target/" + filepath.Base(path): map[string]any{"bytes": 1000, "path": path}}})
	if err := os.WriteFile(filepath.Join(asm, "model.json"), js, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(models, filepath.FromSlash(id))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(asm, link); err != nil {
		t.Fatal(err)
	}
}

// Adoptable offers the GGUFs of the hub cache that Splash serves and has
// not installed, under the ID Download takes.
func TestAdoptable(t *testing.T) {
	e := New()
	hub := t.TempDir()
	e.hubDirs, e.modelsDir = []string{hub}, t.TempDir()

	snapshot(t, hub, "unsloth/Qwen3.8-27B-GGUF", map[string][]byte{
		"Qwen3.8-27B-UD-Q4_K_M.gguf": header("qwen35", "Qwen3.8-27B", 3000),
		"Qwen3.8-27B-BF16.gguf":      header("qwen35", "Qwen3.8-27B", 5000), // Splash refuses BF16
		"mmproj-BF16.gguf":           header("clip", "Qwen3.8-27B", 100),
	})
	snapshot(t, hub, "unsloth/Qwen3.6-35B-A3B-GGUF", map[string][]byte{
		"Qwen3.6-35B-A3B-Q4_K_M-00001-of-00002.gguf": header("qwen35moe", "Qwen3.6-35B-A3B", 2000),
		"Qwen3.6-35B-A3B-Q4_K_M-00002-of-00002.gguf": make([]byte, 1500),
		"Qwen3.6-35B-A3B-Q6_K-00001-of-00002.gguf":   header("qwen35moe", "Qwen3.6-35B-A3B", 2000), // part 2 missing
	})
	snapshot(t, hub, "ggml-org/gemma-4-GGUF", map[string][]byte{
		"gemma-4-Q4_K_M.gguf": header("gemma4", "gemma-4", 2000), // not a Splash family
	})
	done := snapshot(t, hub, "orcarouter/OrcaCyber-Nano-1.0", map[string][]byte{
		"OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf": header("qwen35", "OrcaCyber Nano 1.0 AI Defense", 2000),
	})
	// Installed under another spelling of its tag: the same file.
	install(t, e.modelsDir, "orcarouter/OrcaCyber-Nano-1.0:AI-Defense-Mix-15.7G",
		filepath.Join(done, "OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf"))

	got, err := e.Adoptable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		size   int64
		vision bool
	}{
		"unsloth/Qwen3.6-35B-A3B-GGUF:Q4_K_M": {3500, false},
		"unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M":  {3000, true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d models: %+v", len(got), got)
	}
	for _, m := range got {
		w, ok := want[m.ID]
		if !ok {
			t.Errorf("unexpected %s", m.ID)
			continue
		}
		if m.SizeBytes != w.size || m.Features.Vision != w.vision || m.ContextLength != 32768 {
			t.Errorf("%s: size %d vision %v context %d, want %d %v", m.ID, m.SizeBytes, m.Features.Vision,
				m.ContextLength, w.size, w.vision)
		}
		if _, err := e.ParseModel(m.ID); err != nil {
			t.Errorf("Download would refuse %s: %v", m.ID, err)
		}
	}
}
