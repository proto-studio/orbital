package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A URL keeps its query as parsed, and searchParams parses and serializes it as Node does.
func TestURL_QueryMatchesNode(t *testing.T) {
	const scriptPath = "testdata/url_query.js"
	const expectedPath = "testdata/url_query.node.json"

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
