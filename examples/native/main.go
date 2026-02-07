// Example: Registering a native Go module
//
// This demonstrates how to create and register a native Go module
// that can be required from JavaScript.
//
// Run with: go run examples/native/main.go
package main

import (
	"fmt"
	"math"
	"os"

	"proto.zip/studio/orbital/internal/nodejs/buffer"
	"proto.zip/studio/orbital/internal/nodejs/console"
	"proto.zip/studio/orbital/internal/nodejs/events"
	"proto.zip/studio/orbital/internal/nodejs/fs"
	"proto.zip/studio/orbital/internal/nodejs/module"
	gnodeos "proto.zip/studio/orbital/internal/nodejs/os"
	"proto.zip/studio/orbital/internal/nodejs/path"
	"proto.zip/studio/orbital/internal/nodejs/process"
	"proto.zip/studio/orbital/internal/nodejs/timers"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
)

func main() {
	// Create runtime with default config
	rt, err := runtime.New(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create runtime: %v\n", err)
		os.Exit(1)
	}
	defer rt.Dispose()

	// Register standard Node.js modules
	modules := []runtime.Module{
		console.New(),
		timers.New(),
		events.New(),
		process.New(),
		fs.New(),
		path.New(),
		buffer.New(),
		gnodeos.New(),
		module.New(),
	}

	for _, mod := range modules {
		if err := rt.RegisterModule(mod); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to register module: %v\n", err)
			os.Exit(1)
		}
	}

	// Create and register a native "hello" module
	if err := registerHelloModule(rt); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to register hello module: %v\n", err)
		os.Exit(1)
	}

	// Create and register a native "mathext" module with more functions
	if err := registerMathExtModule(rt); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to register mathext module: %v\n", err)
		os.Exit(1)
	}

	// Run JavaScript that uses our native modules
	script := `
		console.log('=== Native Go Module Demo ===\n');

		// Using the hello module
		const hello = require('hello');
		console.log('hello.greet("World"):', hello.greet('World'));
		console.log('hello.version:', hello.version);
		console.log('hello.config:', JSON.stringify(hello.config));

		console.log('');

		// Using the mathext module
		const mathext = require('mathext');
		console.log('mathext.factorial(5):', mathext.factorial(5));
		console.log('mathext.fibonacci(10):', mathext.fibonacci(10));
		console.log('mathext.isPrime(17):', mathext.isPrime(17));
		console.log('mathext.isPrime(18):', mathext.isPrime(18));
		console.log('mathext.gcd(48, 18):', mathext.gcd(48, 18));
		console.log('mathext.PI:', mathext.PI);
		console.log('mathext.E:', mathext.E);

		console.log('\n=== Demo Complete ===');
	`

	_, err = rt.Run(script, "demo.js")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Script error: %v\n", err)
		os.Exit(1)
	}
}

// registerHelloModule creates and registers a simple "hello" native module
func registerHelloModule(rt *runtime.Runtime) error {
	ctx := rt.Context()
	iso := rt.Isolate()

	// Create the module object
	moduleObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Add a "greet" function
	greetFn, err := iso.NewFunctionTemplate(func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ctx := info.Context()
		name := "stranger"
		if len(info.Args()) > 0 {
			name = info.Args()[0].String()
		}

		greeting := fmt.Sprintf("Hello, %s! (from Go)", name)
		result, _ := ctx.NewString(greeting)
		return result
	})
	if err != nil {
		return err
	}

	greetVal, err := greetFn.GetFunction(ctx)
	if err != nil {
		return err
	}

	if err := moduleObj.Set("greet", greetVal); err != nil {
		return err
	}

	// Add a "version" property
	versionVal, _ := ctx.NewString("1.0.0")
	if err := moduleObj.Set("version", versionVal); err != nil {
		return err
	}

	// Add a "config" object property
	configObj, _ := ctx.NewObject()
	configObj.Set("debug", ctx.NewBoolean(true))
	langVal, _ := ctx.NewString("en")
	configObj.Set("language", langVal)
	if err := moduleObj.Set("config", configObj); err != nil {
		return err
	}

	// Register the module
	return rt.RegisterNativeModule("hello", moduleObj)
}

// registerMathExtModule creates and registers a "mathext" native module
// with additional math functions not in standard JS Math
func registerMathExtModule(rt *runtime.Runtime) error {
	ctx := rt.Context()
	iso := rt.Isolate()

	moduleObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Add factorial function
	factorialFn, _ := iso.NewFunctionTemplate(func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ctx := info.Context()
		if len(info.Args()) == 0 {
			return ctx.NewNumber(1)
		}
		n := int(info.Args()[0].Integer())
		result := factorial(n)
		return ctx.NewNumber(float64(result))
	})
	factorialVal, _ := factorialFn.GetFunction(ctx)
	moduleObj.Set("factorial", factorialVal)

	// Add fibonacci function
	fibFn, _ := iso.NewFunctionTemplate(func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ctx := info.Context()
		if len(info.Args()) == 0 {
			return ctx.NewNumber(0)
		}
		n := int(info.Args()[0].Integer())
		result := fibonacci(n)
		return ctx.NewNumber(float64(result))
	})
	fibVal, _ := fibFn.GetFunction(ctx)
	moduleObj.Set("fibonacci", fibVal)

	// Add isPrime function
	isPrimeFn, _ := iso.NewFunctionTemplate(func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ctx := info.Context()
		if len(info.Args()) == 0 {
			return ctx.NewBoolean(false)
		}
		n := int(info.Args()[0].Integer())
		result := isPrime(n)
		return ctx.NewBoolean(result)
	})
	isPrimeVal, _ := isPrimeFn.GetFunction(ctx)
	moduleObj.Set("isPrime", isPrimeVal)

	// Add gcd function (greatest common divisor)
	gcdFn, _ := iso.NewFunctionTemplate(func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ctx := info.Context()
		if len(info.Args()) < 2 {
			return ctx.NewNumber(0)
		}
		a := int(info.Args()[0].Integer())
		b := int(info.Args()[1].Integer())
		result := gcd(a, b)
		return ctx.NewNumber(float64(result))
	})
	gcdVal, _ := gcdFn.GetFunction(ctx)
	moduleObj.Set("gcd", gcdVal)

	// Add constants
	moduleObj.Set("PI", ctx.NewNumber(math.Pi))
	moduleObj.Set("E", ctx.NewNumber(math.E))

	return rt.RegisterNativeModule("mathext", moduleObj)
}

// Helper functions for mathext module

func factorial(n int) int {
	if n <= 1 {
		return 1
	}
	return n * factorial(n-1)
}

func fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	a, b := 0, 1
	for i := 2; i <= n; i++ {
		a, b = b, a+b
	}
	return b
}

func isPrime(n int) bool {
	if n < 2 {
		return false
	}
	if n == 2 {
		return true
	}
	if n%2 == 0 {
		return false
	}
	for i := 3; i*i <= n; i += 2 {
		if n%i == 0 {
			return false
		}
	}
	return true
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
