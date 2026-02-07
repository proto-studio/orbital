# GNode Makefile

# Variables
BINARY_NAME := gnode
BUILD_DIR := build
V8_DIR := v8
V8_VERSION := 13.1.201.1
GO := go
GOFLAGS := 

# Platform detection
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

ifeq ($(UNAME_S),Darwin)
	PLATFORM := darwin
else ifeq ($(UNAME_S),Linux)
	PLATFORM := linux
else
	PLATFORM := unknown
endif

ifeq ($(UNAME_M),arm64)
	ARCH := arm64
else ifeq ($(UNAME_M),aarch64)
	ARCH := arm64
else
	ARCH := x64
endif

# Default target
.PHONY: all
all: build

# Build the binary
.PHONY: build
build: check-v8
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/gnode

# Build with optimizations
.PHONY: release
release: check-v8
	$(GO) build $(GOFLAGS) -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/gnode

# Run tests
.PHONY: test
test: check-v8
	$(GO) test $(GOFLAGS) ./...

# Run tests with coverage
.PHONY: test-coverage
test-coverage: check-v8
	$(GO) test $(GOFLAGS) -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run the REPL
.PHONY: run
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

# Run with a script
.PHONY: run-script
run-script: build
	@if [ -z "$(SCRIPT)" ]; then \
		echo "Usage: make run-script SCRIPT=path/to/script.js"; \
		exit 1; \
	fi
	./$(BUILD_DIR)/$(BINARY_NAME) $(SCRIPT)

# Clean build artifacts
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Clean everything including V8
.PHONY: clean-all
clean-all: clean
	rm -rf $(V8_DIR)
	rm -rf v8-build

# Check if V8 is built
.PHONY: check-v8
check-v8:
	@if [ ! -f "$(V8_DIR)/lib/libv8_monolith.a" ]; then \
		echo "Error: V8 library not found. Run 'make v8' first."; \
		exit 1; \
	fi

# Build V8 from source
.PHONY: v8
v8:
	@echo "Building V8 $(V8_VERSION) for $(PLATFORM)/$(ARCH)..."
	./scripts/build-v8.sh -v $(V8_VERSION)

# Download and setup V8 (fetch only, no build)
.PHONY: v8-fetch
v8-fetch:
	@echo "Fetching V8 $(V8_VERSION)..."
	./scripts/build-v8.sh -v $(V8_VERSION) -f

# Install Go dependencies
.PHONY: deps
deps:
	$(GO) mod download
	$(GO) mod tidy

# Format code
.PHONY: fmt
fmt:
	$(GO) fmt ./...

# Lint code
.PHONY: lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

# Vet code
.PHONY: vet
vet:
	$(GO) vet ./...

# Generate any code (placeholder)
.PHONY: generate
generate:
	$(GO) generate ./...

# Install binary to GOPATH/bin
.PHONY: install
install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) $(shell go env GOPATH)/bin/

# Uninstall binary
.PHONY: uninstall
uninstall:
	rm -f $(shell go env GOPATH)/bin/$(BINARY_NAME)

# Show help
.PHONY: help
help:
	@echo "GNode Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build targets:"
	@echo "  all          Build the binary (default)"
	@echo "  build        Build the binary"
	@echo "  release      Build optimized binary"
	@echo "  install      Install binary to GOPATH/bin"
	@echo "  uninstall    Remove binary from GOPATH/bin"
	@echo ""
	@echo "V8 targets:"
	@echo "  v8           Build V8 from source (takes a while)"
	@echo "  v8-fetch     Fetch V8 source without building"
	@echo "  check-v8     Check if V8 is built"
	@echo ""
	@echo "Test targets:"
	@echo "  test         Run tests"
	@echo "  test-coverage Run tests with coverage report"
	@echo ""
	@echo "Run targets:"
	@echo "  run          Build and run REPL"
	@echo "  run-script   Run a script (SCRIPT=path/to/file.js)"
	@echo ""
	@echo "Code quality:"
	@echo "  fmt          Format code"
	@echo "  lint         Run linter"
	@echo "  vet          Run go vet"
	@echo ""
	@echo "Other:"
	@echo "  deps         Download dependencies"
	@echo "  clean        Clean build artifacts"
	@echo "  clean-all    Clean everything including V8"
	@echo "  help         Show this help"
	@echo ""
	@echo "Environment:"
	@echo "  Platform: $(PLATFORM)"
	@echo "  Arch:     $(ARCH)"
	@echo "  V8:       $(V8_VERSION)"
