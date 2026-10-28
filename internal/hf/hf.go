// Package hf talks to the Hugging Face API for the "Add model…" field: it
// searches repositories and lists the variants (quantizations) of a GGUF
// repository with their sizes.
//
// The package knows nothing about engines. The caller says which repositories
// it can run (Source) and builds the model name from what comes back.
package hf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kotuke/lumilo/internal/gguf"
)

// BaseURL is the Hugging Face hub.
const BaseURL = "https://huggingface.co"

// FilterGGUF is the library tag of GGUF repositories: the ones that have
// variants.
const FilterGGUF = "gguf"

// maxBody caps what is read from one response. A search of 24 repositories
// with their GGUF headers is about 70 KB; a repository tree of 1000 files
// about 300 KB.
const maxBody = 4 << 20

// Client asks the Hugging Face API. The zero value works.
type Client struct {
	// HTTP is the client to use; nil = one with a 10 s timeout.
	HTTP *http.Client
	// BaseURL is the hub's address; "" = BaseURL. Tests point it at a
	// local server.
	BaseURL string
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return BaseURL
}

var defaultHTTP = &http.Client{Timeout: 10 * time.Second}

func (c *Client) get(ctx context.Context, u string) ([]byte, error) { return c.getUA(ctx, u, "") }

// StatusError is an answer of the hub other than 200 OK.
type StatusError struct {
	Code   int    // e.g. 404
	Status string // e.g. "404 Not Found"
	Path   string
}

func (e *StatusError) Error() string { return "hf: " + e.Status + ": " + e.Path }

