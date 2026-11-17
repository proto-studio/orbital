package crypto

import (
	"bytes"
	"crypto/rand"
	"math/big"
	mrand "math/rand/v2"
	"slices"
	"testing"
	"time"
)

// interleavedLowTimes calls f(i) for each of names in turn, rounds times over,
// shuffling the order each round, and returns each one's 10th-percentile time.
func interleavedLowTimes(rounds int, names []string, f func(i int)) map[string]time.Duration {
	samples := make([][]time.Duration, len(names))
	order := make([]int, len(names))
	for i := range order {
		order[i] = i
	}
	for r := 0; r < rounds; r++ {
		mrand.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
		for _, i := range order {
			start := time.Now()
			f(i)
			samples[i] = append(samples[i], time.Since(start))
		}
	}
	out := make(map[string]time.Duration, len(names))
	for i, ds := range samples {
		slices.Sort(ds)
		out[names[i]] = ds[len(ds)/10]
	}
	return out
}

// medianDuration runs f n times and returns the median time one run took.
func medianDuration(n int, f func()) time.Duration {
	times := make([]time.Duration, n)
	for i := range times {
		start := time.Now()
		f()
		times[i] = time.Since(start)
	}
	slices.Sort(times)
	return times[n/2]
}

// The private-key exponentiation takes the same time whatever the exponent's
// value: exponents of the same length with very different bit patterns (one
// bit set, every bit set, random) take about the same time; math/big's Exp,
// which depends on the value, differs by orders of magnitude on the same
// exponents.
//
// CI machines are shared, so the exponents are timed interleaved, one call
// each in turn, and compared by a low percentile: load that slows one call
// slows its neighbours too. The margin is 1.5x, and the whole measurement is
// taken up to three times before the test fails, so a burst of load can't
// fail it while a real dependence on the exponent still does every time.
func TestDHExpTimeDoesNotDependOnExponent(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	p, _ := new(big.Int).SetString(modpGroups["modp14"], 16)
	m, err := dhModulus(p, errModulusTooLarge)
	if err != nil {
		t.Fatal(err)
	}
	size := m.Size()
	oneBit := make([]byte, size)
	oneBit[size-1] = 1
	allBits := bytes.Repeat([]byte{0xff}, size)
	random := make([]byte, size)
	rand.Read(random)
	g := big.NewInt(2)
	names := []string{"one bit", "all bits", "random"}
	exps := [][]byte{oneBit, allBits, random}

	// Warm up.
	for _, e := range exps {
		dhExp(g, e, m, p)
	}
	var times map[string]time.Duration
	for attempt := 1; attempt <= 3; attempt++ {
		times = interleavedLowTimes(60, names, func(i int) { dhExp(g, exps[i], m, p) })
		lo, hi := times[names[0]], times[names[0]]
		for _, d := range times {
			lo, hi = min(lo, d), max(hi, d)
		}
		t.Logf("constant-time exponentiation, attempt %d: %v", attempt, times)
		if float64(hi) <= 1.5*float64(lo) {
			break
		}
		if attempt == 3 {
			t.Errorf("times differ by exponent in every attempt: %v", times)
		}
	}

	// The same measurement on math/big, which isn't constant time, for
	// comparison in the log.
	bigOne, bigAll := new(big.Int).SetBytes(oneBit), new(big.Int).SetBytes(allBits)
	tOne := medianDuration(15, func() { new(big.Int).Exp(g, bigOne, p) })
	tAll := medianDuration(15, func() { new(big.Int).Exp(g, bigAll, p) })
	t.Logf("math/big for comparison: one bit %v, all bits %v", tOne, tAll)
}
