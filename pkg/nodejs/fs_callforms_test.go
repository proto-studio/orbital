package nodejs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// The fs module is tested here once, against a mock filesystem: each call form makes the
// interface calls it should, and an error from the interface reaches scripts as Node would
// give it. Each Filesystem implementation is tested on its own, through the Go interface,
// in pkg/runtime.

// mockCalls are the interface calls each case in testdata/fs_mock.js should make, by case.
// A call's synchronous, callback and promise forms make the same calls, so they share one
// entry, under the synchronous form's name less "Sync".
var mockCalls = map[string][]string{
	"readFile":   {"Open(/m/f)", "File.Stat(/m/f)", "File.Read(/m/f)", "File.Read(/m/f)", "File.Close(/m/f)"},
	"writeFile":  {"OpenFile(/m/f, O_WRONLY|O_CREAT|O_TRUNC, 666)", `File.Write(/m/f, "data")`, "File.Close(/m/f)"},
	"appendFile": {"OpenFile(/m/f, O_WRONLY|O_CREAT|O_APPEND, 666)", `File.Write(/m/f, "more")`, "File.Close(/m/f)"},
	"stat":       {"Stat(/m/f)"},
	"lstat":      {"Lstat(/m/f)"},
	"readdir":    {"ReadDir(/m/dir)"},
	"mkdir":      {"Mkdir(/m/dir, 777)"},
	// A recursive mkdir makes every missing parent.
	"mkdir_recursive": {"MkdirAll(/m/dir/a/b, 777)"},
	// rmdir and unlink check what the name is first: rmdir refuses a file, unlink a directory.
	"rmdir":  {"Lstat(/m/dir)", "Remove(/m/dir)"},
	"unlink": {"Lstat(/m/f)", "Remove(/m/f)"},
	// rmdir's deprecated recursive option removes the whole tree.
	"rmdir_recursive": {"Lstat(/m/dir)", "RemoveAll(/m/dir)"},
	"rename":          {"Rename(/m/f, /m/g)"},
	"copyFile":        {"Copy(/m/f, /m/g)"},
	"access":          {"Access(/m/f, 4)"},
	"chmod":           {"Chmod(/m/f, 600)"},
	"chown":           {"Chown(/m/f, 1, 2)"},
	"utimes":          {"Chtimes(/m/f, 1000, 2000)"},
	"truncate":        {"Truncate(/m/f, 2)"},
	"symlink":         {"Symlink(target, /m/l)"},
	"readlink":        {"Readlink(/m/l)"},
	"link":            {"Link(/m/f, /m/h)"},
	"statfs":          {"Statfs(/m/f)"},
	// fs.promises.realpath is the native one.
	"realpath.native": {"Realpath(/m/f)"},
	"realpath":        {"Realpath(/m/f)"},
	"existsSync":      {"Exists(/m/f)"},
	// Node's fs.exists is fs.access with F_OK.
	"exists": {"Access(/m/f, 0)"},
	// A positioned read or write seeks there and back, leaving the descriptor's own offset.
	"descriptorSync": {
		"OpenFile(/m/f, O_RDWR, 666)", "File.Stat(/m/f)",
		"File.Seek(/m/f, 0, 1)", "File.Seek(/m/f, 1, 0)", "File.Read(/m/f)", "File.Seek(/m/f, 0, 0)",
		"File.Seek(/m/f, 0, 1)", "File.Seek(/m/f, 2, 0)", `File.Write(/m/f, "XY")`, "File.Seek(/m/f, 0, 0)",
		"File.Close(/m/f)",
	},
	"descriptor": {"OpenFile(/m/f, O_WRONLY|O_CREAT|O_APPEND, 666)", "File.Stat(/m/f)", `File.Write(/m/f, "Z")`, "File.Close(/m/f)"},
	"promises.FileHandle": {
		"OpenFile(/m/f, O_RDONLY, 666)", "File.Stat(/m/f)",
		"File.Seek(/m/f, 0, 1)", "File.Seek(/m/f, 0, 0)", "File.Read(/m/f)", "File.Seek(/m/f, 0, 0)",
		"File.Close(/m/f)",
	},
	"createReadStream":  {"OpenFile(/m/f, O_RDONLY, 0)", "File.Read(/m/f)", "File.Read(/m/f)", "File.Close(/m/f)"},
	"createWriteStream": {"OpenFile(/m/f, O_WRONLY|O_CREAT|O_TRUNC, 666)", "File.Stat(/m/f)", `File.Write(/m/f, "streamed")`, "File.Close(/m/f)"},

	// Error cases: the one call that fails.
	"loop_stat":                 {"Stat(/m/loop)"},
	"loop_read":                 {"Open(/m/cyc_a)"},
	"loop_write":                {"OpenFile(/m/cyc_a, O_WRONLY|O_CREAT|O_TRUNC, 666)"},
	"loop_open":                 {"OpenFile(/m/cyc_b, O_RDONLY, 666)"},
	"loop_readdir":              {"ReadDir(/m/cyc_a)"},
	"loop_access":               {"Access(/m/cyc_a, 0)"},
	"loop_chmod":                {"Chmod(/m/cyc_a, 644)"},
	"loop_mkdir_inner":          {"Mkdir(/m/cyc_a/x, 777)"},
	"loop_realpath_native":      {"Realpath(/m/cyc_a)"},
	"loop_copy":                 {"Copy(/m/f, /m/cyc_a)"},
	"link_loop_path":            {"Link(/m/f, /m/cyc_a/h)"},
	"dangling_stat":             {"Stat(/m/dangling)"},
	"dangling_read":             {"Open(/m/dangling)"},
	"dangling_open_excl":        {"OpenFile(/m/dangling, O_WRONLY|O_CREAT|O_EXCL|O_TRUNC, 666)"},
	"dangling_mkdir":            {"Mkdir(/m/dangling, 777)"},
	"dangling_realpath_native":  {"Realpath(/m/dangling)"},
	"name_too_long_stat":        {"Stat(/m/" + strings.Repeat("n", 256) + ")"},
	"name_too_long_write":       {"OpenFile(/m/" + strings.Repeat("n", 256) + ", O_WRONLY|O_CREAT|O_TRUNC, 666)"},
	"name_too_long_symlink":     {"Symlink(f, /m/" + strings.Repeat("n", 256) + ")"},
	"symlink_onto_file":         {"Symlink(x, /m/f)"},
	"symlink_missing_parent":    {"Symlink(x, /m/nodir/l)"},
	"symlink_parent_file":       {"Symlink(x, /m/f/l)"},
	"readlink_file":             {"Readlink(/m/f)"},
	"readlink_missing":          {"Readlink(/m/nope)"},
	"link_exists":               {"Link(/m/f, /m/copied)"},
	"link_missing":              {"Link(/m/nope, /m/h0)"},
	"link_dir":                  {"Link(/m/b, /m/bh)"},
	"read_through_file":         {"Open(/m/f/x)"},
	"through_file":              {"Stat(/m/abs/x)"},
	"file_trailing_slash_lstat": {"Lstat(/m/f/)"},
	"rmdir_link":                {"Lstat(/m/bl)"},
	"cb_symlink_exists":         {"Symlink(f, /m/cbl)"},
	"cb_readlink_file":          {"Readlink(/m/f)"},
	"cb_stat_loop":              {"Stat(/m/cyc_a)"},
	"cb_stat_dangling":          {"Stat(/m/dangling2)"},
	"cb_link_exists":            {"Link(/m/f, /m/cbh)"},
	"cb_link_dir":               {"Link(/m/b, /m/cbbh)"},
	"cb_realpath_native_loop":   {"Realpath(/m/cyc_a)"},
	"cb_read_loop":              {"Open(/m/cyc_a)"},
	"p_symlink_exists":          {"Symlink(b, /m/pl)"},
	"p_readlink_dir":            {"Readlink(/m/b)"},
	"p_stat_loop":               {"Stat(/m/cyc_a)"},
	"p_link_missing":            {"Link(/m/nope, /m/ph2)"},
	"p_realpath_loop":           {"Realpath(/m/cyc_a)"},
	"p_read_loop":               {"Open(/m/cyc_a)"},
	"p_open_loop":               {"OpenFile(/m/cyc_a, O_RDONLY, 666)"},

	// Other errnos. readFile of a directory sees from the open file's Stat what it is, and
	// fails before reading.
	"isdir_write":    {"OpenFile(/m/dirfull, O_WRONLY|O_CREAT|O_TRUNC, 666)"},
	"isdir_read":     {"Open(/m/dirfull)", "File.Stat(/m/dirfull)", "File.Close(/m/dirfull)"},
	"notempty_rmdir": {"Lstat(/m/dirfull)", "Remove(/m/dirfull)"},
	"badf_write":     {"OpenFile(/m/f, O_RDONLY, 666)", "File.Stat(/m/f)", `File.Write(/m/f, "x")`, "File.Close(/m/f)"},

	// A refused name: every call fails, so each case makes only its first. rmdir, rm and
	// unlink look at the name first; unlink still tries to remove it.
	"refused_readFile":          {"Open(/m/locked/x)"},
	"refused_writeFile":         {"OpenFile(/m/locked/new, O_WRONLY|O_CREAT|O_TRUNC, 666)"},
	"refused_appendFile":        {"OpenFile(/m/locked/x, O_WRONLY|O_CREAT|O_APPEND, 666)"},
	"refused_open":              {"OpenFile(/m/locked/x, O_RDONLY, 666)"},
	"refused_openWrite":         {"OpenFile(/m/locked/new, O_WRONLY|O_CREAT|O_TRUNC, 666)"},
	"refused_stat":              {"Stat(/m/locked/x)"},
	"refused_lstat":             {"Lstat(/m/locked/x)"},
	"refused_readdir":           {"ReadDir(/m/locked/d)"},
	"refused_opendir":           {"ReadDir(/m/locked/d)"},
	"refused_mkdir":             {"Mkdir(/m/locked/e, 777)"},
	"refused_mkdirRecursive":    {"MkdirAll(/m/locked/e/f, 777)"},
	"refused_unlink":            {"Lstat(/m/locked/x)", "Remove(/m/locked/x)"},
	"refused_rmdir":             {"Lstat(/m/locked/d)"},
	"refused_rm":                {"Lstat(/m/locked/x)"},
	"refused_rename":            {"Rename(/m/locked/x, /m/locked/y)"},
	"refused_copyFile":          {"Copy(/m/locked/x, /m/locked/y)"},
	"refused_access":            {"Access(/m/locked/x, 0)"},
	"refused_chmod":             {"Chmod(/m/locked/x, 644)"},
	"refused_chown":             {"Chown(/m/locked/x, 1, 2)"},
	"refused_utimes":            {"Chtimes(/m/locked/x, 1, 1)"},
	"refused_truncate":          {"Truncate(/m/locked/x, 0)"},
	"refused_symlink":           {"Symlink(target, /m/locked/link)"},
	"refused_readlink":          {"Readlink(/m/locked/x)"},
	"refused_link":              {"Link(/m/locked/x, /m/locked/hard)"},
	"refused_statfs":            {"Statfs(/m/locked/x)"},
	"refused_existsSync":        {"Exists(/m/locked/x)"},
	"refused_exists":            {"Access(/m/locked/x, 0)"},
	"refused_createReadStream":  {"OpenFile(/m/locked/x, O_RDONLY, 0)"},
	"refused_createWriteStream": {"OpenFile(/m/locked/new, O_WRONLY|O_CREAT|O_TRUNC, 666)"},
}

