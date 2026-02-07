// GNode - A V8 JavaScript runtime for Go
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrewcurioso/gnode/pkg/filesystem"
	"github.com/andrewcurioso/gnode/pkg/network"
	"github.com/andrewcurioso/gnode/pkg/nodejs/buffer"
	"github.com/andrewcurioso/gnode/pkg/nodejs/console"
	"github.com/andrewcurioso/gnode/pkg/nodejs/crypto"
	"github.com/andrewcurioso/gnode/pkg/nodejs/events"
	"github.com/andrewcurioso/gnode/pkg/nodejs/fs"
	"github.com/andrewcurioso/gnode/pkg/nodejs/http"
	"github.com/andrewcurioso/gnode/pkg/nodejs/module"
	gnodeos "github.com/andrewcurioso/gnode/pkg/nodejs/os"
	"github.com/andrewcurioso/gnode/pkg/nodejs/path"
	"github.com/andrewcurioso/gnode/pkg/nodejs/process"
	"github.com/andrewcurioso/gnode/pkg/nodejs/stream"
	"github.com/andrewcurioso/gnode/pkg/nodejs/timers"
	"github.com/andrewcurioso/gnode/pkg/nodejs/url"
	"github.com/andrewcurioso/gnode/pkg/nodejs/util"
	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/system"
	"golang.org/x/term"
)

// Global config
var documentRoot string
var sandboxMode bool

