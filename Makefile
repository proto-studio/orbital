# Orbital Makefile
# V8 JavaScript Runtime for Go with cross-compilation support

# ============================================================================
# Configuration
# ============================================================================

BINARY_NAME := orbital
BUILD_DIR := build
V8_BUILD_DIR := v8-build
V8_OUTPUT_DIR := deps/v8
# Packaged release artifacts (v8-<goos>-<goarch>.tar.gz + .sha256) land here.
DIST_DIR := dist
V8_VERSION := 15.0.1240245
GO := go
GOFLAGS :=
NUM_JOBS ?= $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

# Release-asset / manifest metadata. CI overrides MODULE_VERSION, SOURCE_COMMIT
# and SOURCE_RUN_ID; the GitHub repo hosting the Releases is proto-studio/orbital.
OWNER ?= proto-studio
REPO ?= orbital
MODULE_VERSION ?= v0.1.0
SOURCE_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
SOURCE_RUN_ID ?=

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

# Host arch uses Go's GOARCH naming (amd64/arm64). V8's own target_cpu uses
# "x64", so V8_ARCH (below) maps amd64 -> x64 for GN args only.
ifeq ($(UNAME_M),arm64)
	HOST_ARCH := arm64
else ifeq ($(UNAME_M),aarch64)
	HOST_ARCH := arm64
else ifeq ($(UNAME_M),x86_64)
	HOST_ARCH := amd64
else
	HOST_ARCH := amd64
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

# Shared V8 public headers (not the full source tree). Populated by
# `make package-v8-headers` / `make v8-headers-setup`. Used by glue-only rebuilds.
V8_INCLUDE_DIR := $(V8_OUTPUT_DIR)/include

# GN output dir for the host/target (has compile_commands.json + the clang the
# glue must be compiled with). Override with V8_OUT_DIR=... when needed.
V8_OUT_DIR ?= $(V8_SRC_DIR)/out.gn/$(TARGET_OS)-$(V8_ARCH)

# ============================================================================
# Default Target
# ============================================================================

.PHONY: all
all: build

# ============================================================================
# Build Targets
# ============================================================================

.PHONY: build
build: check-v8-link
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 \
		$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/orbital

.PHONY: build-native
build-native: check-v8-link
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/orbital

.PHONY: release
release: check-v8-link
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

# Platform-specific V8 builds (dirs/targets use GOARCH naming: amd64/arm64)
.PHONY: v8-linux-amd64
v8-linux-amd64:
	$(MAKE) v8-platform TARGET_OS=linux TARGET_ARCH=amd64

.PHONY: v8-linux-arm64
v8-linux-arm64:
	$(MAKE) v8-platform TARGET_OS=linux TARGET_ARCH=arm64

# Intel macOS (darwin/amd64) is unsupported: current V8 needs the macOS 15+ SDK
# (Apple Silicon only), so there is no v8-darwin-amd64 target.
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
# v8_monolithic does NOT bundle. We ship it as its own libv8_libcxx.a. For ABI
# reasons the cgo C++ glue (pkg/v8/csrc/v8go.cc) is pre-compiled here with V8's
# own clang+libc++ flags into libv8go_glue.a (see scripts/build-glue.py) rather
# than by cgo's system g++. Chromium emits libc++.a/libc++abi.a as LLVM *thin*
# archives; Linux merges them with GNU `ar -M addlib`, but macOS cctools libtool
# cannot read thin archives, so on darwin we repack their underlying .o files.
#
# The three archives (libv8_monolith.a, libv8_libcxx.a, libv8go_glue.a) are NOT
# committed to Git. They are packaged into $(DIST_DIR)/v8-<goos>-<goarch>.tar.gz
# (+ .sha256) and published as GitHub Release assets; consumers fetch them via
# `go generate` (see cmd/v8setup). GitHub Releases allow multi-GB assets, so the
# old 100MB-per-file split is gone — we ship a single monolith again.
# Prerequisite for building. Defaults to v8-fetch so local `make v8-native` /
# `v8-platform` check out + gclient sync before compiling. CI instead runs
# `make v8-fetch` as its OWN step (before the build-output cache is restored, so
# V8's landmines gclient hook has no out.gn dir to clobber), then builds with
# `V8_BUILD_PREREQ=` to skip the now-redundant re-fetch/sync.
V8_BUILD_PREREQ ?= v8-fetch

