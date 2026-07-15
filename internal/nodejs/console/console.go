// Package console implements the Node.js console module.
package console

import (
	"fmt"
	"strings"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// Console provides console logging functionality.
type Console struct {
	writer     runtime.Writer
	formatter  runtime.Formatter
	timers     map[string]time.Time
	counts     map[string]int
	groupDepth int
}

// New creates a new Console module with default settings.
// Colors are auto-detected based on terminal capabilities.
func New() *Console {
	return &Console{
		writer:    runtime.DefaultWriter(),
		formatter: runtime.AutoColorFormatter(),
		timers:    make(map[string]time.Time),
		counts:    make(map[string]int),
	}
}

// NewWithWriter creates a Console with a custom writer.
func NewWithWriter(w runtime.Writer) *Console {
	return &Console{
		writer:    w,
		formatter: runtime.AutoColorFormatter(),
		timers:    make(map[string]time.Time),
		counts:    make(map[string]int),
	}
}

// NewWithWriterAndFormatter creates a Console with custom writer and formatter.
func NewWithWriterAndFormatter(w runtime.Writer, f runtime.Formatter) *Console {
	return &Console{
		writer:    w,
		formatter: f,
		timers:    make(map[string]time.Time),
		counts:    make(map[string]int),
	}
}

// Writer returns the current writer.
func (c *Console) Writer() runtime.Writer {
	return c.writer
}

// Formatter returns the current formatter.
func (c *Console) Formatter() runtime.Formatter {
	return c.formatter
}

// SetWriter sets the console writer.
func (c *Console) SetWriter(w runtime.Writer) {
	c.writer = w
}

// SetFormatter sets the console formatter.
func (c *Console) SetFormatter(f runtime.Formatter) {
	c.formatter = f
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

	// runtime.log
	logFn, err := iso.NewFunctionTemplate(c.createLogFunc("log"))
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

	// runtime.info (alias for log)
	if err := consoleObj.Set("info", logVal); err != nil {
		return err
	}

	// runtime.debug (alias for log)
	if err := consoleObj.Set("debug", logVal); err != nil {
		return err
	}

	// runtime.warn
	warnFn, err := iso.NewFunctionTemplate(c.createLogFunc("warn"))
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

	// runtime.error
	errorFn, err := iso.NewFunctionTemplate(c.createLogFunc("error"))
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

	// runtime.assert
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

	// runtime.clear
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

	// runtime.count
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

	// runtime.countReset
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

	// runtime.time
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

	// runtime.timeEnd
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

	// runtime.timeLog
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

	// runtime.trace
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

	// runtime.table
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

	// runtime.dir
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

	// runtime.dirxml (alias for log in non-browser)
	if err := consoleObj.Set("dirxml", logVal); err != nil {
		return err
	}

	// runtime.group
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

	// runtime.groupCollapsed (same as group in terminal)
	if err := consoleObj.Set("groupCollapsed", groupVal); err != nil {
		return err
	}

	// runtime.groupEnd
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

	// runtime.profile (no-op stub)
	noopFn, err := iso.NewFunctionTemplate(c.noopFunc)
	if err != nil {
		return err
	}
	noopVal, err := noopFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := consoleObj.Set("profile", noopVal); err != nil {
		return err
	}
	if err := consoleObj.Set("profileEnd", noopVal); err != nil {
		return err
	}
	if err := consoleObj.Set("timeStamp", noopVal); err != nil {
		return err
	}

	// Set console as global
	return rt.SetGlobal("console", consoleObj)
}

// createLogFunc creates a logging function for the given level.
func (c *Console) createLogFunc(level string) v8.FunctionCallback {
	return func(info *v8.FunctionCallbackInfo) *v8.Value {
		args := info.Args()
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = c.formatValue(arg)
		}
		indent := strings.Repeat("  ", c.groupDepth)
		message := indent + strings.Join(parts, " ")

		switch level {
		case "warn":
			c.writer.Warn(message)
		case "error":
			c.writer.Error(message)
		default:
			c.writer.Log(message)
		}
		return nil
	}
}

