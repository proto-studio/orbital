#!/bin/bash
set -e

# V8 Build Script for Orbital
# This script builds V8 as a static library for linking with Go
#
# Usage:
#   ./scripts/build-v8.sh                     # Build for current platform
#   ./scripts/build-v8.sh -t linux -a arm64   # Cross-compile for Linux ARM64
#   ./scripts/build-v8.sh -t linux -a x64     # Cross-compile for Linux x86_64
#   ./scripts/build-v8.sh -f                  # Fetch only, don't build

# ============================================================================
# Configuration
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

V8_VERSION="${V8_VERSION:-13.1.201.1}"
BUILD_DIR="${BUILD_DIR:-$(pwd)/v8-build}"
OUTPUT_BASE_DIR="${OUTPUT_BASE_DIR:-$(pwd)/deps/v8}"
DEPOT_TOOLS_DIR="${BUILD_DIR}/depot_tools"
V8_SRC_DIR="${BUILD_DIR}/v8"
NUM_JOBS="${NUM_JOBS:-$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)}"
BUILD_TYPE="${BUILD_TYPE:-release}"

# ============================================================================
# Parse Arguments
# ============================================================================

TARGET_OS=""
TARGET_ARCH=""
FETCH_ONLY=false

print_usage() {
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  -v VERSION    V8 version (default: $V8_VERSION)"
    echo "  -t OS         Target OS: linux, darwin (default: current OS)"
    echo "  -a ARCH       Target arch: x64, arm64 (default: current arch)"
    echo "  -j JOBS       Number of parallel jobs (default: $NUM_JOBS)"
    echo "  -d            Debug build (default: release)"
    echo "  -f            Fetch only, don't build"
    echo "  -h            Show this help"
    echo ""
    echo "Examples:"
    echo "  $0                           # Build for current platform"
    echo "  $0 -t linux -a arm64         # Cross-compile for Linux ARM64"
    echo "  $0 -t linux -a x64           # Cross-compile for Linux x86_64"
    echo "  $0 -v 12.9.202.13            # Build specific V8 version"
}

while getopts "v:t:a:j:dfh" opt; do
    case $opt in
        v) V8_VERSION="$OPTARG" ;;
        t) TARGET_OS="$OPTARG" ;;
        a) TARGET_ARCH="$OPTARG" ;;
        j) NUM_JOBS="$OPTARG" ;;
        d) BUILD_TYPE="debug" ;;
        f) FETCH_ONLY=true ;;
        h) print_usage; exit 0 ;;
        *) print_usage; exit 1 ;;
    esac
done

# ============================================================================
# Detect Host Platform
# ============================================================================

HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
HOST_ARCH="$(uname -m)"

case "$HOST_ARCH" in
    x86_64) HOST_ARCH="x64" ;;
    arm64|aarch64) HOST_ARCH="arm64" ;;
esac

# Default target to host if not specified
TARGET_OS="${TARGET_OS:-$HOST_OS}"
TARGET_ARCH="${TARGET_ARCH:-$HOST_ARCH}"

# V8 arch naming
V8_ARCH="$TARGET_ARCH"

# Output directory for this platform
OUTPUT_DIR="${OUTPUT_BASE_DIR}/${TARGET_OS}-${TARGET_ARCH}"

# ============================================================================
# Print Configuration
# ============================================================================

echo "=== V8 Build Configuration ==="
echo "V8 Version:    $V8_VERSION"
echo "Build Dir:     $BUILD_DIR"
echo "Output Dir:    $OUTPUT_DIR"
echo "Build Type:    $BUILD_TYPE"
echo "Host:          $HOST_OS/$HOST_ARCH"
echo "Target:        $TARGET_OS/$TARGET_ARCH"
echo "Parallel Jobs: $NUM_JOBS"
if [ "$TARGET_OS" != "$HOST_OS" ] || [ "$TARGET_ARCH" != "$HOST_ARCH" ]; then
    echo "Cross-compile: YES"
