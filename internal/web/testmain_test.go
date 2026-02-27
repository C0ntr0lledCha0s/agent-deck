package web

import (
	"os"
	"os/exec"
	"testing"
)

// skipIfNoTmuxServer skips the test if tmux binary is missing or server isn't running.
func skipIfNoTmuxServer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	if err := exec.Command("tmux", "list-sessions").Run(); err != nil {
		t.Skip("tmux server not running")
	}
}

func TestMain(m *testing.M) {
	os.Setenv("AGENTDECK_PROFILE", "_test")
	os.Exit(m.Run())
}
