// Package console implements the Node.js console module.
package console

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

// Console provides console logging functionality.
type Console struct {
	stdout     io.Writer
	stderr     io.Writer
	timers     map[string]time.Time
	counts     map[string]int
	groupDepth int
}

// New creates a new Console module.
func New() *Console {
	return &Console{
		stdout: os.Stdout,
		stderr: os.Stderr,
		timers: make(map[string]time.Time),
		counts: make(map[string]int),
	}
}

// NewWithWriters creates a Console with custom writers.
func NewWithWriters(stdout, stderr io.Writer) *Console {
	return &Console{
		stdout: stdout,
		stderr: stderr,
		timers: make(map[string]time.Time),
		counts: make(map[string]int),
	}
}

// Name returns the module name.
func (c *Console) Name() string {
	return "console"
}

// Register sets up the console global object.
func (c *Console) Register(rt *runtime.Runtime) error {
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create console object
	consoleObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// console.log
	logFn, err := iso.NewFunctionTemplate(c.createLogFunc(c.stdout, ""))
	if err != nil {
		return err
	}
	logVal, err := logFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("log", logVal); err != nil {
		return err
	}

	// console.info (alias for log)
	if err := consoleObj.Set("info", logVal); err != nil {
		return err
	}

	// console.debug (alias for log)
	if err := consoleObj.Set("debug", logVal); err != nil {
		return err
	}

	// console.warn
	warnFn, err := iso.NewFunctionTemplate(c.createLogFunc(c.stderr, "Warning: "))
	if err != nil {
		return err
	}
	warnVal, err := warnFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("warn", warnVal); err != nil {
		return err
	}

	// console.error
	errorFn, err := iso.NewFunctionTemplate(c.createLogFunc(c.stderr, "Error: "))
	if err != nil {
		return err
	}
	errorVal, err := errorFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("error", errorVal); err != nil {
		return err
	}

	// console.assert
	assertFn, err := iso.NewFunctionTemplate(c.assertFunc)
	if err != nil {
		return err
	}
	assertVal, err := assertFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("assert", assertVal); err != nil {
		return err
	}

	// console.clear
	clearFn, err := iso.NewFunctionTemplate(c.clearFunc)
	if err != nil {
		return err
	}
	clearVal, err := clearFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("clear", clearVal); err != nil {
		return err
	}

	// console.count
	countFn, err := iso.NewFunctionTemplate(c.countFunc)
	if err != nil {
		return err
	}
	countVal, err := countFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("count", countVal); err != nil {
		return err
	}

	// console.countReset
	countResetFn, err := iso.NewFunctionTemplate(c.countResetFunc)
	if err != nil {
		return err
	}
	countResetVal, err := countResetFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("countReset", countResetVal); err != nil {
		return err
	}

	// console.time
	timeFn, err := iso.NewFunctionTemplate(c.timeFunc)
	if err != nil {
		return err
	}
	timeVal, err := timeFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("time", timeVal); err != nil {
		return err
	}

	// console.timeEnd
	timeEndFn, err := iso.NewFunctionTemplate(c.timeEndFunc)
	if err != nil {
		return err
	}
	timeEndVal, err := timeEndFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("timeEnd", timeEndVal); err != nil {
		return err
	}

	// console.timeLog
	timeLogFn, err := iso.NewFunctionTemplate(c.timeLogFunc)
	if err != nil {
		return err
	}
	timeLogVal, err := timeLogFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("timeLog", timeLogVal); err != nil {
		return err
	}

	// console.trace
	traceFn, err := iso.NewFunctionTemplate(c.traceFunc)
	if err != nil {
		return err
	}
	traceVal, err := traceFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("trace", traceVal); err != nil {
		return err
	}

	// console.table
	tableFn, err := iso.NewFunctionTemplate(c.tableFunc)
	if err != nil {
		return err
	}
	tableVal, err := tableFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("table", tableVal); err != nil {
		return err
	}

	// console.dir
	dirFn, err := iso.NewFunctionTemplate(c.dirFunc)
	if err != nil {
		return err
	}
	dirVal, err := dirFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("dir", dirVal); err != nil {
		return err
	}

	// console.dirxml (alias for log in non-browser)
	if err := consoleObj.Set("dirxml", logVal); err != nil {
		return err
	}

	// console.group
	groupFn, err := iso.NewFunctionTemplate(c.groupFunc)
	if err != nil {
		return err
	}
	groupVal, err := groupFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("group", groupVal); err != nil {
		return err
	}

	// console.groupCollapsed (same as group in terminal)
	if err := consoleObj.Set("groupCollapsed", groupVal); err != nil {
		return err
	}

	// console.groupEnd
	groupEndFn, err := iso.NewFunctionTemplate(c.groupEndFunc)
	if err != nil {
		return err
	}
	groupEndVal, err := groupEndFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("groupEnd", groupEndVal); err != nil {
		return err
	}

	// console.profile (no-op stub)
	profileFn, err := iso.NewFunctionTemplate(c.profileFunc)
	if err != nil {
		return err
	}
	profileVal, err := profileFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("profile", profileVal); err != nil {
		return err
	}

	// console.profileEnd (no-op stub)
	profileEndFn, err := iso.NewFunctionTemplate(c.profileEndFunc)
	if err != nil {
		return err
	}
	profileEndVal, err := profileEndFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("profileEnd", profileEndVal); err != nil {
		return err
	}

	// console.timeStamp (no-op stub)
	timeStampFn, err := iso.NewFunctionTemplate(c.timeStampFunc)
	if err != nil {
		return err
	}
	timeStampVal, err := timeStampFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("timeStamp", timeStampVal); err != nil {
		return err
	}

	// Set console as global
	return rt.SetGlobal("console", consoleObj)
}

