# Use the official Golang image to create a build artifact.
# This is the builder stage.
FROM golang:1.25-alpine AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod file (required)
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY . .

ARG BUILD_DATE=unknown
ARG GIT_COMMIT=none
ARG GIT_VERSION=dev
ARG TARGETOS
ARG TARGETARCH

# Build the Go app
# -ldflags="-s -w" reduces the size of the binary by removing debug information.
# CGO_ENABLED=0 disables CGO for static linking, useful for alpine base images.
RUN echo "Building version: ${GIT_VERSION}" && \
    echo "Commit: ${GIT_COMMIT}" && \
    echo "Build date: ${BUILD_DATE}" && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -trimpath \
      -o ./haproxy-mcp \
      -ldflags="-s -w \
        -X main.GitVersion=${GIT_VERSION} \
        -X main.GitCommit=${GIT_COMMIT} \
        -X main.BuildDate=${BUILD_DATE}" \
       ./cmd/

# --- Start final stage --- #

# Use a minimal base image like Alpine Linux
FROM docker.io/library/alpine:3.23

ARG BUILD_DATE=unknown
ARG GIT_COMMIT=none
ARG GIT_VERSION=dev

LABEL org.opencontainers.image.created=$BUILD_DATE \
      org.opencontainers.image.revision=$GIT_COMMIT \
      org.opencontainers.image.version=$GIT_VERSION \
      org.opencontainers.image.title="haproxy-mcp" \
      org.opencontainers.image.source="https://github.com/bondar-aleksandr/haproxy-mcp-server" \
      org.opencontainers.image.description="HAProxy MCP server"

RUN addgroup -g 1010 haproxy-mcp && \
    adduser -S -u 1010 -G haproxy-mcp -D haproxy-mcp

# Add ca-certificates in case TLS connections need system CAs
# Add socat for fallback connection method to HAProxy
RUN apk --no-cache add ca-certificates socat

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy the built binary from the builder stage
COPY --from=builder /app/haproxy-mcp .

RUN chown -R haproxy-mcp:haproxy-mcp /app
USER haproxy-mcp

# Expose port if using HTTP transport
EXPOSE 8080

# Liveness: /healthz answers while the process is up and serving; it does not depend on HAProxy being
# reachable (instances are retried in the background and the server runs with none available).
# The container is expected to listen on the default port 8080 (the HTTP transport): map it to any
# port outside with -p. --start-period covers the startup connection attempts to unreachable instances.
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

# Command to run the executable
ENTRYPOINT ["/app/haproxy-mcp"]
CMD ["--config", "/app/config.toml"]
