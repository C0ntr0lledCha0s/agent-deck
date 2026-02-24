package hub

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ContainerExecutor abstracts docker exec operations for testability.
type ContainerExecutor interface {
	// IsHealthy returns true if the container is running.
	IsHealthy(ctx context.Context, container string) bool
	// Exec runs a command inside the container and returns stdout.
	Exec(ctx context.Context, container string, args ...string) (string, error)
}

// DockerExecutor implements ContainerExecutor via the docker CLI.
type DockerExecutor struct{}

// IsHealthy checks if a container is running via docker inspect.
func (d *DockerExecutor) IsHealthy(ctx context.Context, container string) bool {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", container)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// Exec runs a command inside a container via docker exec.
func (d *DockerExecutor) Exec(ctx context.Context, container string, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", container}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker exec %s: %w (stderr: %s)", container, err, stderr.String())
	}
	return stdout.String(), nil
}
