package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secret = "sk-0123456789abcdef0123456789abcdef"

func TestScriptLayout(t *testing.T) {
	got, err := Script(
		[]string{"/opt/homebrew/bin/splash", "claude", "it's here"},
		[]string{"SPLASH_PORT=8000", "OTHER=a b$c"}, nil, "ru.kotuke.lumilo", "api-key")
	if err != nil {
		t.Fatal(err)
	}
	want := "#!/bin/zsh -l\n" +
		"rm -f \"$0\"\n" +
		"cd ~\n" +
		"export SPLASH_PORT='8000'\n" +
		"export OTHER='a b$c'\n" +
		"exec '/opt/homebrew/bin/splash' 'claude' 'it'\\''s here'\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUnsetForEmptyValue(t *testing.T) {
	got, err := Script([]string{"/bin/echo"}, []string{"SPLASH_API_KEY="}, []Secret{{Value: secret}}, "svc", "acct")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\nunset SPLASH_API_KEY\n") || strings.Contains(got, "security") {
		t.Errorf("empty value must unset without a lookup:\n%s", got)
	}
	// An empty secret must not match empty values.
	got, _ = Script([]string{"/bin/echo"}, []string{"A="}, []Secret{{}}, "svc", "acct")
	if strings.Contains(got, "security") {
		t.Errorf("empty secret matched:\n%s", got)
	}
}

func TestSecretBecomesLookup(t *testing.T) {
	got, err := Script([]string{"/opt/homebrew/bin/splash", "pi"},
		[]string{"SPLASH_API_KEY=" + secret}, []Secret{{Value: secret}}, "ru.kotuke.lumilo.dev", "api-key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("secret in script:\n%s", got)
	}
	want := `export SPLASH_API_KEY="$(/usr/bin/security find-generic-password -s 'ru.kotuke.lumilo.dev' -a 'api-key' -w)"` + "\n"
	if !strings.Contains(got, want) {
		t.Errorf("missing lookup line:\n%s", got)
	}
}

func TestSecretInsideValue(t *testing.T) {
	got, err := Script([]string{"/bin/echo", "--key=" + secret},
		[]string{"H=Bearer " + secret + "!"}, []Secret{{Value: secret}}, "svc", "acct")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("secret in script:\n%s", got)
	}
	lookup := `"$(/usr/bin/security find-generic-password -s 'svc' -a 'acct' -w)"`
	for _, w := range []string{"export H='Bearer '" + lookup + "'!'", "'--key='" + lookup} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func TestBadInput(t *testing.T) {
	if _, err := Script(nil, nil, nil, "s", "a"); err == nil {
		t.Error("empty argv must fail")
	}
	for _, kv := range []string{"1BAD=x", "A B=x", "NOEQUALS", "A;rm=x"} {
		if _, err := Script([]string{"/bin/ls"}, []string{kv}, nil, "s", "a"); err == nil {
			t.Errorf("%q must fail", kv)
		}
	}
}

func TestQuote(t *testing.T) {
	cases := map[string]string{"": "''", "a": "'a'", "a'b": `'a'\''b'`, "$(x)`y`": "'$(x)`y`'", "a\nb": "'a\nb'"}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestWriteScript(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents")
	os.MkdirAll(dir, 0o700)
	p, err := writeScript(dir, "#!/bin/sh\n")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o700 || !strings.HasSuffix(p, ".command") {
		t.Errorf("%v %v", fi, err)
	}
}

func TestOpenNeedsSetup(t *testing.T) {
	Setup(Config{})
	if err := Open(Terminal, []string{"/bin/ls"}, nil); err == nil {
		t.Error("want an error without setup")
	}
}

// Two keys, each read from its own Keychain item: the running server's
// older key and the current one.
func TestSecretsWithOwnAccounts(t *testing.T) {
	const older = "sk-ffffffffffffffffffffffffffffffff"
	got, err := Script([]string{"/bin/echo", older}, []string{"K=" + secret},
		[]Secret{{Value: secret}, {Value: older, Account: "api-key.running"}}, "svc", "api-key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, secret) || strings.Contains(got, older) {
		t.Fatalf("secret in script:\n%s", got)
	}
	for _, w := range []string{
		`export K="$(/usr/bin/security find-generic-password -s 'svc' -a 'api-key' -w)"`,
		`'/bin/echo' "$(/usr/bin/security find-generic-password -s 'svc' -a 'api-key.running' -w)"`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}
