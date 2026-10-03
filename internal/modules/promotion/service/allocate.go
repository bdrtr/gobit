package service

import (
	"cmp"
	"math"
	"math/bits"
	"slices"
)

// allocLine is a single line taking part in an allocation.
type allocLine struct {
	// ID is the line's identity; it is the final tie-breaker when deciding who
	// owns the leftover cent.
	ID string
	// Amount is the line's amount (minor unit); the share is proportional to
	// this amount.
	Amount int64
}

// allocateAcross distributes the given total across the lines in PROPORTION to
// their amounts.
//
// The distributed shares add up EXACTLY to the total passed in (leftover cent
// included); the one exception is when every line's amount is zero, and there
// is no base to distribute over.
//
// # Method: largest remainder
//
// Each line's share is first computed rounding DOWN:
//
//	share_i = floor(total × amount_i / base),  base = Σ amount_i
//
// Because of the rounding down, the shares fall short of the total by a
// `leftover`, and the leftover is smaller than the number of lines receiving a
// share. The leftover is handed out ONE BY ONE to the lines whose fractional
// remainder is the LARGEST.
//
// The simple alternatives were rejected deliberately: giving the whole leftover
// to the first line or to the largest line piles a two-cent leftover onto a
// single item and shows that item's discount two cents above its proportional
// entitlement.
//
// # Who gets the leftover cent
//
// The ordering criteria are these; the first DIFFERENCE decides the winner:
//
//  1. Fractional remainder (larger wins) — the line whose proportional
//     entitlement was cut the most.
//  2. Amount (larger wins) — on an equal remainder, the larger line is
//     proportionally less distorted by one cent.
//  3. Identity (smaller wins) — in every remaining case the result is
//     DETERMINISTIC and independent of the lines' ARRIVAL ORDER. Since
//     identities are time-ordered, this means "the item added first".
//
// Determinism is not decoration: when the same cart is computed twice, the same
// cent must land on the same line, otherwise line amounts shift between the two
// computations and reconciliation becomes impossible.
//
// # Bounds
//
// total CANNOT exceed the base; if it does, it is clipped to the base — an
// allocation cannot distribute more than the base it distributes over. A line
// whose amount is NOT positive receives NO share: writing a cent onto a
// zero-amount item would be showing a discount on an item that has none.
func allocateAcross(total int64, lines []allocLine) []int64 {
	out := make([]int64, len(lines))
	if total <= 0 || len(lines) == 0 {
		return out
	}

	var base int64
	for i := range lines {
		if lines[i].Amount > 0 {
			base += lines[i].Amount
		}
	}
	if base <= 0 {
		return out
	}
	if total > base {
		total = base
	}

	// share is a line's share and its fractional remainder; the leftover
	// distribution is ordered by it.
	type share struct {
		index     int
		remainder int64
		amount    int64
		id        string
	}

	shares := make([]share, 0, len(lines))
	var assigned int64
	for i := range lines {
		if lines[i].Amount <= 0 {
			continue
		}
		quotient, remainder := mulDivMod(total, lines[i].Amount, base)
		out[i] = quotient
		assigned += quotient
		shares = append(shares, share{
			index:     i,
			remainder: remainder,
			amount:    lines[i].Amount,
			id:        lines[i].ID,
		})
	}

	slices.SortFunc(shares, func(a, b share) int {
		if c := cmp.Compare(b.remainder, a.remainder); c != 0 {
			return c
		}
		if c := cmp.Compare(b.amount, a.amount); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})

	leftover := total - assigned
	for i := 0; i < len(shares) && leftover > 0; i++ {
		out[shares[i].index]++
		leftover--
	}
	return out
}

// mulDivMod returns the quotient and remainder of a×b/d through a 128-bit
// intermediate result.
//
// The intermediate product MAY NOT FIT in an int64: in an allocation both a and
// b can be as large as [models.MaxAmount] (10^12), and their product reaches
// 10^24. That is why math/bits' 128-bit multiplication/division is mandatory;
// switching to float is what plan Section 8 forbids, and it would produce
// silent errors at the cent level anyway.
//
// The quotient is rounded DOWN (integer division), and the remainder is the
// sort key of the leftover distribution.
//
// Precondition: 0 ≤ a ≤ d, 0 ≤ b ≤ d, d > 0. Under this condition the 128-bit
// division's own precondition (high word < divisor) holds by itself and the
// quotient fits in an int64. When the condition is violated, zero is returned
// — that is, no discount is given. The direction is deliberate: when an
// arithmetic precondition is broken, giving no discount at all is preferable to
// giving the customer a discount that could not be computed, and the situation
// stays visible in the totals.
func mulDivMod(a, b, d int64) (quotient, remainder int64) {
	if a <= 0 || b <= 0 || d <= 0 {
		return 0, 0
	}

	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(d) {
		return 0, 0
	}

	q, r := bits.Div64(hi, lo, uint64(d))
	// The remainder is smaller than the divisor, and since the divisor is an
	// int64 the remainder fits too; the quotient, under the precondition,
	// cannot exceed b. Both are checked anyway — proving the bound LOCALLY makes
	// it impossible for a distant change to silently produce a wraparound.
	if q > math.MaxInt64 || r > math.MaxInt64 {
		return 0, 0
	}
	return int64(q), int64(r)
}
