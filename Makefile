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

# Multi-platform binaries are produced by CI (native runner per platform),
# not locally. Build for your current platform with `build` / `build-native`.

# ============================================================================
# V8 Build Targets
# ============================================================================

# Build V8 for current/native platform
.PHONY: v8-native
v8-native:
	$(MAKE) v8-platform TARGET_OS=$(HOST_OS) TARGET_ARCH=$(HOST_ARCH)

# Check out the latest stable V8 and build it for THIS platform.
# Resolves the V8 version bundled with the current stable Chrome release, then
# builds natively for the host (which also updates an existing checkout via
# `git checkout` + `gclient sync` in v8-fetch). Multi-platform artifacts are
# produced by CI on native runners, not here.
.PHONY: v8-latest
v8-latest:
	@ver=$$(scripts/latest-v8-version.sh) && \
	echo ">>> Building native V8 $$ver for $(HOST_OS)/$(HOST_ARCH)" && \
	$(MAKE) v8-native V8_VERSION=$$ver

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

# Build V8 for target platform.
# Linux always uses Chromium's bundled clang (third_party/llvm-build), which is
# an x86_64 binary that cross-compiles to arm64 (Chromium ships no arm64-linux
# host clang, and system clang is too old for V8 15.x flags). Linux arm64 is
# therefore built on an x86_64 host with target_cpu=arm64 + a Debian sysroot.
# On Linux we also strip Chromium's experimental CREL relocation flag from
# build/config/compiler/BUILD.gn before gn gen. Chromium adds
# -Wa,--crel,--allow-experimental-crel whenever LLD is used, but CREL is only
# understood by LLD / binutils >= 2.44; stock GNU ld (bfd 2.42, on CI runners and
# most consumer machines) reports "unknown architecture". We keep use_lld=true
# (Chromium's archives lack the symbol index GNU ld needs, so host-tool links
# require LLD) and just drop the CREL flag, yielding standard RELA relocations.
# Finally, use_custom_libcxx=true makes V8 reference Chromium's hardened libc++
# (the std::__Cr:: namespace), which lives in a separate libc++.a that
# v8_monolithic does NOT bundle. We ship it as its own libv8_libcxx.a. V8 15.x's
# monolith is itself ~126MB, over GitHub's 100MB per-file limit, so on Linux we
# split it into libv8_monolith_0.a / _1.a (see scripts/split-archive.py) and link
# all parts inside --start-group. Git LFS is intentionally NOT used because
# `go get` / the module proxy do not run LFS smudge, which would break consumers.
# For ABI reasons the cgo C++ glue (pkg/v8/csrc/v8go.cc) is pre-compiled here with
# V8's own clang+libc++ flags into libv8go_glue.a (see scripts/build-glue.py)
# rather than by cgo's system g++.
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
	echo 'use_custom_libcxx=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_sandbox=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'enable_rust=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_temporal_support=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_i18n_support=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'treat_warnings_as_errors=false' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'symbol_level=0' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_webassembly=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_pointer_compression=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'v8_enable_31bit_smis_on_64bit_arch=true' >> "$$V8_OUT_DIR/args.gn" && \
	if [ "$(TARGET_OS)" = "darwin" ]; then \
		echo 'use_xcode_clang=true' >> "$$V8_OUT_DIR/args.gn"; \
	elif [ "$(TARGET_OS)" = "linux" ]; then \
		echo 'clang_use_chrome_plugins=false' >> "$$V8_OUT_DIR/args.gn"; \
		echo 'use_sysroot=true' >> "$$V8_OUT_DIR/args.gn"; \
	fi && \
	if [ "$(HOST_OS)" != "$(TARGET_OS)" ] || [ "$(HOST_ARCH)" != "$(V8_ARCH)" ]; then \
		echo 'target_os="$(TARGET_OS)"' >> "$$V8_OUT_DIR/args.gn"; \
	fi && \
	if [ "$(TARGET_OS)" = "linux" ]; then \
		if [ "$(V8_ARCH)" = "arm64" ]; then sysarch=arm64; else sysarch=amd64; fi; \
		echo ">>> Installing Debian sysroot ($$sysarch) for cross/native build..."; \
		python3 build/linux/sysroot_scripts/install-sysroot.py --arch=$$sysarch; \
		echo ">>> Disabling experimental CREL relocations (stock GNU ld < 2.44 cannot link them)..."; \
		sed -i '/-Wa,--crel,--allow-experimental-crel/d' build/config/compiler/BUILD.gn; \
	fi && \
	echo "GN Args:" && cat "$$V8_OUT_DIR/args.gn" && \
	gn gen "$$V8_OUT_DIR" --export-compile-commands && \
	ninja -C "$$V8_OUT_DIR" v8_monolith -j $(NUM_JOBS) && \
	echo ">>> Building target libc++/libc++abi (the monolith static lib does not compile its link deps)..." && \
	ninja -C "$$V8_OUT_DIR" -j $(NUM_JOBS) \
		obj/buildtools/third_party/libc++/libc++.a \
		obj/buildtools/third_party/libc++abi/libc++abi.a && \
	libcxx="$$(find "$$V8_OUT_DIR/obj" -name 'libc++*.a' 2>/dev/null)" && \
	if [ -z "$$libcxx" ]; then \
		echo "ERROR: use_custom_libcxx=true but no target libc++.a found under $$V8_OUT_DIR/obj;" >&2; \
		echo "       the glue/monolith would ship with undefined std::__Cr:: symbols." >&2; \
		exit 1; \
	fi && \
	echo ">>> Combining Chromium libc++ + libc++abi into libv8_libcxx.a:" && echo "$$libcxx" && \
	if [ "$(TARGET_OS)" = "darwin" ]; then \
		libtool -static -o "$$V8_OUT_DIR/obj/libv8_libcxx.a" $$libcxx; \
	else \
		{ echo "create $$V8_OUT_DIR/obj/libv8_libcxx.a"; \
		  for a in $$libcxx; do echo "addlib $$a"; done; \
		  echo "save"; echo "end"; } | ar -M && \
		ranlib "$$V8_OUT_DIR/obj/libv8_libcxx.a"; \
	fi && \
	if [ "$(TARGET_OS)" = "linux" ]; then \
		echo ">>> Splitting monolith into <100MB parts (GitHub file limit; Git LFS unusable with go get)..."; \
		rm -f $(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8_monolith*.a; \
		python3 $(CURDIR)/scripts/split-archive.py "$$V8_OUT_DIR/obj/libv8_monolith.a" \
			--parts 2 --out-prefix $(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8_monolith; \
	else \
		cp "$$V8_OUT_DIR/obj/libv8_monolith.a" $(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8_monolith.a; \
	fi && \
	cp "$$V8_OUT_DIR/obj/libv8_libcxx.a" $(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8_libcxx.a && \
	cp -R include/* $(CURDIR)/$(V8_PLATFORM_DIR)/include/ && \
	echo ">>> Pre-compiling cgo C++ glue with V8's libc++ ABI..." && \
	python3 $(CURDIR)/scripts/build-glue.py \
		--out-dir "$$(pwd)/$$V8_OUT_DIR" \
		--src "$(CURDIR)/pkg/v8/csrc/v8go.cc" \
		--include "$(CURDIR)/pkg/v8" \
		--include "$$(pwd)/include" \
		--output "$(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8go_glue.a" && \
	echo ">>> Shipped library sizes:" && ls -lh $(CURDIR)/$(V8_PLATFORM_DIR)/lib/*.a && \
	if find $(CURDIR)/$(V8_PLATFORM_DIR)/lib -name '*.a' -size +95M | grep -q .; then \
		echo "ERROR: a shipped archive exceeds ~95MB (GitHub rejects >100MB). Increase --parts in split-archive.py." >&2; \
		find $(CURDIR)/$(V8_PLATFORM_DIR)/lib -name '*.a' -size +95M -exec ls -lh {} \; >&2; \
		exit 1; \
	fi
	@echo ">>> V8 built successfully: $(V8_PLATFORM_DIR)"

# Verify the prebuilt V8 library exists for the target platform.
# cgo selects the matching deps/v8/<os>-<arch> dir automatically via GOOS/GOARCH
# build constraints (see pkg/v8/v8go.go), so no symlink is needed.
# Use the glue archive as the sentinel: it ships on every platform, whereas the
# monolith is split into libv8_monolith_0/_1.a on Linux (see split-archive.py).
.PHONY: check-v8
check-v8:
	@if [ ! -f "$(V8_PLATFORM_DIR)/lib/libv8go_glue.a" ]; then \
		echo "Error: V8 library not found for $(TARGET_OS)/$(TARGET_ARCH)."; \
		echo "Run 'make v8-$(TARGET_OS)-$(TARGET_ARCH)' first."; \
		echo ""; \
		echo "Available V8 builds:"; \
		ls -d deps/v8/*/ 2>/dev/null || echo "  (none)"; \
		exit 1; \
	fi

# Verify the prebuilt V8 library exists for the native (host) platform.
.PHONY: check-v8-native
check-v8-native:
	@if [ ! -f "deps/v8/$(HOST_OS)-$(HOST_ARCH)/lib/libv8go_glue.a" ]; then \
		echo "Error: V8 library not found for native platform $(HOST_OS)/$(HOST_ARCH)."; \
		echo "Run 'make v8-native' first."; \
		exit 1; \
	fi

# List available V8 builds
.PHONY: v8-list
v8-list:
	@echo "Available V8 builds in deps/v8/:"
	@for dir in deps/v8/*/; do \
		name=$$(basename "$$dir"); \
		if [ "$$name" != "current" ] && [ -f "$$dir/lib/libv8go_glue.a" ]; then \
			size=$$(du -sh "$$dir/lib" 2>/dev/null | awk '{print $$1}'); \
			echo "  $$name ($$size total)"; \
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
# Help
# ============================================================================

.PHONY: help
help:
	@echo "Orbital Makefile"
	@echo ""
	@echo "Usage: make [target] [TARGET_OS=linux|darwin] [TARGET_ARCH=x64|arm64]"
	@echo ""
	@echo "Build targets (native / current platform):"
	@echo "  all              Build for current platform (default)"
	@echo "  build            Build for host platform"
	@echo "  build-native     Build for host platform"
	@echo "  release          Build optimized binary"
	@echo "  (multi-platform binaries are produced by CI, not locally)"
	@echo ""
	@echo "V8 targets:"
	@echo "  v8-latest        Check out latest stable V8 and build for THIS platform"
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
	@echo "Current configuration:"
	@echo "  Host:    $(HOST_OS)/$(HOST_ARCH)"
	@echo "  Target:  $(TARGET_OS)/$(TARGET_ARCH)"
	@echo "  V8:      $(V8_VERSION)"
	@echo "  Jobs:    $(NUM_JOBS)"
	@echo ""
	@echo "Examples:"
	@echo "  make v8-latest              # Check out latest stable V8, build for this platform"
	@echo "  make v8-native              # Build V8 for your machine"
	@echo "  make build-native           # Build Orbital for your machine"
