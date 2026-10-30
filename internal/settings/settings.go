// Package settings stores the user's choices as a JSON file. The API key
// is not part of it: the app keeps the key in the system Keychain.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

// Defaults for a fresh install.
const (
	DefaultPort        = 8000
	DefaultIdleRelease = 10 * time.Minute
)

// legacyAccessAll is the saved value of the "all addresses" mode of the
// three-mode settings.
const legacyAccessAll engine.Access = 2

// DefaultTerminal is the bundle id of Terminal, the terminal of a fresh
// install; files written before the choice existed get it too.
const DefaultTerminal = "com.apple.Terminal"

// Settings are the persisted choices. Field tags are the file format; no
// field uses omitempty, so zero values ("never", empty list, off) survive
// a round trip.
type Settings struct {
	Access       engine.Access `json:"access"` // 0 this computer, 1 network; see normalize
	Port         int           `json:"port"`
	IdleMinutes  int           `json:"idle_release_minutes"` // 0 = never
	ModelUpdates bool          `json:"model_updates"`
	Language     string        `json:"language"` // "" = as in the system
	OpenAtLogin  bool          `json:"open_at_login"`
	AnimateIcon  bool          `json:"animate_icon"`
	// ManualUpdates turns the automatic download of new Lumilo versions
	// off; absent, they download by themselves.
	ManualUpdates bool     `json:"manual_updates,omitempty"`
	Theme         string   `json:"theme,omitempty"` // "" = as in the system, "light" or "dark" once chosen in the panel
	Terminal      string   `json:"terminal"`        // bundle id of the terminal that runs agents
	AllowedHosts  []string `json:"allowed_hosts"`
	// LoginApplied is set once the login item was set for the first run.
	LoginApplied bool `json:"login_applied"`

	// Engine is the ID of the engine Lumilo runs, e.g. "splash".
	Engine string `json:"engine"`
	// Models is the model chosen for each engine, by engine ID; a missing
	// one means the supervisor picks.
	Models map[string]string `json:"models"`
	// Contexts is the context window chosen for each model, in tokens, by
	// engine ID and then model ID: two engines may name a model alike and
	// still need their own choices. A missing one means the engine's choice.
	Contexts map[string]map[string]int `json:"engine_contexts"`
	// Options are the vision and draft choices of each model, by engine ID
	// and then model ID; a missing one means the engine's defaults.
	Options map[string]map[string]engine.ModelOptions `json:"engine_model_options,omitempty"`
	// LegacyContexts and LegacyOptions are the choices of files written
	// when they were kept by model ID alone. Load moves them under the
	// engine that runs the model (Models), else under DefaultEngine.
	LegacyContexts map[string]int                 `json:"contexts,omitempty"`
	LegacyOptions  map[string]engine.ModelOptions `json:"model_options,omitempty"`
	// LegacyModel is the model of files written before there were several
	// engines: Splash's. Load moves it into Models.
	LegacyModel string `json:"model,omitempty"`
}

// DefaultEngine is the engine of a fresh install and of older files.
const DefaultEngine = "splash"

// Model is the model chosen for the engine, "" when none was.
func (s Settings) Model(engineID string) string { return s.Models[engineID] }

// SetModel remembers the model chosen for the engine.
func (s *Settings) SetModel(engineID, id string) {
	if s.Models == nil {
		s.Models = map[string]string{}
	}
	s.Models[engineID] = id
}

// Context is the context window chosen for the engine's model; 0 =
// automatic.
func (s Settings) Context(engineID, model string) int { return s.Contexts[engineID][model] }

// SetContext remembers the context window for the engine's model; 0
// forgets it.
func (s *Settings) SetContext(engineID, model string, tokens int) {
	if tokens <= 0 {
		delete(s.Contexts[engineID], model)
		if len(s.Contexts[engineID]) == 0 {
			delete(s.Contexts, engineID)
		}
		return
	}
	if s.Contexts == nil {
		s.Contexts = map[string]map[string]int{}
	}
	if s.Contexts[engineID] == nil {
		s.Contexts[engineID] = map[string]int{}
	}
	s.Contexts[engineID][model] = tokens
}

// ModelOptions are the vision and draft choices of the engine's model.
func (s Settings) ModelOptions(engineID, model string) engine.ModelOptions {
	return s.Options[engineID][model]
}

// SetOptions remembers the vision and draft choices of the engine's model;
// the defaults forget them.
func (s *Settings) SetOptions(engineID, model string, o engine.ModelOptions) {
	if o == (engine.ModelOptions{}) {
		delete(s.Options[engineID], model)
		if len(s.Options[engineID]) == 0 {
			delete(s.Options, engineID)
		}
		return
	}
	if s.Options == nil {
		s.Options = map[string]map[string]engine.ModelOptions{}
	}
	if s.Options[engineID] == nil {
		s.Options[engineID] = map[string]engine.ModelOptions{}
	}
	s.Options[engineID][model] = o
}

