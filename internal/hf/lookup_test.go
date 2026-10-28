package hf

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLookupStaleSearchDeliversNothing(t *testing.T) {
	_, c := newHub(t)
	// The first search waits at the server until the second has started.
	gate := make(chan struct{})
	first := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") == "qw" {
			once.Do(func() { close(first) })
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(`[{"id":"a/` + r.URL.Query().Get("search") + `","downloads":3}]`))
	}))
	defer srv.Close()
	defer close(gate)
	c.BaseURL = srv.URL

	got := make(chan string, 4)
	l := &Lookup{Client: c, Deliver: func(f func()) { f() }}
	src := []Source{{Filter: FilterGGUF}}
	l.Search(Query{Text: "qw", Sources: src}, func(rs []Repo, err error) { got <- "stale" })
	<-first
	l.Search(Query{Text: "qwen", Sources: src}, func(rs []Repo, err error) {
		if err != nil || len(rs) != 1 {
			got <- "bad"
			return
		}
		got <- rs[0].ID
	})
	select {
	case id := <-got:
		if id != "a/qwen" {
			t.Errorf("delivered %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing delivered")
	}
	select {
	case id := <-got:
		t.Errorf("a second answer: %q", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLookupDeliversOnTheUIThread(t *testing.T) {
	_, c := newHub(t)
	posted := make(chan func(), 1)
	l := &Lookup{Client: c, Deliver: func(f func()) { posted <- f }}
	got := make(chan []Variant, 1)
	l.Variants("unsloth/Qwen3.8-27B-GGUF", func(vs []Variant, err error) {
		if err != nil {
			t.Error(err)
		}
		got <- vs
	})
	select {
	case f := <-posted:
		select {
		case <-got:
			t.Fatal("done ran before Deliver ran it")
		default:
		}
		f()
		if vs := <-got; len(vs) == 0 || vs[0].Tag != "UD-Q4_K_M" {
			t.Errorf("variants %+v", vs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing posted")
	}
}

func TestLookupErrorAndCancel(t *testing.T) {
	dead := httptest.NewServer(nil)
	dead.Close()
	l := &Lookup{Client: &Client{BaseURL: dead.URL}}
	errs := make(chan error, 1)
	l.Search(Query{Text: "qwen", Sources: []Source{{Filter: FilterGGUF}}}, func(_ []Repo, err error) { errs <- err })
	if err := <-errs; err == nil {
		t.Error("no error without a network")
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	l = &Lookup{Client: &Client{BaseURL: slow.URL}}
	called := make(chan struct{}, 1)
	l.Variants("a/b", func([]Variant, error) { called <- struct{}{} })
	l.Cancel()
	select {
	case <-called:
		t.Error("a cancelled request delivered")
	case <-time.After(200 * time.Millisecond):
	}

	// A timeout is an error to show nothing for, not a cancel.
	l = &Lookup{Client: &Client{BaseURL: slow.URL}, Timeout: 50 * time.Millisecond}
	l.Search(Query{Text: "qwen", Sources: []Source{{Filter: FilterGGUF}}}, func(_ []Repo, err error) { errs <- err })
	select {
	case err := <-errs:
		if err == nil {
			t.Error("no error on timeout")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a timeout delivered nothing")
	}
}

// An answer waiting for the UI thread is dropped when a newer request starts
// before it runs.
func TestLookupDropsAnswerQueuedBehindNewerRequest(t *testing.T) {
	_, c := newHub(t)
	posted := make(chan func(), 4)
	l := &Lookup{Client: c, Deliver: func(f func()) { posted <- f }}
	src := []Source{{Filter: FilterGGUF}}
	var got []string
	var mu sync.Mutex
	add := func(s string) { mu.Lock(); got = append(got, s); mu.Unlock() }
	l.Search(Query{Text: "qwen", Sources: src}, func([]Repo, error) { add("first") })
	first := <-posted
	l.Search(Query{Text: "qwen3", Sources: src}, func([]Repo, error) { add("second") })
	first() // runs on the UI thread after the second search began
	(<-posted)()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "second" {
		t.Errorf("delivered %v", got)
	}
}
