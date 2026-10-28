package llamacpp

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// llama-server keeps no lock or pid file, so servers are found by process:
// "ps" lists every process with its arguments, the port comes from --port
// (or the defaults), a GET /health on it proves a llama-server answers
// there, and "lsof" proves that the process found is the one that listens
// on that port: another llama-server, or another program, may hold it. The
// supervisor adopts the server, probes it with the user's key and may stop
// it by PID, so a PID tied to the wrong port must not count. Nothing here
// signals or kills a process.

const psTimeout = 2 * time.Second

// Without --port the server listens on 9931 (help of 0.7.0); builds before
// it used 8080. LLAMA_ARG_PORT in its environment is not visible to ps, so
// both are tried and /health decides.
var defaultPorts = []int{9931, 8080}

// runPS lists all processes as "pid ppid rss-KiB args...". With -ww the
// arguments are not cut. ps joins argv with spaces, so an argument that
// itself contains a space is split (see modelArg).
func runPS() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "/bin/ps", "-axww", "-o", "pid=,ppid=,rss=,args=").Output()
}

// runPSPID is runPS for one process.
func runPSPID(pid int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "/bin/ps", "-ww", "-o", "pid=,ppid=,rss=,args=", "-p", strconv.Itoa(pid)).Output()
}

// lsofTool lists the processes that listen on a port.
const lsofTool = "/usr/sbin/lsof"

// runLsof lists the PIDs that listen on TCP port, on any address. lsof
// exits with 1 and prints nothing when none does; that is no error.
func runLsof(port int) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, lsofTool, "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parsePIDs(out), nil
}

// parsePIDs reads lsof -t output: one PID per line.
func parsePIDs(out []byte) []int {
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(f); err == nil && n > 0 {
			pids = append(pids, n)
		}
	}
	return pids
}

// proc is one llama-server process.
type proc struct {
	pid, ppid int
	rssKiB    int64
	args      []string // argv split on spaces; args[0] is the program
}

// parsePS keeps the llama-server processes of a runPS listing.
func parsePS(out []byte) []proc {
	var ps []proc
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || filepath.Base(f[3]) != "llama-server" {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || pid <= 0 {
			continue
		}
		ps = append(ps, proc{pid: pid, ppid: ppid, rssKiB: rss, args: f[3:]})
	}
	return ps
}

// flag returns the value of the first of names, as "--name v" or
// "--name=v", and whether it was there.
func flag(args []string, names ...string) (string, bool) {
	for i := 1; i < len(args); i++ {
		for _, n := range names {
			if args[i] == n && i+1 < len(args) {
				return args[i+1], true
			}
			if v, ok := strings.CutPrefix(args[i], n+"="); ok {
				return v, true
			}
		}
	}
	return "", false
}

// ports is where p listens: its --port, or the defaults to try. nil when
// it listens on a Unix socket only.
func (p proc) ports() []int {
	if h, ok := flag(p.args, "--host"); ok && strings.HasSuffix(h, ".sock") {
		return nil
	}
	if v, ok := flag(p.args, "--port"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			return []int{n}
		}
		return nil
	}
	return defaultPorts
}

// modelArg is the model as given on the command line: the -m path (rejoined
// across spaces up to the next "-" argument, since ps splits paths like
// ".../Application Support/..."), else the -hf repository; "" in router
// mode.
func (p proc) modelArg() string {
	for i := 1; i < len(p.args); i++ {
		switch a := p.args[i]; {
		case a == "-m" || a == "--model":
			j := i + 1
			for j < len(p.args) && (j == i+1 || !strings.HasPrefix(p.args[j], "-")) {
				j++
			}
			return strings.Join(p.args[i+1:j], " ")
		case strings.HasPrefix(a, "--model="):
			return strings.TrimPrefix(a, "--model=")
		}
	}
	if v, ok := flag(p.args, "-hf", "-hfr", "--hf-repo"); ok {
		return v
	}
	return ""
}

func (p proc) idle() time.Duration {
	v, ok := flag(p.args, "--sleep-idle-seconds")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// listens reports whether a process on 127.0.0.1:port answers /health the
// way llama-server does: 200, 503 while loading, or 401 (never in 0.7.0,
// where /health is public, but harmless).
func (l *LlamaCpp) listens(port int) bool {
	r := l.request(context.Background(), engine.Target{Port: port}, http.MethodGet, "/health", nil, probeTimeout)
	return r.Code == http.StatusOK || r.Code == http.StatusServiceUnavailable || r.Code == http.StatusUnauthorized
}

// FindRunning lists llama-server processes that listen and answer on their
// port; a port that lsof cannot tie to the process is skipped. The
// children of a router (llama-server without -m spawns one llama-server
// per model, README "Using multiple models") are left out: the router is
// the server to talk to. Model is the -m path (Model.ID for servers that
// Lumilo started) or the -hf repository.
func (l *LlamaCpp) FindRunning() ([]engine.Running, error) {
	out, err := l.ps()
	if err != nil {
		return nil, err
	}
	all := parsePS(out)
	isServer := map[int]bool{}
	for _, p := range all {
		isServer[p.pid] = true
	}
	var found []engine.Running
	for _, p := range all {
		if isServer[p.ppid] {
			continue
		}
		for _, port := range p.ports() {
			if !l.listens(port) {
				continue
			}
			if pids, err := l.listeners(port); err != nil || !slices.Contains(pids, p.pid) {
				continue // another process holds the port
			}
			l.mu.Lock()
			m := l.memo(port)
			m.pid, m.idle = p.pid, p.idle()
			l.mu.Unlock()
			found = append(found, engine.Running{PID: p.pid, Port: port, Model: p.modelArg()})
			break
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Port < found[j].Port })
	return found, nil
}

// residentBytes is the resident size of the llama-server on port, or 0.
// It includes the memory-mapped weights that are paged in, the KV cache and
// Metal buffers in unified memory. The pid found by port is remembered:
// while it lives, a probe costs one "ps -p" (about 2 ms); a full listing
// (about 30 ms with 800 processes) runs only to find it.
func (l *LlamaCpp) residentBytes(port int) int64 {
	l.mu.Lock()
	pid := l.memo(port).pid
	l.mu.Unlock()
	if pid > 0 {
		if out, err := l.psPID(pid); err == nil {
			for _, p := range parsePS(out) {
				if p.pid == pid && hasPort(p, port) {
					return p.rssKiB * 1024
				}
			}
		}
	}
	out, err := l.ps()
	if err != nil {
		return 0
	}
	for _, p := range parsePS(out) {
		if hasPort(p, port) {
			l.mu.Lock()
			l.memo(port).pid = p.pid
			l.mu.Unlock()
			return p.rssKiB * 1024
		}
	}
	return 0
}

func hasPort(p proc, port int) bool {
	for _, pp := range p.ports() {
		if pp == port {
			return true
		}
	}
	return false
}
