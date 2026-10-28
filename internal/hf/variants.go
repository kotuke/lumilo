package hf

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/kotuke/lumilo/internal/gguf"
)

// Variant is one GGUF of a repository, or one split GGUF, named by the tag
// that "owner/repo:TAG" takes: llama-server's -hf and "splash serve --model"
// both match it to the end of the file name.
type Variant struct {
	Tag   string // e.g. "UD-Q4_K_M"
	Bytes int64  // all the files of the variant
	Parts int    // 1, or the number of parts of a split GGUF
}

// File is a file of a repository.
type File struct {
	Path string // from the repository root, e.g. "Q8_0/model-Q8_0-00001-of-00002.gguf"
	Size int64
}

// Variants lists the GGUF variants of repo ("owner/repo") with their sizes,
// the 4-bit ones first. A repository without GGUF files has none.
func (c *Client) Variants(ctx context.Context, repo string) ([]Variant, error) {
	files, err := c.tree(ctx, repo)
	if err != nil {
		return nil, err
	}
	return ParseVariants(repo, files), nil
}

var splitRe = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})$`)

// ParseVariants finds the variants among a repository's files.
//
// A variant is a GGUF in the root, or the parts of a split GGUF in the root
// or in a folder of its own (Q8_0/model-Q8_0-00001-of-00002.gguf), which
// counts only when every part is there. Vision projectors (mmproj), imatrix
// files, speculative decoding heads and drafts (mtp-*, dflash-*, eagle3-*,
// dspark-*: gguf.Sidecar) and GGUFs in other folders (multi-token
// prediction heads) are not models to serve and are left out.
//
// The tag is what follows the words all the variants' names share
// ("Qwen3.8-27B-UD-Q4_K_M" among "Qwen3.8-27B-…" is "UD-Q4_K_M"); a lone GGUF
// has the words of its name after the repository's name. The 4-bit ones come
// first, Q4_K_M before the rest, then by size.
func ParseVariants(repo string, files []File) []Variant { return parseVariants(repo, files, "") }

// parseVariants is ParseVariants for a model whose general.name, spelled
// with dashes, is model: when every file name starts with it, the tags keep
// the words after it, even those the names share ("Mix-13G" and
// "Mix-15.7G" for OrcaCyber-Nano-1.0-AI-Defense-Mix-*.gguf, not "13G" and
// "15.7G"). Splash takes either: "owner/repo:VARIANT" picks the only GGUF
// whose name ends in -VARIANT (install/upstream.py select_gguf).
func parseVariants(repo string, files []File, model string) []Variant {
	type group struct {
		stem   string // the name without .gguf and the split suffix
		bytes  int64
		parts  int
		total  int // parts the split names; 1 for a lone file
		seen   map[int]bool
		isRoot bool
	}
	groups := map[string]*group{}
	var order []string
	for _, f := range files {
		dir, base := path.Split(f.Path)
		low := strings.ToLower(base)
		if !strings.HasSuffix(low, ".gguf") || gguf.Sidecar(base) != "" {
			continue
		}
		stem := base[:len(base)-len(".gguf")]
		part, total := 1, 1
		if m := splitRe.FindStringSubmatch(stem); m != nil {
			fmt.Sscan(m[2], &part)
			fmt.Sscan(m[3], &total)
			stem = m[1]
		} else if dir != "" {
			continue // a lone GGUF in a folder
		}
		if strings.Count(dir, "/") > 1 {
			continue
		}
		key := dir + stem
		g := groups[key]
		if g == nil {
			g = &group{stem: stem, total: total, seen: map[int]bool{}, isRoot: dir == ""}
			groups[key] = g
			order = append(order, key)
		}
		if g.seen[part] || total != g.total {
			continue
		}
		g.seen[part] = true
		g.bytes += f.Size
		g.parts++
	}

	var names []string
	var done []*group
	for _, k := range order {
		if g := groups[k]; g.parts == g.total {
			names = append(names, g.stem)
			done = append(done, g)
		}
	}
	tags := tagsOf(repo, names, model)
	var out []Variant
	seen := map[string]bool{}
	for i, g := range done {
		tag := tags[i]
		// Two folders may carry the same tag; the first one is what the
		// engine finds.
		if tag == "" || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		out = append(out, Variant{Tag: tag, Bytes: g.bytes, Parts: g.total})
	}
	slices.SortStableFunc(out, func(a, b Variant) int {
		if ra, rb := rank(a.Tag), rank(b.Tag); ra != rb {
			return ra - rb
		}
		switch {
		case a.Bytes < b.Bytes:
			return -1
		case a.Bytes > b.Bytes:
			return 1
		}
		return 0
	})
	return out
}

// tagsOf cuts the words every name starts with, so that no tag is empty.
func tagsOf(repo string, names []string, model string) []string {
	words := make([][]string, len(names))
	for i, n := range names {
		words[i] = strings.Split(n, "-")
	}
	shared := 0
	switch {
	case len(names) == 1:
		// Nothing to compare: the words of the repository's name.
		base := gguf.RepoBase(repo)
		prefix := strings.Split(base, "-")
		w := words[0]
		if len(w) > len(prefix) && strings.EqualFold(strings.Join(w[:len(prefix)], "-"), base) {
			shared = len(prefix)
		} else {
			shared = len(w) // no tag, unless the name ends in a quantization
		}
	case len(names) > 1 && model != "" && allStart(names, model):
		shared = len(strings.Split(model, "-"))
	case len(names) > 1:
		shared = len(words[0])
		for _, w := range words[1:] {
			n := 0
			for n < min(shared, len(w)) && strings.EqualFold(words[0][n], w[n]) {
				n++
			}
			shared = n
		}
		for _, w := range words {
			shared = min(shared, len(w)-1)
		}
		// A tag that starts with a number says little ("13G" of
		// "…-Mix-13G" and "…-Mix-15.7G"): keep the word before it, as the
		// model's name would (parseVariants) when its header is out of
		// reach, as in a gated repository.
		for shared > 0 && slices.ContainsFunc(words, func(w []string) bool {
			return w[shared] != "" && w[shared][0] >= '0' && w[shared][0] <= '9'
		}) {
			shared--
		}
	}
	tags := make([]string, len(names))
	for i, w := range words {
		tags[i] = strings.Join(w[min(shared, len(w)):], "-")
		// "model.Q4_K_M" names its quantization after a dot.
		if j := strings.LastIndex(tags[i], "."); j >= 0 && quantRe.MatchString(tags[i][j+1:]) {
			tags[i] = tags[i][j+1:]
		} else if tags[i] == "" {
			if m := quantSuffixRe.FindStringSubmatch(names[i]); m != nil {
				tags[i] = m[1]
			}
		}
	}
	return tags
}

// quantRe matches a quantization name alone; quantSuffixRe finds one at the
// end of a file name.
var (
	quantRe       = regexp.MustCompile(`(?i)^(?:i?q\d\w*|bf16|f16|f32|mxfp4\w*|nvfp4\w*)$`)
	quantSuffixRe = regexp.MustCompile(`(?i)[-.]((?:ud-)?(?:i?q\d\w*|bf16|f16|f32|mxfp4\w*|nvfp4\w*))$`)
)

// rank orders tags: Q4_K_M, with or without Unsloth's UD- prefix, then the
// other 4-bit quantizations, then the rest.
func rank(tag string) int {
	t := strings.ToUpper(tag)
	t = strings.TrimPrefix(t, "UD-")
	switch {
	case t == "Q4_K_M":
		return 0
	case strings.Contains(t, "Q4") || strings.Contains(t, "IQ4") || strings.Contains(t, "MXFP4") || strings.Contains(t, "NVFP4"):
		return 1
	}
	return 2
}

// allStart reports whether every name starts with prefix and a dash.
func allStart(names []string, prefix string) bool {
	for _, n := range names {
		if len(n) <= len(prefix)+1 || !strings.EqualFold(n[:len(prefix)], prefix) || n[len(prefix)] != '-' {
			return false
		}
	}
	return true
}
