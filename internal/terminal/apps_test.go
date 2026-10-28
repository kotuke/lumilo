package terminal

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeFS makes stat see only the listed directories and files.
func fakeFS(t *testing.T, home string, dirs, files []string) {
	t.Helper()
	oldStat, oldHome, oldLook := stat, homeDir, lookPath
	t.Cleanup(func() { stat, homeDir, lookPath = oldStat, oldHome, oldLook })
	homeDir = func() (string, error) { return home, nil }
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	stat = func(p string) (os.FileInfo, error) {
		for _, d := range dirs {
			if p == d {
				return dirInfo{}, nil
			}
		}
		for _, f := range files {
			if p == f {
				return fileInfo{}, nil
			}
		}
		return nil, fs.ErrNotExist
	}
}

type fileInfo struct{ os.FileInfo }

func (fileInfo) IsDir() bool { return false }

type dirInfo struct{ os.FileInfo }

func (dirInfo) IsDir() bool { return true }

const rexBin = "/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex"

func TestInstalled(t *testing.T) {
	fakeFS(t, "/Users/me", nil, nil)
	if got := Installed(); !reflect.DeepEqual(got, []App{Terminal}) {
		t.Errorf("nothing installed: %v", got)
	}

	fakeFS(t, "/Users/me", []string{"/Users/me/Applications/Ghostty.app", "/Applications/Rex Beta.app"}, nil)
	if got := Installed(); !reflect.DeepEqual(got, []App{Terminal, Ghostty}) {
		t.Errorf("Rex without its tool must not count: %v", got)
	}

	fakeFS(t, "/Users/me", []string{"/Applications/Ghostty.app", "/Applications/Rex Beta.app"}, []string{rexBin})
	if got := Installed(); !reflect.DeepEqual(got, []App{Terminal, Ghostty, Rex}) {
		t.Errorf("all installed: %v", got)
	}
}

