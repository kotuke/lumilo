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

// demoReq is one request of the demo: its speed and how long it runs.
type demoReq struct{ speed, dur float64 }

// demoReqs are the demo's requests: short and of different speeds, as a
// coding agent sends them (Qwen3.8-27B with its DFlash2 draft), enough to
// fill the chart's 20 bars.
var demoReqs = []demoReq{
	{36, 0.9}, {42, 0.6}, {31, 0.8}, {39, 0.5}, {45, 0.7}, {34, 0.6}, {40, 0.9}, {29, 0.5},
	{43, 0.6}, {37, 0.8}, {47, 0.5}, {33, 0.7}, {41, 0.6}, {38, 0.5}, {44, 0.8}, {30, 0.6},
	{46, 0.5}, {35, 0.7}, {42, 0.6}, {39, 0.9},
}

// demoSpeedup is how much faster than real time the requests run: the
// demo shows in a second what takes eight, so it counts eight seconds of
// tokens.
const demoSpeedup = 8

// demoView is Splash with Qwen3.8-27B at second s of the demo: stopped,
// started at 1.2 s, loading for 2.2 s, then requests back to back, as a
// coding agent sends them, so the server stays generating: the live speed
// glides to each request's own, its tokens count up, and the request
// joins the chart when it ends. Nothing drops to zero between requests,
// which would flicker the menu-bar icon between the speed and the memory.
func demoView(s float64) core.View {
	const start, loaded, firstReq, glide = 1.2, 3.4, 3.6, 0.25
	switch {
	case s < start:
		return readmeSplash(core.PhaseStopped)
	case s < loaded:
		return readmeSplash(core.PhaseLoading)
	}
	v := readmeSplash(core.PhaseReady)
	v.Snapshot.MemoryUsed, v.Snapshot.DiskUsed = 18_900_000_000, 0
	v.Speed = core.Speed{FirstToken: 900 * time.Millisecond, Read: 1180}
	prev, from := demoReqs[0].speed*0.6, firstReq
	for i, r := range demoReqs {
		if s < from {
			break
		}
		if s < from+r.dur {
			// Ease out from the last request's speed to this one's.
			u := min(1, (s-from)/glide)
			u = 1 - (1-u)*(1-u)
			v.Phase = core.PhaseGenerating
			v.Snapshot.Active, v.Snapshot.Queued = 1, max(1, 3-i) // the queue pill stays, the card keeps its height
			v.Snapshot.MemoryUsed = 19_600_000_000 + int64(i%8)*100_000_000
			v.Speed.Current = math.Round(prev + (r.speed-prev)*u)
			v.Speed.Tokens = int64(r.speed * (s - from) * demoSpeedup)
			break
		}
		v.Speed.History = append(v.Speed.History, r.speed)
		prev, from = r.speed, from+r.dur
	}
	// The mean of the last 10, as the speed meter's.
	if h := v.Speed.History; len(h) > 0 {
		h = h[max(0, len(h)-10):]
		var sum float64
		for _, x := range h {
			sum += x
		}
		v.Speed.Average = math.Round(sum / float64(len(h)))
	}
	return v
}

// LUMILO_DEMO_DIR=… saves the demo's frames in both themes at 2x with the
// menu-bar icon of each and frames.json: the panel as Lumilo starts Splash
// with Qwen3.8-27B and serves requests back to back, until the last one
// ends and a second after, at demoFPS.
func TestDemo(t *testing.T) {
	dir := os.Getenv("LUMILO_DEMO_DIR")
	if dir == "" {
		t.Skip("LUMILO_DEMO_DIR is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const step = time.Second / demoFPS
	length := 3.6 + 1.0
	for _, r := range demoReqs {
		length += r.dur
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{true: "dark", false: "light"}[dark]
		b := newBench(t, "en")
		b.tt.SetDark(dark)
		b.tt.SetScale(2)
		b.show(demoView(0), 0)
		// The icon eases its number as the app does.
		tps := trayicon.Speed{Ease: true}
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
			tps.Target(v, b.clock)
			tps.Step(b.clock)
			if err := os.WriteFile(filepath.Join(dir, f.Tray), trayicon.PNG(trayicon.For(v, "en", dark, tps.Shown())), 0o644); err != nil {
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
