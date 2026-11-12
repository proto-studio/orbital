package nodejs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Value.String() stops at the first NUL and goes through UTF-8, so bytes must not cross from
// JavaScript to Go as strings. These tests run a script under Orbital and compare what it
// leaves on disk and on the standard streams with what the same script did under Node. The
// Go test does the reading, so nothing on the receiving side is Orbital's own code.

const nodeMajor = "v20."

// captureFromNode reports whether this run should rewrite the expected values.
func captureFromNode(t *testing.T) bool {
	t.Helper()
	if os.Getenv("ORBITAL_CAPTURE_NODE") == "" {
		return false
	}
	version, err := exec.Command("node", "--version").Output()
	if err != nil {
		t.Fatalf("node is needed to capture expected values: %v", err)
	}
	if !strings.HasPrefix(string(version), nodeMajor) {
		t.Fatalf("expected values come from Node 20, found %s", version)
	}
	return true
}

func writeExpected(t *testing.T, path string, value map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

func readExpected(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	return want
}

// encodeBytes renders bytes for comparison, hashing those too long to read in a diff.
func encodeBytes(data []byte) string {
	if len(data) > 4096 {
		return fmt.Sprintf("%d bytes, sha256 %x", len(data), sha256.Sum256(data))
	}
	return hex.EncodeToString(data)
}

// binaryFSDir creates the directory the script works in, holding the file it reads from.
func binaryFSDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seed := make([]byte, 256)
	for i := range seed {
		seed[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.bin"), seed, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// filesOnDisk adds every file in dir to out under "file:<name>".
func filesOnDisk(t *testing.T, dir string, out map[string]any) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out["file:"+entry.Name()] = encodeBytes(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBinaryFS_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/binary_fs.js", "testdata/binary_fs.node.json"
	dir := binaryFSDir(t)

	if captureFromNode(t) {
		output, err := exec.Command("node", scriptPath, dir).Output()
		if err != nil {
			t.Fatal(err)
		}
		var results map[string]any
		if err := json.Unmarshal(output, &results); err != nil {
			t.Fatal(err)
		}
		filesOnDisk(t, dir, results)
		writeExpected(t, expectedPath, results)
		return
	}

	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &runtime.Config{Timeout: 60 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.BINARY_DIR = %q;\n%s", dir, script))
	filesOnDisk(t, dir, got)
	wantJSON(t, got, readExpected(t, expectedPath))
}

// The same writes on the in-memory filesystem an embedder may supply.
func TestBinaryFS_MemoryFilesystem(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	got := runJS(t, &runtime.Config{Filesystem: memory}, `
const fs = require('fs');
const bin = Buffer.from([0x41, 0, 0x42, 0xff, 0, 0x43]);
fs.writeFileSync('/file', bin);
fs.appendFileSync('/file', bin);
const fd = fs.openSync('/fd', 'w');
fs.writeSync(fd, bin);
fs.writeSync(fd, 'X\0');
fs.closeSync(fd);
fs.writeFile('/async', bin, (err) => {
  done({ err, read: fs.readFileSync('/file').toString('hex') });
});`)
	wantJSON(t, got, map[string]any{"err": nil, "read": "410042ff0043410042ff0043"})
	for path, want := range map[string][]byte{
		"/file":  {0x41, 0, 0x42, 0xff, 0, 0x43, 0x41, 0, 0x42, 0xff, 0, 0x43},
		"/fd":    {0x41, 0, 0x42, 0xff, 0, 0x43, 'X', 0},
		"/async": {0x41, 0, 0x42, 0xff, 0, 0x43},
	} {
		data, err := memory.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, want) {
			t.Errorf("%s holds %x, want %x", path, data, want)
		}
	}
}

// stdioChildEnv names the script TestBinaryStdioChild runs.
const stdioChildEnv = "ORBITAL_STDIO_SCRIPT"

// TestBinaryStdioChild is not a test of its own. TestBinaryStdio_MatchesNode runs this binary
// again with it selected, so the script's output reaches real pipes, not a Go test's writer.
func TestBinaryStdioChild(t *testing.T) {
	path := os.Getenv(stdioChildEnv)
	if path == "" {
		t.Skip("runs only as a child of TestBinaryStdio_MatchesNode")
	}
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Runtime.Run(string(script), "script.js"); err != nil {
		t.Fatal(err)
	}
	// Exiting here keeps the test framework's own "PASS" line off stdout.
	os.Exit(0)
}

func runForStdio(t *testing.T, cmd *exec.Cmd) map[string]any {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, encodeBytes(stdout.Bytes()), stderr.String())
	}
	return map[string]any{"stdout": encodeBytes(stdout.Bytes()), "stderr": encodeBytes(stderr.Bytes())}
}

func TestBinaryStdio_MatchesNode(t *testing.T) {
	capture := captureFromNode(t)
	expected := map[string]any{}
	want := map[string]any{}
	if !capture {
		want = readExpected(t, "testdata/binary_stdio.node.json")
	}
	for _, name := range []string{"binary_stdio", "binary_stdio_large", "binary_stdio_exit", "small_diffs"} {
		path := filepath.Join("testdata", name+".js")
		if capture {
			expected[name] = runForStdio(t, exec.Command("node", path))
			continue
		}
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestBinaryStdioChild$")
			cmd.Env = append(os.Environ(), stdioChildEnv+"="+path)
			got := runForStdio(t, cmd)
			wantJSON(t, got, want[name].(map[string]any))
		})
	}
	if capture {
		writeExpected(t, "testdata/binary_stdio.node.json", expected)
	}
}
