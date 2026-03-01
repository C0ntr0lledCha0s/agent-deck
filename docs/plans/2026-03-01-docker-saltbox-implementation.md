# Docker/SaltBox Containerization Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Containerize Agent Deck for deployment on SaltBox with Docker socket mount for provisioning sibling MCP and sandbox containers.

**Architecture:** Multi-stage Dockerfile (Go build → Ubuntu runtime with tmux), docker-compose.yml with Traefik labels, Docker socket mount for sibling container provisioning. Extends `CreateOpts`/`DockerRuntime` with security options and network self-discovery.

**Tech Stack:** Docker, docker-compose, Go 1.24, Docker Engine SDK (`github.com/docker/docker`), Ubuntu 24.04, tmux, Traefik

---

### Task 1: Create .dockerignore

**Files:**
- Create: `.dockerignore`

**Step 1: Create the .dockerignore file**

```
.git
.github
.devcontainer
.worktrees
.claude
build/
docs/
*.md
!go.sum
lefthook.yml
.goreleaser.yml
.air.toml
```

**Step 2: Verify contents**

Run: `cat .dockerignore`
Expected: file contents match above

**Step 3: Commit**

```bash
git add .dockerignore
git commit -m "chore: add .dockerignore for production builds"
```

---

### Task 2: Create production Dockerfile

**Files:**
- Create: `Dockerfile`

**Step 1: Create the multi-stage Dockerfile**

```dockerfile
# Stage 1: Build
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.Version=${VERSION}" \
    -o /out/agent-deck ./cmd/agent-deck

# Stage 2: Runtime
FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux \
    ca-certificates \
    curl \
    git \
    && rm -rf /var/lib/apt/lists/*

# Non-root user
RUN groupadd --gid 1000 agentdeck \
    && useradd --uid 1000 --gid 1000 -m agentdeck

COPY --from=builder /out/agent-deck /usr/local/bin/agent-deck

# Config lives at ~/.agent-deck which resolves to /home/agentdeck/.agent-deck
# The named volume mounts here to persist across container restarts.
VOLUME /home/agentdeck/.agent-deck

EXPOSE 8420

USER agentdeck
WORKDIR /home/agentdeck

ENTRYPOINT ["agent-deck"]
CMD ["web", "--headless", "--listen", "0.0.0.0:8420"]
```

**Step 2: Build the image to verify it compiles**

Run: `docker build -t agent-deck:test .`
Expected: successful build, image created

**Step 3: Run a quick smoke test**

Run: `docker run --rm -d --name agentdeck-smoke -p 9420:8420 agent-deck:test`
Then: `curl -s http://127.0.0.1:9420/healthz`
Expected: health check responds (200 OK or similar)
Cleanup: `docker stop agentdeck-smoke`

**Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat(docker): add multi-stage production Dockerfile"
```

---

### Task 3: Create docker-compose.yml and .env.example

**Files:**
- Create: `docker-compose.yml`
- Create: `.env.example`

**Step 1: Create .env.example**

```env
# Domain for Traefik routing (e.g., example.com)
# Agent Deck will be accessible at agentdeck.${DOMAIN}
DOMAIN=example.com
```

**Step 2: Create docker-compose.yml**

```yaml
services:
  agent-deck:
    build: .
    image: agent-deck:latest
    container_name: agent-deck
    restart: unless-stopped
    ports:
      - "8420:8420"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - agent-deck-config:/home/agentdeck/.agent-deck
    environment:
      - AGENTDECK_PROFILE=default
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.agent-deck.rule=Host(`agentdeck.${DOMAIN}`)"
      - "traefik.http.routers.agent-deck.entrypoints=websecure"
      - "traefik.http.routers.agent-deck.tls.certresolver=cfdns"
      - "traefik.http.services.agent-deck.loadbalancer.server.port=8420"

volumes:
  agent-deck-config:
```

Note: SQLite state.db lives inside `~/.agent-deck/profiles/<profile>/state.db`, so a single volume at `~/.agent-deck` covers both config and data.

**Step 3: Verify compose config parses**

Run: `docker compose config`
Expected: valid YAML output, no errors

**Step 4: Commit**

```bash
git add docker-compose.yml .env.example
git commit -m "feat(docker): add docker-compose.yml with Traefik labels and .env.example"
```

---

### Task 4: Extend CreateOpts with security and networking options

This task extends the `ContainerRuntime` types to support security hardening and network configuration needed for sandbox containers.

**Files:**
- Modify: `internal/hub/workspace/runtime.go`
- Modify: `internal/hub/workspace/runtime_test.go`

**Step 1: Write the failing test**

Add to `internal/hub/workspace/runtime_test.go`:

```go
func TestCreateOptsSecurityFields(t *testing.T) {
	opts := CreateOpts{
		Name:  "sandbox-test",
		Image: "ubuntu:24.04",
		SecurityOpts: []string{"no-new-privileges"},
		CapAdd:       []string{"NET_ADMIN", "NET_RAW"},
		CapDrop:      []string{"ALL"},
		NetworkMode:  "none",
		AutoRemove:   true,
	}

	assert.Equal(t, []string{"no-new-privileges"}, opts.SecurityOpts)
	assert.Equal(t, []string{"NET_ADMIN", "NET_RAW"}, opts.CapAdd)
	assert.Equal(t, []string{"ALL"}, opts.CapDrop)
	assert.Equal(t, "none", opts.NetworkMode)
	assert.True(t, opts.AutoRemove)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/hub/workspace -run TestCreateOptsSecurityFields`
Expected: FAIL — `SecurityOpts`, `CapAdd`, `CapDrop`, `NetworkMode`, `AutoRemove` fields don't exist

**Step 3: Add fields to CreateOpts in runtime.go**

Add these fields to the `CreateOpts` struct in `internal/hub/workspace/runtime.go:43-52`:

```go
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

	// Security and isolation options (used for sandbox containers).
	SecurityOpts []string // e.g. ["no-new-privileges"].
	CapAdd       []string // Linux capabilities to add (e.g. "NET_ADMIN").
	CapDrop      []string // Linux capabilities to drop (e.g. "ALL").
	NetworkMode  string   // Docker network mode (e.g. "none", "bridge", custom network name).
	AutoRemove   bool     // Remove container automatically when it stops.
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/hub/workspace -run TestCreateOptsSecurityFields`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/hub/workspace/runtime.go internal/hub/workspace/runtime_test.go
git commit -m "feat(workspace): extend CreateOpts with security and networking fields"
```

---

### Task 5: Apply new CreateOpts fields in DockerRuntime.Create

**Files:**
- Modify: `internal/hub/workspace/docker.go`
- Modify: `internal/hub/workspace/docker_test.go`

**Step 1: Write the failing test**

Add to `internal/hub/workspace/docker_test.go`. This is a unit test that verifies the Docker API types are built correctly from CreateOpts. Since it calls the real Docker API, use the `skipIfNoDockerRuntime` helper pattern from `integration_test.go`:

```go
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
	assert.Contains(t, info.HostConfig.CapAdd, "NET_ADMIN")
	assert.Contains(t, info.HostConfig.CapDrop, "MKNOD")
	assert.Equal(t, container.NetworkMode("none"), info.HostConfig.NetworkMode)
}
```

Note: This test needs access to the `container` import. Ensure `docker_test.go` imports `"github.com/docker/docker/api/types/container"`.

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/hub/workspace -run TestCreateAppliesSecurityOpts`
Expected: FAIL — security opts not applied (fields ignored in current `Create` method)

**Step 3: Update DockerRuntime.Create to apply new fields**

In `internal/hub/workspace/docker.go`, modify the `Create` method (lines 34-63). The `hostCfg` construction needs to include the new fields:

```go
func (d *DockerRuntime) Create(ctx context.Context, opts CreateOpts) (string, error) {
	cfg := &container.Config{
		Image:  opts.Image,
		Cmd:    opts.Cmd,
		Env:    opts.Env,
		Labels: opts.Labels,
	}

	hostCfg := &container.HostConfig{
		Resources: container.Resources{
			NanoCPUs: opts.NanoCPUs,
			Memory:   opts.Memory,
		},
		SecurityOpt: opts.SecurityOpts,
		CapAdd:      opts.CapAdd,
		CapDrop:     opts.CapDrop,
		AutoRemove:  opts.AutoRemove,
	}

	if opts.NetworkMode != "" {
		hostCfg.NetworkMode = container.NetworkMode(opts.NetworkMode)
	}

	for _, m := range opts.Mounts {
		hostCfg.Mounts = append(hostCfg.Mounts, dockermount.Mount{
			Type:     dockermount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	resp, err := d.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, opts.Name)
	if err != nil {
		return "", fmt.Errorf("container create %q: %w", opts.Name, err)
	}
	return resp.ID, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/hub/workspace -run TestCreateAppliesSecurityOpts`
Expected: PASS

**Step 5: Run all workspace tests to check for regressions**

Run: `go test -race -v ./internal/hub/workspace/...`
Expected: All tests pass (including existing `TestDockerRuntimeLifecycle`)

**Step 6: Commit**

```bash
git add internal/hub/workspace/docker.go internal/hub/workspace/docker_test.go
git commit -m "feat(workspace): apply security opts, capabilities, and network mode in DockerRuntime.Create"
```

---

### Task 6: Add network self-discovery for sibling containers

When Agent Deck runs inside a Docker container, it needs to discover its own Docker networks so spawned sibling containers can communicate with it. This follows the pattern used by Docker's MCP Gateway (`guessNetworks()`).

**Files:**
- Modify: `internal/hub/workspace/docker.go`
- Modify: `internal/hub/workspace/docker_test.go`

**Step 1: Write the failing test**

Add to `internal/hub/workspace/docker_test.go`:

```go
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
```

**Step 2: Run test to verify it fails**

Run: `go test -race -v ./internal/hub/workspace -run TestSelfNetworks`
Expected: FAIL — `SelfNetworks` method doesn't exist

**Step 3: Implement SelfNetworks**

Add to `internal/hub/workspace/docker.go`:

```go
// SelfNetworks returns the Docker networks this process's container belongs to.
// Returns nil if not running inside a container (hostname doesn't match a container).
// Used to join sibling containers to the same networks for communication.
func (d *DockerRuntime) SelfNetworks(ctx context.Context) ([]string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("hostname: %w", err)
	}

	info, err := d.cli.ContainerInspect(ctx, hostname)
	if err != nil {
		// Not running in a container, or container not found — not an error.
		if errdefs.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect self %q: %w", hostname, err)
	}

	var networks []string
	if info.NetworkSettings != nil {
		for name := range info.NetworkSettings.Networks {
			networks = append(networks, name)
		}
	}
	return networks, nil
}
```

Add `"os"` to the imports in `docker.go`.

**Step 4: Run test to verify it passes**

Run: `go test -race -v ./internal/hub/workspace -run TestSelfNetworks`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/hub/workspace/docker.go internal/hub/workspace/docker_test.go
git commit -m "feat(workspace): add SelfNetworks for sibling container network discovery"
```

---

### Task 7: Docker build + smoke test end-to-end

Verify the full Docker workflow works: build, run, health check, stop.

**Files:**
- No code changes — validation only

**Step 1: Build the image**

Run: `docker build -t agent-deck:test .`
Expected: successful multi-stage build

**Step 2: Run the container**

Run: `docker run --rm -d --name agentdeck-e2e -p 9420:8420 -v /var/run/docker.sock:/var/run/docker.sock agent-deck:test`
Expected: container starts, outputs container ID

**Step 3: Wait for startup and check health**

Run: `sleep 2 && curl -sf http://127.0.0.1:9420/healthz`
Expected: health check passes

**Step 4: Check the web dashboard loads**

Run: `curl -sf http://127.0.0.1:9420/ | head -5`
Expected: HTML content from the dashboard

**Step 5: Verify tmux is running inside**

Run: `docker exec agentdeck-e2e tmux list-sessions 2>&1 || echo "no sessions (expected)"`
Expected: either lists sessions or shows "no sessions" — tmux binary is available

**Step 6: Clean up**

Run: `docker stop agentdeck-e2e`

**Step 7: Test docker compose**

Run: `cp .env.example .env && docker compose up -d --build && sleep 3 && curl -sf http://127.0.0.1:8420/healthz && docker compose down`
Expected: compose starts, health check passes, clean shutdown

Clean up: `rm .env`

---

### Task 8: Add Makefile targets for Docker

**Files:**
- Modify: `Makefile`

**Step 1: Add docker targets to Makefile**

Append to `Makefile`:

```makefile
# Docker targets
docker-build:
	docker build -t agent-deck:latest --build-arg VERSION=$(VERSION) .

docker-run: docker-build
	docker run --rm -p 8420:8420 \
		-v /var/run/docker.sock:/var/run/docker.sock \
		agent-deck:latest

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down
```

**Step 2: Add to .PHONY**

Update the `.PHONY` line at the top to include the new targets:

```makefile
.PHONY: build run install clean dev release-local test fmt lint ci docker-build docker-run docker-up docker-down
```

**Step 3: Verify make targets**

Run: `make -n docker-build`
Expected: prints the docker build command without executing

**Step 4: Commit**

```bash
git add Makefile
git commit -m "feat(docker): add Makefile targets for docker build/run/up/down"
```

---

### Summary of Tasks

| Task | Description | Files | Type |
|------|-------------|-------|------|
| 1 | Create .dockerignore | `.dockerignore` | Create |
| 2 | Create production Dockerfile | `Dockerfile` | Create |
| 3 | Create docker-compose.yml + .env.example | `docker-compose.yml`, `.env.example` | Create |
| 4 | Extend CreateOpts with security fields | `runtime.go`, `runtime_test.go` | Modify (TDD) |
| 5 | Apply new fields in DockerRuntime.Create | `docker.go`, `docker_test.go` | Modify (TDD) |
| 6 | Add network self-discovery | `docker.go`, `docker_test.go` | Modify (TDD) |
| 7 | Docker build + smoke test E2E | None (validation) | Test |
| 8 | Add Makefile docker targets | `Makefile` | Modify |

**Dependencies:** Tasks 1-3 are independent (Docker artifacts). Tasks 4→5→6 are sequential (each builds on prior). Task 7 depends on tasks 1-3. Task 8 is independent.

**Parallelizable:** Tasks 1, 2, 3, 8 can run in parallel. Tasks 4, 5, 6 must be sequential.
