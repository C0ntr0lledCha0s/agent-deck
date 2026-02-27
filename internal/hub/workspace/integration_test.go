package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfNoDockerRuntime creates a DockerRuntime and verifies the daemon is
// reachable. It skips the test when Docker is unavailable.
func skipIfNoDockerRuntime(t *testing.T) *DockerRuntime {
	t.Helper()
	rt, err := NewDockerRuntime()
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}
	// Quick ping to verify daemon is reachable.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := rt.cli.Ping(ctx); err != nil {
		t.Skipf("Docker daemon not reachable: %v", err)
	}
	return rt
}

func TestDockerRuntimeLifecycle(t *testing.T) {
	rt := skipIfNoDockerRuntime(t)
	ctx := context.Background()
	name := "agentdeck-integration-test"

	// Cleanup from any previous failed run.
	_ = rt.Remove(ctx, name, true)

	// Create
	id, err := rt.Create(ctx, CreateOpts{
		Name:   name,
		Image:  "alpine:latest",
		Cmd:    []string{"sleep", "300"},
		Labels: map[string]string{"agentdeck.test": "true"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	// Ensure cleanup on test exit.
	t.Cleanup(func() {
		_ = rt.Remove(context.Background(), name, true)
	})

	// Start
	require.NoError(t, rt.Start(ctx, name))

	// Status should be running
	state, err := rt.Status(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, state.Status)

	// Stats — container is running so we should get some data back.
	stats, err := rt.Stats(ctx, name)
	require.NoError(t, err)
	assert.NotZero(t, stats.MemLimit, "expected non-zero memory limit")

	// Exec
	out, exitCode, err := rt.Exec(ctx, name, []string{"echo", "hello"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, string(out), "hello")

	// Stop (5 second timeout)
	require.NoError(t, rt.Stop(ctx, name, 5))

	state, err = rt.Status(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, state.Status)

	// Remove
	require.NoError(t, rt.Remove(ctx, name, false))

	state, err = rt.Status(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, StatusNotFound, state.Status)
}
