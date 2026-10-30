package tensorfold

import (
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

// AgentCommand points agent at the server at t (enginekit.AgentCommand):
// TensorFold serves Anthropic Messages, OpenAI chat and Responses routes
// (README "Quick start"), and only function tools (FunctionToolsOnly).
// The model's served name, context and image input
// are what the server was started with or found with, and the context
// what /memory reported (portMemo). A server Lumilo knows nothing of gives
// nil.
func (tf *TensorFold) AgentCommand(agent string, t engine.Target) (argv []string, env []string) {
	tf.mu.Lock()
	m := *tf.memo(t.Port)
	tf.mu.Unlock()
	if m.served == "" {
		return nil, nil
	}
	return enginekit.AgentCommand(agent, enginekit.AgentServer{Port: t.Port, Model: m.served, Context: m.context,
		Vision: m.vision, Name: tf.info.Name, FunctionToolsOnly: true, Efforts: m.efforts}, t.APIKey)
}
