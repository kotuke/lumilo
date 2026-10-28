package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
	"github.com/kotuke/lumilo/internal/hf"
)

var _ engine.Downloader = (*LlamaCpp)(nil)

// Download fetches "owner/repo:TAG" into the hub cache llama-server reads
// by default (HF_HUB_CACHE, HF_HOME/hub, XDG_CACHE_HOME/huggingface/hub,
// ~/.cache/huggingface/hub; not LLAMA_CACHE, whose layout is unknown), in
// the layout its -hf writes, so that Models lists it and "-hf … --offline"
// finds it too (checked live with ggml-org/Qwen3-0.6B-GGUF:Q4_0 on
// 2026-10-27, enginekit.HubDownload):
//
//   - the files are those -hf would fetch: the hub's manifest of the tag
//     (hf.Client.Manifest) names the GGUF, its first part for a split one,
//     and the vision projector; without an answer, hf.VariantFiles and
//     hf.Projector pick them from the file names;
//   - NoVision leaves the projector out;
//   - the commit main points at and the files' checksums come from one
//     request (hf.Client.Revision); every blob is checked against its
//     SHA-256.
//
// id is the snapshot path of the GGUF (of its first part), which Models
// lists as the Model.ID. A tag the repository does not have, and a
// repository the hub does not know (401, 404), are FailModel.
func (l *LlamaCpp) Download(ctx context.Context, req engine.DownloadRequest, progress func(done, total int64)) (string, error) {
	md, err := l.ParseModel(req.Model)
	if err != nil {
		return "", engine.Failure{Kind: engine.FailModel, Line: err.Error()}
	}
	repo := md.Owner + "/" + md.Name
	tag := md.Variant
	if tag == "" {
		tag = "latest" // -hf without a tag asks for "latest"
	}
	c := &hf.Client{BaseURL: l.hubURL}
	rev, err := c.Revision(ctx, repo)
	if err != nil {
		return "", hubFailure(ctx, err)
	}
	files := rev.FileList()
	model, proj, merr := c.Manifest(ctx, repo, tag)
	if merr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		model, proj = "", hf.Projector(files)
		if vs := hf.VariantFiles(files, md.Variant); len(vs) > 0 {
			model = vs[0]
		}
	}
	if model == "" {
		return "", engine.Failure{Kind: engine.FailModel, Line: fmt.Sprintf("%s has no GGUF named for :%s", repo, md.Variant)}
	}
	names := hf.SplitParts(files, model)
	if names == nil {
		return "", engine.Failure{Kind: engine.FailModel, Line: fmt.Sprintf("%s: a part of %s is missing", repo, model)}
	}
	if proj != "" && !req.NoVision {
		names = append(names, proj)
	}
	byPath := map[string]hf.RevFile{}
	for _, f := range rev.Files {
		byPath[f.Path] = f
	}
	var want []enginekit.HubFile
	for _, n := range names {
		f, ok := byPath[n]
		if !ok {
			return "", engine.Failure{Kind: engine.FailModel, Line: fmt.Sprintf("%s has no file %s", repo, n)}
		}
		want = append(want, enginekit.HubFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256})
	}
	paths, err := enginekit.HubDownload(ctx, nil, func(file string) string { return c.FileURL(repo, rev.Commit, file) },
		l.dlDir, repo, rev.Commit, want, progress)
	if err != nil {
		return "", err
	}
	return paths[names[0]], nil
}

// hubFailure is the error of a hub request: a repository the hub does not
// know or let us read is FailModel; the others (network) stay as they are.
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

// DropPartial removes the parts Download left of the model's repository
// (enginekit.DropHubParts) in every hub cache. They are Lumilo's own, so
// since does not matter.
func (l *LlamaCpp) DropPartial(model string, _ time.Time) {
	md, err := l.ParseModel(model)
	if err != nil {
		return
	}
	enginekit.DropHubParts(enginekit.Uniq(append([]string{l.dlDir}, l.hubDirs...)), md.Owner+"/"+md.Name)
}
