#!/bin/bash
set -e

# V8 Build Script for Go Integration
# This script clones and compiles V8 as a static library for linking with Go

# Configuration
V8_VERSION="${V8_VERSION:-12.9.202.13}"  # Stable version, update as needed
BUILD_DIR="${BUILD_DIR:-$(pwd)/v8-build}"
OUTPUT_DIR="${OUTPUT_DIR:-$(pwd)/v8}"
DEPOT_TOOLS_DIR="${BUILD_DIR}/depot_tools"
V8_SRC_DIR="${BUILD_DIR}/v8"
NUM_JOBS="${NUM_JOBS:-$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4)}"

# Build type: release or debug
BUILD_TYPE="${BUILD_TYPE:-release}"

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64)
        V8_ARCH="x64"
        ;;
    arm64|aarch64)
        V8_ARCH="arm64"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

echo "=== V8 Build Configuration ==="
echo "V8 Version:    $V8_VERSION"
echo "Build Dir:     $BUILD_DIR"
echo "Output Dir:    $OUTPUT_DIR"
echo "Build Type:    $BUILD_TYPE"
echo "OS:            $OS"
echo "Architecture:  $V8_ARCH"
echo "Parallel Jobs: $NUM_JOBS"
echo "=============================="

# Create directories
mkdir -p "$BUILD_DIR"
mkdir -p "$OUTPUT_DIR/lib"
mkdir -p "$OUTPUT_DIR/include"

# Step 1: Install/Update depot_tools
echo ""
echo ">>> Step 1: Setting up depot_tools..."
if [ ! -d "$DEPOT_TOOLS_DIR" ]; then
    echo "Cloning depot_tools..."
    git clone https://chromium.googlesource.com/chromium/tools/depot_tools.git "$DEPOT_TOOLS_DIR"
else
    echo "Updating depot_tools..."
    (cd "$DEPOT_TOOLS_DIR" && git pull)
fi

export PATH="$DEPOT_TOOLS_DIR:$PATH"

# Ensure gclient is configured
if [ ! -f "$HOME/.gclient" ]; then
    echo "Initializing gclient..."
fi

# Step 2: Fetch V8 source
echo ""
echo ">>> Step 2: Fetching V8 source..."
cd "$BUILD_DIR"

if [ ! -d "$V8_SRC_DIR" ]; then
    echo "Fetching V8..."
    fetch v8
    cd "$V8_SRC_DIR"
else
    echo "V8 source exists, syncing..."
    cd "$V8_SRC_DIR"
fi

# Checkout specific version
echo "Checking out V8 version $V8_VERSION..."
git fetch --tags
git checkout "$V8_VERSION" || git checkout "branch-heads/${V8_VERSION%.*}" || {
    echo "Warning: Could not checkout exact version, using main branch"
    git checkout main
}

# Sync dependencies
echo "Syncing V8 dependencies..."
gclient sync -D

# Step 3: Configure build with GN
echo ""
echo ">>> Step 3: Configuring V8 build..."

# Build configuration for static library
BUILD_OUT_DIR="out.gn/$V8_ARCH.$BUILD_TYPE"

# GN args for static library build suitable for Go integration
GN_ARGS="
is_debug=$([ "$BUILD_TYPE" = "debug" ] && echo "true" || echo "false")
target_cpu=\"$V8_ARCH\"
v8_target_cpu=\"$V8_ARCH\"
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
"

# Platform-specific args
if [ "$OS" = "darwin" ]; then
    GN_ARGS="$GN_ARGS
use_xcode_clang=true
"
elif [ "$OS" = "linux" ]; then
    GN_ARGS="$GN_ARGS
is_clang=true
use_sysroot=false
"
fi

# Write args to file
mkdir -p "$BUILD_OUT_DIR"
echo "$GN_ARGS" > "$BUILD_OUT_DIR/args.gn"

echo "GN Args:"
cat "$BUILD_OUT_DIR/args.gn"

# Generate build files
gn gen "$BUILD_OUT_DIR"

# Step 4: Build V8
echo ""
echo ">>> Step 4: Building V8 (this may take a while)..."
ninja -C "$BUILD_OUT_DIR" v8_monolith -j "$NUM_JOBS"

# Step 5: Copy output files
echo ""
echo ">>> Step 5: Copying build artifacts..."

# Copy static library
if [ "$OS" = "darwin" ]; then
    LIB_EXT="a"
else
    LIB_EXT="a"
fi

cp "$BUILD_OUT_DIR/obj/libv8_monolith.$LIB_EXT" "$OUTPUT_DIR/lib/"

# Copy headers
echo "Copying V8 headers..."
cp -R include/* "$OUTPUT_DIR/include/"

# Create pkg-config file for easier linking
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

# Create a Go-specific configuration file
cat > "$OUTPUT_DIR/v8_config.go" << 'GOEOF'
//go:build ignore

package main

// V8 build configuration for cgo
// Include this information in your cgo directives

/*
#cgo CXXFLAGS: -std=c++20 -I${SRCDIR}/v8/include
#cgo LDFLAGS: -L${SRCDIR}/v8/lib -lv8_monolith -lpthread
#cgo darwin LDFLAGS: -lc++ -framework CoreFoundation
#cgo linux LDFLAGS: -lstdc++ -lm -ldl
*/
import "C"
GOEOF

echo ""
echo "=== Build Complete ==="
echo "V8 static library: $OUTPUT_DIR/lib/libv8_monolith.a"
echo "V8 headers:        $OUTPUT_DIR/include/"
echo "pkg-config file:   $OUTPUT_DIR/v8.pc"
echo ""
echo "To use with Go, add these cgo directives to your Go files:"
echo ""
echo '// #cgo CXXFLAGS: -std=c++20 -I${SRCDIR}/v8/include'
echo '// #cgo LDFLAGS: -L${SRCDIR}/v8/lib -lv8_monolith -lpthread'
echo '// #cgo darwin LDFLAGS: -lc++ -framework CoreFoundation'
echo '// #cgo linux LDFLAGS: -lstdc++ -lm -ldl'
echo ""
echo "Library size:"
ls -lh "$OUTPUT_DIR/lib/libv8_monolith.a"
