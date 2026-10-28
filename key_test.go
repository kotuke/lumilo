package main

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/panel"
	"github.com/kotuke/lumilo/internal/settings"
	"github.com/kotuke/lumilo/internal/terminal"
)

func TestServerKey(t *testing.T) {
	if got := serverKey(engine.AccessLocal, "sk-1"); got != "" {
		t.Errorf("this computer: key %q, want none", got)
	}
	if got := serverKey(engine.AccessNetwork, "sk-1"); got != "sk-1" {
		t.Errorf("network: key %q", got)
	}
}

// fakeSecrets is a Keychain in memory.
type fakeSecrets struct {
	mu    sync.Mutex
	items map[string]string
	sets  int
	delay time.Duration // each Set takes this long
}

func (f *fakeSecrets) Get(k string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.items[k]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return []byte(v), nil
}

func (f *fakeSecrets) Set(k string, v []byte) error {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items = map[string]string{}
	}
	f.items[k] = string(v)
	f.sets++
	return nil
}

func (f *fakeSecrets) Delete(k string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, k)
	return nil
}

func (f *fakeSecrets) item(k string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[k]
}

func testApp(t *testing.T) (*app, *fakeSecrets) {
	t.Helper()
	sec := &fakeSecrets{}
	a := &app{keyReady: make(chan struct{}), secrets: sec, path: filepath.Join(t.TempDir(), "settings.json")}
	a.st = settings.Defaults("Mac")
	close(a.keyReady)
	return a, sec
}

// A Keychain item that cannot be read is never replaced: no key is made,
// a key the panel makes is dropped, nothing is written, and the network
// access has no key to serve with (the supervisor refuses: FailNoKey).
func TestUnreadableKeyIsNeverReplaced(t *testing.T) {
	a, sec := testApp(t)
	a.st.Access = engine.AccessNetwork
	if a.takeKeyLocked(nil, errors.New("user canceled")) {
		t.Error("made a key over an unreadable one")
	}
	if a.key != "" || a.keyState != keyUnreadable {
		t.Errorf("key %q state %d", a.key, a.keyState)
	}
	p := a.prefs()
	p.APIKey = "sk-made-by-panel"
	if _, changed := a.takePrefs(p); changed {
		t.Error("the panel's key was taken")
	}
	a.storeKey()
	if sec.sets != 0 || a.key != "" {
		t.Errorf("keychain written %d times, key %q", sec.sets, a.key)
	}
	if k := a.coreSettings("splash").APIKey; k != "" {
		t.Errorf("server key %q", k)
	}
}

// No key at all in the network access (a migrated three-mode file): a new
// one is made and stored.
func TestMissingKeyOnNetworkIsMade(t *testing.T) {
	a, sec := testApp(t)
	a.st.Access = engine.AccessNetwork
	if !a.takeKeyLocked(nil, fmt.Errorf("get: %w", mygo.ErrSecretNotFound)) {
		t.Fatal("no key made")
	}
	a.storeKey()
	if a.key == "" || sec.item(secretAccount) != a.key || !a.keyStored {
		t.Errorf("key %q stored %q (%v)", a.key, sec.item(secretAccount), a.keyStored)
	}
	// On this computer none is needed.
	b, _ := testApp(t)
	if b.takeKeyLocked(nil, mygo.ErrSecretNotFound) || b.key != "" || b.keyState != keyOK {
		t.Errorf("local: key %q state %d", b.key, b.keyState)
	}
}

// Keys stored one after another end with the newest one in the Keychain.
func TestStoreKeyNewestWins(t *testing.T) {
	a, sec := testApp(t)
	sec.delay = 2 * time.Millisecond
	a.takeKeyLocked([]byte("sk-0"), nil)
	var wg sync.WaitGroup
	for i := 1; i <= 10; i++ {
		p := a.prefs()
		p.APIKey = fmt.Sprintf("sk-%d", i)
		if _, changed := a.takePrefs(p); !changed {
			t.Fatal("key not taken")
		}
		wg.Add(1)
		go func() { defer wg.Done(); a.storeKey() }()
	}
	wg.Wait()
	if got := sec.item(secretAccount); got != "sk-10" || !a.keyStored {
		t.Errorf("keychain %q, stored %v", got, a.keyStored)
	}
}

