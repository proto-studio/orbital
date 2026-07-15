// Orbital - A V8 JavaScript runtime for Go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"golang.org/x/term"
	"proto.zip/studio/orbital/internal/nodejs/abort"
	"proto.zip/studio/orbital/internal/nodejs/assert"
	"proto.zip/studio/orbital/internal/nodejs/buffer"
	"proto.zip/studio/orbital/internal/nodejs/child_process"
	"proto.zip/studio/orbital/internal/nodejs/console"
	"proto.zip/studio/orbital/internal/nodejs/crypto"
	"proto.zip/studio/orbital/internal/nodejs/dgram"
	"proto.zip/studio/orbital/internal/nodejs/diagnostics_channel"
	"proto.zip/studio/orbital/internal/nodejs/dns"
	"proto.zip/studio/orbital/internal/nodejs/domain"
	"proto.zip/studio/orbital/internal/nodejs/esm"
	"proto.zip/studio/orbital/internal/nodejs/events"
	"proto.zip/studio/orbital/internal/nodejs/fetch"
	"proto.zip/studio/orbital/internal/nodejs/fs"
	"proto.zip/studio/orbital/internal/nodejs/http"
	"proto.zip/studio/orbital/internal/nodejs/http2"
	"proto.zip/studio/orbital/internal/nodejs/https"
	"proto.zip/studio/orbital/internal/nodejs/module"
	"proto.zip/studio/orbital/internal/nodejs/net"
	orbitalos "proto.zip/studio/orbital/internal/nodejs/os"
	"proto.zip/studio/orbital/internal/nodejs/path"
	"proto.zip/studio/orbital/internal/nodejs/perf_hooks"
	"proto.zip/studio/orbital/internal/nodejs/process"
	"proto.zip/studio/orbital/internal/nodejs/punycode"
	"proto.zip/studio/orbital/internal/nodejs/querystring"
	readlinemod "proto.zip/studio/orbital/internal/nodejs/readline"
	replmod "proto.zip/studio/orbital/internal/nodejs/repl"
	"proto.zip/studio/orbital/internal/nodejs/stream"
	"proto.zip/studio/orbital/internal/nodejs/string_decoder"
	"proto.zip/studio/orbital/internal/nodejs/sys"
	"proto.zip/studio/orbital/internal/nodejs/test"
	"proto.zip/studio/orbital/internal/nodejs/timers"
	"proto.zip/studio/orbital/internal/nodejs/tls"
	"proto.zip/studio/orbital/internal/nodejs/url"
	"proto.zip/studio/orbital/internal/nodejs/util"
	"proto.zip/studio/orbital/internal/nodejs/webcrypto"
	"proto.zip/studio/orbital/internal/nodejs/webstream"
	"proto.zip/studio/orbital/internal/nodejs/zlib"
	"proto.zip/studio/orbital/pkg/runtime"
)

// Version info
const orbitalVersion = "0.1.0"

// Global config
var documentRoot string
var sandboxMode bool
var processTitle string
var silenceWarnings bool
var requireModules []string
var inputType string // "module" or "commonjs"
var executionTimeout time.Duration

// Network permissions (Deno-style)
var allowNet bool          // --allow-net with no value
var allowNetHosts []string // --allow-net=host:port,...
var denyNetHosts []string  // --deny-net=host:port,...

