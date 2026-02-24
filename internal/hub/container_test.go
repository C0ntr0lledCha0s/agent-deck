package hub

import (
	"context"
	"errors"
	"testing"
)

// mockExecutor implements ContainerExecutor for testing.
type mockExecutor struct {
	healthy    bool
	execOutput string
	execErr    error
}

func (m *mockExecutor) IsHealthy(ctx context.Context, container string) bool {
	return m.healthy
}

func (m *mockExecutor) Exec(ctx context.Context, container string, args ...string) (string, error) {
	return m.execOutput, m.execErr
}

func TestContainerExecutorInterface(t *testing.T) {
	var exec ContainerExecutor = &mockExecutor{healthy: true}
	if !exec.IsHealthy(context.Background(), "test-container") {
		t.Fatal("expected healthy")
	}
}

func TestContainerExecutorExecError(t *testing.T) {
	exec := &mockExecutor{execErr: errors.New("container not found")}
	_, err := exec.Exec(context.Background(), "missing", "echo", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
}
