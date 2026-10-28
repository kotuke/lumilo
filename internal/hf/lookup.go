package hf

import (
	"context"
	"sync"
	"time"
)

// Lookup runs the requests of a field that completes as the user types,
// off the caller's thread. A new search cancels the one still running, and
// so does a new list of variants; a request that was cancelled delivers
// nothing.
type Lookup struct {
	Client *Client
	// Deliver runs f on the thread that owns the UI (Window.Update). A
	// nil Deliver runs f on the goroutine of the request.
	Deliver func(f func())
	// Timeout bounds one request; 0 = 10 s.
	Timeout time.Duration

	mu       sync.Mutex
	search   context.CancelFunc
	variants context.CancelFunc
}

// Search looks for repositories and calls done with the answer. A failed
// search calls done with the error, for the caller to show nothing.
func (l *Lookup) Search(q Query, done func([]Repo, error)) {
	ctx := l.begin(&l.search)
	go func() {
		repos, err := l.Client.Search(ctx, q)
		l.deliver(ctx, func() { done(repos, err) })
	}()
}

// Variants lists the variants of repo and calls done with the answer.
func (l *Lookup) Variants(repo string, done func([]Variant, error)) {
	ctx := l.begin(&l.variants)
	go func() {
		vs, err := l.Client.Variants(ctx, repo)
		l.deliver(ctx, func() { done(vs, err) })
	}()
}

// Repo looks at repo (Client.Repo) and calls done with the answer; it
// shares the slot of Variants.
func (l *Lookup) Repo(repo string, rules Rules, done func(RepoInfo, error)) {
	ctx := l.begin(&l.variants)
	go func() {
		info, err := l.Client.Repo(ctx, repo, rules)
		l.deliver(ctx, func() { done(info, err) })
	}()
}

// Cancel drops the requests still running.
func (l *Lookup) Cancel() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range []*context.CancelFunc{&l.search, &l.variants} {
		if *c != nil {
			(*c)()
			*c = nil
		}
	}
}

// begin cancels the request in slot and makes the context of the next.
func (l *Lookup) begin(slot *context.CancelFunc) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if *slot != nil {
		(*slot)()
	}
	d := l.Timeout
	if d <= 0 {
		d = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	*slot = cancel
	return ctx
}

func (l *Lookup) deliver(ctx context.Context, f func()) {
	// Cancelled by a newer request, or by Cancel: the answer is stale. A
	// timeout is not: it is an error to deliver. The check runs again
	// where f runs, since a newer request may start while f waits for the
	// UI thread.
	run := func() {
		if ctx.Err() != context.Canceled {
			f()
		}
	}
	if l.Deliver == nil {
		run()
		return
	}
	l.Deliver(run)
}
