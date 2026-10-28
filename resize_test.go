package main

import (
	"slices"
	"testing"
)

// The panel's OnResize resizes nothing while the frame builds: the resizes
// wait for post to run them, in the order the frames asked for them.
func TestAfterFrame(t *testing.T) {
	var queued []func()
	var sizes []int
	onResize := afterFrame(func(fn func()) { queued = append(queued, fn) }, func(h int) { sizes = append(sizes, h) })

	onResize(300) // a frame of the slide to Settings
	onResize(340) // the next one
	if len(sizes) != 0 {
		t.Fatalf("resized during the frame: %v", sizes)
	}
	if len(queued) != 2 {
		t.Fatalf("queued %d resizes, want 2", len(queued))
	}
	for _, fn := range queued {
		fn()
	}
	if want := []int{300, 340}; !slices.Equal(sizes, want) {
		t.Errorf("resized to %v, want %v", sizes, want)
	}
}
