package console

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// ANSI color codes
const (
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Dim       = "\033[2m"
	Italic    = "\033[3m"
	Underline = "\033[4m"

	// Foreground colors
	Black   = "\033[30m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	Gray    = "\033[90m"

	// Bright foreground colors
	BrightRed     = "\033[91m"
	BrightGreen   = "\033[92m"
	BrightYellow  = "\033[93m"
	BrightBlue    = "\033[94m"
	BrightMagenta = "\033[95m"
	BrightCyan    = "\033[96m"
	BrightWhite   = "\033[97m"
)

// Writer defines the interface for console output.
// Implement this interface to customize where and how console output is written.
type Writer interface {
	// Log writes a log message (console.log, console.info, console.debug)
	Log(message string)
	// Warn writes a warning message (console.warn)
	Warn(message string)
	// Error writes an error message (console.error)
	Error(message string)
	// Clear clears the console output
	Clear()
}

// Formatter defines the interface for formatting values.
// Implement this interface to customize how values are displayed.
type Formatter interface {
	// FormatValue formats a value for display
	FormatValue(value interface{}, valueType ValueType) string
	// ColorEnabled returns whether colors are enabled
	ColorEnabled() bool
}

// ValueType represents the type of a JavaScript value for formatting.
type ValueType int

const (
	TypeUndefined ValueType = iota
	TypeNull
	TypeBoolean
	TypeNumber
	TypeString
	TypeSymbol
	TypeFunction
	TypeArray
	TypeObject
	TypeDate
	TypeRegExp
	TypeError
	TypePromise
	TypeMap
	TypeSet
)

// StandardWriter implements Writer using io.Writer.
type StandardWriter struct {
	stdout io.Writer
	stderr io.Writer
}

// NewStandardWriter creates a standard writer with the given outputs.
func NewStandardWriter(stdout, stderr io.Writer) *StandardWriter {
	return &StandardWriter{
		stdout: stdout,
		stderr: stderr,
	}
}

// DefaultWriter creates a writer using os.Stdout and os.Stderr.
func DefaultWriter() *StandardWriter {
	return NewStandardWriter(os.Stdout, os.Stderr)
}

func (w *StandardWriter) Log(message string) {
	fmt.Fprintln(w.stdout, message)
}

func (w *StandardWriter) Warn(message string) {
	fmt.Fprintln(w.stderr, message)
}

func (w *StandardWriter) Error(message string) {
	fmt.Fprintln(w.stderr, message)
}

func (w *StandardWriter) Clear() {
	fmt.Fprint(w.stdout, "\033[2J\033[H")
}

// BufferedWriter captures console output for testing or integration.
type BufferedWriter struct {
	LogMessages   []string
	WarnMessages  []string
	ErrorMessages []string
	ClearCount    int
}

// NewBufferedWriter creates a new buffered writer.
func NewBufferedWriter() *BufferedWriter {
	return &BufferedWriter{
		LogMessages:   make([]string, 0),
		WarnMessages:  make([]string, 0),
		ErrorMessages: make([]string, 0),
	}
}

func (w *BufferedWriter) Log(message string) {
	w.LogMessages = append(w.LogMessages, message)
}

func (w *BufferedWriter) Warn(message string) {
	w.WarnMessages = append(w.WarnMessages, message)
}

func (w *BufferedWriter) Error(message string) {
	w.ErrorMessages = append(w.ErrorMessages, message)
}

func (w *BufferedWriter) Clear() {
	w.ClearCount++
}

// Reset clears all captured messages.
func (w *BufferedWriter) Reset() {
	w.LogMessages = w.LogMessages[:0]
	w.WarnMessages = w.WarnMessages[:0]
	w.ErrorMessages = w.ErrorMessages[:0]
	w.ClearCount = 0
}

// NoOpWriter discards all output (useful for sandboxing).
type NoOpWriter struct{}

func NewNoOpWriter() *NoOpWriter {
	return &NoOpWriter{}
}

func (w *NoOpWriter) Log(message string)   {}
func (w *NoOpWriter) Warn(message string)  {}
func (w *NoOpWriter) Error(message string) {}
func (w *NoOpWriter) Clear()               {}

// ColorFormatter formats values with ANSI colors like Node.js.
type ColorFormatter struct {
	colors    bool
	maxDepth  int
	maxLength int
}

// NewColorFormatter creates a new color formatter.
// If colors is true, ANSI color codes will be included in output.
func NewColorFormatter(colors bool) *ColorFormatter {
	return &ColorFormatter{
		colors:    colors,
		maxDepth:  4,
		maxLength: 100,
	}
}

// AutoColorFormatter creates a formatter that auto-detects TTY for colors.
func AutoColorFormatter() *ColorFormatter {
	// Check if stdout is a terminal
	colors := term.IsTerminal(int(os.Stdout.Fd()))
	return NewColorFormatter(colors)
}

func (f *ColorFormatter) ColorEnabled() bool {
	return f.colors
}

func (f *ColorFormatter) FormatValue(value interface{}, valueType ValueType) string {
	str := fmt.Sprintf("%v", value)

	if !f.colors {
		return str
	}

	switch valueType {
	case TypeUndefined:
		return Gray + str + Reset
	case TypeNull:
		return Bold + str + Reset
	case TypeBoolean:
		return BrightYellow + str + Reset
	case TypeNumber:
		return BrightYellow + str + Reset
	case TypeString:
		return BrightGreen + str + Reset
	case TypeSymbol:
		return Green + str + Reset
	case TypeFunction:
		return Cyan + str + Reset
	case TypeDate:
		return Magenta + str + Reset
	case TypeRegExp:
		return BrightRed + str + Reset
	case TypeError:
		return Red + str + Reset
	case TypeArray, TypeObject, TypeMap, TypeSet:
		return str // Objects and arrays don't get colored, but their contents do
	case TypePromise:
		return Cyan + str + Reset
	default:
		return str
	}
}

// SetMaxDepth sets the maximum depth for object inspection.
func (f *ColorFormatter) SetMaxDepth(depth int) {
	f.maxDepth = depth
}

// SetMaxLength sets the maximum string length before truncation.
func (f *ColorFormatter) SetMaxLength(length int) {
	f.maxLength = length
}

// PlainFormatter formats values without any colors.
type PlainFormatter struct{}

func NewPlainFormatter() *PlainFormatter {
	return &PlainFormatter{}
}

func (f *PlainFormatter) ColorEnabled() bool {
	return false
}

func (f *PlainFormatter) FormatValue(value interface{}, valueType ValueType) string {
	return fmt.Sprintf("%v", value)
}
