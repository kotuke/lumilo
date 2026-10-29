package enginekit

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// partSuffix marks a blob Lumilo is fetching. It is its own: llama-server
// 0.7.0 writes "<sha256>.downloadInProgress" and huggingface_hub
// "<sha256>.<id>.incomplete" (testdata/hub-*.txt), and a download of ours
// must never take over, or be taken over by, one of theirs.
const partSuffix = ".lumilo-part"

// progressEvery is how often HubDownload reports progress at most.
const progressEvery = 200 * time.Millisecond

// HubFile is a file of a repository to fetch into the hub cache.
type HubFile struct {
	Path   string // from the repository root, e.g. "Qwen3-0.6B-Q4_0.gguf"
	Size   int64
	SHA256 string // the Git LFS checksum, which names the blob
	// BlobID is the Git blob id (SHA-1) of a plain Git file, one without
	// SHA256 (an MLX checkpoint's config.json, its tokenizer): the hub
	// cache names its blob by it, as huggingface_hub does.
	BlobID string
}

// blob is the name of f's blob in the hub cache: the LFS checksum, else
// the Git blob id.
func (f HubFile) blob() string {
	if f.SHA256 != "" {
		return strings.ToLower(f.SHA256)
	}
	return strings.ToLower(f.BlobID)
}

// HubClient is an HTTP client for large downloads: no deadline for the
// whole body, only for connecting and for the answer's headers.
func HubClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
	}}
}

// HubDownload fetches files of repo at commit into the hub cache cacheDir
// in huggingface_hub's layout, the one llama-server 0.7.0 reads with -hf
// (also offline: checked live on 2026-10-27):
//
//	models--OWNER--REPO/blobs/<sha256>                the file
//	models--OWNER--REPO/snapshots/<commit>/<path>     a relative symlink to it
//	models--OWNER--REPO/refs/main                     the commit, no newline
//
// url gives the download address of a file. A blob already there with the
// right size is not fetched again. A blob is fetched into
// "<sha256>.lumilo-part", resumed with a Range request from what that part
// holds, checked against its SHA-256 (read back once complete) and renamed
// into place. A plain Git file goes the same way under its Git blob id
// (HubFile.BlobID), checked as Git hashes a blob. progress gets
// the bytes there so far (cached blobs included) and the total, at most
// every 200 ms and once at the end; it may be nil.
//
// A cancelled ctx returns ctx's error and keeps the part, for a later
// resume or DropHubParts. paths maps each file's Path to its snapshot path.
func HubDownload(ctx context.Context, hc *http.Client, url func(file string) string, cacheDir, repo, commit string,
	files []HubFile, progress func(done, total int64)) (map[string]string, error) {
	if hc == nil {
		hc = HubClient()
	}
	if commit == "" || strings.ContainsAny(commit, "/\\") {
		return nil, fmt.Errorf("hub: bad commit %q", commit)
	}
	root := filepath.Join(cacheDir, RepoFolder(repo))
	blobs := filepath.Join(root, "blobs")
	snap := filepath.Join(root, "snapshots", commit)
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		return nil, err
	}

	var total int64
	for _, f := range files {
		if !validSHA(f.SHA256) && (f.SHA256 != "" || !validBlobID(f.BlobID)) {
			return nil, fmt.Errorf("hub: %s has no SHA-256 or Git blob id", f.Path)
		}
		if !safePath(f.Path) {
			return nil, fmt.Errorf("hub: unsafe path %q", f.Path)
		}
		total += f.Size
	}
	rep := &reporter{fn: progress, total: total}

	paths := map[string]string{}
	for _, f := range files {
		blob := filepath.Join(blobs, f.blob())
		if st, err := os.Stat(blob); err == nil && st.Mode().IsRegular() && st.Size() == f.Size {
			rep.base += f.Size
			rep.report(0, false)
		} else if err := fetchBlob(ctx, hc, url(f.Path), blob, f, rep); err != nil {
			return nil, err
		}
		link := filepath.Join(snap, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return nil, err
		}
		// ../../blobs/<sha>, one more ../ per folder of the path.
		target := strings.Repeat("../", 2+strings.Count(f.Path, "/")) + "blobs/" + f.blob()
		if cur, err := os.Readlink(link); err != nil || cur != target {
			_ = os.Remove(link)
			if err := os.Symlink(target, link); err != nil {
				return nil, err
			}
		}
		paths[f.Path] = link
	}
	if err := writeAtomic(filepath.Join(root, "refs", "main"), []byte(commit)); err != nil {
		return nil, err
	}
	rep.report(0, true)
	return paths, nil
}

