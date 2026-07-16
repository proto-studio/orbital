package v8dist

import (
	"fmt"
	"strings"
)

// regenCmd returns the exact command a user should run to (re)install the V8
// runtime for a given target. It is appended to actionable errors (§16).
func regenCmd(goos, goarch string) string {
	return fmt.Sprintf("GOOS=%s GOARCH=%s go generate ./...", goos, goarch)
}

// UnsupportedTargetError is returned when the manifest has no artifact for the
// requested GOOS/GOARCH.
type UnsupportedTargetError struct {
	GOOS      string
	GOARCH    string
	Supported []string
}

func (e *UnsupportedTargetError) Error() string {
	return fmt.Sprintf(
		"v8dist: no prebuilt V8 library for %s/%s (supported targets: %s)",
		e.GOOS, e.GOARCH, strings.Join(e.Supported, ", "),
	)
}

// NoReleaseError is returned when the pinned Release/tag or asset cannot be
// found (e.g. the module version has not been published yet).
type NoReleaseError struct {
	Tag    string
	Asset  string
	GOOS   string
	GOARCH string
	Status int
}

func (e *NoReleaseError) Error() string {
	loc := e.Tag
	if e.Asset != "" {
		loc = e.Tag + "/" + e.Asset
	}
	return fmt.Sprintf(
		"v8dist: V8 release asset %q not found (HTTP %d); the pinned version may "+
			"not be published yet.\n  Re-run after a release exists: %s",
		loc, e.Status, regenCmd(e.GOOS, e.GOARCH),
	)
}

// DownloadError wraps a transport-level failure fetching an artifact.
type DownloadError struct {
	URL    string
	GOOS   string
	GOARCH string
	Err    error
}

func (e *DownloadError) Error() string {
	return fmt.Sprintf(
		"v8dist: downloading V8 artifact from %s failed: %v\n  Retry: %s",
		e.URL, e.Err, regenCmd(e.GOOS, e.GOARCH),
	)
}

func (e *DownloadError) Unwrap() error { return e.Err }

// ChecksumError is returned when a downloaded artifact does not match the
// sha256 pinned in the manifest.
type ChecksumError struct {
	Filename string
	Want     string
	Got      string
	GOOS     string
	GOARCH   string
}

func (e *ChecksumError) Error() string {
	return fmt.Sprintf(
		"v8dist: checksum mismatch for %s\n  want sha256 %s\n  got  sha256 %s\n"+
			"  the download may be corrupt or tampered with; clear .v8/ and re-run: %s",
		e.Filename, e.Want, e.Got, regenCmd(e.GOOS, e.GOARCH),
	)
}

// ExtractError wraps a failure unpacking the .tar.zst artifact.
type ExtractError struct {
	Filename string
	GOOS     string
	GOARCH   string
	Err      error
}

func (e *ExtractError) Error() string {
	return fmt.Sprintf(
		"v8dist: extracting %s failed: %v\n  clear .v8/ and re-run: %s",
		e.Filename, e.Err, regenCmd(e.GOOS, e.GOARCH),
	)
}

func (e *ExtractError) Unwrap() error { return e.Err }

// MissingLibraryError is returned when the expected static library is absent
// after a successful extraction.
type MissingLibraryError struct {
	Missing string
	Dir     string
	GOOS    string
	GOARCH  string
}

func (e *MissingLibraryError) Error() string {
	return fmt.Sprintf(
		"v8dist: expected library %q not found under %s after extraction\n"+
			"  the artifact may be malformed; clear .v8/ and re-run: %s",
		e.Missing, e.Dir, regenCmd(e.GOOS, e.GOARCH),
	)
}
