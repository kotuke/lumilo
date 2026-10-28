package enginekit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// hubServer serves files by path, honours "Range: bytes=N-" and records
// the ranges asked for.
type hubServer struct {
	*httptest.Server
	mu     sync.Mutex
	files  map[string][]byte
	ranges []string
	block  chan struct{} // when set, a body stops after half and waits
}

func newHubServer(t *testing.T, files map[string][]byte) *hubServer {
	h := &hubServer{files: files}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := h.files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		h.ranges = append(h.ranges, r.Header.Get("Range"))
		h.mu.Unlock()
		from := 0
		if rg := r.Header.Get("Range"); rg != "" {
			fmt.Sscanf(rg, "bytes=%d-", &from)
			if from >= len(data) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(data)-1, len(data)))
			w.WriteHeader(http.StatusPartialContent)
		}
		body := data[from:]
		if h.block != nil {
			_, _ = w.Write(body[:len(body)/2])
			w.(http.Flusher).Flush()
			<-h.block
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(h.Close)
	return h
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestHubDownloadLayout(t *testing.T) {
	model := bytes.Repeat([]byte("model"), 1000)
	proj := bytes.Repeat([]byte("proj"), 300)
	srv := newHubServer(t, map[string][]byte{"m.gguf": model, "sub/mmproj-F16.gguf": proj})
	cache := t.TempDir()
	files := []HubFile{{Path: "m.gguf", Size: int64(len(model)), SHA256: sum(model)},
		{Path: "sub/mmproj-F16.gguf", Size: int64(len(proj)), SHA256: sum(proj)}}
	var last [2]int64
	paths, err := HubDownload(context.Background(), nil, func(f string) string { return srv.URL + "/" + f },
		cache, "acme/Tiny-GGUF", "c0ffee", files, func(done, total int64) { last = [2]int64{done, total} })
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cache, "models--acme--Tiny-GGUF")
	if b, _ := os.ReadFile(filepath.Join(root, "refs", "main")); string(b) != "c0ffee" {
		t.Errorf("refs/main = %q", b)
	}
	link := filepath.Join(root, "snapshots", "c0ffee", "m.gguf")
	if paths["m.gguf"] != link {
		t.Errorf("paths = %v", paths)
	}
	if target, _ := os.Readlink(link); target != "../../blobs/"+sum(model) {
		t.Errorf("link = %q", target)
	}
	if target, _ := os.Readlink(paths["sub/mmproj-F16.gguf"]); target != "../../../blobs/"+sum(proj) {
		t.Errorf("nested link = %q", target)
	}
	if b, err := os.ReadFile(link); err != nil || !bytes.Equal(b, model) {
		t.Errorf("content through the link: %v", err)
	}
	if want := int64(len(model) + len(proj)); last != [2]int64{want, want} {
		t.Errorf("last progress = %v, want %d of %d", last, want, want)
	}
	// A second run finds the blobs and fetches nothing.
	srv.ranges = nil
	if _, err := HubDownload(context.Background(), nil, func(f string) string { return srv.URL + "/" + f },
		cache, "acme/Tiny-GGUF", "c0ffee", files, nil); err != nil || len(srv.ranges) != 0 {
		t.Errorf("second run: err %v, requests %q", err, srv.ranges)
	}
}

func TestHubDownloadResume(t *testing.T) {
	model := bytes.Repeat([]byte("0123456789"), 500)
	srv := newHubServer(t, map[string][]byte{"m.gguf": model})
	cache := t.TempDir()
	blobs := filepath.Join(cache, "models--a--b", "blobs")
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobs, sum(model)+partSuffix), model[:1234], 0o644); err != nil {
		t.Fatal(err)
	}
	files := []HubFile{{Path: "m.gguf", Size: int64(len(model)), SHA256: sum(model)}}
	if _, err := HubDownload(context.Background(), nil, func(f string) string { return srv.URL + "/" + f },
		cache, "a/b", "r1", files, nil); err != nil {
		t.Fatal(err)
	}
	if len(srv.ranges) != 1 || srv.ranges[0] != "bytes=1234-" {
		t.Errorf("requests = %q, want one for bytes=1234-", srv.ranges)
	}
	if b, _ := os.ReadFile(filepath.Join(blobs, sum(model))); !bytes.Equal(b, model) {
		t.Error("resumed blob differs")
	}
	if _, err := os.Stat(filepath.Join(blobs, sum(model)+partSuffix)); !os.IsNotExist(err) {
		t.Error("part left behind")
	}
}

func TestHubDownloadChecksum(t *testing.T) {
	model := []byte("the real model")
	srv := newHubServer(t, map[string][]byte{"m.gguf": []byte("a fake model!!")})
	cache := t.TempDir()
	files := []HubFile{{Path: "m.gguf", Size: int64(len(model)), SHA256: sum(model)}}
	_, err := HubDownload(context.Background(), nil, func(f string) string { return srv.URL + "/" + f },
		cache, "a/b", "r1", files, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	left, _ := filepath.Glob(filepath.Join(cache, "models--a--b", "blobs", "*"))
	if len(left) != 0 {
		t.Errorf("left behind: %q", left)
	}
}

func TestHubDownloadCancel(t *testing.T) {
	model := bytes.Repeat([]byte("x"), 1<<16)
	srv := newHubServer(t, map[string][]byte{"m.gguf": model})
	srv.block = make(chan struct{})
	defer close(srv.block)
	cache := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	files := []HubFile{{Path: "m.gguf", Size: int64(len(model)), SHA256: sum(model)}}
	done := make(chan error, 1)
	go func() {
		_, err := HubDownload(ctx, nil, func(f string) string { return srv.URL + "/" + f },
			cache, "a/b", "r1", files, func(d, _ int64) {
				if d > 0 {
					cancel()
				}
			})
		done <- err
	}()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	part := filepath.Join(cache, "models--a--b", "blobs", sum(model)+partSuffix)
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("the part is gone: %v", err)
	}
	DropHubParts([]string{cache}, "a/b")
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Error("DropHubParts left the part")
	}
}
