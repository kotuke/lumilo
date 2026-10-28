package splash

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

const arex = "BoldingBuilds/AREX-2-Abliterated-GGUF:Abliterated-Q4_K_M"

// The refusal of a repository without a vision projector, as the user's
// "splash serve" printed it (no_vision.serve.txt, 2026-10-27) and as
// install/models.py prepare prints it alone (no_vision.prepare.txt,
// recorded the same day with Splash 1.4.1), is FailNoVision, not a missing
// model.
func TestClassifyNoVision(t *testing.T) {
	e := New()
	const line = "error: the GGUF repository has no BF16, F32 or F16 vision projector (no GGUF named mmproj); use --language-only to serve text only"
	for _, name := range []string{"no_vision.serve.txt", "no_vision.prepare.txt"} {
		code, tail := readTail(t, filepath.Join("testdata", "failures", name))
		f := e.ClassifyFailure(code, tail)
		if f.Kind != engine.FailNoVision || f.Line != line || f.Code != 1 {
			t.Errorf("%s: %+v", name, f)
		}
	}
	// An MLX checkpoint without a vision tower reads the same way.
	f := e.ClassifyFailure(1, []string{"error: cannot install a/b: a/b has no vision tower; use --language-only to serve text only"})
	if f.Kind != engine.FailNoVision {
		t.Errorf("mlx: %+v", f)
	}
}

// writeAssembly publishes an assembly for model at link under root.
func writeAssembly(t *testing.T, root, link, model string, vision bool) {
	t.Helper()
	dir := filepath.Join(root, ".resolved", strings.ReplaceAll(model, "/", "_")+filepath.Base(link))
	weights := filepath.Join(root, "hf", "m.gguf")
	write(t, weights, "gguf", 0o644)
	files := map[string]any{"target/m.gguf": map[string]any{"bytes": 100, "path": weights}}
	if vision {
		files["vision/mmproj.gguf"] = map[string]any{"bytes": 10, "path": "/hf/mmproj.gguf"}
	}
	data, _ := json.Marshal(map[string]any{"model": model, "files": files})
	write(t, filepath.Join(dir, "model.json"), string(data), 0o644)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
}

// A model installed for text only lives at a hashed selection link; it is
// listed by its ID without vision, and served with --language-only even
// when the user's NoVision is off.
func TestTextOnlySelection(t *testing.T) {
	root := t.TempDir()
	// The hash "install/models.py --model … --language-only link" printed.
	if got := filepath.Base(textOnlyLink(root, arex)); got != "90230c95af1dc621adaa3e325fbf221c7bc0b5e38802fa360b9a1747ff9d750a" {
		t.Fatalf("textOnlyLink %s", got)
	}
	writeAssembly(t, root, textOnlyLink(root, arex), arex, false)
	// Both installations of one model: listed once, with vision.
	writeAssembly(t, root, plainLink(root, "a/both:Q4"), "a/both:Q4", true)
	writeAssembly(t, root, textOnlyLink(root, "a/both:Q4"), "a/both:Q4", false)

	models, err := listModels(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "a/both:Q4" || models[1].ID != arex {
		t.Fatalf("models %+v", models)
	}
	if m := models[1]; m.Features.Vision || m.Owner != "BoldingBuilds" || m.Name != "AREX-2-Abliterated-GGUF" || m.Variant != "Abliterated-Q4_K_M" {
		t.Errorf("text-only model %+v", m)
	}
	if !models[0].Features.Vision {
		t.Errorf("plain model lost vision: %+v", models[0])
	}

	e := New()
	e.modelsDir = root
	has := func(model string, noVision bool) bool {
		argv, _ := e.ServeCommand(engine.Config{Model: model, Port: 8000, NoVision: noVision})
		return strings.Contains(strings.Join(argv, " "), "--language-only")
	}
	if !has(arex, false) {
		t.Error("text-only model served without --language-only")
	}
	if has("a/both:Q4", false) || !has("a/both:Q4", true) {
		t.Error("plain model: --language-only should follow NoVision")
	}
}

// fakeLibexec makes a libexec whose python3 is a shell script standing in
// for install/models.py: it records its arguments, then acts as $FAKE_MODE
// says.
func fakeLibexec(t *testing.T) (lib, argsFile string) {
	t.Helper()
	lib = t.TempDir()
	argsFile = filepath.Join(lib, "args")
	write(t, filepath.Join(lib, "install", "models.py"), "# fake\n", 0o644)
	script := `#!/bin/sh
echo "$@" > "` + argsFile + `"
while [ $# -gt 0 ]; do
  case "$1" in
    --models) MODELS="$2"; shift ;;
    --model) MODEL="$2"; shift ;;
  esac
  shift
done
case "$FAKE_MODE" in
ok)
  echo "Installing $MODEL as Qwen3.8-27B (gguf); draft incoai/Qwen3.8-27B-DFlash2; vision enabled."
  echo "Fetching 1 file(s), 0.000002 GB, from $MODEL@0123456789ab; cached files are reused."
  mkdir -p "$FAKE_HUB/models--a--b/blobs"
  head -c 2000 /dev/zero > "$FAKE_HUB/models--a--b/blobs/abc"
  sleep 1.2
  mkdir -p "$(dirname "$FAKE_LINK")" "$MODELS/.resolved/x"
  echo '{"model":"'"$MODEL"'","files":{"target/m.gguf":{"bytes":2000,"path":"'"$FAKE_HUB/models--a--b/blobs/abc"'"}}}' > "$MODELS/.resolved/x/model.json"
  ln -s "$MODELS/.resolved/x" "$FAKE_LINK"
  exit 0 ;;
novision)
  echo "No GGUF is named for :Abliterated-Q4_K_M alone; using AREX-2-Abliterated-Q4_K_M.gguf, the only one whose name ends in -Abliterated-Q4_K_M."
  echo "error: the GGUF repository has no BF16, F32 or F16 vision projector (no GGUF named mmproj); use --language-only to serve text only" >&2
  exit 1 ;;
hang)
  trap 'echo stopped >&2; exit 130' INT
  echo "Fetching 1 file(s), 21.24 GB, from $MODEL@0123456789ab; cached files are reused."
  mkdir -p "$FAKE_HUB/models--a--b/blobs"
  : > "$FAKE_HUB/models--a--b/blobs/abc.xyz.incomplete"
  while :; do sleep 0.1; done ;;
esac
exit 3
`
	write(t, filepath.Join(lib, "python", "bin", "python3"), script, 0o755)
	return lib, argsFile
}

func newDownloadSplash(t *testing.T) (*Splash, string) {
	t.Helper()
	lib, args := fakeLibexec(t)
	e := New()
	e.libexecDir = lib
	e.modelsDir = filepath.Join(t.TempDir(), "models")
	e.runtimeDir = filepath.Join(t.TempDir(), "runtime")
	e.hubDirs = []string{t.TempDir()}
	t.Setenv("FAKE_HUB", e.hubDirs[0])
	return e, args
}

// progressLog keeps the progress calls.
type progressLog struct {
	mu    sync.Mutex
	calls [][2]int64
}

func (p *progressLog) add(done, total int64) {
	p.mu.Lock()
	p.calls = append(p.calls, [2]int64{done, total})
	p.mu.Unlock()
}

func (p *progressLog) last() [2]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return [2]int64{}
	}
	return p.calls[len(p.calls)-1]
}

