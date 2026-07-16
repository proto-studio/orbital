package v8dist

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Source fetches the raw .tar.zst artifact for a target into dstPath.
type Source interface {
	// Fetch writes the artifact for t to dstPath. Implementations should return
	// a *NoReleaseError / *DownloadError with the target populated on failure.
	Fetch(t Target, dstPath string) error
	// Describe returns a short human-readable identifier for diagnostics.
	Describe(t Target) string
}

// ReleaseSource downloads artifacts from a published GitHub Release. This is the
// default source used by consumers.
type ReleaseSource struct {
	Owner  string
	Repo   string
	Tag    string
	Client *http.Client
}

func (s *ReleaseSource) url(t Target) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		s.Owner, s.Repo, s.Tag, t.Filename)
}

func (s *ReleaseSource) Describe(t Target) string { return s.url(t) }

func (s *ReleaseSource) Fetch(t Target, dstPath string) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	url := s.url(t)
	resp, err := client.Get(url)
	if err != nil {
		return &DownloadError{URL: url, GOOS: t.GOOS, GOARCH: t.GOARCH, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return &NoReleaseError{Tag: s.Tag, Asset: t.Filename, GOOS: t.GOOS, GOARCH: t.GOARCH, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return &DownloadError{URL: url, GOOS: t.GOOS, GOARCH: t.GOARCH,
			Err: fmt.Errorf("unexpected HTTP status %s", resp.Status)}
	}
	return streamToFile(resp.Body, dstPath, url, t)
}

// ActionsSource downloads a workflow-run artifact via the `gh` CLI. Used by CI
// (PR validation and release) to fetch the exact artifacts a build produced
// before any Release exists.
type ActionsSource struct {
	RunID string
	// GH is the gh executable name (default "gh").
	GH string
}

// artifactName is the upload-artifact name CI uses: the filename without the
// .tar.zst suffix (e.g. "v8-linux-amd64").
func artifactName(t Target) string {
	return strings.TrimSuffix(t.Filename, ".tar.zst")
}

func (s *ActionsSource) Describe(t Target) string {
	return fmt.Sprintf("actions run %s artifact %s", s.RunID, artifactName(t))
}

func (s *ActionsSource) Fetch(t Target, dstPath string) error {
	gh := s.GH
	if gh == "" {
		gh = "gh"
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(dstPath), "gh-dl-")
	if err != nil {
		return &DownloadError{GOOS: t.GOOS, GOARCH: t.GOARCH, Err: err}
	}
	defer os.RemoveAll(tmpDir)

	name := artifactName(t)
	cmd := exec.Command(gh, "run", "download", s.RunID, "-n", name, "-D", tmpDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return &DownloadError{URL: s.Describe(t), GOOS: t.GOOS, GOARCH: t.GOARCH,
			Err: fmt.Errorf("gh run download: %w", err)}
	}
	// The artifact contains the tarball (t.Filename).
	src := filepath.Join(tmpDir, t.Filename)
	if _, err := os.Stat(src); err != nil {
		return &DownloadError{URL: s.Describe(t), GOOS: t.GOOS, GOARCH: t.GOARCH,
			Err: fmt.Errorf("artifact %s did not contain %s", name, t.Filename)}
	}
	return moveOrCopy(src, dstPath)
}

// LocalSource copies artifacts from a local directory. Used by dev/tests and by
// CI after it has already downloaded artifacts to disk.
type LocalSource struct {
	Dir string
}

func (s *LocalSource) Describe(t Target) string {
	return filepath.Join(s.Dir, t.Filename)
}

func (s *LocalSource) Fetch(t Target, dstPath string) error {
	src := filepath.Join(s.Dir, t.Filename)
	if _, err := os.Stat(src); err != nil {
		return &NoReleaseError{Tag: "local:" + s.Dir, Asset: t.Filename,
			GOOS: t.GOOS, GOARCH: t.GOARCH, Status: 0}
	}
	return moveOrCopy(src, dstPath)
}

func streamToFile(r io.Reader, dstPath, src string, t Target) error {
	f, err := os.Create(dstPath)
	if err != nil {
		return &DownloadError{URL: src, GOOS: t.GOOS, GOARCH: t.GOARCH, Err: err}
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return &DownloadError{URL: src, GOOS: t.GOOS, GOARCH: t.GOARCH, Err: err}
	}
	return f.Close()
}

func moveOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
