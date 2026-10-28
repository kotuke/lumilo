package splash

import (
	"strconv"

	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// agents are the coding agents offered, in this order. Splash also knows
// hermes (ref §5); it is left out on purpose.
func agents() []engine.Agent {
	return []engine.Agent{
		{ID: "claude", Name: "Claude", Logo: enginekit.MustFile(logos, "logos/logo-claude.svg"), Color: "#D97757"},
		{ID: "codex", Name: "Codex", Logo: enginekit.MustFile(logos, "logos/logo-codex.svg")},
		{ID: "opencode", Name: "OpenCode", Logo: enginekit.MustFile(logos, "logos/logo-opencode.svg")},
		{ID: "pi", Name: "Pi", Logo: enginekit.MustFile(logos, "logos/logo-pi.svg")},
	}
}

// AgentCommand is "splash <agent>". It does not start a server: it probes
// 127.0.0.1:$SPLASH_PORT (default 8000) with SPLASH_API_KEY as the bearer
// token and then execs the agent, which it finds on the PATH of the
// Terminal's login shell (ref §5, install/launcher.py:439-459,
// install/clients.py:77-84). An unknown agent gives nil.
func (s *Splash) AgentCommand(agent string, t engine.Target) (argv []string, env []string) {
	known := false
	for _, a := range s.info.Agents {
		known = known || a.ID == agent
	}
	if !known {
		return nil, nil
	}
	// An empty SPLASH_API_KEY unsets a stale one from the user's shell.
	env = []string{"SPLASH_PORT=" + strconv.Itoa(t.Port), "SPLASH_API_KEY=" + t.APIKey}
	return []string{s.binPath(), agent}, env
}
