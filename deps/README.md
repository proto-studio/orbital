# Dependencies

## V8 JavaScript Engine

V8 static libraries are **not committed** to this repository. They are built by
CI on native runners, packaged into `dist/v8-<goos>-<goarch>.tar.gz`, and
published as checksum-verified GitHub Release assets. Consumers (and this repo's
own tests) fetch them on demand via `go generate` into a project-local `.v8/`
directory — see `cmd/v8setup` and the top-level `README.md`. The tarballs use
gzip so the fetch tool depends only on the Go standard library.

### Local builds

`deps/v8/` is only a **transient build-output directory** (gitignored). When you
build V8 locally it is populated per platform, then packaged into `dist/`:

```
deps/v8/<goos>-<goarch>/lib/
├── libv8_monolith.a     # V8 itself
├── libv8_libcxx.a       # Chromium's hardened libc++/libc++abi
└── libv8go_glue.a       # pre-compiled cgo C++ glue (V8's exact ABI)
```

Build V8 for the platform you're on and package it:

```bash
make v8-native            # build for the current host
make package-v8           # -> dist/v8-<goos>-<goarch>.tar.gz (+ .sha256)
make v8-setup-local       # install that asset into .v8/ + generate the link file
```

Or fetch and build the latest stable V8:

```bash
make v8-latest
```

### Supported platforms

| Platform | Target |
|----------|--------|
| macOS ARM64 (Apple Silicon) | `darwin/arm64` |
| Linux ARM64 | `linux/arm64` |
| Linux x86_64 | `linux/amd64` |

Intel macOS (`darwin/amd64`) is **not supported**: current V8 requires the
macOS 15+ SDK, which ships only on Apple Silicon.

Libraries for platforms other than your host are produced by CI on native
runners (see `.github/workflows/update-v8.yml`), not built locally. The
`v8-build/` directory (V8 source + build cache) is not committed and can be
deleted after building.
