package nodejs

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// limitedConfig runs scripts on a memory filesystem in an isolate with a 64 MiB heap limit.
// The accounting itself is unit tested in pkg/runtime; this checks what scripts see.
func limitedConfig() *runtime.Config {
	return &runtime.Config{
		Filesystem:     runtime.NewMemoryFilesystem(),
		IsolateOptions: v8.IsolateOptions{MaxHeapSizeInBytes: 64 << 20},
	}
}

// A script that fills a memory filesystem gets Node's ENOSPC, and a file it removes makes room
// again. How the fs module reports ENOSPC from each form of a call is tested with a mock
// filesystem (TestFSWriteFailures, TestFSTruncateFailures), and the accounting in pkg/runtime.
func TestFS_MemoryFilesystemFillsUp(t *testing.T) {
	got := runJS(t, limitedConfig(), `
const fs = require('fs');
const describe = (e) => e && { code: e.code, errno: e.errno, syscall: e.syscall,
  path: e.path === undefined ? null : e.path, message: e.message };
const attempt = (fn) => { try { fn(); return null; } catch (e) { return describe(e); } };
fs.writeFileSync('/existing', '');
// Fill the filesystem: grow /pad until nothing more fits.
fs.writeFileSync('/pad', '');
let lo = 0, hi = 100 << 20;
while (hi - lo > 1) {
  const mid = Math.floor((lo + hi) / 2);
  try { fs.truncateSync('/pad', mid); lo = mid; } catch (e) { if (e.code !== 'ENOSPC') throw e; hi = mid; }
}
const out = {
  under64: lo > 0 && lo < 64 << 20,
  write: attempt(() => fs.writeFileSync('/existing', Buffer.alloc(1 << 20))),
};
fs.unlinkSync('/pad');
out.again = attempt(() => fs.writeFileSync('/again', Buffer.alloc(1 << 20)));
done(out);`)
	wantJSON(t, got, map[string]any{
		"under64": true,
		"write": map[string]any{"code": "ENOSPC", "errno": float64(-28), "syscall": "write", "path": nil,
			"message": "ENOSPC: no space left on device, write"},
		"again": nil,
	})
}