func main() {
	args := os.Args[1:]

	// Parse flags
	var evalCode string
	var printCode string
	var checkOnly bool
	var forceInteractive bool
	var scriptFile string
	var scriptArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Once we have a script file, remaining args are script args
		if scriptFile != "" {
			scriptArgs = append(scriptArgs, arg)
			continue
		}

		// Handle --flag=value style
		if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			arg = parts[0]
			args = append(args[:i+1], append([]string{parts[1]}, args[i+1:]...)...)
		}

		switch arg {
		case "-e", "--eval":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: -e requires an argument")
				os.Exit(1)
			}
			i++
			evalCode = args[i]
		case "-p", "--print":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: -p requires an argument")
				os.Exit(1)
			}
			i++
			printCode = args[i]
		case "-c", "--check":
			checkOnly = true
		case "-i", "--interactive":
			forceInteractive = true
		case "-r", "--require":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: -r requires a module path")
				os.Exit(1)
			}
			i++
			requireModules = append(requireModules, args[i])
		case "--root":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --root requires a directory path")
				os.Exit(1)
			}
			i++
			documentRoot = args[i]
		case "-s", "--sandbox":
			sandboxMode = true
		case "--input-type":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --input-type requires 'module' or 'commonjs'")
				os.Exit(1)
			}
			i++
			inputType = args[i]
			if inputType != "module" && inputType != "commonjs" {
				fmt.Fprintln(os.Stderr, "Error: --input-type must be 'module' or 'commonjs'")
				os.Exit(1)
			}
		case "--title":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --title requires a string")
				os.Exit(1)
			}
			i++
			processTitle = args[i]
		case "--timeout":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --timeout requires a duration (e.g., 30s, 5m)")
				os.Exit(1)
			}
			i++
			var err error
			executionTimeout, err = time.ParseDuration(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid timeout duration: %v\n", err)
				os.Exit(1)
			}
		case "--no-warnings":
			silenceWarnings = true
		case "-N", "--allow-net":
			// Deno-style: --allow-net or --allow-net=host:port,host2
			// Check if there's a value (either next arg or already split by =)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				allowNetHosts = append(allowNetHosts, strings.Split(args[i], ",")...)
			} else {
				allowNet = true // Allow all network access
			}
		case "--deny-net":
			// Deno-style: --deny-net or --deny-net=host:port,host2
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				denyNetHosts = append(denyNetHosts, strings.Split(args[i], ",")...)
			} else {
				// --deny-net with no value means deny all
				denyNetHosts = append(denyNetHosts, "*")
			}
		case "-h", "--help":
			printHelp()
			return
		case "-v", "--version":
			fmt.Printf("orbital v%s\n", orbitalVersion)
			return
		case "--":
			// Everything after -- is script args
			if i+1 < len(args) {
				scriptArgs = args[i+1:]
			}
			i = len(args) // Exit loop
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "Error: unknown flag %s\n", arg)
				fmt.Fprintln(os.Stderr, "Use -h or --help for usage information")
				os.Exit(1)
			}
			scriptFile = arg
		}
	}

	// Set runtime.argv for scripts
	setProcessArgv(scriptFile, scriptArgs)

	// Execute based on what was provided
	if printCode != "" {
		result, err := runCodeAndReturn(printCode, "[eval]")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if result != "" {
			fmt.Println(result)
		}
		return
	}

	if evalCode != "" {
		if err := runCode(evalCode, "[eval]"); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if forceInteractive {
			repl()
		}
		return
	}

	if checkOnly {
		if scriptFile == "" {
			fmt.Fprintln(os.Stderr, "Error: -c requires a script file")
			os.Exit(1)
		}
		if err := checkSyntax(scriptFile); err != nil {
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
		if forceInteractive {
			repl()
		}
		return
	}

	// No script - check stdin or start REPL
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := runStdin(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if forceInteractive {
			repl()
		}
		return
	}

	repl()
}

// setProcessArgv sets up runtime.argv values
var processArgv []string

func setProcessArgv(scriptFile string, scriptArgs []string) {
	processArgv = []string{"orbital"}
	if scriptFile != "" {
		processArgv = append(processArgv, scriptFile)
	}
	processArgv = append(processArgv, scriptArgs...)
}

// buildNetworkPolicy creates a NetworkPolicy from CLI flags (Deno-style).
func buildNetworkPolicy() (runtime.NetworkPolicy, error) {
	// Check for --deny-net with no hosts (deny all)
	for _, h := range denyNetHosts {
		if h == "*" {
			return runtime.NewDenyAllPolicy(), nil
		}
	}

	// If --allow-net with no hosts specified (allow all), but may have deny rules
	if allowNet && len(allowNetHosts) == 0 && len(denyNetHosts) == 0 {
		return runtime.NewAllowAllPolicy(), nil
	}

	// Build a rule-based policy
	policy := runtime.NewRuleBasedPolicy()

	// Deny rules take precedence - add them first
	for _, hostSpec := range denyNetHosts {
		host, port := parseHostPort(hostSpec)
		ports := []string{"*"}
		if port != "" {
			ports = []string{port}
		}
		policy.AddRule(&runtime.NetworkRule{
			Action:      runtime.ActionDeny,
			Protocol:    runtime.ProtocolAny,
			Direction:   runtime.DirectionAny,
			Ports:       ports,
			Hosts:       []string{host},
			Description: fmt.Sprintf("deny %s", hostSpec),
		})
	}

	// Add allow rules
	if allowNet {
		// --allow-net without specific hosts means allow all (after deny rules)
		policy.DefaultAction = runtime.ActionAllow
	} else if len(allowNetHosts) > 0 {
		// Allow only specific hosts
		for _, hostSpec := range allowNetHosts {
			host, port := parseHostPort(hostSpec)
			ports := []string{"*"}
			if port != "" {
				ports = []string{port}
			}
			policy.AddRule(&runtime.NetworkRule{
				Action:      runtime.ActionAllow,
				Protocol:    runtime.ProtocolAny,
				Direction:   runtime.DirectionAny,
				Ports:       ports,
				Hosts:       []string{host},
				Description: fmt.Sprintf("allow %s", hostSpec),
			})
		}
		// Default is deny if specific hosts are listed
		policy.DefaultAction = runtime.ActionDeny
	}

	return policy, nil
}

