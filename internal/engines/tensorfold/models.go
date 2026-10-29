package tensorfold

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/hf"
)

// Model.ID is the repository, "owner/repo": "tensorfold serve REPO" serves
// a cached checkpoint by its repository name (README "Models on disk"),
// and the server reads the snapshot refs/main names.
//
// Models lists the checkpoints of the hub caches the engine serves, as
// "tensorfold models" does ("lists the cached checkpoints with a model type
// it knows", README; fixture models.txt): a snapshot with a config.json of
// a family and format of families.go and every safetensors shard its
// index names. DFlash drafters are no models of their own: they show as a
// model's draft (Features), the first of them its default one.

// Models lists the checkpoints found locally.
func (tf *TensorFold) Models(ctx context.Context) ([]engine.Model, error) {
	var out []engine.Model
	seen := map[string]bool{}
	fams, noJPEG := tf.capabilities(ctx)
	for _, d := range tf.hubDirs {
		snaps, err := hubSnapshots(ctx, d)
		if err != nil {
			return nil, err
		}
		for _, s := range snaps {
			if seen[s.repo] {
				continue
			}
			m, cp, ok := s.model(fams)
			if !ok {
				continue
			}
			if cp.drafterOK() {
				m.Features.Drafts = tf.drafters(m.Name)
			}
			m.Features.NoJPEG = m.Features.Vision && noJPEG
			seen[s.repo] = true
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out, nil
}

// snapshot is the snapshot of a repository in a hub cache that the server
// would read.
type snapshot struct {
	repo string // "owner/repo"
	dir  string // .../models--owner--repo/snapshots/<commit>
}

// hubSnapshots lists one snapshot per repository of the hub cache root:
// the one refs/main names, else the newest.
func hubSnapshots(ctx context.Context, root string) ([]snapshot, error) {
	repos, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []snapshot
	for _, r := range repos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rest, ok := strings.CutPrefix(r.Name(), "models--")
		if !ok || !r.IsDir() {
			continue
		}
		owner, name, ok := strings.Cut(rest, "--")
		if !ok || owner == "" || name == "" {
			continue
		}
		dir := filepath.Join(root, r.Name())
		if snap := pickSnapshot(dir); snap != "" {
			out = append(out, snapshot{repo: owner + "/" + name, dir: snap})
		}
	}
	return out, nil
}

// pickSnapshot is the snapshot of the repository folder dir that refs/main
// names, else the newest; "" when there is none.
func pickSnapshot(dir string) string {
	if b, err := os.ReadFile(filepath.Join(dir, "refs", "main")); err == nil {
		snap := filepath.Join(dir, "snapshots", strings.TrimSpace(string(b)))
		if st, err := os.Stat(snap); err == nil && st.IsDir() {
			return snap
		}
	}
	snaps, err := os.ReadDir(filepath.Join(dir, "snapshots"))
	if err != nil {
		return ""
	}
	best, bestTime := "", int64(0)
	for _, s := range snaps {
		fi, err := s.Info()
		if err != nil || !s.IsDir() {
			continue
		}
		if t := fi.ModTime().UnixNano(); best == "" || t > bestTime {
			best, bestTime = s.Name(), t
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(dir, "snapshots", best)
}

// model is the snapshot as a Model, if the engine serves it and all of its
// weights are there, with what its config.json says.
func (s snapshot) model(fams map[string][]string) (engine.Model, checkpoint, bool) {
	data, err := os.ReadFile(filepath.Join(s.dir, "config.json"))
	if err != nil {
		return engine.Model{}, checkpoint{}, false
	}
	cp, err := parseCheckpoint(data)
	if err != nil || cp.supported(fams) != "" {
		return engine.Model{}, cp, false
	}
	size, ok := weightsSize(s.dir)
	if !ok {
		return engine.Model{}, checkpoint{}, false
	}
	owner, name, _ := strings.Cut(s.repo, "/")
	cfg := hf.ParseConfig(data)
	m := engine.Model{ID: s.repo, Name: name, Owner: owner, SizeBytes: size,
		ContextLength: cfg.Context, KVElements: cfg.KVElements}
	// The vision tower's weights are part of the safetensors (SizeBytes);
	// loaded, it takes visionBytes more.
	m.Features.Vision = cp.visionOK()
	if m.Features.Vision {
		m.Features.VisionBytes = visionBytes
	}
	return m, cp, true
}

// weightsSize is the size of the snapshot's safetensors in its root. ok is
// false when there are none, when model.safetensors.index.json names a
// shard that is missing, or when a shard "<name>-0000k-of-0000n" is there
// without all n of them: a download in progress or broken off (a snapshot
// links each file once it is complete, the index maybe after the shards).
func weightsSize(dir string) (int64, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, false
	}
	var total int64
	have := map[string]bool{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".safetensors") {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, e.Name())) // follows the snapshot link
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		have[e.Name()] = true
		total += st.Size()
	}
	if total == 0 {
		return 0, false
	}
	for name := range have {
		if m := shardRe.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[2])
			for k := 1; k <= n; k++ {
				if !have[fmt.Sprintf("%s-%05d-of-%s.safetensors", m[1], k, m[2])] {
					return 0, false
				}
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "model.safetensors.index.json")); err == nil {
		var idx struct {
			WeightMap map[string]string `json:"weight_map"`
		}
		if json.Unmarshal(b, &idx) == nil {
			for _, f := range idx.WeightMap {
				if !have[f] {
					return 0, false
				}
			}
		}
	}
	return total, true
}

