package nodejs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// An async loader hook is waited on however long its timers and I/O take, as Node
// waits on its hooks thread, rather than for a fixed number of loop turns. A hook
// promise nothing can settle any more fails the import instead of hanging.
func TestLoaderHook_WaitsForSlowHooks(t *testing.T) {
	start := time.Now()
	got := runLogModule(t, map[string]string{
		"dep.mjs":  `export default 'dep';`,
		"slow.mjs": ``,
		"hooks.mjs": `import { readFile } from 'node:fs/promises';
export async function resolve(s, c, n) {
  if (s.includes('hang')) return new Promise(() => {});
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.endsWith('slow.mjs')) {
    await new Promise((r) => setTimeout(r, 1000));
    const source = await readFile(u.replace(/^file:\/\//, '').replace(/slow\.mjs$/, 'dep.mjs'), 'utf8');
    return { format: 'module', source, shortCircuit: true };
  }
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
log.push((await import('./slow.mjs')).default);
try { await import('./hang'); log.push('loaded'); } catch (e) { log.push('rejected'); }`,
	})
	if want := `["dep","rejected"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("finished in %v, before the hook's timer", elapsed)
	}
}

// Killing the importing runtime ends an import that is waiting on a slow hook.
func TestLoaderHook_KillStopsTheWait(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"slow.mjs":  ``,
		"hooks.mjs": `export async function load(u, c, n) { await new Promise((r) => setTimeout(r, 60000)); return n(u, c); }`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
await import('./slow.mjs');`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	time.AfterFunc(300*time.Millisecond, func() { inst.Runtime.Kill("test") })
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := inst.ESM.RunModuleFile(filepath.Join(dir, "entry.mjs"))
		done <- err
	}()
	select {
	case <-done:
		// Returned with the kill, not after the hook's minute-long timer.
	case <-time.After(20 * time.Second):
		t.Fatalf("still waiting %v after the kill", time.Since(start))
	}
}
