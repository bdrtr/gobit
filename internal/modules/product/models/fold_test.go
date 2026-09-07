package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// TestTheFoldMatchesTheSpellingsOfOneValue is what A18 decided the filter for.
//
// A merchant types a color once and a shopper types it another way. The four
// spellings below are the same color, and the whole point of the folded form is
// that they arrive at one string. The letters are written as \u escapes so this
// file carries no Turkish letter of its own (ADR 0012): U+0131 is the dotless i
// and U+0130 the dotted capital I. The Cyrillic and CJK values below need no
// escaping — the ratchet reads for TURKISH, and those letters are the data this
// fold exists to preserve.
func TestTheFoldMatchesTheSpellingsOfOneValue(t *testing.T) {
	t.Parallel()

	for _, spelling := range []string{
		"K\u0131rm\u0131z\u0131", "KIRMIZI", "kirmizi", "  Kirmizi  ", "K\u0130RM\u0131Z\u0131",
	} {
		assert.Equal(t, "kirmizi", models.FoldOptionValue(spelling),
			"%q is the same color as the others and has to fold to the same bytes", spelling)
	}
}

// TestTheFoldKeepsWhatItCannotTransliterate is the property that separates this
// fold from slugify, and the reason A18 was not answered with the fold this
// repository already had.
//
// slugify drops a rune with no ASCII base, because a URL handle must be ASCII.
// Applied to option values that would put every Cyrillic and every CJK value in
// an option at the SAME folded form — and since the folded form carries a UNIQUE
// index per option, the second such value is not merely mismatched, it is
// REFUSED. Measured on the real schema: with slugify's result, inserting two
// Cyrillic colors into one option fails on
// product_option_value_folded_uniq.
//
// So the requirement is not "fold as far as possible". It is: two spellings of
// one value must meet, and two different values must NOT.
func TestTheFoldKeepsWhatItCannotTransliterate(t *testing.T) {
	t.Parallel()

	// Two different Cyrillic colors: red and blue.
	red := "красный"
	blue := "синий"

	assert.NotEmpty(t, models.FoldOptionValue(red),
		"a value the fold cannot transliterate must survive it; an empty folded form makes "+
			"every such value equal to every other")
	assert.NotEqual(t, models.FoldOptionValue(red), models.FoldOptionValue(blue),
		"two different colors must not fold together — under slugify both become \"\" and the "+
			"unique index refuses the second, so a catalog in this script cannot be migrated")

	// The case fold still applies to them, which is a gain over an exact match.
	assert.Equal(t, models.FoldOptionValue(red), models.FoldOptionValue("КРАСНЫЙ"),
		"the upper-case spelling of the same word must still meet it")

	// A CJK value has no case and no ASCII base; it must pass through untouched.
	assert.Equal(t, "赤", models.FoldOptionValue("赤"))
}

// TestTheFoldDoesNotReshapeTheText holds the line between a MATCHING form and a
// handle.
//
// slugify turns spaces into dashes because a handle may not contain one. Two
// option values that differ only in that way are values a merchant may have meant
// to keep apart, and merging them would report one value's products under
// another's name.
func TestTheFoldDoesNotReshapeTheText(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, models.FoldOptionValue("Navy Blue"), models.FoldOptionValue("navy-blue"),
		"a space is not a dash; this fold changes case and marks and nothing else")
	assert.Equal(t, "navy blue", models.FoldOptionValue("Navy Blue"))
}
