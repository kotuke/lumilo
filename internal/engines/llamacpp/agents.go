package llamacpp

import (
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// AgentCommand points agent at the server at t (enginekit.AgentCommand):
// llama-server serves OpenAI-, Anthropic- and Responses-compatible routes
// (README "OpenAI-compatible API Endpoints", "/v1/messages",
// "/v1/responses"); a Responses tool other than a function it skips with a
// warning (server-chat.cpp server_chat_convert_responses_tools), so Codex
// keeps the user's own home. The model's name is its --alias (ServeCommand) or what
// /props names; the context and image input are what /props reported
// (portMemo). A server Lumilo knows nothing of gives nil.
func (l *LlamaCpp) AgentCommand(agent string, t engine.Target) (argv []string, env []string) {
	l.mu.Lock()
	m := *l.memo(t.Port)
	l.mu.Unlock()
	if m.served == "" {
		return nil, nil
	}
	return enginekit.AgentCommand(agent, enginekit.AgentServer{Port: t.Port, Model: m.served, Context: m.context,
		Vision: m.vision, Name: l.info.Name}, t.APIKey)
}
