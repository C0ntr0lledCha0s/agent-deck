package workspace

import (
	"context"
	"io"
	"regexp"
)

// Container status constants.
const (
	StatusRunning    = "running"
	StatusStopped    = "stopped"
	StatusNotFound   = "not_found"
	StatusNotCreated = "not_created"
)

// ContainerRuntime abstracts container lifecycle and execution operations.
// Implementations may target Docker, Podman, or other OCI-compatible runtimes.
type ContainerRuntime interface {
	// Create builds a new container from the given options without starting it.
	Create(ctx context.Context, opts CreateOpts) (containerID string, err error)

	// Start starts a previously created container.
	Start(ctx context.Context, containerID string) error

	// Stop gracefully stops a running container with the given timeout in seconds.
	Stop(ctx context.Context, containerID string, timeoutSecs int) error

	// Remove deletes a container. If force is true, a running container is killed first.
	Remove(ctx context.Context, containerID string, force bool) error

	// Status returns the current state of a container.
	Status(ctx context.Context, containerID string) (ContainerState, error)

	// Stats returns live resource usage statistics for a container.
	Stats(ctx context.Context, containerID string) (ContainerStats, error)

	// Exec runs a command inside a running container and returns its combined output.
	Exec(ctx context.Context, containerID string, cmd []string, stdin io.Reader) ([]byte, int, error)
}

// CreateOpts describes how to create a new container.
type CreateOpts struct {
	Name     string            // Container name (must be unique).
	Image    string            // OCI image reference (e.g. "ubuntu:24.04").
	Cmd      []string          // Entrypoint command and arguments.
	Env      []string          // Environment variables in "KEY=VALUE" form.
	Labels   map[string]string // Metadata labels attached to the container.
	Mounts   []Mount           // Bind mounts from host to container.
	NanoCPUs int64             // CPU quota in billionths of a CPU (1e9 = 1 core).
	Memory   int64             // Memory limit in bytes.
}

// Mount describes a bind mount from the host filesystem into the container.
type Mount struct {
	Source   string // Absolute path on the host.
	Target   string // Absolute path inside the container.
	ReadOnly bool   // If true, mount is read-only.
}

// ContainerState describes the current status of a container.
type ContainerState struct {
	Status   string // One of the Status* constants.
	ExitCode int    // Last exit code (meaningful only when stopped).
}

// ContainerStats holds point-in-time resource usage for a running container.
type ContainerStats struct {
	CPUPercent float64 // CPU usage as a percentage of allocated cores.
	MemUsage   uint64  // Current memory usage in bytes.
	MemLimit   uint64  // Memory limit in bytes.
}

var invalidContainerNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// ContainerNameForProject returns the canonical container name for a given project.
// The name is sanitized to satisfy Docker's container naming rules.
func ContainerNameForProject(projectName string) string {
	sanitized := invalidContainerNameChars.ReplaceAllString(projectName, "-")
	if sanitized == "" {
		sanitized = "unnamed"
	}
	return "agentdeck-" + sanitized
}
