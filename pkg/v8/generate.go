package v8

// The prebuilt V8 static libraries are not committed. Running `go generate ./...`
// fetches the version-pinned, checksum-verified libraries into <module>/.v8/ and
// writes the per-target cgo link file (zz_generated_v8link_<goos>_<goarch>.go)
// that carries the -L/-l flags for this package. Do this before `go build`/`go
// test`. Cross-compile with e.g. `GOOS=linux GOARCH=arm64 go generate ./...`.
//
//go:generate go run proto.zip/studio/orbital/cmd/v8setup -link-out . -link-pkg v8