// createLogFunc creates a logging function with the given writer and prefix.
func (c *Console) createLogFunc(w io.Writer, prefix string) v8go.FunctionCallback {
	return func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		args := info.Args()
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = formatValue(arg)
		}
		indent := strings.Repeat("  ", c.groupDepth)
		fmt.Fprintln(w, indent+prefix+strings.Join(parts, " "))
		return nil
	}
}

// assertFunc implements console.assert.
func (c *Console) assertFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) == 0 {
		return nil
	}

	if !args[0].Boolean() {
		parts := []string{"Assertion failed:"}
		for i := 1; i < len(args); i++ {
			parts = append(parts, formatValue(args[i]))
		}
		if len(parts) == 1 {
			parts = append(parts, "console.assert")
		}
		fmt.Fprintln(c.stderr, strings.Join(parts, " "))
	}
	return nil
}

// clearFunc implements console.clear.
func (c *Console) clearFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// Send ANSI escape code to clear terminal
	fmt.Fprint(c.stdout, "\033[2J\033[H")
	return nil
}

// countFunc implements console.count.
func (c *Console) countFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	c.counts[label]++
	fmt.Fprintf(c.stdout, "%s: %d\n", label, c.counts[label])
	return nil
}

// countResetFunc implements console.countReset.
func (c *Console) countResetFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	if _, exists := c.counts[label]; exists {
		delete(c.counts, label)
	} else {
		fmt.Fprintf(c.stderr, "Warning: Count for '%s' does not exist\n", label)
	}
	return nil
}

// timeFunc implements console.time.
func (c *Console) timeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	if _, exists := c.timers[label]; exists {
		fmt.Fprintf(c.stderr, "Warning: Label '%s' already exists for console.time()\n", label)
		return nil
	}

	c.timers[label] = time.Now()
	return nil
}

// timeEndFunc implements console.timeEnd.
func (c *Console) timeEndFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	start, exists := c.timers[label]
	if !exists {
		fmt.Fprintf(c.stderr, "Warning: No such label '%s' for console.timeEnd()\n", label)
		return nil
	}

	elapsed := time.Since(start)
	delete(c.timers, label)
	fmt.Fprintf(c.stdout, "%s: %vms\n", label, elapsed.Milliseconds())
	return nil
}

// timeLogFunc implements console.timeLog.
func (c *Console) timeLogFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	label := "default"
	if len(args) > 0 {
		label = args[0].String()
	}

	start, exists := c.timers[label]
	if !exists {
		fmt.Fprintf(c.stderr, "Warning: No such label '%s' for console.timeLog()\n", label)
		return nil
	}

	elapsed := time.Since(start)
	parts := []string{fmt.Sprintf("%s: %vms", label, elapsed.Milliseconds())}
	for i := 1; i < len(args); i++ {
		parts = append(parts, formatValue(args[i]))
	}
	fmt.Fprintln(c.stdout, strings.Join(parts, " "))
	return nil
}

// traceFunc implements console.trace.
func (c *Console) traceFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	indent := strings.Repeat("  ", c.groupDepth)
	parts := []string{"Trace:"}
	for _, arg := range args {
		parts = append(parts, formatValue(arg))
	}
	fmt.Fprintln(c.stderr, indent+strings.Join(parts, " "))
	// Note: In a full implementation, we'd capture the JS stack trace here
	return nil
}

