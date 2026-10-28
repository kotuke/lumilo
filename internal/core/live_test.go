//go:build live

// Live integration test of the supervisor against a real Splash server.
//
//	CGO_ENABLED=0 go test -tags live -run TestLive -v -timeout 20m ./internal/core/
//
// It needs Splash 1.4.0 and the model below installed; it runs offline, on
// ports 18100 and up, and never touches a server it did not start.
package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/splash"
)

const (
	liveModel = "orcarouter/OrcaCyber-Nano-1.0:Mix-15.7G"
	liveBogus = "orcarouter/NoSuchModel-1.0"

	loadTimeout = 120 * time.Second
	stopTimeout = 60 * time.Second

	portLifecycle = 18100
	portBusy      = 18110
	portExternal  = 18120
	portBogus     = 18130
	portClose     = 18140
	portUpdate    = 18160
	portNetwork   = 18141
)

// Helpers.

var phaseNames = map[core.Phase]string{
	core.PhaseStopped: "Stopped", core.PhaseLoading: "Loading", core.PhaseReady: "Ready",
	core.PhaseGenerating: "Generating", core.PhaseReleased: "Released", core.PhaseError: "Error",
	core.PhaseExternal: "External", core.PhaseUnresponsive: "Unresponsive", core.PhaseStopping: "Stopping",
	core.PhaseNotInstalled: "NotInstalled", core.PhaseNoInstaller: "NoBrew", core.PhaseInstalling: "Installing",
	core.PhaseInstallFailed: "InstallFailed",
}

func phaseName(p core.Phase) string {
	if n, ok := phaseNames[p]; ok {
		return n
	}
	return fmt.Sprintf("Phase(%d)", int(p))
}

// recorder keeps every distinct phase a supervisor showed, from OnChange
// and from a fast poller (OnChange coalesces, so it can skip short phases).
type recorder struct {
	mu     sync.Mutex
	phases []core.Phase
	stop   chan struct{}
	sup    *core.Supervisor
}

func (r *recorder) note(p core.Phase) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := len(r.phases); n == 0 || r.phases[n-1] != p {
		r.phases = append(r.phases, p)
	}
}

func (r *recorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.phases)
}

// since returns the phases seen after mark.
func (r *recorder) since(mark int) []core.Phase {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.Phase(nil), r.phases[mark:]...)
}

func (r *recorder) poll() {
	for {
		select {
		case <-r.stop:
			return
		case <-time.After(10 * time.Millisecond):
			r.note(r.sup.View().Phase)
		}
	}
}

func phaseList(ps []core.Phase) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = phaseName(p)
	}
	return strings.Join(names, " > ")
}

// indexOfSeq reports whether want appears in ps as a subsequence.
func hasSeq(ps []core.Phase, want ...core.Phase) bool {
	i := 0
	for _, p := range ps {
		if i < len(want) && p == want[i] {
			i++
		}
	}
	return i == len(want)
}

func settingsFor(model string, port int) core.Settings {
	return core.Settings{Config: engine.Config{
		Model:        model,
		Port:         port,
		Host:         "127.0.0.1",
		Access:       engine.AccessLocal,
		ModelUpdates: false,
	}}
}

// live is the state shared by one test run.
type live struct {
	t     *testing.T
	eng   *splash.Splash
	start time.Time
	pids  []int // process groups we started through a supervisor
	mu    sync.Mutex
}

func (l *live) logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("[%7.2fs] %s", time.Since(l.start).Seconds(), fmt.Sprintf(format, args...))
}

// newSup starts a supervisor and registers its cleanup on t.
func (l *live) newSup(t *testing.T, s core.Settings) (*core.Supervisor, *recorder) {
	t.Helper()
	rec := &recorder{stop: make(chan struct{})}
	sup := core.New(core.Options{
		Engine:   l.eng,
		Settings: s,
		LogDir:   t.TempDir(),
		OnChange: func(v core.View) { rec.note(v.Phase) },
	})
	rec.sup = sup
	go rec.poll()
	t.Cleanup(func() {
		sup.Close()
		close(rec.stop)
		l.reap(t, s.Port)
	})
	return sup, rec
}

