package splash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

var _ engine.Remover = (*Splash)(nil)

// Splash 1.4 has no command that deletes a model: install/models.py knows
// prepare, verify and link only. Remove does what its own clean-up does
// after an install (install/assembly.py collect_garbage, checked on
// 2026-10-28 with Splash 1.4.1), for the one model:
//   - the model's selection links go: the plain one and the text-only one
//     under .selections, or Models would list the other;
//   - an assembly in .resolved that no selection link names any more goes,
//     unless a server holds it (a shared flock on its model.json, assembly.py
//     hold); then nothing is deleted, as a foreign "splash serve" runs it;
//   - an entry of .metadata no assembly that stays links goes;
//   - the files of the hub cache the assembly linked go, in every snapshot
//     of their repository, unless an assembly that stays links them too
//     (the DFlash2 draft of a family is shared). Splash itself never
//     deletes them, so they would fill the disk; a copy in a newer snapshot
//     (llama.cpp downloads the newest) would keep their blob.
//
// It holds models/.install.lock, under which Splash writes all of these
// (install/models.py installation_lock), and runtime/serve.lock shared, so
// an upgrade of Splash waits (shareInstall).

// PlanRemove counts what Remove(id) deletes.
func (s *Splash) PlanRemove(ctx context.Context, id string) (engine.RemovePlan, error) {
	r, _, err := s.removal(ctx, id)
	if err != nil {
		return engine.RemovePlan{}, err
	}
	return r.Plan()
}

// Remove deletes the model id and what only it uses.
func (s *Splash) Remove(ctx context.Context, id string) error {
	unlock, err := s.shareInstall()
	if err != nil {
		return err
	}
	defer unlock()
	unlockModels, err := lockModels(s.modelsDir)
	if err != nil {
		return err
	}
	defer unlockModels()
	r, assemblies, err := s.removal(ctx, id)
	if err != nil {
		return err
	}
	// Counted while our server still ran it, the deletion finds it stopped.
	for _, dir := range assemblies {
		if held(dir) {
			return errors.New("splash: a Splash server runs " + id + "; stop it first")
		}
	}
	return r.Apply()
}

// removal collects what deleting id takes, and the assemblies that go.
// Only a model that Models lists is deleted.
func (s *Splash) removal(ctx context.Context, id string) (*enginekit.Removal, []string, error) {
	root := s.modelsDir
	models, err := listModels(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	if !slices.ContainsFunc(models, func(m engine.Model) bool { return m.ID == id }) {
		return nil, nil, fmt.Errorf("splash: %s is not an installed model", id)
	}
	gone := map[string]bool{}
	for _, l := range []string{plainLink(root, id), textOnlyLink(root, id)} {
		if st, err := os.Lstat(l); err == nil && st.Mode()&os.ModeSymlink != 0 {
			gone[l] = true
		}
	}
	// The assemblies other links still name.
	named := map[string]bool{}
	for _, l := range selectionLinks(root) {
		if !gone[l] {
			if dir, err := filepath.EvalSymlinks(l); err == nil {
				named[dir] = true
			}
		}
	}
	resolved, _ := filepath.EvalSymlinks(filepath.Join(root, ".resolved"))
	r := &enginekit.Removal{}
	drop := map[string]bool{} // assemblies to delete
	for l := range gone {
		r.AddFile(l)
		if filepath.Dir(l) != filepath.Join(root, ".selections") {
			r.AddDirIfEmpty(filepath.Dir(l)) // models/OWNER
		}
		dir, err := filepath.EvalSymlinks(l)
		if err != nil || named[dir] || filepath.Dir(dir) != resolved {
			continue
		}
		drop[dir] = true
	}

	// What the assemblies that stay use. An assembly no link names but a
	// server holds stays too, as Splash's clean-up keeps it.
	keepPaths, keepMeta := map[string]bool{}, map[string]bool{}
	meta := filepath.Join(root, ".metadata")
	entries, _ := os.ReadDir(resolved)
	for _, e := range entries {
		dir := filepath.Join(resolved, e.Name())
		if drop[dir] {
			continue
		}
		for _, p := range assemblyPaths(dir) {
			keepPaths[p] = true
			if m, ok := metaEntry(meta, p); ok {
				keepMeta[m] = true
			}
		}
	}
	assemblies := slices.Sorted(maps.Keys(drop))
	for _, dir := range assemblies {
		paths := assemblyPaths(dir)
		if err := r.AddTree(dir); err != nil {
			return nil, nil, err
		}
		for _, p := range paths {
			if m, ok := metaEntry(meta, p); ok {
				if !keepMeta[m] {
					if err := r.AddTree(filepath.Join(meta, m)); err != nil {
						return nil, nil, err
					}
				}
				continue
			}
			if keepPaths[p] || !s.inHub(p) {
				continue // shared, or the user's own files
			}
			// The same file in the repository's other snapshots links the
			// same blob: kept, it would keep the blob too.
			for _, c := range enginekit.HubCopies(p) {
				if keepPaths[c] {
					continue
				}
				if err := r.AddHubFile(c); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	return r, assemblies, nil
}

// selectionLinks are the links Splash's clean-up counts: every link one
// level under the models directory (install/models.py selection_links),
// .selections among them.
func selectionLinks(root string) []string {
	var out []string
	dirs, _ := os.ReadDir(root)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(root, d.Name()))
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				out = append(out, filepath.Join(root, d.Name(), e.Name()))
			}
		}
	}
	return out
}

// assemblyPaths are the source paths model.json of an assembly lists.
func assemblyPaths(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "model.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Files map[string]struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	var out []string
	for _, f := range m.Files {
		if f.Path != "" {
			out = append(out, filepath.Clean(f.Path))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// metaEntry is the entry of .metadata a path is in.
func metaEntry(meta, path string) (string, bool) {
	rel, ok := strings.CutPrefix(path, meta+string(filepath.Separator))
	if !ok {
		return "", false
	}
	entry, _, _ := strings.Cut(rel, string(filepath.Separator))
	return entry, entry != "" && entry != ".."
}

// inHub reports whether path is in a repository of one of the hub caches.
func (s *Splash) inHub(path string) bool {
	for _, d := range s.hubDirs {
		if strings.HasPrefix(path, filepath.Clean(d)+string(filepath.Separator)+"models--") {
			return true
		}
	}
	return false
}

// held reports whether a server holds the assembly at dir: it keeps its
// model.json locked shared while it serves (install/assembly.py is_held).
func held(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "model.json"))
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// lockModels takes models/.install.lock without waiting.
func lockModels(root string) (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(root, ".install.lock"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("splash is installing a model; wait for it to finish")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
