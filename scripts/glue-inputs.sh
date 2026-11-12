#!/usr/bin/env bash
# Print a hash of everything libv8go_glue.a is compiled from, and of what packages it: its
# sources, the scripts that set up, run and package the compile, and the Makefile, which sets
# the V8 version whose headers
# it builds against and the GN args its compile flags come from. The whole Makefile
# counts, since picking out those rules could miss one. The manifest records this as
# glue_inputs, so a mismatch means the pinned glue is out of date and needs rebuilding.
set -euo pipefail

cd "$(dirname "$0")/.."

shasum -a 256 Makefile pkg/v8/csrc/v8go.cc pkg/v8/csrc/v8go_exports.h pkg/v8/v8go.h scripts/build-glue.py scripts/install-glue-sdk.sh \
  scripts/ci-package-glue.sh scripts/reproducible-tar.py \
  | shasum -a 256 | cut -d' ' -f1
