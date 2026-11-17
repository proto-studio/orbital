package nodejs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/internal/v8dist"
	"proto.zip/studio/orbital/pkg/runtime"
)

// runFileUncaught writes files into a new directory, runs entry from it as the orbital
// command does, a .mjs file as an ES module, and returns the error that ended the runtime.
// DIR in a file's text and in the result stands for the directory.
func runFileUncaught(t *testing.T, files map[string]string, entry string) (*runtime.UncaughtError, string) {
	t.Helper()
	dir := t.TempDir()
	texts := map[string]string{}
	for name, text := range files {
		texts[name] = strings.ReplaceAll(text, "DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(texts[name]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inst, err := New(allowWorkers(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	path := filepath.Join(dir, entry)
	var runErr error
	if strings.HasSuffix(entry, ".mjs") {
		if _, runErr = inst.ESM.RunModuleFile(path); runErr == nil {
			inst.Runtime.EventLoop().Run()
			runErr = inst.Runtime.Ended()
		}
	} else {
		_, runErr = inst.Runtime.Run(texts[entry], path)
	}
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("run returned %v, want an *UncaughtError", runErr)
	}
	return uncaught, dir
}

// Above a fatal exception Node prints where it was thrown: "file:line", the line, and
// carets under the place. Each expected header is what Node 20.18.1 prints for the same
// files, except that an ES module is named by its path here rather than its file: URL.
func TestUncaughtHeaderMatchesNode(t *testing.T) {
	const bom = "\uFEFF"
	for name, tc := range map[string]struct {
		files  map[string]string
		entry  string // defaults to main.js
		want   string // DIR stands for the directory
		inline bool
	}{
		"main script": {
			files: map[string]string{"main.js": "const x = 1;\n  throw new Error(\"main\");\n"},
			want:  "DIR/main.js:2\n  throw new Error(\"main\");\n  ^\n",
		},
		"error made before it is thrown": {
			files: map[string]string{"main.js": "const e = new Error(\"made\");\nfunction g() {\n    throw e;\n}\ng();\n"},
			want:  "DIR/main.js:3\n    throw e;\n    ^\n",
		},
		"main script that opens like a CommonJS wrapper": {
			files: map[string]string{"main.js": "(function(exports, require, module, __filename, __dirname) { throw new Error('w'); })();\n"},
			want:  "DIR/main.js:1\n(function(exports, require, module, __filename, __dirname) { throw new Error('w'); })();\n" + strings.Repeat(" ", 61) + "^\n",
		},
		"rethrown from a catch": {
			files: map[string]string{"main.js": "try { null.x; } catch (e) {\n  throw e;\n}\n"},
			want:  "DIR/main.js:2\n  throw e;\n  ^\n",
		},
		"syntax error": {
			files: map[string]string{"main.js": "let x = ;\n"},
			want:  "DIR/main.js:1\nlet x = ;\n        ^\n",
		},
		"syntax error on an empty line": {
			files: map[string]string{"main.js": "function f(){\n"},
			want:  "DIR/main.js:2\n\n\n",
		},
		"timer callback": {
			files: map[string]string{"main.js": "setTimeout(() => {\n  throw new Error(\"cb\");\n}, 1);\n"},
			want:  "DIR/main.js:2\n  throw new Error(\"cb\");\n  ^\n",
		},
		"tab before the throw": {
			files: map[string]string{"main.js": "setTimeout(() => {\n\tthrow new Error(\"tab\");\n}, 1);\n"},
			want:  "DIR/main.js:2\n\tthrow new Error(\"tab\");\n\t^\n",
		},
		"value that is not an error, from a callback": {
			files:  map[string]string{"main.js": "setTimeout(() => {\n  throw 1;\n}, 1);\n"},
			want:   "DIR/main.js:2\n  throw 1;\n  ^\n",
			inline: true,
		},
		"nextTick": {
			files:  map[string]string{"main.js": "process.nextTick(() => {\n  throw \"tick\";\n});\n"},
			want:   "DIR/main.js:2\n  throw \"tick\";\n  ^\n",
			inline: true,
		},
		"rejection": {
			files: map[string]string{"main.js": "const p = Promise.reject(\n  new Error(\"rej\"));\n"},
			want:  "DIR/main.js:2\n  new Error(\"rej\"));\n  ^\n",
		},
		"eval'd code, from a callback": {
			files: map[string]string{"main.js": "const f = eval(\"() => {\\n  throw new Error(\\\"x\\\");\\n}\");\nsetTimeout(f, 1);\n"},
			want:  "undefined:2\n  throw new Error(\"x\");\n  ^\n",
		},
		"eval'd value that is not an error": {
			files:  map[string]string{"main.js": "setTimeout(() => { eval(\"throw 2\"); }, 1);\n"},
			want:   "undefined:1\nthrow 2\n^\n",
			inline: true,
		},
		"BOM in the main script": {
			files: map[string]string{"main.js": bom + "setTimeout(() => { throw new Error(\"bom\"); }, 1);\n"},
			want:  "DIR/main.js:1\n" + bom + "setTimeout(() => { throw new Error(\"bom\"); }, 1);\n" + strings.Repeat(" ", 20) + "^\n",
		},
		"required module": {
			files: map[string]string{
				"main.js": "require(\"DIR/dep.js\")();\n",
				"dep.js":  "module.exports = () => { throw new Error(\"dep\"); };\n",
			},
			want: "DIR/dep.js:1\nmodule.exports = () => { throw new Error(\"dep\"); };\n" + strings.Repeat(" ", 25) + "^\n",
		},
		"required module with a BOM": {
			files: map[string]string{
				"main.js": "require(\"DIR/dep.js\")();\n",
				"dep.js":  bom + "module.exports = () => { throw new Error(\"bomdep\"); };\n",
			},
			want: "DIR/dep.js:1\n" + bom + "module.exports = () => { throw new Error(\"bomdep\"); };\n" + strings.Repeat(" ", 26) + "^\n",
		},
		"required module throwing while it loads": {
			files: map[string]string{
				"main.js": "require(\"DIR/dep.js\");\n",
				"dep.js":  "throw 5;\n",
			},
			want:   "DIR/dep.js:1\nthrow 5;\n^\n",
			inline: true,
		},
		"syntax error in a required module": {
			files: map[string]string{
				"main.js": "require(\"DIR/dep.js\");\n",
				"dep.js":  "let x = ;\n",
			},
			want: "DIR/dep.js:1\nlet x = ;\n        ^\n",
		},
		"syntax error at the end of a required module": {
			files: map[string]string{
				"main.js": "require(\"DIR/dep.js\");\n",
				"dep.js":  "const o = {\n  a: 1,",
			},
			want: "DIR/dep.js:2\n  a: 1,\n       \n",
		},
		"stray brace in a required module": {
			files: map[string]string{
				"main.js": "require(\"DIR/dep.js\");\n",
				"dep.js":  "}\n",
			},
			want: "DIR/dep.js:1\n}\n^\n",
		},
		"required module with a NUL character": {
			files: map[string]string{
				"main.js": "require(\"DIR/dep.js\")();\n",
				"dep.js":  "/*\x00*/ module.exports = () => { throw new Error(\"nul\"); };\n",
			},
			want: "DIR/dep.js:1\n/*\n  ^\n",
		},
		"ES module": {
			files: map[string]string{"main.mjs": "const y = 2;\nthrow new Error(\"esm\");\n"},
			entry: "main.mjs",
			want:  "DIR/main.mjs:2\nthrow new Error(\"esm\");\n      ^\n",
		},
		"ES module after top-level await": {
			files: map[string]string{"main.mjs": "await null;\nthrow new Error(\"tla\");\n"},
			entry: "main.mjs",
			want:  "DIR/main.mjs:2\nthrow new Error(\"tla\");\n      ^\n",
		},
		"ES module callback": {
			files: map[string]string{"main.mjs": "setTimeout(() => { throw new Error(\"esmcb\"); }, 1);\n"},
			entry: "main.mjs",
			want:  "DIR/main.mjs:1\nsetTimeout(() => { throw new Error(\"esmcb\"); }, 1);\n" + strings.Repeat(" ", 19) + "^\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			entry := tc.entry
			if entry == "" {
				entry = "main.js"
			}
			uncaught, dir := runFileUncaught(t, tc.files, entry)
			if want := strings.ReplaceAll(tc.want, "DIR", dir); uncaught.Source != want {
				t.Errorf("Source = %q, want %q", uncaught.Source, want)
			}
			if uncaught.SourceInline != tc.inline {
				t.Errorf("SourceInline = %v, want %v", uncaught.SourceInline, tc.inline)
			}
		})
	}
}

