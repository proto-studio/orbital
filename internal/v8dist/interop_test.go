package v8dist

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestInstallFromSystemTar verifies that an archive produced by the system
// `tar --zstd` (what the Makefile/CI produce) is installable by the pure-Go
// extractor consumers use. This guards the producer/consumer toolchain interop.
func TestInstallFromSystemTar(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not available")
	}

	goos, goarch := runtime.GOOS, runtime.GOARCH
	filename := "v8-" + goos + "-" + goarch + ".tar.zst"

	srcDir := t.TempDir()
	libDir := filepath.Join(srcDir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{LibMonolith, LibGlue, LibCxx} {
		if err := os.WriteFile(filepath.Join(libDir, name), []byte("dummy "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	assetDir := t.TempDir()
	asset := filepath.Join(assetDir, filename)
	cmd := exec.Command("tar", "-C", srcDir, "--zstd", "-cf", asset, "lib")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("tar --zstd unavailable: %v\n%s", err, out)
	}

	data, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)

	m := &Manifest{
		ModuleVersion: "v9.9.9",
		Targets: []Target{
			{GOOS: goos, GOARCH: goarch, Filename: filename, SHA256: hex.EncodeToString(sum[:])},
		},
	}
	res, err := InstallWithManifest(m, Options{
		GOOS: goos, GOARCH: goarch,
		Source:      &LocalSource{Dir: assetDir},
		InstallRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("install from system tar archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.LibDir, LibMonolith)); err != nil {
		t.Errorf("monolith not extracted: %v", err)
	}
}