func main() {
	args := os.Args[1:]

	// Parse flags
	var evalCode string
	var scriptFile string
	
	for i := 0; i < len(args); i++ {
		arg := args[i]
		
		switch arg {
		case "-e", "--eval":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: -e requires an argument")
				os.Exit(1)
			}
			i++
			evalCode = args[i]
		case "-r", "--root":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --root requires a directory path")
				os.Exit(1)
			}
			i++
			documentRoot = args[i]
		case "-s", "--sandbox":
			sandboxMode = true
		case "-h", "--help":
			printHelp()
			return
		case "-v", "--version":
			fmt.Println("gnode v0.1.0")
			return
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "Error: unknown flag %s\n", arg)
				os.Exit(1)
			}
			if scriptFile == "" {
				scriptFile = arg
			}
		}
	}

	// Execute based on what was provided
	if evalCode != "" {
		if err := runCode(evalCode, "eval"); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if scriptFile != "" {
		if err := runFile(scriptFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// No script - check stdin or start REPL
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := runStdin(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	repl()
}

func printHelp() {
	fmt.Println(`GNode - JavaScript runtime powered by V8 and Go

Usage: gnode [options] [script.js] [arguments]

Options:
  -e, --eval <code>   Evaluate JavaScript code
  -r, --root <dir>    Sandbox fs operations to this directory
  -s, --sandbox       Use fake system info (hides real hostname, etc.)
  -h, --help          Show this help message
  -v, --version       Show version number

Examples:
  gnode script.js                   Run a JavaScript file
  gnode -e "console.log(1)"         Evaluate code
  gnode -r ./sandbox script.js      Run with sandboxed filesystem
  gnode -s -r ./sandbox script.js   Full sandbox (fs + system info)
  gnode                             Start REPL`)
}

func createRuntime() (*runtime.Runtime, error) {
	cfg := runtime.DefaultConfig()
	
	// Set up filesystem with optional sandboxing
	if documentRoot != "" {
		cfg.Filesystem = filesystem.NewLocalFilesystem(documentRoot)
	}

	// Set up system info sandboxing
	if sandboxMode {
		cfg.SystemInfo = system.NewSandboxedSystemInfo(nil)
		cfg.HTTPClient = network.NewNoOpHTTPClient()
	}

	rt, err := runtime.New(cfg)
	if err != nil {
		return nil, err
	}

	// Register modules (order matters - module system must be last)
	modules := []runtime.Module{
		console.New(),
		timers.New(),
		events.New(),
		process.New(),
		fs.New(),
		path.New(),
		buffer.New(),
		stream.New(),
		url.New(),
		gnodeos.New(),
		util.New(),
		crypto.New(),
		http.New(),
		module.New(), // CommonJS module system - must be last
	}

	for _, mod := range modules {
		if err := rt.RegisterModule(mod); err != nil {
			rt.Dispose()
			return nil, fmt.Errorf("failed to register %s module: %w", mod.Name(), err)
		}
	}

	return rt, nil
}

func runFile(filename string) error {
	source, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Get absolute path for __filename and __dirname
	absPath, err := filepath.Abs(filename)
	if err != nil {
		absPath = filename
	}
	dirname := filepath.Dir(absPath)

	return runCodeWithPath(string(source), filename, absPath, dirname)
}

func runStdin() error {
	source, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}

	return runCode(string(source), "[stdin]")
}

func runCode(source, origin string) error {
	return runCodeWithPath(source, origin, "", "")
}

func runCodeWithPath(source, origin, filename, dirname string) error {
	rt, err := createRuntime()
	if err != nil {
		return err
	}
	defer rt.Dispose()

	// Set __filename and __dirname if provided
	if filename != "" && dirname != "" {
		setupCode := fmt.Sprintf(`
			globalThis.__filename = %q;
			globalThis.__dirname = %q;
			if (typeof module !== 'undefined') {
				module.filename = %q;
				module.id = %q;
			}
		`, filename, dirname, filename, filename)
		if _, err := rt.RunScript(setupCode, "path_setup.js"); err != nil {
			return fmt.Errorf("failed to set paths: %w", err)
		}
	}

	result, err := rt.Run(source, origin)
	if err != nil {
		return fmt.Errorf("script error: %w", err)
	}

	if result != nil && !result.IsUndefined() {
		// Don't print result for file execution, only in REPL
		_ = result
	}

	return nil
}

func repl() {
	fmt.Println("GNode JavaScript Runtime v0.1.0")
	fmt.Println("Type .help for help, .exit to quit")
	fmt.Println()

	rt, err := createRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create runtime: %v\n", err)
		os.Exit(1)
	}
	defer rt.Dispose()

	scanner := bufio.NewScanner(os.Stdin)
	multiline := false
	var buffer strings.Builder

	for {
		if multiline {
			fmt.Print("... ")
		} else {
			fmt.Print("> ")
		}

		if !scanner.Scan() {
			fmt.Println()
			break
		}

		line := scanner.Text()

		// Handle REPL commands
		if !multiline && strings.HasPrefix(line, ".") {
			switch line {
			case ".exit":
				return
			case ".help":
				fmt.Println("REPL Commands:")
				fmt.Println("  .exit    Exit the REPL")
				fmt.Println("  .help    Show this help")
				fmt.Println("  .clear   Clear the current input")
				continue
			case ".clear":
				buffer.Reset()
				multiline = false
				continue
			}
		}

		buffer.WriteString(line)
		buffer.WriteString("\n")

		// Try to execute
		code := buffer.String()

		// Check for incomplete input (very basic heuristic)
		if isIncomplete(code) {
			multiline = true
			continue
		}

		multiline = false
		buffer.Reset()

		result, err := rt.Run(code, "repl")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			continue
		}

		// Run any pending async operations
		rt.EventLoop().Run()

		if result != nil && !result.IsUndefined() {
			fmt.Println(result.String())
		}
	}
}

// isIncomplete checks if the code appears to be incomplete.
func isIncomplete(code string) bool {
	// Count braces, brackets, and parens
	var braces, brackets, parens int
	inString := false
	stringChar := byte(0)
	escaped := false

	for i := 0; i < len(code); i++ {
		c := code[i]

		if escaped {
			escaped = false
			continue
		}

		if c == '\\' && inString {
			escaped = true
			continue
		}

		if !inString {
			switch c {
			case '"', '\'', '`':
				inString = true
				stringChar = c
			case '{':
				braces++
			case '}':
				braces--
			case '[':
				brackets++
			case ']':
				brackets--
			case '(':
				parens++
			case ')':
				parens--
			}
		} else if c == stringChar {
			inString = false
		}
	}

	return braces > 0 || brackets > 0 || parens > 0 || inString
}