// tableFunc implements console.table.
// This is a simplified implementation that formats tables in text.
func (c *Console) tableFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) == 0 {
		return nil
	}

	data := args[0]
	indent := strings.Repeat("  ", c.groupDepth)

	// Handle arrays of primitives
	if data.IsArray() {
		length := data.ArrayLength()
		if length == 0 {
			fmt.Fprintln(c.stdout, indent+"┌─────────┬────────┐")
			fmt.Fprintln(c.stdout, indent+"│ (index) │ Values │")
			fmt.Fprintln(c.stdout, indent+"├─────────┼────────┤")
			fmt.Fprintln(c.stdout, indent+"└─────────┴────────┘")
			return nil
		}

		// Collect values and determine max widths
		values := make([]string, length)
		maxIdxWidth := 7 // "(index)"
		maxValWidth := 6 // "Values"

		for i := 0; i < length; i++ {
			elem, err := data.GetIndex(i)
			if err != nil || elem == nil {
				values[i] = "undefined"
			} else {
				values[i] = formatValue(elem)
			}
			idxStr := fmt.Sprintf("%d", i)
			if len(idxStr) > maxIdxWidth {
				maxIdxWidth = len(idxStr)
			}
			if len(values[i]) > maxValWidth {
				maxValWidth = len(values[i])
			}
		}

		// Print table
		fmt.Fprintf(c.stdout, "%s┌─%s─┬─%s─┐\n", indent, strings.Repeat("─", maxIdxWidth), strings.Repeat("─", maxValWidth))
		fmt.Fprintf(c.stdout, "%s│ %s │ %s │\n", indent, padRight("(index)", maxIdxWidth), padRight("Values", maxValWidth))
		fmt.Fprintf(c.stdout, "%s├─%s─┼─%s─┤\n", indent, strings.Repeat("─", maxIdxWidth), strings.Repeat("─", maxValWidth))

		for i, val := range values {
			fmt.Fprintf(c.stdout, "%s│ %s │ %s │\n", indent, padRight(fmt.Sprintf("%d", i), maxIdxWidth), padRight(val, maxValWidth))
		}

		fmt.Fprintf(c.stdout, "%s└─%s─┴─%s─┘\n", indent, strings.Repeat("─", maxIdxWidth), strings.Repeat("─", maxValWidth))
		return nil
	}

	// For objects, just use a formatted output
	if data.IsObject() {
		fmt.Fprintln(c.stdout, indent+formatValue(data))
		return nil
	}

	// Fallback
	fmt.Fprintln(c.stdout, indent+formatValue(data))
	return nil
}

// dirFunc implements console.dir.
func (c *Console) dirFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) == 0 {
		return nil
	}

	indent := strings.Repeat("  ", c.groupDepth)
	// For now, just format and print the object
	fmt.Fprintln(c.stdout, indent+formatValueDeep(args[0], 2))
	return nil
}

// groupFunc implements console.group.
func (c *Console) groupFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	indent := strings.Repeat("  ", c.groupDepth)

	if len(args) > 0 {
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = formatValue(arg)
		}
		fmt.Fprintln(c.stdout, indent+strings.Join(parts, " "))
	}

	c.groupDepth++
	return nil
}

// groupEndFunc implements console.groupEnd.
func (c *Console) groupEndFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	if c.groupDepth > 0 {
		c.groupDepth--
	}
	return nil
}

// profileFunc implements console.profile (no-op).
func (c *Console) profileFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// No-op in non-browser environment
	return nil
}

// profileEndFunc implements console.profileEnd (no-op).
func (c *Console) profileEndFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// No-op in non-browser environment
	return nil
}

// timeStampFunc implements console.timeStamp (no-op).
func (c *Console) timeStampFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// No-op in non-browser environment
	return nil
}

// padRight pads a string to the right with spaces.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// formatValueDeep formats a value with depth control.
func formatValueDeep(v *v8go.Value, depth int) string {
	if v == nil {
		return "undefined"
	}
	if v.IsUndefined() {
		return "undefined"
	}
	if v.IsNull() {
		return "null"
	}
	if depth <= 0 {
		if v.IsArray() {
			return "[Array]"
		}
		if v.IsObject() {
			return "[Object]"
		}
	}
	if v.IsArray() {
		return formatArrayDeep(v, depth)
	}
	if v.IsObject() && !v.IsFunction() {
		return formatObjectDeep(v, depth)
	}
	return v.String()
}

func formatArrayDeep(v *v8go.Value, depth int) string {
	length := v.ArrayLength()
	if length == 0 {
		return "[]"
	}

	parts := make([]string, length)
	for i := 0; i < length; i++ {
		elem, err := v.GetIndex(i)
		if err != nil || elem == nil {
			parts[i] = "undefined"
		} else {
			parts[i] = formatValueDeep(elem, depth-1)
		}
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

func formatObjectDeep(v *v8go.Value, depth int) string {
	// Without GetPropertyNames, we just use the string representation
	return v.String()
}

// formatValue converts a V8 value to a string representation.
func formatValue(v *v8go.Value) string {
	if v == nil {
		return "undefined"
	}
	if v.IsUndefined() {
		return "undefined"
	}
	if v.IsNull() {
		return "null"
	}
	if v.IsArray() {
		return formatArray(v)
	}
	if v.IsObject() && !v.IsFunction() {
		return formatObject(v)
	}
	return v.String()
}

// formatArray formats an array value.
func formatArray(v *v8go.Value) string {
	length := v.ArrayLength()
	if length == 0 {
		return "[]"
	}

	parts := make([]string, length)
	for i := 0; i < length; i++ {
		elem, err := v.GetIndex(i)
		if err != nil || elem == nil {
			parts[i] = "undefined"
		} else {
			parts[i] = formatValue(elem)
		}
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

// formatObject formats an object value (basic implementation).
func formatObject(v *v8go.Value) string {
	// Basic object stringification
	// A full implementation would enumerate properties
	return v.String()
}
