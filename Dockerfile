# Use the official Golang image to create a build artifact.
# This is the builder stage.
FROM golang:1.24-alpine AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod file (required)
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go app
# -ldflags="-w -s" reduces the size of the binary by removing debug information.
# CGO_ENABLED=0 disables CGO for static linking, useful for alpine base images.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o ./haproxy-mcp ./cmd/

# --- Start final stage --- #

# Use a minimal base image like Alpine Linux
FROM docker.io/library/alpine:3.23

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

# Default environment variables for HAProxy connection
ENV HAPROXY_HOST="172.18.62.7"
ENV HAPROXY_PORT="9999"
ENV HAPROXY_RUNTIME_MODE="tcp4"
ENV HAPROXY_RUNTIME_TIMEOUT="10"
ENV HAPROXY_STATS_ENABLED="true"
ENV HAPROXY_STATS_URL="http://172.18.62.6:8404/stats"
ENV HAPROXY_STATS_TIMEOUT="5"
ENV MCP_TRANSPORT="http"
ENV MCP_PORT="8082"
ENV LOG_LEVEL="info"

# Expose port if using HTTP transport
EXPOSE ${MCP_PORT}

# Command to run the executable
ENTRYPOINT ["/app/haproxy-mcp"]
