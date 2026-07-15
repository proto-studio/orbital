package runtime

import (
	"os"
	"testing"
)

func TestRealEnvironment(t *testing.T) {
	env := NewRealEnvironment()

	// Test Get with existing variable
	path, ok := env.Get("PATH")
	if !ok || path == "" {
		t.Error("Get(PATH) should return a value")
	}

	// Test Get with non-existing variable
	_, ok = env.Get("__GNODE_TEST_NONEXISTENT__")
	if ok {
		t.Error("Get for non-existent key should return false")
	}

	// Test Set and Unset
	testKey := "__GNODE_TEST_VAR__"
	testVal := "test_value"

	if err := env.Set(testKey, testVal); err != nil {
		t.Errorf("Set failed: %v", err)
	}

	val, ok := env.Get(testKey)
	if !ok || val != testVal {
		t.Errorf("Get after Set = (%q, %v), want (%q, true)", val, ok, testVal)
	}

	if err := env.Unset(testKey); err != nil {
		t.Errorf("Unset failed: %v", err)
	}

	_, ok = env.Get(testKey)
	if ok {
		t.Error("Get after Unset should return false")
	}

	// Clean up just in case
	os.Unsetenv(testKey)
}

func TestSandboxedEnvironment(t *testing.T) {
	env := NewSandboxedEnvironment(map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	})

	// Test Get
	val, ok := env.Get("FOO")
	if !ok || val != "bar" {
		t.Errorf("Get(FOO) = (%q, %v), want (\"bar\", true)", val, ok)
	}

	// Test Set
	env.Set("NEW", "value")
	val, ok = env.Get("NEW")
	if !ok || val != "value" {
		t.Errorf("Get(NEW) = (%q, %v), want (\"value\", true)", val, ok)
	}

	// Test Unset
	env.Unset("FOO")
	_, ok = env.Get("FOO")
	if ok {
		t.Error("Get after Unset should return false")
	}

	// Test List
	list := env.List()
	if len(list) != 2 { // BAZ and NEW
		t.Errorf("List() returned %d items, want 2", len(list))
	}

	// Test All
	all := env.All()
	if len(all) != 2 {
		t.Errorf("All() returned %d items, want 2", len(all))
	}
}

func TestSandboxedEnvironmentWithDefaults(t *testing.T) {
	env := NewSandboxedEnvironmentWithDefaults()

	// Should have safe defaults
	home, _ := env.Get("HOME")
	if home != "/sandbox" {
		t.Errorf("HOME = %q, want \"/sandbox\"", home)
	}

	user, _ := env.Get("USER")
	if user != "sandbox" {
		t.Errorf("USER = %q, want \"sandbox\"", user)
	}

	// Should not have real environment variables
	_, ok := env.Get("ANDROID_HOME")
	if ok {
		t.Error("Sandboxed env should not have ANDROID_HOME")
	}
}

func TestFilteredEnvironment(t *testing.T) {
	base := NewSandboxedEnvironment(map[string]string{
		"ALLOWED":  "yes",
		"DENIED":   "no",
		"SECRET":   "hidden",
		"PUBLIC":   "visible",
	})

	t.Run("allow list", func(t *testing.T) {
		env := NewFilteredEnvironment(base, []string{"ALLOWED", "PUBLIC"}, nil)

		val, ok := env.Get("ALLOWED")
		if !ok || val != "yes" {
			t.Error("ALLOWED should be accessible")
		}

		_, ok = env.Get("DENIED")
		if ok {
			t.Error("DENIED should not be accessible with allow list")
		}

		all := env.All()
		if len(all) != 2 {
			t.Errorf("All() returned %d items, want 2", len(all))
		}
	})

	t.Run("deny list", func(t *testing.T) {
		env := NewFilteredEnvironment(base, nil, []string{"SECRET", "DENIED"})

		val, ok := env.Get("ALLOWED")
		if !ok || val != "yes" {
			t.Error("ALLOWED should be accessible")
		}

		_, ok = env.Get("SECRET")
		if ok {
			t.Error("SECRET should not be accessible with deny list")
		}

		all := env.All()
		if len(all) != 2 {
			t.Errorf("All() returned %d items, want 2", len(all))
		}
	})
}

func TestReadOnlyEnvironment(t *testing.T) {
	base := NewSandboxedEnvironment(map[string]string{
		"KEY": "value",
	})
	env := NewReadOnlyEnvironment(base)

	// Read should work
	val, ok := env.Get("KEY")
	if !ok || val != "value" {
		t.Error("Get should work on read-only env")
	}

	// Write should be silently ignored
	env.Set("KEY", "newvalue")
	val, _ = env.Get("KEY")
	if val != "value" {
		t.Error("Set should be ignored on read-only env")
	}

	env.Set("NEW", "value")
	_, ok = env.Get("NEW")
	if ok {
		t.Error("New keys should not be created on read-only env")
	}

	// Unset should be silently ignored
	env.Unset("KEY")
	_, ok = env.Get("KEY")
	if !ok {
		t.Error("Unset should be ignored on read-only env")
	}
}
