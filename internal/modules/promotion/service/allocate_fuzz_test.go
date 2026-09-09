package service

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// fuzzMaxLines caps how many lines a generated cart carries.
//
// The bound is not about realism — a cart of sixty-four lines is already past
// what a shop sees — but about the fuzzer's budget: a body that walks a
// thousand lines spends its time in a loop rather than in the arithmetic the
// target is about.
const fuzzMaxLines = 64

// FuzzMulDivMod checks the 128-bit multiply-divide against arbitrary precision.
//
// # Why this function
//
// It is the only place in the promotion arithmetic that leaves int64: the
// product of a total and a line amount does not fit, so the function multiplies
// into a 128-bit pair and divides back down. Every table test written for it
// picks numbers a person thought of, and the numbers that break a 128-bit
// division are the ones nobody thinks of.
//
// # The overflow contract is DERIVED and not skipped
//
// The obvious shape for this target is to return early whenever the inputs look
// like they might overflow, and that shape asserts nothing about the branch the
// function exists for. The expectation is computed instead: math/big says what
// the quotient and remainder are, and whether the quotient fits in an int64
// decides which of the two contracts applies — an exact answer, or the zero
// pair the function returns rather than wrapping.
func FuzzMulDivMod(f *testing.F) {
	f.Add(int64(0), int64(0), int64(0))
	f.Add(int64(1), int64(1), int64(1))
	f.Add(int64(9_999), int64(1_000), int64(3_000))
	f.Add(models.MaxAmount, models.MaxAmount, models.MaxAmount)
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64), int64(1))
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64), int64(math.MaxInt64))
	f.Add(int64(math.MinInt64), int64(2), int64(3))
	f.Add(int64(7), int64(3), int64(math.MaxInt64))

	// The four boundary seeds below are DERIVED and not discovered, and the
	// reason is measured: with the overflow guard mutated from `hi >= d` to
	// `hi > d`, twenty-five seconds and ten million generated inputs never
	// reached the case that separates them. `hi == d` needs the 128-bit product
	// to land inside one exact window of width 2^64, and random bytes do not
	// land there. A fuzzer covers the space it can reach; a boundary this thin
	// arrives by arithmetic or not at all.
	f.Add(int64(1)<<32, int64(1)<<32, int64(1))   // hi == d == 1
	f.Add(int64(1)<<32, int64(1)<<33, int64(2))   // hi == d == 2
	f.Add(int64(1)<<32, int64(1)<<31, int64(1))   // quotient is exactly 2^63
	f.Add(int64(1)<<32, int64(1)<<31-1, int64(1)) // the largest quotient that fits

	f.Fuzz(func(t *testing.T, a, b, d int64) {
		quotient, remainder := mulDivMod(a, b, d)

		if a <= 0 || b <= 0 || d <= 0 {
			require.Equal(t, int64(0), quotient, "a non-positive input has no share")
			require.Equal(t, int64(0), remainder, "a non-positive input has no remainder")
			return
		}

		product := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
		wantQuotient, wantRemainder := new(big.Int).QuoRem(product, big.NewInt(d), new(big.Int))

		if !wantQuotient.IsInt64() {
			require.Equal(t, int64(0), quotient,
				"a quotient of %s does not fit in an int64 and must not wrap", wantQuotient)
			require.Equal(t, int64(0), remainder, "a refused division reports no remainder")
			return
		}

		require.Equal(t, wantQuotient.Int64(), quotient, "quotient of %d x %d / %d", a, b, d)
		require.Equal(t, wantRemainder.Int64(), remainder, "remainder of %d x %d / %d", a, b, d)
	})
}

