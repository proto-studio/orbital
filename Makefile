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
	# Prefer Xcode's SDK over Command Line Tools. CLT on macOS 26 ships a
	# libc++ that requires Clang 19+ builtins (__builtin_ctzg/__builtin_clzg);
	# Xcode 16's clang cannot compile against that SDK. Override with
	# DEVELOPER_DIR=... or SDKROOT=... if needed.
	DEVELOPER_DIR ?= /Applications/Xcode.app/Contents/Developer
	export DEVELOPER_DIR
	SDKROOT ?= $(shell DEVELOPER_DIR="$(DEVELOPER_DIR)" xcrun --sdk macosx --show-sdk-path 2>/dev/null)
	export SDKROOT
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
	@if [ "$(HOST_OS)" = "linux" ]; then \
		$(MAKE) build TARGET_OS=linux TARGET_ARCH=x64 BINARY_NAME=orbital-linux-x64; \
	else \
		$(MAKE) docker-build-orbital TARGET_ARCH=x64; \
	fi

.PHONY: build-linux-arm64
build-linux-arm64:
	@echo "Building for Linux ARM64..."
	@if [ "$(HOST_OS)" = "linux" ]; then \
		$(MAKE) build TARGET_OS=linux TARGET_ARCH=arm64 BINARY_NAME=orbital-linux-arm64; \
	else \
		$(MAKE) docker-build-orbital TARGET_ARCH=arm64; \
	fi

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

# Check out the latest stable V8 and build it for every supported platform.
# Resolves the V8 version bundled with the current stable Chrome release, then
# runs v8-all-platforms with that version (which also updates an existing
# checkout via `git checkout` + `gclient sync` in v8-fetch).
.PHONY: v8-latest
v8-latest:
	@ver=$$(scripts/latest-v8-version.sh) && \
	echo ">>> Building all platforms for V8 $$ver" && \
	$(MAKE) v8-all-platforms V8_VERSION=$$ver

# Build V8 for every platform reachable from this host:
#   - macOS host: darwin arm64/x64 natively + linux arm64/x64 via Docker.
#   - Linux host: linux arm64/x64 natively (darwin cannot be built on Linux).
.PHONY: v8-all-platforms
v8-all-platforms:
ifeq ($(HOST_OS),darwin)
	$(MAKE) v8-darwin-arm64 V8_VERSION=$(V8_VERSION)
	$(MAKE) v8-darwin-x64 V8_VERSION=$(V8_VERSION)
	$(MAKE) docker-build-linux V8_VERSION=$(V8_VERSION)
else ifeq ($(HOST_OS),linux)
	$(MAKE) v8-linux-arm64 V8_VERSION=$(V8_VERSION)
	$(MAKE) v8-linux-x64 V8_VERSION=$(V8_VERSION)
else
	@echo "Unsupported host OS '$(HOST_OS)' for all-platform V8 build."; exit 1
endif
	@echo "V8 $(V8_VERSION) built for all reachable platforms. Run 'make v8-list'."

# Platform-specific V8 builds
.PHONY: v8-linux-x64
v8-linux-x64:
	$(MAKE) v8-platform TARGET_OS=linux TARGET_ARCH=x64

.PHONY: v8-linux-arm64
v8-linux-arm64:
	$(MAKE) v8-platform TARGET_OS=linux TARGET_ARCH=arm64

# Build V8 for both Linux architectures (used by docker-build-linux)
.PHONY: v8-all-linux
v8-all-linux: v8-linux-x64 v8-linux-arm64
	@echo "V8 $(V8_VERSION) built for linux-x64 and linux-arm64"

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
		echo 'clang_base_path="/usr/lib/llvm-19"' >> "$$V8_OUT_DIR/args.gn"; \
		echo 'clang_use_chrome_plugins=false' >> "$$V8_OUT_DIR/args.gn"; \
		echo 'clang_version="19"' >> "$$V8_OUT_DIR/args.gn"; \
		echo 'use_lld=true' >> "$$V8_OUT_DIR/args.gn"; \
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
	# Exclude v8-build (chromium checkout) and examples (multi-main demo pkgs).
	$(GO) test $(GOFLAGS) ./pkg/... ./internal/... ./cmd/...

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

# Cap parallelism inside Docker; full host nproc OOMs Desktop VM during -O3.
DOCKER_NUM_JOBS ?= 4

