package worker

import (
	"context"
	"fmt"

	"github.com/reyansh7/Forge/internal/queue"
)

// Dispatcher routes allowlisted job types to the matching handler.
//
// Unknown types are rejected here as well as at enqueue (defense in depth).
// A client cannot add a "command" type and have it executed.
type Dispatcher struct {
	Example Handler
	Deploy  Handler
}

func (d Dispatcher) Handle(ctx context.Context, job queue.Job) error {
	switch job.Type {
	case queue.TypeExample:
		if d.Example == nil {
			return fmt.Errorf("example handler is not configured")
		}
		return d.Example.Handle(ctx, job)
	case queue.TypeDeploy:
		if d.Deploy == nil {
			return fmt.Errorf("deploy handler is not configured")
		}
		return d.Deploy.Handle(ctx, job)
	default:
		return fmt.Errorf("%w: %q", queue.ErrUnknownType, job.Type)
	}
}