// FuzzAllocateAcross checks the promise the largest-remainder method exists for.
//
// # The property, in one sentence
//
// Every kurus of the total lands on exactly one line, no line receives more
// than it is worth, and which line receives the remainder does not depend on
// the order the lines arrived in.
//
// The last third is the one a table test cannot reach. The tie-break runs
// remainder, then amount, then identity, and a cart where two lines agree on
// the first two is a cart somebody has to construct on purpose. A fuzzer
// constructs it by accident, and then the SHUFFLE decides whether the third
// criterion is doing anything.
//
// # The precondition is enforced and not skipped
//
// `allocateAcross` sums the line amounts into an int64 without guarding the
// addition, which is sound because its only caller refuses a subtotal over
// [models.MaxAmount] before reaching it. A target handing it sixty-four lines
// of 10^18 would be measuring a precondition violation and calling it a defect,
// so the generator truncates the cart at the point where the positive sum would
// pass that ceiling. What is left is a cart the caller could really produce.
func FuzzAllocateAcross(f *testing.F) {
	f.Add(int64(0), []byte{})
	f.Add(int64(100), fuzzAmounts(50, 50))
	f.Add(int64(9_999), fuzzAmounts(1_000, 2_000, 3_000, 4_000))
	f.Add(int64(1), fuzzAmounts(1, 1, 1))
	f.Add(int64(7), fuzzAmounts(3, 3, 3))
	f.Add(models.MaxAmount, fuzzAmounts(models.MaxAmount/2, models.MaxAmount/2))
	f.Add(int64(10), fuzzAmounts(-5, 0, 10))
	f.Add(int64(math.MaxInt64), fuzzAmounts(math.MaxInt64))

	f.Fuzz(func(t *testing.T, total int64, raw []byte) {
		lines := fuzzLines(raw)

		out := allocateAcross(total, lines)
		require.Len(t, out, len(lines), "an allocation answers for every line it was given")

		var base, assigned int64
		for i, line := range lines {
			require.GreaterOrEqualf(t, out[i], int64(0), "line %d received a negative share", i)
			if line.Amount <= 0 {
				require.Zerof(t, out[i], "line %d is worth nothing and may not receive a share", i)
				continue
			}
			require.LessOrEqualf(t, out[i], line.Amount,
				"line %d received %d against an amount of %d", i, out[i], line.Amount)
			base += line.Amount
			assigned += out[i]
		}

		want := min(total, base)
		if total <= 0 || base <= 0 {
			want = 0
		}
		require.Equal(t, want, assigned,
			"the distributed shares must add up to the total, remainder included")

		// The order the lines arrived in must not decide who gets the kurus.
		// Reversing is enough to break a tie-break that fell through to the
		// slice index, and it costs one more call rather than a shuffle whose
		// own randomness would have to come from somewhere.
		reversed := make([]allocLine, len(lines))
		for i, line := range lines {
			reversed[len(lines)-1-i] = line
		}
		mirrored := allocateAcross(total, reversed)
		for i := range lines {
			require.Equalf(t, out[i], mirrored[len(lines)-1-i],
				"line %d (%s) received a different share when the lines arrived in the other order",
				i, lines[i].ID)
		}
	})
}

// fuzzAmounts writes a seed cart in the encoding [fuzzLines] reads.
func fuzzAmounts(amounts ...int64) []byte {
	raw := make([]byte, 0, len(amounts)*8)
	for _, amount := range amounts {
		raw = binary.BigEndian.AppendUint64(raw, uint64(amount))
	}
	return raw
}

// fuzzLines decodes a cart from the fuzzer's bytes.
//
// Identities are generated rather than fuzzed, and they are DISTINCT on
// purpose: the tie-break's last criterion is the identity, and two lines
// sharing one would make the result depend on the sort's stability instead —
// a different property from the one this target is about.
func fuzzLines(raw []byte) []allocLine {
	lines := make([]allocLine, 0, min(len(raw)/8, fuzzMaxLines))

	var positive int64
	for i := 0; i+8 <= len(raw) && len(lines) < fuzzMaxLines; i += 8 {
		amount := int64(binary.BigEndian.Uint64(raw[i : i+8]))
		if amount > 0 {
			if amount > models.MaxAmount || positive > models.MaxAmount-amount {
				break
			}
			positive += amount
		}
		lines = append(lines, allocLine{ID: fmt.Sprintf("line-%03d", len(lines)), Amount: amount})
	}

	return lines
}