# Optional compiler wrapper (e.g. sccache) injected as GN's cc_wrapper. Empty by
# default so local builds need no extra tooling. CI sets CC_WRAPPER=sccache to
# get a content-addressed compile cache: object reuse is keyed by preprocessed
# source + flags, so it survives gclient-sync mtime churn and gn-gen reruns that
# defeat plain out.gn caching. When set, precompiled headers are disabled because
# sccache cannot cache PCH-based compiles.
CC_WRAPPER ?=

.PHONY: v8-build
v8-build: v8-deps $(V8_BUILD_PREREQ)
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
	echo 'v8_enable_i18n_support=true' >> "$$V8_OUT_DIR/args.gn" && \
	echo 'icu_use_data_file=false' >> "$$V8_OUT_DIR/args.gn" && \
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
	if [ -n "$(CC_WRAPPER)" ]; then \
		echo 'cc_wrapper="$(CC_WRAPPER)"' >> "$$V8_OUT_DIR/args.gn"; \
		echo 'enable_precompiled_headers=false' >> "$$V8_OUT_DIR/args.gn"; \
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
		objs="$$(find "$$V8_OUT_DIR/obj/buildtools/third_party/libc++" "$$V8_OUT_DIR/obj/buildtools/third_party/libc++abi" -name '*.o' 2>/dev/null)"; \
		if [ -z "$$objs" ]; then echo "ERROR: no libc++/libc++abi object files found under $$V8_OUT_DIR/obj (thin-archive workaround)" >&2; exit 1; fi; \
		libtool -static -o "$$V8_OUT_DIR/obj/libv8_libcxx.a" $$objs; \
	else \
		{ echo "create $$V8_OUT_DIR/obj/libv8_libcxx.a"; \
		  for a in $$libcxx; do echo "addlib $$a"; done; \
		  echo "save"; echo "end"; } | ar -M && \
		ranlib "$$V8_OUT_DIR/obj/libv8_libcxx.a"; \
	fi && \
	cp "$$V8_OUT_DIR/obj/libv8_monolith.a" $(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8_monolith.a && \
	cp "$$V8_OUT_DIR/obj/libv8_libcxx.a" $(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8_libcxx.a && \
	echo ">>> Pre-compiling cgo C++ glue with V8's libc++ ABI..." && \
	python3 $(CURDIR)/scripts/build-glue.py \
		--out-dir "$$(pwd)/$$V8_OUT_DIR" \
		--src "$(CURDIR)/pkg/v8/csrc/v8go.cc" \
		--include "$(CURDIR)/pkg/v8" \
		--include "$$(pwd)/include" \
		--output "$(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8go_glue.a" && \
	echo ">>> Shipped library sizes:" && ls -lh $(CURDIR)/$(V8_PLATFORM_DIR)/lib/*.a
	@echo ">>> V8 built: $(V8_PLATFORM_DIR)"
	@$(MAKE) --no-print-directory package-v8-headers TARGET_OS=$(TARGET_OS) TARGET_ARCH=$(TARGET_ARCH)
	@$(MAKE) --no-print-directory package-v8 TARGET_OS=$(TARGET_OS) TARGET_ARCH=$(TARGET_ARCH)
	@echo ">>> V8 built and packaged successfully for $(TARGET_OS)/$(TARGET_ARCH)"

# Copy V8's public headers (include/) into deps/v8/include and package them as
# dist/v8-headers.tar.gz. Glue-only rebuilds need these headers but not the full
# multi-GB V8 source tree. CI uploads this as the `v8-headers` Actions artifact.
.PHONY: package-v8-headers
package-v8-headers:
	@src="$(V8_SRC_DIR)/include"; \
	if [ ! -f "$$src/v8.h" ]; then \
		echo "Error: $$src/v8.h not found; run 'make v8-fetch' (or a full v8-native) first." >&2; \
		exit 1; \
	fi; \
	mkdir -p "$(V8_INCLUDE_DIR)" "$(V8_PLATFORM_DIR)/include" "$(DIST_DIR)"; \
	echo ">>> Installing V8 headers → $(V8_INCLUDE_DIR)"; \
	rm -rf "$(V8_INCLUDE_DIR)"; mkdir -p "$(V8_INCLUDE_DIR)"; \
	cp -R "$$src"/. "$(V8_INCLUDE_DIR)/"; \
	rm -rf "$(V8_PLATFORM_DIR)/include"; mkdir -p "$(V8_PLATFORM_DIR)/include"; \
	cp -R "$$src"/. "$(V8_PLATFORM_DIR)/include/"; \
	asset="v8-headers.tar.gz"; \
	echo ">>> Packaging $(DIST_DIR)/$$asset"; \
	tar -C "$(V8_OUTPUT_DIR)" -czf "$(DIST_DIR)/$$asset" include; \
	( cd $(DIST_DIR) && shasum -a 256 "$$asset" > "$$asset.sha256" ); \
	echo ">>> Wrote $(DIST_DIR)/$$asset ($$(du -h "$(DIST_DIR)/$$asset" | cut -f1))"; \
	cat "$(DIST_DIR)/$$asset.sha256"

# Install V8 public headers into deps/v8/include from dist/v8-headers.tar.gz.
# Optionally download the CI artifact first: make v8-headers-setup RUN_ID=<id>
.PHONY: v8-headers-setup
v8-headers-setup:
	@mkdir -p $(DIST_DIR)
	@if [ -n "$(RUN_ID)" ]; then \
		echo ">>> Downloading v8-headers artifact from Actions run $(RUN_ID)..."; \
		gh run download "$(RUN_ID)" -n v8-headers -D $(DIST_DIR); \
	fi
	@if [ ! -f "$(DIST_DIR)/v8-headers.tar.gz" ]; then \
		echo "Error: $(DIST_DIR)/v8-headers.tar.gz not found." >&2; \
		echo "  make package-v8-headers          # after a local V8 fetch/build" >&2; \
		echo "  make v8-headers-setup RUN_ID=…   # download CI artifact via gh" >&2; \
		exit 1; \
	fi
	@echo ">>> Extracting headers → $(V8_INCLUDE_DIR)"
	@rm -rf "$(V8_INCLUDE_DIR)"
	@mkdir -p "$(V8_OUTPUT_DIR)"
	@tar -C "$(V8_OUTPUT_DIR)" -xzf "$(DIST_DIR)/v8-headers.tar.gz"
	@test -f "$(V8_INCLUDE_DIR)/v8.h" || { echo "Error: extract did not produce $(V8_INCLUDE_DIR)/v8.h" >&2; exit 1; }
	@echo ">>> Headers ready at $(V8_INCLUDE_DIR) (override discovery with V8_INCLUDE=...)"

# Rebuild only libv8go_glue.a (does NOT rebuild libv8_monolith.a).
# Needs:
#   - V8_OUT_DIR with compile_commands.json (from a prior gn gen / v8-build)
#   - V8 public headers (discovered automatically; see below)
# Header discovery (first hit wins): V8_INCLUDE → deps/v8/include →
# deps/v8/<plat>/include → v8-build/v8/include
.PHONY: v8-glue
v8-glue:
	@inc="$(V8_INCLUDE)"; \
	if [ -z "$$inc" ] && [ -f "$(V8_INCLUDE_DIR)/v8.h" ]; then inc="$(V8_INCLUDE_DIR)"; fi; \
	if [ -z "$$inc" ] && [ -f "$(V8_PLATFORM_DIR)/include/v8.h" ]; then inc="$(V8_PLATFORM_DIR)/include"; fi; \
	if [ -z "$$inc" ] && [ -f "$(V8_SRC_DIR)/include/v8.h" ]; then inc="$(V8_SRC_DIR)/include"; fi; \
	if [ -z "$$inc" ]; then \
		echo "Error: V8 public headers not found." >&2; \
		echo "  make v8-headers-setup RUN_ID=<ci-run>   # from CI artifact" >&2; \
		echo "  make package-v8-headers                 # from local v8-build/v8/include" >&2; \
		echo "  V8_INCLUDE=/path/to/include make v8-glue" >&2; \
		exit 1; \
	fi; \
	if [ ! -f "$(V8_OUT_DIR)/compile_commands.json" ]; then \
		echo "Error: $(V8_OUT_DIR)/compile_commands.json not found." >&2; \
		echo "  Glue rebuild needs a prior GN output dir (compile flags + clang)." >&2; \
		echo "  Run a full 'make v8-native' once, or point V8_OUT_DIR= at an existing out.gn." >&2; \
		exit 1; \
	fi; \
	mkdir -p "$(V8_PLATFORM_DIR)/lib"; \
	echo ">>> Rebuilding glue with headers from $$inc"; \
	echo ">>> Using V8_OUT_DIR=$(V8_OUT_DIR)"; \
	python3 scripts/build-glue.py \
		--out-dir "$(V8_OUT_DIR)" \
		--src "$(CURDIR)/pkg/v8/csrc/v8go.cc" \
		--include "$(CURDIR)/pkg/v8" \
		--include "$$inc" \
		--drop-missing-includes \
		--output "$(CURDIR)/$(V8_PLATFORM_DIR)/lib/libv8go_glue.a"; \
	for d in .v8/*/$(TARGET_OS)-$(TARGET_ARCH)/lib; do \
		[ -d "$$d" ] || continue; \
		echo ">>> Updating installed runtime: $$d/libv8go_glue.a"; \
		cp "$(V8_PLATFORM_DIR)/lib/libv8go_glue.a" "$$d/libv8go_glue.a"; \
	done; \
	echo ">>> Glue rebuilt. Re-link with: make build-native"

# Package a built platform's libraries into a checksum-verified release asset:
#   $(DIST_DIR)/v8-<goos>-<goarch>.tar.gz  (+ .sha256)
# The archive contains only the lib/ directory (consumers never compile V8's
# headers — pkg/v8 uses its own v8go.h). This is what CI uploads and publishes.
# Headers ship separately as dist/v8-headers.tar.gz (see package-v8-headers).
# gzip (not zstd) keeps the consumer-side extractor pure Go stdlib.
.PHONY: package-v8
package-v8:
	@if [ ! -f "$(V8_PLATFORM_DIR)/lib/libv8_monolith.a" ]; then \
		echo "Error: no built libraries for $(TARGET_OS)/$(TARGET_ARCH); run 'make v8-$(TARGET_OS)-$(TARGET_ARCH)' first." >&2; \
		exit 1; \
	fi
	@mkdir -p $(DIST_DIR)
	@asset="v8-$(GOOS)-$(GOARCH).tar.gz"; \
	echo ">>> Packaging $(DIST_DIR)/$$asset"; \
	tar -C "$(V8_PLATFORM_DIR)" -czf "$(DIST_DIR)/$$asset" lib; \
	( cd $(DIST_DIR) && shasum -a 256 "$$asset" > "$$asset.sha256" ); \
	echo ">>> Wrote $(DIST_DIR)/$$asset ($$(du -h "$(DIST_DIR)/$$asset" | cut -f1))"; \
	cat "$(DIST_DIR)/$$asset.sha256"

# Assemble internal/v8dist/manifest.json from the packaged artifacts' checksums
# plus the run metadata. Run after all target tarballs exist in $(DIST_DIR).
.PHONY: v8-manifest
v8-manifest:
	python3 scripts/gen-manifest.py \
		--dist-dir $(DIST_DIR) \
		--v8-version "$(V8_VERSION)" \
		--module-version "$(MODULE_VERSION)" \
		--commit "$(SOURCE_COMMIT)" \
		--run-id "$(SOURCE_RUN_ID)" \
		--owner "$(OWNER)" \
		--repo "$(REPO)" \
		--output internal/v8dist/manifest.json

# ============================================================================
# V8 Runtime Setup (fetch prebuilt libraries into .v8/ + generate link file)
# ============================================================================

# Generated per-target cgo link file that carries the -L/-l flags (gitignored).
V8_LINK_FILE := pkg/v8/zz_generated_v8link_$(GOOS)_$(GOARCH).go

# Fetch the version-pinned V8 libraries from the published GitHub Release into
# .v8/ and write the per-target cgo link file. This is what consumers run via
# `go generate`; use it once a Release for the pinned module version exists.
.PHONY: v8-setup
v8-setup:
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) run ./cmd/v8setup -source release -link-out pkg/v8 -link-pkg v8

# Install the V8 libraries from a locally built asset (dist/) instead of a
# Release. Use after `make v8-native` (or `make v8-<target>`) during development
# or before any Release exists.
.PHONY: v8-setup-local
v8-setup-local:
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) run ./cmd/v8setup -source local -local-dir $(DIST_DIR) -link-out pkg/v8 -link-pkg v8

