// Package terminal starts a command in a new window of a terminal app:
// Terminal, Ghostty or Rex.
//
// It writes a short-lived .command script and has the app run it. The
// script never holds a secret: a value that is a known secret is read from
// the login Keychain when the script runs.
package terminal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Paths of the tools the script and the launcher use.
const (
	openTool     = "/usr/bin/open"
	securityTool = "/usr/bin/security"
	shell        = "/bin/zsh"
	staleAfter   = time.Hour
)

// Config says where scripts go and where secrets live.
type Config struct {
	// Dir holds the scripts; it is created with mode 0700.
	Dir string
	// Service and Account name the Keychain item that holds a secret
	// whose Secret.Account is "".
	Service, Account string
}

// Secret is a value that must not reach the script, and the Keychain
// account (of Config.Service) the script reads it from at run time; ""
// is Config.Account. The caller makes sure the item holds the value.
type Secret struct {
	Value   string
	Account string
}

var (
	mu  sync.Mutex
	cfg Config
)

// Setup sets the configuration. Call it before Open.
func Setup(c Config) {
	mu.Lock()
	cfg = c
	mu.Unlock()
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Open runs argv in a new window of app with env on top of the terminal's
// own environment. An app that is not installed is replaced by Terminal. An env entry is NAME=value; an empty value unsets NAME.
// Every non-empty secret is replaced by a lookup of its Keychain item at
// run time. Open fails if a secret would still reach the file.
func Open(app App, argv, env []string, secrets ...Secret) error {
	mu.Lock()
	c := cfg
	mu.Unlock()
	if c.Dir == "" || c.Service == "" || c.Account == "" {
		return errors.New("terminal: not configured")
	}
	script, err := Script(argv, env, secrets, c.Service, c.Account)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	removeStale(c.Dir)
	path, err := writeScript(c.Dir, script)
	if err != nil {
		return err
	}
	app = Resolve(app)
	if err := launch(app, path); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// launch has app run the script at path.
func launch(app App, path string) error {
	if app == Rex {
		return openRex(bundle(Rex), path)
	}
	c := scriptCall(app, bundle(app), path)
	if out, err := runner(c.name, c.args...); err != nil {
		return fmt.Errorf("terminal: open: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// writeScript creates a new executable file with a random name.
func writeScript(dir, script string) (string, error) {
	var r [8]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "agent-"+hex.EncodeToString(r[:])+".command")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// removeStale deletes scripts that never ran, e.g. after a crash.
func removeStale(dir string) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if fi, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".command") &&
			time.Since(fi.ModTime()) > staleAfter {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Script builds the script text. It is pure, so tests can read it. account
// is the Keychain account of a secret without its own.
func Script(argv, env []string, secretValues []Secret, service, account string) (string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", errors.New("terminal: empty command")
	}
	var secrets []Secret
	for _, s := range secretValues {
		if s.Value == "" {
			continue
		}
		if s.Account == "" {
			s.Account = account
		}
		secrets = append(secrets, s)
	}
	// word quotes s for the shell, replacing each secret by its lookup.
	word := func(s string) string { return splice(s, secrets, service) }

	var b strings.Builder
	// -l: a login shell, so the user's PATH applies to the command.
	b.WriteString("#!" + shell + " -l\n")
	b.WriteString("rm -f \"$0\"\n")
	b.WriteString("cd ~\n")
	for _, kv := range env {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || !envName.MatchString(name) {
			return "", fmt.Errorf("terminal: bad environment entry %q", name)
		}
		if val == "" {
			b.WriteString("unset " + name + "\n")
			continue
		}
		b.WriteString("export " + name + "=" + word(val) + "\n")
	}
	b.WriteString("exec")
	for _, a := range argv {
		b.WriteString(" " + word(a))
	}
	b.WriteString("\n")
	out := b.String()
	for _, s := range secrets {
		if strings.Contains(out, s.Value) {
			return "", errors.New("terminal: a secret would be written to the script")
		}
	}
	return out, nil
}

// lookup is the shell text that reads the Keychain item of service and
// account.
func lookup(service, account string) string {
	return fmt.Sprintf(`"$(%s find-generic-password -s %s -a %s -w)"`,
		securityTool, quote(service), quote(account))
}

// splice quotes s for the shell. Each secret inside s becomes the lookup
// of its item; the text around it is single-quoted, and the parts join
// into one word.
func splice(s string, secrets []Secret, service string) string {
	if len(secrets) == 0 {
		return quote(s)
	}
	// Find the earliest, then longest, secret at each position.
	var out strings.Builder
	rest := s
	for rest != "" {
		at, n, acct := -1, 0, ""
		for _, sec := range secrets {
			if i := strings.Index(rest, sec.Value); i >= 0 && (at < 0 || i < at || i == at && len(sec.Value) > n) {
				at, n, acct = i, len(sec.Value), sec.Account
			}
		}
		if at < 0 {
			out.WriteString(quote(rest))
			break
		}
		if at > 0 {
			out.WriteString(quote(rest[:at]))
		}
		out.WriteString(lookup(service, acct))
		rest = rest[at+n:]
	}
	if out.Len() == 0 {
		return "''"
	}
	return out.String()
}

// quote wraps s in single quotes, the only quoting that keeps every
// character literal.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
