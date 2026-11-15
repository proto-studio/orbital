package nodejs

import (
	"testing"
	"time"
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
