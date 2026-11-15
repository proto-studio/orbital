package runtime

import (
	"testing"
	"time"
)

// Times past 2262 keep their seconds when given to the OS.
func TestTimevalAndTimespecFarFuture(t *testing.T) {
	future := time.Unix(10413792000, 500000000)
	if tv := timeval(future); tv.Sec != 10413792000 || tv.Usec != 500000 {
		t.Errorf("timeval = %+v", tv)
	}
	if ts := timespec(future); ts.Sec != 10413792000 || ts.Nsec != 500000000 {
		t.Errorf("timespec = %+v", ts)
	}
}
