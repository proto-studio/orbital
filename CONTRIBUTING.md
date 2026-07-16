# Contributing to Orbital

This guide covers building from source, compiling V8, cross-platform builds, and development workflows. If you only want to **use** Orbital as a library or run the CLI, see [README.md](README.md).

## Prerequisites

- Go 1.24+
- Git
- Python 3 (used to build V8, pre-compile the C++ glue, and assemble the manifest)
- For building V8 from source: Xcode on macOS; on Linux, Chromium's bundled clang is used (host needs `build-essential` + `ninja-build`). ~10 GB disk space.
- For only *using* the prebuilt libraries: just a C toolchain for CGO (`clang`/`gcc`). No C++ compiler needed — the bridge ships pre-compiled.

## Project structure

```
gnode/
├── internal/v8dist/          # V8 distribution logic + embedded manifest
│   ├── manifest.json         # Pinned V8/module version + per-target sha256 (generated)
│   ├── version.go            # ModuleVersion constant
│   └── *.go                  # Source-agnostic fetch/verify/extract/link logic
├── cmd/v8setup/              # `go generate` tool that installs V8 into .v8/
├── pkg/
│   ├── v8/                   # CGO bindings (v8go.go, v8go.h); carries NO -L/-l
│   │   └── csrc/v8go.cc       # C++ bridge, pre-compiled into libv8go_glue.a
│   └── runtime/              # JS runtime, event loop, sandbox interfaces
├── internal/nodejs/          # Node.js standard library modules
├── cmd/orbital/              # CLI entry point
├── examples/                 # Usage examples
├── deps/v8/                  # Built V8 output per platform (gitignored)
├── dist/                     # Packaged v8-<goos>-<goarch>.tar.gz assets (gitignored)
├── .v8/                      # Fetched runtime for local builds (gitignored)
└── v8-build/                 # V8 source checkout (gitignored, local only)
```

The V8 static libraries are **not committed**. They are packaged into
`dist/v8-<goos>-<goarch>.tar.gz`, published as GitHub Release assets, and fetched
into `.v8/` by `cmd/v8setup`. The full V8 source tree in `v8-build/` is only for
maintainers rebuilding V8.

## Quick start (native)

```bash
# Fetch the pinned V8 runtime for your platform + write the cgo link file.
# Once a Release exists for the pinned module version:
make v8-setup
# ...or, before any Release exists / to use a locally built lib:
make v8-native v8-setup-local

make build-native
./build/orbital

make test
```

`make v8-setup` runs `go run ./cmd/v8setup` (the same thing consumers do via
`go generate`). If V8 is not yet built/published for your platform, build it
first (see below), which produces the `dist/` asset that `v8-setup-local` installs.

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

V8 takes 30–60+ minutes on first build. Build output lands in
`deps/v8/<os>-<arch>/lib/`, and each build also packages a release asset into
`dist/v8-<goos>-<goarch>.tar.gz` (+ `.sha256`).

```bash
# Check out the latest stable V8 and build it for THIS platform.
# Updates an existing checkout to the latest stable version.
make v8-latest

# Current machine, pinned version
make v8-native

# Specific platform (only builds reliably on a matching host)
make v8-linux-arm64
make v8-linux-amd64
make v8-darwin-arm64   # Intel macOS is unsupported (V8 needs the macOS 15+ SDK)

# Install a freshly built asset into .v8/ + generate the link file
make v8-setup-local
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
make clean-v8          # Remove deps/v8 build output
make clean-v8-runtime  # Remove fetched .v8/ + generated link files
make clean-all         # Everything (also removes dist/)
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
| Package `v8-<goos>-<goarch>.tar.gz` + sha256 | Alongside the V8 build (into `dist/`) |
| Publish Release asset | CI (`release.yml` on merge) |
| Fetch into `.v8/` + write cgo link file | Consumer/CI via `cmd/v8setup` (`go generate`) |
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
(or `scripts/build-v8.sh`), then `make v8-setup-local` to reinstall into `.v8/`.

### Distribution: Release assets, not committed binaries

The three archives (`libv8_monolith.a`, `libv8_libcxx.a`, `libv8go_glue.a`) are
packaged into a single `dist/v8-<goos>-<goarch>.tar.gz` and published as a GitHub
Release asset. `pkg/v8` carries **no** `-L`/`-l` flags; instead `cmd/v8setup`
downloads the version-pinned, checksum-verified asset into
`.v8/<version>/<goos>-<goarch>/` and writes a build-tagged
`zz_generated_v8link_<goos>_<goarch>.go` link file (gitignored) into a consumer
package. On Linux the archives are linked inside `-Wl,--start-group ... --end-group`
so ld resolves the cross-references between the monolith, the glue, and libc++.

Why Releases? **Git LFS is unusable** (`go get` / the module proxy do not run the
LFS smudge filter, so pointers would break importers), and V8's monolith exceeds
GitHub's 100MB per-file Git limit. GitHub Releases allow multi-GB assets, so a
single monolith ships intact — no archive splitting. The pinned metadata lives in
`internal/v8dist/manifest.json` (V8 version, module version, source run id/commit,
and per-target `{filename, sha256}`), assembled by `scripts/gen-manifest.py`.

## Makefile reference

```bash
make help                  # All targets

