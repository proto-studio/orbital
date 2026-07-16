// Command v8setup downloads the version-pinned, checksum-verified prebuilt V8
// static libraries for a target platform into a project-local .v8/ directory and
// writes the per-target cgo link file the consumer's build uses.
//
// It is normally invoked via `go generate`:
//
//	//go:generate go run proto.zip/studio/orbital/cmd/v8setup -link-out .
//
// Sources (‑source): "release" (default, GitHub Release asset), "actions"
// (a GitHub Actions run's artifacts, via the gh CLI — used by CI), or "local"
// (copy from ‑local-dir — used by dev/tests).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"proto.zip/studio/orbital/internal/v8dist"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		sourceName  = flag.String("source", envOr("V8_ARTIFACT_SOURCE", "release"), "artifact source: release|actions|local")
		runID       = flag.String("run-id", os.Getenv("V8_ACTIONS_RUN_ID"), "GitHub Actions run id (for -source actions)")
		localDir    = flag.String("local-dir", os.Getenv("V8_LOCAL_DIR"), "directory containing the artifact (for -source local)")
		linkOut     = flag.String("link-out", "", "directory to write the generated cgo link file into")
		linkPkg     = flag.String("link-pkg", "", "Go package name for the generated link file (default: auto-detect)")
		target      = flag.String("target", "", "target as goos/goarch (default: $GOOS/$GOARCH or host)")
		installRoot = flag.String("install-root", "", "override the .v8 root (default: $V8_HOME or <module>/.v8)")
	)
	flag.Parse()

	goos, goarch, err := parseTarget(*target)
	if err != nil {
		return err
	}
	goos, goarch = v8dist.ResolveTarget(goos, goarch)

	m, err := v8dist.LoadManifest()
	if err != nil {
		return err
	}

	src, err := buildSource(*sourceName, m, *runID, *localDir)
	if err != nil {
		return err
	}

	res, err := v8dist.Install(v8dist.Options{
		GOOS:        goos,
		GOARCH:      goarch,
		Source:      src,
		InstallRoot: *installRoot,
		LinkOut:     *linkOut,
		LinkPkg:     *linkPkg,
		Log:         os.Stderr,
	})
	if err != nil {
		return err
	}

	if res.Skipped {
		fmt.Fprintf(os.Stderr, "v8setup: up to date (%s %s/%s)\n", m.ModuleVersion, goos, goarch)
	} else {
		fmt.Fprintf(os.Stderr, "v8setup: installed V8 %s (%s/%s)\n", m.ModuleVersion, goos, goarch)
	}
	return nil
}

func buildSource(name string, m *v8dist.Manifest, runID, localDir string) (v8dist.Source, error) {
	switch name {
	case "release":
		return &v8dist.ReleaseSource{Owner: m.Owner, Repo: m.Repo, Tag: m.Tag()}, nil
	case "actions":
		if runID == "" {
			return nil, fmt.Errorf("v8setup: -source actions requires -run-id (or $V8_ACTIONS_RUN_ID)")
		}
		return &v8dist.ActionsSource{RunID: runID}, nil
	case "local":
		if localDir == "" {
			return nil, fmt.Errorf("v8setup: -source local requires -local-dir (or $V8_LOCAL_DIR)")
		}
		return &v8dist.LocalSource{Dir: localDir}, nil
	default:
		return nil, fmt.Errorf("v8setup: unknown -source %q (want release|actions|local)", name)
	}
}

func parseTarget(s string) (goos, goarch string, err error) {
	if s == "" {
		return "", "", nil
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("v8setup: invalid -target %q (want goos/goarch)", s)
	}
	return parts[0], parts[1], nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