func TestDownloadInstalls(t *testing.T) {
	e, args := newDownloadSplash(t)
	t.Setenv("FAKE_MODE", "ok")
	t.Setenv("FAKE_LINK", textOnlyLink(e.modelsDir, "a/b:Q4"))
	var p progressLog
	id, err := e.Download(context.Background(), engine.DownloadRequest{Model: "a/b:Q4", NoVision: true}, p.add)
	if err != nil {
		t.Fatal(err)
	}
	if id != "a/b:Q4" {
		t.Errorf("id %q", id)
	}
	got, _ := os.ReadFile(args)
	want := "-u " + filepath.Join(e.libexecDir, "install", "models.py") + " --models " + e.modelsDir + " --model a/b:Q4 --language-only prepare\n"
	if string(got) != want {
		t.Errorf("args\n got %q\nwant %q", got, want)
	}
	// The total is the "Fetching" line's; the bytes the hub cache's growth.
	if l := p.last(); l != [2]int64{2000, 2000} {
		t.Errorf("last progress %v (all %v)", l, p.calls)
	}
	models, _ := e.Models(context.Background())
	if len(models) != 1 || models[0].ID != "a/b:Q4" || models[0].Features.Vision {
		t.Errorf("models after download: %+v", models)
	}
}

func TestDownloadNoVision(t *testing.T) {
	e, _ := newDownloadSplash(t)
	t.Setenv("FAKE_MODE", "novision")
	_, err := e.Download(context.Background(), engine.DownloadRequest{Model: arex}, nil)
	var f engine.Failure
	if !errors.As(err, &f) || f.Kind != engine.FailNoVision || f.Code != 1 {
		t.Fatalf("err %v (%+v)", err, f)
	}
}

func TestDownloadCancel(t *testing.T) {
	e, _ := newDownloadSplash(t)
	t.Setenv("FAKE_MODE", "hang")
	since := time.Now().Add(-time.Second)
	// Another download's temporary file, there before this one began.
	other := filepath.Join(e.hubDirs[0], "models--c--d", "blobs", "other.xyz.incomplete")
	write(t, other, "", 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	var p progressLog
	done := make(chan error, 1)
	go func() {
		_, err := e.Download(ctx, engine.DownloadRequest{Model: "a/b:Q4"}, p.add)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for p.last()[1] == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if l := p.last(); l[1] != 21_240_000_000 {
		t.Fatalf("total %v", l)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(dlStopWait + 2*time.Second):
		t.Fatal("cancel did not stop the installer")
	}
	// The run's own temporary file is gone already: no rerun resumes it.
	part := filepath.Join(e.hubDirs[0], "models--a--b", "blobs", "abc.xyz.incomplete")
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Errorf("the cancelled run kept its temporary file: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another download's file was removed: %v", err)
	}
	write(t, part, "", 0o644)
	old := filepath.Join(e.hubDirs[0], "models--a--b", "blobs", "old.xyz.incomplete")
	write(t, old, "", 0o644)
	if err := os.Chtimes(old, since.Add(-time.Hour), since.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.DropPartial("a/b:Q4", since)
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Errorf("partial file kept: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("an older download's file was removed: %v", err)
	}
}

// A Splash being upgraded holds serve.lock exclusively: no install then.
func TestDownloadWhileUpgrading(t *testing.T) {
	e, _ := newDownloadSplash(t)
	unlock, err := e.shareInstall()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	f, err := os.OpenFile(filepath.Join(e.runtimeDir, "serve.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := flockEx(f); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Download(context.Background(), engine.DownloadRequest{Model: "a/b"}, nil); err == nil ||
		!strings.Contains(err.Error(), "being upgraded") {
		t.Errorf("err %v", err)
	}
}

func flockEx(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
