# Orbital Makefile
# V8 JavaScript Runtime for Go with cross-compilation support

# ============================================================================
# Configuration
# ============================================================================

BINARY_NAME := orbital
BUILD_DIR := build
V8_BUILD_DIR := v8-build
V8_OUTPUT_DIR := deps/v8
V8_VERSION := 13.1.201.1
GO := go
GOFLAGS :=
NUM_JOBS ?= $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

# Depot tools location
DEPOT_TOOLS_DIR := $(V8_BUILD_DIR)/depot_tools
V8_SRC_DIR := $(V8_BUILD_DIR)/v8

# ============================================================================
# Platform Detection
# ============================================================================

UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# Host platform
ifeq ($(UNAME_S),Darwin)
	HOST_OS := darwin
else ifeq ($(UNAME_S),Linux)
	HOST_OS := linux
else
	HOST_OS := unknown
endif

ifeq ($(UNAME_M),arm64)
	HOST_ARCH := arm64
else ifeq ($(UNAME_M),aarch64)
	HOST_ARCH := arm64
else ifeq ($(UNAME_M),x86_64)
	HOST_ARCH := x64
else
	HOST_ARCH := x64
endif

# Default target platform (same as host)
TARGET_OS ?= $(HOST_OS)
TARGET_ARCH ?= $(HOST_ARCH)

# V8 arch naming
ifeq ($(TARGET_ARCH),arm64)
	V8_ARCH := arm64
else
	V8_ARCH := x64
endif

# ============================================================================
# Cross-compilation Configuration
# ============================================================================

# Go cross-compilation settings
ifeq ($(TARGET_OS),linux)
	GOOS := linux
else ifeq ($(TARGET_OS),darwin)
	GOOS := darwin
endif

ifeq ($(TARGET_ARCH),arm64)
	GOARCH := arm64
else
	GOARCH := amd64
endif

# Platform-specific V8 output directory
V8_PLATFORM_DIR := $(V8_OUTPUT_DIR)/$(TARGET_OS)-$(TARGET_ARCH)

# ============================================================================
# Default Target
# ============================================================================

.PHONY: all
all: build

# ============================================================================
# Build Targets
# ============================================================================

.PHONY: build
build: check-v8
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 \
		$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/orbital

.PHONY: build-native
build-native: check-v8-native
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/orbital

.PHONY: release
release: check-v8
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 \
		$(GO) build $(GOFLAGS) -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/orbital

# Build for all supported platforms
.PHONY: build-all
build-all: build-linux-amd64 build-linux-arm64
	@echo "Built binaries for all platforms in $(BUILD_DIR)/"

.PHONY: build-linux-amd64
build-linux-amd64:
	@echo "Building for Linux x86_64..."
	$(MAKE) build TARGET_OS=linux TARGET_ARCH=x64

.PHONY: build-linux-arm64
build-linux-arm64:
	@echo "Building for Linux ARM64..."
	$(MAKE) build TARGET_OS=linux TARGET_ARCH=arm64

.PHONY: build-darwin-arm64
build-darwin-arm64:
	@echo "Building for macOS ARM64..."
	$(MAKE) build TARGET_OS=darwin TARGET_ARCH=arm64

.PHONY: build-darwin-amd64
build-darwin-amd64:
	@echo "Building for macOS x86_64..."
	$(MAKE) build TARGET_OS=darwin TARGET_ARCH=x64

# ============================================================================
# V8 Build Targets
# ============================================================================

# Build V8 for all platforms
.PHONY: v8
v8: v8-linux-x64 v8-linux-arm64 v8-darwin-x64 v8-darwin-arm64
	@echo "V8 $(V8_VERSION) built for all platforms"

# Build V8 for current/native platform
.PHONY: v8-native
v8-native:
	$(MAKE) v8-platform TARGET_OS=$(HOST_OS) TARGET_ARCH=$(HOST_ARCH)

# Platform-specific V8 builds
.PHONY: v8-linux-x64
v8-linux-x64:
	$(MAKE) v8-platform TARGET_OS=linux TARGET_ARCH=x64

