package nodejs

import (
	"os"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A child gets the environment the script sees, which the embedder may have filtered,
// rather than the host process's. An empty env option gives an empty environment, as in Node.
func TestChildProcessEnvironment(t *testing.T) {
	os.Setenv("ORBITAL_FILTERED_VAR", "filtered-value")
	defer os.Unsetenv("ORBITAL_FILTERED_VAR")
	env := runtime.NewFilteredEnvironment(runtime.NewRealEnvironment(), nil, []string{"ORBITAL_FILTERED_VAR"})
	cfg := &runtime.Config{Environment: env, ProcessSpawner: runtime.NewAllowListProcessSpawner(lookPath(t, "printenv"), "/usr/bin/env"), Timeout: 20 * time.Second}
	got := runJS(t, cfg, `
const cp = require('child_process');
const run = (cmd, args, opts) => new Promise((r) => {
  const c = cp.spawn(cmd, args, opts);
  let o = '';
  c.stdout.on('data', (d) => { o += d; });
  c.on('close', () => r(o.trim()));
});
process.env.ORBITAL_SCRIPT_VAR = 'set-by-script';
(async () => done({
  processEnv: process.env.ORBITAL_FILTERED_VAR === undefined ? 'hidden' : 'visible',
  noEnv: await run('printenv', ['ORBITAL_FILTERED_VAR'], {}),
  scriptVar: await run('printenv', ['ORBITAL_SCRIPT_VAR'], {}),
  emptyEnv: await run('/usr/bin/env', [], { env: {} }),
  givenEnv: await run('/usr/bin/env', [], { env: { ONLY: '1' } }),
  sync: cp.execFileSync('printenv', ['ORBITAL_FILTERED_VAR']).toString().trim(),
}))().catch((e) => done({ failed: String(e && e.message) }));`)
	wantJSON(t, got, map[string]any{
		"processEnv": "hidden",
		"noEnv":      "",
		"scriptVar":  "set-by-script",
		"emptyEnv":   "",
		"givenEnv":   "ONLY=1",
		"sync":       "",
	})
}
