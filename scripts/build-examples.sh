#!/usr/bin/env bash
# Compile every in-repo Go example so a consumer-facing break (bad cgo link
# flags, API drift) fails the build on each platform, not just the unit tests.
#
# Some example directories ship several standalone snippet files that EACH
# declare their own package-level main() (run individually via `go run
# examples/<x>/<file>.go`); those cannot build as one package, so they are built
# file-by-file. Directories with a single program build as a unit.
#
# The standalone example module examples/hellov8 has its own go.mod (it consumes
# the library like an external project) and is built separately by the workflow.
set -uo pipefail

cd "$(dirname "$0")/.."

status=0
while IFS= read -r dir; do
  [ "$dir" = "examples/hellov8" ] && continue

  gofiles=$(find "$dir" -maxdepth 1 -name '*.go' -not -name '*_test.go')
  [ -z "$gofiles" ] && continue

  # Files declaring a package-level main().
  mains=$(grep -lE '^func main\(' $gofiles || true)
  count=$(printf '%s' "$mains" | grep -c . || true)

  if [ "$count" -le 1 ]; then
    echo ">>> go build ./$dir"
    CGO_ENABLED=1 go build -o /dev/null "./$dir" || status=1
  else
    # Multiple standalone programs in one directory: build each on its own.
    for f in $mains; do
      echo ">>> go build $f"
      CGO_ENABLED=1 go build -o /dev/null "$f" || status=1
    done
  fi
done < <(find examples -mindepth 1 -maxdepth 1 -type d | sort)

exit "$status"
