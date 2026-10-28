package enginekit

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// HubCaches are the Hugging Face hub caches (models--OWNER--REPO/...) an
// engine that uses huggingface_hub's layout may read or download into, in
// huggingface_hub's order of precedence: HF_HUB_CACHE, HF_HOME/hub,
// XDG_CACHE_HOME/huggingface/hub, ~/.cache/huggingface/hub. extra
// directories go after HF_HOME/hub. All of them are listed, as the
// engine's environment may differ from the app's.
func HubCaches(home string, extra ...string) []string {
	var dirs []string
	if d := os.Getenv("HF_HUB_CACHE"); d != "" {
		dirs = append(dirs, d)
	}
	if d := os.Getenv("HF_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "hub"))
	}
	dirs = append(dirs, extra...)
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "huggingface", "hub"))
	}
	return Uniq(append(dirs, filepath.Join(home, ".cache", "huggingface", "hub")))
}

// RepoFolder is the folder of the repository OWNER/REPO in a hub cache:
// models--OWNER--REPO.
func RepoFolder(repo string) string {
	return "models--" + strings.ReplaceAll(repo, "/", "--")
}

// HubMeter measures a download into the hub caches dirs by how their files
// grow: the meter it returns is the bytes added since HubMeter was called
// (never less than 0). It counts the regular files of the folders of
// repos, or of every repository when repos is empty, whatever they are
// called: a file being downloaded is a blob with a suffix, and both
// writers checked grow it as they fetch (llama-server 0.7.0
// "<sha256>.downloadInProgress", huggingface_hub 1.28 with hf_xet
// "<sha256>.<id>.incomplete"; testdata/hub-*.txt). The files already there
// (another variant of the repository, say) are the baseline.
func HubMeter(dirs []string, repos ...string) func() int64 {
	var folders []string
	for _, d := range dirs {
		if len(repos) == 0 {
			folders = append(folders, d)
			continue
		}
		for _, r := range repos {
			folders = append(folders, filepath.Join(d, RepoFolder(r)))
		}
	}
	folders = Uniq(folders)
	size := func() int64 {
		var n int64
		for _, f := range folders {
			n += treeSize(f, len(repos) == 0)
		}
		return n
	}
	base := size()
	return func() int64 { return max(size()-base, 0) }
}

// treeSize is the size of the regular files under dir, symlinks not
// followed (a snapshot links to its blobs). onlyRepos counts only the
// models--* folders of a hub cache.
func treeSize(dir string, onlyRepos bool) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // a folder that is gone or unreadable counts as empty
		}
		if onlyRepos && d.IsDir() && filepath.Dir(path) == dir && !strings.HasPrefix(d.Name(), "models--") {
			return fs.SkipDir
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
