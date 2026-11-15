package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// NODE_OPTIONS is checked as Node 20.18.1 checks it. Each error is the message Node
// prints after "node: " (and exits 9 for); each accepted value lists the options
// Orbital applies, as the argv tokens it injects.
func TestParseNodeOptions(t *testing.T) {
	cases := []struct {
		in      string
		args    []string
		wantErr string
	}{
		{in: "--bogus", wantErr: "--bogus is not allowed in NODE_OPTIONS"},
		{in: "--bogus=1", wantErr: "--bogus= is not allowed in NODE_OPTIONS"},
		{in: "--no-bogus", wantErr: "--no-bogus is not allowed in NODE_OPTIONS"},
		{in: "-e 1", wantErr: "-e is not allowed in NODE_OPTIONS"},
		{in: "--print", wantErr: "--print is not allowed in NODE_OPTIONS"},
		{in: "--v8-options", wantErr: "--v8-options is not allowed in NODE_OPTIONS"},
		{in: "--harmony", wantErr: "--harmony is not allowed in NODE_OPTIONS"},
		{in: "--stack-size=100", wantErr: "--stack-size= is not allowed in NODE_OPTIONS"},
		{in: "-rfoo", wantErr: "-rfoo is not allowed in NODE_OPTIONS"},
		{in: "--", wantErr: "-- is not allowed in NODE_OPTIONS"},
		{in: "--no-warnings --bogus", wantErr: "--bogus is not allowed in NODE_OPTIONS"},
		{in: `"unterminated`, wantErr: "invalid value for NODE_OPTIONS (unterminated string)"},
		{in: `"a\`, wantErr: "invalid value for NODE_OPTIONS (invalid escape)"},
		{in: "-r", wantErr: "-r requires an argument"},
		{in: "--require", wantErr: "--require requires an argument"},
		{in: "--require=", wantErr: "--require= requires an argument"},
		{in: "--title=", wantErr: "--title= requires an argument"},
		{in: "-C", wantErr: "-C requires an argument"},
		{in: "--no-title", wantErr: "--no-title is an invalid negation because it is not a boolean option"},
		{in: "--no-experimental-modules", wantErr: "--no-experimental-modules is an invalid negation because it is not a boolean option"},
		{in: "--input-type=bogus", wantErr: `--input-type must be "module" or "commonjs"`},
		{in: "--input-type=commonjs --input-type=bogus", wantErr: `--input-type must be "module" or "commonjs"`},
		{in: "--conditions --bogus --title=ok", wantErr: "--conditions requires an argument"},
		{in: "--title --bogus", wantErr: "--title requires an argument"},
		{in: "--title -x", wantErr: "--title requires an argument"},
		{in: "--require --import", wantErr: "--require requires an argument"},

		{in: "--title=x", args: []string{"--title", "x"}},
		{in: "--title=-x", args: []string{"--title", "-x"}},
		{in: "--title café -r ./café.js", args: []string{"--require", "./café.js", "--title", "café"}},
		{in: "--input-type=bogus --input-type=commonjs", args: []string{"--input-type", "commonjs"}},
		{in: `"--title=a b"`, args: []string{"--title", "a b"}},
		{in: `--title "a b"`, args: []string{"--title", "a b"}},
		{in: `--title "a\"b"`, args: []string{"--title", `a"b`}},
		{in: "--title 'x y'", args: []string{"--title", "'x"}},
		{in: "--title a --title b", args: []string{"--title", "b"}},
		{in: "--input-type module", args: []string{"--input-type", "module"}},
		{in: "--no-warnings", args: []string{"--no-warnings"}},
		{in: "--no_warnings", args: []string{"--no-warnings"}},
		{in: "--no-warnings --warnings", args: nil},
		{in: "-r ./a.js --import=./b.mjs --require=c", args: []string{"--require", "./a.js", "--import", "./b.mjs", "--require", "c"}},
		{in: "  --import  x  ", args: []string{"--import", "x"}},
		// Node stops at the first argument that isn't an option.
		{in: "foo --bogus", args: nil},
		{in: "- --bogus", args: nil},
		// Accepted, with no effect in Orbital.
		{in: "--enable-source-maps --max-old-space-size=100 --max_old_space_size=100 --expose-gc --no-expose-gc", args: nil},
		{in: "--conditions=dev -C dev --unhandled-rejections=strict --trace-warnings=1 --no-deprecation --deprecation=false", args: nil},
		{in: "--experimental-modules --es-module-specifier-resolution=node --loader x --inspect=9229 --trace-events-enabled", args: nil},
	}
	for _, tc := range cases {
		got, err := parseNodeOptions(tc.in)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%q: error %v, want %q", tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if args := got.args(); !reflect.DeepEqual(args, tc.args) && !(len(args) == 0 && len(tc.args) == 0) {
			t.Errorf("%q: args %q, want %q", tc.in, args, tc.args)
		}
	}
}

// TestMain runs the CLI itself when a test re-executes this binary with
// ORBITAL_TEST_RUN_MAIN set, so tests can check what the orbital command does.
func TestMain(m *testing.M) {
	if os.Getenv("ORBITAL_TEST_RUN_MAIN") == "1" {
		os.Args = append([]string{"orbital"}, strings.Fields(os.Getenv("ORBITAL_TEST_ARGS"))...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runOrbital(t *testing.T, nodeOptions string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ORBITAL_TEST_RUN_MAIN=1",
		"ORBITAL_TEST_ARGS="+strings.Join(args, " "), "NODE_OPTIONS="+nodeOptions)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// Like Node, an option NODE_OPTIONS may not hold stops the program with exit code 9
// before anything runs, and the options Orbital implements take effect, with the
// command line's own overriding them.
func TestNodeOptions_CLI(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "t.js")
	if err := os.WriteFile(script, []byte(`console.log(process.title);`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runOrbital(t, "--title=x --bogus", script); code != 9 || out != "orbital: --bogus is not allowed in NODE_OPTIONS\n" {
		t.Fatalf("--bogus: exit %d, output %q", code, out)
	}
	if out, code := runOrbital(t, `"--title=from env"`, script); code != 0 || out != "from env\n" {
		t.Fatalf("--title: exit %d, output %q", code, out)
	}
	if out, code := runOrbital(t, "--title=env", "--title", "cli", script); code != 0 || out != "cli\n" {
		t.Fatalf("--title override: exit %d, output %q", code, out)
	}
	pre := filepath.Join(dir, "pre.js")
	if err := os.WriteFile(pre, []byte(`console.log('preloaded');`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runOrbital(t, "--enable-source-maps -r "+pre, "--title", "main", script); code != 0 || out != "preloaded\nmain\n" {
		t.Fatalf("-r: exit %d, output %q", code, out)
	}
}
