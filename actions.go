package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/hf"
	"github.com/kotuke/lumilo/internal/panel"
	"github.com/kotuke/lumilo/internal/settings"
	"github.com/kotuke/lumilo/internal/terminal"
)

// app implements panel.Actions: engine commands go to the supervisor, the
// rest to the system.
var _ panel.Actions = (*app)(nil)

// Commands that may start a server wait for the API key: loadSystem reads
// it off the main thread at startup (afterKey).
func (a *app) Start()             { a.afterKey(a.sup.Start) }
func (a *app) Stop()              { a.sup.Stop() }
func (a *app) ForceStop()         { a.sup.ForceStop() }
func (a *app) Restart()           { a.afterKey(a.sup.Restart) }
func (a *app) KillExternal()      { a.sup.KillExternal() }
func (a *app) Release()           { a.sup.Release() }
func (a *app) Restore()           { a.sup.Restore() }
func (a *app) Install()           { a.sup.Install() }
func (a *app) Update()            { a.sup.Update() }
func (a *app) CheckUpdate()       { a.sup.CheckUpdate() }
func (a *app) RunAgent(id string) { a.afterKey(func() { a.sup.RunAgent(id) }) }
func (a *app) Recheck()           { a.sup.Recheck() }
func (a *app) Quit()              { mygo.App.Quit() }

// ToggleTheme switches the panel to the other theme and remembers the
// choice, as Kvoto does; until then the panel follows the system.
func (a *app) ToggleTheme() {
	src := mygo.ThemeDark
	if mygo.Theme.IsDark() {
		src = mygo.ThemeLight
	}
	mygo.Theme.SetSource(src)
	a.update(func(s *settings.Settings) { s.Theme = string(src) })
	a.win.Invalidate()
}
func (a *app) Copy(text string)   { mygo.Clipboard.WriteText(text) }
func (a *app) OpenURL(url string) { openURL(url) }
func (a *app) OpenChat()          { openURL(a.sup.View().ChatURL) }
func (a *app) OpenLog() {
	if p := a.sup.View().LogPath; p != "" {
		if err := mygo.Shell.OpenPath(p); err != nil {
			log.Println("open log:", err)
		}
	}
}

// StartOn starts on another port for this run only. The saved port stays
// the user's: the busy one is tried again next time, and the settings screen
// keeps showing it.
func (a *app) StartOn(port int) { a.afterKey(func() { a.sup.StartOn(port) }) }

// SelectModel remembers the user's choice. A model that the supervisor
// picks by itself is not stored.
func (a *app) SelectModel(id string) {
	eng := a.sup.View().EngineID
	a.update(func(s *settings.Settings) { s.SetModel(eng, id) })
	a.sup.SelectModel(id)
}

// Download downloads a model apart from the server, or, for an engine
// that cannot, starts the server with it (onModel stores it once it
// serves). A download for text only stores that choice for the model, and
// the supervisor gets it with the request, so the order of the two does
// not matter.
func (a *app) Download(id string, o engine.ModelOptions) {
	if o.NoVision {
		a.setNoVision(id)
	}
	a.afterKey(func() { a.sup.DownloadModel(id, o) })
}

// CancelDownload stops a download apart from the server.
func (a *app) CancelDownload(id string) { a.sup.CancelDownload(id) }

// DismissDownload removes a failed or finished download from the list.
func (a *app) DismissDownload(id string) { a.sup.DismissDownload(id) }

// PauseDownload stops a download apart from the server, keeping its files.
func (a *app) PauseDownload(id string) { a.sup.PauseDownload(id) }

// PlanRemove counts what deleting an installed model frees, for the
// confirmation; CancelRemove forgets the count; RemoveModel deletes the
// model (onRemove forgets its settings).
func (a *app) PlanRemove(id string)  { a.sup.PlanRemove(id) }
func (a *app) CancelRemove()         { a.sup.CancelRemove() }
func (a *app) RemoveModel(id string) { a.sup.RemoveModel(id) }

// ServeTextOnly turns image input off for the model and starts the server:
// the supervisor stores the choice on its worker before the Start queued
// after it, which a.applyCore, running on its own goroutine, would not
// promise.
func (a *app) ServeTextOnly(id string) {
	o := a.setNoVision(id)
	a.sup.SetModelOptions(id, o)
	a.afterKey(a.sup.Start)
}

// setNoVision stores image input off for the model id of the running
// engine, shows it in the panel and returns the model's options.
func (a *app) setNoVision(id string) engine.ModelOptions {
	eng := a.sup.View().EngineID
	var o engine.ModelOptions
	a.update(func(s *settings.Settings) {
		o = s.Options[eng][id]
		o.NoVision = true
		s.SetOptions(eng, id, o)
	})
	a.panel.SetPrefs(a.prefs())
	return o
}

