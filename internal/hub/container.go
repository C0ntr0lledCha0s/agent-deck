package hub

import (
	"context"

	"github.com/asheshgoplani/agent-deck/internal/hub/workspace"
)

// ContainerExecutor abstracts docker exec operations for testability.
type ContainerExecutor interface {
	// IsHealthy returns true if the container is running.
	IsHealthy(ctx context.Context, container string) bool
	// Exec runs a command inside the container and returns stdout.
	Exec(ctx context.Context, container string, args ...string) (string, error)
}

// RuntimeExecutor adapts a workspace.ContainerRuntime to the ContainerExecutor interface.
type RuntimeExecutor struct {
	Runtime workspace.ContainerRuntime
}

// IsHealthy checks if the container is running via the ContainerRuntime.
func (r *RuntimeExecutor) IsHealthy(ctx context.Context, container string) bool {
	state, err := r.Runtime.Status(ctx, container)
	if err != nil {
		return false
	}
	return state.Status == workspace.StatusRunning
}

// Exec runs a command inside a container via the ContainerRuntime.
func (r *RuntimeExecutor) Exec(ctx context.Context, container string, args ...string) (string, error) {
	out, _, err := r.Runtime.Exec(ctx, container, args, nil)
	return string(out), err
}
