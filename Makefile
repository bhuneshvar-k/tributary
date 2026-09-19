.PHONY: build run tidy test clean dev snapshot release

# Version information - override with: make build VERSION=v1.0.0
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
BUILT_BY ?= make

# Build flags for version embedding
LDFLAGS := -X github.com/bhuneshvar-k/tributary/internal/version.version=$(VERSION) \
           -X github.com/bhuneshvar-k/tributary/internal/version.commit=$(COMMIT) \
           -X github.com/bhuneshvar-k/tributary/internal/version.date=$(DATE) \
           -X github.com/bhuneshvar-k/tributary/internal/version.builtBy=$(BUILT_BY)

# Development build
build:
	go build -ldflags "$(LDFLAGS)" -o bin/tributary ./cmd/tributary

# Build for release (strips debug info for smaller binary)
build-release:
	CGO_ENABLED=0 go build -ldflags "-s -w $(LDFLAGS)" -o bin/tributary ./cmd/tributary

# Development build with debug info (no ldflags)
dev:
	go build -o bin/tributary ./cmd/tributary

run: build
	./bin/tributary

version: build
	./bin/tributary --version

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf bin/
	rm -rf dist/

# GoReleaser targets
snapshot:
	goreleaser build --snapshot --clean

release:
	goreleaser release --clean

# Check GoReleaser config
check:
	goreleaser check