// ParseModel checks a typed model name in the running engine's syntax.
func (a *app) ParseModel(spec string) (engine.Model, error) { return a.sup.ParseModel(spec) }

// SearchModels looks up Hugging Face repositories the running engine can run.
// The answer goes to the panel on the main thread; a search that did not
// reach the hub says so (hubUnreachable), any other failure answers with
// nothing found. Adding a model is an online act whatever "check for model
// updates" says: the download is.
func (a *app) SearchModels(text string) {
	var src []hf.Source
	for _, s := range a.sup.ModelSearch().Sources {
		src = append(src, hf.Source{Filter: s.Filter, Architectures: s.Architectures})
	}
	a.look.Search(hf.Query{Text: text, Sources: src, Limit: 8}, func(repos []hf.Repo, err error) {
		if err != nil {
			log.Println("model search:", err)
			if hubUnreachable(err) {
				a.panel.SetModelSearchFailed(text)
				return
			}
			repos = nil
		}
		a.panel.SetModelHits(text, repos)
	})
}

// ModelVariants looks at a repository with the engine's quantization rules:
// the GGUF variants it runs, or whether it runs an MLX checkpoint.
func (a *app) ModelVariants(repo string) {
	spec := a.sup.ModelSearch()
	rules := hf.Rules{RefuseGGUF: spec.RefuseGGUF}
	if q := spec.MLX; q != nil {
		rules.MLX = &hf.MLXRules{Bits: q.Bits, Groups: q.Groups, Modes: q.Modes}
	}
	a.look.Repo(repo, rules, func(info hf.RepoInfo, err error) {
		if err != nil {
			log.Println("model variants:", err)
			if hubUnreachable(err) {
				a.panel.SetModelInfoFailed(repo)
				return
			}
			info = hf.RepoInfo{ID: repo} // the hub answered: nothing to run there
		}
		a.panel.SetModelInfo(repo, info)
	})
}

// hubUnreachable reports whether a lookup failed before the hub answered
// (no network, a timeout) or with the hub's own failure (5xx): the panel
// says Hugging Face is unreachable rather than that nothing was found,
// which an answer such as 404 means.
func hubUnreachable(err error) bool {
	var se *hf.StatusError
	if errors.As(err, &se) {
		return se.Code >= 500
	}
	return true
}

// CancelModelSearch drops the lookups still running.
func (a *app) CancelModelSearch() { a.look.Cancel() }

// SwitchEngine remembers the engine and runs it with its own model. The
// panel asked before it stops our server.
func (a *app) SwitchEngine(id string) {
	var model string
	a.update(func(s *settings.Settings) {
		s.Engine = id
		model = s.Model(id)
	})
	a.sup.SwitchEngine(id, model)
	a.panel.SetPrefs(a.prefs()) // the contexts and options of the new engine
}

// NewAPIKey returns "sk-" and 32 random hex characters.
func (a *app) NewAPIKey() string { return newAPIKey() }

func newAPIKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system random source failed
	}
	return "sk-" + hex.EncodeToString(b[:])
}

