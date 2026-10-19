.PHONY: all build test vet lint clean run run-bin docker-build docker-run release-snapshot

# Variables
BINARY_NAME ?= $(shell grep -m 1 '^module ' go.mod 2>/dev/null | sed 's/^module github.com\/[^\/]*\/\([^\/]*\).*/\1/' || basename $$(pwd))
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_DIR = bin
# The image gets the date of the commit, not the time of the build: a build argument that changes on
# every call defeats Docker's layer cache and recompiles the program each time (27 s instead of 1 s).
# Source changes still rebuild, since the cache follows the content of the files.
DOCKER_BUILD_DATE ?= $(shell TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")

# Configuration file for run, run-bin and docker-run (see config.example.toml)
CONFIG ?= config.toml
# Docker image name, and the host port the container's port 8080 is published on
IMAGE ?= $(BINARY_NAME)
PORT ?= 8080

# Default target
all: clean build

# Build the binary into bin/, with the version information
build:
	mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.GitVersion=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildDate=$(BUILD_DATE)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd

# Run tests
test:
	go test ./...

vet:
	go vet ./...

# Run linting checks (same as CI)
lint:
	@echo "Running linters..."
	@go mod tidy
	@if ! git diff --quiet go.mod go.sum; then echo "go.mod or go.sum is not tidy, run 'go mod tidy'"; git diff go.mod go.sum; exit 1; fi
	@if ! command -v golangci-lint &> /dev/null; then echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; fi
	@golangci-lint run --timeout=5m

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

# Run from source with $(CONFIG): make run CONFIG=my.toml
run:
	go run ./cmd --config $(CONFIG)

# Run the binary built by 'make build' with $(CONFIG)
run-bin: build
	./$(BUILD_DIR)/$(BINARY_NAME) --config $(CONFIG)

# Build the Docker image, tagged $(IMAGE):$(VERSION) and $(IMAGE):latest
docker-build:
	docker build \
		--build-arg GIT_VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(DOCKER_BUILD_DATE) \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

# Run the image with $(CONFIG) mounted, published on host port $(PORT): make docker-run CONFIG=my.toml PORT=9000
# (inside the container the server always listens on 8080, which the image's health check relies on)
docker-run: docker-build
	docker run --rm -p $(PORT):8080 -v $(abspath $(CONFIG)):/app/config.toml:ro $(IMAGE):$(VERSION)

# Create a release snapshot using GoReleaser
release-snapshot:
	goreleaser release --snapshot --clean