# Shared v8-build/ volume keeps CIPD clients per-CPU; wipe before each arch switch.
define docker-clean-cipd
	rm -rf \
		$(V8_BUILD_DIR)/.cipd \
		$(DEPOT_TOOLS_DIR)/.cipd_bin \
		$(DEPOT_TOOLS_DIR)/.cipd_client \
		$(DEPOT_TOOLS_DIR)/.cipd_client_cache \
		$(DEPOT_TOOLS_DIR)/.versions
endef

define docker-run-v8
	docker run --rm --platform $(1) -v $(CURDIR):/workspace \
		orbital-builder:$(2) bash -lc '\
			rm -rf "$$HOME/.cache/vpython-root"* "$$HOME/.vpython_cipd_cache" "$$HOME/.cipd"; \
			make $(3) V8_VERSION=$(V8_VERSION) NUM_JOBS=$(DOCKER_NUM_JOBS)'
endef

.PHONY: docker-build-linux
docker-build-linux:
	@echo "Building V8 for Linux using Docker (native arch per target)..."
	@# Cross-compiling V8 across CPU arches (arm64 host → x64) is fragile;
	@# build each Linux arch inside a matching Docker platform instead.
	docker build --platform linux/arm64 -t orbital-builder:arm64 -f docker/Dockerfile.builder .
	docker build --platform linux/amd64 -t orbital-builder:amd64 -f docker/Dockerfile.builder .
	@echo ">>> V8 linux-arm64 (native arm64 container)..."
	$(docker-clean-cipd)
	$(call docker-run-v8,linux/arm64,arm64,v8-linux-arm64)
	@echo ">>> V8 linux-x64 (native amd64 container; qemu on Apple Silicon)..."
	$(docker-clean-cipd)
	$(call docker-run-v8,linux/amd64,amd64,v8-linux-x64)
	@echo "V8 Linux builds complete. Run 'make v8-list' to verify."

# Final Orbital binary for Linux. On a Linux host, use `make build` instead —
# this target is only for non-Linux hosts (CGO link needs a Linux toolchain).
# Prebuilt deps/v8/linux-*/libv8_monolith.a is reused; V8 is not rebuilt.
.PHONY: docker-build-orbital
docker-build-orbital:
	@if [ "$(TARGET_ARCH)" = "arm64" ]; then \
		platform=linux/arm64; tag=arm64; \
	else \
		platform=linux/amd64; tag=amd64; \
	fi; \
	echo ">>> Orbital linux-$(TARGET_ARCH) via Docker ($$platform)..."; \
	docker build --platform $$platform -t orbital-builder:$$tag -f docker/Dockerfile.builder .; \
	docker run --rm --platform $$platform -v $(CURDIR):/workspace \
		-e HOME=/tmp \
		orbital-builder:$$tag \
		make build TARGET_OS=linux TARGET_ARCH=$(TARGET_ARCH) \
			BINARY_NAME=orbital-linux-$(TARGET_ARCH)

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
	@echo "  build-linux-amd64  Build for Linux x86_64 (Docker CGO link if not on Linux)"
	@echo "  build-linux-arm64  Build for Linux ARM64 (Docker CGO link if not on Linux)"
	@echo "  build-darwin-arm64 Build for macOS ARM64"
	@echo "  build-darwin-amd64 Build for macOS x86_64"
	@echo ""
	@echo "V8 targets:"
	@echo "  v8-latest        Check out latest stable V8 and build all platforms"
	@echo "  v8-all-platforms Build V8 for all platforms reachable from this host"
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
	@echo "  docker-build-linux    Build V8 for Linux using Docker"
	@echo "  docker-build-orbital  Link Orbital for TARGET_ARCH via Docker (non-Linux hosts)"
	@echo ""
	@echo "Current configuration:"
	@echo "  Host:    $(HOST_OS)/$(HOST_ARCH)"
	@echo "  Target:  $(TARGET_OS)/$(TARGET_ARCH)"
	@echo "  V8:      $(V8_VERSION)"
	@echo "  Jobs:    $(NUM_JOBS)"
	@echo ""
	@echo "Examples:"
	@echo "  make v8-latest              # Check out latest stable V8, build all platforms"
	@echo "  make v8-native              # Build V8 for your machine"
	@echo "  make docker-build-linux     # Build V8 for linux-arm64 + linux-x64"
	@echo "  make build-linux-arm64      # Orbital Linux ARM64 (native on Linux)"
	@echo "  make build-native           # Build Orbital for your machine"