// Report lays the header out as Node does: above an error's report with a blank line
// between, or after a blank line and right above a value that isn't an error.
func TestUncaughtReportLayout(t *testing.T) {
	version := "\nOrbital " + v8dist.ModuleVersion + "\n"
	uncaught, dir := runFileUncaught(t, map[string]string{"main.js": "setTimeout(() => {\n  throw 1;\n}, 1);\n"}, "main.js")
	if got, want := uncaught.Report(), "\n"+dir+"/main.js:2\n  throw 1;\n  ^\n1\n"+version; got != want {
		t.Errorf("Report = %q, want %q", got, want)
	}
	uncaught, dir = runFileUncaught(t, map[string]string{"main.js": "throw new Error('x');\n"}, "main.js")
	if got, want := uncaught.Report(), dir+"/main.js:1\nthrow new Error('x');\n^\n\n"; !strings.HasPrefix(got, want) || !strings.HasSuffix(got, version) {
		t.Errorf("Report = %q, want it to start %q and end %q", got, want, version)
	}
}

// Nothing is shown for a place in the runtime's own code: a rejection with a value that
// isn't an error is reported through an error the runtime makes.
func TestUncaughtHeaderLeavesOutRuntimeCode(t *testing.T) {
	uncaught, _ := runFileUncaught(t, map[string]string{"main.js": "Promise.reject(5);\n"}, "main.js")
	if uncaught.Source != "" {
		t.Errorf("Source = %q, want none", uncaught.Source)
	}
}

// A handler for 'uncaughtException' sees the exception, and the next one thrown still gets
// its own place.
func TestUncaughtHeaderAfterHandledException(t *testing.T) {
	uncaught, dir := runFileUncaught(t, map[string]string{"main.js": "process.once('uncaughtException', () => {});\n" +
		"setTimeout(() => { throw 1; }, 1);\n" +
		"setTimeout(() => {\n  throw 2;\n}, 5);\n"}, "main.js")
	if want := dir + "/main.js:4\n  throw 2;\n  ^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

// Ticks after one that throws to a handler still run, each in order.
func TestNextTickAfterHandledThrowStillRuns(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e) => note('caught', e));
process.nextTick(() => note('a'));
process.nextTick(() => { throw 'b'; });
process.nextTick(() => note('c'));
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if got, want := strings.Join(notes, ","), "a,caught b,c"; got != want {
		t.Errorf("notes = %s, want %s", got, want)
	}
}
