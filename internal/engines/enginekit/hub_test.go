package enginekit

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestHubCaches(t *testing.T) {
	t.Setenv("HF_HUB_CACHE", "/a")
	t.Setenv("HF_HOME", "/h")
	t.Setenv("XDG_CACHE_HOME", "/x")
	got := HubCaches("/home", "/extra", "/a")
	want := []string{"/a", "/h/hub", "/extra", "/x/huggingface/hub", "/home/.cache/huggingface/hub"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
	t.Setenv("HF_HUB_CACHE", "")
	t.Setenv("HF_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if got := HubCaches("/home"); !slices.Equal(got, []string{"/home/.cache/huggingface/hub"}) {
		t.Errorf("defaults: %v", got)
	}
	if RepoFolder("ggml-org/Qwen3-0.6B-GGUF") != "models--ggml-org--Qwen3-0.6B-GGUF" {
		t.Error("RepoFolder")
	}
}

// listing is one moment of a recorded download: file path -> size.
type listing map[string]int64

// readListings reads testdata/hub-*.txt: "--- t=N" starts a listing,
// "SIZE PATH" lines follow.
func readListings(t *testing.T, name string) []listing {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []listing
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "--- "):
			out = append(out, listing{})
		default:
			size, path, ok := strings.Cut(line, " ")
			n, err := strconv.ParseInt(size, 10, 64)
			if !ok || err != nil || len(out) == 0 {
				t.Fatalf("%s: bad line %q", name, line)
			}
			out[len(out)-1][path] = n
		}
	}
	return out
}

// apply makes dir hold the files of l at their sizes (sparse, so no disk
// is used) and removes the files l lacks.
func apply(t *testing.T, dir string, l listing, had listing) {
	t.Helper()
	for p := range had {
		if _, ok := l[p]; !ok {
			_ = os.Remove(filepath.Join(dir, p))
		}
	}
	for p, n := range l {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(n); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// The meter follows a recorded download: it grows with the file being
// fetched, whatever its suffix, ends at the file's size, and leaves out
// the files that were there before (here another variant of the repository).
func TestHubMeterRecorded(t *testing.T) {
	const repo = "ggml-org/Qwen3-0.6B-GGUF"
	const size = 804_753_632 // Qwen3-0.6B-Q8_0.gguf
	for _, name := range []string{"hub-llamacpp.txt", "hub-hfxet.txt"} {
		for _, repos := range [][]string{{repo}, nil} {
			t.Run(name+"/"+strings.Join(repos, ","), func(t *testing.T) {
				listings := readListings(t, name)
				if len(listings) < 10 {
					t.Fatalf("%d listings", len(listings))
				}
				hub := t.TempDir()
				other := listing{"models--" + "ggml-org--Qwen3-0.6B-GGUF/blobs/other-variant": 3_000_000_000}
				apply(t, hub, other, nil)
				meter := HubMeter([]string{hub, filepath.Join(hub, "absent")}, repos...)
				prev, had := int64(0), other
				grew := 0
				for _, l := range listings {
					for p, n := range other {
						l[p] = n
					}
					apply(t, hub, l, had)
					had = l
					got := meter()
					if got < prev {
						t.Fatalf("meter went back from %d to %d", prev, got)
					}
					if got > prev {
						grew++
					}
					prev = got
				}
				// The final listing holds the blob and small metadata files
				// (refs/main, a tree listing).
				if prev < size || prev > size+10_000 {
					t.Errorf("final %d, want about %d", prev, size)
				}
				if grew < 5 {
					t.Errorf("the meter grew only %d times", grew)
				}
			})
		}
	}
}
