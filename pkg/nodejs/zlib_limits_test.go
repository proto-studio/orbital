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

// A stream whose codec finishes before the writable side ends, as an empty deflate stream
// does, still finishes: 'finish', end()'s callback and 'end' all happen.
func TestZlib_ShortStreamsFinish(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const zlib = require('zlib');
const cases = [
  ['createInflate', zlib.deflateSync(Buffer.alloc(0))],
  ['createInflateRaw', zlib.deflateRawSync(Buffer.alloc(0))],
  ['createGunzip', zlib.gzipSync(Buffer.alloc(0))],
  ['createInflate', zlib.deflateSync('hi')],
];
const results = [];
let pending = cases.length * 20;
for (let i = 0; i < 20; i++) {
  for (const [name, input] of cases) {
    const s = zlib[name]();
    const seen = { finish: false, endCallback: false, end: false };
    s.on('finish', () => { seen.finish = true; });
    s.on('end', () => { seen.end = true; });
    s.resume();
    s.end(input, () => { seen.endCallback = true; });
    setTimeout(() => {
      results.push(seen.finish && seen.endCallback && seen.end ? 'ok' : name + ' ' + JSON.stringify(seen));
      if (--pending === 0) done({ failures: results.filter((r) => r !== 'ok') });
    }, 200);
  }
}
`)
	wantJSON(t, got, map[string]any{"failures": []any{}})
}
