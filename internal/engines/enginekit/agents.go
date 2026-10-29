package enginekit

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kotuke/lumilo/internal/engine"
)

// The agent logos are the colour icons of Lobe Icons
// (@lobehub/icons-static-svg 1.101.0, MIT; THIRD_PARTY_NOTICES).
//
//go:embed logos/logo-*.svg
var agentLogos embed.FS

// Agents are the coding agents Lumilo can point at a local server, in the
// order to offer them: Claude Code, Codex, OpenCode and Pi.
func Agents() []engine.Agent {
	return []engine.Agent{
		// Claude Code's own prompt and tools do not fit in 32K ("Prompt is
		// too long" from a 32,768-token server; fine at 131,072), and it
		// refuses an auto-compact window under 100k.
		{ID: "claude", Name: "Claude", Logo: MustFile(agentLogos, "logos/logo-claude.svg"), Color: "#D97757", MinContext: 100_000},
		{ID: "codex", Name: "Codex", Logo: MustFile(agentLogos, "logos/logo-codex.svg")},
		{ID: "opencode", Name: "OpenCode", Logo: MustFile(agentLogos, "logos/logo-opencode.svg")},
		{ID: "pi", Name: "Pi", Logo: MustFile(agentLogos, "logos/logo-pi.svg")},
	}
}

// AgentServer is the local server an agent is pointed at.
type AgentServer struct {
	Port int
	// Model is the name the server serves its model by (its /v1/models id).
	Model string
	// Context is the context window in tokens; 0 = not known
	// (defaultAgentContext is assumed).
	Context int
	// Vision: the server reads images.
	Vision bool
	// Name is the engine's name, for the agents that show their provider.
	Name string
	// FunctionToolsOnly: the server refuses a Responses tool other than a
	// function. Codex groups its subagent, MCP and app tools in
	// "namespace" tools, so it runs with a home of its own (CodexHome)
	// and its subagents off: TensorFold 1.1.9 answers them "tools of type
	// 'namespace' are not supported: this server runs function tools
	// only" (Codex 0.165, checked live 2026-10-29).
	FunctionToolsOnly bool
	// Efforts are the reasoning efforts the server takes, as /v1/models
	// lists them (TensorFold: "supported_efforts"); nil = not known, any.
	Efforts []string
}

// defaultAgentContext is the window assumed when the server's is not known.
const defaultAgentContext = 32768

// agentKeyVar is the variable that carries the server's API key to the
// agent: the agents read it from their environment, so it never goes into
// argv or a file (terminal.Open looks it up in the Keychain at run time).
const agentKeyVar = "LUMILO_API_KEY"

// agentOutput is the output allowance of one response for the agents that
// need one (OpenCode, Pi): a quarter of the window, at most 32,768.
func agentOutput(context int) int { return min(32768, max(1, context/4)) }

// AgentDir is Lumilo's folder for the agents: Pi's provider extensions and
// Codex's own home (CodexHome), never an agent's configuration. Tests
// replace it.
var AgentDir = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Lumilo", "agents")
}

