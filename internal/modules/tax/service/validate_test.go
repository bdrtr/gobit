package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestNormalizeCountryCode checks the country code normalization.
func TestNormalizeCountryCode(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		// A slice is used, not a map: some of the inputs carry whitespace ON
		// PURPOSE (trimming is tested), and map keys with whitespace would look
		// like typos when read.
		tests := []struct{ in, want string }{
			{"TR", "TR"},
			{"tr", "TR"},
			{" de ", "DE"},
			{"\tus\n", "US"},
		}
		for _, tt := range tests {
			got, err := NormalizeCountryCode(tt.in)
			require.NoError(t, err, "input: %q", tt.in)
			assert.Equal(t, tt.want, got)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		// "T\u00dc" is a T followed by a U with diaeresis (U+00DC): two runes, one
		// of them a letter outside ASCII.
		for _, in := range []string{"", "T", "TUR", "T1", "T-", "T R", "T\u00dc"} {
			_, err := NormalizeCountryCode(in)
			require.Error(t, err, "input: %q must not be accepted", in)
			assert.True(t, errors.IsInvalid(err))
		}
	})

	// This is a real trap: Unicode's simple upper-case mapping moves the
	// dotless i (U+0131) onto the ASCII "I". Had the check been made AFTER the
	// conversion, a dotless i followed by "s" would silently become "IS"
	// (Iceland). The inputs are the dotless i (U+0131) followed by "s", "ſe",
	// and the dotted capital I (U+0130) followed by "s".
	t.Run("a non-ASCII letter cannot slip through by being upper-cased", func(t *testing.T) {
		for _, in := range []string{"\u0131s", "ſe", "\u0130s"} {
			_, err := NormalizeCountryCode(in)
			require.Error(t, err, "input: %q", in)
		}
	})
}

// TestNormalizeProvinceCode checks the province code normalization.
func TestNormalizeProvinceCode(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		tests := []struct{ in, want string }{
			{"", ""},
			{"  ", ""},
			{"ca", "CA"},
			{"34", "34"},
			{"BC-1", "BC-1"},
			{"NL2A", "NL2A"},
			{"ABCDEFGHIJ", "ABCDEFGHIJ"},
		}
		for _, tt := range tests {
			got, err := NormalizeProvinceCode(tt.in)
			require.NoError(t, err, "input: %q", tt.in)
			assert.Equal(t, tt.want, got)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		// "K\u0130" is a K followed by the dotted capital I (U+0130), a letter
		// outside ASCII.
		for _, in := range []string{"-CA", "C A", "CA!", "ABCDEFGHIJK", "K\u0130"} {
			_, err := NormalizeProvinceCode(in)
			require.Error(t, err, "input: %q must not be accepted", in)
			assert.True(t, errors.IsInvalid(err))
		}
	})

	t.Run("consistent with the database constraint", func(t *testing.T) {
		assert.Equal(t, 10, models.MaxProvinceCodeLength,
			"the bound has to be the same as the CHECK in the migration; if they diverge the service cannot write what it accepts")
	})
}

// TestRequireIDChecksThePrefix checks that an id of the wrong kind produces a
// validation error (not a 404).
func TestRequireIDChecksThePrefix(t *testing.T) {
	require.NoError(t, requireID(trRegionID, models.TaxRegionIDPrefix, "region"))

	for _, id := range []string{
		"",
		" " + trRegionID,
		trRegionID + " ",
		rateA,
		"reg_0000",
	} {
		err := requireID(id, models.TaxRegionIDPrefix, "region")
		require.Error(t, err, "id: %q", id)
		assert.True(t, errors.IsInvalid(err))
	}

	tooLong := models.TaxRegionIDPrefix
	for range maxIDLen {
		tooLong += "X"
	}
	require.Error(t, requireID(tooLong, models.TaxRegionIDPrefix, "region"))
}

// TestRequireReferenceIDDoesNotCheckThePrefix checks that foreign ids are NOT
// subject to the prefix requirement.
//
// The rule is deliberate: the id belongs to another module, and repeating that
// module's prefix contract here would mean tax silently refusing rules when
// the prefix changed.
func TestRequireReferenceIDDoesNotCheckThePrefix(t *testing.T) {
	require.NoError(t, requireReferenceID("prod_1"))
	require.NoError(t, requireReferenceID("any-id-at-all"))

	require.Error(t, requireReferenceID(""))
	require.Error(t, requireReferenceID(" prod_1"))
}

// TestNormalizeCode checks the reconciliation code validation.
func TestNormalizeCode(t *testing.T) {
	got, err := normalizeCode("  KDV20  ")
	require.NoError(t, err)
	assert.Equal(t, "KDV20", got)

	got, err = normalizeCode("   ")
	require.NoError(t, err)
	assert.Empty(t, got, "a code made only of whitespace means the ABSENCE of a code")

	for _, in := range []string{"KDV 20", "KDV\t20", "KDV\n20"} {
		_, err := normalizeCode(in)
		require.Error(t, err, "input: %q", in)
	}
}

// TestNormalizePagingBounds checks the paging clamp.
func TestNormalizePagingBounds(t *testing.T) {
	limit, offset, err := normalizePaging(0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit)
	assert.Equal(t, int32(0), offset)

	limit, _, err = normalizePaging(MaxLimit+1, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, limit)

	_, _, err = normalizePaging(10, -1)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestClampToInt32 checks clamping to the int32 bound.
func TestClampToInt32(t *testing.T) {
	assert.Equal(t, int32(5), clampToInt32(5))
	assert.Equal(t, int32(math.MaxInt32), clampToInt32(math.MaxInt64))
	assert.Equal(t, int32(math.MinInt32), clampToInt32(math.MinInt64))
}
