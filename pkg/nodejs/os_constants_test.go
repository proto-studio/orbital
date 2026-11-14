package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// os.constants holds the host's own errno and signal numbers, with the same names Node has
// on that platform. Each platform has its own expected output, captured from Node there.
func TestOSConstants_MatchesNode(t *testing.T) {
	scriptPath := "testdata/os_constants.js"
	expectedPath := "testdata/os_constants." + goruntime.GOOS + ".node.json"

	if captureFromNode(t) {
		output, err := exec.Command("node", scriptPath).Output()
		if err != nil {
			t.Fatal(err)
		}
		var results map[string]any
		if err := json.Unmarshal(output, &results); err != nil {
			t.Fatal(err)
		}
		writeExpected(t, expectedPath, results)
		return
	}

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Skipf("no Node output captured for %s", goruntime.GOOS)
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON(t, runJS(t, &runtime.Config{}, string(script)), readExpected(t, expectedPath))
}

// kill() sends the signal its name means on this platform: SIGUSR1 is 30 on macOS but 10 on
// Linux, where 30 is SIGPWR.
func TestChildProcess_KillSendsHostSignal(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const cp = require('child_process');
const child = cp.spawn('/bin/sh', ['-c', 'trap "echo got-usr1; exit 0" USR1; echo ready; while :; do sleep 0.05; done']);
let out = '';
child.stdout.on('data', (d) => {
  out += d;
  if (out.includes('ready') && !child.killed) child.kill('SIGUSR1');
});
child.on('close', (code) => done({ code, out: out.trim() }));
`)
	if got["code"] != float64(0) || !strings.Contains(got["out"].(string), "got-usr1") {
		t.Fatalf("child did not get SIGUSR1: %v", got)
	}
}
