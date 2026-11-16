package runtime

import (
	"strings"
	"testing"
)

// Only a single character the module loader would choose stands for NUL in kept text;
// text with any other marker isn't kept.
func TestKeepModuleSourceWithNULChecksTheMarker(t *testing.T) {
	rt, err := New(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	const text = "module.exports = 1;\n"
	for _, tc := range []struct {
		nul  string
		kept bool
	}{
		{"￿", true},
		{"", true},
		{"", true},
		{"", false},
		{"x", false},
		{"", false},
		{strings.Repeat("", 1<<16), false},
	} {
		name := "m" + string(rune('a'+len(rt.keptSources))) + ".js"
		before := rt.keptBytes
		rt.KeepModuleSourceWithNUL(name, text, tc.nul)
		kept, ok := rt.keptSources[name]
		if got := ok && kept.hasText; got != tc.kept {
			t.Errorf("marker %q: kept = %v, want %v", tc.nul, got, tc.kept)
		}
		if !tc.kept && rt.keptBytes != before {
			t.Errorf("marker %q: keptBytes went from %d to %d", tc.nul, before, rt.keptBytes)
		}
	}
}