// ForgetModel drops what is stored for the engine's model id, deleted
// from the disk: its context window, its options and the choice of it.
func (s *Settings) ForgetModel(engineID, id string) {
	s.SetContext(engineID, id, 0)
	s.SetOptions(engineID, id, engine.ModelOptions{})
	if s.Models[engineID] == id {
		delete(s.Models, engineID)
	}
}

// Clone is a deep copy: it shares no map or slice with s, so one goroutine
// may save it while another changes s.
func (s Settings) Clone() Settings {
	c := s
	c.AllowedHosts = slices.Clone(s.AllowedHosts)
	c.Models = maps.Clone(s.Models)
	c.Contexts = cloneNested(s.Contexts)
	c.Options = cloneNested(s.Options)
	c.LegacyContexts = maps.Clone(s.LegacyContexts)
	c.LegacyOptions = maps.Clone(s.LegacyOptions)
	return c
}

func cloneNested[V any](m map[string]map[string]V) map[string]map[string]V {
	if m == nil {
		return nil
	}
	c := make(map[string]map[string]V, len(m))
	for k, v := range m {
		c[k] = maps.Clone(v)
	}
	return c
}

// Defaults returns the settings of a fresh install. localHost is the
// computer's local host name ("MacBook"), "" when unknown.
func Defaults(localHost string) Settings {
	s := Settings{
		Access:      engine.AccessLocal,
		Port:        DefaultPort,
		IdleMinutes: int(DefaultIdleRelease / time.Minute),
		OpenAtLogin: true,
		Engine:      DefaultEngine,
		Terminal:    DefaultTerminal,
	}
	if localHost != "" {
		s.AllowedHosts = []string{localHost + ".local"}
	}
	return s
}

// IdleRelease is the idle-unload time; 0 = never.
func (s Settings) IdleRelease() time.Duration { return time.Duration(s.IdleMinutes) * time.Minute }

// SetIdleRelease sets the idle-unload time, rounded down to minutes.
func (s *Settings) SetIdleRelease(d time.Duration) { s.IdleMinutes = max(0, int(d/time.Minute)) }

// Load reads the file over def. A missing file gives def and no error. A
// damaged file gives def and an error. Invalid values fall back to def.
func Load(path string, def Settings) (Settings, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return def.Clone(), nil
	}
	if err != nil {
		return def.Clone(), err
	}
	s := def.Clone()
	if err := json.Unmarshal(b, &s); err != nil {
		return def.Clone(), fmt.Errorf("settings: %s: %w", path, err)
	}
	s.normalize(def)
	return s, nil
}

// normalize replaces values that cannot be used with those of def.
func (s *Settings) normalize(def Settings) {
	// Files written when there were three modes hold 0 this computer,
	// 1 local network, 2 all addresses. Both open modes are the network
	// access now: 1 already has that value, 2 is moved to it. Anything else
	// is damage and falls back to def.
	switch s.Access {
	case engine.AccessLocal, engine.AccessNetwork:
	case legacyAccessAll:
		s.Access = engine.AccessNetwork
	default:
		s.Access = def.Access
	}
	if s.Port < 1 || s.Port > 65535 {
		s.Port = def.Port
	}
	if s.IdleMinutes < 0 {
		s.IdleMinutes = def.IdleMinutes
	}
	if s.Engine == "" {
		s.Engine = DefaultEngine // a file from before the engine choice
	}
	if s.LegacyModel != "" {
		if s.Model(DefaultEngine) == "" {
			s.SetModel(DefaultEngine, s.LegacyModel)
		}
		s.LegacyModel = ""
	}
	s.migrateChoices()
	for eng, m := range s.Options {
		for id, o := range m {
			if o == (engine.ModelOptions{}) {
				delete(m, id)
			}
		}
		if len(m) == 0 {
			delete(s.Options, eng)
		}
	}
	for eng, m := range s.Contexts {
		for id, n := range m {
			if n <= 0 {
				delete(m, id)
			}
		}
		if len(m) == 0 {
			delete(s.Contexts, eng)
		}
	}
	if s.Terminal == "" {
		s.Terminal = def.Terminal
	}
}

// migrateChoices moves the contexts and options kept by model ID alone
// under an engine: the one whose chosen model it is, else DefaultEngine.
// A choice the file already keeps under that engine wins.
func (s *Settings) migrateChoices() {
	owner := func(model string) string {
		for _, eng := range slices.Sorted(maps.Keys(s.Models)) {
			if s.Models[eng] == model {
				return eng
			}
		}
		return DefaultEngine
	}
	for id, n := range s.LegacyContexts {
		if eng := owner(id); s.Context(eng, id) == 0 {
			s.SetContext(eng, id, n)
		}
	}
	for id, o := range s.LegacyOptions {
		if eng := owner(id); s.ModelOptions(eng, id) == (engine.ModelOptions{}) {
			s.SetOptions(eng, id, o)
		}
	}
	s.LegacyContexts, s.LegacyOptions = nil, nil
}

// Save writes the settings atomically with owner-only permissions.
func Save(path string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
