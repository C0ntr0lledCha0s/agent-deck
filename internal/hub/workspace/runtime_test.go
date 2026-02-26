package workspace

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRuntime implements ContainerRuntime for unit testing.
type mockRuntime struct {
	createID  string
	createErr error

	startErr error
	stopErr  error

	removeErr error

	state    ContainerState
	stateErr error

	stats    ContainerStats
	statsErr error

	execOut  []byte
	execCode int
	execErr  error
}

func (m *mockRuntime) Create(_ context.Context, _ CreateOpts) (string, error) {
	return m.createID, m.createErr
}

func (m *mockRuntime) Start(_ context.Context, _ string) error {
	return m.startErr
}

func (m *mockRuntime) Stop(_ context.Context, _ string, _ int) error {
	return m.stopErr
}

func (m *mockRuntime) Remove(_ context.Context, _ string, _ bool) error {
	return m.removeErr
}

func (m *mockRuntime) Status(_ context.Context, _ string) (ContainerState, error) {
	return m.state, m.stateErr
}

func (m *mockRuntime) Stats(_ context.Context, _ string) (ContainerStats, error) {
	return m.stats, m.statsErr
}

func (m *mockRuntime) Exec(_ context.Context, _ string, _ []string, _ io.Reader) ([]byte, int, error) {
	return m.execOut, m.execCode, m.execErr
}

func TestContainerRuntimeInterface(t *testing.T) {
	// Verify the mock satisfies the interface at compile time.
	var rt ContainerRuntime = &mockRuntime{
		createID: "abc123",
		state:    ContainerState{Status: StatusRunning, ExitCode: 0},
		stats:    ContainerStats{CPUPercent: 25.5, MemUsage: 1024 * 1024, MemLimit: 512 * 1024 * 1024},
		execOut:  []byte("hello\n"),
		execCode: 0,
	}
	ctx := context.Background()

	// Create
	id, err := rt.Create(ctx, CreateOpts{
		Name:  "test-container",
		Image: "ubuntu:24.04",
		Cmd:   []string{"sleep", "infinity"},
		Env:   []string{"FOO=bar"},
		Labels: map[string]string{
			"managed-by": "agentdeck",
		},
		Mounts: []Mount{
			{Source: "/home/user/project", Target: "/workspace", ReadOnly: false},
		},
		NanoCPUs: 2e9,
		Memory:   512 * 1024 * 1024,
	})
	require.NoError(t, err)
	assert.Equal(t, "abc123", id)

	// Start
	err = rt.Start(ctx, id)
	require.NoError(t, err)

	// Status
	state, err := rt.Status(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, state.Status)
	assert.Equal(t, 0, state.ExitCode)

	// Stats
	stats, err := rt.Stats(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 25.5, stats.CPUPercent)
	assert.Equal(t, uint64(1024*1024), stats.MemUsage)
	assert.Equal(t, uint64(512*1024*1024), stats.MemLimit)

	// Exec
	out, code, err := rt.Exec(ctx, id, []string{"echo", "hello"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, []byte("hello\n"), out)

	// Stop
	err = rt.Stop(ctx, id, 10)
	require.NoError(t, err)

	// Remove
	err = rt.Remove(ctx, id, true)
	require.NoError(t, err)
}

func TestContainerRuntimeErrors(t *testing.T) {
	errFail := errors.New("something went wrong")

	rt := &mockRuntime{
		createErr: errFail,
		startErr:  errFail,
		stopErr:   errFail,
		removeErr: errFail,
		stateErr:  errFail,
		statsErr:  errFail,
		execErr:   errFail,
	}
	ctx := context.Background()

	_, err := rt.Create(ctx, CreateOpts{})
	assert.ErrorIs(t, err, errFail)

	assert.ErrorIs(t, rt.Start(ctx, "x"), errFail)
	assert.ErrorIs(t, rt.Stop(ctx, "x", 5), errFail)
	assert.ErrorIs(t, rt.Remove(ctx, "x", false), errFail)

	_, err = rt.Status(ctx, "x")
	assert.ErrorIs(t, err, errFail)

	_, err = rt.Stats(ctx, "x")
	assert.ErrorIs(t, err, errFail)

	_, _, err = rt.Exec(ctx, "x", []string{"ls"}, nil)
	assert.ErrorIs(t, err, errFail)
}

func TestContainerNameForProject(t *testing.T) {
	tests := []struct {
		project  string
		expected string
	}{
		{"myapp", "agentdeck-myapp"},
		{"web-frontend", "agentdeck-web-frontend"},
		{"api", "agentdeck-api"},
		{"", "agentdeck-"},
	}
	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			assert.Equal(t, tt.expected, ContainerNameForProject(tt.project))
		})
	}
}
