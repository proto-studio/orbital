// Command hellov8 is a minimal consumer of the orbital V8 bindings.
//
// The prebuilt V8 static libraries are not committed to the library. Fetch them
// (and generate the per-target cgo link file) with `go generate`, then build:
//
//	go generate ./...   # downloads V8 into ./.v8 and writes zz_generated_v8link_*.go
//	go run .
//
// Cross-compile by setting GOOS/GOARCH before `go generate` (and the build).
package main

//go:generate go run proto.zip/studio/orbital/cmd/v8setup -link-out . -link-pkg main

import (
	"fmt"
	"log"

	v8 "proto.zip/studio/orbital/pkg/v8"
)

func main() {
	iso, err := v8.NewIsolate()
	if err != nil {
		log.Fatalf("create isolate: %v", err)
	}
	defer iso.Dispose()

	ctx, err := iso.NewContext()
	if err != nil {
		log.Fatalf("create context: %v", err)
	}
	defer ctx.Dispose()

	// Exercise a few modern JS features to prove the engine really runs.
	const script = `
		const nums = [1, 2, 3, 4, 5];
		const sum = nums.reduce((a, b) => a + b, 0);
		` + "`sum=${sum} upper=${\"hello\".toUpperCase()}`" + `;
	`
	val, err := ctx.RunScript(script, "hello.js")
	if err != nil {
		log.Fatalf("run script: %v", err)
	}

	fmt.Printf("V8 says: %s\n", val.String())
}
