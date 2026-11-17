package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// zlib honors maxOutputLength as Node does: the same validation, and ERR_BUFFER_TOO_LARGE
// from every sync and callback form once the output passes it.
func TestZlib_OutputLimitMatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/zlib_limits.js", "testdata/zlib_limits.node.json"

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
	got := runJS(t, &runtime.Config{}, string(script))
	wantJSON(t, got, readExpected(t, expectedPath))
}

// Orbital's buffer.constants.MAX_LENGTH is smaller than Node's, so the range errors name it.
func TestZlib_OutputLimitRange(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const zlib = require('zlib');
const hello = zlib.gzipSync('hello');
const outcome = (v) => { try { return zlib.gunzipSync(hello, { maxOutputLength: v }).length; } catch (e) { return e.code + ' ' + e.message; } };
done({ zero: outcome(0), max: outcome(2147483647), over: outcome(2147483648), huge: outcome(2 ** 53) });
`)
	const prefix = `ERR_OUT_OF_RANGE The value of "options.maxOutputLength" is out of range. It must be >= 1 and <= 2147483647. Received `
	wantJSON(t, got, map[string]any{
		"zero": prefix + "0",
		"max":  float64(5),
		"over": prefix + "2147483648",
		"huge": prefix + "9_007_199_254_740_992",
	})
}

// A stream that nothing reads holds about one chunk of output: the codec waits for the
// reader instead of expanding the whole input. It also doesn't keep the event loop alive,
// and reading it later still yields every byte, in order.
func TestZlib_StreamWaitsForItsReader(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const zlib = require('zlib');
// Concatenated gzip members decompress as one stream.
const member = zlib.gzipSync(Buffer.alloc(1 << 20));
const input = Buffer.concat(Array.from({ length: 32 }, () => member));
const size = 32 << 20;
const g = zlib.createGunzip();
g.end(input);
setTimeout(() => {
  const buffered = g.readableLength;
  let total = 0;
  g.on('data', (d) => { total += d.length; });
  g.on('end', () => done({ bufferedWhilePaused: buffered <= 4 * g.readableHighWaterMark, total, size }));
}, 200);
`)
	wantJSON(t, got, map[string]any{"bufferedWhilePaused": true, "total": float64(32 << 20), "size": float64(32 << 20)})
}

// Destroying a stream whose codec is waiting for its reader stops the codec, and the run
// still finishes.
func TestZlib_DestroyWhileWaiting(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const zlib = require('zlib');
const g = zlib.createGunzip();
const member = zlib.gzipSync(Buffer.alloc(1 << 20));
g.end(Buffer.concat(Array.from({ length: 16 }, () => member)));
setTimeout(() => {
  g.on('close', () => done({ destroyed: g.destroyed }));
  g.destroy();
}, 100);
`)
	wantJSON(t, got, map[string]any{"destroyed": true})
}

// A stream nobody reads doesn't keep the process alive, as in Node: the run returns.
func TestZlib_PausedStreamLetsTheRunEnd(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const zlib = require('zlib');
const g = zlib.createGunzip();
const member = zlib.gzipSync(Buffer.alloc(1 << 20));
g.end(Buffer.concat(Array.from({ length: 16 }, () => member)));
done({ started: true });
`)
	wantJSON(t, got, map[string]any{"started": true})
}
