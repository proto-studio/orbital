package fs

import (
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Stats times past 2262, which a filesystem such as ext4 can hold, keep their value.
func TestMillis(t *testing.T) {
	for _, tc := range []struct {
		t    time.Time
		want float64
	}{
		{time.Unix(0, 0), 0},
		{time.Unix(1700000000, 123456789), 1700000000123.456789},
		{time.Unix(10413792000, 500000000), 10413792000500},
		{time.Unix(-1, 0), -1000},
	} {
		if got := millis(tc.t); got != tc.want {
			t.Errorf("millis(%v) = %v, want %v", tc.t, got, tc.want)
		}
	}
}

func TestNanos(t *testing.T) {
	for _, tc := range []struct {
		t    time.Time
		want string
	}{
		{time.Unix(1700000000, 123456789), "1700000000123456789"},
		{time.Unix(10413792000, 1), "10413792000000000001"},
		{time.Unix(0, 0), "0"},
	} {
		if got := nanos(tc.t); got != tc.want {
			t.Errorf("nanos(%v) = %s, want %s", tc.t, got, tc.want)
		}
	}
}

// A large inode keeps its exact value in BigInt stats.
func TestBigIntStatsKeepExactNumbers(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	f := New()
	f.rt = rt
	ctx := rt.Context()
	obj := f.createStatObject(ctx, &runtime.FileInfo{Ino: 9007199254740993, Size: 1, HasDetails: true})
	if err := rt.SetGlobal("__rawStat", obj); err != nil {
		t.Fatal(err)
	}
	result, err := rt.RunScript(`String(__fs_module.__toBigIntStats(__fs_module.__addStatsMethods(__rawStat)).ino)`, "t.js")
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "9007199254740993" {
		t.Errorf("ino = %s", result.String())
	}
}
