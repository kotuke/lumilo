package llamacpp

import (
	"bytes"
	"encoding/binary"
	"fmt"
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

// fixtureModel is the model of the recorded servers, scrubbed to /Users/user.
const fixtureModel = "/Users/user/.cache/huggingface/hub/models--ggml-org--Qwen3-0.6B-GGUF/snapshots/13ac06624948e4d8589cb4b6f90100837342fcb8/Qwen3-0.6B-Q8_0.gguf"

// fixture is a recorded HTTP reply. code 0 means no reply (connection refused).
type fixture struct {
	code  int
	ctype string
	body  string
}

var hashHeaderRe = regexp.MustCompile(`^# HTTP (\d+|None) ?(.*?) t=[\d.]+$`)

// readFixture reads a testdata endpoint file: "# HTTP <code> <ctype>
// t=<sec>", then the body.
func readFixture(t *testing.T, path string) fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first, rest, _ := strings.Cut(string(data), "\n")
	m := hashHeaderRe.FindStringSubmatch(first)
	if m == nil {
		t.Fatalf("%s: unknown fixture format: %q", path, first)
	}
	code, _ := strconv.Atoi(m[1]) // "None" -> 0
	return fixture{code, m[2], rest}
}

// serve starts a server that answers the given paths; others get 404.
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
		w.Header().Set("Content-Type", f.ctype)
		w.WriteHeader(f.code)
		_, _ = w.Write([]byte(f.body))
	}
}

// noSlots fails the test if /slots is asked: it wakes a sleeping server.
func noSlots(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Probe asked %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// stateServer replays testdata/lifecycle/<state>.{health,props,metrics}.
// The state can be switched while it runs, so one port sees a sequence.
type stateServer struct {
	t     *testing.T
	dir   string
	state string
	port  int
}

func newStateServer(t *testing.T, dir, state string) *stateServer {
	t.Helper()
	s := &stateServer{t: t, dir: dir, state: state}
	routes := map[string]http.HandlerFunc{"/slots": noSlots(t)}
	for _, ep := range []string{"health", "props", "metrics"} {
		routes["/"+ep] = func(w http.ResponseWriter, r *http.Request) {
			replay(readFixture(t, filepath.Join("testdata", s.dir, s.state+"."+ep+".txt")))(w, r)
		}
	}
	s.port = serve(t, routes)
	return s
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

var exitRe = regexp.MustCompile(`^exit (-?\d+)$`)

// readTail reads a recorded stderr file. A trailing "exit N" line gives the
// exit code.
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
		if l != "" {
			lines = append(lines, l)
		}
	}
	return code, lines
}

func write(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func envValue(env []string, name string) (string, bool) {
	val, found := "", false
	for _, kv := range env { // the last duplicate wins, as in exec
		if k, v, _ := strings.Cut(kv, "="); k == name {
			val, found = v, true
		}
	}
	return val, found
}

// kv is one GGUF metadata entry for ggufFile. Values: string, uint32,
// []string (an array of strings) or []uint32.
// GGUF value types the builder writes.
const (
	ggufString = 8
	ggufArray  = 9
)

type kv struct {
	key string
	val any
}

// ggufFile builds a GGUF v3 header with the given metadata and no tensors,
// followed by pad bytes.
func ggufFile(kvs []kv, pad int) []byte {
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { le(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	le(uint32(3))
	le(uint64(0))
	le(uint64(len(kvs)))
	for _, e := range kvs {
		str(e.key)
		switch v := e.val.(type) {
		case string:
			le(uint32(ggufString))
			str(v)
		case uint32:
			le(uint32(4))
			le(v)
		case []string:
			le(uint32(ggufArray))
			le(uint32(ggufString))
			le(uint64(len(v)))
			for _, s := range v {
				str(s)
			}
		case []uint32:
			le(uint32(ggufArray))
			le(uint32(4))
			le(uint64(len(v)))
			for _, x := range v {
				le(x)
			}
		default:
			panic(fmt.Sprintf("kv %s: %T", e.key, v))
		}
	}
	b.Write(make([]byte, pad))
	return b.Bytes()
}

// textModel is the header of a llama-architecture text model.
func textModel(arch string) []byte {
	return ggufFile([]kv{
		{"general.architecture", arch},
		{"general.type", "model"},
		{"general.name", "Tiny"},
		{"general.tags", []string{"text-generation", "tiny"}},
		{arch + ".context_length", uint32(4096)},
		{"tokenizer.ggml.tokens", []string{"a", "b"}},
	}, 1000)
}