// AgentCommand builds the command that starts agent against s, for
// Engine.AgentCommand; key is the server's API key, "" for none. None of
// the agents' own configuration files changes:
//
//   - claude: Claude Code with ANTHROPIC_BASE_URL at the server's
//     Anthropic Messages route, its model for every role, the cloud
//     providers turned off and ANTHROPIC_API_KEY unset; WebSearch, a tool
//     Anthropic's API runs, disallowed; the normal permission mode.
//   - codex: Codex with a provider of its own given by -c overrides, on
//     the server's Responses route, the key in LUMILO_API_KEY; for a
//     server of FunctionToolsOnly, in Lumilo's CODEX_HOME (codexHome).
//   - opencode: OpenCode with its whole configuration in
//     OPENCODE_CONFIG_CONTENT; OpenCode 2 also with --standalone, as its
//     background service does not read this environment (checked with
//     OpenCode 2.2.1: without the flag "opencode models" does not list the
//     provider), which OpenCode 1 refuses. A sh line picks by
//     "opencode --version" in the terminal (opencodeLine).
//   - pi: Pi with a provider extension (-e) that Lumilo writes to
//     AgentDir; Pi reads providers otherwise only from its own models.json.
//
// These are the settings Splash's own launchers use (install/clients.py of
// Splash 1.5.0). An unknown agent gives nil.
func AgentCommand(agent string, s AgentServer, key string) (argv []string, env []string) {
	if s.Context <= 0 {
		s.Context = defaultAgentContext
	}
	if key == "" {
		key = "local" // the agents want one; the server checks none
	}
	base := "http://127.0.0.1:" + strconv.Itoa(s.Port)
	switch agent {
	case "claude":
		ctx := strconv.Itoa(s.Context)
		env = []string{
			"ANTHROPIC_BASE_URL=" + base,
			"ANTHROPIC_AUTH_TOKEN=" + key,
			"ANTHROPIC_MODEL=" + s.Model,
			"ANTHROPIC_DEFAULT_OPUS_MODEL=" + s.Model,
			"ANTHROPIC_DEFAULT_SONNET_MODEL=" + s.Model,
			"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + s.Model,
			"ANTHROPIC_SMALL_FAST_MODEL=" + s.Model,
			"CLAUDE_CODE_MAX_CONTEXT_TOKENS=" + ctx,
			"CLAUDE_CODE_AUTO_COMPACT_WINDOW=" + ctx,
			"ANTHROPIC_API_KEY=", // unset: it would win over the token
			"CLAUDE_CODE_USE_BEDROCK=0",
			"CLAUDE_CODE_USE_VERTEX=0",
			"CLAUDE_CODE_USE_FOUNDRY=0",
		}
		return []string{"claude", "--disallowedTools", "WebSearch", "--model", s.Model, "--permission-mode", "default"}, env
	case "codex":
		provider := fmt.Sprintf(`{name=%s,base_url=%s,env_key=%q,wire_api="responses"}`,
			jsonString(s.Name), jsonString(base+"/v1"), agentKeyVar)
		argv = []string{"codex"}
		for _, kv := range [][2]string{
			{"model", jsonString(s.Model)},
			{"web_search", `"disabled"`},
			{"model_provider", `"lumilo"`},
			{"model_providers.lumilo", provider},
			{"model_context_window", strconv.Itoa(s.Context)},
			// The remaining 10% is room for the reply and the compaction.
			{"model_auto_compact_token_limit", strconv.Itoa(s.Context * 9 / 10)},
		} {
			argv = append(argv, "-c", kv[0]+"="+kv[1])
		}
		env = []string{agentKeyVar + "=" + key}
		if s.FunctionToolsOnly {
			home, err := codexHome()
			if err != nil {
				return nil, nil
			}
			argv = append(argv, "-c", "features.multi_agent=false")
			env = append(env, "CODEX_HOME="+home)
		}
		return argv, env
	case "opencode":
		model := "lumilo/" + s.Model
		input := []string{"text"}
		if s.Vision {
			input = append(input, "image")
		}
		out := agentOutput(s.Context)
		cfg := map[string]any{
			"model": model, "small_model": model,
			"provider": map[string]any{"lumilo": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": s.Name,
				// OpenCode substitutes {env:NAME} when it reads the config.
				"options": map[string]any{"baseURL": base + "/v1", "apiKey": "{env:" + agentKeyVar + "}"},
				"models": map[string]any{s.Model: map[string]any{
					"name":       s.Model,
					"reasoning":  true,
					"attachment": s.Vision,
					"modalities": map[string]any{"input": input, "output": []string{"text"}},
					"limit":      map[string]any{"context": s.Context, "input": max(1, s.Context-out), "output": out},
				}},
			}},
		}
		// An agent may pin its own model; point the built-in ones here.
		agents := map[string]any{}
		for _, a := range []string{"build", "plan", "general", "explore", "title", "compaction"} {
			agents[a] = map[string]any{"model": model}
		}
		cfg["agent"] = agents
		data, _ := json.Marshal(cfg)
		return []string{"/bin/sh", "-c", opencodeLine}, []string{"OPENCODE_CONFIG_CONTENT=" + string(data), agentKeyVar + "=" + key}
	case "pi":
		path, err := writePiExtension(s)
		if err != nil {
			return nil, nil
		}
		provider := piProvider(s.Port)
		return []string{"pi", "-e", path, "--provider", provider, "--model", s.Model}, []string{agentKeyVar + "=" + key}
	}
	return nil, nil
}

// opencodeLine starts OpenCode 2 ("opencode v2.2.1") with --standalone and
// OpenCode 1 without it, as each takes; it runs in the terminal's login
// shell, which finds opencode on the user's PATH.
const opencodeLine = `case "$(opencode --version 2>/dev/null)" in *v1.*|1.*) exec opencode ;; *) exec opencode --standalone ;; esac`

