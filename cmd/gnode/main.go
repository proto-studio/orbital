// GNode - A V8 JavaScript runtime for Go
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/andrewcurioso/gnode/pkg/nodejs/console"
	"github.com/andrewcurioso/gnode/pkg/nodejs/events"
	"github.com/andrewcurioso/gnode/pkg/nodejs/fs"
	"github.com/andrewcurioso/gnode/pkg/nodejs/path"
	"github.com/andrewcurioso/gnode/pkg/nodejs/process"
	"github.com/andrewcurioso/gnode/pkg/nodejs/timers"
	"github.com/andrewcurioso/gnode/pkg/runtime"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 2 {
		// Check if stdin is a terminal or a pipe
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			// Stdin is piped, read and execute as script
			if err := runStdin(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		repl()
		return
	}

	// Handle flags
	arg := os.Args[1]
	if arg == "-e" || arg == "--eval" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Error: -e requires an argument")
			os.Exit(1)
		}
		if err := runCode(os.Args[2], "eval"); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if arg == "-h" || arg == "--help" {
		printHelp()
		return
	}

	if arg == "-v" || arg == "--version" {
		fmt.Println("gnode v0.1.0")
		return
	}

	// Run file
	if err := runFile(arg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`GNode - JavaScript runtime powered by V8 and Go

Usage: gnode [options] [script.js] [arguments]

Options:
  -e, --eval <code>   Evaluate JavaScript code
  -h, --help          Show this help message
  -v, --version       Show version number

Examples:
  gnode script.js           Run a JavaScript file
  gnode -e "console.log(1)" Evaluate code
  gnode                     Start REPL`)
}

func createRuntime() (*runtime.Runtime, error) {
	rt, err := runtime.New(nil)
	if err != nil {
		return nil, err
	}

	// Register modules
	modules := []runtime.Module{
		console.New(),
		timers.New(),
		events.New(),
		process.New(),
		fs.New(),
		path.New(),
	}

	for _, mod := range modules {
		if err := rt.RegisterModule(mod); err != nil {
			rt.Dispose()
			return nil, fmt.Errorf("failed to register %s module: %w", mod.Name(), err)
		}
	}

	// Add a basic require function
	requireCode := `
globalThis.require = function(moduleName) {
	switch(moduleName) {
		case 'events':
			return __events_module;
		case 'fs':
			return __fs_module;
		case 'path':
			return __path_module;
		default:
			throw new Error('Cannot find module: ' + moduleName);
	}
};
`
	if _, err := rt.RunScript(requireCode, "require.js"); err != nil {
		rt.Dispose()
		return nil, fmt.Errorf("failed to setup require: %w", err)
	}

	return rt, nil
}

func runFile(filename string) error {
	source, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	return runCode(string(source), filename)
}

func runStdin() error {
	source, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}

	return runCode(string(source), "[stdin]")
}

func runCode(source, origin string) error {
	rt, err := createRuntime()
	if err != nil {
		return err
	}
	defer rt.Dispose()

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