// formatValue formats a v8.Value with type-based coloring.
func (c *Console) formatValue(v *v8.Value) string {
	if v == nil {
		return c.formatter.FormatValue("undefined", runtime.TypeUndefined)
	}
	if v.IsUndefined() {
		return c.formatter.FormatValue("undefined", runtime.TypeUndefined)
	}
	if v.IsNull() {
		return c.formatter.FormatValue("null", runtime.TypeNull)
	}
	if v.IsBoolean() {
		return c.formatter.FormatValue(v.String(), runtime.TypeBoolean)
	}
	if v.IsNumber() {
		return c.formatter.FormatValue(v.String(), runtime.TypeNumber)
	}
	if v.IsString() {
		// For runtime.log, strings are printed without quotes and without color
		// Only in inspect/dir mode do strings get quoted and colored
		return v.String()
	}
	if v.IsFunction() {
		return c.formatter.FormatValue("[Function]", runtime.TypeFunction)
	}
	if v.IsArray() {
		return c.formatArray(v)
	}
	if v.IsObject() {
		return c.formatObject(v)
	}
	return v.String()
}

// formatValueQuoted formats a value with quotes for strings (used in arrays/objects).
func (c *Console) formatValueQuoted(v *v8.Value) string {
	return c.formatValueQuotedWithDepth(v, 2)
}

// formatValueQuotedWithDepth formats a value with depth control.
func (c *Console) formatValueQuotedWithDepth(v *v8.Value, depth int) string {
	if v == nil {
		return c.formatter.FormatValue("undefined", runtime.TypeUndefined)
	}
	if v.IsUndefined() {
		return c.formatter.FormatValue("undefined", runtime.TypeUndefined)
	}
	if v.IsNull() {
		return c.formatter.FormatValue("null", runtime.TypeNull)
	}
	if v.IsBoolean() {
		return c.formatter.FormatValue(v.String(), runtime.TypeBoolean)
	}
	if v.IsNumber() {
		return c.formatter.FormatValue(v.String(), runtime.TypeNumber)
	}
	if v.IsString() {
		// In arrays/objects, strings are quoted and colored
		quoted := fmt.Sprintf("'%s'", v.String())
		return c.formatter.FormatValue(quoted, runtime.TypeString)
	}
	if v.IsFunction() {
		return c.formatter.FormatValue("[Function]", runtime.TypeFunction)
	}
	if v.IsArray() {
		return c.formatArrayWithDepth(v, depth-1)
	}
	if v.IsObject() {
		return c.formatObjectWithDepth(v, depth-1)
	}
	return v.String()
}

// formatArray formats an array value with colored elements.
func (c *Console) formatArray(v *v8.Value) string {
	return c.formatArrayWithDepth(v, 2)
}

// formatArrayWithDepth formats an array with depth limiting.
func (c *Console) formatArrayWithDepth(v *v8.Value, depth int) string {
	if depth <= 0 {
		return "[Array]"
	}

	length := v.ArrayLength()
	if length == 0 {
		return "[]"
	}

	parts := make([]string, length)
	for i := 0; i < length; i++ {
		elem, err := v.GetIndex(i)
		if err != nil || elem == nil {
			parts[i] = c.formatter.FormatValue("undefined", runtime.TypeUndefined)
		} else {
			parts[i] = c.formatValueQuotedWithDepth(elem, depth)
		}
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

// formatObject formats an object value.
func (c *Console) formatObject(v *v8.Value) string {
	return c.formatObjectWithDepth(v, 2)
}

// formatObjectWithDepth formats an object with depth limiting.
func (c *Console) formatObjectWithDepth(v *v8.Value, depth int) string {
	if depth <= 0 {
		return "[Object]"
	}

	// Get property names
	names, err := v.GetPropertyNames()
	if err != nil || names == nil {
		return "{}"
	}

	length := names.ArrayLength()
	if length == 0 {
		return "{}"
	}

	parts := make([]string, 0, length)
	for i := 0; i < length; i++ {
		keyVal, err := names.GetIndex(i)
		if err != nil || keyVal == nil {
			continue
		}
		key := keyVal.String()

		val, err := v.Get(key)
		if err != nil || val == nil {
			continue
		}

		var valStr string
		if val.IsUndefined() {
			valStr = c.formatter.FormatValue("undefined", runtime.TypeUndefined)
		} else if val.IsNull() {
			valStr = c.formatter.FormatValue("null", runtime.TypeNull)
		} else if val.IsBoolean() {
			valStr = c.formatter.FormatValue(val.String(), runtime.TypeBoolean)
		} else if val.IsNumber() {
			valStr = c.formatter.FormatValue(val.String(), runtime.TypeNumber)
		} else if val.IsString() {
			quoted := fmt.Sprintf("'%s'", val.String())
			valStr = c.formatter.FormatValue(quoted, runtime.TypeString)
		} else if val.IsFunction() {
			valStr = c.formatter.FormatValue("[Function]", runtime.TypeFunction)
		} else if val.IsArray() {
			valStr = c.formatArrayWithDepth(val, depth-1)
		} else if val.IsObject() {
			valStr = c.formatObjectWithDepth(val, depth-1)
		} else {
			valStr = val.String()
		}

		parts = append(parts, fmt.Sprintf("%s: %s", key, valStr))
	}

	if len(parts) == 0 {
		return "{}"
	}

	return "{ " + strings.Join(parts, ", ") + " }"
}

// assertFunc implements runtime.assert.
func (c *Console) assertFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) == 0 {
		return nil
	}

	if !args[0].Boolean() {
		parts := []string{"Assertion failed:"}
		for i := 1; i < len(args); i++ {
			parts = append(parts, c.formatValue(args[i]))
		}
		if len(parts) == 1 {
			parts = append(parts, "runtime.assert")
		}
		c.writer.Error(strings.Join(parts, " "))
	}
	return nil
}