// PortFree reports whether 127.0.0.1:port can be bound.
func (a *app) PortFree(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// SetPrefs stores the settings, hands them to the supervisor and applies
// the ones that act at once. It returns at once: the file, the Keychain and
// the supervisor are written in the background. Never log p: it holds the
// API key.
func (a *app) SetPrefs(p panel.Prefs) {
	old, keyChanged := a.takePrefs(p)
	a.persistAsync()
	go a.applyCore()
	if keyChanged {
		go a.storeKey()
	}

	a.mu.Lock()
	lang, login := a.st.Language, a.st.OpenAtLogin
	a.mu.Unlock()
	if lang != old.Language {
		mygo.App.SetLocale(lang) // redraws the panel and calls OnLocaleChanged
	}
	if login != old.OpenAtLogin {
		go a.setLogin(login)
	}
	a.drawTray() // the animation and value settings
	a.panel.SetPrefs(a.prefs())
}

// takePrefs stores the panel's settings in a.st and a.key and returns the
// settings as they were. The contexts and options are those of the engine
// being run. A key the panel made while the Keychain's key is unknown (not
// read yet, or unreadable) is dropped: it would replace a key that exists.
func (a *app) takePrefs(p panel.Prefs) (old settings.Settings, keyChanged bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old = a.st.Clone()
	if p.APIKey != a.key {
		if a.keyState == keyOK {
			keyChanged = true
			if a.key != "" {
				// A running server may still use the old key: the agent of
				// that server gets it (openTerminal), and the terminal
				// script never holds it.
				a.retired = append(a.retired, a.key)
			}
			a.key, a.keyStored = p.APIKey, false
		} else {
			log.Println("keychain: the stored API key is unknown; a new one is not kept")
		}
	}
	a.st.Access = p.Access
	a.st.Port = p.Port
	a.st.SetIdleRelease(p.IdleRelease)
	a.st.ModelUpdates = p.ModelUpdates
	a.st.AllowedHosts = append([]string(nil), p.AllowedHosts...)
	a.st.Language = p.Language
	a.st.Terminal = p.Terminal
	a.st.OpenAtLogin = p.OpenAtLogin
	a.st.AnimateIcon = p.AnimateIcon
	a.st.IconOnly = !p.TrayValue
	a.st.ManualUpdates = !p.AppUpdates
	eng := a.st.Engine
	delete(a.st.Contexts, eng)
	for id, n := range p.Contexts {
		a.st.SetContext(eng, id, n)
	}
	delete(a.st.Options, eng)
	for id, o := range p.Options {
		a.st.SetOptions(eng, id, o)
	}
	return old, keyChanged
}

// storeKey keeps the current key in the Keychain, or removes the item when
// the key is empty. Calls run one at a time and each stores the key that
// is current in its turn, so the newest key lands last.
func (a *app) storeKey() {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	a.mu.Lock()
	key, stored, store := a.key, a.keyStored, a.secrets
	a.mu.Unlock()
	if stored || store == nil {
		return // an earlier call stored it already
	}
	var err error
	if key == "" {
		err = store.Delete(secretAccount)
		if errors.Is(err, mygo.ErrSecretNotFound) {
			err = nil
		}
	} else {
		err = store.Set(secretAccount, []byte(key))
	}
	a.mu.Lock()
	if a.key == key { // not replaced meanwhile
		a.keyStored = err == nil
	}
	a.mu.Unlock()
	if err != nil {
		log.Println("keychain:", err)
	}
}

// runningAccount is the Keychain account that holds the key of a running
// server that is older than the current one, for its agents.
const runningAccount = secretAccount + ".running"

// storeRunningKey puts key in the runningAccount item.
func (a *app) storeRunningKey(key string) error {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	a.mu.Lock()
	store := a.secrets
	a.mu.Unlock()
	if store == nil {
		return core.ErrKeyNotStored
	}
	return store.Set(runningAccount, []byte(key))
}

// setLogin switches the login item; it may block on the system.
func (a *app) setLogin(on bool) {
	a.mu.Lock()
	dev := a.dev
	a.mu.Unlock()
	if dev {
		return // see applyLoginFirstRun
	}
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		log.Println("login item:", err)
	}
}

// installedTerminals lists the terminals the settings offer.
func installedTerminals() []panel.TerminalChoice {
	var out []panel.TerminalChoice
	for _, t := range terminal.Installed() {
		out = append(out, panel.TerminalChoice{ID: string(t), Name: t.Name()})
	}
	return out
}

// openTerminal runs an agent command in the chosen terminal. The API key
// reaches the script as a Keychain lookup, never as text. The agent gets
// the key of the server it talks to (the supervisor puts it in env): the
// current key, read from its own item, or an older one a running server
// still uses, put in runningAccount first.
func (a *app) openTerminal(argv, env []string) error {
	<-a.keyReady
	a.mu.Lock()
	choice := terminal.App(a.st.Terminal)
	a.mu.Unlock()
	secrets, err := a.agentSecrets(argv, env)
	if err != nil {
		return err
	}
	return terminal.Open(choice, argv, env, secrets...)
}

// agentSecrets are the keys in argv and env, each with the Keychain item
// that holds it when the script runs.
func (a *app) agentSecrets(argv, env []string) ([]terminal.Secret, error) {
	a.mu.Lock()
	cur, stored := a.key, a.keyStored
	retired := slices.Clone(a.retired)
	a.mu.Unlock()
	used := func(v string) bool {
		if v == "" {
			return false
		}
		for _, s := range slices.Concat(argv, env) {
			if strings.Contains(s, v) {
				return true
			}
		}
		return false
	}
	var out []terminal.Secret
	if used(cur) {
		if !stored {
			return nil, fmt.Errorf("agent: %w", core.ErrKeyNotStored)
		}
		out = append(out, terminal.Secret{Value: cur, Account: secretAccount})
	}
	for _, k := range retired {
		if k == cur || !used(k) {
			continue
		}
		if err := a.storeRunningKey(k); err != nil {
			log.Println("keychain:", err)
			return nil, fmt.Errorf("agent: the running server's key: %w", core.ErrKeyNotStored)
		}
		out = append(out, terminal.Secret{Value: k, Account: runningAccount})
		break // one server, one key
	}
	return out, nil
}

func openURL(url string) {
	if url == "" {
		return
	}
	if err := mygo.Shell.OpenExternal(url); err != nil {
		log.Println("open url:", err)
	}
}
