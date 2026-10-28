package enginekit

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
)

// Removal is the files that deleting a model takes off the disk, for an
// engine.Remover: collect them with the Add methods, then read Plan or run
// Apply. Nothing is deleted before Apply.
//
// Files of a hub cache are links in a snapshot that point to blobs
// (HubDownload): a link goes, and its blob goes with it only when no other
// link of the repository points to it, as another variant or snapshot may
// share it. The repository's folder goes once nothing of it is left; a
// partly fetched blob of another download keeps it.
type Removal struct {
	files []string            // removed with os.Remove, in this order
	dirs  []string            // removed after the files, when empty
	hub   map[string][]string // repository folder → its snapshot links to remove
	seen  map[string]bool
	bytes int64
	done  bool // hub resolved into files and dirs
}

// AddFile adds a file or a link that is not in a hub cache, such as a
// .gguf of a flat cache. A missing one is skipped.
func (r *Removal) AddFile(path string) {
	st, err := os.Lstat(path)
	if err != nil {
		return
	}
	r.file(path, st)
}

// AddDirIfEmpty adds a directory to remove after the files, when nothing
// else is left in it.
func (r *Removal) AddDirIfEmpty(dir string) { r.dirs = append(r.dirs, dir) }

// AddTree adds a directory and everything in it. Links in it are removed,
// not followed.
func (r *Removal) AddTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			r.dirs = append(r.dirs, p)
			return nil
		}
		if st, err := d.Info(); err == nil {
			r.file(p, st)
		}
		return nil
	})
}

// AddHubFile adds a file of a hub cache by its path in a snapshot:
// <cache>/models--OWNER--REPO/snapshots/<commit>/<file>.
func (r *Removal) AddHubFile(path string) error {
	repo, ok := hubRepoOf(path)
	if !ok {
		return fmt.Errorf("hub: %s is not in a snapshot of a hub cache", path)
	}
	if r.hub == nil {
		r.hub = map[string][]string{}
	}
	r.hub[repo] = append(r.hub[repo], filepath.Clean(path))
	r.done = false
	return nil
}

// Plan is what Apply deletes.
func (r *Removal) Plan() (engine.RemovePlan, error) {
	if err := r.resolve(); err != nil {
		return engine.RemovePlan{}, err
	}
	return engine.RemovePlan{Bytes: r.bytes, Shared: len(r.hub) > 0}, nil
}

// Apply deletes the files, then the directories left empty, deepest
// first. A file already gone is no error; it goes on past the others and
// returns the first.
func (r *Removal) Apply() error {
	if err := r.resolve(); err != nil {
		return err
	}
	var first error
	for _, f := range r.files {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = err
		}
	}
	dirs := slices.Clone(r.dirs)
	slices.SortStableFunc(dirs, func(a, b string) int {
		return strings.Count(b, string(filepath.Separator)) - strings.Count(a, string(filepath.Separator))
	})
	for _, d := range dirs {
		_ = os.Remove(d) // fails while something else is in it
	}
	return first
}

func (r *Removal) file(path string, st fs.FileInfo) {
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	if r.seen[path] {
		return
	}
	r.seen[path] = true
	r.files = append(r.files, path)
	if st.Mode().IsRegular() {
		r.bytes += st.Size()
	}
}

// resolve turns the hub links into files: the links, the blobs no link
// that stays points to, and the repository's metadata once nothing else
// of it is left.
func (r *Removal) resolve() error {
	if r.done {
		return nil
	}
	for _, repo := range slices.Sorted(maps.Keys(r.hub)) {
		gone := map[string]bool{}
		for _, l := range r.hub[repo] {
			gone[l] = true
		}
		snaps := filepath.Join(repo, "snapshots")
		kept := map[string]bool{} // blobs a staying link points to
		stays := false
		err := filepath.WalkDir(snaps, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !gone[p] {
				stays = true
				if b, ok := blobOf(p); ok {
					kept[b] = true
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, l := range r.hub[repo] {
			st, err := os.Lstat(l)
			if err != nil {
				continue
			}
			r.file(l, st)
			for d := filepath.Dir(l); strings.HasPrefix(d, snaps+string(filepath.Separator)); d = filepath.Dir(d) {
				r.dirs = append(r.dirs, d)
			}
			if b, ok := blobOf(l); ok && !kept[b] && filepath.Dir(b) == filepath.Join(repo, "blobs") {
				r.AddFile(b)
			}
		}
		r.dirs = append(r.dirs, snaps)
		if stays {
			continue
		}
		// Nothing of the repository stays: its refs and the hub's markers
		// of missing files go too, and the folder once blobs/ is empty.
		for _, sub := range []string{"refs", ".no_exist"} {
			if err := r.AddTree(filepath.Join(repo, sub)); err != nil {
				return err
			}
		}
		r.dirs = append(r.dirs, filepath.Join(repo, "blobs"), repo)
	}
	r.done = true
	return nil
}

// hubRepoOf is the repository folder of a path in one of its snapshots:
// <repo>/snapshots/<commit>/<file>, the file maybe in a folder.
func hubRepoOf(path string) (string, bool) {
	path = filepath.Clean(path)
	for d := filepath.Dir(path); d != filepath.Dir(d); d = filepath.Dir(d) {
		if filepath.Base(d) != "snapshots" || !strings.HasPrefix(filepath.Base(filepath.Dir(d)), "models--") {
			continue
		}
		rel, err := filepath.Rel(d, path)
		ok := err == nil && len(strings.Split(rel, string(filepath.Separator))) >= 2
		return filepath.Dir(d), ok
	}
	return "", false
}

// HubCopies are the paths of the file at path, in a snapshot of a hub
// cache, in every snapshot of its repository that holds it, path among
// them: a listing that shows a file once, from the newest snapshot, shows
// an older snapshot's copy once the newer one is gone.
func HubCopies(path string) []string {
	repo, ok := hubRepoOf(path)
	if !ok {
		return []string{path}
	}
	snaps := filepath.Join(repo, "snapshots")
	rel, _ := filepath.Rel(snaps, filepath.Clean(path))
	_, file, _ := strings.Cut(rel, string(filepath.Separator)) // past <commit>/
	out := []string{filepath.Clean(path)}
	commits, _ := os.ReadDir(snaps)
	for _, c := range commits {
		p := filepath.Join(snaps, c.Name(), file)
		if _, err := os.Lstat(p); err == nil && p != out[0] {
			out = append(out, p)
		}
	}
	return out
}

// blobOf is the blob a snapshot link points to.
func blobOf(link string) (string, bool) {
	t, err := os.Readlink(link)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(filepath.Dir(link), t)
	}
	return filepath.Clean(t), true
}