// The agent gets the key of the server it talks to: a server started
// before the key changed still uses the older key, which goes to an item
// of its own; the current key is read from the usual one.
func TestAgentGetsRunningServersKey(t *testing.T) {
	a, sec := testApp(t)
	a.takeKeyLocked([]byte("sk-old"), nil)
	p := a.prefs()
	p.APIKey = "sk-new"
	a.takePrefs(p)
	a.storeKey()

	got, err := a.agentSecrets([]string{"/bin/agent"}, []string{"KEY=sk-old"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []terminal.Secret{{Value: "sk-old", Account: runningAccount}}; !reflect.DeepEqual(got, want) {
		t.Errorf("secrets %+v", got)
	}
	if sec.item(runningAccount) != "sk-old" || sec.item(secretAccount) != "sk-new" {
		t.Errorf("keychain %v", sec.items)
	}

	got, err = a.agentSecrets([]string{"/bin/agent"}, []string{"KEY=sk-new"})
	if want := []terminal.Secret{{Value: "sk-new", Account: secretAccount}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("secrets %+v, %v", got, err)
	}

	// A current key not in the Keychain yet is refused, not guessed.
	p.APIKey = "sk-newer"
	a.takePrefs(p)
	if _, err := a.agentSecrets(nil, []string{"KEY=sk-newer"}); !errors.Is(err, core.ErrKeyNotStored) {
		t.Errorf("err %v", err)
	}
}

// The panel edits the contexts and options of the engine being run; the
// other engine's stay as they were.
func TestPrefsPerEngine(t *testing.T) {
	a, _ := testApp(t)
	a.takeKeyLocked(nil, mygo.ErrSecretNotFound)
	a.st.Engine = "llamacpp"
	a.st.SetContext("splash", "m", 65536)
	a.st.SetContext("llamacpp", "m", 4096)
	p := a.prefs()
	if !maps.Equal(p.Contexts, map[string]int{"m": 4096}) {
		t.Errorf("panel contexts %v", p.Contexts)
	}
	p.Contexts = map[string]int{"m": 8192}
	p.Options = map[string]engine.ModelOptions{"m": {NoVision: true}}
	a.takePrefs(p)
	if a.st.Context("splash", "m") != 65536 || a.st.Context("llamacpp", "m") != 8192 ||
		!a.st.ModelOptions("llamacpp", "m").NoVision || a.st.ModelOptions("splash", "m").NoVision {
		t.Errorf("settings %+v %+v", a.st.Contexts, a.st.Options)
	}
	ctx, opt := coreChoices(a.st)
	if ctx[core.ModelKey{Engine: "splash", Model: "m"}] != 65536 || ctx[core.ModelKey{Engine: "llamacpp", Model: "m"}] != 8192 ||
		!opt[core.ModelKey{Engine: "llamacpp", Model: "m"}].NoVision || len(opt) != 1 {
		t.Errorf("core %v %v", ctx, opt)
	}
}

// Settings changed from several goroutines are saved without a data race
// (go test -race), and the file ends with the newest state.
func TestUpdateConcurrentSaves(t *testing.T) {
	a, _ := testApp(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.update(func(s *settings.Settings) {
				s.SetModel("splash", fmt.Sprint("m", i))
				s.SetContext("splash", fmt.Sprint("m", i), 1024*(i+1))
				s.SetOptions("llamacpp", fmt.Sprint("m", i), engine.ModelOptions{Draft: "off"})
				s.AllowedHosts = append(s.AllowedHosts, fmt.Sprint("h", i))
			})
		}()
	}
	p := a.prefs()
	p.Contexts = map[string]int{"x": 2048}
	a.SetPrefsForTest(p)
	wg.Wait()
	a.saves.Wait()
	got, err := settings.Load(a.path, settings.Defaults(""))
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	want := a.st.Clone()
	a.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file %+v\nwant %+v", got, want)
	}
}

// SetPrefsForTest is the part of SetPrefs that needs no window: take the
// settings and save them.
func (a *app) SetPrefsForTest(p panel.Prefs) {
	a.takePrefs(p)
	a.persistAsync()
}
