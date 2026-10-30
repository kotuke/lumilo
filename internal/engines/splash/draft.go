package splash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
	"github.com/kotuke/lumilo/internal/hf"
)

// lostDraft is the DFlash2 draft an installed assembly records whose files
// are gone from the hub cache (deleted by another program, or a download
// that never finished): its repository, the commit model.json records and
// the files under that commit's snapshot.
type lostDraft struct {
	repo, commit string
	cache        string   // the hub cache the assembly's links point into
	files        []string // paths from the repository root, e.g. "config.json"
}

// missingDraft reads model.json of the assembly at link: sources.draft
// names the repository and commit, and files maps each "draft/…" path to
// the snapshot file it links to (install/upstream.py assembly_record). ok
// is false when the assembly has no draft, or all its files are there.
func missingDraft(link string) (lostDraft, bool) {
	data, err := os.ReadFile(filepath.Join(link, "model.json"))
	if err != nil {
		return lostDraft{}, false
	}
	var m struct {
		Files map[string]struct {
			Path string `json:"path"`
		} `json:"files"`
		Sources struct {
			Draft struct {
				Repo     string `json:"repo"`
				Revision string `json:"revision"`
			} `json:"draft"`
		} `json:"sources"`
	}
	if json.Unmarshal(data, &m) != nil || m.Sources.Draft.Repo == "" || len(m.Sources.Draft.Revision) != 40 {
		return lostDraft{}, false // none, or a local draft directory (revision null)
	}
	d := lostDraft{repo: m.Sources.Draft.Repo, commit: m.Sources.Draft.Revision}
	// <cache>/models--OWNER--REPO/snapshots/<commit>/<file>
	snap := string(filepath.Separator) + filepath.Join(enginekit.RepoFolder(d.repo), "snapshots", d.commit) + string(filepath.Separator)
	lost := false
	for name, f := range m.Files {
		if !strings.HasPrefix(name, "draft/") {
			continue
		}
		cache, file, ok := strings.Cut(f.Path, snap)
		if !ok || file == "" {
			return lostDraft{}, false // not a hub cache snapshot: nothing to fetch
		}
		if d.cache == "" {
			d.cache = cache
		}
		d.files = append(d.files, filepath.ToSlash(file))
		if _, err := os.Stat(f.Path); err != nil {
			lost = true
		}
	}
	return d, lost && d.cache != ""
}

// fetchDraft downloads the files of d at its commit into the hub cache its
// assembly links into, so the next offline start verifies it as it was:
// the commit is the one the installation records, never main's. The
// cache's refs/main stays as it was (HubDownload points it at the commit
// it fetched), so Splash does not take that commit for main's.
func (s *Splash) fetchDraft(ctx context.Context, d lostDraft, progress func(done, total int64)) error {
	c := &hf.Client{BaseURL: s.hubURL}
	rev, err := c.RevisionAt(ctx, d.repo, d.commit)
	if err != nil {
		return hubFailure(ctx, err)
	}
	var files []enginekit.HubFile
	for _, name := range d.files {
		i := slices.IndexFunc(rev.Files, func(f hf.RevFile) bool { return f.Path == name })
		if i < 0 {
			return engine.Failure{Kind: engine.FailModel, Repo: d.repo,
				Line: fmt.Sprintf("%s@%s has no file %s", d.repo, d.commit[:12], name)}
		}
		f := rev.Files[i]
		files = append(files, enginekit.HubFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256, BlobID: f.BlobID})
	}
	refs := filepath.Join(d.cache, enginekit.RepoFolder(d.repo), "refs", "main")
	old, oldErr := os.ReadFile(refs)
	_, err = enginekit.HubDownload(ctx, nil, func(file string) string { return c.FileURL(d.repo, d.commit, file) },
		d.cache, d.repo, d.commit, files, progress)
	if oldErr == nil {
		_ = os.WriteFile(refs, old, 0o644)
	} else {
		_ = os.Remove(refs)
	}
	if ctx.Err() != nil {
		return ctx.Err() // the parts stay for a resume; DropPartial drops them
	}
	return err
}

// hubFailure is FailModel for a repository or commit the hub does not
// know or let us read; other errors (network) stay as they are.
func hubFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var se *hf.StatusError
	if errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusUnauthorized ||
		se.Code == http.StatusForbidden || se.Code == http.StatusBadRequest) {
		return engine.Failure{Kind: engine.FailModel, Line: err.Error()}
	}
	return err
}
