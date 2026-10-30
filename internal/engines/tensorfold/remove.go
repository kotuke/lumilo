package tensorfold

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

var _ engine.Remover = (*TensorFold)(nil)

// PlanRemove counts what Remove(id) deletes.
func (tf *TensorFold) PlanRemove(ctx context.Context, id string) (engine.RemovePlan, error) {
	r, err := tf.removal(ctx, id)
	if err != nil {
		return engine.RemovePlan{}, err
	}
	return r.Plan()
}

// Remove deletes the checkpoint id: every file of its repository's
// snapshots in every hub cache (an older snapshot would be listed in its
// place), and the blobs no other link points to (enginekit.Removal). Its
// DFlash drafters go too, unless another installed model lists them.
func (tf *TensorFold) Remove(ctx context.Context, id string) error {
	r, err := tf.removal(ctx, id)
	if err != nil {
		return err
	}
	return r.Apply()
}

// removal collects the files of id. Only a model that Models lists is
// deleted.
func (tf *TensorFold) removal(ctx context.Context, id string) (*enginekit.Removal, error) {
	models, err := tf.Models(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(models, func(m engine.Model) bool { return m.ID == id })
	if i < 0 {
		return nil, fmt.Errorf("tensorfold: %s is not an installed model", id)
	}
	repos := []string{id}
	for _, d := range models[i].Features.Drafts {
		shared := slices.ContainsFunc(models, func(m engine.Model) bool {
			return m.ID != id && slices.ContainsFunc(m.Features.Drafts, func(o engine.Draft) bool { return o.ID == d.ID })
		})
		if !shared {
			repos = append(repos, d.ID)
		}
	}
	r := &enginekit.Removal{}
	for _, repo := range repos {
		for _, cache := range tf.hubDirs {
			snaps := filepath.Join(cache, enginekit.RepoFolder(repo), "snapshots")
			err := filepath.WalkDir(snaps, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					if p == snaps {
						return fs.SkipDir // no such repository in this cache
					}
					return err
				}
				if d.IsDir() {
					return nil
				}
				return r.AddHubFile(p)
			})
			if err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}
