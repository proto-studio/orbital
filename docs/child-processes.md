# Child processes

How `child_process` reaches the embedder's `runtime.Config.ProcessSpawner`, the built-in
spawners, and how to write your own. See the sandboxing example in the
[README](../README.md#sandboxing).

## What a spawner receives

Every `child_process` function a script calls (`spawn`, `exec`, `execFile`, `fork` and the
sync forms) starts its process through `ProcessSpawner.Spawn`, as Node builds them all on
spawn. `fork` spawns `process.execPath` with the module as an argument. What `Spawn`
receives depends on the call:

| Script call | `command` | `args` | `options.Shell` |
| --- | --- | --- | --- |
| `spawn(file, args)`, `execFile(file, args)` | `file` | `args` | `false` |
| `exec(line)`, `execSync(line)` | `line` | none | `true` |
| `spawn(file, args, { shell: true })` | `file` and `args` joined by spaces | none | `true` |
| `spawn(file, args, { shell: '/bin/bash' })` | `/bin/bash` | `-c` and the joined line | `false` |

With `Shell` set, `command` is a command line for `/bin/sh -c`, not a program name.

`SpawnOptions` also carries the child's working directory (`Cwd`), its environment
(`Env`), and `Detached`, `Argv0` and `Stdio`. Scripts always pass `Env`: the `env` option,
or `process.env` when there is none, as in Node. A nil `Env` from Go code means the host
process's environment.

The interface also has `Exec` and `ExecFile`, which run a command to completion and return
its output. Orbital's own modules don't call them, but other Go code may, so a spawner
should handle them as carefully as `Spawn`. `Exec` receives a whole command line for the
shell. `ExecFile` runs `file` directly.

A spawner refuses a call by returning an error. Scripts see these as Node's error codes:

| Error | Code |
| --- | --- |
| `runtime.ErrCommandNotAllowed` | `EACCES` |
| `runtime.ErrSpawnBlocked` | `EPERM` |
| `runtime.ErrTooManyProcesses` | `EMFILE` |
| a `syscall.Errno` | its own code, such as `ENOENT` |

## Built-in spawners

- `NewRealProcessSpawner()` starts real processes with no checks. It is what a nil
  `Config.ProcessSpawner` uses.
- `NewNoOpProcessSpawner()` refuses everything with `ErrSpawnBlocked`.
- `NewSandboxedProcessSpawner(cfg)` checks each call against a `SandboxProcessConfig`, then
  starts it with the real spawner. `NewAllowListProcessSpawner(paths...)` and
  `NewDenyListProcessSpawner(paths...)` are shorthands for the two modes below.

`SandboxProcessConfig` has these fields:

- `AllowedCommands`: in allowlist mode (`AllowAll` false), the absolute paths of the
  executables that may run, such as `/bin/ls`.
- `AllowAll` and `BlockedCommands`: in denylist mode, everything may run except the
  executables at the absolute paths in `BlockedCommands`. `BlockedCommands` applies in
  allowlist mode too.
- `AllowShellCommands`: allows command lines, from `Exec` or `Spawn` with `Shell`, in
  allowlist mode. Without it, they're refused. In either mode they're refused if
  `/bin/sh` is blocked.
- `MaxProcesses`: how many children may run at once, counting `Exec` and `ExecFile` while
  they run. 0 means no limit.

The checks compare executables, not names. Each entry is resolved through symlinks when the
rules are first used. Each command is resolved the way it will run: a bare name such as
`ls` on the host's `PATH` (not the `PATH` in the script's environment), a relative path
against the working directory, and then through symlinks. The command may run only if that
path equals an allowed entry, and no blocked one. The resolved path is what runs.

Some programs act on the name they run under, so in allowlist mode a program runs only
under the name of an entry for it. If the name the script gave (or its `argv0` option) has
the base name of one of those entries, the process sees it as `argv[0]`. Otherwise it sees
the first entry's path. For example, with `/bin/sh` allowed, `spawn('sh')` runs `/bin/sh` as
`sh`.

An entry that isn't an absolute path to an existing file makes the spawner refuse
everything, and `Err()` reports which entry. Check it after building a spawner:

```go
spawner := runtime.NewAllowListProcessSpawner("/bin/ls", "/usr/bin/grep")
if err := spawner.Err(); err != nil {
	log.Fatal(err)
}
```

## Writing your own

`ProcessSpawner` is an interface, so you can wrap a built-in spawner to log calls, refuse
some, or change their options, and pass the rest on. [`examples/process-spawner`](../examples/process-spawner/main.go)
logs every call, allows a few programs by name, and refuses command lines:

```go
func (p *policySpawner) Spawn(ctx context.Context, file string, args []string, options *runtime.SpawnOptions) (runtime.ChildProcess, error) {
	if options != nil && options.Shell {
		return nil, runtime.ErrCommandNotAllowed // a command line, not a program name
	}
	if !p.allowed[file] {
		return nil, runtime.ErrCommandNotAllowed
	}
	return p.next.Spawn(ctx, file, args, options)
}
```

Run it with `go run ./examples/process-spawner`.

## Choosing a policy

- Allow programs by name in `Spawn` and `ExecFile`, and compare the whole `command` string.
  A wrapper that allows `echo` by name allows only the bare name, which the sandboxed
  spawner underneath then resolves on the host's `PATH` and checks by path.
- Refuse `Exec`, and `Spawn` with `Shell` set. They receive a command line, and a shell runs
  whatever the line says, so its first word doesn't tell you what runs. This is what the
  sandboxed spawner does unless `AllowShellCommands` is set.
- Treat programs that run other programs as you would a shell: shells such as `sh` and
  `bash`, interpreters such as `node` and `python`, and launchers such as `env` and `xargs`.
  Their arguments decide what runs, so allowing one by name allows what it can run.
- Wrap `NewSandboxedProcessSpawner` rather than the real spawner, to keep its path checks
  and `MaxProcesses` limit underneath your own rules.
