package textfold_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/textfold"
)

// TestTheSQLTableIsTheGoFold holds SQLTranslate to Fold: each letter the SQL
// translates goes where Fold takes it, so lower(translate(v, from, to)) and
// Fold agree on every letter of the table.
func TestTheSQLTableIsTheGoFold(t *testing.T) {
	from, to := textfold.SQLTranslate()
	require.Equal(t, utf8.RuneCountInString(from), len(to), "one base per letter")

	bases := []rune(to)
	for i, letter := range []rune(from) {
		assert.Equal(t, string(bases[i]), textfold.Fold(string(letter)), "letter %U", letter)
	}
}

// TestEveryCapitalOfTheTableIsTranslated fails when a capital whose small
// letter is in the table is left to lower(), which folds it only on a
// cluster whose CTYPE knows it.
func TestEveryCapitalOfTheTableIsTranslated(t *testing.T) {
	from, _ := textfold.SQLTranslate()
	for _, letter := range from {
		capital, _ := utf8.DecodeRuneInString(strings.ToUpper(string(letter)))
		if capital > utf8.RuneSelf {
			assert.Contains(t, from, string(capital), "the capital of %U", letter)
		}
	}
	assert.Contains(t, from, "\u0130", "the dotted capital I, which Go lowers to an ASCII i")
}
