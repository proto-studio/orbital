package v8dist

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Options configures a single Install call.
type Options struct {
	// GOOS/GOARCH select the target. Resolve them with ResolveTarget first.
	GOOS   string
	GOARCH string
	// Source provides the artifact. Defaults to a ReleaseSource built from the
	// manifest when nil.
	Source Source
	// InstallRoot is the .v8 root directory. Defaults to DefaultInstallRoot.
	InstallRoot string
	// LinkOut, when set, is the directory a per-target cgo link file is written
	// into. LinkPkg overrides the auto-detected Go package name for that file.
	LinkOut string
	LinkPkg string
	// Log receives human-readable progress lines (optional).
	Log io.Writer
}

// Result reports what Install did.
type Result struct {
	Target     Target
	InstallDir string
	LibDir     string
	LinkFile   string
	Skipped    bool
}

func (o *Options) logf(format string, args ...any) {
	if o.Log != nil {
		fmt.Fprintf(o.Log, format+"\n", args...)
	}
}

// Install ensures the prebuilt V8 library for the target is present under the
// versioned .v8 layout, then (optionally) writes the per-target cgo link file.
// It is idempotent: a present, checksum-matching install is left untouched.
func Install(opts Options) (*Result, error) {
	m, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	return InstallWithManifest(m, opts)
}

// InstallWithManifest is like Install but uses a caller-supplied manifest. It
// exists primarily for testing; production code should use Install.
func InstallWithManifest(m *Manifest, opts Options) (*Result, error) {
	goos, goarch := ResolveTarget(opts.GOOS, opts.GOARCH)
	target, err := m.TargetFor(goos, goarch)
	if err != nil {
		return nil, err
	}

	installRoot := opts.InstallRoot
	if installRoot == "" {
		installRoot, err = DefaultInstallRoot()
		if err != nil {
			return nil, err
		}
	}

	installDir := m.InstallDir(installRoot, goos, goarch)
	libDir := filepath.Join(installDir, "lib")

	res := &Result{Target: target, InstallDir: installDir, LibDir: libDir}

	if alreadyInstalled(installDir, libDir, target.SHA256) {
		opts.logf(">>> V8 %s (%s) already present: %s", m.ModuleVersion, goos+"/"+goarch, installDir)
		res.Skipped = true
	} else {
		src := opts.Source
		if src == nil {
			src = &ReleaseSource{Owner: m.Owner, Repo: m.Repo, Tag: m.Tag()}
		}
		if err := download(m, src, target, installRoot, installDir, libDir, &opts); err != nil {
			return nil, err
		}
	}

	if opts.LinkOut != "" {
		linkPath, err := writeLinkFile(&opts, libDir)
		if err != nil {
			return nil, err
		}
		res.LinkFile = linkPath
		opts.logf(">>> Wrote cgo link file: %s", linkPath)
	}

	return res, nil
}

func alreadyInstalled(installDir, libDir, wantSHA string) bool {
	if wantSHA == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(libDir, SentinelLib)); err != nil {
		return false
	}
	got, err := os.ReadFile(filepath.Join(installDir, markerFile))
	if err != nil {
		return false
	}
	return string(got) == wantSHA
}

func download(m *Manifest, src Source, t Target, installRoot, installDir, libDir string, opts *Options) error {
	versionDir := filepath.Dir(installDir)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp(versionDir, ".tmp-"+t.GOOS+"-"+t.GOARCH+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, t.Filename)
	opts.logf(">>> Downloading %s", src.Describe(t))
	if err := src.Fetch(t, archivePath); err != nil {
		return err
	}

	if t.SHA256 == "" {
		return fmt.Errorf("v8dist: manifest has no sha256 for %s/%s; cannot verify %s",
			t.GOOS, t.GOARCH, t.Filename)
	}
	got, err := sha256File(archivePath)
	if err != nil {
		return &ExtractError{Filename: t.Filename, GOOS: t.GOOS, GOARCH: t.GOARCH, Err: err}
	}
	if got != t.SHA256 {
		return &ChecksumError{Filename: t.Filename, Want: t.SHA256, Got: got, GOOS: t.GOOS, GOARCH: t.GOARCH}
	}
	opts.logf(">>> Verified sha256 %s", got)

	stageDir := filepath.Join(tmpDir, "stage")
	if err := extractTarGz(archivePath, stageDir); err != nil {
		return &ExtractError{Filename: t.Filename, GOOS: t.GOOS, GOARCH: t.GOARCH, Err: err}
	}

	stageLib := filepath.Join(stageDir, "lib")
	for _, lib := range []string{LibMonolith, LibGlue} {
		if _, err := os.Stat(filepath.Join(stageLib, lib)); err != nil {
			return &MissingLibraryError{Missing: lib, Dir: stageLib, GOOS: t.GOOS, GOARCH: t.GOARCH}
		}
	}

	if err := os.WriteFile(filepath.Join(stageDir, markerFile), []byte(t.SHA256), 0o644); err != nil {
		return err
	}

	// Atomic-ish swap: remove any previous install, then rename the staged dir
	// into place (same filesystem, so rename is atomic on POSIX).
	if err := os.RemoveAll(installDir); err != nil {
		return err
	}
	if err := os.Rename(stageDir, installDir); err != nil {
		return err
	}
	opts.logf(">>> Installed V8 %s (%s): %s", m.ModuleVersion, t.GOOS+"/"+t.GOARCH, installDir)
	return nil
}
