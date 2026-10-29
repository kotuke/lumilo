package tensorfold

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// fixtureRepo is the model of the recorded servers.
const fixtureRepo = "mlx-community/Qwen3.5-2B-MLX-4bit"

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	return strings.Split(strings.TrimRight(readFile(t, path), "\n"), "\n")
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

// reply answers code with the body of a fixture file.
func reply(t *testing.T, code int, path string) http.HandlerFunc {
	body := readFile(t, path)
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

// keyed answers 401 unless the request carries "Bearer key".
func keyed(key string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": {"message": "invalid API key", "type": "authentication_error"}}`))
			return
		}
		h(w, r)
	}
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

// newTest is the engine with hub caches of its own and no processes.
func newTest(t *testing.T, hubDirs ...string) *TensorFold {
	t.Helper()
	tf := New()
	if len(hubDirs) == 0 {
		hubDirs = []string{t.TempDir()}
	}
	tf.hubDirs, tf.dlDir = hubDirs, hubDirs[0]
	// No executable: families falls back to families.go, whatever is
	// installed here.
	tf.inst.Bins = []string{filepath.Join(t.TempDir(), "tensorfold")}
	tf.ps = func() ([]byte, error) { return nil, nil }
	enginekit.AgentDir = func() string { return filepath.Join(t.TempDir(), "agents") }
	tf.listeners = func(int) ([]int, error) { return nil, nil }
	return tf
}

// hubFile is a file of a fake checkpoint.
type hubFile struct{ path, body string }

// putRepo lays out a snapshot of repo in the hub cache dir as
// huggingface_hub does: blobs, links in snapshots/<commit>, refs/main.
func putRepo(t *testing.T, dir, repo, commit string, files ...hubFile) string {
	t.Helper()
	root := filepath.Join(dir, enginekit.RepoFolder(repo))
	snap := filepath.Join(root, "snapshots", commit)
	for i, f := range files {
		blob := filepath.Join(root, "blobs", strings.Repeat("0", 63)+string(rune('a'+i)))
		if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blob, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(snap, f.path)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(filepath.Dir(link), blob)
		if err := os.Symlink(rel, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "refs", "main"), []byte(commit), 0o644); err != nil {
		t.Fatal(err)
	}
	return snap
}

// config is a fixture config.json from testdata/configs.
func config(t *testing.T, name string) string {
	return readFile(t, filepath.Join("testdata", "configs", name+".json"))
}
