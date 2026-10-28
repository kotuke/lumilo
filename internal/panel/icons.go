package panel

import (
	"bytes"
	"embed"
	"strconv"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Tabler outline icons and the Homebrew logo from the mockup. The files
// draw with stroke-width 2; the mockup uses other widths in places, so
// icon makes those variants from the same files.
//
//go:embed icons/*.svg
var iconFiles embed.FS

type iconKey struct {
	name   string
	stroke float32
}

// icons are parsed once for every panel: the files are part of the
// program, so the cache only grows to their number times a few strokes.
// The lock lets the panels of parallel tests share it.
var icons struct {
	sync.Mutex
	m map[iconKey]*ui.SVG
}

// icon returns the icon named like its file, drawn with the given stroke
// width in the 24×24 viewBox (0 keeps the file's width).
func icon(name string, stroke float32) *ui.SVG {
	k := iconKey{name, stroke}
	icons.Lock()
	defer icons.Unlock()
	if s, ok := icons.m[k]; ok {
		return s
	}
	data, err := iconFiles.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	s := ui.MustParseSVG(withStroke(data, stroke))
	if icons.m == nil {
		icons.m = map[iconKey]*ui.SVG{}
	}
	icons.m[k] = s
	return s
}

// withStroke rewrites the first stroke-width="2" of an SVG; 0 keeps it.
func withStroke(data []byte, stroke float32) []byte {
	if stroke == 0 {
		return data
	}
	w := strconv.FormatFloat(float64(stroke), 'f', -1, 32)
	return bytes.Replace(data, []byte(`stroke-width="2"`), []byte(`stroke-width="`+w+`"`), 1)
}
