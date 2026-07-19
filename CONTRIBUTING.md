# Contributing to Orbital

This guide covers local development against a custom V8 build, rebuilding the
C++ glue without a full V8 recompile, and the CI-equivalent host-only build
path. If you only want to **use** Orbital as a library or run the CLI, see
[README.md](README.md).

## Prerequisites

- Go 1.24+
- Git
- Python 3 (used to build V8, pre-compile the C++ glue, and assemble the manifest)
- For building V8 from source: Xcode on macOS; on Linux, Chromium's bundled clang is used (host needs `build-essential` + `ninja-build`). ~10 GB disk space.
- For only *using* the prebuilt libraries: just a C toolchain for CGO (`clang`/`gcc`). No C++ compiler needed — the bridge ships pre-compiled.
- For glue-only rebuilds: V8 public headers (see [Headers](#v8-headers-without-full-source)) plus a prior GN `out.gn` dir. Not the full V8 source tree.

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
│   ├── runtime/              # JS runtime, event loop, sandbox interfaces
│   └── nodejs/               # Node.js compat runtime (`New`) + stdlib modules
├── cmd/orbital/              # CLI entry point
├── examples/                 # Usage examples
├── deps/v8/                  # Built V8 output per platform (gitignored)
│   ├── include/              # Public headers for glue-only rebuilds
│   └── <os>-<arch>/lib/      # libv8_monolith.a, libv8_libcxx.a, libv8go_glue.a
├── dist/                     # Packaged assets (gitignored)
├── .v8/                      # Fetched runtime for local builds (gitignored)
└── v8-build/                 # V8 source checkout (gitignored, local only)
```

The V8 static libraries are **not committed**. They are packaged into
`dist/v8-<goos>-<goarch>.tar.gz`, published as GitHub Release assets, and fetched
into `.v8/` by `cmd/v8setup`. Public headers ship separately as
`v8-headers.tar.gz` on each GitHub Release (and as the CI `v8-headers` artifact)
for glue rebuilds. The full V8 source tree in `v8-build/` is only needed when
rebuilding the monolith.

## Local development against a custom V8

The usual contributor loop is: install (or build) V8 libs once, then iterate on
Go/JS — and when you touch `pkg/v8/csrc/v8go.cc`, **rebuild only the glue**.
You do not need to recompile `libv8_monolith.a` for glue changes.

| What you changed | What to rebuild |
|------------------|-----------------|
| Go / JS under `pkg/`, `internal/`, `cmd/` | `make build-native` / `make test` |
| `pkg/v8/csrc/v8go.cc` (C++ glue) | `make v8-glue` then `make build-native` |
| V8 version, GN flags, or ICU/sandbox/etc. | Full `make v8-native` (30–60+ min) |

GitHub Actions (`.github/workflows/update-v8.yml`) does the multi-platform
equivalent. Locally, run the same steps for **your host OS/arch only**.

### 1. Install a V8 runtime (once)

```bash
# Preferred: pinned libs from the published Release (same bits CI validates)
make v8-setup

# Or: build V8 yourself for this machine, then install from dist/
make v8-native          # long; also writes deps/v8/include + dist/v8-headers.tar.gz
make v8-setup-local
```

### 2. Day-to-day Orbital build / test

Same commands as the CI **validate** job (Release fetch instead of Actions artifacts):

```bash
# Linux: sudo apt-get update && sudo apt-get install -y build-essential
make test
make coverage
bash scripts/build-examples.sh
make build-native
./build/orbital
```

### 3. Rebuild the glue only (no monolith rebuild)

Editing `v8go.cc` only requires recompiling `libv8go_glue.a`. That needs:

1. **V8 public headers** — not the full source tree (see next section)
2. **A prior GN output dir** (`compile_commands.json` + the clang that built the
   monolith) — normally `v8-build/v8/out.gn/<os>-<arch>` from an earlier
   `make v8-native`

```bash
# Headers: from a local build, or download the CI artifact
make v8-headers-setup                    # from dist/v8-headers.tar.gz
# make v8-headers-setup RUN_ID=<id>      # download artifact via gh

make v8-glue                             # → deps/v8/<os>-<arch>/lib/libv8go_glue.a
                                         #   also updates .v8/*/…/lib/ if present
make build-native
make test
```

Header discovery for `make v8-glue` (first hit wins):

1. `V8_INCLUDE=...` (explicit override)
2. `deps/v8/include` (from `package-v8-headers` / `v8-headers-setup`)
3. `deps/v8/<os>-<arch>/include`
4. `v8-build/v8/include` (full checkout)

Override the GN dir with `V8_OUT_DIR=...` if your `out.gn` is not at the default path.

### V8 headers (without full source)

Glue rebuilds `#include <v8.h>` / `<libplatform/...>`. Those live in V8's public
`include/` tree (~small). You do **not** need the multi-GB `v8-build/v8` checkout
for glue-only work if you have:

- headers installed under `deps/v8/include`, and
- a leftover `out.gn` (or another dir with `compile_commands.json` + a working clang)

CI uploads a **`v8-headers`** artifact from every `update-v8` build, and
`release.yml` publishes the same `v8-headers.tar.gz` (+ `.sha256`) onto the
GitHub Release alongside the platform lib tarballs. Install with:

```bash
# From a Release (after publish):
gh release download <tag> -p 'v8-headers*' -D dist
make v8-headers-setup

# Or from an Actions run (before/without a Release):
make v8-headers-setup RUN_ID=123456789
# → deps/v8/include/v8.h etc.
```

After a local `make v8-native` / `make package-v8-headers`, headers are already
in `deps/v8/include` and packaged in `dist/`.

### 4. Full V8 rebuild (this platform only)

Mirrors one matrix entry of the CI **build** job. Use when changing V8 version,
GN args, or anything that affects `libv8_monolith.a` / `libv8_libcxx.a`.

```bash
# Linux: bash scripts/ci-install-linux-toolchain.sh
# macOS: Xcode with the macOS 15+ SDK (Intel macOS unsupported)

make v8-native           # v8-fetch + v8-build for HOST; packages libs + headers
make v8-setup-local
make test
make build-native
```

`make v8-native` writes `deps/v8/<os>-<arch>/lib/`, `deps/v8/include/`,
`dist/v8-<goos>-<goarch>.tar.gz`, and `dist/v8-headers.tar.gz`. Pass
`V8_VERSION=...` to match a specific pin.

### Building Orbital only

Once `.v8/` and the cgo link file exist:

```bash
make build-native          # → build/orbital
make build                 # Same (honors TARGET_OS/TARGET_ARCH when they match host)
make release               # Optimized (stripped) binary for host
make v8-list               # List installed runtimes under .v8/
```

## Building V8

For the host-only path and glue-only rebuild, see
[Local development against a custom V8](#local-development-against-a-custom-v8).
Extra targets:

```bash
# Check out the latest stable V8 and build it for THIS platform.
make v8-latest

# Current machine, pinned version (same as CI build job for one platform)
make v8-native

# Explicit platform name (only builds reliably on a matching host)
make v8-linux-arm64
make v8-linux-amd64
make v8-darwin-arm64   # Intel macOS is unsupported (V8 needs the macOS 15+ SDK)

make package-v8              # libs → dist/v8-<goos>-<goarch>.tar.gz
make package-v8-headers      # include/ → dist/v8-headers.tar.gz + deps/v8/include
make v8-setup-local          # install libs from dist/ into .v8/
make v8-glue                 # rebuild glue only
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
| C++ glue compile (`libv8go_glue.a`) | Alongside the V8 build **or** later via `make v8-glue` |
| Package `v8-<goos>-<goarch>.tar.gz` + sha256 | Alongside the V8 build (into `dist/`) |
| Package `v8-headers.tar.gz` | Alongside the V8 build; CI uploads as `v8-headers`; published on the Release |
| Publish Release assets (libs + headers) | CI (`release.yml` on merge) |
| Fetch into `.v8/` + write cgo link file | Consumer/CI via `cmd/v8setup` (`go generate`) |
| Go compile + CGO link | Native platform (this host, or a native CI runner) |

The C++ bridge is **pre-compiled**, not by CGO. V8 uses Chromium's custom libc++
(the `std::__Cr::` inline namespace), which is ABI-incompatible with the system
`libstdc++` that a stock `g++` links. If CGO compiled `v8go.cc` with `g++`, its
`std::` symbols would not match V8's and linking would fail. Instead
`scripts/build-glue.py` reuses V8's exact compile command (from
`compile_commands.json`) to build `pkg/v8/csrc/v8go.cc` into `libv8go_glue.a`.
Consumers then link the prebuilt archives with a plain C toolchain — no C++
compiler, no V8 headers.

Because the glue lives in `pkg/v8/csrc/` (a subdirectory), the Go tool does not
hand it to CGO. **If you change `v8go.cc`, run `make v8-glue`** (not a full
`v8-native`) — that rebuilds only `libv8go_glue.a` using public headers + the
existing `out.gn`. Use a full V8 rebuild only when the monolith itself must change.

### Distribution: Release assets, not committed binaries

The three archives (`libv8_monolith.a`, `libv8_libcxx.a`, `libv8go_glue.a`) are
packaged into a single `dist/v8-<goos>-<goarch>.tar.gz` and published as a GitHub
Public headers are **not** in the lib tarball (consumers use `v8go.h`); they ship
as `v8-headers.tar.gz` on the same GitHub Release (and as the CI `v8-headers`
artifact) for local glue rebuilds. `pkg/v8` carries **no** `-L`/`-l` flags; instead
`cmd/v8setup` downloads the version-pinned, checksum-verified lib asset into
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

# V8 build / glue (maintainers + local custom V8)
make v8-latest             # Latest stable V8 for this platform
make v8-native             # Full monolith + glue + package libs/headers
make v8-glue               # Rebuild ONLY libv8go_glue.a
make v8-headers-setup      # Install headers → deps/v8/include (RUN_ID=… to download)
make package-v8            # Package libs into dist/
make package-v8-headers    # Package include/ → dist/v8-headers.tar.gz
make v8-linux-arm64
make v8-linux-amd64
make v8-darwin-arm64       # Intel macOS unsupported (V8 needs the macOS 15+ SDK)
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
| `V8_VERSION` | V8 version to build | (Makefile pin) |
| `V8_INCLUDE` | Public headers dir for `v8-glue` | auto-discovered |
| `V8_OUT_DIR` | GN out dir (`compile_commands.json`) | `v8-build/v8/out.gn/<os>-<arch>` |
| `RUN_ID` | Actions run id for `v8-headers-setup` | (none) |
| `MODULE_VERSION` | Module/release version for the manifest | `v0.1.0` |
| `NUM_JOBS` | Parallel ninja jobs | CPU count |
| `BINARY_NAME` | Output binary name | `orbital` |

(`TARGET_ARCH=amd64` maps to V8's internal `target_cpu="x64"` automatically.)

## Testing

Same commands as the CI **validate** job (after `make v8-setup`):

```bash
make test
make coverage
make coverage-html          # local only — opens the HTML report
bash scripts/build-examples.sh
```

Tests link against the `.v8/` runtime for the host platform via the generated
cgo link file (`make test` fails with setup instructions if it's missing). They
exclude `v8-build/` and `examples/` (which contain multi-`main` demo packages).

## Adding a Node.js module

1. Create `pkg/nodejs/<module>/`
2. Implement `runtime.Module` (`Name()`, `Register()`)
3. Register it in `pkg/nodejs/nodejs.go` (`defaultModules`) so `nodejs.New` and the CLI pick it up
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
  Actions artifacts (plus a shared `v8-headers` artifact for glue-only local
  rebuilds), commits `internal/v8dist/manifest.json` (pinning **this run's**
  id + sha256), then `validate` installs those exact artifacts into `.v8/` (the
  `go generate` flow) and runs `make test`/`make coverage` on a native runner per
  arch, and finally marks the PR ready. A failure leaves the PR a draft. Because the
  manifest is re-pinned on every build, it always names the most recent run.
- `.github/workflows/release.yml` — On merge to `main` (when `manifest.json` changes),
  verifies provenance (run succeeded, belongs to this repo, `source_commit` matches,
  checksums match, tag/release absent), re-downloads the **same** artifacts by run id
  (platform libs + `v8-headers`), then tags and publishes them as a GitHub Release.
  Never rebuilds.

The GitHub repository that hosts the Releases is `proto-studio/orbital` (distinct
from the vanity module path `proto.zip/studio/orbital`).

## License

MIT
