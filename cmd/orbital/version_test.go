package main

import (
	"strings"
	"testing"

	"proto.zip/studio/orbital/internal/v8dist"
)

// --version reports the module version the Release workflow records, not a fixed string.
func TestOrbitalVersionIsModuleVersion(t *testing.T) {
	if want := strings.TrimPrefix(v8dist.ModuleVersion, "v"); orbitalVersion != want {
		t.Fatalf("orbitalVersion = %q, want %q", orbitalVersion, want)
	}
}
