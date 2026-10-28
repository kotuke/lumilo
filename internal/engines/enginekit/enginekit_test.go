package enginekit

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func TestFirstExecutable(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "plain"), "x", 0o644)
	write(t, filepath.Join(root, "real"), "#!/bin/sh\n", 0o755)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	p := func(n string) string { return filepath.Join(root, n) }
	if got := FirstExecutable([]string{p("missing"), p("plain"), p("dir"), p("link"), p("real")}); got != p("link") {
		t.Errorf("got %q, want the symlink to an executable", got)
	}
	if got := FirstExecutable([]string{p("missing"), p("plain")}); got != "" {
		t.Errorf("no executable: %q", got)
	}
	if got := StdBins("splash"); !slices.Equal(got, []string{"/opt/homebrew/bin/splash", "/usr/local/bin/splash"}) {
		t.Errorf("StdBins: %v", got)
	}
}

func TestUniq(t *testing.T) {
	if got := Uniq([]string{"a", "", "b", "a", "c", "b"}); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("Uniq: %v", got)
	}
}

func TestEnviron(t *testing.T) {
	t.Setenv("PATH", "/custom/bin:/usr/bin")
	t.Setenv("ENGINE_API_KEY", "stray")
	t.Setenv("KEEP_ME", "1")
	env := Environ(func(n string) bool { return n == "ENGINE_API_KEY" }, "/extra")
	var path string
	for _, kv := range env {
		if strings.HasPrefix(kv, "ENGINE_API_KEY=") {
			t.Error("dropped variable inherited")
		}
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			if path != "" {
				t.Error("two PATHs")
			}
			path = v
		}
	}
	if path != SystemPath+":/extra:/custom/bin" {
		t.Errorf("PATH %q", path)
	}
	if !slices.Contains(env, "KEEP_ME=1") {
		t.Error("other variables dropped")
	}
	if !slices.Contains(Environ(nil), "ENGINE_API_KEY=stray") {
		t.Error("nil drop dropped a variable")
	}
}

func TestMustFile(t *testing.T) {
	fsys := fstest.MapFS{"logos/a.svg": {Data: []byte("<svg/>")}}
	if got := string(MustFile(fsys, "logos/a.svg")); got != "<svg/>" {
		t.Errorf("MustFile: %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("a missing file did not panic")
		}
	}()
	MustFile(fsys, "logos/missing.svg")
}

func TestChatURL(t *testing.T) {
	if got := ChatURL(8123); got != "http://127.0.0.1:8123/" {
		t.Errorf("ChatURL: %q", got)
	}
}

// serve runs handlers on a free port of 127.0.0.1.
func serve(t *testing.T, handlers map[string]http.HandlerFunc) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for p, h := range handlers {
		mux.HandleFunc(p, h)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestRequest(t *testing.T) {
	var auth, ctype, body string
	port := serve(t, map[string]http.HandlerFunc{
		"/echo": func(w http.ResponseWriter, r *http.Request) {
			auth, ctype = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("short and stout"))
		},
		"/slow": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		},
	})
	c := LocalClient()
	r := Request(context.Background(), c, engine.Target{Port: port, APIKey: "sk-1"}, http.MethodPost, "/echo", []byte(`{"a":1}`), time.Second)
	if r.Err != nil || r.Code != http.StatusTeapot || string(r.Body) != "short and stout" {
		t.Fatalf("reply %+v", r)
	}
	if auth != "Bearer sk-1" || ctype != "application/json" || body != `{"a":1}` {
		t.Errorf("request: auth %q, type %q, body %q", auth, ctype, body)
	}
	r = Request(context.Background(), c, engine.Target{Port: port}, http.MethodGet, "/echo", nil, time.Second)
	if auth != "" || ctype != "" || r.Code != http.StatusTeapot {
		t.Errorf("no key, no body: auth %q, type %q, %+v", auth, ctype, r)
	}
	start := time.Now()
	r = Request(context.Background(), c, engine.Target{Port: port}, http.MethodGet, "/slow", nil, 100*time.Millisecond)
	if r.Err == nil || r.Code != 0 || time.Since(start) > 2*time.Second {
		t.Errorf("timeout: %+v after %v", r, time.Since(start))
	}
}

