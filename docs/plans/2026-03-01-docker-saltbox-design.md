# Docker Containerization for SaltBox Deployment

**Date:** 2026-03-01
**Status:** Approved
**Branch:** feature/docker-cicd

## Problem

Agent Deck needs to run as a Docker container on a SaltBox (Ansible-based self-hosted) server. The container must be able to provision sibling Docker containers for two use cases: MCP server containers and isolated sandbox containers for agent sessions.

## Decisions

- **Deployment model:** Socket mount + sibling containers (Approach A)
- **Use case:** Personal self-hosted instance (no multi-user auth)
- **Runtime mode:** Headless only (`agent-deck web --headless`)
- **Persistence:** Named Docker volumes for SQLite DB and config
- **Networking:** Behind SaltBox's Traefik reverse proxy (HTTPS/domain routing)
- **Sandbox model:** Container-based sandbox replicating Docker Desktop Sandbox isolation (Docker Sandboxes require Docker Desktop, unavailable on headless Linux)
- **Deliverables:** Docker artifacts only (Dockerfile, docker-compose.yml, .env template) — no Ansible role

## Architecture

```
Host (SaltBox / Docker Engine)
├── agent-deck container
│   ├── agent-deck binary (headless, port 8420)
│   ├── tmux server (manages agent sessions)
│   ├── SQLite DB (/data/state.db)
│   └── Docker socket mount (/var/run/docker.sock)
│
├── agentdeck-mcp-* (sibling MCP server containers)
│   └── Provisioned via Docker socket, stdio communication
│
├── agentdeck-sandbox-* (sibling sandbox containers)
│   ├── Claude Code + --dangerously-skip-permissions
│   ├── iptables firewall (restricted networking)
│   └── Workspace bind mount (project dir only)
│
└── Traefik (SaltBox reverse proxy)
    └── Routes agentdeck.yourdomain.com → :8420
```

## Dockerfile (Production)

Multi-stage build:

**Stage 1 — Build:**
- Base: `golang:1.24-alpine`
- Copy `go.mod`/`go.sum`, download dependencies
- Copy source, build with `CGO_ENABLED=0` and `-s -w` ldflags
- Output: static binary

**Stage 2 — Runtime:**
- Base: `ubuntu:24.04` (not Alpine — better tmux compatibility, avoids musl/SQLite issues)
- Install: `tmux`, `ca-certificates`, `curl`, `git`
- Create non-root user (`agentdeck`, UID 1000)
- Copy binary from build stage
- Expose port 8420
- Volume mounts: `/data` (SQLite), `/config` (`~/.agent-deck`)
- Entrypoint: `agent-deck web --headless --listen 0.0.0.0:8420`

## docker-compose.yml

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
      - agent-deck-data:/data
      - agent-deck-config:/config
    environment:
      - AGENTDECK_DATA_DIR=/data
      - AGENTDECK_CONFIG_DIR=/config
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.agent-deck.rule=Host(`agentdeck.${DOMAIN}`)"
      - "traefik.http.routers.agent-deck.entrypoints=websecure"
      - "traefik.http.routers.agent-deck.tls.certresolver=cfdns"
      - "traefik.http.services.agent-deck.loadbalancer.server.port=8420"

volumes:
  agent-deck-data:
  agent-deck-config:
```

No `privileged` mode required. Only Docker socket mount needed.

## Sandbox Container Provisioning

When Agent Deck spawns a sandbox container for an agent session:

| Constraint | Implementation |
|---|---|
| Filesystem isolation | Only the project workspace bind-mounted (read-write). No host home directory or system paths. |
| Network restriction | `NET_ADMIN` cap + iptables firewall script (modeled on Anthropic's devcontainer `init-firewall.sh`). Allowlist: API provider domains, npm/pip registries, GitHub. |
| Resource limits | CPU quota (`--cpus`), memory limit (`--memory`), no swap. |
| Security hardening | `--security-opt no-new-privileges`, dropped capabilities, read-only root filesystem where possible. |
| Lifecycle | Auto-remove on stop (`--rm`). Agent Deck tracks state via labels (`agentdeck-sandbox=true`, session ID). |

## MCP Container Provisioning

Two supported patterns:

1. **Direct provisioning** — Agent Deck pulls and runs `docker.io/mcp/<server-name>` images as siblings. Communication via stdio attachment (requires new `Attach` method on `ContainerRuntime`).

2. **Delegate to MCP Gateway** — Agent Deck spawns `docker/mcp-gateway` as a sibling with socket mount. Gateway handles MCP server lifecycle. Agent Deck connects via SSE transport.

Both patterns work through the socket mount. The existing `ContainerRuntime` interface covers CRUD lifecycle; stdio attachment is the only missing capability.

## Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `Dockerfile` | Create | Multi-stage production build |
| `docker-compose.yml` | Create | Compose stack with Traefik labels |
| `.env.example` | Create | Template for DOMAIN and other env vars |
| `.dockerignore` | Create | Exclude .git, build/, .worktrees/, etc. |
| `internal/hub/workspace/runtime.go` | Modify | Extend `CreateOpts` with NetworkConfig, SecurityOpts |
| `internal/hub/workspace/docker.go` | Modify | Apply security defaults, add network self-discovery |

## Out of Scope

- Ansible role / SaltBox role (user integrates manually)
- Multi-user auth (personal instance)
- TLS termination (Traefik handles this)
- Docker Desktop Sandbox (microVM) support (not available on headless Linux)
- CI/CD pipeline (GitHub Actions) — separate concern
- MCP Gateway integration wiring (plumbing exists; follow-on work)

## Research References

- [Docker MCP Catalog and Toolkit](https://docs.docker.com/ai/mcp-catalog-and-toolkit/)
- [Docker MCP Gateway](https://docs.docker.com/ai/mcp-catalog-and-toolkit/mcp-gateway/)
- [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) — microVM product, requires Docker Desktop
- [Claude Code Devcontainer](https://code.claude.com/docs/en/devcontainer) — Anthropic's reference Docker setup
- [Claude Code Sandboxing](https://code.claude.com/docs/en/sandboxing) — Native OS-level sandbox docs
- [docker/mcp-gateway GitHub](https://github.com/docker/mcp-gateway)
