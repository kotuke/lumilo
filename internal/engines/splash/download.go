package splash

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

var _ engine.Downloader = (*Splash)(nil)

const (
	// dlTail is how many output lines ClassifyFailure gets.
	dlTail = 50
	// dlTick is how often the hub cache is measured while files arrive.
	dlTick = time.Second
	// dlStopWait is how long a cancelled install has after SIGINT before
	// it is killed.
	dlStopWait = 5 * time.Second
)

// Download installs a model without serving it: it runs Splash's own
// model installer, the step "splash serve" runs before it serves
// (install/launcher.py _ensure_installed):
//
//	<libexec>/python/bin/python3 -u install/models.py --models <models>
//	    --model OWNER/REPO[:VARIANT] [--language-only] prepare
//
// in libexec, online. It resolves the repository on the Hub, checks the
// model with the engine, downloads the target, its vision projector and
// its DFlash2 draft into the hub cache, and publishes the assembly at the
// model's selection link under the models directory, where Models finds
// it; nothing listens on a port, so a running server is not disturbed.
// Checked on 2026-10-27 with Splash 1.4.1: a model whose repository has no
// vision projector fails in 4 s after reading GGUF headers alone
// (testdata/failures/no_vision.prepare.txt); an installed model exits 0.
//
// Progress: the installer prints "Fetching N file(s), X GB, from …"
// before it downloads (ParseLine), the total; the bytes are the growth of
// the hub caches (WatchDownload): huggingface_hub shows no progress
// without a terminal. A failure is ClassifyFailure of its output, as for
// "splash serve" (the launcher prints the installer's errors as they are).
// Cancelling sends SIGINT to the installer's process group (it exits 130)
// and SIGKILL after dlStopWait.
//
// A stopped install cannot be resumed within a file, only by file:
// huggingface_hub 1.28.0 writes each file to a temporary
// "<sha256>.<uuid8>.incomplete" of its own process and deletes it when it
// fails (file_download.py _download_to_tmp_and_move), so a rerun fetches
// an unfinished file from its start, while the files that were complete
// stay in the cache and are not fetched again ("Fetching 1 file(s)" of
// two). Checked live on 2026-10-28 with Splash 1.4.1 and hf_xet 1.6.0:
// after SIGINT the installer still ran 15 s later (hf_xet does not stop
// at once), so the SIGKILL left its temporary file, which the rerun did
// not take up. Download therefore removes the temporary files of its own
// run once a cancelled installer has stopped: kept, they would only fill
// the disk.
//
// While it runs it holds runtime/serve.lock shared, as the launcher does
// while it installs and serves: an upgrade of Splash waits for it
// ("Splash is being upgraded" otherwise, launcher.py serve).
func (s *Splash) Download(ctx context.Context, req engine.DownloadRequest, progress func(done, total int64)) (string, error) {
	md, err := s.ParseModel(req.Model)
	if err != nil {
		return "", engine.Failure{Kind: engine.FailModel, Line: err.Error()}
	}
	lib, err := s.libexec()
	if err != nil {
		return "", engine.Failure{Kind: engine.FailExit, Code: -1, Line: err.Error()}
	}
	unlock, err := s.shareInstall()
	if err != nil {
		return "", engine.Failure{Kind: engine.FailExit, Code: -1, Line: err.Error()}
	}
	defer unlock()

	// An installed model whose draft is gone from the hub cache gets that
	// draft at the commit it records; then the installer only verifies,
	// offline, so the model stays at its commit too (online it would
	// resolve both on main: install/upstream.py, after "Reinstalling").
	link := plainLink(s.modelsDir, md.ID)
	if req.NoVision {
		link = textOnlyLink(s.modelsDir, md.ID)
	}
	offline := false
	if d, ok := missingDraft(link); ok {
		if err := s.fetchDraft(ctx, d, progress); err != nil {
			return "", err
		}
		offline, progress = true, nil
	}

	argv := []string{filepath.Join(lib, "python", "bin", "python3"), "-u", filepath.Join(lib, "install", "models.py"),
		"--models", s.modelsDir, "--model", md.ID}
	if req.NoVision {
		argv = append(argv, "--language-only")
	}
	argv = append(argv, "prepare")

	pr, pw := io.Pipe()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = lib
	cmd.Env = append(environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1")
	if offline {
		cmd.Env = append(cmd.Env, "HF_HUB_OFFLINE=1")
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = dlStopWait
	meter := s.WatchDownload(md.ID) // its baseline: before the installer starts
	began, had := time.Now(), s.incomplete()
	if err := cmd.Start(); err != nil {
		pw.Close()
		return "", engine.Failure{Kind: engine.FailExit, Code: -1, Line: err.Error()}
	}

	var (
		mu    sync.Mutex
		total int64
		tail  []string
	)
	read := make(chan struct{})
	go func() {
		defer close(read)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			// tqdm redraws its bar with \r: the last state is the line.
			line := sc.Text()
			if i := strings.LastIndexByte(strings.TrimRight(line, "\r"), '\r'); i >= 0 {
				line = line[i+1:]
			}
			line = strings.TrimRight(line, "\r")
			if strings.TrimSpace(line) == "" {
				continue
			}
			ev, ok := s.ParseLine(line, false)
			mu.Lock()
			tail = append(tail, line)
			if len(tail) > dlTail {
				tail = slices.Delete(tail, 0, len(tail)-dlTail)
			}
			if ok && ev.Kind == engine.EventDownloading {
				total += ev.Bytes
			}
			mu.Unlock()
		}
		_, _ = io.Copy(io.Discard, pr)
	}()

	exited := make(chan int, 1)
	go func() {
		_ = cmd.Wait()
		pw.Close()
		exited <- exitCode(cmd)
	}()

	tick := time.NewTicker(dlTick)
	defer tick.Stop()
	var code int
	cancelled := false
	var killAt <-chan time.Time
loop:
	for {
		select {
		case code = <-exited:
			break loop
		case <-tick.C:
			mu.Lock()
			t := total
			mu.Unlock()
			if progress != nil {
				progress(meter(), t)
			}
		case <-ctx.Done():
			if !cancelled {
				cancelled = true
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
				killAt = time.After(dlStopWait)
			}
		case <-killAt:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			killAt = nil
		}
	}
	<-read
	if cancelled || ctx.Err() != nil {
		// Nothing resumes them; files that were there before are another
		// download's (hf download in a terminal, say).
		for p := range s.incomplete() {
			if fi, err := os.Stat(p); err == nil && !had[p] && !fi.ModTime().Before(began) {
				_ = os.Remove(p)
			}
		}
		return "", ctx.Err()
	}
	mu.Lock()
	t, lines := total, slices.Clone(tail)
	mu.Unlock()
	if code != 0 {
		f := s.ClassifyFailure(code, lines)
		if f.Kind == engine.FailNone {
			f = engine.Failure{Kind: engine.FailExit, Code: code}
		}
		return "", f
	}
	if progress != nil {
		progress(max(meter(), t), t)
	}
	if _, ok := readAssembly(link); !ok {
		return "", engine.Failure{Kind: engine.FailExit, Line: "the installer did not publish " + md.ID}
	}
	return md.ID, nil
}

// DropPartial removes the files huggingface_hub was downloading when the
// install stopped: "<sha256>.<id>.incomplete" blobs (with hf_xet,
// enginekit/testdata/hub-hfxet.txt) changed since the install began, and
// the parts of a draft fetchDraft was fetching. The
// whole cache is looked at: the draft's repository is only known to
// Splash. Files that were complete stay, and the next install reuses them.
func (s *Splash) DropPartial(model string, since time.Time) {
	for _, link := range []string{plainLink(s.modelsDir, model), textOnlyLink(s.modelsDir, model)} {
		if a, ok := readAssembly(link); ok && a.drafts != "" {
			enginekit.DropHubParts(s.hubDirs, a.drafts)
		}
	}
	for _, d := range s.hubDirs {
		repos, _ := os.ReadDir(d)
		for _, r := range repos {
			if !r.IsDir() || !strings.HasPrefix(r.Name(), "models--") {
				continue
			}
			blobs := filepath.Join(d, r.Name(), "blobs")
			_ = filepath.WalkDir(blobs, func(path string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".incomplete") {
					return nil
				}
				if fi, err := e.Info(); err == nil && !fi.ModTime().Before(since) {
					_ = os.Remove(path)
				}
				return nil
			})
		}
	}
}

