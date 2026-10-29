package enginekit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

var testServer = AgentServer{Port: 8000, Model: "Qwen3.8-27B-MLX-4bit", Context: 32768, Vision: true, Name: "TensorFold"}

func TestAgents(t *testing.T) {
	var ids []string
	for _, a := range Agents() {
		ids = append(ids, a.ID)
		if len(a.Logo) == 0 || !strings.HasPrefix(string(a.Logo), "<svg") {
			t.Errorf("%s: no logo", a.ID)
		}
	}
	if !slices.Equal(ids, []string{"claude", "codex", "opencode", "pi"}) {
		t.Errorf("agents %v", ids)
	}
}

// The key reaches every agent through the environment only, as the whole
// value of a variable (terminal.Open swaps such values for a Keychain
// lookup): never in argv nor inside a larger value.
func TestAgentKeyStaysInEnv(t *testing.T) {
	AgentDir = func() string { return t.TempDir() }
	const key = "s3cret-key"
	for _, a := range Agents() {
		argv, env := AgentCommand(a.ID, testServer, key)
		if len(argv) == 0 {
			t.Fatalf("%s: no command", a.ID)
		}
		if strings.Contains(strings.Join(argv, " "), key) {
			t.Errorf("%s: key in argv %q", a.ID, argv)
		}
		found := false
		for _, e := range env {
			k, v, _ := strings.Cut(e, "=")
			if v == key {
				found = true
			} else if strings.Contains(v, key) {
				t.Errorf("%s: key inside %s", a.ID, k)
			}
		}
		if !found {
			t.Errorf("%s: no key in env %q", a.ID, env)
		}
	}
	if argv, _ := AgentCommand("hermes", testServer, key); argv != nil {
		t.Errorf("unknown agent: %q", argv)
	}
}

