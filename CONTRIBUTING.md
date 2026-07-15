# Contributing to Orbital

This guide covers building from source, compiling V8, cross-platform builds, and development workflows. If you only want to **use** Orbital as a library or run the CLI, see [README.md](README.md).

## Prerequisites

- Go 1.24+
- Git
- Python 3
- C++ compiler (Xcode on macOS, `build-essential` on Linux)
- ~10 GB disk space (only if building V8 from source)
- Docker (optional — for building Linux V8 and CGO link from macOS)

## Project structure

```
gnode/
├── deps/v8/                  # Prebuilt V8 per platform (committed)
│   ├── darwin-arm64/
│   ├── darwin-x64/
│   ├── linux-arm64/
│   └── linux-x64/
│       ├── lib/libv8_monolith.a
│       └── include/          # V8 public headers
├── pkg/
│   ├── v8/                   # CGO bindings (v8go.go, v8go.cc)
│   └── runtime/              # JS runtime, event loop, sandbox interfaces
├── internal/nodejs/          # Node.js standard library modules
├── cmd/orbital/              # CLI entry point
├── examples/                 # Usage examples
├── docker/
│   └── Dockerfile.builder    # Linux build environment
└── v8-build/                 # V8 source checkout (gitignored, local only)
```

Consumers need `deps/v8/<platform>/` (`.a` + headers). The full V8 source tree in `v8-build/` is only for maintainers rebuilding V8.

## Quick start (native)

```bash
# If deps/v8/<your-platform>/ already exists (committed in repo):
make build-native
./build/orbital

make test
```

If V8 is missing for your platform, build it first (see below) or run the GitHub Actions "Build V8" workflow.

## Building Orbital

```bash
# Host platform
make build-native          # macOS/Linux native
make build                 # Same, with explicit TARGET_OS/TARGET_ARCH

# Linux targets
make build-linux-arm64     # Native on Linux; Docker CGO link from macOS
make build-linux-amd64

# macOS targets
make build-darwin-arm64
make build-darwin-amd64

# Optimized binary
make release TARGET_OS=linux TARGET_ARCH=arm64

# List available V8 builds
make v8-list
```

Output binaries go to `build/`:

- `build/orbital` — host platform
- `build/orbital-linux-arm64` — Linux ARM64
- `build/orbital-linux-x64` — Linux x86_64

## Building V8

V8 takes 30–60+ minutes on first build. Artifacts land in `deps/v8/<os>-<arch>/`.

```bash
# Check out the latest stable V8 and build every platform reachable from
# this host (macOS: darwin native + linux via Docker; Linux: linux native).
# Updates an existing checkout to the latest stable version.
make v8-latest

# Current machine
make v8-native

# Specific platform (on matching host or with cross-toolchain)
make v8-linux-arm64
make v8-linux-x64
make v8-darwin-arm64
make v8-darwin-x64

# Both Linux arches via Docker (recommended from macOS)
make docker-build-linux
```

`make v8-latest` resolves the V8 version bundled with the current stable Chrome
release via `scripts/latest-v8-version.sh` (which queries chromiumdash), then
builds all platforms with that version. To pin a specific version instead, pass
`V8_VERSION` explicitly, e.g. `make v8-all-platforms V8_VERSION=13.1.201.1`.

### Docker V8 builds

`make docker-build-linux` builds V8 for `linux-arm64` and `linux-x64` using native-arch containers. This avoids fragile cross-compilation of V8 itself.

```bash
make docker-build-linux

# Tune parallelism (default 4; higher values can OOM in Docker Desktop)
make docker-build-linux DOCKER_NUM_JOBS=2
```

The builder image (`docker/Dockerfile.builder`) includes clang-19, ninja, Go, and Linux build deps.

### V8 build cache

Compiled objects persist in `v8-build/v8/out.gn/<platform>/`. Re-runs reuse ninja's cache. To wipe:

```bash
make clean-v8-build    # Remove v8-build/ entirely
make clean-v8          # Remove deps/v8 artifacts
make clean-all         # Everything
```

### macOS SDK note

On recent macOS, Command Line Tools may ship an SDK requiring Clang 19+. The Makefile prefers Xcode's SDK via `DEVELOPER_DIR`. Override if needed:

```bash
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer make build-native
```

## Cross-compilation model

| Step | Where it runs |
|------|---------------|
| V8 compile | Native platform (or Docker per arch) |
| Go compile + CGO link | Target platform toolchain |

From **Linux**, `make build-linux-arm64` is fully native — no Docker.

From **macOS**, Go cross-compiles but CGO needs a Linux linker. `make build-linux-*` runs only the final link inside Docker, reusing the prebuilt `deps/v8/linux-*/libv8_monolith.a`.

## Makefile reference

```bash
make help                  # All targets

# Build
make build-native
make build-linux-arm64
make build-linux-amd64
make build-all
make release

# V8
make v8-native
make v8-linux-arm64
make v8-linux-x64
make v8-all-linux
make v8-list
make docker-build-linux
make docker-build-orbital TARGET_ARCH=arm64   # CGO link only (non-Linux hosts)

# Quality
make test
make coverage
make fmt
make vet
make lint

# Clean
make clean
make clean-v8
make clean-v8-build
make clean-all
```

### Build variables

| Variable | Description | Default |
|----------|-------------|---------|
| `TARGET_OS` | `darwin` or `linux` | Host OS |
| `TARGET_ARCH` | `arm64` or `x64` | Host arch |
| `V8_VERSION` | V8 version to build | `13.1.201.1` |
| `NUM_JOBS` | Parallel ninja jobs | CPU count |
| `DOCKER_NUM_JOBS` | Jobs inside Docker | `4` |
| `BINARY_NAME` | Output binary name | `orbital` |

## Testing

```bash
make test

# Coverage
make coverage
make coverage-html
```

Tests run against the native platform V8 build. They exclude `v8-build/` and `examples/` (which contain multi-`main` demo packages).

## Adding a Node.js module

1. Create `internal/nodejs/<module>/`
2. Implement `runtime.Module` (`Name()`, `Register()`)
3. Register in `cmd/orbital/main.go` for CLI support
4. Add tests in `<module>_test.go`

## CI

- `.github/workflows/build-v8.yml` — Build V8 for all platforms (manual dispatch)
- `.github/workflows/tests.yml` — Run tests on Linux (expects `deps/v8/linux-x64/`)

## License

MIT
