# Orbital

A Go JavaScript runtime powered by V8 with Node.js-compatible APIs.

Use Orbital as a **CLI** to run scripts, or embed it as a **Go library** to execute JavaScript with sandboxing, native modules, and CommonJS/ESM support.

## Requirements

### Running the prebuilt CLI

On a standard glibc-based Linux or macOS system, no extra setup is needed beyond the binary itself. Linux builds dynamically link system libraries that are already present on most distros (`libc`, `libstdc++`, `libatomic`, etc.).

### Building or importing the library

Orbital uses CGO. You need:

- Go 1.24+
- A C++ compiler (`clang++` on macOS, `g++` on Linux)

The package ships with pre-built V8 libraries (currently V8 <!-- V8_VERSION -->13.1.201.1<!-- /V8_VERSION -->) for the following platforms:

| Platform | Directory |
|----------|-----------|
| macOS ARM64 (Apple Silicon) | `deps/v8/darwin-arm64/` |
| macOS x86_64 (Intel) | `deps/v8/darwin-x64/` |
| Linux ARM64 | `deps/v8/linux-arm64/` |
| Linux x86_64 | `deps/v8/linux-x64/` |

V8 is linked **statically** from `libv8_monolith.a`, so you only need the shipped `deps/v8/*/lib/` and `deps/v8/*/include/` files plus a C++ toolchain to compile the small CGO bridge in `pkg/v8`.

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

1. CGO compiles `pkg/v8/v8go.cc` against V8 headers
2. The linker pulls in `deps/v8/current/lib/libv8_monolith.a` (static)
3. Standard system libraries are linked dynamically on Linux

Consumers do **not** manually pass `-lv8_monolith` — the `#cgo` directives in `pkg/v8` handle that. They **do** need the correct platform directory selected via the `deps/v8/current` symlink (created automatically by `make check-v8`).

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

On Linux, build natively with `make build` or `make build-linux-arm64`. From macOS, `make build-linux-arm64` uses Docker only for the CGO link step against the prebuilt `.a`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for building V8 from source, Docker workflows, cross-compilation, testing, and development setup.

## License

MIT