# Verify the V8 runtime + generated link file are present for the target.
.PHONY: check-v8-link
check-v8-link:
	@if [ ! -f "$(V8_LINK_FILE)" ]; then \
		echo "Error: V8 link file $(V8_LINK_FILE) not found for $(GOOS)/$(GOARCH)."; \
		echo ""; \
		echo "Fetch the prebuilt libraries and generate the link file first:"; \
		echo "  make v8-setup                 # from the published GitHub Release"; \
		echo "  make v8-native v8-setup-local # build locally, then install from dist/"; \
		exit 1; \
	fi

# List installed V8 runtimes under .v8/
.PHONY: v8-list
v8-list:
	@echo "Installed V8 runtimes in .v8/:"
	@for dir in .v8/*/*/; do \
		[ -f "$$dir/lib/libv8go_glue.a" ] || continue; \
		size=$$(du -sh "$$dir/lib" 2>/dev/null | awk '{print $$1}'); \
		echo "  $$dir ($$size total)"; \
	done 2>/dev/null || echo "  (none installed yet)"

# ============================================================================
# Test Targets
# ============================================================================

.PHONY: test
test: check-v8-link
	# Exclude v8-build (chromium checkout) and examples (multi-main demo pkgs).
	$(GO) test $(GOFLAGS) ./pkg/... ./internal/... ./cmd/...

