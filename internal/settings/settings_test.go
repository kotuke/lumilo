package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kotuke/lumilo/internal/engine"
)

func TestDefaults(t *testing.T) {
	d := Defaults("Mac")
	if d.Access != engine.AccessLocal || d.Port != 8000 || d.IdleMinutes != 10 ||
		d.ModelUpdates || d.Language != "" || d.Terminal != "com.apple.Terminal" || !d.OpenAtLogin || d.StillIcon ||
		d.Engine != "splash" || len(d.Models) != 0 || len(d.Contexts) != 0 || len(d.Options) != 0 {
		t.Errorf("defaults: %+v", d)
	}
	if !reflect.DeepEqual(d.AllowedHosts, []string{"Mac.local"}) {
		t.Errorf("hosts: %v", d.AllowedHosts)
	}
	if h := Defaults("").AllowedHosts; len(h) != 0 {
		t.Errorf("no host name must give no hosts, got %v", h)
	}
}

// A file from before the engine choice runs Splash with its model; the
// old key goes away on the next save.
func TestLegacyModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{"model": "a/b:c", "contexts": {"x": 0, "a/b:c": 32768}}`), 0o600)
	got, err := Load(path, Defaults("Mac"))
	if err != nil || got.Engine != "splash" || got.Model("splash") != "a/b:c" || got.LegacyModel != "" ||
		got.Context("splash", "a/b:c") != 32768 || len(got.Contexts["splash"]) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := Save(path, got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"model"`) || strings.Contains(string(b), `"contexts"`) {
		t.Errorf("old key kept: %s", b)
	}
}

func TestContextAndModel(t *testing.T) {
	var s Settings
	s.SetModel("llamacpp", "/m/x.gguf")
	s.SetContext("llamacpp", "/m/x.gguf", 131072)
	if s.Model("llamacpp") != "/m/x.gguf" || s.Model("splash") != "" || s.Context("llamacpp", "/m/x.gguf") != 131072 {
		t.Errorf("%+v", s)
	}
	s.SetContext("llamacpp", "/m/x.gguf", 0)
	if s.Context("llamacpp", "/m/x.gguf") != 0 || len(s.Contexts) != 0 {
		t.Errorf("auto kept: %+v", s.Contexts)
	}
}

// Two engines may name a model alike: the context and the options chosen
// for one must not reach the other.
func TestChoicesPerEngine(t *testing.T) {
	var s Settings
	s.SetContext("splash", "m/x", 65536)
	s.SetOptions("splash", "m/x", engine.ModelOptions{NoVision: true})
	if n := s.Context("llamacpp", "m/x"); n != 0 {
		t.Errorf("llama.cpp got splash's context %d", n)
	}
	if o := s.ModelOptions("llamacpp", "m/x"); o != (engine.ModelOptions{}) {
		t.Errorf("llama.cpp got splash's options %+v", o)
	}
	if s.Context("splash", "m/x") != 65536 || !s.ModelOptions("splash", "m/x").NoVision {
		t.Errorf("splash lost its choices: %+v", s)
	}
}

// A deleted model takes its choices with it, of its engine only.
func TestForgetModel(t *testing.T) {
	var s Settings
	for _, e := range []string{"splash", "llamacpp"} {
		s.SetModel(e, "m/x")
		s.SetContext(e, "m/x", 65536)
		s.SetOptions(e, "m/x", engine.ModelOptions{NoVision: true})
	}
	s.SetContext("splash", "m/y", 8192)
	s.ForgetModel("splash", "m/x")
	if s.Model("splash") != "" || s.Context("splash", "m/x") != 0 || s.ModelOptions("splash", "m/x").NoVision {
		t.Errorf("splash kept m/x: %+v", s)
	}
	if s.Context("splash", "m/y") != 8192 || s.Model("llamacpp") != "m/x" || s.Context("llamacpp", "m/x") != 65536 {
		t.Errorf("other choices went: %+v", s)
	}
	s.ForgetModel("splash", "m/y")
	if len(s.Contexts["splash"]) != 0 {
		t.Errorf("contexts %v", s.Contexts)
	}
}

// Files that kept contexts and options by model alone: each goes to the
// engine whose chosen model it is, the rest to the default engine.
func TestLegacyChoicesMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{
		"models": {"llamacpp": "/m/x.gguf", "splash": "a/b:c"},
		"contexts": {"/m/x.gguf": 8192, "a/b:c": 32768, "o/old:q": 4096, "z": 0},
		"model_options": {"/m/x.gguf": {"no_vision": true}, "o/old:q": {"draft": "off"}}
	}`), 0o600)
	got, err := Load(path, Defaults("Mac"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]int{
		"llamacpp": {"/m/x.gguf": 8192},
		"splash":   {"a/b:c": 32768, "o/old:q": 4096},
	}
	if !reflect.DeepEqual(got.Contexts, want) {
		t.Errorf("contexts %v", got.Contexts)
	}
	wantOpt := map[string]map[string]engine.ModelOptions{
		"llamacpp": {"/m/x.gguf": {NoVision: true}},
		"splash":   {"o/old:q": {Draft: "off"}},
	}
	if !reflect.DeepEqual(got.Options, wantOpt) {
		t.Errorf("options %v", got.Options)
	}
	if got.LegacyContexts != nil || got.LegacyOptions != nil {
		t.Errorf("legacy kept: %v %v", got.LegacyContexts, got.LegacyOptions)
	}
	if err := Save(path, got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"contexts"`) || strings.Contains(string(b), `"model_options"`) {
		t.Errorf("legacy keys written: %s", b)
	}
	again, err := Load(path, Defaults("Mac"))
	if err != nil || !reflect.DeepEqual(again.Contexts, want) || !reflect.DeepEqual(again.Options, wantOpt) {
		t.Errorf("round trip: %+v, %v", again, err)
	}
}

