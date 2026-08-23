# Orbital CLI Flags vs Node.js

This document compares Orbital command-line flags with Node.js.

## Supported Flags (Node.js Compatible)

| Flag | Orbital | Node.js | Description |
|------|-------|---------|-------------|
| `-e, --eval <code>` | ✅ | ✅ | Evaluate JavaScript code |
| `-p, --print <code>` | ✅ | ✅ | Evaluate and print result |
| `-c, --check` | ✅ | ✅ | Syntax check without executing |
| `-i, --interactive` | ✅ | ✅ | Start REPL after script/stdin |
| `-r, --require <module>` | ✅ | ✅ | Preload module at startup |
| `--input-type=<type>` | ✅ | ✅ | Set input type: 'module' or 'commonjs' |
| `-h, --help` | ✅ | ✅ | Show help message |
| `-v, --version` | ✅ | ✅ | Show version number |
| `--` | ✅ | ✅ | End of options, rest are script args |

## Orbital-Specific Flags (Sandbox/Security)

| Flag | Description |
|------|-------------|
| `--root <dir>` | Sandbox filesystem to this directory |
| `-s, --sandbox` | Full sandbox mode (fake system info, no network/process) |
| `--timeout <duration>` | Execution timeout (e.g., `30s`, `5m`) |
| `--title <title>` | Set process.title |
| `--no-warnings` | Silence warnings |

## Network Permission Flags (Deno-style)

Orbital provides Deno-compatible network permission flags.

| Flag | Orbital | Deno | Description |
|------|-------|------|-------------|
| `-N, --allow-net` | ✅ | ✅ | Allow all network access |
| `--allow-net=<hosts>` | ✅ | ✅ | Allow network to specific hosts |
| `--deny-net` | ✅ | ✅ | Deny all network access |
| `--deny-net=<hosts>` | ✅ | ✅ | Deny network to specific hosts |

### Host Format

Hosts can be specified as:
- Hostname: `example.com`
- Hostname with port: `example.com:443`
- IP address: `192.168.1.1`
- IP with port: `192.168.1.1:8080`
- CIDR notation: `10.0.0.0/8`
- IPv6: `[::1]` or `[::1]:8080`

### Precedence

Deny rules always take precedence over allow rules. This matches Deno's behavior.

### Network Permission Examples

```bash
# Allow all network access
orbital --allow-net script.js
orbital -N script.js              # Short form

# Deny all network access
orbital --deny-net script.js

# Allow only specific host
orbital --allow-net=example.com script.js

# Allow only specific host and port
orbital --allow-net=example.com:443 script.js

# Allow multiple hosts
orbital --allow-net=example.com,api.example.com script.js

# Allow all except private networks
orbital --allow-net --deny-net=10.0.0.0/8,192.168.0.0/16 script.js

# Allow all except localhost
orbital --allow-net --deny-net=127.0.0.1,localhost script.js
```

## Not Implemented (Node.js Only)

### Debugging/Inspection
| Flag | Description | Reason |
|------|-------------|--------|
| `--inspect[=host:port]` | Enable inspector | Requires V8 inspector protocol |
| `--inspect-brk[=host:port]` | Break at start | Requires V8 inspector protocol |
| `--inspect-publish-uid` | Inspector UID | Requires V8 inspector protocol |

### V8 Engine Options
| Flag | Description | Reason |
|------|-------------|--------|
| `--max-old-space-size=<size>` | V8 heap size | CLI not wired; use `v8.IsolateOptions.MaxHeapSizeInBytes` / `MaxOldGenerationSizeInBytes` at isolate create |
| `--max-semi-space-size=<size>` | V8 semi-space | V8 binding limitation |
| `--expose-gc` | Expose gc() | CLI not wired; `RequestGarbageCollectionForTesting` / `MemoryPressureNotification` available on Isolate |
| `--v8-options` | List V8 options | V8 binding limitation |

### Module System
| Flag | Description | Reason |
|------|-------------|--------|
| `--preserve-symlinks` | Don't resolve symlinks | Not yet implemented |
| `--preserve-symlinks-main` | For main module | Not yet implemented |
| `--experimental-modules` | Legacy flag | ES modules are enabled by default |
| `--experimental-vm-modules` | VM modules | Not applicable |
| `--experimental-import-meta-resolve` | import.meta.resolve | Partial support |

### Warnings/Deprecation
| Flag | Description | Reason |
|------|-------------|--------|
| `--trace-warnings` | Stack trace for warnings | Can be added |
| `--throw-deprecation` | Throw on deprecation | Can be added |
| `--trace-deprecation` | Stack trace for deprecation | Can be added |
| `--no-deprecation` | Silence deprecation | Can be added |
| `--pending-deprecation` | Emit pending deprecations | Can be added |

### Networking
| Flag | Description | Reason |
|------|-------------|--------|
| `--dns-result-order` | DNS result order | Can be added |
| `--enable-source-maps` | Enable source maps | Partial support |

### Other
| Flag | Description | Reason |
|------|-------------|--------|
| `--abort-on-uncaught-exception` | Abort on exception | Can be added |
| `--completion-bash` | Bash completion | Can be added |
| `--cpu-prof` | CPU profiling | V8 binding limitation |
| `--heap-prof` | Heap profiling | V8 binding limitation |
| `--report-*` | Diagnostic reports | Not yet implemented |
| `--frozen-intrinsics` | Freeze intrinsics | V8 binding limitation |
| `--disable-proto` | Disable __proto__ | V8 binding limitation |

## Usage Examples

```bash
# Run a JavaScript file
orbital script.js

# Evaluate code
orbital -e "console.log('Hello, World!')"

# Evaluate and print result
orbital -p "1 + 2"  # Prints: 3

# Check syntax only
orbital -c script.js

# Run script then start REPL
orbital -i script.js

# Preload modules
orbital -r ./setup.js -r ./config.js script.js

# Read from stdin as ES module
echo "export default 42" | orbital --input-type=module

# Sandboxed filesystem
orbital --root ./sandbox script.js

# Full sandbox mode
orbital -s --root ./sandbox script.js

# Execution timeout
orbital --timeout 30s script.js

# Pass arguments to script
orbital script.js -- arg1 arg2 arg3
# In script: process.argv = ['orbital', 'script.js', 'arg1', 'arg2', 'arg3']
```

## Environment Variables

| Variable | Orbital | Node.js | Description |
|----------|-------|---------|-------------|
| `NODE_PATH` | ✅ | ✅ | Additional module search paths |
| `NODE_REPL_HISTORY` | ✅ | ✅ | REPL history file path |
| `NODE_ENV` | ✅ | ✅ | Environment (development/production) |
| `NO_COLOR` | ⚠️ | ✅ | Disable colors (partial support) |

## REPL Commands

Both Orbital and Node.js support these REPL commands:

| Command | Description |
|---------|-------------|
| `.exit` | Exit the REPL |
| `.help` | Show help |
| `.clear` | Clear current input |
| `.history` | Show command history (Orbital specific) |

## Notes

1. **Sandbox Mode**: Orbital's `--sandbox` and `--root` flags have no Node.js equivalent. They provide security isolation for running untrusted code.

2. **Timeout**: The `--timeout` flag allows setting execution limits, useful for sandboxed environments.

3. **ES Modules**: Files ending in `.mjs` are automatically treated as ES modules. Use `--input-type=module` for stdin.

4. **V8 Bindings**: Some Node.js flags that directly control V8 behavior are not available due to the v8go binding limitations.
