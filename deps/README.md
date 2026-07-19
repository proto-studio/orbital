# Dependencies

## V8 JavaScript Engine

V8 static libraries are **not committed** to this repository. They are built by
CI on native runners, packaged into `dist/v8-<goos>-<goarch>.tar.gz`, and
published as checksum-verified GitHub Release assets. Consumers (and this repo's
own tests) fetch them on demand via `go generate` into a project-local `.v8/`
directory — see `cmd/v8setup` and the top-level `README.md`. The tarballs use
gzip so the fetch tool depends only on the Go standard library.

Public headers (`include/`) are packaged separately as `v8-headers.tar.gz`,
uploaded as the CI `v8-headers` artifact, and published on each GitHub Release
alongside the platform lib tarballs. Glue-only rebuilds (`make v8-glue`) need
those headers but not the full V8 source tree — see
[CONTRIBUTING.md](../CONTRIBUTING.md).

### Local builds

`deps/v8/` is a **transient build-output directory** (gitignored):

```
deps/v8/
├── include/                 # Public headers (for make v8-glue)
└── <goos>-<goarch>/lib/
    ├── libv8_monolith.a     # V8 itself
    ├── libv8_libcxx.a       # Chromium's hardened libc++/libc++abi
    └── libv8go_glue.a       # pre-compiled cgo C++ glue (V8's exact ABI)
```

```bash
make v8-native            # build for the current host (libs + headers)
make v8-setup-local       # install libs into .v8/ + generate the link file

# After editing pkg/v8/csrc/v8go.cc — do NOT rebuild the monolith:
make v8-glue              # needs deps/v8/include (or V8_INCLUDE=…) + out.gn
make build-native
```

Install headers without a full source tree:

```bash
make v8-headers-setup RUN_ID=<update-v8-run-id>
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
deleted after building — keep `out.gn` if you still want `make v8-glue`, and
use the `v8-headers` artifact for `include/`.