# Build (native / current platform)
make build-native
make build
make release

# V8 runtime setup
make v8-setup              # Fetch pinned libs from the Release into .v8/ + link file
make v8-setup-local        # Install a locally built dist/ asset + link file
make v8-list               # List installed runtimes under .v8/

# V8 build (maintainers)
make v8-latest             # Latest stable V8 for this platform
make v8-native
make v8-linux-arm64
make v8-linux-amd64
make v8-darwin-arm64       # Intel macOS unsupported (V8 needs the macOS 15+ SDK)
make package-v8            # Package a built platform into dist/
make v8-manifest           # Assemble internal/v8dist/manifest.json from dist/

# Quality
make test
make coverage
make fmt
make vet
make lint

# Clean
make clean
make clean-v8
make clean-v8-runtime
make clean-v8-build
make clean-all
```

### Build variables

| Variable | Description | Default |
|----------|-------------|---------|
| `TARGET_OS` | `darwin` or `linux` | Host OS |
| `TARGET_ARCH` | `arm64` or `amd64` | Host arch |
| `V8_VERSION` | V8 version to build | `13.1.201.1` |
| `MODULE_VERSION` | Module/release version for the manifest | `v0.1.0` |
| `NUM_JOBS` | Parallel ninja jobs | CPU count |
| `BINARY_NAME` | Output binary name | `orbital` |

(`TARGET_ARCH=amd64` maps to V8's internal `target_cpu="x64"` automatically.)

## Testing

```bash
make v8-setup   # or: make v8-native v8-setup-local   (once, to install .v8/ + link file)
make test

# Coverage
make coverage
make coverage-html
```

Tests link against the `.v8/` runtime for the host platform via the generated
cgo link file (`make test` fails with setup instructions if it's missing). They
exclude `v8-build/` and `examples/` (which contain multi-`main` demo packages).

## Adding a Node.js module

1. Create `internal/nodejs/<module>/`
2. Implement `runtime.Module` (`Name()`, `Register()`)
3. Register in `cmd/orbital/main.go` for CLI support
4. Add tests in `<module>_test.go`

## CI

No Docker, no emulation. Linux libraries are cross-compiled on x64 runners
(Chromium clang + a Debian sysroot targets arm64), while macOS builds run on
matching runners. Tests always run on a **native** runner per architecture
against the exact packaged artifacts — never a rebuild.

- `.github/workflows/update-v8.yml` — The whole update flow in one workflow, with
  two entry points feeding a shared `build -> manifest -> validate -> ready` chain:
  - **Dispatch/schedule:** detect a newer stable V8, and (create-only) open a
    **draft** PR early — bumping `V8_VERSION` + `ModuleVersion` — so a PR exists no
    matter what happens next. The first build runs in this same run.
  - **Push to `automated/update-v8-**`:** the developer loop. If the build or tests
    fail, clone the PR branch, fix it, and push; that reruns build + validate,
    uploads fresh artifacts, and re-pins the manifest on the same PR. Opening the
    PR is skipped when the branch already exists, so pushed fixes are never reset.

  Each build packages every target into `v8-<target>.tar.gz`, uploads them as
  Actions artifacts, commits `internal/v8dist/manifest.json` (pinning **this run's**
  id + sha256), then `validate` installs those exact artifacts into `.v8/` (the
  `go generate` flow) and runs `make test`/`make coverage` on a native runner per
  arch, and finally marks the PR ready. A failure leaves the PR a draft. Because the
  manifest is re-pinned on every build, it always names the most recent run.
- `.github/workflows/release.yml` — On merge to `main` (when `manifest.json` changes),
  verifies provenance (run succeeded, belongs to this repo, `source_commit` matches,
  checksums match, tag/release absent), re-downloads the **same** artifacts by run id,
  then tags and publishes them as a GitHub Release. Never rebuilds.

The GitHub repository that hosts the Releases is `proto-studio/orbital` (distinct
from the vanity module path `proto.zip/studio/orbital`).

## License

MIT
