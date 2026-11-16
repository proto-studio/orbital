package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// import() of a module whose evaluation hasn't finished waits for it, so a module that
// awaits an import of itself, or of a module waiting on it, never settles and exits 13; an
// import of a module whose evaluation threw rejects every time. The expected results are
// Node 20.18.1's, which the test also checks against node when it is installed.
func TestDynamicImport_WaitsForEvaluation(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"self.mjs":     "export const obj = {};\nconst self = await import('./self.mjs');\nconsole.log(self.obj === obj);\n",
		"cyc.mjs":      "export const x = 1;\nawait import('./cycdep.mjs');\nconsole.log('entry done');\n",
		"cycdep.mjs":   "const e = await import('./cyc.mjs');\nconsole.log('dep', e.x);\n",
		"bad.mjs":      "throw new Error('bad');\n",
		"errtwice.mjs": "for (const n of [1, 2]) {\n  try { await import('./bad.mjs'); console.log(n, 'resolved'); } catch (e) { console.log(n, e.message); }\n}\n",
		"done.mjs":     "export const v = 1;\n",
		"twice.mjs":    "const a = await import('./done.mjs');\nconst b = await import('./done.mjs');\nconsole.log(a === b, a.v);\n",
		"later.mjs":    "export const obj = {};\nsetTimeout(async () => { const self = await import('./later.mjs'); console.log('later', self.obj === obj); }, 10);\nawait new Promise((r) => setTimeout(r, 50));\nconsole.log('body done');\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		entry string
		out   string
		code  int
	}{
		{"self.mjs", "", 13},
		{"cyc.mjs", "", 13},
		{"errtwice.mjs", "1 bad\n2 bad\n", 0},
		{"twice.mjs", "true 1\n", 0},
		{"later.mjs", "body done\nlater true\n", 0},
	}
	_, nodeErr := exec.LookPath("node")
	for _, tc := range cases {
		entry := filepath.Join(dir, tc.entry)
		if nodeErr == nil {
			cmd := exec.Command("node", entry)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); string(out) != tc.out || code != tc.code {
				t.Errorf("node %s: exit %d, output %q; the expected results are out of date", tc.entry, code, out)
			}
		}
		out, code := runOrbital(t, "", entry)
		if out != tc.out || code != tc.code {
			t.Errorf("%s: exit %d, output %q; want %d, %q", tc.entry, code, out, tc.code, tc.out)
		}
	}
}
