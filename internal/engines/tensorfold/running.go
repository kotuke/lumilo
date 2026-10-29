package tensorfold

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

// The server keeps no lock or pid file, so servers are found by process,
// as for llama.cpp: "ps" lists every process with its arguments, the port
// comes from --port (default 8080, README "Serve flags"), a GET /health on
// it proves a TensorFold answers there, and "lsof" proves that the process
// found is the one that listens on that port. Nothing here signals or
// kills a process.

const psTimeout = 2 * time.Second

const defaultPort = 8080

// runPS lists all processes as "pid args...". With -ww the arguments are
// not cut.
func runPS() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "/bin/ps", "-axww", "-o", "pid=,args=").Output()
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
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(f); err == nil && n > 0 {
			pids = append(pids, n)
		}
	}
	return pids, nil
}

// proc is one "tensorfold serve" process.
type proc struct {
	pid  int
	args []string // the arguments after "serve"
}

// parsePS keeps the servers of a runPS listing: the program is tensorfold
// or tensorfold-native (the formula links both to one file; ps shows the
// name it was started by, fixture: "tensorfold serve mlx-community/…")
// and its first argument is "serve".
func parsePS(out []byte) []proc {
	var ps []proc
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[2] != "serve" {
			continue
		}
		if b := filepath.Base(f[1]); b != "tensorfold" && b != "tensorfold-native" {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid <= 0 {
			continue
		}
		ps = append(ps, proc{pid: pid, args: f[3:]})
	}
	return ps
}

// boolFlags are the serve flags that take no value (serve --help).
var boolFlags = map[string]bool{
	"-h": true, "--help": true, "--metrics-open": true, "--dashboard": true, "--prompt-cache-over-cap": true,
	"--learn": true, "--learn-conversations": true, "--recall": true, "--recall-read-only": true,
	"--prompt-seed": true, "--thinking": true, "--no-thinking": true, "--loop-guard": true,
	"--loop-guard-stop": true, "--no-drafts": true, "--slide": true, "--cortheon": true,
	"--no-update-check": true, "--vision": true, "--vision-urls": true, "--cfg-allow-request-gamma": true,
	"--cfg-force-guided": true, "--cfg-batched": true,
}

// parse reads the model (the one positional argument) and the flags of
// p's command line, as "--flag v" or "--flag=v".
func (p proc) parse() (model string, flags map[string]string) {
	flags = map[string]string{}
	for i := 0; i < len(p.args); i++ {
		a := p.args[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			if model == "" {
				model = a
			}
		case strings.Contains(a, "="):
			k, v, _ := strings.Cut(a, "=")
			flags[k] = v
		case boolFlags[a]:
			flags[a] = ""
		case i+1 < len(p.args):
			flags[a] = p.args[i+1]
			i++
		}
	}
	return model, flags
}

// listens reports whether a process on 127.0.0.1:port answers /health the
// way TensorFold does: 200, or 503 while unloaded or draining.
func (tf *TensorFold) listens(port int) bool {
	r := tf.request(context.Background(), engine.Target{Port: port}, http.MethodGet, "/health", nil, probeTimeout)
	return r.Code == http.StatusOK || r.Code == http.StatusServiceUnavailable
}

// FindRunning lists "tensorfold serve" processes that listen and answer
// on their port; a port that lsof cannot tie to the process is skipped.
// Model is the repository of the command line (Model.ID).
func (tf *TensorFold) FindRunning() ([]engine.Running, error) {
	out, err := tf.ps()
	if err != nil {
		return nil, err
	}
	var found []engine.Running
	for _, p := range parsePS(out) {
		model, flags := p.parse()
		port := defaultPort
		if v, ok := flags["--port"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > 65535 {
				continue
			}
			port = n
		}
		if !tf.listens(port) {
			continue
		}
		if pids, err := tf.listeners(port); err != nil || !slices.Contains(pids, p.pid) {
			continue // another process holds the port
		}
		var idle time.Duration
		if n, err := strconv.Atoi(flags["--idle-unload"]); err == nil && n > 0 {
			idle = time.Duration(n) * time.Second
		}
		served := flags["--name"]
		if served == "" {
			served = servedName(model)
		}
		_, vision := flags["--vision"]
		tf.remember(port, model, served, idle, vision)
		found = append(found, engine.Running{PID: p.pid, Port: port, Model: model})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Port < found[j].Port })
	return found, nil
}
