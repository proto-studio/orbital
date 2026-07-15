# Contributing to Orbital

This guide covers building from source, compiling V8, cross-platform builds, and development workflows. If you only want to **use** Orbital as a library or run the CLI, see [README.md](README.md).

## Prerequisites

- Go 1.24+
- Git
- Python 3 (used to build V8 and to pre-compile the C++ glue)
- For building V8 from source: Xcode on macOS; on Linux, Chromium's bundled clang is used (host needs `build-essential` + `ninja-build`). ~10 GB disk space.
- For only *using* the prebuilt libraries: just a C toolchain for CGO (`clang`/`gcc`). No C++ compiler needed — the bridge ships pre-compiled.

## Project structure

```
gnode/
├── deps/v8/                  # Prebuilt V8 per platform (committed)
│   ├── darwin-arm64/
│   ├── darwin-x64/
│   ├── linux-arm64/
│   └── linux-x64/
│       ├── lib/libv8_monolith_0.a # V8 engine (split into <100MB parts on Linux)
│       ├── lib/libv8_monolith_1.a
│       ├── lib/libv8_libcxx.a     # Chromium libc++ + libc++abi
│       ├── lib/libv8go_glue.a     # Pre-compiled Go↔V8 C++ bridge
│       └── include/               # V8 public headers
├── pkg/
│   ├── v8/                   # CGO bindings (v8go.go, v8go.h)
│   │   └── csrc/v8go.cc       # C++ bridge, pre-compiled into libv8go_glue.a
│   └── runtime/              # JS runtime, event loop, sandbox interfaces
├── internal/nodejs/          # Node.js standard library modules
├── cmd/orbital/              # CLI entry point
├── examples/                 # Usage examples
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

If V8 is missing for your platform, build it first (see below).

## Building Orbital

Builds are **native only** — you build Orbital for the platform you're on.
Multi-platform binaries and refreshed V8 libraries are produced by CI on native
runners (see [CI](#ci)); there is no local cross-compilation path.

```bash
make build-native          # Build for host platform
make build                 # Same (honors TARGET_OS/TARGET_ARCH when they match host)
make release               # Optimized (stripped) binary for host

# List available V8 builds
make v8-list
```

Output binary goes to `build/orbital`.

## Building V8

V8 takes 30–60+ minutes on first build. Artifacts land in `deps/v8/<os>-<arch>/`.

```bash
# Check out the latest stable V8 and build it for THIS platform.
# Updates an existing checkout to the latest stable version.
make v8-latest

# Current machine, pinned version
make v8-native

# Specific platform (only builds reliably on a matching host)
make v8-linux-arm64
make v8-linux-x64
make v8-darwin-arm64
make v8-darwin-x64
```

`make v8-latest` resolves the V8 version bundled with the current stable Chrome
release via `scripts/latest-v8-version.sh` (which queries chromiumdash), then
builds it for the host platform. To pin a specific version instead, pass
`V8_VERSION` explicitly, e.g. `make v8-native V8_VERSION=13.1.201.1`.

Cross-arch and cross-OS V8 builds are intentionally **not** supported locally —
they were fragile and slow. The `update-v8.yml` workflow builds each platform on
its own native GitHub runner instead.

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

## Build model

| Step | Where it runs |
|------|---------------|
| V8 compile (`libv8_monolith.a`) | Per target platform (native, or Linux cross-compiled on x64 in CI) |
| libc++ archive (`libv8_libcxx.a`) | Alongside the V8 build (Chromium libc++ + libc++abi) |
| C++ glue compile (`libv8go_glue.a`) | Alongside the V8 build, with V8's own clang + libc++ |
| Go compile + CGO link | Native platform (this host, or a native CI runner) |

The C++ bridge is **pre-compiled** during the V8 build, not by CGO. V8 uses
Chromium's custom libc++ (the `std::__Cr::` inline namespace), which is
ABI-incompatible with the system `libstdc++` that a stock `g++` links. If CGO
compiled `v8go.cc` with `g++`, its `std::` symbols would not match V8's and
linking would fail (`undefined reference to v8::platform::NewDefaultPlatform(...,
std::__Cr::unique_ptr<...>)`). Instead `scripts/build-glue.py` reuses V8's exact
compile command (from `compile_commands.json`) to build `pkg/v8/csrc/v8go.cc`
into `libv8go_glue.a`, guaranteeing an identical ABI. Consumers then link the
prebuilt archives with a plain C toolchain — no C++ compiler, no V8 headers.

Because the glue lives in `pkg/v8/csrc/` (a subdirectory), the Go tool does not
hand it to CGO. If you change `v8go.cc`, rebuild the glue with `make v8-<platform>`
(or `scripts/build-v8.sh`) so `deps/v8/<platform>/lib/libv8go_glue.a` is refreshed.

V8 15.x's `libv8_monolith.a` is ~126MB — over GitHub's 100MB per-file limit — so
on Linux it is split by `scripts/split-archive.py` into `libv8_monolith_0.a` /
`libv8_monolith_1.a` (~55MB each). libc++ ships as a separate `libv8_libcxx.a`.
**Git LFS is intentionally not used**: `go get` and the Go module proxy do not run
the LFS smudge filter, so LFS pointers would break anyone importing the package.
The archives are therefore committed as plain files that must each stay under
100MB — the build prints their sizes and fails if any exceeds ~95MB. On Linux all
archives are linked inside `-Wl,--start-group ... --end-group` so ld resolves the
cross-references between the split parts, the monolith, and libc++/libc++abi.

`split-archive.py` works at the ar byte level (not `ar x`, which would clobber
the monolith's many duplicate member basenames) and preserves the GNU long-name
table in each part; per-part symbol indexes are regenerated with `ranlib`. If V8
grows past ~180MB, bump `--parts` (and the `libv8_monolith_N` entries in
`pkg/v8/v8go.go`).

## Makefile reference

```bash
make help                  # All targets

# Build (native / current platform)
make build-native
make build
make release

# V8
make v8-latest             # Latest stable V8 for this platform
make v8-native
make v8-linux-arm64
make v8-linux-x64
make v8-darwin-arm64
make v8-darwin-x64
make v8-list

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

No Docker, no emulation. Linux libraries are cross-compiled on x64 runners
(Chromium clang + a Debian sysroot targets arm64), while macOS builds run on
matching runners. Unit tests always run on a **native** runner per architecture,
so the prebuilt libraries are validated on real hardware before a PR is opened.

- `.github/workflows/update-v8.yml` — Detects a newer stable V8, rebuilds the
  prebuilt libraries (`libv8_monolith.a` + `libv8go_glue.a`), runs the tests on a
  native runner per arch, then opens a PR that refreshes `deps/v8/` and bumps the
  pinned version.
- `.github/workflows/build-v8.yml` — Build V8 for all platforms (manual dispatch)
- `.github/workflows/tests.yml` — Run tests on Linux against the committed `deps/v8/`

## License

MIT
