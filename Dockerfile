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