// track remembers the process group of the server on port, so cleanup can
// kill it if the supervisor fails to.
func (l *live) track(port int) int {
	pid := l.livePID(port)
	if pid > 0 {
		l.mu.Lock()
		l.pids = append(l.pids, pid)
		l.mu.Unlock()
	}
	return pid
}

// reap SIGKILLs the groups of servers we started that are still alive. It
// is the safety net; the tests assert the normal stop separately.
func (l *live) reap(t *testing.T, _ int) {
	l.mu.Lock()
	pids := l.pids
	l.pids = nil
	l.mu.Unlock()
	for _, pid := range pids {
		if groupAlive(pid) {
			t.Logf("cleanup: group %d still alive, SIGKILL", pid)
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}
}

// livePID is the PID of the live server on port, from the lock file; 0 if none.
func (l *live) livePID(port int) int {
	rs, _ := l.eng.FindRunning()
	for _, r := range rs {
		if r.Port == port {
			return r.PID
		}
	}
	return 0
}

func groupAlive(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func lockPath(port int) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Splash", "runtime", fmt.Sprintf("serve-%d.lock", port))
}

// lockHeld reports whether a process holds the Splash lock of port.
func lockHeld(port int) bool {
	f, err := os.Open(lockPath(port))
	if err != nil {
		return false
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	return errors.Is(err, syscall.EWOULDBLOCK)
}

func serveNativeCount() int {
	out, err := exec.Command("pgrep", "-f", "serve-native").Output()
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(out)))
}

// requireFree fails when something uses port or holds its Splash lock.
func (l *live) requireFree(t *testing.T, ports ...int) {
	t.Helper()
	for _, p := range ports {
		if lockHeld(p) {
			t.Fatalf("port %d: a Splash lock is live; refusing to run", p)
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			t.Fatalf("port %d is in use: %v", p, err)
		}
		ln.Close()
	}
}

