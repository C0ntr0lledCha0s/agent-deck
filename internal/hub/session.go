package hub

import (
	"context"
	"fmt"
)

// SessionLauncher manages tmux sessions inside containers.
type SessionLauncher struct {
	Executor ContainerExecutor
}

// Launch creates a new tmux session inside a container and starts Claude Code.
// Returns the tmux session name (e.g. "agent-t-001").
func (l *SessionLauncher) Launch(ctx context.Context, container, taskID string) (string, error) {
	if !l.Executor.IsHealthy(ctx, container) {
		return "", fmt.Errorf("container %s is not running", container)
	}

	sessionName := "agent-" + taskID

	// Create tmux session with Claude Code.
	_, err := l.Executor.Exec(ctx, container,
		"tmux", "new-session", "-d", "-s", sessionName,
		"claude", "--dangerously-skip-permissions",
	)
	if err != nil {
		return "", fmt.Errorf("create tmux session: %w", err)
	}

	// Enable pipe-pane for streaming output to a log file.
	logFile := fmt.Sprintf("/tmp/%s.log", sessionName)
	_, err = l.Executor.Exec(ctx, container,
		"tmux", "pipe-pane", "-o", "-t", sessionName,
		fmt.Sprintf("cat >> %s", logFile),
	)
	if err != nil {
		return "", fmt.Errorf("configure pipe-pane: %w", err)
	}

	return sessionName, nil
}

// SendInput sends text to a tmux session via send-keys.
func (l *SessionLauncher) SendInput(ctx context.Context, container, sessionName, input string) error {
	_, err := l.Executor.Exec(ctx, container,
		"tmux", "send-keys", "-t", sessionName, input, "Enter",
	)
	if err != nil {
		return fmt.Errorf("send-keys to %s: %w", sessionName, err)
	}
	return nil
}