// visionBytes is what --vision adds in memory: "Qwen3.8-27B: image tower
// loaded (1.79 GiB with scratch for 4096 image tokens a request)" (the
// server's log, 2026-10-29). Measured for that model only; the other
// families that read images get the same.
const visionBytes = 1_922_000_000

// shardRe is a safetensors shard: model-00001-of-00003.safetensors.
var shardRe = regexp.MustCompile(`^(.*)-\d{5}-of-(\d{5})\.safetensors$`)

// drafterBase is the model name a drafter is for: its repository name up
// to "-DFlash" ("Qwen3.8-27B" of z-lab/Qwen3.8-27B-DFlash2).
func drafterBase(repo string) string {
	_, name, _ := strings.Cut(repo, "/")
	i := strings.Index(strings.ToLower(name), "-dflash")
	if i <= 0 {
		return ""
	}
	return strings.ToLower(name[:i])
}

// drafters are the cached DFlash drafters made for the model named name
// (its repository's name): the README recipe's (qwen27Drafter) first, the
// others by repository; the first is the default one (Draft.Default),
// which ServeCommand passes when Config.Draft is "". The cache may hold
// another engine's DFlash2 of the same model, such as Splash's
// incoai/Qwen3.8-27B-DFlash2, which TensorFold loads too.
// A drafter is made for a model when its base name starts the model's name
// (z-lab/Qwen3.8-27B-DFlash2 for TensorFold/Qwen3.8-27B-MLX-4bit,
// z-lab/gemma-4-31B-it-DFlash for mlx-community/gemma-4-31B-it-4bit) and
// its config.json has "dflash_config". The Config.Draft of one is its
// repository, which --drafter takes. Only repositories with "-DFlash" in
// their name are read, so a model's list costs a directory listing.
func (tf *TensorFold) drafters(name string) []engine.Draft {
	var out []engine.Draft
	seen := map[string]bool{}
	for _, cache := range tf.hubDirs {
		entries, err := os.ReadDir(cache)
		if err != nil {
			continue
		}
		for _, e := range entries {
			rest, ok := strings.CutPrefix(e.Name(), "models--")
			owner, repoName, ok2 := strings.Cut(rest, "--")
			if !ok || !ok2 || !e.IsDir() {
				continue
			}
			repo := owner + "/" + repoName
			base := drafterBase(repo)
			if base == "" || seen[repo] || !strings.HasPrefix(strings.ToLower(name), base) {
				continue
			}
			snap := pickSnapshot(filepath.Join(cache, e.Name()))
			data, err := os.ReadFile(filepath.Join(snap, "config.json"))
			if err != nil {
				continue
			}
			cp, err := parseCheckpoint(data)
			size, complete := weightsSize(snap)
			if err != nil || !cp.Drafter || !complete {
				continue
			}
			seen[repo] = true
			out = append(out, engine.Draft{Kind: engine.DraftDFlash, Name: repo, ID: repo, SizeBytes: drafterMemory(size)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].ID == qwen27Drafter) != (out[j].ID == qwen27Drafter) {
			return out[i].ID == qwen27Drafter
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > 0 {
		out[0].Default = true
	}
	return out
}

// drafterMemory is what a drafter of size bytes of bf16 weights takes in
// memory, its own cache included: z-lab/Qwen3.8-27B-DFlash2 (3.85 GB)
// took 1.6 GiB, 28.6 GiB kept wired with it and 27.0 GiB with
// --no-drafts, Qwen3.8-27B at 131K on 48 GiB (2026-10-29). The server
// loads its weights at 4 bits ("--drafter-bits 0|4: 4 bits (the
// default)", README), about 1.1 GB of it.
func drafterMemory(size int64) int64 { return size * 45 / 100 }