fi
echo "=============================="

# ============================================================================
# Create Directories
# ============================================================================

mkdir -p "$BUILD_DIR"
mkdir -p "$OUTPUT_DIR/lib"
mkdir -p "$OUTPUT_DIR/include"

# ============================================================================
# Step 1: Install depot_tools
# ============================================================================

echo ""
echo ">>> Step 1: Setting up depot_tools..."
if [ ! -d "$DEPOT_TOOLS_DIR" ]; then
    echo "Cloning depot_tools..."
    git clone https://chromium.googlesource.com/chromium/tools/depot_tools.git "$DEPOT_TOOLS_DIR"
else
    echo "depot_tools already installed"
fi

export PATH="$DEPOT_TOOLS_DIR:$PATH"

# ============================================================================
# Step 2: Fetch V8 Source
# ============================================================================

echo ""
echo ">>> Step 2: Fetching V8 source..."
cd "$BUILD_DIR"

if [ ! -d "$V8_SRC_DIR" ]; then
    echo "Fetching V8..."
    fetch v8
    cd "$V8_SRC_DIR"
else
    echo "V8 source exists, using existing checkout..."
    cd "$V8_SRC_DIR"
fi

# Checkout specific version
echo "Checking out V8 version $V8_VERSION..."
git fetch --tags
git checkout "$V8_VERSION" 2>/dev/null || \
git checkout "branch-heads/${V8_VERSION%.*}" 2>/dev/null || {
    echo "Warning: Could not checkout exact version, using main branch"
    git checkout main
}

# Sync dependencies
echo "Syncing V8 dependencies..."
gclient sync -D

if [ "$FETCH_ONLY" = true ]; then
    echo ""
    echo ">>> Fetch complete (skipping build)"
    exit 0
fi

# ============================================================================
# Step 3: Configure Build
# ============================================================================

echo ""
echo ">>> Step 3: Configuring V8 build..."

BUILD_OUT_DIR="out.gn/${TARGET_OS}-${V8_ARCH}"
mkdir -p "$BUILD_OUT_DIR"

# Generate GN args
cat > "$BUILD_OUT_DIR/args.gn" << GNARGS
# V8 build configuration for Orbital
# Target: ${TARGET_OS}/${V8_ARCH}

is_debug=$([ "$BUILD_TYPE" = "debug" ] && echo "true" || echo "false")
target_cpu="${V8_ARCH}"
v8_target_cpu="${V8_ARCH}"
is_component_build=false
v8_monolithic=true
v8_use_external_startup_data=false
# Chromium's bundled, hardened libc++: modern C++20 (std::bit_cast,
# <source_location>) and required for the V8 sandbox (use_safe_libcxx).
use_custom_libcxx=true
v8_enable_sandbox=true
# Chromium's rustc is often x86_64-only; ARM hosts hit Exec format error.
# Temporal (JS Temporal API) is what pulls Rust into V8 — disable both.
enable_rust=false
v8_enable_temporal_support=false
v8_enable_i18n_support=false
treat_warnings_as_errors=false
symbol_level=0
v8_enable_webassembly=true
v8_enable_pointer_compression=true
v8_enable_31bit_smis_on_64bit_arch=true
GNARGS

# Platform-specific args
if [ "$TARGET_OS" = "darwin" ]; then
    cat >> "$BUILD_OUT_DIR/args.gn" << GNARGS

# macOS settings
use_xcode_clang=true
GNARGS
elif [ "$TARGET_OS" = "linux" ]; then
    cat >> "$BUILD_OUT_DIR/args.gn" << GNARGS

# Linux: Chromium bundled clang (system clang is too old for V8 15.x). It is an
# x86_64 binary that cross-compiles arm64, so use the Chromium Debian sysroot.
clang_use_chrome_plugins=false
use_sysroot=true
GNARGS
fi

# Cross-compilation settings
if [ "$TARGET_OS" != "$HOST_OS" ] || [ "$TARGET_ARCH" != "$HOST_ARCH" ]; then
    cat >> "$BUILD_OUT_DIR/args.gn" << GNARGS

