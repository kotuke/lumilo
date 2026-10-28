package llamacpp

import (
	"net"
	"os"
	"slices"
	"testing"
)

// runLsof finds this test process on a port it listens on, and nothing on
// a closed one.
func TestRunLsof(t *testing.T) {
	if _, err := os.Stat(lsofTool); err != nil {
		t.Skip("no lsof")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	pids, err := runLsof(ln.Addr().(*net.TCPAddr).Port)
	if err != nil || !slices.Contains(pids, os.Getpid()) {
		t.Errorf("pids %v, %v; want %d", pids, err, os.Getpid())
	}
	if pids, err := runLsof(closedPort(t)); err != nil || len(pids) != 0 {
		t.Errorf("closed port: %v, %v", pids, err)
	}
}

func TestParsePIDs(t *testing.T) {
	if got := parsePIDs([]byte("123\n456\nx\n0\n")); !slices.Equal(got, []int{123, 456}) {
		t.Errorf("pids %v", got)
	}
}
