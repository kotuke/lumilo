package splash

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

var lockNameRe = regexp.MustCompile(`^serve-(\d+)\.lock$`)

// FindRunning lists servers by their lock files (ref §2.1). Each server holds
// an exclusive flock on runtime/serve-<port>.lock from launcher start until
// it dies, and the file stays behind afterwards, so a file proves nothing.
// A shared non-blocking flock fails only while a server holds the lock; if it
// succeeds the file is stale and we release it at once. The JSON
// {"pid","model","port"} is written after the lock, so it can be empty.
func (s *Splash) FindRunning() ([]engine.Running, error) {
	files, err := filepath.Glob(filepath.Join(s.runtimeDir, "serve-*.lock"))
	if err != nil {
		return nil, err
	}
	var out []engine.Running
	for _, path := range files {
		m := lockNameRe.FindStringSubmatch(filepath.Base(path))
		if m == nil {
			continue
		}
		port, _ := strconv.Atoi(m[1])
		if r, ok := readLive(path, port); ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out, nil
}

// readLive returns the server that holds the lock file, if any. Servers whose
// owner record is still empty after a retry are skipped: without a PID there
// is nothing safe to act on, and the launcher is only seconds into starting.
func readLive(path string, filePort int) (engine.Running, bool) {
	f, err := os.Open(path) // read-only, never creates
	if err != nil {
		return engine.Running{}, false
	}
	defer f.Close()
	if !lockedByOther(f) {
		return engine.Running{}, false
	}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(60 * time.Millisecond)
		}
		data, err := io.ReadAll(io.NewSectionReader(f, 0, 1<<16))
		if err != nil {
			continue
		}
		var rec struct {
			PID   int    `json:"pid"`
			Model string `json:"model"`
			Port  int    `json:"port"`
		}
		if json.Unmarshal(data, &rec) != nil || rec.PID <= 0 {
			continue
		}
		if rec.Port <= 0 {
			rec.Port = filePort
		}
		return engine.Running{PID: rec.PID, Port: rec.Port, Model: rec.Model}, true
	}
	return engine.Running{}, false
}

// lockedByOther reports whether another open file description holds a lock
// that conflicts with a shared one.
func lockedByOther(f *os.File) bool {
	fd := int(f.Fd())
	for {
		err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB)
		switch {
		case err == nil:
			_ = syscall.Flock(fd, syscall.LOCK_UN)
			return false
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return true
		default:
			return false
		}
	}
}
