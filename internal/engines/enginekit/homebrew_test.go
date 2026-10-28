package enginekit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

// fakeRunner records brew runs and answers with last and err.
type fakeRunner struct {
	runs []string // "brewPath args..."
	last string
	err  error
}

func (f *fakeRunner) run(_ context.Context, brew string, args []string, log io.Writer) (string, error) {
	f.runs = append(f.runs, brew+" "+strings.Join(args, " "))
	if log != nil {
		_, _ = io.WriteString(log, "==> brew output\n")
	}
	return f.last, f.err
}

func newBrew(t *testing.T) (*Homebrew, *fakeRunner, string) {
	t.Helper()
	root := t.TempDir()
	run := &fakeRunner{last: "done"}
	h := &Homebrew{
		Formula: "tap/eng",
		Size:    42_000_000,
		Bins:    []string{filepath.Join(root, "missing/eng"), filepath.Join(root, "bin/eng")},
		Brews:   []string{filepath.Join(root, "bin/brew")},
		Version: func(_ context.Context, bin string) string { return "1.2.3 at " + bin },
		Feed:    func(context.Context) (string, error) { return "1.3.0", nil },
		Notes:   func(v string) string { return "https://example.com/" + v },
		Run:     run.run,
	}
	return h, run, root
}

func TestHomebrewDescribe(t *testing.T) {
	h, _, _ := newBrew(t)
	want := engine.InstallInfo{
		Method: engine.InstallHomebrew, Size: 42_000_000,
		InstallCommand: "brew install tap/eng", UpgradeCommand: "brew upgrade tap/eng",
		Prerequisite: engine.Prerequisite{Name: "Homebrew", URL: "https://brew.sh", Host: "brew.sh",
			Arch: "Apple silicon", OS: "macOS 26.4"},
	}
	if got := h.Describe(); got != want {
		t.Errorf("Describe:\n got %+v\nwant %+v", got, want)
	}
	if got := h.ReleaseNotesURL("1.3.0"); got != "https://example.com/1.3.0" {
		t.Errorf("notes: %q", got)
	}
	if v, err := h.Latest(context.Background()); err != nil || v != "1.3.0" {
		t.Errorf("Latest: %q, %v", v, err)
	}
	if v, err := (&Homebrew{}).Latest(context.Background()); err == nil || v != "" {
		t.Errorf("no feed: %q, %v", v, err)
	}
}

func TestHomebrewDetect(t *testing.T) {
	h, _, root := newBrew(t)
	ctx := context.Background()
	if in := h.Detect(ctx); in != (engine.Install{}) {
		t.Errorf("nothing there: %+v", in)
	}
	if h.Bin() != h.Bins[0] {
		t.Errorf("Bin before install: %q", h.Bin())
	}
	write(t, filepath.Join(root, "bin/brew"), "#!/bin/sh\n", 0o755)
	if in := h.Detect(ctx); in.Installed || !in.Ready {
		t.Errorf("brew only: %+v", in)
	}
	bin := filepath.Join(root, "bin/eng")
	write(t, bin, "#!/bin/sh\n", 0o755)
	want := engine.Install{Installed: true, Version: "1.2.3 at " + bin, Path: bin, Ready: true}
	if in := h.Detect(ctx); in != want {
		t.Errorf("installed: %+v", in)
	}
	if h.Bin() != bin {
		t.Errorf("Bin: %q", h.Bin())
	}
}

func TestHomebrewRuns(t *testing.T) {
	h, run, root := newBrew(t)
	ctx := context.Background()
	if _, err := h.Install(ctx, nil); err == nil || len(run.runs) != 0 {
		t.Errorf("install without brew: %v, runs %v", err, run.runs)
	}
	brew := filepath.Join(root, "bin/brew")
	write(t, brew, "#!/bin/sh\n", 0o755)
	var log bytes.Buffer
	if last, err := h.Install(ctx, &log); err != nil || last != "done" {
		t.Errorf("install: %q, %v", last, err)
	}
	run.last, run.err = "Error: no bottle", errors.New("exit status 1")
	if last, err := h.Upgrade(ctx, &log); err == nil || last != "Error: no bottle" {
		t.Errorf("failed upgrade: %q, %v", last, err)
	}
	if want := []string{brew + " install tap/eng", brew + " upgrade tap/eng"}; strings.Join(run.runs, "|") != strings.Join(want, "|") {
		t.Errorf("runs %q", run.runs)
	}
	if strings.Count(log.String(), "==> brew output") != 2 {
		t.Errorf("log %q", log.String())
	}
}
