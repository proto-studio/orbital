// Example: Native Go modules with ESM
//
// This demonstrates using native Go modules from ES modules via import.
//
// Run with: go run examples/native/esm-demo.go
package main

import (
	"fmt"
	"os"

	"github.com/andrewcurioso/gnode/pkg/nodejs/buffer"
	"github.com/andrewcurioso/gnode/pkg/nodejs/console"
	"github.com/andrewcurioso/gnode/pkg/nodejs/esm"
	"github.com/andrewcurioso/gnode/pkg/nodejs/events"
	"github.com/andrewcurioso/gnode/pkg/nodejs/fs"
	"github.com/andrewcurioso/gnode/pkg/nodejs/module"
	gnodeos "github.com/andrewcurioso/gnode/pkg/nodejs/os"
	"github.com/andrewcurioso/gnode/pkg/nodejs/path"
	"github.com/andrewcurioso/gnode/pkg/nodejs/process"
	"github.com/andrewcurioso/gnode/pkg/nodejs/timers"
	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

func main() {
	// Create runtime
	rt, err := runtime.New(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create runtime: %v\n", err)
		os.Exit(1)
	}
	defer rt.Dispose()

	// Create ESM loader
	esmLoader := esm.New()

	// Register standard modules
	modules := []runtime.Module{
		console.New(),
		timers.New(),
		events.New(),
		process.New(),
		fs.New(),
		path.New(),
		buffer.New(),
		gnodeos.New(),
		esmLoader,
		module.New(),
	}

	for _, mod := range modules {
		if err := rt.RegisterModule(mod); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to register module: %v\n", err)
			os.Exit(1)
		}
	}

	// Register a native "database" module (simulated)
	if err := registerDatabaseModule(rt); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to register database module: %v\n", err)
		os.Exit(1)
	}

	// Run ES module code that imports the native module
	script := `
		// This is ES Module code
		console.log('=== Native Go Modules with ESM ===\\n');

		// Import native Go module
		import database from 'database';

		console.log('Connecting to database...');
		const result = database.connect('localhost:5432');
		console.log('Connection result:', result);

		console.log('');
		console.log('Querying users table...');
		const users = database.query('SELECT * FROM users');
		console.log('Users:', JSON.stringify(users, null, 2));

		console.log('');
		console.log('Database version:', database.version);
		console.log('Supported drivers:', database.drivers);

		console.log('\\n=== ESM Native Module Demo Complete ===');
	`

	_, err = esmLoader.RunModule(script, "esm-demo.mjs")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Script error: %v\n", err)
		os.Exit(1)
	}
}

// registerDatabaseModule creates a simulated database module
func registerDatabaseModule(rt *runtime.Runtime) error {
	ctx := rt.Context()
	iso := rt.Isolate()

	moduleObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Add connect function
	connectFn, _ := iso.NewFunctionTemplate(func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ctx := info.Context()
		connectionString := "localhost"
		if len(info.Args()) > 0 {
			connectionString = info.Args()[0].String()
		}

		// Simulate connection
		result, _ := ctx.NewObject()
		successVal, _ := ctx.NewString("connected")
		result.Set("status", successVal)
		hostVal, _ := ctx.NewString(connectionString)
		result.Set("host", hostVal)

		return result
	})
	connectVal, _ := connectFn.GetFunction(ctx)
	moduleObj.Set("connect", connectVal)

	// Add query function
	queryFn, _ := iso.NewFunctionTemplate(func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ctx := info.Context()

		// Return simulated query results
		results, _ := ctx.NewArray(2)

		user1, _ := ctx.NewObject()
		id1Val, _ := ctx.NewString("1")
		user1.Set("id", id1Val)
		name1Val, _ := ctx.NewString("Alice")
		user1.Set("name", name1Val)
		email1Val, _ := ctx.NewString("alice@example.com")
		user1.Set("email", email1Val)
		results.SetIndex(0, user1)

		user2, _ := ctx.NewObject()
		id2Val, _ := ctx.NewString("2")
		user2.Set("id", id2Val)
		name2Val, _ := ctx.NewString("Bob")
		user2.Set("name", name2Val)
		email2Val, _ := ctx.NewString("bob@example.com")
		user2.Set("email", email2Val)
		results.SetIndex(1, user2)

		return results
	})
	queryVal, _ := queryFn.GetFunction(ctx)
	moduleObj.Set("query", queryVal)

	// Add version
	versionVal, _ := ctx.NewString("2.0.0")
	moduleObj.Set("version", versionVal)

	// Add supported drivers array
	drivers, _ := ctx.NewArray(3)
	pg, _ := ctx.NewString("postgres")
	drivers.SetIndex(0, pg)
	mysql, _ := ctx.NewString("mysql")
	drivers.SetIndex(1, mysql)
	sqlite, _ := ctx.NewString("sqlite")
	drivers.SetIndex(2, sqlite)
	moduleObj.Set("drivers", drivers)

	return rt.RegisterNativeModule("database", moduleObj)
}