.PHONY: v8-linux-arm64
v8-linux-arm64:
	$(MAKE) v8-platform TARGET_OS=linux TARGET_ARCH=arm64

.PHONY: v8-darwin-x64
v8-darwin-x64:
	$(MAKE) v8-platform TARGET_OS=darwin TARGET_ARCH=x64

.PHONY: v8-darwin-arm64
v8-darwin-arm64:
	$(MAKE) v8-platform TARGET_OS=darwin TARGET_ARCH=arm64

# Internal target to build V8 for a specific platform
.PHONY: v8-platform
v8-platform: v8-deps v8-fetch v8-build
	@echo "V8 $(V8_VERSION) built for $(TARGET_OS)/$(TARGET_ARCH)"

# Install depot_tools
.PHONY: v8-deps
v8-deps:
	@mkdir -p $(V8_BUILD_DIR)
	@if [ ! -d "$(DEPOT_TOOLS_DIR)" ]; then \
		echo ">>> Installing depot_tools..."; \
		git clone https://chromium.googlesource.com/chromium/tools/depot_tools.git $(DEPOT_TOOLS_DIR); \
	else \
		echo ">>> depot_tools already installed"; \
	fi

# Fetch V8 source
.PHONY: v8-fetch
v8-fetch: v8-deps
	@echo ">>> Fetching V8 $(V8_VERSION)..."
	@export PATH="$(CURDIR)/$(DEPOT_TOOLS_DIR):$$PATH"; \
	cd $(V8_BUILD_DIR) && \
	if [ ! -d "v8" ]; then \
		echo "Fetching V8 source..."; \
		fetch v8; \
	fi && \
	cd v8 && \
	echo "Checking out V8 $(V8_VERSION)..." && \
	git fetch --tags && \
	(git checkout $(V8_VERSION) || git checkout "branch-heads/$${V8_VERSION%.*}" || git checkout main) && \
	echo "Syncing dependencies..." && \
	gclient sync -D

# Build V8 for target platform
.PHONY: v8-build
v8-build: v8-fetch
	@echo ">>> Building V8 for $(TARGET_OS)/$(TARGET_ARCH)..."
	@mkdir -p $(V8_PLATFORM_DIR)/lib $(V8_PLATFORM_DIR)/include
	@export PATH="$(CURDIR)/$(DEPOT_TOOLS_DIR):$$PATH"; \
	cd $(V8_SRC_DIR) && \
	V8_OUT_DIR="out.gn/$(TARGET_OS)-$(V8_ARCH)" && \
	mkdir -p "$$V8_OUT_DIR" && \
	echo "Writing GN args..." && \
	echo 'is_debug=false' > "$$V8_OUT_DIR/args.gn" && \
	echo 'target_cpu="$(V8_ARCH)"' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_target_cpu="$(V8_ARCH)"' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'is_component_build=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_monolithic=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_use_external_startup_data=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'use_custom_libcxx=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_i18n_support=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'treat_warnings_as_errors=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'symbol_level=0' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_webassembly=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_pointer_compression=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_31bit_smis_on_64bit_arch=true' >> "$$V8_OUT_DIR/args.gn" && \
	if [ "$(TARGET_OS)" = "darwin" ]; then \
		echo 'use_xcode_clang=true' >> "$$V8_OUT_DIR/args.gn"; \
	elif [ "$(TARGET_OS)" = "linux" ]; then \
		echo 'is_clang=true' >> "$$V8_OUT_DIR/args.gn"; \
		echo 'use_sysroot=false' >> "$$V8_OUT_DIR/args.gn"; \
	fi && \
	if [ "$(HOST_OS)" != "$(TARGET_OS)" ] || [ "$(HOST_ARCH)" != "$(V8_ARCH)" ]; then \
		echo 'target_os="$(TARGET_OS)"' >> "$$V8_OUT_DIR/args.gn"; \
	fi && \
	echo "GN Args:" && cat "$$V8_OUT_DIR/args.gn" && \
	gn gen "$$V8_OUT_DIR" && \
	ninja -C "$$V8_OUT_DIR" v8_monolith -j $(NUM_JOBS) && \
	cp "$$V8_OUT_DIR/obj/libv8_monolith.a" $(CURDIR)/$(V8_PLATFORM_DIR)/lib/ && \
	cp -R include/* $(CURDIR)/$(V8_PLATFORM_DIR)/include/
	@echo ">>> V8 built successfully: $(V8_PLATFORM_DIR)"

