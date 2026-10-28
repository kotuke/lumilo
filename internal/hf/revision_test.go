package hf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestRevision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/ggml-org/Qwen3-0.6B-GGUF/revision/main" || r.URL.Query().Get("blobs") != "true" {
			http.NotFound(w, r)
			return
		}
		// Cut from the live answer of 2026-10-27.
		_, _ = w.Write([]byte(`{"id":"ggml-org/Qwen3-0.6B-GGUF","sha":"13ac06624948e4d8589cb4b6f90100837342fcb8","siblings":[
{"rfilename":".gitattributes","blobId":"2f5f4d1d5ba8d1c2b7d401c51d9066027a01515b","size":1803},
{"rfilename":"Qwen3-0.6B-Q8_0.gguf","blobId":"af3a37a6fce2a8b41f457b3df9e3d6d5f0f9593b","size":804753632,
 "lfs":{"sha256":"361cc68159042c36ebff7715dc5a2e4612153e88f3e9c9c234820849d6dc9e1d","size":804753632,"pointerSize":134}}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	rev, err := c.Revision(context.Background(), "ggml-org/Qwen3-0.6B-GGUF")
	if err != nil {
		t.Fatal(err)
	}
	if rev.Commit != "13ac06624948e4d8589cb4b6f90100837342fcb8" || len(rev.Files) != 2 {
		t.Fatalf("revision = %+v", rev)
	}
	if f := rev.Files[1]; f.Path != "Qwen3-0.6B-Q8_0.gguf" || f.Size != 804753632 ||
		f.SHA256 != "361cc68159042c36ebff7715dc5a2e4612153e88f3e9c9c234820849d6dc9e1d" {
		t.Errorf("gguf = %+v", f)
	}
	if rev.Files[0].SHA256 != "" {
		t.Errorf("plain git file has a sha256: %+v", rev.Files[0])
	}
	if _, err := c.Revision(context.Background(), "nobody/none"); err == nil {
		t.Error("404 gave no error")
	}
	if got := c.FileURL("a/b c", "abc123", "Q8_0/x y.gguf"); got != srv.URL+"/a/b%20c/resolve/abc123/Q8_0/x%20y.gguf" {
		t.Errorf("FileURL = %s", got)
	}
}

func TestVariantFiles(t *testing.T) {
	files := func(names ...string) []File {
		var out []File
		for _, n := range names {
			out = append(out, File{Path: n, Size: 1})
		}
		return out
	}
	qwen := files("README.md", "Qwen3-0.6B-BF16.gguf", "Qwen3-0.6B-Q4_0.gguf", "Qwen3-0.6B-Q8_0.gguf", "Qwen3-0.6B-f16.gguf")
	unsloth := files(
		"Qwen3.8-27B-UD-Q4_K_M.gguf",
		"Qwen3.8-27B-Q4_K_M.gguf",
		"mmproj-F16.gguf",
		"mmproj-BF16.gguf",
		"BF16/Qwen3.8-27B-BF16-00002-of-00002.gguf",
		"BF16/Qwen3.8-27B-BF16-00001-of-00002.gguf",
		"Q8_0/Qwen3.8-27B-Q8_0-00001-of-00002.gguf", // a part missing
		"dflash-Qwen3.8-27B-Q4_K_M.gguf",
	)
	for _, tc := range []struct {
		files []File
		tag   string
		want  []string
	}{
		{qwen, "Q8_0", []string{"Qwen3-0.6B-Q8_0.gguf"}},
		{qwen, "q4_0", []string{"Qwen3-0.6B-Q4_0.gguf"}},
		{qwen, "F16", []string{"Qwen3-0.6B-f16.gguf"}}, // not BF16: "-F16", not "BF16"
		{qwen, "Q5_K_M", nil},
		{unsloth, "UD-Q4_K_M", []string{"Qwen3.8-27B-UD-Q4_K_M.gguf"}},
		// Both end in "Q4_K_M" after "-"; the first in the listing wins.
		{unsloth, "Q4_K_M", []string{"Qwen3.8-27B-UD-Q4_K_M.gguf"}},
		{unsloth, "BF16", []string{"BF16/Qwen3.8-27B-BF16-00001-of-00002.gguf", "BF16/Qwen3.8-27B-BF16-00002-of-00002.gguf"}},
		{unsloth, "Q8_0", nil},
		{unsloth, "", nil},
		{files("Tiny_Q4.gguf", "Tiny-x-Q4.gguf"), "Q4", []string{"Tiny-x-Q4.gguf"}}, // "-TAG" first
	} {
		if got := VariantFiles(tc.files, tc.tag); !slices.Equal(got, tc.want) {
			t.Errorf("VariantFiles(%q) = %q, want %q", tc.tag, got, tc.want)
		}
	}
}

func TestManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The hub names the files only to llama.cpp (checked live 2026-10-27).
		if r.URL.Path != "/v2/unsloth/Qwen3.8-27B-GGUF/manifests/UD-Q4_K_M" || r.Header.Get("User-Agent") != "llama-cpp" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"schemaVersion":2,"ggufFile":{"rfilename":"Qwen3.8-27B-UD-Q4_K_M.gguf"},"mmprojFile":{"rfilename":"mmproj-BF16.gguf"}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	m, p, err := c.Manifest(context.Background(), "unsloth/Qwen3.8-27B-GGUF", "UD-Q4_K_M")
	if err != nil || m != "Qwen3.8-27B-UD-Q4_K_M.gguf" || p != "mmproj-BF16.gguf" {
		t.Errorf("Manifest = %q, %q, %v", m, p, err)
	}
	var se *StatusError
	if _, _, err := c.Manifest(context.Background(), "unsloth/Qwen3.8-27B-GGUF", "Q9"); !errors.As(err, &se) || se.Code != 400 {
		t.Errorf("unknown tag: %v", err)
	}
}

func TestSplitParts(t *testing.T) {
	fs := []File{{Path: "BF16/M-BF16-00002-of-00002.gguf"}, {Path: "BF16/M-BF16-00001-of-00002.gguf"}, {Path: "Q8/M-00001-of-00003.gguf"}}
	if got := SplitParts(fs, "BF16/M-BF16-00001-of-00002.gguf"); !slices.Equal(got, []string{"BF16/M-BF16-00001-of-00002.gguf", "BF16/M-BF16-00002-of-00002.gguf"}) {
		t.Errorf("split = %q", got)
	}
	if got := SplitParts(fs, "Q8/M-00001-of-00003.gguf"); got != nil {
		t.Errorf("missing parts = %q", got)
	}
	if got := SplitParts(fs, "m.gguf"); !slices.Equal(got, []string{"m.gguf"}) {
		t.Errorf("lone = %q", got)
	}
}

func TestProjector(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"m.gguf", "mmproj-BF16.gguf", "mmproj-F16.gguf"}, "mmproj-F16.gguf"},
		{[]string{"mmproj-Q8_0.gguf", "mmproj-Qwen3-BF16.gguf"}, "mmproj-Qwen3-BF16.gguf"},
		{[]string{"mmproj-Q8_0.gguf", "mmproj-F32.gguf"}, "mmproj-F32.gguf"},
		{[]string{"mmproj-Q8_0.gguf"}, "mmproj-Q8_0.gguf"},
		{[]string{"sub/mmproj-F16.gguf", "m.gguf"}, ""},
		{nil, ""},
	} {
		var fs []File
		for _, n := range tc.names {
			fs = append(fs, File{Path: n})
		}
		if got := Projector(fs); got != tc.want {
			t.Errorf("Projector(%q) = %q, want %q", tc.names, got, tc.want)
		}
	}
}
