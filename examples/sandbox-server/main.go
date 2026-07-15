// Sandbox Server Example
//
// This server executes JavaScript code in isolated sandboxes.
// Each request gets its own temporary directory as the filesystem root.
//
// Features:
// - Isolated filesystem per request
// - Execution timeout (default 30 seconds)
// - Kill switch support
// - All network/process operations blocked
//
// Run with: go run main.go
// Test with: curl -X POST -d @test.js http://localhost:8081/run
//
// Temp directories are NOT deleted so you can inspect them after.

package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"proto.zip/studio/orbital/internal/nodejs/abort"
	"proto.zip/studio/orbital/internal/nodejs/assert"
	"proto.zip/studio/orbital/internal/nodejs/buffer"
	"proto.zip/studio/orbital/internal/nodejs/child_process"
	"proto.zip/studio/orbital/internal/nodejs/console"
	"proto.zip/studio/orbital/internal/nodejs/crypto"
	"proto.zip/studio/orbital/internal/nodejs/events"
	"proto.zip/studio/orbital/internal/nodejs/fs"
	"proto.zip/studio/orbital/internal/nodejs/module"
	orbitalos "proto.zip/studio/orbital/internal/nodejs/os"
	"proto.zip/studio/orbital/internal/nodejs/path"
	"proto.zip/studio/orbital/internal/nodejs/process"
	"proto.zip/studio/orbital/internal/nodejs/stream"
	"proto.zip/studio/orbital/internal/nodejs/timers"
	"proto.zip/studio/orbital/internal/nodejs/url"
	"proto.zip/studio/orbital/internal/nodejs/util"
	"proto.zip/studio/orbital/pkg/runtime"
)

var requestCounter uint64
var tempBaseDir string
var activeRuntimes = struct {
	sync.RWMutex
	m map[uint64]*runtime.Runtime
}{m: make(map[uint64]*runtime.Runtime)}

// Default execution timeout
const defaultTimeout = 30 * time.Second

func main() {
	// Create base temp directory for all sandboxes
	var err error
	tempBaseDir, err = os.MkdirTemp("", "orbital-sandbox-server-")
	if err != nil {
		log.Fatal("Failed to create temp base directory:", err)
	}

	log.Printf("Sandbox base directory: %s", tempBaseDir)
	log.Printf("NOTE: Temp directories will NOT be deleted for inspection")

	http.HandleFunc("/run", handleRun)
	http.HandleFunc("/kill/", handleKill)
	http.HandleFunc("/active", handleActive)
	http.HandleFunc("/", handleIndex)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	log.Printf("Sandbox server listening on http://localhost:%s", port)
	log.Println("POST JavaScript code to /run to execute it")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Sandbox Server</title></head>
<body>
<h1>Sandbox Server</h1>
<p>POST JavaScript code to <code>/run</code> to execute it in an isolated sandbox.</p>
<p>Each request gets its own filesystem root directory.</p>
<p>Default timeout: %v</p>

<h2>Endpoints:</h2>
<ul>
<li><code>POST /run</code> - Execute JavaScript code</li>
<li><code>POST /kill/{id}</code> - Kill a running execution</li>
<li><code>GET /active</code> - List active executions</li>
</ul>

<h2>Test with curl:</h2>
<pre>
# Run code
curl -X POST -d @test.js http://localhost:8081/run

# Run with custom timeout (in seconds)
curl -X POST -H "X-Timeout: 5" -d 'while(true){}' http://localhost:8081/run

# Kill a running execution
curl -X POST http://localhost:8081/kill/1
</pre>

<h2>Sandbox base directory:</h2>
<pre>%s</pre>
</body>
</html>`, defaultTimeout, tempBaseDir)
}

func handleKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// Parse the request ID from the URL
	idStr := r.URL.Path[len("/kill/"):]
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid request ID", http.StatusBadRequest)
		return
	}

	// Find and kill the runtime
	activeRuntimes.RLock()
	rt, exists := activeRuntimes.m[id]
	activeRuntimes.RUnlock()

	if !exists {
		http.Error(w, "Request not found or already completed", http.StatusNotFound)
		return
	}

	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "killed via API"
	}

	rt.Kill(reason)
	log.Printf("Request %d: killed (%s)", id, reason)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status": "killed", "id": %d, "reason": %q}`, id, reason)
}