func TestResolve(t *testing.T) {
	fakeFS(t, "/Users/me", []string{"/Applications/Ghostty.app"}, nil)
	for in, want := range map[App]App{Terminal: Terminal, Ghostty: Ghostty, Rex: Terminal, "": Terminal, "org.x.y": Terminal} {
		if got := Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScriptCall(t *testing.T) {
	got := scriptCall(Terminal, "", "/tmp/a.command")
	want := call{"/usr/bin/open", []string{"-a", "Terminal", "/tmp/a.command"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Terminal: %v", got)
	}
	got = scriptCall(Ghostty, "/Applications/Ghostty.app", "/tmp/a.command")
	want = call{"/usr/bin/open", []string{"-n", "-a", "/Applications/Ghostty.app", "--args", "--wait-after-command=true", "-e", "/tmp/a.command"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ghostty: %v", got)
	}
}

// rexRun fakes the Rex tool: it records the calls and answers from replies,
// by call prefix; the outputs of a prefix are used in turn, the last one
// repeats.
type rexRun struct {
	calls   []string
	replies map[string][]string
}

func (r *rexRun) run(name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for k, outs := range r.replies {
		if strings.HasPrefix(line, k) && len(outs) > 0 {
			out := outs[0]
			if len(outs) > 1 {
				r.replies[k] = outs[1:]
			}
			return []byte(out), nil
		}
	}
	return []byte("{}"), nil
}

func fakeRex(t *testing.T, replies map[string][]string) *rexRun {
	t.Helper()
	r := &rexRun{replies: replies}
	fakeFS(t, "/Users/me", []string{"/Applications/Rex Beta.app"}, []string{rexBin})
	oldRun, oldSleep := runner, sleep
	t.Cleanup(func() { runner, sleep = oldRun, oldSleep })
	runner, sleep = r.run, func(time.Duration) {}
	return r
}

const (
	appClient = `{"clients":[{"client_id":"client:app","info":{"kind":"app"}},{"client_id":"client:cli","info":{"kind":"cli"}}]}`
	cliOnly   = `{"clients":[{"client_id":"client:cli","info":{"kind":"cli"}}]}`
)

func TestOpenRexNewSession(t *testing.T) {
	r := fakeRex(t, map[string][]string{
		rexBin + " client ls": {appClient},
		rexBin + " ls":        {`{"sessions":[{"session_id":"session:a","label":"calm-sea"},{"session_id":"session:b","label":"big-sky"}]}`},
		rexBin + " new":       {`{"session_id":"session:new"}`},
	})
	if err := openRex("/Applications/Rex Beta.app", "/tmp/a.command"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		rexBin + " client ls --json",
		rexBin + " ls --json",
		rexBin + " new Lumilo --window Agent --keep-open --shell=none --json -c /Users/me -- /tmp/a.command",
		rexBin + " -C client:app do session.select session_id=session:new",
		"/usr/bin/open -a /Applications/Rex Beta.app",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestOpenRexKnownSession(t *testing.T) {
	r := fakeRex(t, map[string][]string{
		rexBin + " client ls": {appClient},
		rexBin + " ls":        {`{"sessions":[{"session_id":"session:x","label":"calm-sea"},{"session_id":"session:l","label":"Lumilo"}]}`},
	})
	if err := openRex("/Applications/Rex Beta.app", "/tmp/a.command"); err != nil {
		t.Fatal(err)
	}
	want := rexBin + " window new -s session:l Agent --keep-open --shell=none --json -c /Users/me -- /tmp/a.command"
	if r.calls[2] != want {
		t.Errorf("got %s\nwant %s", r.calls[2], want)
	}
}

// With one session, that is the one the app shows: the tab goes there.
func TestOpenRexOnlySession(t *testing.T) {
	r := fakeRex(t, map[string][]string{
		rexBin + " client ls": {appClient},
		rexBin + " ls":        {`{"sessions":[{"session_id":"session:only","label":"calm-sea"}]}`},
	})
	if err := openRex("/Applications/Rex Beta.app", "/tmp/a.command"); err != nil {
		t.Fatal(err)
	}
	want := rexBin + " window new -s session:only Agent --keep-open --shell=none --json -c /Users/me -- /tmp/a.command"
	if r.calls[2] != want {
		t.Errorf("got %s\nwant %s", r.calls[2], want)
	}
}

func TestOpenRexStartsApp(t *testing.T) {
	r := fakeRex(t, map[string][]string{
		rexBin + " client ls": {cliOnly, cliOnly, appClient},
		rexBin + " ls":        {`{"sessions":[]}`},
		rexBin + " new":       {`{"session_id":"session:new"}`},
	})
	if err := openRex("/Applications/Rex Beta.app", "/tmp/a.command"); err != nil {
		t.Fatal(err)
	}
	if r.calls[1] != "/usr/bin/open -a /Applications/Rex Beta.app" {
		t.Errorf("the app must start before the tab opens: %v", r.calls)
	}
	n := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, rexBin+" client ls") {
			n++
		}
	}
	if n != 3 {
		t.Errorf("polled %d times, want 3", n)
	}
}

func TestOpenRexAppNeverConnects(t *testing.T) {
	fakeRex(t, map[string][]string{rexBin + " client ls": {cliOnly}})
	if err := openRex("/Applications/Rex Beta.app", "/tmp/a.command"); err == nil {
		t.Error("want an error when the app does not connect")
	}
}

func TestOpenRemovesScriptOnFailure(t *testing.T) {
	fakeFS(t, "/Users/me", nil, nil)
	oldRun := runner
	t.Cleanup(func() { runner = oldRun; Setup(Config{}) })
	runner = func(string, ...string) ([]byte, error) { return []byte("boom"), errors.New("exit 1") }
	dir := filepath.Join(t.TempDir(), "agents")
	Setup(Config{Dir: dir, Service: "svc", Account: "acct"})
	if err := Open(Terminal, []string{"/bin/ls"}, nil); err == nil {
		t.Fatal("want an error")
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("script left behind: %v", es)
	}
}

// A secret reaches no launch command: they name the script file, and the
// file holds a Keychain lookup.
func TestOpenKeepsSecretOutOfCommandsAndFile(t *testing.T) {
	fakeFS(t, "/Users/me", []string{"/Applications/Ghostty.app"}, nil)
	var calls []call
	oldRun := runner
	t.Cleanup(func() { runner = oldRun; Setup(Config{}) })
	runner = func(n string, a ...string) ([]byte, error) { calls = append(calls, call{n, a}); return nil, nil }
	dir := filepath.Join(t.TempDir(), "agents")
	Setup(Config{Dir: dir, Service: "svc", Account: "acct"})
	if err := Open(Ghostty, []string{"/bin/echo", "--key=" + secret}, []string{"K=" + secret}, Secret{Value: secret}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].args[len(calls[0].args)-2] != "-e" {
		t.Fatalf("calls: %v", calls)
	}
	for _, a := range calls[0].args {
		if strings.Contains(a, secret) {
			t.Errorf("secret in argument %q", a)
		}
	}
	es, _ := os.ReadDir(dir)
	if len(es) != 1 {
		t.Fatalf("files: %v", es)
	}
	b, _ := os.ReadFile(filepath.Join(dir, es[0].Name()))
	if strings.Contains(string(b), secret) || !strings.Contains(string(b), "find-generic-password") {
		t.Errorf("script:\n%s", b)
	}
}
