package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// sumOf returns the sum of a slice.
func sumOf(values []int64) int64 {
	var sum int64
	for _, v := range values {
		sum += v
	}
	return sum
}

func TestAllocateAcrossDistributesTheTotalExactly(t *testing.T) {
	tests := []struct {
		name  string
		total int64
		lines []allocLine
		want  []int64
	}{
		{
			name:  "equal lines, a total that does not divide",
			total: 100,
			lines: []allocLine{{"li_a", 1000}, {"li_b", 1000}, {"li_c", 1000}},
			want:  []int64{34, 33, 33},
		},
		{
			name:  "proportional distribution",
			total: 100,
			lines: []allocLine{{"li_a", 3000}, {"li_b", 1000}},
			want:  []int64{75, 25},
		},
		{
			name:  "a two-cent leftover goes to two SEPARATE lines",
			total: 10,
			lines: []allocLine{{"li_a", 100}, {"li_b", 100}, {"li_c", 100}, {"li_d", 100}},
			want:  []int64{3, 3, 2, 2},
		},
		{
			// The shares are 3⅓ and 6⅔; the single leftover cent goes to the
			// second line, whose fractional remainder is LARGER (distributed by
			// order or by identity, it would have gone to the first line).
			name:  "the line with the larger fractional remainder takes the leftover",
			total: 10,
			lines: []allocLine{{"li_a", 10}, {"li_b", 20}},
			want:  []int64{3, 7},
		},
		{
			name:  "a zero-amount line receives no share",
			total: 50,
			lines: []allocLine{{"li_a", 0}, {"li_b", 500}},
			want:  []int64{0, 50},
		},
		{
			name:  "a total above the base is clipped to the base",
			total: 5000,
			lines: []allocLine{{"li_a", 100}, {"li_b", 100}},
			want:  []int64{100, 100},
		},
		{
			name:  "a zero total distributes nothing",
			total: 0,
			lines: []allocLine{{"li_a", 100}},
			want:  []int64{0},
		},
		{
			name:  "a negative total distributes nothing",
			total: -10,
			lines: []allocLine{{"li_a", 100}},
			want:  []int64{0},
		},
		{
			name:  "nothing can be distributed when every line is zero",
			total: 100,
			lines: []allocLine{{"li_a", 0}, {"li_b", 0}},
			want:  []int64{0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allocateAcross(tt.total, tt.lines)
			assert.Equal(t, tt.want, got)

			var base int64
			for _, line := range tt.lines {
				if line.Amount > 0 {
					base += line.Amount
				}
			}
			wantSum := min(tt.total, base)
			if wantSum < 0 {
				wantSum = 0
			}
			assert.Equal(t, wantSum, sumOf(got),
				"the distributed shares must add up EXACTLY to the distributed total")
		})
	}
}

func TestAllocateAcrossNeverLosesTheLeftoverCent(t *testing.T) {
	// A set built from primes where no share divides evenly: every line's share
	// is rounded down, and if the leftover were not distributed the total would
	// fall short.
	lines := []allocLine{
		{"li_1", 101}, {"li_2", 103}, {"li_3", 107}, {"li_4", 109},
		{"li_5", 113}, {"li_6", 127}, {"li_7", 131},
	}
	for _, total := range []int64{1, 2, 7, 13, 99, 331, 790} {
		got := allocateAcross(total, lines)
		assert.Equal(t, total, sumOf(got),
			"the total %d must be distributed exactly; the leftover cent cannot be lost", total)
	}
}

func TestAllocateAcrossEmptySlice(t *testing.T) {
	assert.Empty(t, allocateAcross(100, nil))
	assert.Empty(t, allocateAcross(100, []allocLine{}))
}

func TestMulDivMod(t *testing.T) {
	tests := []struct {
		name                string
		a, b, d             int64
		quotient, remainder int64
	}{
		{name: "divides evenly", a: 100, b: 50, d: 10, quotient: 500, remainder: 0},
		{name: "with a remainder", a: 10, b: 3, d: 4, quotient: 7, remainder: 2},
		{name: "zero divisor", a: 10, b: 3, d: 0, quotient: 0, remainder: 0},
		{name: "zero factor", a: 0, b: 3, d: 4, quotient: 0, remainder: 0},
		{name: "negative input", a: -1, b: 3, d: 4, quotient: 0, remainder: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quotient, remainder := mulDivMod(tt.a, tt.b, tt.d)
			assert.Equal(t, tt.quotient, quotient)
			assert.Equal(t, tt.remainder, remainder)
		})
	}
}

func TestMulDivModIsCorrectEvenWhenTheIntermediateProductDoesNotFitInt64(t *testing.T) {
	// 10^12 × 10^12 = 10^24; the upper bound of an int64 is 9.22×10^18. A plain
	// multiplication would wrap and the result would come out silently wrong
	// (even negative).
	a, b, d := models.MaxAmount, models.MaxAmount, models.MaxAmount

	quotient, remainder := mulDivMod(a, b, d)

	assert.Equal(t, models.MaxAmount, quotient, "10^24 / 10^12 = 10^12")
	assert.Zero(t, remainder)
	assert.Positive(t, quotient, "a plain int64 multiplication would give a negative result here")
}

func TestMulDivModReturnsZeroWhenThePreconditionBreaks(t *testing.T) {
	// a > d: the quotient may not fit in an int64. The function does NOT panic;
	// it returns zero and the caller counts that as "no discount".
	quotient, remainder := mulDivMod(math.MaxInt64, math.MaxInt64, 1)

	assert.Zero(t, quotient, "when the precondition breaks, no discount that could not be computed is given")
	assert.Zero(t, remainder)
}

func TestPercentageOf(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		bps    int64
		want   int64
	}{
		{name: "an exact percentage", amount: 10000, bps: 2000, want: 2000},
		{name: "rounds down", amount: 999, bps: 2000, want: 199},
		{name: "rounds an exact half down", amount: 5, bps: 5000, want: 2},
		{name: "one hundred percent", amount: 1234, bps: 10000, want: 1234},
		{name: "zero rate", amount: 1234, bps: 0, want: 0},
		{name: "zero amount", amount: 0, bps: 5000, want: 0},
		{name: "negative amount", amount: -100, bps: 5000, want: 0},
		{name: "the rate is clipped to the upper bound", amount: 100, bps: 99999, want: 100},
		{name: "the amount is clipped to the upper bound", amount: models.MaxAmount + 1, bps: 10000, want: models.MaxAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, percentageOf(tt.amount, tt.bps))
		})
	}
}

func TestPercentageOfDoesNotOverflowAtTheMaximumAmount(t *testing.T) {
	got := percentageOf(models.MaxAmount, models.BasisPointDenominator)

	require.Positive(t, got)
	assert.Equal(t, models.MaxAmount, got,
		"the intermediate product is 10^16 and fits in an int64; had it overflowed, the result would have come out negative")
}

func TestLineStateChargeDoesNotExceedTheRemainder(t *testing.T) {
	line := &lineState{id: "li_1", amount: 1000, quantity: 1}

	assert.Equal(t, int64(400), line.charge(400))
	assert.Equal(t, int64(600), line.charge(900), "the remainder is 600; the excess is clipped")
	assert.Equal(t, int64(1000), line.discount)
	assert.Zero(t, line.charge(1), "no further discount is written onto an exhausted line")
	assert.Zero(t, line.remaining())
}