# Check if V8 is built for target platform and setup symlink
.PHONY: check-v8
check-v8:
	@if [ ! -f "$(V8_PLATFORM_DIR)/lib/libv8_monolith.a" ]; then \
		echo "Error: V8 library not found for $(TARGET_OS)/$(TARGET_ARCH)."; \
		echo "Run 'make v8 TARGET_OS=$(TARGET_OS) TARGET_ARCH=$(TARGET_ARCH)' first."; \
		echo ""; \
		echo "Available V8 builds:"; \
		ls -d deps/v8/*/ 2>/dev/null | grep -v current || echo "  (none)"; \
		exit 1; \
	fi
	@rm -f deps/v8/current
	@ln -s $(TARGET_OS)-$(TARGET_ARCH) deps/v8/current
	@echo "V8 symlink: deps/v8/current -> $(TARGET_OS)-$(TARGET_ARCH)"

# Check if V8 is built for native platform and setup symlink
.PHONY: check-v8-native
check-v8-native:
	@if [ ! -f "deps/v8/$(HOST_OS)-$(HOST_ARCH)/lib/libv8_monolith.a" ]; then \
		echo "Error: V8 library not found for native platform $(HOST_OS)/$(HOST_ARCH)."; \
		echo "Run 'make v8-native' first."; \
		exit 1; \
	fi
	@rm -f deps/v8/current
	@ln -s $(HOST_OS)-$(HOST_ARCH) deps/v8/current
	@echo "V8 symlink: deps/v8/current -> $(HOST_OS)-$(HOST_ARCH)"

# List available V8 builds
.PHONY: v8-list
v8-list:
	@echo "Available V8 builds in deps/v8/:"
	@for dir in deps/v8/*/; do \
		name=$$(basename "$$dir"); \
		if [ "$$name" != "current" ] && [ -f "$$dir/lib/libv8_monolith.a" ]; then \
			size=$$(ls -lh "$$dir/lib/libv8_monolith.a" | awk '{print $$5}'); \
			echo "  $$name ($$size)"; \
		fi; \
	done || echo "  (none built yet)"

# ============================================================================
# Test Targets
# ============================================================================

.PHONY: test
test: check-v8-native
	$(GO) test $(GOFLAGS) ./...

.PHONY: coverage
coverage: check-v8-native
	$(GO) test -coverpkg=./pkg/...,./internal/... -coverprofile=coverage.out ./pkg/... ./internal/nodejs/...
	$(GO) tool cover -func coverage.out

.PHONY: coverage-html
coverage-html: coverage
	$(GO) tool cover -html=coverage.out

# ============================================================================
# Run Targets
# ============================================================================

.PHONY: run
run: build-native
	./$(BUILD_DIR)/$(BINARY_NAME)

.PHONY: run-script
run-script: build-native
	@if [ -z "$(SCRIPT)" ]; then \
		echo "Usage: make run-script SCRIPT=path/to/script.js"; \
		exit 1; \
	fi
	./$(BUILD_DIR)/$(BINARY_NAME) $(SCRIPT)

# ============================================================================
# Clean Targets
# ============================================================================

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	rm -f $(BINARY_NAME)

.PHONY: clean-v8
clean-v8:
	rm -rf deps/v8

.PHONY: clean-v8-build
clean-v8-build:
	rm -rf $(V8_BUILD_DIR)

.PHONY: clean-all
clean-all: clean clean-v8 clean-v8-build

# ============================================================================
# Code Quality
# ============================================================================

.PHONY: deps
deps:
	$(GO) mod download
	$(GO) mod tidy

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: generate
generate:
	$(GO) generate ./...

