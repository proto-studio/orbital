package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// With a real signaler, process.kill validates, signals and fails as Node does.
func TestProcessKill_MatchesNode(t *testing.T) {
	const scriptPath = "testdata/process_kill.js"
	const expectedPath = "testdata/process_kill.node.json"

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

	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &runtime.Config{ProcessSignaler: runtime.NewRealProcessSignaler()}
	wantJSON(t, runJS(t, cfg, string(script)), readExpected(t, expectedPath))
}

// recordingSignaler records what it is asked to send and refuses it.
type recordingSignaler struct {
	pid int
	sig syscall.Signal
}

func (r *recordingSignaler) Kill(pid int, sig syscall.Signal) error {
	r.pid, r.sig = pid, sig
	return syscall.ESRCH
}

// process.kill goes through the configured signaler, and refuses by default.
func TestProcessKill_UsesSignaler(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
let result;
try { result = process.kill(process.pid, 0); } catch (e) { result = e.code + ' ' + e.errno + ' ' + e.message; }
done({ result });`)
	wantJSON(t, got, map[string]any{"result": "EPERM -1 kill EPERM"})

	rec := &recordingSignaler{}
	got = runJS(t, &runtime.Config{ProcessSignaler: rec}, `
let result;
try { result = process.kill(12345, 'SIGUSR1'); } catch (e) { result = e.code; }
done({ result });`)
	wantJSON(t, got, map[string]any{"result": "ESRCH"})
	if rec.pid != 12345 || rec.sig != syscall.SIGUSR1 {
		t.Fatalf("signaler got pid %d signal %d, want 12345 and %d", rec.pid, rec.sig, syscall.SIGUSR1)
	}
}
