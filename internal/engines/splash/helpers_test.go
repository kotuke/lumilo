package splash

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fixture is a recorded HTTP reply. code 0 means no reply (connection refused).
type fixture struct {
	code int
	body string
}

var (
	hashHeaderRe = regexp.MustCompile(`^# HTTP (\d+|None)\b`)
	rawStatusRe  = regexp.MustCompile(`^HTTP/\d\.\d (\d+) `)
)

// readFixture reads a testdata endpoint file in either recorded format:
// "# HTTP <code> <ctype> t=<sec>" then the body (lifecycle/), or a raw
// "HTTP/1.1 <code> ..." response with headers (failures/apikey_*).
func readFixture(t *testing.T, path string) fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	first, rest, _ := strings.Cut(text, "\n")
	if m := hashHeaderRe.FindStringSubmatch(first); m != nil {
		code, _ := strconv.Atoi(m[1]) // "None" -> 0
		return fixture{code, rest}
	}
	if m := rawStatusRe.FindStringSubmatch(first); m != nil {
		code, _ := strconv.Atoi(m[1])
		rest = strings.ReplaceAll(rest, "\r\n", "\n")
		_, body, _ := strings.Cut(rest, "\n\n")
		return fixture{code, body}
	}
	t.Fatalf("%s: unknown fixture format", path)
	return fixture{}
}

// serve starts a server that answers the given paths; others get 404. The
// returned port is on 127.0.0.1 as Probe expects.
func serve(t *testing.T, routes map[string]http.HandlerFunc) int {
	t.Helper()
	mux := http.NewServeMux()
	for p, h := range routes {
		mux.HandleFunc(p, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func replay(f fixture) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if f.code == 0 {
			if hj, ok := w.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				c.Close()
			}
			return
		}
		w.WriteHeader(f.code)
		_, _ = w.Write([]byte(f.body))
	}
}

// lifecycleServer replays one state of testdata/lifecycle, e.g. "ready".
func lifecycleServer(t *testing.T, state string) int {
	t.Helper()
	routes := map[string]http.HandlerFunc{}
	for _, ep := range []string{"health", "ready", "status", "metrics"} {
		f := readFixture(t, filepath.Join("testdata", "lifecycle", state+"."+ep+".txt"))
		routes["/"+ep] = replay(f)
	}
	return serve(t, routes)
}

// closedPort returns a port with nothing listening.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

var tailNoiseRe = regexp.MustCompile(`^(real|user|sys)\s`)
var exitRe = regexp.MustCompile(`^exit (\d+)$`)

// readTail reads a recorded stderr file. A trailing "exit N" line gives the
// exit code; "real/user/sys" timing lines are dropped.
func readTail(t *testing.T, path string) (code int, lines []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(data), "\n") {
		if m := exitRe.FindStringSubmatch(l); m != nil {
			code, _ = strconv.Atoi(m[1])
			continue
		}
		if l == "" || tailNoiseRe.MatchString(l) {
			continue
		}
		lines = append(lines, l)
	}
	return code, lines
}
