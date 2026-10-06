package ordercancel

import (
	"crypto/rand"
	"math/big"
	"time"
)

// jittered spreads one pause over its upper half, so the handlers of one
// order's lines, refused together, do not ask again together; draw answers a
// number in [0, n]. The pause never exceeds wait, so [busyBudget] still bounds
// the sum (ADR 0420).
func jittered(wait time.Duration, draw func(n int64) int64) time.Duration {
	half := wait / 2

	return wait - half + time.Duration(draw(int64(half)))
}

// drawUniform answers a number in [0, n] from crypto/rand, the repository's
// one source of randomness; a failed read answers n, the nominal pause.
//
// It lives apart from the flow because a file importing crypto/rand is one the
// constant-time gate reads as making a secret (ADR 0257), and this one makes
// none: the number only spreads a pause.
func drawUniform(n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n+1))
	if err != nil {
		return n
	}

	return v.Int64()
}
