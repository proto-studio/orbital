# Orbital

A Go JavaScript runtime powered by V8 with Node.js-compatible APIs.

Use Orbital as a **CLI** to run scripts, or embed it as a **Go library** to execute JavaScript with sandboxing, native modules, and CommonJS/ESM support.

## Requirements

### Running the prebuilt CLI

On a standard glibc-based Linux or macOS system, no extra setup is needed beyond the binary itself. Linux builds dynamically link system libraries that are already present on most distros (`libc`, `libstdc++`, `libatomic`, etc.).

### Building or importing the library

Orbital uses CGO and links a prebuilt V8 static library. You need:

- Go 1.24+
- A C toolchain for CGO (`clang` on macOS, `gcc` on Linux)

You do **not** need a C++ compiler or the V8 headers: the C++ bridge is shipped
pre-compiled (see [Linking model](#linking-model)), so CGO only compiles a small
pure-C shim and links the prebuilt archives.

The V8 static libraries are **not committed to the repository**. They are
published as checksum-verified GitHub Release assets (currently V8
<!-- V8_VERSION -->15.0.1240245<!-- /V8_VERSION -->) and fetched on demand by
`go generate` into a project-local `.v8/` directory. Prebuilt libraries are
available for:

| Platform | Target |
|----------|--------|
| macOS ARM64 (Apple Silicon) | `darwin/arm64` |
| Linux ARM64 | `linux/arm64` |
| Linux x86_64 | `linux/amd64` |

> Intel macOS (`darwin/amd64`) is not supported: current V8 requires the
> macOS 15+ SDK, which ships only on Apple Silicon.

See [Installing the V8 runtime](#installing-the-v8-runtime) for the one-time
setup an embedding project needs.

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

Fetch the V8 runtime once, then build with CGO enabled:

```bash
go generate ./...          # downloads V8 into .v8/ and writes the cgo link file
CGO_ENABLED=1 go build -o myapp .
```

See [Installing the V8 runtime](#installing-the-v8-runtime) for the one-time
`go generate` wiring, and `examples/native/main.go` /
`examples/sandbox-server/main.go` for fuller examples.

## Installing the V8 runtime

Because the V8 libraries live in the (clearable) Go module cache when you `go get`
Orbital, they cannot be linked directly from there. Instead, a small setup tool
(`cmd/v8setup`) downloads the version-pinned, checksum-verified libraries into a
project-local `.v8/` directory and writes a per-target cgo file that carries the
`-L`/`-l` link flags. Wire it into your project **once**:

1. Add a tiny helper package that runs the setup tool via `go generate` and is
   blank-imported by your `main` so its generated link flags are in the build:

```go
// internal/v8dist/v8dist.go
//go:generate go run proto.zip/studio/orbital/cmd/v8setup -link-out .
package v8dist
```

```go
// main.go
import _ "yourmodule/internal/v8dist"
```

2. Run `go generate ./...` before building. It fetches the libraries for your
   `GOOS`/`GOARCH` into `.v8/<version>/<goos>-<goarch>/` and writes
   `zz_generated_v8link_<goos>_<goarch>.go` into the helper package.

3. Add `.v8/` and `**/zz_generated_v8link_*.go` to your `.gitignore` (the
   libraries and link file are machine/target-specific and regenerated).

`go build` does **not** run `go generate` automatically — run it yourself after
`go get`, after bumping the Orbital version, or in CI before building.

### Cross-compiling

`go generate` respects `GOOS`/`GOARCH`, so you can install multiple targets side
by side and cross-compile:

```bash
GOOS=linux GOARCH=arm64 go generate ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -o myapp-linux-arm64 .
```

Each target gets its own `.v8/<version>/<goos>-<goarch>/` directory and its own
build-tagged link file, so targets never collide.

### Layout, caching, and clearing

```
.v8/
  v1.4.2/                    # pinned module version
    linux-amd64/lib/*.a
    darwin-arm64/lib/*.a
```

- **Custom location:** set `V8_HOME=/path` to install into a shared OS user-data
  directory instead of the project (the generated link file then uses an absolute
  path; keep it gitignored).
- **CI caching:** cache the `.v8/` directory keyed on the Orbital module version
  to skip re-downloading on every run.
- **Clearing:** delete `.v8/` (and re-run `go generate`) to force a fresh,
  re-verified download.

### If the link fails

If `go build` reports `cannot find -lv8go_glue` or `undefined reference` errors,
the V8 runtime hasn't been installed for that target. Run the exact command the
tool prints, e.g.:

```bash
GOOS=linux GOARCH=arm64 go generate ./...
```

## Packages

| Package | Purpose |
|---------|---------|
| `pkg/v8` | Low-level V8 bindings (CGO) |
| `pkg/runtime` | JavaScript runtime, event loop, sandbox interfaces |
| `internal/nodejs/*` | Node.js standard library modules (`fs`, `console`, `process`, etc.) |
| `cmd/orbital` | CLI binary |

Register the Node.js modules you need with `runtime.RegisterModule()`. The CLI registers the full set; library users pick only what their scripts require.

## Linking model

After `go generate` has installed the runtime, when you `go build`:

1. CGO compiles only the pure-C boundary (`pkg/v8/v8go.h`) with your C compiler.
   `pkg/v8` itself carries **no** `-L`/`-l` flags.
2. The generated `zz_generated_v8link_<goos>_<goarch>.go` file in your helper
   package supplies the `-L${SRCDIR}/…/.v8/<version>/<goos>-<goarch>/lib` path and
   the `-l` flags. Because it is blank-imported into your build, its flags are
   aggregated at the final link step, pulling in the static archives:
   `libv8go_glue.a`, `libv8_monolith.a`, and `libv8_libcxx.a`.
3. Standard system libraries are linked dynamically on Linux.

The C++ bridge (`pkg/v8/csrc/v8go.cc`) is **not** compiled by CGO. V8 is built
with Chromium's custom libc++ (the `std::__Cr::` inline namespace), which is
ABI-incompatible with the system `libstdc++` a stock `g++` would use. The bridge
is therefore pre-compiled per platform into `libv8go_glue.a` with V8's own
toolchain (see `scripts/build-glue.py`) so its `std::` symbols match
`libv8_monolith.a`. This keeps the V8 sandbox enabled while letting consumers
link with a plain C toolchain. Chromium's libc++ ships as a separate
`libv8_libcxx.a`.

Why not commit the archives? The Go module proxy / `go get` do not run Git LFS
smudge, so LFS is unusable, and V8's monolith exceeds GitHub's 100MB per-file Git
limit. GitHub **Releases**, by contrast, allow multi-GB assets — so the libraries
are published there and fetched on demand, and the repository stays small.

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
- **Built-ins:** `fs`, `path`, `stream`, `url`, `os`, `util`, `crypto`, `http`, `worker_threads` (isolate-backed), `async_hooks`, and more

Not yet implemented: `cluster`, full stream piping, async iterators. `async_hooks`
context does not yet cross native `await` boundaries (see `docs/async-context.md`).

## Platform support

Prebuilt V8 libraries are published as Release assets for macOS (ARM64) and
Linux (ARM64/x86_64) — see the table under
[Requirements](#building-or-importing-the-library).

After `go generate`, build for your current platform with `make build` (or
`make build-native`), or cross-compile with `GOOS`/`GOARCH` (see
[Cross-compiling](#cross-compiling)). Refreshed V8 libraries are built by CI on
native runners per platform and published to a new Release.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for building V8 from source, testing, and development setup.

## License

MIT
