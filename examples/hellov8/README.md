# hellov8 — minimal orbital/v8 consumer

A tiny program that embeds V8 through `proto.zip/studio/orbital/pkg/v8` and runs
a snippet of JavaScript.

## Run it

```sh
go generate ./...   # fetch V8 into ./.v8 and write the cgo link file
go run .
```

Expected output:

```
V8 says: sum=15 upper=HELLO
```

## What `go generate` does

The `//go:generate` directive in `main.go` runs `cmd/v8setup`, which:

1. reads the version-pinned, checksum-verified manifest embedded in the library,
2. downloads the prebuilt V8 static libraries for your `GOOS/GOARCH` from the
   matching GitHub Release into a project-local `.v8/` directory, and
3. writes `zz_generated_v8link_<goos>_<goarch>.go` next to `main.go` carrying the
   `-L`/`-l` cgo `LDFLAGS` that statically link V8.

Both `.v8/` and the generated link file are disposable and git-ignored; re-run
`go generate` any time to restore them. To cross-compile, set `GOOS`/`GOARCH`
before both the generate and build steps, e.g.:

```sh
GOOS=linux GOARCH=arm64 go generate ./...
GOOS=linux GOARCH=arm64 go build .
```

## Note for external consumers

This example lives inside the orbital repository and therefore points at the
local checkout via a `replace` directive in `go.mod`. In your own project, drop
that line and depend on the published module directly.
