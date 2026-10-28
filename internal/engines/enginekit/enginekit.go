// Package enginekit holds what engine implementations share: the HTTP
// request to a local server, the Prometheus text parser, the lookup of
// executables, the child environment, logos, the Hugging Face hub cache,
// release feeds and the Homebrew installer (Homebrew). The supervisor and
// the panel never import it: they see engines through package engine.
package enginekit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SystemPath is the PATH given to every child. A Finder-launched app has
// a minimal PATH; engines and brew need none of the user's.
const SystemPath = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// StdBins are where Homebrew links an executable called name, Apple
// silicon first: /opt/homebrew/bin, then /usr/local/bin.
func StdBins(name string) []string {
	return []string{"/opt/homebrew/bin/" + name, "/usr/local/bin/" + name}
}

// FirstExecutable is the first of paths that is an executable regular file
// (symlinks followed), "" for none. PATH is never consulted.
func FirstExecutable(paths []string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// Uniq drops empty and repeated strings, keeping the first of each. It
// reuses the backing array of xs.
func Uniq(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Environ is a child's environment: the inherited one without PATH and
// without the variables drop names (a stray key, port or offline switch
// that would change what Lumilo asks for), plus an explicit PATH of
// SystemPath, extraDirs and the inherited PATH. It is complete, so it works
// whether the caller replaces or extends its own environment (the last
// duplicate wins).
func Environ(drop func(name string) bool, extraDirs ...string) []string {
	env := make([]string, 0, 64)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name != "PATH" && (drop == nil || !drop(name)) {
			env = append(env, kv)
		}
	}
	dirs := strings.Split(SystemPath, ":")
	dirs = append(dirs, extraDirs...)
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	return append(env, "PATH="+strings.Join(Uniq(dirs), ":"))
}

// MustFile reads name from an embedded file system; it panics when the
// file is missing, which a build with the file embedded never does.
func MustFile(fsys fs.FS, name string) []byte {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		panic(err)
	}
	return b
}

// ChatURL is the address of a chat page served at the root of a local
// server on port.
func ChatURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
}
