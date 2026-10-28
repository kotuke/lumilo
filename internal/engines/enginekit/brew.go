package enginekit

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// RunBrew runs brewPath with args and waits for it. Its stdout and stderr
// go to log (may be nil). lastLine is the last non-empty output line, for
// error messages; err is non-nil when brew could not start or exited
// non-zero.
func RunBrew(ctx context.Context, brewPath string, args []string, log io.Writer) (lastLine string, err error) {
	cmd := exec.CommandContext(ctx, brewPath, args...)
	home, _ := os.UserHomeDir()
	cmd.Env = []string{
		"PATH=" + SystemPath,
		"HOME=" + home,
		"HOMEBREW_NO_ENV_HINTS=1",
		"NONINTERACTIVE=1",
	}
	if log == nil {
		log = io.Discard
	}
	lw := &lastLineWriter{w: log}
	cmd.Stdout = lw
	cmd.Stderr = lw
	err = cmd.Run()
	return lw.last(), err
}

// lastLineWriter copies output to w and remembers the last non-empty line.
type lastLineWriter struct {
	mu      sync.Mutex
	w       io.Writer
	partial string
	line    string
}

func (l *lastLineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(p) // a failed log write must not fail brew
	lines := strings.Split(l.partial+string(p), "\n")
	l.partial = lines[len(lines)-1]
	for _, s := range lines[:len(lines)-1] {
		if s = strings.TrimSpace(s); s != "" {
			l.line = s
		}
	}
	return len(p), nil
}

func (l *lastLineWriter) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := strings.TrimSpace(l.partial); s != "" {
		return s
	}
	return l.line
}