// clearFunc implements runtime.clear.
func (c *Console) clearFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	c.writer.Clear()
	return nil
}

// countFunc implements runtime.count.
func (c *Console) countFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	c.counts[label]++
	c.writer.Log(fmt.Sprintf("%s: %s", label, c.formatter.FormatValue(c.counts[label], runtime.TypeNumber)))
	return nil
}

// countResetFunc implements runtime.countReset.
func (c *Console) countResetFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	if _, exists := c.counts[label]; exists {
		delete(c.counts, label)
	} else {
		c.writer.Warn(fmt.Sprintf("Warning: Count for '%s' does not exist", label))
	}
	return nil
}

// timeFunc implements runtime.time.
func (c *Console) timeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	if _, exists := c.timers[label]; exists {
		c.writer.Warn(fmt.Sprintf("Warning: Label '%s' already exists for runtime.time()", label))
		return nil
	}

	c.timers[label] = time.Now()
	return nil
}

// timeEndFunc implements runtime.timeEnd.
func (c *Console) timeEndFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	label := "default"
	if len(info.Args()) > 0 {
		label = info.Args()[0].String()
	}

	start, exists := c.timers[label]
	if !exists {
		c.writer.Warn(fmt.Sprintf("Warning: No such label '%s' for runtime.timeEnd()", label))
		return nil
	}

	elapsed := time.Since(start)
	delete(c.timers, label)
	c.writer.Log(fmt.Sprintf("%s: %sms", label, c.formatter.FormatValue(elapsed.Milliseconds(), runtime.TypeNumber)))
	return nil
}

// timeLogFunc implements runtime.timeLog.
func (c *Console) timeLogFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	label := "default"
	if len(args) > 0 {
		label = args[0].String()
	}

	start, exists := c.timers[label]
	if !exists {
		c.writer.Warn(fmt.Sprintf("Warning: No such label '%s' for runtime.timeLog()", label))
		return nil
	}

	elapsed := time.Since(start)
	parts := []string{fmt.Sprintf("%s: %sms", label, c.formatter.FormatValue(elapsed.Milliseconds(), runtime.TypeNumber))}
	for i := 1; i < len(args); i++ {
		parts = append(parts, c.formatValue(args[i]))
	}
	c.writer.Log(strings.Join(parts, " "))
	return nil
}

// traceFunc implements runtime.trace.
func (c *Console) traceFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	indent := strings.Repeat("  ", c.groupDepth)
	parts := []string{"Trace:"}
	for _, arg := range args {
		parts = append(parts, c.formatValue(arg))
	}
	c.writer.Error(indent + strings.Join(parts, " "))
	// Note: In a full implementation, we'd capture the JS stack trace here
	return nil
}