# Cross-compilation settings
target_os="${TARGET_OS}"
GNARGS
fi

# Install the Debian sysroot for the target arch (Linux only).
if [ "$TARGET_OS" = "linux" ]; then
    if [ "$V8_ARCH" = "arm64" ]; then sysarch=arm64; else sysarch=amd64; fi
    echo ">>> Installing Debian sysroot ($sysarch)..."
    python3 build/linux/sysroot_scripts/install-sysroot.py --arch="$sysarch"

    # Strip Chromium's experimental CREL relocation flag. Chromium adds
    # -Wa,--crel,--allow-experimental-crel whenever LLD is used, but CREL is only
    # understood by LLD / binutils >= 2.44. Stock GNU ld (bfd 2.42, on CI runners
    # and most consumer machines) reports "unknown architecture" for such objects.
    # We keep use_lld=true (Chromium's archives lack the symbol index GNU ld needs,
    # so host-tool links require LLD) and only drop the CREL flag, giving standard
    # RELA relocations that any consumer's GNU ld can link.
    echo ">>> Disabling experimental CREL relocations..."
    sed -i '/-Wa,--crel,--allow-experimental-crel/d' build/config/compiler/BUILD.gn
fi

echo "GN Args:"
cat "$BUILD_OUT_DIR/args.gn"
echo ""

# Generate build files (compile_commands.json is used to pre-compile the glue
# with V8's exact flags/ABI, see below).
gn gen "$BUILD_OUT_DIR" --export-compile-commands

# ============================================================================
# Step 4: Build V8
# ============================================================================

echo ""
echo ">>> Step 4: Building V8 (this may take a while)..."
ninja -C "$BUILD_OUT_DIR" v8_monolith -j "$NUM_JOBS"

# use_custom_libcxx=true makes V8 reference Chromium's hardened libc++ (the
# std::__Cr:: namespace). The monolith is a static_library, so GN never compiles
# its link-time deps; libc++ only gets built for the x64 host snapshot toolchain
# (under <toolchain>/obj/...), not the target arch. Explicitly build the target
# libc++/libc++abi so they land under the default toolchain's obj/, then ship them
# as their own libv8_libcxx.a (NOT merged into the monolith: that would push the
# monolith past GitHub's 100MB per-file limit, and Git LFS is unusable because
# `go get` does not smudge LFS pointers). libc++ and libc++abi are mutually
# recursive, so combining them into one archive lets ld resolve their cross-refs
# internally. Linux uses GNU ar (MRI scripts); macOS uses libtool -static.
echo ">>> Building target libc++/libc++abi..."
ninja -C "$BUILD_OUT_DIR" -j "$NUM_JOBS" \
    obj/buildtools/third_party/libc++/libc++.a \
    obj/buildtools/third_party/libc++abi/libc++abi.a
LIBCXX_ARCHIVES="$(find "$BUILD_OUT_DIR/obj" -name 'libc++*.a' 2>/dev/null)"
if [ -z "$LIBCXX_ARCHIVES" ]; then
    echo "ERROR: use_custom_libcxx=true but no target libc++.a found under $BUILD_OUT_DIR/obj;" >&2
    echo "       the glue/monolith would ship with undefined std::__Cr:: symbols." >&2
    exit 1
fi
echo ">>> Combining Chromium libc++ + libc++abi into libv8_libcxx.a:"
echo "$LIBCXX_ARCHIVES"
if [ "$TARGET_OS" = "darwin" ]; then
    libtool -static -o "$BUILD_OUT_DIR/obj/libv8_libcxx.a" $LIBCXX_ARCHIVES
else
    {
        echo "create $BUILD_OUT_DIR/obj/libv8_libcxx.a"
        for a in $LIBCXX_ARCHIVES; do echo "addlib $a"; done
        echo "save"
        echo "end"
    } | ar -M
    ranlib "$BUILD_OUT_DIR/obj/libv8_libcxx.a"