// piLevels are Pi's thinking levels, lowest first (pi --help: off,
// minimal, low, medium, high, xhigh, max); "off" sends "none".
var piLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// piLevelMap maps Pi's thinking levels to the efforts the server takes:
// each to the nearest of efforts, the higher one on a tie ("high" to
// "xhigh" when the server takes "medium" and "xhigh"). Without efforts
// only "off" is mapped, to "none", which turns thinking off.
func piLevelMap(efforts []string) map[string]string {
	if len(efforts) == 0 {
		return map[string]string{"off": "none"}
	}
	rank := map[string]int{}
	for i, l := range piLevels {
		rank[l] = i
	}
	out := map[string]string{}
	for i, l := range piLevels {
		best, bestD := "", 1<<30
		for _, e := range efforts {
			r, ok := rank[e]
			if !ok {
				continue
			}
			d := r - i
			if d < 0 {
				d = -d*2 + 1 // below: a tie goes to the higher one
			} else {
				d *= 2
			}
			if d < bestD {
				best, bestD = e, d
			}
		}
		if best == "" {
			continue
		}
		if l == "none" {
			l = "off"
		}
		if best != l {
			out[l] = best
		}
	}
	return out
}

// codexHome is the CODEX_HOME of Codex for a server that takes function
// tools only: AgentDir/codex, which holds no MCP servers, plugins or apps,
// whose tools Codex would send in namespaces; the user's ~/.codex is
// neither read nor changed. The user's global instructions come along:
// AGENTS.md of ~/.codex (or of their CODEX_HOME) is linked into it.
func codexHome() (string, error) {
	dir := AgentDir()
	if dir == "" {
		return "", fmt.Errorf("no folder for Codex")
	}
	home := filepath.Join(dir, "codex")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	user := os.Getenv("CODEX_HOME")
	if user == "" {
		if h, err := os.UserHomeDir(); err == nil {
			user = filepath.Join(h, ".codex")
		}
	}
	link := filepath.Join(home, "AGENTS.md")
	if src := filepath.Join(user, "AGENTS.md"); user != "" && user != home {
		if _, err := os.Stat(src); err == nil {
			if cur, err := os.Readlink(link); err != nil || cur != src {
				_ = os.Remove(link)
				_ = os.Symlink(src, link)
			}
		}
	}
	return home, nil
}

// piProvider is the name of the provider for the server on port: a
// session of Pi keeps the server it began with.
func piProvider(port int) string { return "lumilo-" + strconv.Itoa(port) }

// writePiExtension writes the extension that registers s as a provider of
// Pi (docs/custom-provider.md of Pi 1.4.0, "Register a compatible
// endpoint"), at AgentDir/pi-<port>.js, and returns its path. Pi reads the key
// from LUMILO_API_KEY for each request ("$NAME" reads an environment
// variable). It is rewritten at each launch, through a temporary file.
func writePiExtension(s AgentServer) (string, error) {
	dir := AgentDir()
	if dir == "" {
		return "", fmt.Errorf("no folder for Pi's extension")
	}
	input := []string{"text"}
	if s.Vision {
		input = append(input, "image")
	}
	model := map[string]any{
		"type": "chat", "id": s.Model, "name": s.Model,
		"reasoning": true,
		// Pi sends no effort when thinking is off, which leaves the chat
		// template's default; "none" turns thinking off. The other levels
		// go to the nearest effort the server takes (piLevelMap).
		"thinkingLevelMap": piLevelMap(s.Efforts),
		"input":            input,
		"cost":             map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		"contextWindow":    s.Context,
		"maxTokens":        agentOutput(s.Context),
	}
	cfg := map[string]any{
		"name": s.Name, "baseUrl": "http://127.0.0.1:" + strconv.Itoa(s.Port) + "/v1",
		"apiKey": "$" + agentKeyVar, "api": "openai-completions",
		"models": []any{model},
	}
	pName, _ := json.Marshal(piProvider(s.Port))
	pCfg, _ := json.MarshalIndent(cfg, "  ", "  ")
	src := "// Written by Lumilo for each launch of Pi: the local server as a provider.\n" +
		"export default function (pi) {\n  pi.registerProvider(" + string(pName) + ", " + string(pCfg) + ");\n}\n"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "pi-"+strconv.Itoa(s.Port)+".js")
	if err := writeAtomic(path, []byte(src)); err != nil {
		return "", err
	}
	return path, nil
}

// jsonString is s as a JSON (and TOML) string literal.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
