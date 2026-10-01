package config

import (
	"fmt"
	"strconv"
	"strings"
)

// OfflineWaits is PAYMENT_OFFLINE_WAIT_DAYS read as written: the days each
// named offline method waits for its money (ADR 0289).
//
// It reads its own text rather than leaving the pairs to the environment
// library, which keeps the last of a method given twice without a word.
// PAYMENT_OFFLINE_METHODS refuses a name given twice, and so does this.
type OfflineWaits map[string]int

// UnmarshalText reads "method:days,method:days". Empty is no wait. A pair
// without a colon, a count that is not a whole number and a method given twice
// are refused; a name with surrounding whitespace is a method nobody offers,
// and the range is [Config.Validate]'s.
func (w *OfflineWaits) UnmarshalText(text []byte) error {
	out := OfflineWaits{}
	if len(text) > 0 {
		for i, pair := range strings.Split(string(text), ",") {
			method, days, _ := strings.Cut(pair, ":")
			if method == "" {
				return fmt.Errorf("pair %d, %q, names no method", i+1, pair)
			}
			// A pair without a colon leaves the days empty, which is not a
			// number either.
			n, err := strconv.Atoi(days)
			if err != nil {
				return fmt.Errorf("pair %d, %q, is not method:days with whole days", i+1, pair)
			}
			if _, dup := out[method]; dup {
				return fmt.Errorf("%q is given twice", method)
			}
			out[method] = n
		}
	}
	*w = out

	return nil
}
