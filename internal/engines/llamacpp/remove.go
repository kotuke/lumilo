package llamacpp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

var _ engine.Remover = (*LlamaCpp)(nil)

// PlanRemove counts what Remove(id) deletes.
func (l *LlamaCpp) PlanRemove(ctx context.Context, id string) (engine.RemovePlan, error) {
	r, err := l.removal(ctx, id)
	if err != nil {
		return engine.RemovePlan{}, err
	}
	return r.Plan()
}

// Remove deletes the GGUF file of id, every part of a split one, and the
// vision projector and DFlash draft beside it when no other model of the
// directory is left to load them. In a hub cache it deletes the file's
// links in every snapshot (Models lists the newest) and the blobs no other
// link points to (enginekit.Removal).
func (l *LlamaCpp) Remove(ctx context.Context, id string) error {
	r, err := l.removal(ctx, id)
	if err != nil {
		return err
	}
	return r.Apply()
}

// removal collects the files of id. Only a model that Models lists is
// deleted: an ID is a path, and nothing else of the disk is touched.
func (l *LlamaCpp) removal(ctx context.Context, id string) (*enginekit.Removal, error) {
	models, err := l.Models(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(models, func(m engine.Model) bool { return m.ID == id }) {
		return nil, fmt.Errorf("llama.cpp: %s is not an installed model", id)
	}
	files := []string{id}
	if m := splitRe.FindStringSubmatch(filepath.Base(id)); m != nil {
		files = nil
		var n int
		fmt.Sscanf(m[3], "%d", &n)
		for k := 1; k <= n; k++ {
			files = append(files, filepath.Join(filepath.Dir(id), fmt.Sprintf("%s-%05d-of-%s.gguf", m[1], k, m[3])))
		}
	}
	dir := filepath.Dir(id)
	alone := !slices.ContainsFunc(models, func(m engine.Model) bool { return m.ID != id && filepath.Dir(m.ID) == dir })
	if alone {
		for _, kind := range []string{"mmproj", "dflash"} {
			if p := sidecarFor(id, kind); p != "" {
				files = append(files, p)
			}
		}
	}

	r := &enginekit.Removal{}
	hub := l.inHub(id)
	for _, f := range files {
		if hub {
			// An older snapshot's copy would be listed in its place.
			for _, c := range enginekit.HubCopies(f) {
				if err := r.AddHubFile(c); err != nil {
					return nil, err
				}
			}
		} else {
			r.AddFile(f)
		}
	}
	return r, nil
}

// inHub reports whether path is in a snapshot of one of the hub caches.
func (l *LlamaCpp) inHub(path string) bool {
	for _, d := range l.hubDirs {
		if strings.HasPrefix(path, filepath.Clean(d)+string(filepath.Separator)+"models--") {
			return true
		}
	}
	return false
}
