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
use_custom_libcxx=false
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

# Linux settings
is_clang=true
use_sysroot=false
GNARGS
fi

# Cross-compilation settings
if [ "$TARGET_OS" != "$HOST_OS" ] || [ "$TARGET_ARCH" != "$HOST_ARCH" ]; then
    cat >> "$BUILD_OUT_DIR/args.gn" << GNARGS

# Cross-compilation settings
target_os="${TARGET_OS}"
GNARGS

    # ARM64 cross-compilation on Linux
    if [ "$HOST_OS" = "linux" ] && [ "$TARGET_ARCH" = "arm64" ] && [ "$HOST_ARCH" = "x64" ]; then
        cat >> "$BUILD_OUT_DIR/args.gn" << GNARGS

# ARM64 cross-compilation
use_sysroot=false
GNARGS
    fi
fi

echo "GN Args:"
cat "$BUILD_OUT_DIR/args.gn"
echo ""

# Generate build files
gn gen "$BUILD_OUT_DIR"

# ============================================================================
# Step 4: Build V8
# ============================================================================

echo ""
echo ">>> Step 4: Building V8 (this may take a while)..."
ninja -C "$BUILD_OUT_DIR" v8_monolith -j "$NUM_JOBS"

# ============================================================================
# Step 5: Copy Output Files
# ============================================================================

echo ""
echo ">>> Step 5: Copying build artifacts..."

# Copy static library
cp "$BUILD_OUT_DIR/obj/libv8_monolith.a" "$OUTPUT_DIR/lib/"

# Copy headers
echo "Copying V8 headers..."
cp -R include/* "$OUTPUT_DIR/include/"

# Create pkg-config file
cat > "$OUTPUT_DIR/v8.pc" << EOF
prefix=$OUTPUT_DIR
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: V8
Description: V8 JavaScript Engine
Version: $V8_VERSION
Libs: -L\${libdir} -lv8_monolith
Cflags: -I\${includedir}
EOF

# ============================================================================
# Done
# ============================================================================

echo ""
echo "=== Build Complete ==="
echo "V8 static library: $OUTPUT_DIR/lib/libv8_monolith.a"
echo "V8 headers:        $OUTPUT_DIR/include/"
echo "pkg-config file:   $OUTPUT_DIR/v8.pc"
echo ""
echo "Library size:"
ls -lh "$OUTPUT_DIR/lib/libv8_monolith.a"
echo ""
echo "To build Orbital for this platform:"
echo "  make build TARGET_OS=$TARGET_OS TARGET_ARCH=$TARGET_ARCH"
