// GNode - A V8 JavaScript runtime for Go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrewcurioso/gnode/internal/nodejs/buffer"
	"github.com/andrewcurioso/gnode/internal/nodejs/console"
	"github.com/andrewcurioso/gnode/internal/nodejs/crypto"
	"github.com/andrewcurioso/gnode/internal/nodejs/esm"
	"github.com/andrewcurioso/gnode/internal/nodejs/events"
	"github.com/andrewcurioso/gnode/internal/nodejs/fs"
	"github.com/andrewcurioso/gnode/internal/nodejs/http"
	"github.com/andrewcurioso/gnode/internal/nodejs/module"
	gnodeos "github.com/andrewcurioso/gnode/internal/nodejs/os"
	"github.com/andrewcurioso/gnode/internal/nodejs/path"
	"github.com/andrewcurioso/gnode/internal/nodejs/process"
	"github.com/andrewcurioso/gnode/internal/nodejs/stream"
	"github.com/andrewcurioso/gnode/internal/nodejs/timers"
	"github.com/andrewcurioso/gnode/internal/nodejs/url"
	"github.com/andrewcurioso/gnode/internal/nodejs/util"
	"github.com/andrewcurioso/gnode/pkg/environment"
	"github.com/andrewcurioso/gnode/pkg/filesystem"
	"github.com/andrewcurioso/gnode/pkg/network"
	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/system"
	"github.com/chzyer/readline"
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

// esmLoader is the global ESM module loader (initialized after runtime creation)
var esmLoader *esm.ESM

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
		cfg.Environment = environment.NewSandboxedEnvironmentWithDefaults()
	}

	rt, err := runtime.New(cfg)
	if err != nil {
		return nil, err
	}

	// Create ESM loader
	esmLoader = esm.New()

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
		esmLoader,    // ES Module system
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
	// Get absolute path for __filename and __dirname
	absPath, err := filepath.Abs(filename)
	if err != nil {
		absPath = filename
	}

	// Check if this is an ES module (.mjs extension)
	if strings.HasSuffix(filename, ".mjs") {
		return runESModule(absPath)
	}

	source, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	dirname := filepath.Dir(absPath)
	return runCodeWithPath(string(source), filename, absPath, dirname)
}

func runESModule(filename string) error {
	rt, err := createRuntime()
	if err != nil {
		return err
	}
	defer rt.Dispose()

	// Run the ES module
	_, err = esmLoader.RunModuleFile(filename)
	if err != nil {
		return fmt.Errorf("module error: %w", err)
	}

	// Run any pending async operations
	rt.EventLoop().Run()

	return nil
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

	// Set up readline with history
	historyFile := filepath.Join(os.TempDir(), ".gnode_history")
	if home, err := os.UserHomeDir(); err == nil {
		historyFile = filepath.Join(home, ".gnode_history")
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "> ",
		HistoryFile:       historyFile,
		HistoryLimit:      1000,
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize readline: %v\n", err)
		os.Exit(1)
	}
	defer rl.Close()

	multiline := false
	var buffer strings.Builder

	for {
		if multiline {
			rl.SetPrompt("... ")
		} else {
			rl.SetPrompt("> ")
		}

		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				if multiline {
					// Cancel multiline input
					buffer.Reset()
					multiline = false
					fmt.Println()
					continue
				}
				// Double Ctrl+C to exit
				fmt.Println("(To exit, press Ctrl+C again or type .exit)")
				line2, err2 := rl.Readline()
				if err2 == readline.ErrInterrupt {
					return
				}
				if err2 == nil {
					line = line2
				} else {
					continue
				}
			} else if err == io.EOF {
				fmt.Println()
				return
			} else {
				continue
			}
		}

		// Handle REPL commands
		if !multiline && strings.HasPrefix(line, ".") {
			switch line {
			case ".exit":
				return
			case ".help":
				fmt.Println("REPL Commands:")
				fmt.Println("  .exit      Exit the REPL")
				fmt.Println("  .help      Show this help")
				fmt.Println("  .clear     Clear the current input")
				fmt.Println("  .history   Show command history")
				fmt.Println()
				fmt.Println("Keyboard Shortcuts:")
				fmt.Println("  Up/Down    Navigate history")
				fmt.Println("  Left/Right Move cursor")
				fmt.Println("  Home/End   Jump to start/end of line")
				fmt.Println("  Ctrl+A/E   Jump to start/end of line")
				fmt.Println("  Ctrl+W     Delete word backward")
				fmt.Println("  Ctrl+U     Delete to start of line")
				fmt.Println("  Ctrl+K     Delete to end of line")
				fmt.Println("  Ctrl+L     Clear screen")
				fmt.Println("  Ctrl+R     Search history")
				fmt.Println("  Ctrl+C     Cancel current input")
				fmt.Println("  Ctrl+D     Exit (on empty line)")
				continue
			case ".clear":
				buffer.Reset()
				multiline = false
				continue
			case ".history":
				// Read and display history
				if data, err := os.ReadFile(historyFile); err == nil {
					lines := strings.Split(string(data), "\n")
					for i, l := range lines {
						if l != "" {
							fmt.Printf("%4d  %s\n", i+1, l)
						}
					}
				}
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