fi
MONOLITH="$BUILD_OUT_DIR/obj/libv8_monolith.a"
LIBCXX="$BUILD_OUT_DIR/obj/libv8_libcxx.a"

# ============================================================================
# Step 5: Copy Output Files
# ============================================================================

echo ""
echo ">>> Step 5: Copying build artifacts..."

# Copy static libraries (separate libc++; monolith split on Linux to stay under
# GitHub's 100MB per-file limit — see scripts/split-archive.py).
if [ "$TARGET_OS" = "linux" ]; then
    echo "Splitting monolith into <100MB parts..."
    rm -f "$OUTPUT_DIR/lib/"libv8_monolith*.a
    python3 "$REPO_ROOT/scripts/split-archive.py" "$MONOLITH" \
        --parts 2 --out-prefix "$OUTPUT_DIR/lib/libv8_monolith"
else
    cp "$MONOLITH" "$OUTPUT_DIR/lib/libv8_monolith.a"
fi
cp "$LIBCXX" "$OUTPUT_DIR/lib/libv8_libcxx.a"

# Copy headers
echo "Copying V8 headers..."
cp -R include/* "$OUTPUT_DIR/include/"

# Pre-compile the cgo C++ glue with V8's own toolchain/libc++ ABI so its std::
# symbols match libv8_monolith.a. Consumers then link libv8go_glue.a with plain
# gcc (no clang, no libc++ headers, no C++ compilation on their side).
echo "Pre-compiling cgo C++ glue with V8's libc++ ABI..."
python3 "$REPO_ROOT/scripts/build-glue.py" \
    --out-dir "$(pwd)/$BUILD_OUT_DIR" \
    --src "$REPO_ROOT/pkg/v8/csrc/v8go.cc" \
    --include "$REPO_ROOT/pkg/v8" \
    --include "$(pwd)/include" \
    --output "$OUTPUT_DIR/lib/libv8go_glue.a"

# Guard against future growth: GitHub rejects any file over 100MB and Git LFS is
# unusable (go get does not smudge LFS pointers). Fail early if a part is too big.
if find "$OUTPUT_DIR/lib" -name '*.a' -size +95M | grep -q .; then
    echo "ERROR: a shipped archive exceeds ~95MB (GitHub rejects >100MB)." >&2
    echo "       Increase --parts in the split-archive.py invocation above." >&2
    find "$OUTPUT_DIR/lib" -name '*.a' -size +95M -exec ls -lh {} \; >&2
    exit 1
fi

# Create pkg-config file
if [ "$TARGET_OS" = "linux" ]; then
    MONOLITH_LIBS="-Wl,--start-group -lv8go_glue -lv8_monolith_0 -lv8_monolith_1 -lv8_libcxx -Wl,--end-group"
else
    MONOLITH_LIBS="-lv8go_glue -lv8_monolith -lv8_libcxx"
fi
cat > "$OUTPUT_DIR/v8.pc" << EOF
prefix=$OUTPUT_DIR
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: V8
Description: V8 JavaScript Engine
Version: $V8_VERSION
Libs: -L\${libdir} $MONOLITH_LIBS
Cflags: -I\${includedir}
EOF

# ============================================================================
# Done
# ============================================================================

echo ""
echo "=== Build Complete ==="
echo "V8 libraries:      $OUTPUT_DIR/lib/ (libv8_monolith.a, libv8_libcxx.a, libv8go_glue.a)"
echo "V8 headers:        $OUTPUT_DIR/include/"
echo "pkg-config file:   $OUTPUT_DIR/v8.pc"
echo ""
echo "Library sizes (must each stay under GitHub's 100MB per-file limit):"
ls -lh "$OUTPUT_DIR/lib/"*.a
echo ""
echo "To build Orbital for this platform:"
echo "  make build TARGET_OS=$TARGET_OS TARGET_ARCH=$TARGET_ARCH"
