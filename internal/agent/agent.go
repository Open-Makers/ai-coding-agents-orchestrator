package agent

import (
	"context"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// Agent is implemented by every specialized agent in the pipeline.
type Agent interface {
	Role() bus.AgentRole
	Run(ctx context.Context, msg bus.Message) (bus.Message, error)
}
