package fs

import (
	"testing"
	"time"
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
