package llamacpp

import "github.com/kotuke/lumilo/internal/engine"

// There are no agents (Info.Agents is empty). Splash's "splash claude|codex|..." wrappers are its own;
// llama-server only serves OpenAI-, Anthropic- and Responses-compatible
// routes (README "OpenAI-compatible API Endpoints", "/v1/messages",
// "/v1/responses"), so an agent would need Lumilo to set its base URL and
// key itself.

// AgentCommand offers nothing: there are no agents.
func (l *LlamaCpp) AgentCommand(string, engine.Target) (argv []string, env []string) {
	return nil, nil
}
