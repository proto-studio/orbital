package crypto

import (
	"strconv"
	"strings"
)

// The natives hand bytes to JavaScript as base64 or hex strings, so what one
// can return is bounded by V8's longest string, String::kMaxLength on 64-bit
// platforms (Node's buffer.constants.MAX_STRING_LENGTH). The natives check
// the sizes and lengths they're given against these bounds themselves, since
// scripts can call them directly, and the JavaScript wrappers check the same
// bounds after Node's own.
const (
	maxStringLength = 1<<29 - 24
	// maxNativeBytes is the most bytes one base64 string holds.
	maxNativeBytes = maxStringLength / 4 * 3
	// maxPrimeBits is the largest prime, in bits, whose hex form fits a string.
	maxPrimeBits = maxStringLength * 4
)

// outOfRange is Node's ERR_OUT_OF_RANGE message for an integer outside
// [min, max], with Node's digit grouping for values of 2^32 and more.
func outOfRange(name string, min, max, got int64) string {
	return `The value of "` + name + `" is out of range. It must be >= ` + strconv.FormatInt(min, 10) +
		` && <= ` + strconv.FormatInt(max, 10) + `. Received ` + groupDigits(got)
}

func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 1<<32 && n > -(1<<32) {
		return s
	}
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('_')
		}
		b.WriteRune(c)
	}
	return sign + b.String()
}
