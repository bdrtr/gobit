package models_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// TestAGiftCardCodeIsPrintedInGroups and reads back as itself.
func TestAGiftCardCodeIsPrintedInGroups(t *testing.T) {
	t.Parallel()

	printed := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`)
	seen := map[string]bool{}
	for range 200 {
		code := models.NewGiftCardCode()
		require.Regexp(t, printed, code)
		assert.False(t, seen[code], "a code was drawn twice")
		seen[code] = true

		normalized, ok := models.NormalizeGiftCardCode(code)
		require.True(t, ok)
		assert.Len(t, normalized, models.GiftCardCodeLength)
		assert.Equal(t, code[len(code)-4:], models.GiftCardCodeTail(normalized))
	}
}

// TestAGiftCardCodeIsReadAsAPersonTypesIt: case, dashes and spaces do not
// matter, and the letters that look like digits are read as those digits.
func TestAGiftCardCodeIsReadAsAPersonTypesIt(t *testing.T) {
	t.Parallel()

	for typed, want := range map[string]string{
		"ABCD-EFGH-JKMN-PQ10": "ABCDEFGHJKMNPQ10",
		"abcd efgh jkmn pq10": "ABCDEFGHJKMNPQ10",
		"ABCDEFGHJKMNPQIO":    "ABCDEFGHJKMNPQ10",
		"abcdefghjkmnpqlo":    "ABCDEFGHJKMNPQ10",
	} {
		got, ok := models.NormalizeGiftCardCode(typed)
		require.True(t, ok, typed)
		assert.Equal(t, want, got, typed)
	}

	for _, typed := range []string{"", "ABCD-EFGH-JKMN", "ABCD-EFGH-JKMN-PQRSX", "ABCD-EFGH-JKMN-PQRU", "ABCD-EFGH-JKMN-PQR!"} {
		_, ok := models.NormalizeGiftCardCode(typed)
		assert.False(t, ok, "%q is not a code", typed)
	}
}

// TestAGiftCardCodeIsKeptAsItsDigest: what the schema's CHECK expects.
func TestAGiftCardCodeIsKeptAsItsDigest(t *testing.T) {
	t.Parallel()

	digest := models.GiftCardCodeDigest("ABCDEFGHJKMNPQ10")

	assert.Regexp(t, `^[0-9a-f]{64}$`, digest)
	assert.Equal(t, digest, models.GiftCardCodeDigest("ABCDEFGHJKMNPQ10"))
	assert.NotEqual(t, digest, models.GiftCardCodeDigest("ABCDEFGHJKMNPQ11"))
}
