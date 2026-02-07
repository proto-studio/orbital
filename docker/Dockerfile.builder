# Orbital V8 Builder for Linux
# This Dockerfile provides an environment for building V8 for Linux platforms
# Usage: docker build -t orbital-builder -f docker/Dockerfile.builder .
#        docker run --rm -v $(pwd):/workspace orbital-builder make v8-linux-amd64

FROM ubuntu:22.04

# Prevent interactive prompts
ENV DEBIAN_FRONTEND=noninteractive

# Install build dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    clang \
    cmake \
    curl \
    git \
    lsb-release \
    ninja-build \
    pkg-config \
    python3 \
    python3-pip \
    sudo \
    wget \
    xz-utils \
    # Cross-compilation tools for ARM64
    gcc-aarch64-linux-gnu \
    g++-aarch64-linux-gnu \
    binutils-aarch64-linux-gnu \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for building
RUN useradd -m -s /bin/bash builder && \
    echo "builder ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

USER builder
WORKDIR /home/builder

# Set up workspace
WORKDIR /workspace

# Default command
CMD ["make", "help"]
