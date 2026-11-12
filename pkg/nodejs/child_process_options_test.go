package nodejs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// spawn's timeout, killSignal, signal, argv0, env PATH and detached options behave as in
// Node, and an unref'd child does not keep the script running. The expected values are the
// script's output under Node v20.18.1:
//   node testdata/child_process_options.js <dir holding orbital-only-here>
func TestChildProcessOptions_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/child_process_options.js", "testdata/child_process_options.node.json"
	toolDir := t.TempDir()
	tool := filepath.Join(toolDir, "orbital-only-here")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho custom-path-tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	got := runJS(t, &runtime.Config{Timeout: 60 * time.Second}, fmt.Sprintf("globalThis.TOOL_DIR = %q;\n%s", toolDir, script))
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	wantJSON(t, got, want)
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Errorf("took %s: the unref'd child kept the script running", elapsed)
	}
}
