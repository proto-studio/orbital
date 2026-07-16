package v8dist

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// makeArtifact writes a v8-<target>.tar.zst containing lib/<the three archives>
// into dir and returns its path and sha256.
func makeArtifact(t *testing.T, dir, filename string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, name := range []string{LibMonolith, LibGlue, LibCxx} {
		body := []byte("dummy " + name)
		if err := tw.WriteHeader(&tar.Header{
			Name: "lib/" + name,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:])
}

func TestInstallWithManifest(t *testing.T) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	filename := "v8-" + goos + "-" + goarch + ".tar.zst"

	srcDir := t.TempDir()
	_, sum := makeArtifact(t, srcDir, filename)

	m := &Manifest{
		V8Version:     "13.1.201.1",
		ModuleVersion: "v9.9.9",
		Owner:         "proto-studio",
		Repo:          "orbital",
		Targets: []Target{
			{GOOS: goos, GOARCH: goarch, Filename: filename, SHA256: sum},
		},
	}

	installRoot := t.TempDir()
	linkOut := t.TempDir()
	// A stub package file so package-name detection works.
	if err := os.WriteFile(filepath.Join(linkOut, "doc.go"), []byte("package consumer\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{
		GOOS:        goos,
		GOARCH:      goarch,
		Source:      &LocalSource{Dir: srcDir},
		InstallRoot: installRoot,
		LinkOut:     linkOut,
	}

	res, err := InstallWithManifest(m, opts)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Skipped {
		t.Fatal("first install should not be skipped")
	}

	for _, lib := range []string{LibMonolith, LibGlue, LibCxx} {
		if _, err := os.Stat(filepath.Join(res.LibDir, lib)); err != nil {
			t.Errorf("missing installed lib %s: %v", lib, err)
		}
	}
	wantDir := filepath.Join(installRoot, "v9.9.9", goos+"-"+goarch)
	if res.InstallDir != wantDir {
		t.Errorf("install dir = %s, want %s", res.InstallDir, wantDir)
	}

	linkContent, err := os.ReadFile(res.LinkFile)
	if err != nil {
		t.Fatalf("read link file: %v", err)
	}
	lc := string(linkContent)
	if !strings.Contains(lc, "package consumer") {
		t.Errorf("link file missing detected package name:\n%s", lc)
	}
	if !strings.Contains(lc, "//go:build "+goos+" && "+goarch) {
		t.Errorf("link file missing build constraint:\n%s", lc)
	}
	if !strings.Contains(lc, "-lv8go_glue") || !strings.Contains(lc, "-lv8_monolith") {
		t.Errorf("link file missing link flags:\n%s", lc)
	}
	if !strings.Contains(lc, "${SRCDIR}/") {
		t.Errorf("link file should use ${SRCDIR}-relative path:\n%s", lc)
	}

	// Second run must be idempotent.
	res2, err := InstallWithManifest(m, opts)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !res2.Skipped {
		t.Error("second install should be skipped (idempotent)")
	}
}

func TestChecksumMismatch(t *testing.T) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	filename := "v8-" + goos + "-" + goarch + ".tar.zst"
	srcDir := t.TempDir()
	makeArtifact(t, srcDir, filename)

	m := &Manifest{
		ModuleVersion: "v9.9.9",
		Targets: []Target{
			{GOOS: goos, GOARCH: goarch, Filename: filename, SHA256: strings.Repeat("0", 64)},
		},
	}
	_, err := InstallWithManifest(m, Options{
		GOOS: goos, GOARCH: goarch,
		Source:      &LocalSource{Dir: srcDir},
		InstallRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if _, ok := err.(*ChecksumError); !ok {
		t.Fatalf("expected *ChecksumError, got %T: %v", err, err)
	}
}

func TestUnsupportedTarget(t *testing.T) {
	m := &Manifest{ModuleVersion: "v9.9.9", Targets: []Target{{GOOS: "linux", GOARCH: "amd64"}}}
	_, err := m.TargetFor("plan9", "mips")
	if err == nil {
		t.Fatal("expected unsupported target error")
	}
	if _, ok := err.(*UnsupportedTargetError); !ok {
		t.Fatalf("expected *UnsupportedTargetError, got %T", err)
	}
}
