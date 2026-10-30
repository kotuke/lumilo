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
// started at 1.2 s, loading for 2.2 s, then requests back to back, as a
// coding agent sends them, so the server stays generating: the live speed
// glides to each request's own and the request joins the chart when it
// ends. A request takes 2.85 s, long enough for the live bar of the chart
// (6 s to its full height) to grow visibly before it settles. Nothing drops to zero between requests, which would flicker the
// menu-bar icon between the speed and the memory.
func demoView(s float64) core.View {
	const start, loaded, firstReq, req, glide = 1.2, 3.4, 3.6, 2.85, 0.6
	switch {
	case s < start:
		return readmeSplash(core.PhaseStopped)
	case s < loaded:
		return readmeSplash(core.PhaseLoading)
	}
	v := readmeSplash(core.PhaseReady)
	v.Snapshot.MemoryUsed, v.Snapshot.DiskUsed = 18_900_000_000, 0
	v.Speed = core.Speed{FirstToken: 900 * time.Millisecond, Read: 1180}
	// The speeds of the requests: Qwen3.8-27B with its DFlash2 draft.
	speeds := []float64{36, 39, 38, 41, 39, 42, 40, 41}
	prev := speeds[0] * 0.6
	for i, top := range speeds {
		from := firstReq + float64(i)*req
		if s < from {
			break
		}
		if s < from+req {
			// Ease out from the last request's speed to this one's.
			u := min(1, (s-from)/glide)
			u = 1 - (1-u)*(1-u)
			v.Phase = core.PhaseGenerating
			v.Snapshot.Active, v.Snapshot.Queued = 1, max(1, 3-i) // the queue pill stays, the card keeps its height
			v.Snapshot.MemoryUsed = 19_600_000_000 + int64(i)*100_000_000
			v.Speed.Current = math.Round(prev + (top-prev)*u)
			break
		}
		v.Speed.History = append(v.Speed.History, top)
		prev = top
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
// with Qwen3.8-27B and serves requests back to back, 15 seconds at demoFPS.
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
			if err := os.WriteFile(filepath.Join(dir, f.Tray), trayicon.PNG(trayicon.For(v, "en", dark, liveTPS(v))), 0o644); err != nil {
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

// liveTPS is the speed the app shows for v without a hold.
func liveTPS(v core.View) float64 {
	if v.Phase == core.PhaseGenerating {
		return v.Speed.Current
	}
	return 0
}