// parseHostPort splits a host:port string into host and port parts.
func parseHostPort(hostSpec string) (host, port string) {
	hostSpec = strings.TrimSpace(hostSpec)

	// Handle IPv6 addresses like [::1]:8080
	if strings.HasPrefix(hostSpec, "[") {
		if idx := strings.LastIndex(hostSpec, "]:"); idx != -1 {
			return hostSpec[:idx+1], hostSpec[idx+2:]
		}
		return strings.Trim(hostSpec, "[]"), ""
	}

	// Handle regular host:port
	if idx := strings.LastIndex(hostSpec, ":"); idx != -1 {
		// Make sure this isn't an IPv6 address without brackets
		if strings.Count(hostSpec, ":") == 1 {
			return hostSpec[:idx], hostSpec[idx+1:]
		}
	}

	return hostSpec, ""
}

func printHelp() {
	fmt.Printf(`Orbital - JavaScript runtime powered by V8 and Go (v%s)

Usage: orbital [options] [script.js] [arguments]

Options (Node.js compatible):
  -e, --eval <code>       Evaluate JavaScript code
  -p, --print <code>      Evaluate and print result
  -c, --check             Syntax check without executing
  -i, --interactive       Start REPL after script/stdin
  -r, --require <module>  Preload module at startup (can be repeated)
  --input-type=<type>     Set input type for stdin: 'module' or 'commonjs'
  -h, --help              Show this help message
  -v, --version           Show version number

Sandbox Options:
  --root <dir>            Sandbox filesystem to this directory
  -s, --sandbox           Full sandbox mode (fake system info, no network)
  --timeout <duration>    Execution timeout (e.g., 30s, 5m)
  --title <title>         Set runtime.title
  --no-warnings           Silence warnings

Network Permissions (Deno-style):
  -N, --allow-net         Allow all network access
  --allow-net=<hosts>     Allow network to specific hosts (comma-separated)
  --deny-net              Deny all network access
  --deny-net=<hosts>      Deny network to specific hosts (comma-separated)

  Host format: hostname, hostname:port, IP, IP:port, or CIDR (e.g., 10.0.0.0/8)
  Deny rules take precedence over allow rules.

Examples:
  orbital script.js                   Run a JavaScript file
  orbital -e "runtime.log(1+1)"       Evaluate code
  orbital -p "1+1"                    Evaluate and print: 2
  orbital -c script.js                Check syntax only
  orbital -i script.js                Run script then start REPL
  orbital -r ./setup.js script.js     Preload setup.js before script
  orbital --root ./sandbox script.js  Run with sandboxed filesystem
  orbital -s --root ./sandbox app.js  Full sandbox (fs + system info)
  orbital --timeout 30s script.js     Kill after 30 seconds
  echo "runtime.log(1)" | orbital     Read from stdin
  orbital                             Start REPL

Network Permission Examples (Deno-compatible):
  orbital --allow-net script.js                    Allow all network access
  orbital --deny-net script.js                     Block all network access
  orbital --allow-net=example.com script.js        Allow only example.com
  orbital --allow-net=example.com:443 script.js    Allow only example.com on port 443
  orbital --allow-net --deny-net=10.0.0.0/8 s.js   Allow all except private network
  orbital -N script.js                             Short form: allow all network

Environment Variables:
  NODE_PATH                         Additional module search paths
  NODE_REPL_HISTORY                 Path to REPL history file
`, orbitalVersion)
}

// esmLoader is the global ESM module loader (initialized after runtime creation)
var esmLoader *esm.ESM

