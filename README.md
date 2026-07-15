# Orbital

A Go JavaScript runtime powered by V8 with Node.js-compatible APIs.

Use Orbital as a **CLI** to run scripts, or embed it as a **Go library** to execute JavaScript with sandboxing, native modules, and CommonJS/ESM support.

## Requirements

### Running the prebuilt CLI

On a standard glibc-based Linux or macOS system, no extra setup is needed beyond the binary itself. Linux builds dynamically link system libraries that are already present on most distros (`libc`, `libstdc++`, `libatomic`, etc.).

### Building or importing the library

Orbital uses CGO. You need:

- Go 1.24+
- A C toolchain for CGO (`clang` on macOS, `gcc` on Linux)

You do **not** need a C++ compiler or the V8 headers: the C++ bridge is shipped
pre-compiled (see [Linking model](#linking-model)), so CGO only compiles a small
pure-C shim and links the prebuilt archives.

The package ships with pre-built V8 libraries (currently V8 <!-- V8_VERSION -->13.1.201.1<!-- /V8_VERSION -->) for the following platforms:

| Platform | Directory |
|----------|-----------|
| macOS ARM64 (Apple Silicon) | `deps/v8/darwin-arm64/` |
| macOS x86_64 (Intel) | `deps/v8/darwin-x64/` |
| Linux ARM64 | `deps/v8/linux-arm64/` |
| Linux x86_64 | `deps/v8/linux-x64/` |

Each platform directory ships static archives that are linked **statically**:
the V8 engine (`libv8_monolith.a`, split into `libv8_monolith_0.a` /
`libv8_monolith_1.a` on Linux — see [Linking model](#linking-model)),
`libv8_libcxx.a` (Chromium's libc++/libc++abi), and `libv8go_glue.a` (the
pre-compiled Go↔V8 C++ bridge). You only need the shipped `deps/v8/*/lib/` files
and a C toolchain for CGO.

## Quick start

### CLI

```bash
# On your platform (after `make build-native` on macOS/Linux)
./build/orbital script.js

# Evaluate inline code
./build/orbital -e "console.log('Hello!')"

# REPL
./build/orbital
```

Prebuilt Linux binaries (when available):

```bash
./build/orbital-linux-arm64 script.js
./build/orbital-linux-x64 script.js
```

### Go library

```go
package main

import (
	"fmt"
	"os"

	"proto.zip/studio/orbital/internal/nodejs/console"
	"proto.zip/studio/orbital/internal/nodejs/process"
	"proto.zip/studio/orbital/pkg/runtime"
)

func main() {
	rt, err := runtime.New(nil)
	if err != nil {
		panic(err)
	}
	defer rt.Dispose()

	_ = console.New().Register(rt)
	_ = process.New().Register(rt)

	result, err := rt.RunScript(`console.log('Hello from Orbital!')`, "main.js")
	if err != nil {
		panic(err)
	}
	fmt.Println(result.String())
}
```

Build with CGO enabled on a machine that matches the target platform:

```bash
CGO_ENABLED=1 go build -o myapp .
```

See `examples/native/main.go` and `examples/sandbox-server/main.go` for fuller examples including native Go modules and sandboxing.

## Packages

| Package | Purpose |
|---------|---------|
| `pkg/v8` | Low-level V8 bindings (CGO) |
| `pkg/runtime` | JavaScript runtime, event loop, sandbox interfaces |
| `internal/nodejs/*` | Node.js standard library modules (`fs`, `console`, `process`, etc.) |
| `cmd/orbital` | CLI binary |

Register the Node.js modules you need with `runtime.RegisterModule()`. The CLI registers the full set; library users pick only what their scripts require.

## Linking model

When you `go build` an app that imports `pkg/v8`:

1. CGO compiles only the pure-C boundary (`pkg/v8/v8go.h`) with your C compiler
2. The linker pulls in the prebuilt static archives (`libv8go_glue.a`, the `libv8_monolith*` V8 archives, and `libv8_libcxx.a`) from `deps/v8/<os>-<arch>/lib/`
3. Standard system libraries are linked dynamically on Linux

Consumers do **not** compile any C++ and do **not** manually pass `-l` flags —
the `#cgo` directives in `pkg/v8` select the correct `deps/v8/<os>-<arch>`
directory automatically via Go's `GOOS`/`GOARCH` build constraints.

The C++ bridge (`pkg/v8/csrc/v8go.cc`) is **not** compiled by CGO. V8 is built
with Chromium's custom libc++ (the `std::__Cr::` inline namespace), which is
ABI-incompatible with the system `libstdc++` a stock `g++` would use. The bridge
is therefore pre-compiled per platform into `libv8go_glue.a` with V8's own
toolchain (see `scripts/build-glue.py`) so its `std::` symbols match
`libv8_monolith.a`. This keeps the V8 sandbox enabled while letting consumers
link with a plain C toolchain.

V8 15.x's monolith is ~126MB, over GitHub's 100MB per-file limit, so on Linux it
is split into `libv8_monolith_0.a` / `libv8_monolith_1.a` and linked back together
inside `-Wl,--start-group`. Chromium's libc++ ships as a separate `libv8_libcxx.a`.
Git LFS is intentionally avoided because `go get` / the module proxy do not
resolve LFS pointers, so the archives are committed as plain files under 100MB.

## Sandboxing

```bash
# Restrict filesystem to a directory
./build/orbital --root ./sandbox script.js

# Full sandbox (fake system info, blocked network)
./build/orbital -s --root ./sandbox script.js

# Network allow/deny lists
./build/orbital --allow-net=api.example.com script.js
```

In library code, pass a `runtime.Config` with sandboxed implementations:

```go
cfg := &runtime.Config{
	Filesystem:     runtime.NewLocalFilesystem("/sandbox"),
	SystemInfo:     runtime.NewSandboxedSystemInfo(nil),
	HTTPClient:     runtime.NewFilteredHTTPClient(runtime.DenyAllPolicy()),
	ProcessSpawner: runtime.NewNoOpProcessSpawner(),
	DocumentRoot:   "/sandbox",
	Timeout:        30 * time.Second,
}
rt, err := runtime.New(cfg)
```

## CLI options

| Flag | Description |
|------|-------------|
| `-e, --eval <code>` | Evaluate JavaScript code |
| `-p, --print <code>` | Evaluate and print result |
| `-c, --check` | Syntax check without executing |
| `-i, --interactive` | Start REPL after script/stdin |
| `-r, --require <module>` | Preload module at startup |
| `--root <dir>` | Sandbox filesystem to directory |
| `-s, --sandbox` | Fake system info and block network |
| `--timeout <duration>` | Execution timeout (e.g. `30s`, `5m`) |
| `-N, --allow-net` | Allow all network access |
| `--allow-net=<hosts>` | Allow specific hosts |
| `--deny-net` | Deny all network access |
| `-h, --help` | Show help |
| `-v, --version` | Show version |

## Node.js APIs

Orbital implements a growing subset of Node.js:

- **Modules:** CommonJS (`require`) and ES Modules (`import`/`export`)
- **Globals:** `console`, timers, `process`, `EventEmitter`, `Buffer`, `URL`, `TextEncoder`/`TextDecoder`, `atob`/`btoa`
- **Built-ins:** `fs`, `path`, `stream`, `url`, `os`, `util`, `crypto`, `http`, and more

Not yet implemented: `worker_threads`, `cluster`, full stream piping, async iterators.

## Platform support

Pre-built V8 libraries ship for macOS (ARM64/x86_64) and Linux (ARM64/x86_64) — see the table under [Requirements](#building-or-importing-the-library).

Build for your current platform with `make build` (or `make build-native`). Multi-platform binaries and refreshed V8 libraries are produced by CI on native runners per platform.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for building V8 from source, testing, and development setup.

## License

MIT
