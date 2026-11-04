#!/usr/bin/env bash
# Print a hash of everything libv8go_glue.a is compiled from: its sources, the
# script that compiles it, and the V8 version whose headers it builds against.
# The manifest records this as glue_inputs, so a mismatch means the pinned glue
# is out of date and needs rebuilding.
set -euo pipefail

cd "$(dirname "$0")/.."

{
  sed -nE 's/^V8_VERSION := *//p' Makefile
  shasum -a 256 pkg/v8/csrc/v8go.cc pkg/v8/csrc/v8go_exports.h pkg/v8/v8go.h scripts/build-glue.py
} | shasum -a 256 | cut -d' ' -f1
