package runtime

import (
	"strconv"
	"strings"

	"proto.zip/studio/orbital/pkg/v8"
)

// sourceHeader formats the header Node prints above a fatal exception from where V8's
// message places it: "file:line", the line, and carets under the place, each followed by a
// newline. It follows Node's GetErrorSource, columns and all: V8 counts columns in UTF-16
// units and Node lays the carets out over the line's UTF-8 bytes. It returns "" if there
// is no place or no line.
func (rt *Runtime) sourceHeader(place *v8.ExceptionPlace) string {
	if place == nil || !place.HasSourceLine || place.ResourceName == "" {
		return ""
	}
	line, start, end := place.SourceLine, place.StartColumn, place.EndColumn
	var b strings.Builder
	b.WriteString(place.ResourceName)
	b.WriteString(":")
	b.WriteString(strconv.Itoa(place.Line))
	b.WriteString("\n")
	// Node prints the line as a C string, so up to a NUL character, and lays out the
	// carets over all of it, stopping the spaces before the place at a NUL.
	if i := strings.IndexByte(line, 0); i != -1 {
		b.WriteString(line[:i])
	} else {
		b.WriteString(line)
	}
	b.WriteString("\n")
	if start > end || start < 0 || end > len(line) {
		return b.String()
	}
	// Node's underline buffer holds this many characters.
	const underlineLimit = 1020
	n := 0
	for i := 0; i < start && n < underlineLimit; i, n = i+1, n+1 {
		if line[i] == 0 {
			break
		}
		if line[i] == '\t' {
			b.WriteByte('\t')
		} else {
			b.WriteByte(' ')
		}
	}
	for i := start; i < end && n < underlineLimit; i, n = i+1, n+1 {
		if line[i] == 0 {
			break
		}
		b.WriteByte('^')
	}
	b.WriteString("\n")
	return b.String()
}
