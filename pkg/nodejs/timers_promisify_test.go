package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// util.promisify(setTimeout) and util.promisify(setImmediate) give timers/promises' versions,
// which validate, abort and iterate as Node's do.
func TestTimers_PromisifyMatchesNode(t *testing.T) {
	const scriptPath = "testdata/timers_promisify.js"
	const expectedPath = "testdata/timers_promisify.node.json"

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
	wantJSON(t, runJS(t, &runtime.Config{}, string(script)), readExpected(t, expectedPath))
}

// A timer from timers/promises with ref: false doesn't keep the event loop running.
func TestTimers_PromisesUnrefLetsTheLoopExit(t *testing.T) {
	exitsPromptly(t, `
const tp = require('timers/promises');
tp.setTimeout(8000, 1, { ref: false });
tp.setInterval(8000, 1, { ref: false }).next();
done({ ok: true });`)
}
