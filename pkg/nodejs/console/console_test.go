package console

import (
	"strings"
	"testing"
)

func TestColorFormatter(t *testing.T) {
	t.Run("with colors enabled", func(t *testing.T) {
		f := NewColorFormatter(true)

		tests := []struct {
			value    interface{}
			vtype    ValueType
			contains string
		}{
			{"undefined", TypeUndefined, Gray},
			{"null", TypeNull, Bold},
			{"true", TypeBoolean, BrightYellow},
			{"42", TypeNumber, BrightYellow},
			{"'hello'", TypeString, BrightGreen},
			{"[Function]", TypeFunction, Cyan},
			{"error", TypeError, Red},
		}

		for _, tt := range tests {
			result := f.FormatValue(tt.value, tt.vtype)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("FormatValue(%v, %v) = %q, want to contain %q",
					tt.value, tt.vtype, result, tt.contains)
			}
			if !strings.Contains(result, Reset) {
				t.Errorf("FormatValue(%v, %v) = %q, should end with Reset",
					tt.value, tt.vtype, result)
			}
		}
	})

	t.Run("with colors disabled", func(t *testing.T) {
		f := NewColorFormatter(false)

		result := f.FormatValue("42", TypeNumber)
		if strings.Contains(result, "\033") {
			t.Errorf("FormatValue with colors disabled should not contain escape codes, got %q", result)
		}
		if result != "42" {
			t.Errorf("FormatValue = %q, want %q", result, "42")
		}
	})
}

func TestPlainFormatter(t *testing.T) {
	f := NewPlainFormatter()

	if f.ColorEnabled() {
		t.Error("PlainFormatter.ColorEnabled() should return false")
	}

	result := f.FormatValue("test", TypeString)
	if result != "test" {
		t.Errorf("FormatValue = %q, want %q", result, "test")
	}
}

func TestBufferedWriter(t *testing.T) {
	w := NewBufferedWriter()

	w.Log("log message")
	w.Warn("warn message")
	w.Error("error message")
	w.Clear()

	if len(w.LogMessages) != 1 || w.LogMessages[0] != "log message" {
		t.Errorf("LogMessages = %v, want [\"log message\"]", w.LogMessages)
	}
	if len(w.WarnMessages) != 1 || w.WarnMessages[0] != "warn message" {
		t.Errorf("WarnMessages = %v, want [\"warn message\"]", w.WarnMessages)
	}
	if len(w.ErrorMessages) != 1 || w.ErrorMessages[0] != "error message" {
		t.Errorf("ErrorMessages = %v, want [\"error message\"]", w.ErrorMessages)
	}
	if w.ClearCount != 1 {
		t.Errorf("ClearCount = %d, want 1", w.ClearCount)
	}

	w.Reset()
	if len(w.LogMessages) != 0 || len(w.WarnMessages) != 0 || len(w.ErrorMessages) != 0 {
		t.Error("Reset should clear all messages")
	}
	if w.ClearCount != 0 {
		t.Error("Reset should reset ClearCount")
	}
}

func TestNoOpWriter(t *testing.T) {
	w := NewNoOpWriter()

	// Should not panic
	w.Log("test")
	w.Warn("test")
	w.Error("test")
	w.Clear()
}

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"plain text", "plain text"},
		{"\033[31mred\033[0m", "red"},
		{"\033[1m\033[33mbold yellow\033[0m", "bold yellow"},
		{"no colors", "no colors"},
		{"\033[90mundefined\033[0m", "undefined"},
	}

	for _, tt := range tests {
		result := stripAnsi(tt.input)
		if result != tt.expected {
			t.Errorf("stripAnsi(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
