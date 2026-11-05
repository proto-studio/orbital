#!/usr/bin/env bash
# Print a hash of the Makefile recipes that decide how V8 is built, GN args included.
# The manifest records this as v8_build_config. The pinned V8 libraries were built this
# way, and glue compiled any other way may not be compatible with them, so changing
# these recipes needs a full V8 build rather than a glue rebuild.
set -euo pipefail

cd "$(dirname "$0")/.."

recipes="$(sed -n '/^v8-gn-gen:/,/V8 built and packaged successfully/p' Makefile)"
case "$recipes" in
  *"V8 built and packaged successfully"*) ;;
  *) echo "ERROR: couldn't find the V8 build recipes in the Makefile" >&2; exit 1 ;;
esac

printf '%s\n' "$recipes" | shasum -a 256 | cut -d' ' -f1
