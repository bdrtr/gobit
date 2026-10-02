package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// TestNormalizeCurrencyCode proves the acceptance and rejection rules of an ISO
// 4217 code.
func TestNormalizeCurrencyCode(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		cases := []struct{ input, want string }{
			{input: "TRY", want: "TRY"},
			{input: "try", want: "TRY"},
			{input: "TrY", want: "TRY"},
			{input: "  usd  ", want: "USD"},
			{input: "\tjpy\n", want: "JPY"},
		}
		for _, tc := range cases {
			got, err := NormalizeCurrencyCode(tc.input)
			require.NoError(t, err, "input: %q", tc.input)
			assert.Equal(t, tc.want, got, "input: %q", tc.input)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		// "₺₺₺" is three RUNES and nine bytes: measured in runes it passes the
		// length check and is refused by the letter check, which names the
		// reason; measured in bytes it would be refused as the wrong length.
		// Two runes of three bytes ("\u015fa") are refused as the wrong length,
		// which measured in bytes they would pass. The reasons are asserted
		// below.
		//
		// The dotless i (U+0131) followed by "ls", and "ſek", contain NON-ASCII
		// letters, but Unicode's simple upper-case mapping moves them onto
		// "ILS" and "SEK" — two REAL currencies in the seed. Had the ASCII check
		// been made after the conversion, both would silently pass and the
		// function's contract would break.
		for _, input := range []string{
			"", "TR", "TRYX", "TR1", "T RY", "₺₺₺", "TR-", "123", "\u0131ls", "ſek",
		} {
			_, err := NormalizeCurrencyCode(input)
			require.Error(t, err, "input: %q", input)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "input: %q", input)
			assert.Equal(t, CodeInvalidInput, errors.CodeOf(err), "input: %q", input)
		}

		_, err := NormalizeCurrencyCode("₺₺₺")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can only contain ASCII letters", "three runes are the right length")
		_, err = NormalizeCurrencyCode("\u015fa")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has to be exactly 3 letters", "two runes are not, whatever their bytes")
	})
}

// TestNormalizeCountryCode proves the acceptance and rejection rules of an ISO
// 3166-1 alpha-2 code.
func TestNormalizeCountryCode(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		cases := []struct{ input, want string }{
			{input: "TR", want: "TR"},
			{input: "tr", want: "TR"},
			{input: " de ", want: "DE"},
			{input: "Us", want: "US"},
		}
		for _, tc := range cases {
			got, err := NormalizeCountryCode(tc.input)
			require.NoError(t, err, "input: %q", tc.input)
			assert.Equal(t, tc.want, got, "input: %q", tc.input)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		// The C with cedilla and G with breve pair (U+00C7 U+011E) is still not
		// ASCII after the upper-case conversion, so it is rejected whatever the
		// order of the checks. The dotless i (U+0131) followed by "s", and "ſe",
		// are the exact opposite: the simple upper-case mapping turns them into
		// "IS" (Iceland) and "SE" (Sweden), so had the check been made after the
		// conversion they would become valid ISO codes and resolve real
		// countries in the seed.
		for _, input := range []string{"", "T", "TUR", "T1", "1R", "\u00c7\u011e", "T-", "\u0131s", "ſe"} {
			_, err := NormalizeCountryCode(input)
			require.Error(t, err, "input: %q", input)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "input: %q", input)
		}
	})
}

// TestNormalizeName proves that a region name is trimmed, and that empty names
// and names containing control characters are rejected.
func TestNormalizeName(t *testing.T) {
	got, err := normalizeName("  European Union  ")
	require.NoError(t, err)
	assert.Equal(t, "European Union", got)

	for _, input := range []string{"", "   ", "\t\n", "Name\nLine", "Name\x00"} {
		_, err := normalizeName(input)
		require.Error(t, err, "input: %q", input)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "input: %q", input)
	}

	_, err = normalizeName(strings.Repeat("a", maxNameLen+1))
	require.Error(t, err, "an overly long name has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestValidateTaxRate proves the bounds of the tax rate.
func TestValidateTaxRate(t *testing.T) {
	for _, rate := range []int32{models.MinTaxRate, 1, 2000, models.MaxTaxRate} {
		require.NoError(t, validateTaxRate(rate), "rate: %d", rate)
	}
	for _, rate := range []int32{-1, models.MaxTaxRate + 1, 1 << 20} {
		err := validateTaxRate(rate)
		require.Error(t, err, "rate: %d", rate)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "rate: %d", rate)
	}
}

// TestRequireRegionID proves the prefix, whitespace and length rules of id
// validation.
func TestRequireRegionID(t *testing.T) {
	require.NoError(t, requireRegionID(models.RegionIDPrefix+"01ABCDEF"))

	for _, id := range []string{
		"",
		" reg_1",
		"reg_1 ",
		"prod_1",
		"cust_1",
		"1",
		models.RegionIDPrefix + strings.Repeat("a", maxIDLen),
	} {
		err := requireRegionID(id)
		require.Error(t, err, "id: %q", id)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "id: %q", id)
	}
}

// TestClampToInt32 proves that clamping to the int32 range does not produce a
// wraparound.
//
// The Query layer's limit field is an int; on a 64-bit platform a huge value
// coming from there would WRAP to a negative limit if it were converted
// directly.
func TestClampToInt32(t *testing.T) {
	assert.Equal(t, int32(5), clampToInt32(5))
	assert.Equal(t, int32(2147483647), clampToInt32(1<<40))
	assert.Equal(t, int32(-2147483648), clampToInt32(-(1 << 40)))
}

// TestNormalizePaging proves that the paging bounds are applied.
func TestNormalizePaging(t *testing.T) {
	limit, offset, err := normalizePaging(0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit)
	assert.Zero(t, offset)

	limit, _, err = normalizePaging(-5, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit, "a negative limit has to fall back to the default")

	limit, _, err = normalizePaging(MaxLimit+1, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, limit)

	limit, offset, err = normalizePaging(10, 20)
	require.NoError(t, err)
	assert.Equal(t, int32(10), limit)
	assert.Equal(t, int32(20), offset)

	_, _, err = normalizePaging(10, -1)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}
