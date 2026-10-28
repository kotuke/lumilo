package enginekit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBrew(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "brew")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunBrewSuccess(t *testing.T) {
	brew := fakeBrew(t, `echo "args: $*"; echo "env: $NONINTERACTIVE $HOMEBREW_NO_ENV_HINTS $PATH"; echo; echo "done"`)
	var log bytes.Buffer
	last, err := RunBrew(context.Background(), brew, []string{"upgrade", "x/y/z"}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if last != "done" {
		t.Errorf("lastLine = %q, want done", last)
	}
	out := log.String()
	if !strings.Contains(out, "args: upgrade x/y/z") || !strings.Contains(out, "env: 1 1 "+SystemPath) {
		t.Errorf("log = %q", out)
	}
}

func TestRunBrewFailure(t *testing.T) {
	brew := fakeBrew(t, `echo "Downloading"; printf 'Error: no bottle available\n\n' >&2; exit 1`)
	last, err := RunBrew(context.Background(), brew, []string{"install", "f"}, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if last != "Error: no bottle available" {
		t.Errorf("lastLine = %q", last)
	}
}

func TestRunBrewPartialLastLine(t *testing.T) {
	brew := fakeBrew(t, `printf 'one\ntwo'`)
	last, err := RunBrew(context.Background(), brew, nil, nil)
	if err != nil || last != "two" {
		t.Errorf("got %q, %v", last, err)
	}
}

func TestRunBrewMissing(t *testing.T) {
	if _, err := RunBrew(context.Background(), "/nonexistent/brew", nil, nil); err == nil {
		t.Fatal("want error")
	}
}