func createRuntime() (*runtime.Runtime, error) {
	cfg := runtime.DefaultConfig()

	// Set up filesystem with optional sandboxing
	if documentRoot != "" {
		cfg.Filesystem = runtime.NewLocalFilesystem(documentRoot)
		cfg.DocumentRoot = documentRoot
	}

	// Set up execution timeout
	if executionTimeout > 0 {
		cfg.Timeout = executionTimeout
	}

	// Set up system info sandboxing
	if sandboxMode {
		cfg.SystemInfo = runtime.NewSandboxedSystemInfo(nil)
		cfg.HTTPClient = runtime.NewNoOpHTTPClient()
		cfg.Environment = runtime.NewSandboxedEnvironmentWithDefaults()
		cfg.DNSResolver = runtime.NewSandboxedResolver()
		cfg.SocketFactory = runtime.NewNoOpSocketFactory()
		cfg.ProcessSpawner = runtime.NewNoOpProcessSpawner()
	}

	// Set up network policy (Deno-style --allow-net / --deny-net)
	if allowNet || len(allowNetHosts) > 0 || len(denyNetHosts) > 0 {
		policy, err := buildNetworkPolicy()
		if err != nil {
			return nil, fmt.Errorf("invalid network policy: %w", err)
		}

		// Apply policy to socket factory
		if cfg.SocketFactory == nil {
			cfg.SocketFactory = runtime.NewRealSocketFactory()
		}
		// Only wrap if not already a NoOp factory (sandbox mode takes precedence)
		if _, isNoOp := cfg.SocketFactory.(*runtime.NoOpSocketFactory); !isNoOp {
			cfg.SocketFactory = runtime.NewFilteredSocketFactory(cfg.SocketFactory, policy)
		}

		// Apply policy to HTTP client
		if cfg.HTTPClient == nil {
			cfg.HTTPClient = runtime.NewRealHTTPClient()
		}
		// Only wrap if not already a NoOp client
		if _, isNoOp := cfg.HTTPClient.(*runtime.NoOpHTTPClient); !isNoOp {
			cfg.HTTPClient = runtime.NewFilteredHTTPClient(cfg.HTTPClient, policy)
		}
	}

	rt, err := runtime.New(cfg)
	if err != nil {
		return nil, err
	}

	// Create ESM loader
	esmLoader = esm.New()

	// Register modules (order matters - module system must be last)
	modules := []runtime.Module{
		abort.New(), // AbortController/AbortSignal globals - early for other modules
		console.New(),
		timers.New(),
		events.New(),
		process.New(),
		fs.New(),
		path.New(),
		buffer.New(),
		stream.New(),
		webstream.New(), // Web Streams API
		url.New(),
		orbitalos.New(),
		util.New(),
		crypto.New(),
		webcrypto.New(), // Web Crypto API (must come after crypto)
		net.New(),       // TCP/IPC sockets
		dgram.New(),     // UDP sockets
		tls.New(),       // TLS/SSL (must come after net)
		http.New(),
		https.New(), // Must come after http
		http2.New(), // HTTP/2 (must come after tls)
		string_decoder.New(),
		querystring.New(),
		assert.New(),
		zlib.New(),
		dns.New(),
		readlinemod.New(),
		fetch.New(),               // Web Fetch API
		perf_hooks.New(),          // Performance hooks
		punycode.New(),            // Punycode (deprecated)
		sys.New(),                 // Sys (deprecated, must come after util)
		diagnostics_channel.New(), // Diagnostics channel
		domain.New(),              // Domain (deprecated, must come after events)
		replmod.New(),             // REPL module (must come after events and util)
		test.New(),                // Test runner (must come after events)
		child_process.New(),       // Child process spawning
		esmLoader,                 // ES Module system
		module.New(),              // CommonJS module system - must be last
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
	// If document root is set, resolve filename relative to it
	resolvedPath := filename
	if documentRoot != "" && !filepath.IsAbs(filename) {
		resolvedPath = filepath.Join(documentRoot, filename)
	}

	// Get absolute path for file reading
	absPath, err := filepath.Abs(resolvedPath)
	if err != nil {
		absPath = resolvedPath
	}

	// Compute the path to expose to JS (__filename, __dirname)
	// If document root is set, make paths relative to it
	jsFilename := absPath
	jsDirname := filepath.Dir(absPath)

	if documentRoot != "" {
		absRoot, err := filepath.Abs(documentRoot)
		if err == nil {
			// Strip the root prefix to get the sandboxed path
			if strings.HasPrefix(absPath, absRoot) {
				relPath := strings.TrimPrefix(absPath, absRoot)
				if relPath == "" {
					relPath = "/"
				} else if !strings.HasPrefix(relPath, "/") {
					relPath = "/" + relPath
				}
				jsFilename = relPath
				jsDirname = filepath.Dir(relPath)
				if jsDirname == "." {
					jsDirname = "/"
				}
			}
		}
	}

	// Check if this is an ES module (.mjs extension)
	if strings.HasSuffix(filename, ".mjs") {
		return runESModule(absPath)
	}

	source, err := os.ReadFile(resolvedPath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	return runCodeWithPath(string(source), filename, jsFilename, jsDirname)
}

func runESModule(filename string) error {
	// If document root is set, resolve filename relative to it
	resolvedPath := filename
	if documentRoot != "" && !filepath.IsAbs(filename) {
		resolvedPath = filepath.Join(documentRoot, filename)
	}

	// Get absolute path
	absPath, err := filepath.Abs(resolvedPath)
	if err != nil {
		absPath = resolvedPath
	}

	rt, err := createRuntime()
	if err != nil {
		return err
	}
	defer rt.Dispose()

	// Run the ES module
	_, err = esmLoader.RunModuleFile(absPath)
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

	// Check for ES module input type
	if inputType == "module" {
		return runESModuleSource(string(source), "[stdin]")
	}

	return runCode(string(source), "[stdin]")
}

// checkSyntax validates JavaScript syntax without executing
func checkSyntax(filename string) error {
	source, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	rt, err := createRuntime()
	if err != nil {
		return err
	}
	defer rt.Dispose()

	// Use V8's syntax check by wrapping in a function (doesn't execute)
	checkCode := fmt.Sprintf("(function(){\n%s\n})", string(source))
	_, err = rt.RunScript(checkCode, filename)
	if err != nil {
		return fmt.Errorf("syntax error: %w", err)
	}

	fmt.Printf("Syntax OK: %s\n", filename)
	return nil
}

// runCodeAndReturn executes code and returns the result as a string
func runCodeAndReturn(source, origin string) (string, error) {
	rt, err := createRuntime()
	if err != nil {
		return "", err
	}
	defer rt.Dispose()

	// Preload required modules
	for _, mod := range requireModules {
		requireCode := fmt.Sprintf("require(%q);", mod)
		if _, err := rt.RunScript(requireCode, "[require]"); err != nil {
			return "", fmt.Errorf("failed to require %s: %w", mod, err)
		}
	}

	result, err := rt.Run(source, origin)
	if err != nil {
		return "", fmt.Errorf("script error: %w", err)
	}

	if result != nil && !result.IsUndefined() {
		return result.String(), nil
	}
	return "", nil
}

// runESModuleSource runs source code as an ES module
func runESModuleSource(source, origin string) error {
	rt, err := createRuntime()
	if err != nil {
		return err
	}
	defer rt.Dispose()

	// For stdin ES modules, we need to create a virtual module
	// This is a simplified approach - write to temp file and run
	tmpFile, err := os.CreateTemp("", "orbital-stdin-*.mjs")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(source); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	_, err = esmLoader.RunModuleFile(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("module error: %w", err)
	}

	rt.EventLoop().Run()
	return nil
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

	// Set runtime.title if specified
	if processTitle != "" {
		titleCode := fmt.Sprintf("runtime.title = %q;", processTitle)
		if _, err := rt.RunScript(titleCode, "[title]"); err != nil {
			// Non-fatal
			if !silenceWarnings {
				fmt.Fprintf(os.Stderr, "Warning: failed to set runtime.title: %v\n", err)
			}
		}
	}

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

	// Preload required modules
	for _, mod := range requireModules {
		requireCode := fmt.Sprintf("require(%q);", mod)
		if _, err := rt.RunScript(requireCode, "[require]"); err != nil {
			return fmt.Errorf("failed to require %s: %w", mod, err)
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
	fmt.Println("Orbital JavaScript Runtime v0.1.0")
	fmt.Println("Type .help for help, .exit to quit")
	fmt.Println()

	rt, err := createRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create runtime: %v\n", err)
		os.Exit(1)
	}
	defer rt.Dispose()

	// Set up readline with history
	historyFile := filepath.Join(os.TempDir(), ".orbital_history")
	if home, err := os.UserHomeDir(); err == nil {
		historyFile = filepath.Join(home, ".orbital_history")
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
