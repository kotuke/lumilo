package main

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// physicalMemory reads the same number as "sysctl -n hw.memsize".
func TestPhysicalMemory(t *testing.T) {
	out, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		t.Skip("no sysctl:", err)
	}
	want, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if got := physicalMemory(); got != want {
		t.Errorf("physicalMemory %d, sysctl %d", got, want)
	}
}