func handleActive(w http.ResponseWriter, r *http.Request) {
	activeRuntimes.RLock()
	defer activeRuntimes.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"active_count": %d, "active_ids": [`, len(activeRuntimes.m))
	
	first := true
	for id := range activeRuntimes.m {
		if !first {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%d", id)
		first = false
	}
	fmt.Fprint(w, "]}")
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// Read the JavaScript code
	code, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Parse custom timeout from header
	timeout := defaultTimeout
	if timeoutStr := r.Header.Get("X-Timeout"); timeoutStr != "" {
		if seconds, err := strconv.Atoi(timeoutStr); err == nil && seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}

	// Create a unique sandbox directory for this request
	requestID := atomic.AddUint64(&requestCounter, 1)
	sandboxDir := filepath.Join(tempBaseDir, fmt.Sprintf("request-%d-%d", requestID, time.Now().UnixNano()))
	
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		http.Error(w, "Failed to create sandbox directory", http.StatusInternalServerError)
		return
	}

	log.Printf("Request %d: sandbox at %s (timeout: %v)", requestID, sandboxDir, timeout)

	// Execute the code and capture output
	output, execErr, wasKilled, killReason := executeInSandbox(string(code), sandboxDir, requestID, timeout)

	// Return the output
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Sandbox-Dir", sandboxDir)
	w.Header().Set("X-Request-ID", fmt.Sprintf("%d", requestID))
	
	if wasKilled {
		w.Header().Set("X-Killed", "true")
		w.Header().Set("X-Kill-Reason", killReason)
	}
	
	if execErr != nil {
		w.WriteHeader(http.StatusOK) // Still return 200 so we see the output
		fmt.Fprintf(w, "%s\n\n--- ERROR ---\n%v\n", output, execErr)
	} else {
		fmt.Fprint(w, output)
	}
}

func executeInSandbox(code string, sandboxDir string, requestID uint64, timeout time.Duration) (output string, execErr error, wasKilled bool, killReason string) {
	// Create output buffer
	var outputBuf bytes.Buffer

	// Create runtime config with sandbox settings
	cfg := runtime.DefaultConfig()
	
	// Filesystem sandboxed to the unique directory
	cfg.Filesystem = runtime.NewLocalFilesystem(sandboxDir)
	cfg.DocumentRoot = sandboxDir
	
	// Set execution timeout
	cfg.Timeout = timeout
	
	// Full sandbox mode
	cfg.SystemInfo = runtime.NewSandboxedSystemInfo(nil)
	cfg.HTTPClient = runtime.NewNoOpHTTPClient()
	cfg.Environment = runtime.NewSandboxedEnvironmentWithDefaults()
	cfg.DNSResolver = runtime.NewSandboxedResolver()
	cfg.SocketFactory = runtime.NewNoOpSocketFactory()
	cfg.ProcessSpawner = runtime.NewNoOpProcessSpawner()

	// Create runtime
	rt, err := runtime.New(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create runtime: %w", err), false, ""
	}
	
	// Register runtime so it can be killed
	activeRuntimes.Lock()
	activeRuntimes.m[requestID] = rt
	activeRuntimes.Unlock()
	
	// Ensure cleanup on exit
	defer func() {
		// Remove from active runtimes
		activeRuntimes.Lock()
		delete(activeRuntimes.m, requestID)
		activeRuntimes.Unlock()
		
		// Check if it was killed
		wasKilled = rt.IsKilled()
		killReason = rt.KillReason()
		
		// Dispose runtime (closes all resources)
		rt.Dispose()
		
		log.Printf("Request %d: completed (killed=%v)", requestID, wasKilled)
	}()

	// Register modules with custom console that writes to our buffer
	modules := []runtime.Module{
		abort.New(),
		console.NewWithWriter(runtime.NewStandardWriter(&outputBuf, &outputBuf)),
		timers.New(),
		events.New(),
		process.New(),
		fs.New(),
		path.New(),
		buffer.New(),
		stream.New(),
		url.New(),
		orbitalos.New(),
		util.New(),
		crypto.New(),
		assert.New(),
		child_process.New(),
		module.New(),
	}

	for _, mod := range modules {
		if err := rt.RegisterModule(mod); err != nil {
			return "", fmt.Errorf("failed to register %s module: %w", mod.Name(), err), false, ""
		}
	}

	// Set up __filename and __dirname for sandboxed execution
	setupCode := `
		globalThis.__filename = '/script.js';
		globalThis.__dirname = '/';
		if (typeof module !== 'undefined') {
			module.filename = '/script.js';
			module.id = '/script.js';
		}
	`
	if _, err := rt.RunScript(setupCode, "setup.js"); err != nil {
		return outputBuf.String(), fmt.Errorf("setup error: %w", err), false, ""
	}

	// Run the user code
	_, err = rt.Run(code, "script.js")
	
	// Run event loop for any async operations
	rt.EventLoop().Run()

	return outputBuf.String(), err, false, ""
}
