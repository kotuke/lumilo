package enginekit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// LocalClient is an HTTP client for a server on 127.0.0.1: no keep-alive
// (some servers answer "Connection: close") and never a system proxy.
func LocalClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}
}

// Reply is the answer to Request.
type Reply struct {
	Code int // HTTP status; 0 when no response
	Body []byte
	Err  error
}

// Request sends method path to the server at t on 127.0.0.1 with client,
// within timeout. The API key of t goes in "Authorization: Bearer"; a body
// is sent as JSON. At most 4 MiB of the answer is read.
func Request(ctx context.Context, client *http.Client, t engine.Target, method, path string, body []byte, timeout time.Duration) Reply {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", t.Port, path), rd)
	if err != nil {
		return Reply{Err: err}
	}
	if t.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return Reply{Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return Reply{Code: resp.StatusCode, Body: data, Err: err}
}

// feedTimeout bounds a request to a release feed.
const feedTimeout = 10 * time.Second

// getFeed fetches a release feed with the headers given (name, value
// pairs) and returns at most limit bytes of a 200 answer.
func getFeed(ctx context.Context, url string, limit int64, headers ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Lumilo")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release feed answered HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// GitHubLatest asks the GitHub releases API (unauthenticated) for the
// latest release at url, e.g.
// https://api.github.com/repos/OWNER/REPO/releases/latest, and returns its
// tag without a leading "v".
func GitHubLatest(ctx context.Context, url string) (string, error) {
	body, err := getFeed(ctx, url, 1<<20,
		"Accept", "application/vnd.github+json", "X-GitHub-Api-Version", "2022-11-28")
	if err != nil {
		return "", err
	}
	return ParseGitHubRelease(body)
}

// ParseGitHubRelease reads tag_name from a GitHub release and drops a
// leading "v".
func ParseGitHubRelease(body []byte) (string, error) {
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("release feed: %w", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(rel.Tag), "v")
	if v == "" {
		return "", errors.New("release feed has no tag_name")
	}
	return v, nil
}

// FormulaLatest reads a formula from the Homebrew JSON API at url, e.g.
// https://formulae.brew.sh/api/formula/NAME.json: the version
// "brew upgrade NAME" installs once brew has updated.
func FormulaLatest(ctx context.Context, url string) (string, error) {
	body, err := getFeed(ctx, url, 4<<20, "Accept", "application/json")
	if err != nil {
		return "", err
	}
	return ParseFormula(body)
}

// ParseFormula reads versions.stable of a Homebrew formula. The formula's
// revision is left out, as DropRevision leaves it out of a keg name.
func ParseFormula(body []byte) (string, error) {
	var f struct {
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &f); err != nil {
		return "", fmt.Errorf("formula feed: %w", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(f.Versions.Stable), "v")
	if v == "" {
		return "", errors.New("formula feed has no stable version")
	}
	return v, nil
}

// DropRevision cuts a Homebrew revision ("0.7.0_1" -> "0.7.0"). The
// supervisor compares dotted numbers and reads "0_1" as 0, so "0.7.10_1"
// would look older than "0.7.9". Without the suffix a rebuild of the same
// version is not offered as an update, which is harmless.
func DropRevision(v string) string {
	v, _, _ = strings.Cut(v, "_")
	return v
}
