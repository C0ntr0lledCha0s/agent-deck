package hub

import (
	"context"
	"testing"
)

func TestLaunchSessionCreatesSession(t *testing.T) {
	exec := &mockExecutor{healthy: true, execOutput: ""}
	launcher := &SessionLauncher{Executor: exec}

	sessionName, err := launcher.Launch(context.Background(), "sandbox-api", "t-001")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if sessionName == "" {
		t.Fatal("expected non-empty session name")
	}
}

func TestLaunchSessionUnhealthyContainer(t *testing.T) {
	exec := &mockExecutor{healthy: false}
	launcher := &SessionLauncher{Executor: exec}

	_, err := launcher.Launch(context.Background(), "sandbox-api", "t-001")
	if err == nil {
		t.Fatal("expected error for unhealthy container")
	}
}

func TestSendInputToSession(t *testing.T) {
	exec := &mockExecutor{healthy: true, execOutput: ""}
	launcher := &SessionLauncher{Executor: exec}

	err := launcher.SendInput(context.Background(), "sandbox-api", "agent-t-001", "Fix the auth bug")
	if err != nil {
		t.Fatalf("SendInput: %v", err)
	}
}