// tableFunc implements runtime.table.
func (c *Console) tableFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
			c.writer.Log(indent + "┌─────────┬────────┐")
			c.writer.Log(indent + "│ (index) │ Values │")
			c.writer.Log(indent + "├─────────┼────────┤")
			c.writer.Log(indent + "└─────────┴────────┘")
			return nil
		}

		// Collect values and determine max widths
		values := make([]string, length)
		maxIdxWidth := 7 // "(index)"
		maxValWidth := 6 // "Values"

		for i := 0; i < length; i++ {
			elem, err := data.GetIndex(i)
			if err != nil || elem == nil {
				values[i] = c.formatter.FormatValue("undefined", runtime.TypeUndefined)
			} else {
				values[i] = c.formatValueQuoted(elem)
			}
			idxStr := fmt.Sprintf("%d", i)
			// Need to strip ANSI codes for width calculation
			plainVal := stripAnsi(values[i])
			if len(idxStr) > maxIdxWidth {
				maxIdxWidth = len(idxStr)
			}
			if len(plainVal) > maxValWidth {
				maxValWidth = len(plainVal)
			}
		}

		// Print table
		c.writer.Log(fmt.Sprintf("%s┌─%s─┬─%s─┐", indent, strings.Repeat("─", maxIdxWidth), strings.Repeat("─", maxValWidth)))
		c.writer.Log(fmt.Sprintf("%s│ %s │ %s │", indent, padRight("(index)", maxIdxWidth), padRight("Values", maxValWidth)))
		c.writer.Log(fmt.Sprintf("%s├─%s─┼─%s─┤", indent, strings.Repeat("─", maxIdxWidth), strings.Repeat("─", maxValWidth)))

		for i, val := range values {
			idxStr := c.formatter.FormatValue(i, runtime.TypeNumber)
			plainIdx := stripAnsi(idxStr)
			plainVal := stripAnsi(val)
			// Pad based on plain text width
			idxPadded := idxStr + strings.Repeat(" ", maxIdxWidth-len(plainIdx))
			valPadded := val + strings.Repeat(" ", maxValWidth-len(plainVal))
			c.writer.Log(fmt.Sprintf("%s│ %s │ %s │", indent, idxPadded, valPadded))
		}

		c.writer.Log(fmt.Sprintf("%s└─%s─┴─%s─┘", indent, strings.Repeat("─", maxIdxWidth), strings.Repeat("─", maxValWidth)))
		return nil
	}

	// For objects, just use a formatted output
	c.writer.Log(indent + c.formatValue(data))
	return nil
}

// dirFunc implements runtime.dir.
func (c *Console) dirFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) == 0 {
		return nil
	}

	indent := strings.Repeat("  ", c.groupDepth)
	c.writer.Log(indent + c.formatValueDeep(args[0], 2))
	return nil
}

// groupFunc implements runtime.group.
func (c *Console) groupFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	indent := strings.Repeat("  ", c.groupDepth)

	if len(args) > 0 {
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = c.formatValue(arg)
		}
		c.writer.Log(indent + strings.Join(parts, " "))
	}

	c.groupDepth++
	return nil
}

// groupEndFunc implements runtime.groupEnd.
func (c *Console) groupEndFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	if c.groupDepth > 0 {
		c.groupDepth--
	}
	return nil
}

// noopFunc is a no-op stub for unimplemented console methods.
func (c *Console) noopFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return nil
}

// formatValueDeep formats a value with depth control.
func (c *Console) formatValueDeep(v *v8.Value, depth int) string {
	if v == nil {
		return c.formatter.FormatValue("undefined", runtime.TypeUndefined)
	}
	if v.IsUndefined() {
		return c.formatter.FormatValue("undefined", runtime.TypeUndefined)
	}
	if v.IsNull() {
		return c.formatter.FormatValue("null", runtime.TypeNull)
	}
	if depth <= 0 {
		if v.IsArray() {
			return "[Array]"
		}
		if v.IsObject() {
			return "[Object]"
		}
	}
	if v.IsBoolean() {
		return c.formatter.FormatValue(v.String(), runtime.TypeBoolean)
	}
	if v.IsNumber() {
		return c.formatter.FormatValue(v.String(), runtime.TypeNumber)
	}
	if v.IsString() {
		quoted := fmt.Sprintf("'%s'", v.String())
		return c.formatter.FormatValue(quoted, runtime.TypeString)
	}
	if v.IsFunction() {
		return c.formatter.FormatValue("[Function]", runtime.TypeFunction)
	}
	if v.IsArray() {
		return c.formatArrayDeep(v, depth)
	}
	if v.IsObject() {
		return c.formatObject(v)
	}
	return v.String()
}

func (c *Console) formatArrayDeep(v *v8.Value, depth int) string {
	length := v.ArrayLength()
	if length == 0 {
		return "[]"
	}

	parts := make([]string, length)
	for i := 0; i < length; i++ {
		elem, err := v.GetIndex(i)
		if err != nil || elem == nil {
			parts[i] = c.formatter.FormatValue("undefined", runtime.TypeUndefined)
		} else {
			parts[i] = c.formatValueDeep(elem, depth-1)
		}
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

// padRight pads a string to the right with spaces.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// stripAnsi removes ANSI escape codes from a string.
func stripAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
