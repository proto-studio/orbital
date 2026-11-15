package crypto

import (
	"bytes"
	"testing"
)

// TestCrypto_RandomIntUniform feeds uniformInt every possible random input
// equally often and checks that each value of the range comes out equally
// often. Reducing the input modulo the range would favor the low values.
func TestCrypto_RandomIntUniform(t *testing.T) {
	for _, rng := range []int64{3, 5, 6, 7, 100, 200} {
		// rand.Int reads one byte for these ranges; give it all 256 values
		// several times over so every draw, including retries, is covered.
		var input []byte
		for rep := 0; rep < 64; rep++ {
			for b := 0; b < 256; b++ {
				input = append(input, byte(b))
			}
		}
		r := bytes.NewReader(input)
		counts := make([]int, rng)
		for {
			n, err := uniformInt(r, rng)
			if err != nil {
				break
			}
			if n < 0 || n >= rng {
				t.Fatalf("range %d: got %d", rng, n)
			}
			counts[n]++
		}
		for v, c := range counts {
			if c != counts[0] {
				t.Errorf("range %d: value %d came up %d times, value 0 %d times", rng, v, c, counts[0])
			}
		}
	}
}
