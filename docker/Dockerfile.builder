# Orbital V8 Builder for Linux
# This Dockerfile provides an environment for building V8 for Linux platforms
# Usage: docker build -t orbital-builder -f docker/Dockerfile.builder .
#        docker run --rm -v $(pwd):/workspace orbital-builder make v8-linux-arm64
#
# On Apple Silicon, build/run with matching --platform so each Linux arch is
# compiled natively (or under qemu), not cross-compiled:
#   docker build --platform linux/arm64 -t orbital-builder:arm64 -f docker/Dockerfile.builder .
#   docker build --platform linux/amd64 -t orbital-builder:amd64 -f docker/Dockerfile.builder .

FROM ubuntu:24.04

# Prevent interactive prompts
ENV DEBIAN_FRONTEND=noninteractive
ENV GCLIENT_SUPPRESS_GIT_VERSION_WARNING=1
ARG GO_VERSION=1.24.0
ARG TARGETARCH

# Install build dependencies (matches CI + both Linux arches from arm64/amd64 hosts).
# Chromium only ships a linux-x64 prebuilt clang, so we use system clang-19 for
# both aarch64 and x86_64 native builds inside matching Docker platforms.
# Go is included so Orbital's CGO link step can run in-container when the
# host is not Linux (V8 .a is prebuilt; only the Go/C++ bridge + link needs Linux).
RUN apt-get update && apt-get install -y \
    build-essential \
    clang-19 \
    lld-19 \
    libclang-rt-19-dev \
    ca-certificates \
    cmake \
    curl \
    file \
    git \
    lsb-release \
    ninja-build \
    pkg-config \
    python3 \
    python3-pip \
    sudo \
    wget \
    xz-utils \
    libglib2.0-dev \
    # Cross-compilation tools kept for host-tool edge cases
    gcc-aarch64-linux-gnu \
    g++-aarch64-linux-gnu \
    binutils-aarch64-linux-gnu \
    gcc-x86-64-linux-gnu \
    g++-x86-64-linux-gnu \
    binutils-x86-64-linux-gnu \
    && update-alternatives --install /usr/bin/clang clang /usr/bin/clang-19 100 \
    && update-alternatives --install /usr/bin/clang++ clang++ /usr/bin/clang++-19 100 \
    && update-alternatives --install /usr/bin/lld lld /usr/bin/lld-19 100 \
    && update-alternatives --install /usr/bin/ld.lld ld.lld /usr/bin/ld.lld-19 100 \
    && mkdir -p \
         /usr/lib/llvm-19/lib/clang/19/lib/aarch64-unknown-linux-gnu \
         /usr/lib/llvm-19/lib/clang/19/lib/x86_64-unknown-linux-gnu \
    && if [ -f /usr/lib/llvm-19/lib/clang/19/lib/linux/libclang_rt.builtins-aarch64.a ]; then \
         ln -sf ../linux/libclang_rt.builtins-aarch64.a \
           /usr/lib/llvm-19/lib/clang/19/lib/aarch64-unknown-linux-gnu/libclang_rt.builtins.a; \
       fi \
    && if [ -f /usr/lib/llvm-19/lib/clang/19/lib/linux/libclang_rt.builtins-x86_64.a ]; then \
         ln -sf ../linux/libclang_rt.builtins-x86_64.a \
           /usr/lib/llvm-19/lib/clang/19/lib/x86_64-unknown-linux-gnu/libclang_rt.builtins.a; \
       fi \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" \
         | tar -C /usr/local -xz \
    && rm -rf /var/lib/apt/lists/*

ENV PATH="/usr/local/go/bin:${PATH}"

# Create non-root user for building
RUN useradd -m -s /bin/bash builder && \
    echo "builder ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

USER builder
WORKDIR /home/builder

# Set up workspace
WORKDIR /workspace

# Default command
CMD ["make", "help"]
