package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// App is a terminal application, named by its bundle identifier. The same
// string is what the settings file stores.
type App string

// The terminals Lumilo can start an agent in.
const (
	Terminal App = "com.apple.Terminal"
	Ghostty  App = "com.mitchellh.ghostty"
	Rex      App = "com.superlogical.rex"
)

// Default is the terminal a fresh install uses; it is always present.
const Default = Terminal

// Known lists the terminals in the order the settings show them.
func Known() []App { return []App{Terminal, Ghostty, Rex} }

// Name is the name shown to the user. It is a product name, so it is the
// same in every language.
func (a App) Name() string {
	switch a {
	case Terminal:
		return "Terminal"
	case Ghostty:
		return "Ghostty"
	case Rex:
		return "Rex"
	}
	return string(a)
}

// Valid reports whether a is one of the known terminals.
func (a App) Valid() bool {
	for _, k := range Known() {
		if a == k {
			return true
		}
	}
	return false
}

// bundleNames are the file names of each app's bundle in /Applications and
// ~/Applications. Terminal lives in the system volume and needs no search.
var bundleNames = map[App][]string{
	Ghostty: {"Ghostty.app"},
	Rex:     {"Rex Beta.app", "Rex.app"},
}

// Where the tools are looked up; tests replace them.
var (
	stat     = os.Stat
	lookPath = exec.LookPath
	homeDir  = os.UserHomeDir
)

// bundle returns the path of the app's bundle, "" when it is not installed.
func bundle(a App) string {
	dirs := []string{"/Applications"}
	if h, err := homeDir(); err == nil && h != "" {
		dirs = append(dirs, filepath.Join(h, "Applications"))
	}
	for _, d := range dirs {
		for _, n := range bundleNames[a] {
			p := filepath.Join(d, n)
			if fi, err := stat(p); err == nil && fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

// rexCLI finds the Rex command-line tool: one on PATH, else the copy inside
// the app bundle.
func rexCLI(bundlePath string) string {
	if p, err := lookPath("rex"); err == nil {
		return p
	}
	p := filepath.Join(bundlePath, "Contents", "Helpers", "Rex Server.rex", "Contents", "MacOS", "rex")
	if fi, err := stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return ""
}

// Installed lists the terminals present on this computer, Terminal first.
// Rex counts only when its command-line tool is there too, since Lumilo
// drives it through that tool.
func Installed() []App {
	out := []App{Terminal}
	for _, a := range Known()[1:] {
		b := bundle(a)
		if b == "" || a == Rex && rexCLI(b) == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// Resolve returns the terminal to use for a saved choice: the choice when
// it is installed, else Terminal.
func Resolve(a App) App {
	for _, i := range Installed() {
		if i == a {
			return a
		}
	}
	return Terminal
}

// A call is one program run.
type call struct {
	name string
	args []string
}

// scriptCall is the command that opens script in a new Terminal window, or
// in a new Ghostty window that stays after the agent exits.
//
// Ghostty gets the script as its command (-e) in a process of its own
// (open -n). Opening the file with "open -a Ghostty" does not do: Ghostty
// asks for a confirmation before it runs a script opened that way. The
// "-e" flag and "wait-after-command" are described in
// `ghostty +show-config --docs`; "open -n ... --args" in `ghostty --help`.
func scriptCall(a App, bundlePath, script string) call {
	if a == Ghostty {
		return call{openTool, []string{"-n", "-a", bundlePath, "--args", "--wait-after-command=true", "-e", script}}
	}
	return call{openTool, []string{"-a", "Terminal", script}}
}

// runner runs a program and returns its output. Tests replace it.
var runner = func(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// sleep waits between polls; tests replace it.
var sleep = time.Sleep

// rexSession is the session in Rex that holds the agent tabs when there are
// several sessions. Rex refuses a command that names no session then, so
// Lumilo keeps one of its own.
const rexSession = "Lumilo"

// rexWait is how long Lumilo waits for the Rex app to connect after it
// started the app.
const rexWait = 10 * time.Second

// openRex runs script in a new tab of the Lumilo session in Rex, starting
// the app first when it does not run. The commands are those of the Rex
// command-line tool (`rex run --help`, `rex window new --help`, and the
// guide printed by `rex __internal skill`).
func openRex(bundlePath, script string) error {
	cli := rexCLI(bundlePath)
	if cli == "" {
		return errors.New("terminal: Rex command-line tool not found")
	}
	client, err := rexAppClient(cli)
	if err != nil {
		return err
	}
	if client == "" {
		if out, err := runner(openTool, "-a", bundlePath); err != nil {
			return fmt.Errorf("terminal: start Rex: %w: %s", err, strings.TrimSpace(string(out)))
		}
		for waited := time.Duration(0); client == "" && waited < rexWait; waited += 250 * time.Millisecond {
			sleep(250 * time.Millisecond)
			if client, err = rexAppClient(cli); err != nil {
				return err
			}
		}
		if client == "" {
			return errors.New("terminal: Rex did not start")
		}
	}

	tab := []string{"--keep-open", "--shell=none", "--json"}
	if home, _ := homeDir(); home != "" {
		tab = append(tab, "-c", home)
	}
	id, err := rexPickSession(cli)
	if err != nil {
		return err
	}
	var out []byte
	if id == "" {
		args := append([]string{"new", rexSession, "--window", "Agent"}, tab...)
		out, err = runner(cli, append(args, "--", script)...)
		if err == nil {
			var r struct {
				SessionID string `json:"session_id"`
			}
			if json.Unmarshal(out, &r) == nil {
				id = r.SessionID
			}
		}
	} else {
		args := append([]string{"window", "new", "-s", id, "Agent"}, tab...)
		out, err = runner(cli, append(args, "--", script)...)
	}
	if err != nil {
		return fmt.Errorf("terminal: rex: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Best effort: show the session in the app (it needs "Remote Control"
	// in the Rex settings) and bring the app forward.
	if id != "" {
		_, _ = runner(cli, "-C", client, "do", "session.select", "session_id="+id)
	}
	_, _ = runner(openTool, "-a", bundlePath)
	return nil
}

// rexAppClient returns the id of a connected Rex app, "" when none.
func rexAppClient(cli string) (string, error) {
	out, err := runner(cli, "client", "ls", "--json")
	if err != nil {
		return "", fmt.Errorf("terminal: rex client ls: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var r struct {
		Clients []struct {
			ID   string `json:"client_id"`
			Info struct {
				Kind string `json:"kind"`
			} `json:"info"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", fmt.Errorf("terminal: rex client ls: %w", err)
	}
	for _, c := range r.Clients {
		if c.Info.Kind == "app" {
			return c.ID, nil
		}
	}
	return "", nil
}

// rexPickSession picks the session for the tab: the only one there is, since
// the app shows it; else the Lumilo session. It returns "" when a session
// has to be made.
func rexPickSession(cli string) (string, error) {
	out, err := runner(cli, "ls", "--json")
	if err != nil {
		return "", fmt.Errorf("terminal: rex ls: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var r struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", fmt.Errorf("terminal: rex ls: %w", err)
	}
	if len(r.Sessions) == 1 {
		return r.Sessions[0].ID, nil
	}
	for _, s := range r.Sessions {
		if s.Label == rexSession {
			return s.ID, nil
		}
	}
	return "", nil
}