// waitView polls until pred is true.
func (l *live) waitView(t *testing.T, sup *core.Supervisor, timeout time.Duration, what string, pred func(core.View) bool) core.View {
	t.Helper()
	begin := time.Now()
	deadline := begin.Add(timeout)
	for {
		v := sup.View()
		if pred(v) {
			l.logf(t, "%s (after %.2fs, phase %s)", what, time.Since(begin).Seconds(), phaseName(v.Phase))
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout after %s waiting for %s: phase %s, failure %+v, lastLine %q",
				timeout, what, phaseName(v.Phase), v.Failure, v.LastLine)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitPhase(l *live, t *testing.T, sup *core.Supervisor, timeout time.Duration, ps ...core.Phase) core.View {
	t.Helper()
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = phaseName(p)
	}
	return l.waitView(t, sup, timeout, "phase "+strings.Join(names, "|"), func(v core.View) bool {
		for _, p := range ps {
			if v.Phase == p {
				return true
			}
		}
		return false
	})
}

// waitCond polls a plain condition.
func (l *live) waitCond(t *testing.T, timeout time.Duration, what string, f func() bool) {
	t.Helper()
	begin := time.Now()
	for !f() {
		if time.Since(begin) > timeout {
			t.Fatalf("timeout after %s waiting for %s", timeout, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
	l.logf(t, "%s (after %.2fs)", what, time.Since(begin).Seconds())
}

var chatClient = &http.Client{
	Timeout:   180 * time.Second,
	Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
}

type chatResult struct {
	status int
	tokens int
	text   string
	took   time.Duration
	err    error
}

// chat sends one non-streaming chat completion.
func chat(port int, prompt string, maxTokens int) chatResult {
	body, _ := json.Marshal(map[string]any{
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": maxTokens,
		"stream":     false,
	})
	begin := time.Now()
	resp, err := chatClient.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port), "application/json", bytes.NewReader(body))
	if err != nil {
		return chatResult{err: err, took: time.Since(begin)}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	r := chatResult{status: resp.StatusCode, took: time.Since(begin)}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &out) == nil {
		r.tokens = out.Usage.Completion
		if len(out.Choices) > 0 {
			r.text = out.Choices[0].Message.Content
		}
	}
	if resp.StatusCode != http.StatusOK {
		r.err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return r
}

// TestLive runs the scenarios in order. Scenarios 1-4 share one server.
func TestLive(t *testing.T) {
	top := t
	eng := splash.New()
	l := &live{t: t, eng: eng, start: time.Now()}

	if in := eng.Installer().Detect(context.Background()); !in.Installed {
		t.Skip("Splash is not installed")
	}
	l.requireFree(t, portLifecycle, portBusy, portBusy+1, portExternal, portBogus, portClose, portNetwork)
	if rs, _ := eng.FindRunning(); len(rs) > 0 {
		t.Logf("note: foreign servers are running and stay untouched: %+v", rs)
	}

	var (
		sup   *core.Supervisor
		rec   *recorder
		pid   int
		ready bool
	)
	baseNative := serveNativeCount()
	l.logf(t, "baseline serve-native processes: %d", baseNative)

	needReady := func(t *testing.T) {
		t.Helper()
		if !ready {
			t.Skip("scenario 1 did not reach Ready")
		}
	}

	t.Run("1_start_ready", func(t *testing.T) {
		sup, rec = l.newSup(top, settingsFor(liveModel, portLifecycle))
		begin := time.Now()
		mark := rec.mark()
		sup.Start()
		waitPhase(l, t, sup, 30*time.Second, core.PhaseLoading)
		v := waitPhase(l, t, sup, loadTimeout, core.PhaseReady)
		l.logf(t, "Loading -> Ready in %.1fs", time.Since(begin).Seconds())
		if seq := rec.since(mark); !hasSeq(seq, core.PhaseLoading, core.PhaseReady) {
			t.Errorf("phases %s: want Loading > Ready", phaseList(seq))
		}
		pid = l.track(portLifecycle)
		if pid <= 0 {
			t.Errorf("no lock record for port %d", portLifecycle)
		}
		ready = true

		// The first probe after Ready fills the snapshot.
		snapBegin := time.Now()
		v = l.waitView(t, sup, 10*time.Second, "snapshot filled", func(v core.View) bool {
			return v.Snapshot.MemoryUsed > 0 && v.Snapshot.MemoryLimit > 0
		})
		l.logf(t, "snapshot filled %.2fs after Ready", time.Since(snapBegin).Seconds())

		if v.Model.ID != liveModel {
			t.Errorf("Model.ID = %q, want %q", v.Model.ID, liveModel)
		}
		if v.Port != portLifecycle {
			t.Errorf("Port = %d", v.Port)
		}
		if want := fmt.Sprintf("127.0.0.1:%d", portLifecycle); v.Address != want {
			t.Errorf("Address = %q, want %q", v.Address, want)
		}
		if v.ChatURL == "" {
			t.Error("ChatURL is empty")
		}
		l.logf(t, "memory %.1f / %.1f GB, ChatURL %s, version %s, pid %d",
			float64(v.Snapshot.MemoryUsed)/1e9, float64(v.Snapshot.MemoryLimit)/1e9, v.ChatURL, v.Version, pid)
		if v.LogPath == "" {
			t.Fatal("LogPath is empty")
		}
		data, err := os.ReadFile(v.LogPath)
		if err != nil {
			t.Fatalf("log: %v", err)
		}
		if !strings.Contains(string(data), "Ready ·") {
			t.Errorf("log lacks %q:\n%s", "Ready ·", data)
		}
	})

	t.Run("2_generating_speed", func(t *testing.T) {
		needReady(t)
		before := sup.View().Speed
		l.logf(t, "history before: %d", len(before.History))

		done := make(chan chatResult, 1)
		go func() {
			done <- chat(portLifecycle, "Write a detailed, long story about a lighthouse keeper who finds a message in a bottle.", 300)
		}()
		var sawGen bool
		var maxCur float64
		var res chatResult
		func() {
			for {
				select {
				case res = <-done:
					return
				case <-time.After(20 * time.Millisecond):
					v := sup.View()
					if v.Phase == core.PhaseGenerating && !sawGen {
						sawGen = true
						l.logf(t, "saw Generating")
					}
					if v.Speed.Current > maxCur {
						if maxCur == 0 {
							l.logf(t, "Speed.Current %.1f tok/s", v.Speed.Current)
						}
						maxCur = v.Speed.Current
					}
				}
			}
		}()
		if res.err != nil {
			t.Fatalf("chat: %v", res.err)
		}
		l.logf(t, "chat done: %d tokens in %.1fs (%.1f tok/s), %d chars", res.tokens, res.took.Seconds(),
			float64(res.tokens)/res.took.Seconds(), len(res.text))
		if !sawGen {
			t.Error("never saw PhaseGenerating")
		}
		if maxCur <= 0 {
			t.Error("Speed.Current stayed 0 during the request")
		}

		waitPhase(l, t, sup, 10*time.Second, core.PhaseReady)
		v := l.waitView(t, sup, 10*time.Second, "History grew and Average>0", func(v core.View) bool {
			return len(v.Speed.History) > len(before.History) && v.Speed.Average > 0
		})
		l.logf(t, "history %v average %.1f current %.1f ttft %s read %.0f tok/s", v.Speed.History, v.Speed.Average,
			v.Speed.Current, v.Speed.FirstToken, v.Speed.Read)
		n := len(v.Speed.History)

		for i := 0; i < 2; i++ {
			r := chat(portLifecycle, fmt.Sprintf("Name %d colours.", i+3), 40)
			if r.err != nil {
				t.Fatalf("short chat %d: %v", i, r.err)
			}
			l.logf(t, "short chat %d: %d tokens in %.1fs", i, r.tokens, r.took.Seconds())
		}
		v = l.waitView(t, sup, 15*time.Second, "History grew by 2", func(v core.View) bool {
			return len(v.Speed.History) >= n+2
		})
		l.logf(t, "history %v average %.1f", v.Speed.History, v.Speed.Average)
		if v.Phase != core.PhaseReady {
			t.Errorf("phase %s, want Ready", phaseName(v.Phase))
		}
	})

	t.Run("3_release_restore", func(t *testing.T) {
		needReady(t)
		memBefore := sup.View().Snapshot.MemoryUsed
		histBefore := len(sup.View().Speed.History)

		sup.Release()
		v := waitPhase(l, t, sup, 30*time.Second, core.PhaseReleased)
		l.logf(t, "released: memory %.2f GB (was %.2f)", float64(v.Snapshot.MemoryUsed)/1e9, float64(memBefore)/1e9)
		if !v.Snapshot.Released {
			t.Error("Snapshot.Released is false in PhaseReleased")
		}

		completed := v.Snapshot.Completed
		begin := time.Now()
		sup.Restore()
		v = waitPhase(l, t, sup, 90*time.Second, core.PhaseReady)
		l.logf(t, "restored in %.1fs", time.Since(begin).Seconds())

		// The ping must be counted by the server and ignored by the meter.
		v = l.waitView(t, sup, 10*time.Second, "ping counted by server", func(v core.View) bool {
			return v.Snapshot.Completed > completed
		})
		time.Sleep(3 * time.Second) // a few more probes
		v = sup.View()
		if got := len(v.Speed.History); got != histBefore {
			t.Errorf("History length %d after restore, want %d (ping must not count)", got, histBefore)
		}
		if v.Phase != core.PhaseReady {
			t.Errorf("phase %s after restore, want Ready", phaseName(v.Phase))
		}
		l.logf(t, "completed %d -> %d, history %d", completed, v.Snapshot.Completed, len(v.Speed.History))
	})

	t.Run("4_stop", func(t *testing.T) {
		needReady(t)
		if pid <= 0 {
			t.Skip("no pid")
		}
		withServer := serveNativeCount()
		mark := rec.mark()
		begin := time.Now()
		sup.Stop()
		waitPhase(l, t, sup, 10*time.Second, core.PhaseStopping, core.PhaseStopped)
		waitPhase(l, t, sup, stopTimeout, core.PhaseStopped)
		l.logf(t, "Stopped in %.2fs", time.Since(begin).Seconds())
		seq := rec.since(mark)
		l.logf(t, "phases: %s", phaseList(seq))
		if !hasSeq(seq, core.PhaseStopping, core.PhaseStopped) {
			t.Errorf("phases %s: want Stopping > Stopped", phaseList(seq))
		}
		l.waitCond(t, 10*time.Second, "process group gone", func() bool { return !groupAlive(pid) })
		if lockHeld(portLifecycle) {
			t.Error("lock file is still locked")
		}
		after := serveNativeCount()
		l.logf(t, "serve-native: baseline %d, running %d, after stop %d", baseNative, withServer, after)
		if after > baseNative {
			t.Errorf("serve-native count %d grew over baseline %d", after, baseNative)
		}
		if v := sup.View(); v.Failure.Kind != engine.FailNone {
			t.Errorf("stop left a failure: %+v", v.Failure)
		}
	})

	t.Run("5_port_busy", func(t *testing.T) {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", portBusy))
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() { // accept and drop, like a plain server would
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		sup, rec := l.newSup(t, settingsFor(liveModel, portBusy))
		begin := time.Now()
		sup.Start()
		v := waitPhase(l, t, sup, 30*time.Second, core.PhaseError)
		l.logf(t, "Error in %.2fs: %+v free port %d", time.Since(begin).Seconds(), v.Failure, v.FreePort)
		if v.Failure.Kind != engine.FailPortBusy {
			t.Errorf("Failure.Kind = %v, want FailPortBusy", v.Failure.Kind)
		}
		if v.FreePort <= portBusy {
			t.Fatalf("FreePort = %d, want > %d", v.FreePort, portBusy)
		}
		l.requireFree(t, v.FreePort)

		start := time.Now()
		sup.StartOn(v.FreePort)
		waitPhase(l, t, sup, loadTimeout, core.PhaseReady)
		l.logf(t, "Ready on %d in %.1fs", v.FreePort, time.Since(start).Seconds())
		if got := sup.View(); got.Port != v.FreePort || !strings.HasSuffix(got.Address, fmt.Sprint(":", v.FreePort)) {
			t.Errorf("View port/address %d %q, want port %d", got.Port, got.Address, v.FreePort)
		}
		fp := l.track(v.FreePort)
		sup.Stop()
		waitPhase(l, t, sup, stopTimeout, core.PhaseStopped)
		if fp > 0 {
			l.waitCond(t, 10*time.Second, "group gone", func() bool { return !groupAlive(fp) })
		}
		if lockHeld(v.FreePort) {
			t.Error("lock still held")
		}
		l.logf(t, "phases: %s", phaseList(rec.since(0)))
	})

	t.Run("6_external", func(t *testing.T) {
		logFile, err := os.Create(filepath.Join(t.TempDir(), "external.log"))
		if err != nil {
			t.Fatal(err)
		}
		defer logFile.Close()
		cmd := exec.Command("/opt/homebrew/bin/splash", "serve", "--model", liveModel, "--offline",
			"--port", fmt.Sprint(portExternal), "--host", "127.0.0.1", "--idle-release", "off")
		cmd.Stdout, cmd.Stderr = logFile, logFile
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		epid := cmd.Process.Pid
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		t.Cleanup(func() { // only our own group
			select {
			case <-exited:
			default:
				_ = syscall.Kill(-epid, syscall.SIGKILL)
				<-exited
			}
		})
		begin := time.Now()
		l.waitCond(t, loadTimeout, "external /ready 200", func() bool {
			select {
			case <-exited:
				data, _ := os.ReadFile(logFile.Name())
				t.Fatalf("external server exited early:\n%s", data)
			default:
			}
			resp, err := chatClient.Get(fmt.Sprintf("http://127.0.0.1:%d/ready", portExternal))
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
		l.logf(t, "external server ready in %.1fs, pid %d", time.Since(begin).Seconds(), epid)

		sup, _ := l.newSup(t, settingsFor(liveModel, portExternal))
		v := waitPhase(l, t, sup, 30*time.Second, core.PhaseExternal)
		if v.External == nil || v.External.PID != epid {
			t.Fatalf("External = %+v, want PID %d", v.External, epid)
		}
		if v.External.Port != portExternal {
			t.Errorf("External.Port = %d", v.External.Port)
		}
		v = l.waitView(t, sup, 10*time.Second, "external snapshot reachable", func(v core.View) bool {
			return v.Snapshot.Reachable && v.Snapshot.MemoryUsed > 0
		})
		l.logf(t, "external: model %q address %s chat %s", v.Model.ID, v.Address, v.ChatURL)
		if v.Model.ID != liveModel {
			t.Errorf("external model %q, want %q", v.Model.ID, liveModel)
		}

		killBegin := time.Now()
		sup.KillExternal()
		waitPhase(l, t, sup, 10*time.Second, core.PhaseStopped)
		select {
		case <-exited:
			l.logf(t, "external process exited %.2fs after KillExternal", time.Since(killBegin).Seconds())
		case <-time.After(stopTimeout):
			t.Fatal("external process did not exit")
		}
		l.waitCond(t, 10*time.Second, "external group gone", func() bool { return !groupAlive(epid) })
		if lockHeld(portExternal) {
			t.Error("external lock still held")
		}
		// The supervisor must stay stopped and not re-adopt anything.
		time.Sleep(3 * time.Second)
		if p := sup.View().Phase; p != core.PhaseStopped {
			t.Errorf("phase %s after KillExternal settled, want Stopped", phaseName(p))
		}
	})

	t.Run("7_bad_model", func(t *testing.T) {
		sup, _ := l.newSup(t, settingsFor(liveModel, portBogus))
		// Detection replaces an uninstalled model with the first installed
		// one, so apply the bogus id after it (commands run in order).
		sup.Apply(settingsFor(liveBogus, portBogus))
		begin := time.Now()
		sup.Start()
		v := waitPhase(l, t, sup, 60*time.Second, core.PhaseError)
		l.logf(t, "Error in %.2fs: %+v last %q", time.Since(begin).Seconds(), v.Failure, v.LastLine)
		if v.Failure.Kind != engine.FailModel {
			t.Errorf("Failure.Kind = %v, want FailModel", v.Failure.Kind)
		}
		if !strings.Contains(v.Failure.Line, "HF_HUB_OFFLINE") {
			t.Errorf("Failure.Line = %q, want it to contain HF_HUB_OFFLINE", v.Failure.Line)
		}
		if lockHeld(portBogus) {
			t.Error("lock still held after failure")
		}
	})

	t.Run("8_close_stops_child", func(t *testing.T) {
		// Not l.newSup: Close is the thing under test and runs inline.
		rec := &recorder{stop: make(chan struct{})}
		sup := core.New(core.Options{Engine: eng, Settings: settingsFor(liveModel, portClose), LogDir: t.TempDir(),
			OnChange: func(v core.View) { rec.note(v.Phase) }})
		rec.sup = sup
		go rec.poll()
		defer close(rec.stop)
		closed := false
		t.Cleanup(func() {
			if !closed {
				sup.Close()
			}
			l.reap(t, 0)
		})
		sup.Start()
		waitPhase(l, t, sup, loadTimeout, core.PhaseReady)
		cpid := l.track(portClose)
		if cpid <= 0 {
			t.Fatal("no lock record")
		}
		before := serveNativeCount()
		begin := time.Now()
		sup.Close()
		closed = true
		l.logf(t, "Close returned after %.2fs", time.Since(begin).Seconds())
		l.waitCond(t, 10*time.Second, "group gone after Close", func() bool { return !groupAlive(cpid) })
		if lockHeld(portClose) {
			t.Error("lock still held after Close")
		}
		if after := serveNativeCount(); after > baseNative {
			t.Errorf("serve-native count %d after Close (running %d, baseline %d)", after, before, baseNative)
		}
	})

	t.Run("9_network_with_key", func(t *testing.T) {
		ip := lanIPv4()
		if ip == "" {
			t.Skip("no LAN address")
		}
		key := "sk-" + strings.Repeat("0123456789abcdef", 2)
		st := settingsFor(liveModel, portNetwork)
		st.Host, st.Access, st.APIKey, st.LANAddress = "0.0.0.0", engine.AccessNetwork, key, ip
		sup, _ := l.newSup(t, st)
		sup.Start()
		waitPhase(l, t, sup, loadTimeout, core.PhaseReady)
		l.track(portNetwork)
		v := l.waitView(t, sup, 15*time.Second, "authorized snapshot over the loopback address", func(v core.View) bool {
			return v.Snapshot.Authorized && v.Snapshot.MemoryUsed > 0
		})
		// The chip shows the address other devices use; we dial the loopback.
		if v.Address != fmt.Sprintf("%s:%d", ip, portNetwork) || !strings.Contains(v.ChatURL, "127.0.0.1") {
			t.Errorf("address %q chat %q", v.Address, v.ChatURL)
		}
		time.Sleep(14 * time.Second)
		if p := sup.View().Phase; p != core.PhaseReady {
			t.Errorf("phase %s after 14 s, want ready", phaseName(p))
		}
		// Both the LAN address and the loopback address answer, with the key required.
		for _, host := range []string{ip, "127.0.0.1"} {
			resp, err := http.Get(fmt.Sprintf("http://%s:%d/status", host, portNetwork))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s: status without key: %d, want 401", host, resp.StatusCode)
			}
		}
		sup.Stop()
		waitPhase(l, t, sup, 30*time.Second, core.PhaseStopped)
	})
}

// TestLiveUpdate upgrades the installed engine through Homebrew while our
// server runs, as the panel's Update button does. It changes the system,
// so it runs only with LUMILO_LIVE_UPDATE=1 and only when an update exists.
func TestLiveUpdate(t *testing.T) {
	if os.Getenv("LUMILO_LIVE_UPDATE") != "1" {
		t.Skip("set LUMILO_LIVE_UPDATE=1 to upgrade the engine for real")
	}
	eng := splash.New()
	l := &live{t: t, eng: eng, start: time.Now()}
	in := eng.Installer().Detect(context.Background())
	latest, err := eng.Installer().Latest(context.Background())
	if !in.Installed || err != nil || latest == "" || latest == in.Version {
		t.Skipf("nothing to update: installed %q, latest %q, err %v", in.Version, latest, err)
	}
	l.requireFree(t, portUpdate)
	sup, _ := l.newSup(t, settingsFor(liveModel, portUpdate))
	sup.Start()
	waitPhase(l, t, sup, loadTimeout, core.PhaseReady)
	before := l.track(portUpdate)
	l.logf(t, "ready on %s, pid %d", in.Version, before)

	sup.Update()
	l.waitView(t, sup, 30*time.Second, "updating", func(v core.View) bool { return v.Update == core.UpdateUpdating })
	v := l.waitView(t, sup, 15*time.Minute, "update finished", func(v core.View) bool {
		return v.Update == core.UpdateNone || v.Update == core.UpdateFailed
	})
	if v.Update == core.UpdateFailed {
		t.Fatalf("update failed: %s", v.UpdateError)
	}
	if v.Version != latest {
		t.Errorf("version %q after update, want %q", v.Version, latest)
	}
	v = waitPhase(l, t, sup, loadTimeout, core.PhaseReady)
	l.waitCond(t, 15*time.Second, "new server pid in the lock", func() bool {
		p := l.livePID(portUpdate)
		return p > 0 && p != before
	})
	after := l.track(portUpdate)
	if after == before || after <= 0 {
		t.Errorf("server pid %d after update, before %d: not restarted", after, before)
	}
	if v.Model.ID != liveModel || v.Port != portUpdate {
		t.Errorf("restarted with model %q port %d", v.Model.ID, v.Port)
	}
	l.logf(t, "restarted on %s, pid %d", v.Version, after)
	sup.Stop()
	waitPhase(l, t, sup, 30*time.Second, core.PhaseStopped)
}

// lanIPv4 is the first private IPv4 address of an up, non-loopback interface.
func lanIPv4() string {
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				return n.IP.String()
			}
		}
	}
	return ""
}