// Clone shares nothing: changing the clone leaves the original alone.
func TestCloneIsDeep(t *testing.T) {
	var s Settings
	s.SetModel("splash", "a")
	s.SetContext("splash", "a", 8192)
	s.SetOptions("splash", "a", engine.ModelOptions{Draft: "off"})
	s.AllowedHosts = []string{"h"}
	c := s.Clone()
	c.SetModel("splash", "b")
	c.SetContext("splash", "a", 4096)
	c.SetOptions("splash", "a", engine.ModelOptions{NoVision: true})
	c.AllowedHosts[0] = "x"
	if s.Model("splash") != "a" || s.Context("splash", "a") != 8192 ||
		s.ModelOptions("splash", "a") != (engine.ModelOptions{Draft: "off"}) || s.AllowedHosts[0] != "h" {
		t.Errorf("original changed: %+v", s)
	}
}

func TestMissingFileGivesDefaults(t *testing.T) {
	def := Defaults("Mac")
	got, err := Load(filepath.Join(t.TempDir(), "none.json"), def)
	if err != nil || !reflect.DeepEqual(got, def) {
		t.Errorf("got %+v, %v", got, err)
	}
}

// Zero values must survive a save and a load: "never", off, no hosts.
func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	want := Settings{
		Access: engine.AccessNetwork, Port: 9100, IdleMinutes: 0, ModelUpdates: true,
		Language: "de", OpenAtLogin: false, StillIcon: true,
		AllowedHosts: []string{}, LoginApplied: true, Terminal: "com.mitchellh.ghostty",
		Engine:   "llamacpp",
		Models:   map[string]string{"splash": "a/b:c", "llamacpp": "/m/x.gguf"},
		Contexts: map[string]map[string]int{"splash": {"a/b:c": 65536}},
		Options:  map[string]map[string]engine.ModelOptions{"llamacpp": {"/m/x.gguf": {Draft: "off"}}},
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, Defaults("Mac"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
}

// Files from before the terminal choice, and files with an empty value,
// get the default terminal.
func TestOldFileGetsDefaultTerminal(t *testing.T) {
	for _, body := range []string{`{"port": 8123}`, `{"terminal": ""}`} {
		path := filepath.Join(t.TempDir(), "s.json")
		os.WriteFile(path, []byte(body), 0o600)
		got, err := Load(path, Defaults("Mac"))
		if err != nil || got.Terminal != DefaultTerminal {
			t.Errorf("%s: %q, %v", body, got.Terminal, err)
		}
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{"port": 8123}`), 0o600)
	def := Defaults("Mac")
	got, err := Load(path, def)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8123 || got.IdleMinutes != 10 || !got.OpenAtLogin || len(got.AllowedHosts) != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestInvalidValuesFallBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{"access": -1, "port": 70000, "idle_release_minutes": -3}`), 0o600)
	def := Defaults("")
	got, err := Load(path, def)
	if err != nil || got.Access != def.Access || got.Port != 8000 || got.IdleMinutes != 10 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestDamagedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{not json`), 0o600)
	def := Defaults("Mac")
	got, err := Load(path, def)
	if err == nil || !reflect.DeepEqual(got, def) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestFileHasNoKey(t *testing.T) {
	// The type has no key field; the file must not mention one.
	path := filepath.Join(t.TempDir(), "s.json")
	if err := Save(path, Defaults("Mac")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(strings.ToLower(string(b)), "key") {
		t.Errorf("file mentions a key: %s", b)
	}
}

func TestIdleRelease(t *testing.T) {
	var s Settings
	s.SetIdleRelease(30 * 60e9)
	if s.IdleMinutes != 30 || s.IdleRelease().Minutes() != 30 {
		t.Errorf("%+v", s)
	}
}

// The access setting had three modes: 0 this computer, 1 local network,
// 2 all addresses. Saved files of the old modes must keep the server
// reachable from the network and must not fall back to the computer only.
func TestOldAccessModesLoadAsNetwork(t *testing.T) {
	for in, want := range map[string]engine.Access{
		`{"access": 0}`: engine.AccessLocal,
		`{"access": 1}`: engine.AccessNetwork, // old local network
		`{"access": 2}`: engine.AccessNetwork, // old all addresses
		`{"access": 3}`: engine.AccessLocal,   // damage: the default
	} {
		path := filepath.Join(t.TempDir(), "s.json")
		os.WriteFile(path, []byte(in), 0o600)
		got, err := Load(path, Defaults("Mac"))
		if err != nil || got.Access != want {
			t.Errorf("%s: access %d, %v; want %d", in, got.Access, err, want)
		}
	}
}

// The icon moves unless asked not to: a file of a version before 0.4.2
// holds "animate_icon": false only because that was the default, and it
// is not read.
func TestIconMovesByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"animate_icon": false, "port": 8000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, Defaults("Mac"))
	if err != nil {
		t.Fatal(err)
	}
	if got.StillIcon {
		t.Error("an old animate_icon false keeps the icon still")
	}
	if Defaults("Mac").StillIcon {
		t.Error("a fresh install keeps the icon still")
	}
}