.PHONY: coverage
coverage: check-v8-link
	$(GO) test -coverpkg=./pkg/...,./internal/... -coverprofile=coverage.out ./pkg/... ./internal/...
	$(GO) tool cover -func coverage.out

.PHONY: coverage-html
coverage-html: coverage
	$(GO) tool cover -html=coverage.out

# Directory holding the core-package regression suites.
CORE_PKG_DIR := tests/core-packages

# Run the core-package regression suites described by
# $(CORE_PKG_DIR)/manifest.json against the freshly built native binary. The
# runner clones each upstream project from GitHub (at a pinned ref), installs
# and builds it with the host toolchain, then runs its own test suite with
# Orbital (exposed to test commands as $$ORBITAL). Fails if any package fails.
#
# Options:
#   PKG=<name>        run only the named manifest package
#   CORE_PKG_ARGS=... extra flags passed through (e.g. --offline --skip-install)
.PHONY: test-core-packages
test-core-packages: build-native
	python3 scripts/run-core-package-tests.py \
		--binary "$(CURDIR)/$(BUILD_DIR)/$(BINARY_NAME)" \
		$(if $(PKG),--package $(PKG),) $(CORE_PKG_ARGS)

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
	rm -rf $(DIST_DIR)
	rm -f coverage.out coverage.html
	rm -f $(BINARY_NAME)

