package runtime

import (
	"strings"
	"testing"

	"proto.zip/studio/orbital/pkg/v8"
)

// sourceHeader lays out the header as Node's GetErrorSource does.
func TestSourceHeaderLayout(t *testing.T) {
	rt := &Runtime{}
	for name, tc := range map[string]struct {
		place *v8.ExceptionPlace
		want  string
	}{
		"no place":   {nil, ""},
		"no line":    {&v8.ExceptionPlace{ResourceName: "a.js", Line: 1}, ""},
		"no name":    {&v8.ExceptionPlace{Line: 1, SourceLine: "x", HasSourceLine: true}, ""},
		"plain":      {&v8.ExceptionPlace{ResourceName: "a.js", Line: 3, SourceLine: "  throw 1;", HasSourceLine: true, StartColumn: 2, EndColumn: 3}, "a.js:3\n  throw 1;\n  ^\n"},
		"wide":       {&v8.ExceptionPlace{ResourceName: "a.js", Line: 1, SourceLine: "let x = foo barbaz;", HasSourceLine: true, StartColumn: 12, EndColumn: 18}, "a.js:1\nlet x = foo barbaz;\n            ^^^^^^\n"},
		"tabs":       {&v8.ExceptionPlace{ResourceName: "a.js", Line: 1, SourceLine: "\t\tthrow 1;", HasSourceLine: true, StartColumn: 2, EndColumn: 3}, "a.js:1\n\t\tthrow 1;\n\t\t^\n"},
		"no columns": {&v8.ExceptionPlace{ResourceName: "a.js", Line: 1, SourceLine: "x", HasSourceLine: true, StartColumn: -1, EndColumn: -1}, "a.js:1\nx\n"},
		"past end":   {&v8.ExceptionPlace{ResourceName: "a.js", Line: 1, SourceLine: "x", HasSourceLine: true, StartColumn: 0, EndColumn: 5}, "a.js:1\nx\n"},
		"empty line": {&v8.ExceptionPlace{ResourceName: "a.js", Line: 2, SourceLine: "", HasSourceLine: true}, "a.js:2\n\n\n"},
		// The line is printed up to a NUL, and the carets still find the place past it.
		"NUL":            {&v8.ExceptionPlace{ResourceName: "a.js", Line: 1, SourceLine: "/*\x00*/ throw 1;", HasSourceLine: true, StartColumn: 6, EndColumn: 7}, "a.js:1\n/*\n  ^\n"},
		"long underline": {&v8.ExceptionPlace{ResourceName: "a.js", Line: 1, SourceLine: strings.Repeat(" ", 2000) + "x", HasSourceLine: true, StartColumn: 2000, EndColumn: 2001}, "a.js:1\n" + strings.Repeat(" ", 2000) + "x\n" + strings.Repeat(" ", 1020) + "\n"},
	} {
		if got := rt.sourceHeader(tc.place); got != tc.want {
			t.Errorf("%s: sourceHeader = %q, want %q", name, got, tc.want)
		}
	}
}
