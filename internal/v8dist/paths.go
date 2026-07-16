package v8dist

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Library file names shipped inside every artifact (under a top-level lib/).
const (
	LibMonolith = "libv8_monolith.a"
	LibCxx      = "libv8_libcxx.a"
	LibGlue     = "libv8go_glue.a"
)

// SentinelLib is the archive whose presence marks a complete install. The glue
// ships on every platform under a stable name, so it is a reliable sentinel.
const SentinelLib = LibGlue

// markerFile records the sha256 of the artifact a directory was installed from,
// enabling an idempotent skip on re-runs.
const markerFile = ".artifact.sha256"

// ResolveTarget returns the GOOS/GOARCH to install for. Explicit non-empty
// arguments win; otherwise the GOOS/GOARCH environment (as set by `go generate`)
// is honored; otherwise the running toolchain's defaults are used.
func ResolveTarget(goos, goarch string) (string, string) {
	if goos == "" {
		goos = os.Getenv("GOOS")
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = os.Getenv("GOARCH")
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// FindModuleRoot walks up from start looking for a go.mod file and returns the
// directory containing it. If none is found it returns start.
func FindModuleRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

// DefaultInstallRoot returns the directory that holds the versioned .v8 layout.
// It is $V8_HOME when set (allowing an OS user-data dir shared across projects),
// otherwise the project-local <module-root>/.v8.
func DefaultInstallRoot() (string, error) {
	if home := os.Getenv("V8_HOME"); home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return "", fmt.Errorf("v8dist: resolving V8_HOME %q: %w", home, err)
		}
		return abs, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("v8dist: determining working directory: %w", err)
	}
	return filepath.Join(FindModuleRoot(cwd), ".v8"), nil
}

// InstallDir returns the versioned per-target directory inside installRoot:
// <installRoot>/<module_version>/<goos>-<goarch>.
func (m *Manifest) InstallDir(installRoot, goos, goarch string) string {
	return filepath.Join(installRoot, m.ModuleVersion, goos+"-"+goarch)
}
