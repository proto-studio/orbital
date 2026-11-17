package crypto

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"slices"
	"testing"
	"time"
)

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
// bit set, every bit set, random) take about the same time. The margin (1.5x)
// allows for a busy CI machine; math/big's Exp, which depends on the value,
// differs by orders of magnitude on the same exponents.
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
	exps := map[string][]byte{"one bit": oneBit, "all bits": allBits, "random": random}

	// Warm up, then measure each exponent in turn several times so drift in
	// the machine's speed affects them alike.
	for _, e := range exps {
		dhExp(g, e, m, p)
	}
	medians := map[string][]time.Duration{}
	for round := 0; round < 5; round++ {
		for name, e := range exps {
			medians[name] = append(medians[name], medianDuration(15, func() { dhExp(g, e, m, p) }))
		}
	}
	best := map[string]time.Duration{}
	for name, ds := range medians {
		slices.Sort(ds)
		best[name] = ds[0]
	}
	lo, hi := best["one bit"], best["one bit"]
	for _, d := range best {
		lo, hi = min(lo, d), max(hi, d)
	}
	t.Logf("constant-time exponentiation: %v", best)
	if float64(hi) > 1.5*float64(lo) {
		t.Errorf("times differ by exponent: %v", best)
	}

	// The same measurement on math/big, which isn't constant time, for
	// comparison in the log.
	bigOne, bigAll := new(big.Int).SetBytes(oneBit), new(big.Int).SetBytes(allBits)
	tOne := medianDuration(15, func() { new(big.Int).Exp(g, bigOne, p) })
	tAll := medianDuration(15, func() { new(big.Int).Exp(g, bigAll, p) })
	t.Logf("math/big for comparison: one bit %v, all bits %v", tOne, tAll)
}