// incomplete is the set of huggingface_hub's temporary files in the hub
// caches: "<sha256>.<id>.incomplete" blobs.
func (s *Splash) incomplete() map[string]bool {
	out := map[string]bool{}
	for _, d := range s.hubDirs {
		parts, _ := filepath.Glob(filepath.Join(d, "models--*", "blobs", "*.incomplete"))
		for _, p := range parts {
			out[p] = true
		}
	}
	return out
}

// libexec is the directory of Splash's program files: libexec beside the
// resolved launcher (<prefix>/Cellar/splash/<v>/bin/splash), or the one
// set for tests.
func (s *Splash) libexec() (string, error) {
	dir := s.libexecDir
	if dir == "" {
		real, err := filepath.EvalSymlinks(s.binPath())
		if err != nil {
			return "", err
		}
		dir = filepath.Join(filepath.Dir(filepath.Dir(real)), "libexec")
	}
	if _, err := os.Stat(filepath.Join(dir, "install", "models.py")); err != nil {
		return "", errors.New("this Splash has no model installer (" + filepath.Join(dir, "install", "models.py") + ")")
	}
	return dir, nil
}

// shareInstall takes runtime/serve.lock shared, without waiting, as the
// launcher does (launcher.py serve: "Servers share the installation;
// upgrades require exclusive access"); unlock releases it.
func (s *Splash) shareInstall() (unlock func(), err error) {
	if err := os.MkdirAll(s.runtimeDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.runtimeDir, "serve.lock"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("splash is being upgraded; wait for the upgrade to finish")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// exitCode is the installer's exit code, 128+signal when a signal ended it.
func exitCode(cmd *exec.Cmd) int {
	st, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	switch {
	case !ok:
		return cmd.ProcessState.ExitCode()
	case st.Signaled():
		return 128 + int(st.Signal())
	}
	return st.ExitStatus()
}