# ============================================================================
# Install Targets
# ============================================================================

.PHONY: install
install: build-native
	cp $(BUILD_DIR)/$(BINARY_NAME) $(shell go env GOPATH)/bin/

.PHONY: uninstall
uninstall:
	rm -f $(shell go env GOPATH)/bin/$(BINARY_NAME)

# ============================================================================
# Docker Targets (for cross-compilation)
# ============================================================================

.PHONY: docker-build-linux
docker-build-linux:
	@echo "Building V8 for Linux using Docker..."
	docker build -t orbital-builder -f docker/Dockerfile.builder .
	docker run --rm -v $(CURDIR):/workspace orbital-builder make v8-all-linux

# ============================================================================
# Help
# ============================================================================

.PHONY: help
help:
	@echo "Orbital Makefile"
	@echo ""
	@echo "Usage: make [target] [TARGET_OS=linux|darwin] [TARGET_ARCH=x64|arm64]"
	@echo ""
	@echo "Build targets:"
	@echo "  all              Build for current/specified platform (default)"
	@echo "  build            Build for TARGET_OS/TARGET_ARCH"
	@echo "  build-native     Build for host platform"
	@echo "  release          Build optimized binary"
	@echo "  build-all        Build for all supported platforms"
	@echo "  build-linux-amd64  Build for Linux x86_64"
	@echo "  build-linux-arm64  Build for Linux ARM64"
	@echo "  build-darwin-arm64 Build for macOS ARM64"
	@echo "  build-darwin-amd64 Build for macOS x86_64"
	@echo ""
	@echo "V8 targets:"
	@echo "  v8               Build V8 for all platforms"
	@echo "  v8-native        Build V8 for host platform"
	@echo "  v8-linux-x64     Build V8 for Linux x86_64"
	@echo "  v8-linux-arm64   Build V8 for Linux ARM64"
	@echo "  v8-darwin-x64    Build V8 for macOS x86_64"
	@echo "  v8-darwin-arm64  Build V8 for macOS ARM64"
	@echo "  v8-fetch         Fetch V8 source only"
	@echo "  v8-deps          Install depot_tools"
	@echo "  v8-list          List available V8 builds"
	@echo ""
	@echo "Test targets:"
	@echo "  test             Run tests"
	@echo "  coverage         Run tests with coverage summary"
	@echo "  coverage-html    Run tests and open HTML coverage report"
	@echo ""
	@echo "Run targets:"
	@echo "  run              Build and run REPL"
	@echo "  run-script       Run a script (SCRIPT=path/to/file.js)"
	@echo ""
	@echo "Code quality:"
	@echo "  fmt              Format code"
	@echo "  lint             Run linter"
	@echo "  vet              Run go vet"
	@echo "  deps             Download dependencies"
	@echo ""
	@echo "Clean targets:"
	@echo "  clean            Clean build artifacts"
	@echo "  clean-v8         Clean V8 deps (keep source)"
	@echo "  clean-v8-build   Clean V8 build cache"
	@echo "  clean-all        Clean everything"
	@echo ""
	@echo "Install targets:"
	@echo "  install          Install binary to GOPATH/bin"
	@echo "  uninstall        Remove binary from GOPATH/bin"
	@echo ""
	@echo "Docker targets:"
	@echo "  docker-build-linux  Build V8 for Linux using Docker"
	@echo ""
	@echo "Current configuration:"
	@echo "  Host:    $(HOST_OS)/$(HOST_ARCH)"
	@echo "  Target:  $(TARGET_OS)/$(TARGET_ARCH)"
	@echo "  V8:      $(V8_VERSION)"
	@echo "  Jobs:    $(NUM_JOBS)"
	@echo ""
	@echo "Examples:"
	@echo "  make v8-native              # Build V8 for your machine"
	@echo "  make v8                     # Build V8 for all platforms"
	@echo "  make v8-linux-arm64         # Build V8 for Linux ARM64"
	@echo "  make build-native           # Build Orbital for your machine"
