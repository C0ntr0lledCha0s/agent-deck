package workspace

import (
	"context"
	"os/exec"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfNoDocker skips the test when the Docker daemon is not reachable.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not available, skipping")
	}
}

func TestNewDockerRuntime(t *testing.T) {
	skipIfNoDocker(t)

	rt, err := NewDockerRuntime()
	require.NoError(t, err)
	require.NotNil(t, rt)
	require.NotNil(t, rt.cli)

	// Verify we can actually talk to the daemon.
	_, err = rt.cli.Ping(context.Background())
	assert.NoError(t, err)
}

func TestDockerRuntimeImplementsInterface(t *testing.T) {
	// Compile-time check that DockerRuntime satisfies ContainerRuntime.
	var _ ContainerRuntime = (*DockerRuntime)(nil)
}

func TestSelfNetworks(t *testing.T) {
	rt := skipIfNoDockerRuntime(t)
	ctx := context.Background()

	// When not running inside a container, SelfNetworks should return nil/empty
	// (hostname won't match any container).
	networks, err := rt.SelfNetworks(ctx)
	require.NoError(t, err)
	// Outside a container, expect empty result (not an error).
	t.Logf("SelfNetworks() returned %d networks: %v", len(networks), networks)
	_ = networks // No assertion on count — depends on environment.
}

func TestCreateAppliesSecurityOpts(t *testing.T) {
	rt := skipIfNoDockerRuntime(t)
	ctx := context.Background()
	name := "agentdeck-security-test"

	// Cleanup from any previous failed run.
	_ = rt.Remove(ctx, name, true)
	t.Cleanup(func() {
		_ = rt.Remove(context.Background(), name, true)
	})

	id, err := rt.Create(ctx, CreateOpts{
		Name:         name,
		Image:        "alpine:latest",
		Cmd:          []string{"sleep", "10"},
		SecurityOpts: []string{"no-new-privileges"},
		CapAdd:       []string{"NET_ADMIN"},
		CapDrop:      []string{"MKNOD"},
		NetworkMode:  "none",
		AutoRemove:   false, // Can't inspect auto-removed containers
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	// Inspect the container to verify security options were applied.
	info, err := rt.cli.ContainerInspect(ctx, name)
	require.NoError(t, err)
	assert.Contains(t, info.HostConfig.SecurityOpt, "no-new-privileges")
	// Docker normalizes capability names by prepending "CAP_".
	assert.Contains(t, info.HostConfig.CapAdd, "CAP_NET_ADMIN")
	assert.Contains(t, info.HostConfig.CapDrop, "CAP_MKNOD")
	assert.Equal(t, container.NetworkMode("none"), info.HostConfig.NetworkMode)
}