// getUA is get with the User-Agent ua; "" keeps Go's. An answer other than
// 200 is a *StatusError.
func (c *Client) getUA(ctx context.Context, u, ua string) ([]byte, error) {
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, &StatusError{Code: resp.StatusCode, Status: resp.Status, Path: req.URL.Path}
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// Source is one kind of repository to search for.
type Source struct {
	// Filter is the Hugging Face tag the repositories carry, e.g. "gguf"
	// or "mlx".
	Filter string
	// Architectures are the architectures the caller can run, as the hub
	// reports them: gguf.architecture of a GGUF repository ("qwen35"),
	// config.model_type of the others ("qwen3_5"). Empty = any. A
	// repository whose architecture the hub does not report is kept: it is
	// unknown, not refused.
	Architectures []string
}

// Query is one search.
type Query struct {
	Text    string
	Sources []Source
	// Limit is how many repositories to return; 0 = 8.
	Limit int
}

// Repo is a repository found by Search.
type Repo struct {
	ID        string // "owner/repo"
	Downloads int64
	Likes     int64
	// Filter is the Source.Filter that found it.
	Filter string
	// Architecture is as the hub reports it; "" when not asked or unknown.
	Architecture string
}

// GGUF reports whether the repository holds GGUF files, which come in
// variants.
func (r Repo) GGUF() bool { return r.Filter == FilterGGUF }

// Search finds repositories matching q.Text, most downloaded first, from
// all of q.Sources (asked at the same time). It fails only when every
// source failed.
func (c *Client) Search(ctx context.Context, q Query) ([]Repo, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 8
	}
	text := strings.TrimSpace(q.Text)
	if text == "" || len(q.Sources) == 0 {
		return nil, nil
	}
	type result struct {
		repos []Repo
		err   error
	}
	results := make([]result, len(q.Sources))
	var wg sync.WaitGroup
	for i, s := range q.Sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].repos, results[i].err = c.searchOne(ctx, text, s, limit)
		}()
	}
	wg.Wait()

	var all []Repo
	var errs []error
	for _, r := range results {
		all = append(all, r.repos...)
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	if len(errs) == len(results) {
		return nil, errors.Join(errs...)
	}
	// Most downloaded first; the first source wins a repository found twice.
	slices.SortStableFunc(all, func(a, b Repo) int {
		switch {
		case a.Downloads > b.Downloads:
			return -1
		case a.Downloads < b.Downloads:
			return 1
		}
		return 0
	})
	seen := map[string]bool{}
	out := all[:0]
	for _, r := range all {
		if !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	return out[:min(len(out), limit)], nil
}

// apiModel is a repository in the API's list.
type apiModel struct {
	ID        string `json:"id"`
	Downloads int64  `json:"downloads"`
	Likes     int64  `json:"likes"`
	Siblings  []struct {
		Name string `json:"rfilename"`
	} `json:"siblings"`
	GGUF *struct {
		Architecture string `json:"architecture"`
	} `json:"gguf"`
	Config *struct {
		ModelType string `json:"model_type"`
	} `json:"config"`
}

func (c *Client) searchOne(ctx context.Context, text string, s Source, limit int) ([]Repo, error) {
	v := url.Values{}
	v.Set("search", text)
	v.Set("sort", "downloads")
	v.Set("direction", "-1")
	if s.Filter != "" {
		v.Set("filter", s.Filter)
	}
	// The list names no files and no architecture unless asked; an
	// expanded list holds only the fields asked for. The file names
	// (siblings) tell a repository of GGUF models from one of vision
	// projectors or drafts alone; the architecture comes with the hub's
	// gguf or config summary, which also carries every repository's chat
	// template, so config is asked only when the caller filters. Ask for
	// more than needed: the filters drop some.
	v["expand[]"] = []string{"downloads", "likes", "siblings"}
	switch {
	case s.Filter == FilterGGUF:
		v["expand[]"] = append(v["expand[]"], "gguf")
	case len(s.Architectures) > 0:
		v["expand[]"] = append(v["expand[]"], "config")
	}
	v.Set("limit", fmt.Sprint(limit*3))
	body, err := c.get(ctx, c.base()+"/api/models?"+v.Encode())
	if err != nil {
		return nil, err
	}
	var list []apiModel
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("hf: search: %w", err)
	}
	var repos []Repo
	for _, m := range list {
		if m.ID == "" || !strings.Contains(m.ID, "/") {
			continue
		}
		r := Repo{ID: m.ID, Downloads: m.Downloads, Likes: m.Likes, Filter: s.Filter}
		switch {
		case m.GGUF != nil && s.Filter == FilterGGUF:
			r.Architecture = m.GGUF.Architecture
		case m.Config != nil:
			r.Architecture = m.Config.ModelType
		}
		if r.Architecture == "clip" {
			// The hub describes a GGUF repository by one of its files, at
			// times its vision projector: the model is still unknown
			// (cdiamond/Qwen3.8-27B-iMatrix-NVFP4-MTP-GGUF).
			r.Architecture = ""
		}
		if gguf.NonTextArchs[r.Architecture] {
			continue // an encoder or a draft
		}
		if len(s.Architectures) > 0 && r.Architecture != "" && !slices.Contains(s.Architectures, r.Architecture) {
			continue
		}
		if s.Filter == FilterGGUF && m.Siblings != nil {
			names := make([]string, len(m.Siblings))
			for i, f := range m.Siblings {
				names[i] = f.Name
			}
			if !HasModelGGUF(names) {
				continue
			}
		}
		repos = append(repos, r)
	}
	return repos, nil
}

// HasModelGGUF reports whether a repository with these files holds a GGUF
// model to serve: a .gguf in the root, or the parts of a split one in a
// folder, that is not a vision projector, an imatrix, a speculative decoding
// head or draft alone (gguf.Sidecar, or "DFlash"/"Eagle3" in its name), nor
// an embedding or reranking model by its name. HermiHg/Qwen3.8-27B-mmproj-
// Q5_K-MIX-GGUF holds only mmproj-Qwen3.8-27B-Q5_K-MIX.gguf, and
// Alittlehammmer/Qwen3.6-27B-DFlash-GGUF-llama.cpp only DFlash drafts.
func HasModelGGUF(files []string) bool {
	for _, f := range files {
		low := strings.ToLower(f)
		if !strings.HasSuffix(low, ".gguf") || gguf.Sidecar(f) != "" {
			continue
		}
		if dir, _ := path.Split(f); dir != "" && (strings.Count(dir, "/") > 1 || !splitPartRe.MatchString(low)) {
			continue // a lone GGUF in a folder: an MTP head or the like
		}
		base := path.Base(low)
		if strings.Contains(base, "dflash") || strings.Contains(base, "eagle3") || strings.Contains(base, "dspark") ||
			strings.Contains(base, "embed") || strings.Contains(base, "rerank") {
			continue
		}
		return true
	}
	return false
}

var splitPartRe = regexp.MustCompile(`-\d{5}-of-\d{5}\.gguf$`)