# Remove the fetched V8 runtime and generated cgo link files (consumer layout).
.PHONY: clean-v8-runtime
clean-v8-runtime:
	rm -rf .v8
	rm -f pkg/v8/zz_generated_v8link_*.go

.PHONY: clean-v8
clean-v8:
	rm -rf deps/v8

.PHONY: clean-v8-build
clean-v8-build:
	rm -rf $(V8_BUILD_DIR)

.PHONY: clean-all
clean-all: clean clean-v8 clean-v8-runtime clean-v8-build

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
	@echo "V8 runtime setup (consumers / developers):"
	@echo "  v8-setup         Fetch pinned V8 libraries from the Release into .v8/ + link file"
	@echo "  v8-setup-local   Install V8 libraries from a locally built dist/ asset + link file"
	@echo "  v8-list          List installed V8 runtimes under .v8/"
	@echo ""
	@echo "V8 build targets (maintainers; produces dist/ release assets):"
	@echo "  v8-latest        Check out latest stable V8 and build for THIS platform"
	@echo "  v8-native        Build V8 for host platform"
	@echo "  v8-linux-amd64   Build V8 for Linux x86_64"
	@echo "  v8-linux-arm64   Build V8 for Linux ARM64"
	@echo "  v8-darwin-arm64  Build V8 for macOS ARM64 (Apple Silicon)"
	@echo "  v8-glue          Rebuild ONLY libv8go_glue.a (needs headers + out.gn)"
	@echo "  package-v8       Package a built platform into dist/v8-<goos>-<goarch>.tar.gz"
	@echo "  package-v8-headers  Package V8 public headers → dist/v8-headers.tar.gz"
	@echo "  v8-headers-setup Install headers into deps/v8/include (RUN_ID=… to download)"
	@echo "  v8-manifest      Assemble internal/v8dist/manifest.json from dist/"
	@echo "  v8-fetch         Fetch V8 source only"
	@echo "  v8-deps          Install depot_tools"
	@echo ""
	@echo "Test targets:"
	@echo "  test               Run Go tests"
	@echo "  test-core-packages Clone core npm packages (manifest.json) and run their"
	@echo "                     suites on Orbital (PKG=<name> for one)"
	@echo "  coverage           Run tests with coverage summary"
	@echo "  coverage-html      Run tests and open HTML coverage report"
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
	@echo "  clean            Clean build/dist artifacts"
	@echo "  clean-v8         Clean built V8 deps (keep source)"
	@echo "  clean-v8-runtime Clean fetched .v8/ runtime + generated link files"
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
	@echo "  make v8-glue                # Rebuild glue only (after editing v8go.cc)"
	@echo "  make build-native           # Build Orbital for your machine"
