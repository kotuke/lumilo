package tensorfold

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
	"github.com/kotuke/lumilo/internal/hf"
)

var _ engine.Downloader = (*TensorFold)(nil)

// Download fetches the checkpoint "owner/repo" into the hub cache the
// server reads, in the layout "tensorfold pull" writes (README "Models on
// disk"; fixture pull-qwen35-2b.txt: the snapshot under
// ~/.cache/huggingface/hub/models--OWNER--REPO), so that Models lists it
// and "tensorfold serve REPO" finds it (enginekit.HubDownload):
//
//   - first config.json, which must name a family and format the engine
//     serves (pull.zig: "The family check runs on config.json before any
//     weight moves"); otherwise FailModel, as pull refuses
//     LiquidAI/LFM3-1.7B-MLX-4bit (fixture pull-refused-lfm3.txt). A
//     DFlash2 drafter alone is refused too: it comes with its model;
//   - then the files in the repository's root, as pull fetches them all
//     (.gitattributes, README.md, chat_template.jinja, tokenizer files,
//     the safetensors and their index), but GGUF files, which a mixed
//     repository may hold beside the checkpoint;
//   - with a Qwen3.8-27B-class checkpoint, its DFlash2 drafter
//     z-lab/Qwen3.8-27B-DFlash2 the same way, as the README's recipe pulls
//     both (Models offers it as the default draft); progress counts both;
//   - LFS files are checked against their SHA-256, plain Git files against
//     their Git blob id, which also names their blobs.
//
// NoVision changes nothing: the vision tower is part of the safetensors.
// id is the repository, which Models lists as the Model.ID. A repository
// the hub does not know (401, 404) is FailModel.
func (tf *TensorFold) Download(ctx context.Context, req engine.DownloadRequest, progress func(done, total int64)) (string, error) {
	md, err := tf.ParseModel(req.Model)
	if err != nil {
		return "", engine.Failure{Kind: engine.FailModel, Line: err.Error()}
	}
	c := &hf.Client{BaseURL: tf.hubURL}
	model, err := tf.plan(ctx, c, md.ID)
	if err != nil {
		return "", err
	}
	if model.cp.Drafter {
		return "", engine.Failure{Kind: engine.FailModel, Line: md.ID + ": a DFlash2 drafter comes with its model; add the model"}
	}
	if why := model.cp.supported(tf.families(ctx)); why != "" {
		return "", engine.Failure{Kind: engine.FailModel, Line: md.ID + ": " + why}
	}
	repos := []repoPlan{model}
	if model.cp.Dense27 {
		d, err := tf.plan(ctx, c, qwen27Drafter)
		if err != nil {
			return "", err
		}
		repos = append(repos, d)
	}
	var total, base int64
	for _, r := range repos {
		total += r.size()
	}
	for _, r := range repos {
		report := func(done, _ int64) {
			if progress != nil {
				progress(base+done, total)
			}
		}
		if _, err := enginekit.HubDownload(ctx, nil, func(file string) string { return c.FileURL(r.repo, r.commit, file) },
			tf.dlDir, r.repo, r.commit, r.files, report); err != nil {
			return "", err
		}
		base += r.size()
	}
	return md.ID, nil
}

// qwen27Drafter is the DFlash2 drafter of Qwen3.8-27B (README model table:
// "tensorfold pull z-lab/Qwen3.8-27B-DFlash2"; "Add --drafter
// z-lab/Qwen3.8-27B-DFlash2").
const qwen27Drafter = "z-lab/Qwen3.8-27B-DFlash2"

// repoPlan is what Download fetches of one repository.
type repoPlan struct {
	repo, commit string
	files        []enginekit.HubFile
	cp           checkpoint
}

func (r repoPlan) size() int64 {
	var n int64
	for _, f := range r.files {
		n += f.Size
	}
	return n
}

// plan asks the hub for the files of repo at the commit main points at,
// those in its root but GGUF files, and reads its config.json.
func (tf *TensorFold) plan(ctx context.Context, c *hf.Client, repo string) (repoPlan, error) {
	rev, err := c.Revision(ctx, repo)
	if err != nil {
		return repoPlan{}, hubFailure(ctx, err)
	}
	r := repoPlan{repo: repo, commit: rev.Commit}
	hasConfig := false
	for _, f := range rev.Files {
		if strings.Contains(f.Path, "/") || strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		hasConfig = hasConfig || f.Path == "config.json"
		r.files = append(r.files, enginekit.HubFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256, BlobID: f.BlobID})
	}
	if !hasConfig {
		return repoPlan{}, engine.Failure{Kind: engine.FailModel, Line: fmt.Sprintf("%s has no config.json: not a checkpoint", repo)}
	}
	// The small files first, the weights' index among them: Models takes a
	// snapshot for complete when its index names no missing shard.
	sort.SliceStable(r.files, func(i, j int) bool {
		return !strings.HasSuffix(r.files[i].Path, ".safetensors") && strings.HasSuffix(r.files[j].Path, ".safetensors")
	})
	data, err := tf.fetch(ctx, c.FileURL(repo, rev.Commit, "config.json"))
	if err != nil {
		return repoPlan{}, hubFailure(ctx, err)
	}
	if r.cp, err = parseCheckpoint(data); err != nil {
		return repoPlan{}, engine.Failure{Kind: engine.FailModel, Line: fmt.Sprintf("%s: config.json: %v", repo, err)}
	}
	return r, nil
}

// fetch GETs a small file of the hub (config.json), at most 16 MiB.
func (tf *TensorFold) fetch(ctx context.Context, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := enginekit.HubClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &hf.StatusError{Code: resp.StatusCode, Status: resp.Status, Path: u}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
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
// and of the drafter that comes with a 27B-class model
// (enginekit.DropHubParts) in every hub cache. They are Lumilo's own, so
// since does not matter.
func (tf *TensorFold) DropPartial(model string, _ time.Time) {
	md, err := tf.ParseModel(model)
	if err != nil {
		return
	}
	dirs := enginekit.Uniq(append([]string{tf.dlDir}, tf.hubDirs...))
	enginekit.DropHubParts(dirs, md.ID)
	// The drafter's, when the model is one it comes with (its config.json
	// is in the cache: Download fetched it): a paused download of another
	// model may own them otherwise.
	if cp, ok := tf.checkpointOf(md.ID); ok && cp.Dense27 {
		enginekit.DropHubParts(dirs, qwen27Drafter)
	}
}
