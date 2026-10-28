package main

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/egoist/mygo"
)

// secretAccount is the Keychain account (the mygo.Secrets key) of the API key.
const secretAccount = "api-key"

// setupLog sends the standard log to a file, since a Finder-launched app
// has no stderr that anyone sees. Output still goes to stderr under
// `mygo dev`.
func setupLog() {
	dir, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, "lumilo.log")
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC // keep the log small
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return
	}
	// The file goes first: MultiWriter stops at the first writer that fails,
	// and stderr of a Finder app may be closed.
	log.SetOutput(io.MultiWriter(f, os.Stderr))
}

// localHostName is the Bonjour name of this Mac ("MacBook"), or "".
func localHostName() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "LocalHostName").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// physicalMemory is the computer's memory in bytes (sysctl hw.memsize, a
// little-endian uint64), 0 when unknown. syscall.Sysctl drops one trailing
// NUL byte, which hw.memsize has for every size below 2^56.
func physicalMemory() int64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil || len(s) == 0 || len(s) > 8 {
		return 0
	}
	var b [8]byte
	copy(b[:], s)
	return int64(binary.LittleEndian.Uint64(b[:]))
}

// lanAddress is the first private IPv4 address of an up, non-loopback
// interface, or "".
func lanAddress() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip := n.IP.To4(); ip != nil && ip.IsPrivate() {
					return ip.String()
				}
			}
		}
	}
	return ""
}

// secretService is the Keychain service mygo.Secrets uses for this app:
// the identifier of the running bundle (ru.kotuke.lumilo, or
// ru.kotuke.lumilo.dev under `mygo dev`), else the app name. It mirrors
// mygo's appID (login.go:56-60), which reads NSBundle's bundleIdentifier.
func secretService() string {
	if exe, err := os.Executable(); err == nil {
		// <name>.app/Contents/MacOS/<exe>
		contents := filepath.Dir(filepath.Dir(exe))
		if strings.HasSuffix(filepath.Dir(contents), ".app") {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier",
				"raw", "-o", "-", filepath.Join(contents, "Info.plist")).Output()
			if id := strings.TrimSpace(string(out)); err == nil && id != "" {
				return id
			}
		}
	}
	return mygo.App.Name()
}