// DropHubParts removes the parts HubDownload left of repo's blobs in the
// caches dirs.
func DropHubParts(dirs []string, repo string) {
	for _, d := range dirs {
		parts, _ := filepath.Glob(filepath.Join(d, RepoFolder(repo), "blobs", "*"+partSuffix))
		for _, p := range parts {
			_ = os.Remove(p)
		}
	}
}

// reporter throttles progress: base is what earlier files brought.
type reporter struct {
	fn    func(done, total int64)
	total int64
	base  int64
	last  time.Time
}

func (r *reporter) report(cur int64, force bool) {
	if r.fn == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(r.last) < progressEvery {
		return
	}
	r.last = now
	r.fn(r.base+cur, r.total)
}

// fetchBlob downloads f into blob through blob+partSuffix.
func fetchBlob(ctx context.Context, hc *http.Client, u, blob string, f HubFile, rep *reporter) error {
	part := blob + partSuffix
	// Two tries: a part the server will not resume (or one past the end)
	// starts over once.
	for try := 0; try < 2; try++ {
		restart, err := fetchOnce(ctx, hc, u, part, f, rep)
		if err != nil {
			return err
		}
		if !restart {
			break
		}
		_ = os.Remove(part)
	}
	st, err := os.Stat(part)
	if err != nil {
		return err
	}
	if st.Size() != f.Size {
		return fmt.Errorf("hub: %s: got %d of %d bytes", f.Path, st.Size(), f.Size)
	}
	kind, want, sum := "sha256", f.SHA256, ""
	if want == "" {
		kind, want = "git blob", f.BlobID
		sum, err = fileBlobID(part, f.Size)
	} else {
		sum, err = fileSHA(part)
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, want) {
		_ = os.Remove(part)
		return fmt.Errorf("hub: %s: checksum mismatch: got %s %s, want %s", f.Path, kind, sum, want)
	}
	if err := os.Rename(part, blob); err != nil {
		return err
	}
	rep.base += f.Size
	rep.report(0, false)
	return nil
}

// fetchOnce appends to part what the server sends from part's size on.
// restart asks the caller to drop the part and try once more.
func fetchOnce(ctx context.Context, hc *http.Client, u, part string, f HubFile, rep *reporter) (restart bool, err error) {
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	if have > f.Size {
		return true, nil
	}
	if have == f.Size && f.Size > 0 {
		return false, nil // complete; the checksum decides
	}
	if have > 0 {
		rep.report(have, true) // a resumed part counts before the network answers
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("hub: %s: %w", f.Path, err)
	}
	defer resp.Body.Close()
	flags := os.O_WRONLY | os.O_CREATE
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC // the server sends it all: start over
		have = 0
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return true, nil
	default:
		return false, fmt.Errorf("hub: %s: %s", f.Path, resp.Status)
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return false, err
	}
	w := &countWriter{w: out, n: have, rep: rep}
	_, cerr := io.Copy(w, resp.Body)
	serr := out.Sync()
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	if cerr == nil {
		cerr = serr
	}
	if cerr != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("hub: %s: %w", f.Path, cerr)
	}
	return false, nil
}

type countWriter struct {
	w   io.Writer
	n   int64
	rep *reporter
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.rep.report(c.n, false)
	return n, err
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileBlobID is the Git blob id of the file at p: the SHA-1 of
// "blob <size>\x00" and the content.
func fileBlobID(p string, size int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	io.WriteString(h, "blob "+strconv.FormatInt(size, 10)+"\x00")
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func validBlobID(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// safePath: a relative slash path without "." or ".." parts.
func safePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || path.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}

// writeAtomic writes data to p through a temporary file and a rename.
func writeAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".lumilo-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := tmp.Close(); werr == nil {
		werr = err
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
