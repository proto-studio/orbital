package crypto

import (
	"crypto/rand"
	_ "embed"
	"io"
	"math/big"

	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed randomint.js
var randomIntJS string

// randomIntFunc returns a uniformly distributed integer in [0, range).
// randomint.js validates the arguments, so range is an integer in
// [1, 2^48-1] and the result is exact as a float64. rand.Int rejects
// out-of-range samples instead of reducing them modulo range, which would
// favor low values.
func (c *Crypto) randomIntFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("randomInt: range is required")
	}
	rng := args[0].Integer()
	if rng < 1 {
		return ctx.Throw("randomInt: range must be positive")
	}
	n, err := uniformInt(rand.Reader, rng)
	if err != nil {
		return ctx.Throw("randomInt: " + err.Error())
	}
	return ctx.NewNumber(float64(n))
}

// uniformInt draws from r a value in [0, rng) with every value equally likely.
func uniformInt(r io.Reader, rng int64) (int64, error) {
	n, err := rand.Int(r, big.NewInt(rng))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}