// mockCallsFor gives the calls case name should make: its own entry, or its call's.
func mockCallsFor(name string) ([]string, bool) {
	if want, ok := mockCalls[name]; ok {
		return want, true
	}
	call := strings.Replace(name, "promises.", "", 1)
	call = strings.Replace(call, "Sync", "", 1)
	want, ok := mockCalls[call]
	return want, ok
}

// Every form of each fs call makes the interface calls it should, and an errno from the
// interface reaches scripts as Node gives it. Under Node, the error cases fail on disk with
// the same errno, and fs_mock.node.json is what they must match.
func TestFS_CallFormsMapToFilesystem(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_mock.js", "testdata/fs_mock.node.json"
	if captureFromNode(t) {
		dir := t.TempDir()
		output, err := exec.Command("node", scriptPath, dir).Output()
		if chmodErr := os.Chmod(filepath.Join(dir, "locked"), 0o755); chmodErr != nil {
			t.Fatal(chmodErr)
		}
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
	mock := newMockFS()
	got := runJS(t, &runtime.Config{Filesystem: mock, Timeout: 60 * time.Second}, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", "/m", script))
	if failed, ok := got["failed"]; ok {
		t.Fatal(failed)
	}
	node := readExpected(t, expectedPath)
	for name := range node {
		if _, ok := got[name]; !ok {
			t.Errorf("%s: captured from Node but not run", name)
		}
	}

	ran := slices.DeleteFunc(slices.Clone(mock.cases), func(name string) bool { return name == "end" })
	if len(ran) != len(got) {
		t.Fatalf("%d cases marked, %d results", len(ran), len(got))
	}
	errorCases := 0
	for _, name := range ran {
		want, ok := mockCallsFor(name)
		if !ok {
			t.Errorf("%s: no expected calls; made\n\t%s", name, strings.Join(mock.callsIn(name), "\n\t"))
		} else if calls := mock.callsIn(name); !slices.Equal(calls, want) {
			t.Errorf("%s made\n\t%s\nwant\n\t%s", name, strings.Join(calls, "\n\t"), strings.Join(want, "\n\t"))
		}
		result, _ := got[name].(map[string]any)
		if expected, ok := node[name]; ok {
			errorCases++
			if !reflect.DeepEqual(any(result), expected) {
				t.Errorf("%s = %v, want %v as in Node", name, result, expected)
			}
		} else if result["error"] != nil {
			t.Errorf("%s failed: %v", name, result["error"])
		}
	}
	if errorCases == 0 {
		t.Error("no case was compared with Node")
	}
}

// Reading a directory fails at the read, not the open, with EISDIR, as on disk, in each form.
func TestFS_ReadErrorAfterOpen(t *testing.T) {
	got := runJS(t, &runtime.Config{Filesystem: newMockFS()}, `
const fs = require('fs');
fs.existsSync('/@mark/read/EISDIR/File.Read');
const describe = (e) => e && [e.code, e.syscall, e.message, e.path === undefined];
const attempt = (fn) => { try { fn(); return null; } catch (e) { return describe(e); } };
fs.readFile('/dir', (err) => require('fs/promises').readFile('/dir').catch((perr) => done({
  sync: attempt(() => fs.readFileSync('/dir')),
  callback: describe(err),
  promise: describe(perr),
})));`)
	read := []any{"EISDIR", "read", "EISDIR: illegal operation on a directory, read", true}
	wantJSON(t, got, map[string]any{"sync": read, "callback": read, "promise": read})
}

// unlink refuses a directory before asking the filesystem to remove it, with the error
// unlink(2) gives: EPERM on macOS and EISDIR on Linux.
func TestFS_UnlinkRefusesADirectory(t *testing.T) {
	mock := newMockFS()
	got := runJS(t, &runtime.Config{Filesystem: mock}, `
const fs = require('fs');
fs.existsSync('/@mark/unlink/-');
try { fs.unlinkSync('/m/dir'); done({ removed: true }); } catch (e) { done({ code: e.code, syscall: e.syscall, path: e.path }); }`)
	code := "EISDIR"
	if goruntime.GOOS == "darwin" {
		code = "EPERM"
	}
	wantJSON(t, got, map[string]any{"code": code, "syscall": "unlink", "path": "/m/dir"})
	if calls := mock.callsIn("unlink"); !slices.Equal(calls, []string{"Lstat(/m/dir)"}) {
		t.Errorf("unlink made %v, want only the Lstat", calls)
	}
}
