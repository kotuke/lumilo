package panel

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/trayicon"
)

// demoFPS is the frame rate of the README's demo (scripts/readme-images.py).
const demoFPS = 20

// demoFrame is what scripts/readme-images.py needs beside a frame's
// pictures: where the pointer is and whether it presses, in 2x pixels.
type demoFrame struct {
	Panel string     `json:"panel"`
	Tray  string     `json:"tray"`
	Click [2]float32 `json:"click"`
	Press bool       `json:"press"`
}

// demoView is Splash with Qwen3.8-27B at second s of the demo: stopped,
// started at 1.2 s, loading for 3 s, then eight requests, the speed rising
// live in each and joining the chart when it ends.
func demoView(s float64) core.View {
	const start, loaded, firstReq, req, pause = 1.2, 4.2, 5.0, 1.0, 0.2
	switch {
	case s < start:
		return readmeSplash(core.PhaseStopped)
	case s < loaded:
		return readmeSplash(core.PhaseLoading)
	}
	v := readmeSplash(core.PhaseReady)
	v.Snapshot.MemoryUsed, v.Snapshot.DiskUsed = 18_900_000_000, 0
	v.Speed = core.Speed{FirstToken: 900 * time.Millisecond, Read: 1180}
	// The speeds of the eight requests: Qwen3.8-27B with its DFlash2 draft.
	done := []float64{34, 39, 37, 41, 38, 42, 36, 40}
	for i, top := range done {
		from := firstReq + float64(i)*(req+pause)
		if s < from {
			break
		}
		if s < from+req {
			// The live speed climbs to the request's own in its first
			// half second and wavers around it.
			t := s - from
			v.Phase = core.PhaseGenerating
			v.Snapshot.Active, v.Snapshot.Queued = 1, max(0, 3-i)
			v.Snapshot.MemoryUsed = 19_600_000_000 + int64(i)*150_000_000
			v.Speed.Current = math.Round(top * min(1, t/0.5) * (1 + 0.04*math.Sin(t*9)))
			break
		}
		v.Speed.History = append(v.Speed.History, top)
		v.Snapshot.MemoryUsed = 19_400_000_000 + int64(i)*150_000_000
	}
	if n := len(v.Speed.History); n > 0 {
		var sum float64
		for _, x := range v.Speed.History {
			sum += x
		}
		v.Speed.Average = math.Round(sum / float64(n))
	}
	return v
}

// LUMILO_DEMO_DIR=… saves the demo's frames in both themes at 2x with the
// menu-bar icon of each and frames.json: the panel as Lumilo starts Splash
// with Qwen3.8-27B and serves eight requests, 15 seconds at demoFPS.
func TestDemo(t *testing.T) {
	dir := os.Getenv("LUMILO_DEMO_DIR")
	if dir == "" {
		t.Skip("LUMILO_DEMO_DIR is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const length, step = 15.0, time.Second / demoFPS
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{true: "dark", false: "light"}[dark]
		b := newBench(t, "en")
		b.tt.SetDark(dark)
		b.tt.SetScale(2)
		b.show(demoView(0), 0)
		var frames []demoFrame
		for i := 0; float64(i)/demoFPS < length; i++ {
			s := float64(i) / demoFPS
			v := demoView(s)
			b.p.SetView(v)
			b.clock = b.clock.Add(step)
			b.tt.Advance(step)
			b.tt.SetSize(Width, b.p.height)
			b.tt.Frame()
			f := demoFrame{Panel: fmt.Sprintf("panel-%s-%03d.png", theme, i),
				Tray: fmt.Sprintf("tray-%s-%03d.png", theme, i)}
			// The pointer comes to Start and presses it at 1.0–1.2 s.
			if s < 1.2 {
				r, ok := b.tt.Find(b.m.ActStart())
				if !ok {
					t.Fatalf("no Start in %q", b.tt.Texts())
				}
				f.Click = [2]float32{2 * (r.X + r.W/2), 2 * (r.Y + r.H/2)}
				f.Press = s >= 1.0
			}
			save(t, b.tt, filepath.Join(dir, f.Panel))
			if err := os.WriteFile(filepath.Join(dir, f.Tray), trayicon.PNG(trayicon.For(v, "en", dark)), 0o644); err != nil {
				t.Fatal(err)
			}
			frames = append(frames, f)
		}
		js, err := json.Marshal(frames)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "frames-"+theme+".json"), js, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