func TestAgentClaude(t *testing.T) {
	argv, env := AgentCommand("claude", testServer, "")
	if !slices.Equal(argv, []string{"claude", "--disallowedTools", "WebSearch", "--model", testServer.Model, "--permission-mode", "default"}) {
		t.Errorf("argv %q", argv)
	}
	e := envMap(env)
	if e["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8000" || e["ANTHROPIC_AUTH_TOKEN"] != "local" ||
		e["ANTHROPIC_SMALL_FAST_MODEL"] != testServer.Model || e["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "32768" ||
		e["CLAUDE_CODE_USE_BEDROCK"] != "0" {
		t.Errorf("env %v", e)
	}
	if v, ok := e["ANTHROPIC_API_KEY"]; !ok || v != "" {
		t.Errorf("ANTHROPIC_API_KEY not unset: %q %v", v, ok)
	}
}

func TestAgentCodex(t *testing.T) {
	argv, env := AgentCommand("codex", testServer, "k")
	got := strings.Join(argv, " ")
	for _, want := range []string{
		`-c model="Qwen3.8-27B-MLX-4bit"`,
		`-c model_provider="lumilo"`,
		`-c model_providers.lumilo={name="TensorFold",base_url="http://127.0.0.1:8000/v1",env_key="LUMILO_API_KEY",wire_api="responses"}`,
		`-c model_context_window=32768`,
		`-c model_auto_compact_token_limit=29491`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if envMap(env)["LUMILO_API_KEY"] != "k" {
		t.Errorf("env %q", env)
	}
}

func TestAgentOpenCode(t *testing.T) {
	argv, env := AgentCommand("opencode", testServer, "k")
	if !slices.Equal(argv, []string{"/bin/sh", "-c", opencodeLine}) {
		t.Errorf("argv %q", argv)
	}
	var cfg struct {
		Model    string `json:"model"`
		Provider map[string]struct {
			Options map[string]string `json:"options"`
			Models  map[string]struct {
				Limit      map[string]int      `json:"limit"`
				Modalities map[string][]string `json:"modalities"`
			} `json:"models"`
		} `json:"provider"`
		Agent map[string]map[string]string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(envMap(env)["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg.Provider["lumilo"]
	m := p.Models[testServer.Model]
	if cfg.Model != "lumilo/"+testServer.Model || p.Options["baseURL"] != "http://127.0.0.1:8000/v1" ||
		p.Options["apiKey"] != "{env:LUMILO_API_KEY}" || cfg.Agent["build"]["model"] != cfg.Model ||
		m.Limit["context"] != 32768 || m.Limit["output"] != 8192 || m.Limit["input"] != 24576 ||
		!slices.Equal(m.Modalities["input"], []string{"text", "image"}) {
		t.Errorf("config %+v", cfg)
	}
}

func TestAgentPi(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents") // made by AgentCommand
	AgentDir = func() string { return dir }
	s := testServer
	s.Context = 0 // unknown: the default window
	argv, _ := AgentCommand("pi", s, "k")
	path := filepath.Join(dir, "pi-8000.js")
	if !slices.Equal(argv, []string{"pi", "-e", path, "--provider", "lumilo-8000", "--model", s.Model}) {
		t.Errorf("argv %q", argv)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`pi.registerProvider("lumilo-8000"`, `"apiKey": "$LUMILO_API_KEY"`,
		`"baseUrl": "http://127.0.0.1:8000/v1"`, `"contextWindow": 32768`, `"api": "openai-completions"`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("no %s in\n%s", want, src)
		}
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode())
	}
}

// A server that takes function tools only gets Codex in Lumilo's home,
// with the user's AGENTS.md linked in, and its subagents off.
func TestAgentCodexFunctionToolsOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents")
	AgentDir = func() string { return dir }
	user := t.TempDir()
	t.Setenv("CODEX_HOME", user)
	if err := os.WriteFile(filepath.Join(user, "AGENTS.md"), []byte("rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testServer
	s.FunctionToolsOnly = true
	argv, env := AgentCommand("codex", s, "k")
	home := filepath.Join(dir, "codex")
	if e := envMap(env); e["CODEX_HOME"] != home || e["LUMILO_API_KEY"] != "k" {
		t.Errorf("env %q", env)
	}
	if !strings.Contains(strings.Join(argv, " "), "-c features.multi_agent=false") {
		t.Errorf("argv %q", argv)
	}
	if b, err := os.ReadFile(filepath.Join(home, "AGENTS.md")); err != nil || string(b) != "rules" {
		t.Errorf("AGENTS.md: %q %v", b, err)
	}
	// Without it: the user's own home, subagents as they are.
	argv, env = AgentCommand("codex", testServer, "k")
	if _, ok := envMap(env)["CODEX_HOME"]; ok || strings.Contains(strings.Join(argv, " "), "multi_agent") {
		t.Errorf("plain codex: %q %q", argv, env)
	}
}

// The sh line gives OpenCode 2 --standalone and OpenCode 1 none.
func TestOpenCodeLine(t *testing.T) {
	bin := t.TempDir()
	for version, want := range map[string]string{"opencode v2.2.1": "--standalone", "v1.4.0": "", "1.0.3": "", "opencode v12.0.0": "--standalone"} {
		script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo '" + version + "'; else echo \"args:$*\"; fi\n"
		if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("/bin/sh", "-c", opencodeLine)
		cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
		out, err := cmd.Output()
		if got := strings.TrimSpace(string(out)); err != nil || got != "args:"+want {
			t.Errorf("%s: %q %v, want args:%s", version, got, err, want)
		}
	}
}

func TestPiLevelMap(t *testing.T) {
	got := piLevelMap([]string{"xhigh", "medium", "low", "none"}) // TensorFold's Qwen3.8-27B
	want := map[string]string{"off": "none", "minimal": "low", "high": "xhigh", "max": "xhigh"}
	if len(got) != len(want) {
		t.Errorf("map %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s -> %q, want %q (map %v)", k, got[k], v, got)
		}
	}
	if got := piLevelMap(nil); len(got) != 1 || got["off"] != "none" {
		t.Errorf("unknown efforts: %v", got)
	}
	if got := piLevelMap([]string{"low", "high"}); got["off"] != "low" || got["medium"] != "high" || got["max"] != "high" {
		t.Errorf("no none: %v", got)
	}
}