func TestParseGitHubRelease(t *testing.T) {
	for in, want := range map[string]string{
		`{"tag_name":"1.4.1","name":"Splash 1.4.1"}`: "1.4.1",
		`{"tag_name":"v2.0"}`:                        "2.0",
		`{"tag_name":" 1.0.1 "}`:                     "1.0.1",
	} {
		if got, err := ParseGitHubRelease([]byte(in)); err != nil || got != want {
			t.Errorf("%s: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{``, `{}`, `{"tag_name":""}`, `[1]`, `not json`} {
		if _, err := ParseGitHubRelease([]byte(in)); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestParseFormula(t *testing.T) {
	// The fields of https://formulae.brew.sh/api/formula/llama.cpp.json
	// on 2026-10-27, trimmed.
	live := `{"name":"llama.cpp","versions":{"stable":"0.7.0","head":"HEAD","bottle":true},"urls":{"stable":{"url":"https://github.com/ggml-org/llama.cpp.git","tag":"v0.7.0"}},"revision":0,"version_scheme":1}`
	for in, want := range map[string]string{
		live: "0.7.0",
		`{"versions":{"stable":"0.7.1"},"revision":2}`: "0.7.1",
		`{"versions":{"stable":" v1.0.0 "}}`:           "1.0.0",
	} {
		if got, err := ParseFormula([]byte(in)); err != nil || got != want {
			t.Errorf("%s: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{``, `{}`, `{"versions":{"stable":""}}`, `[1]`, `not json`} {
		if _, err := ParseFormula([]byte(in)); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
	if DropRevision("0.7.0_1") != "0.7.0" || DropRevision("1.4.0") != "1.4.0" {
		t.Error("DropRevision")
	}
}

func TestFeeds(t *testing.T) {
	var ua, accept string
	port := serve(t, map[string]http.HandlerFunc{
		"/latest": func(w http.ResponseWriter, r *http.Request) {
			ua, accept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
			_, _ = w.Write([]byte(`{"tag_name":"1.4.1"}`))
		},
		"/limited": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) },
		"/f": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"versions":{"stable":"0.7.2"},"revision":0}`))
		},
		"/down": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
	})
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	ctx := context.Background()
	if v, err := GitHubLatest(ctx, base+"/latest"); err != nil || v != "1.4.1" {
		t.Fatalf("GitHubLatest: %q, %v", v, err)
	}
	if ua == "" || !strings.Contains(accept, "json") {
		t.Errorf("headers: ua %q accept %q", ua, accept)
	}
	if _, err := GitHubLatest(ctx, base+"/limited"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("rate limit: %v", err)
	}
	if v, err := FormulaLatest(ctx, base+"/f"); err != nil || v != "0.7.2" {
		t.Errorf("FormulaLatest: %q, %v", v, err)
	}
	if _, err := FormulaLatest(ctx, base+"/down"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("bad gateway: %v", err)
	}
}

func TestParseProm(t *testing.T) {
	text := "# HELP splash_info Splash runtime metrics.\n" +
		"# TYPE splash_info gauge\n" +
		"splash_info{runtime=\"native\"} 1\n" +
		"splash_memory_pressure{state=\"normal\"} 0\n" +
		"splash_memory_pressure{state=\"warning\"} 1\n" +
		"splash_memory_pressure{state=\"critical\"} 0\n" +
		"splash_requests_completed_total 5\n" +
		"splash_ttft_p50_milliseconds 80.55616701\n" +
		"splash_big_bytes 3.9127154688e+10 1700000000\n" +
		"llamacpp:prompt_seconds_total 1.5\n" +
		"odd{le=\"a}\\\"b\",x=\"1\"} 7\n" +
		"splash_nan NaN\n" +
		"garbage line here\n" +
		"splash_no_value\n"
	p := ParseProm([]byte(text))
	if p.OneHot("splash_memory_pressure") != "warning" {
		t.Errorf("one-hot: %q", p.OneHot("splash_memory_pressure"))
	}
	if p.Num("splash_requests_completed_total") != 5 || p.Millis("splash_ttft_p50_milliseconds") != 80556167*time.Nanosecond {
		t.Errorf("plain samples: %v", p.Plain)
	}
	if p.Seconds("llamacpp:prompt_seconds_total") != 1500*time.Millisecond {
		t.Errorf("name with ':': %v", p.Plain)
	}
	if p.Num("splash_big_bytes") != 39127154688 {
		t.Errorf("exponent with timestamp: %d", p.Num("splash_big_bytes"))
	}
	if got := p.Labeled["odd"]["a}\"b"]; got != 7 {
		t.Errorf("quoted label with brace and quote: %v", p.Labeled["odd"])
	}
	if _, ok := p.Plain["splash_info"]; ok {
		t.Error("labelled sample stored as plain")
	}
	if p.Num("missing") != 0 || p.OneHot("missing") != "" {
		t.Error("missing sample is not zero")
	}
}
