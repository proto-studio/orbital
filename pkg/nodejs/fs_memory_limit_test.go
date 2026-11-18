package nodejs

import (
	"errors"
	"fmt"
	"testing"
	"time"

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

// With a MemoryBudget, the memory filesystem and the heap draw from it together, whichever
// grows first. The heap growing into what files hold runs out of memory, as Node does at its
// heap limit, and files growing into what the heap holds fail with ENOSPC.
func TestFS_MemoryFilesystemSharesTheBudget(t *testing.T) {
	// Each runtime gets a 64 MiB budget, and a file sized to what its heap left free when it
	// started, less 4 MiB: room for one or the other of the file and a 24 MB array, not both.
	run := func(order func(fill, hold string) string) (map[string]any, error) {
		inst, err := New(&runtime.Config{
			Filesystem:   runtime.NewMemoryFilesystem(),
			MemoryBudget: runtime.NewMemoryBudget(64 << 20),
			MaxOpenFiles: -1,
			Timeout:      60 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer inst.Runtime.Dispose()
		size := inst.Runtime.MemoryBudget().Available() - 4<<20
		fill := fmt.Sprintf("require('fs').writeFileSync('/f', ''); require('fs').truncateSync('/f', %d);", size)
		const hold = "globalThis.kept = new Array(3e6).fill(1.5);" // 24 MB of doubles
		prelude := "globalThis.done = (v) => { globalThis.__result = JSON.stringify(v); };\n"
		if _, err := inst.Runtime.Run(prelude+order(fill, hold), "test.js"); err != nil {
			return nil, err
		}
		return resultOf(t, inst), nil
	}

	// Files first: the heap can't grow into them.
	_, err := run(func(fill, hold string) string { return fill + hold + "done({});" })
	var oom *runtime.OutOfMemoryError
	if !errors.As(err, &oom) {
		t.Fatalf("filling the heap beside a file using the rest of the budget: Run = %v, want an *OutOfMemoryError", err)
	}

	// The heap first: the files can't grow into it.
	got, err := run(func(fill, hold string) string {
		return hold + "let failure = null;\ntry { " + fill + " } catch (e) { failure = { code: e.code, syscall: e.syscall, message: e.message }; }\ndone({ failure, kept: kept.length });"
	})
	if err != nil {
		t.Fatal(err)
	}
	wantJSON(t, got, map[string]any{
		"failure": map[string]any{"code": "ENOSPC", "syscall": "ftruncate", "message": "ENOSPC: no space left on device, ftruncate"},
		"kept":    float64(3e6),
	})
}
