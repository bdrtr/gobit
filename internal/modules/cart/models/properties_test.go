package models_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// TestLinePropertiesAreTrimmedAndBounded is ADR 0223: names and texts are
// trimmed, none is nil, and each bound is refused one past it.
func TestLinePropertiesAreTrimmedAndBounded(t *testing.T) {
	t.Parallel()

	none, err := models.NormalizeLineProperties(map[string]string{})
	require.NoError(t, err)
	assert.Nil(t, none)

	clean, err := models.NormalizeLineProperties(map[string]string{" Engraving ": " For Anna "})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Engraving": "For Anna"}, clean)

	most := map[string]string{}
	for i := range models.MaxLineProperties {
		most[fmt.Sprintf("p%d", i)] = "x"
	}
	_, err = models.NormalizeLineProperties(most)
	require.NoError(t, err, "the most properties")
	_, err = models.NormalizeLineProperties(map[string]string{
		strings.Repeat("n", models.MaxPropertyNameLength): strings.Repeat("é", models.MaxPropertyValueLength),
	})
	require.NoError(t, err, "the longest name and text, counted in characters")

	// The second name is the first with a space before it, which trimming
	// makes the same name.
	padded := " Engraving"
	twice := map[string]string{"Engraving": "a", padded: "b"}
	tooMany := map[string]string{"extra": "x"}
	for key, value := range most {
		tooMany[key] = value
	}
	for name, properties := range map[string]map[string]string{
		"too many":            tooMany,
		"an empty name":       {"  ": "x"},
		"a name too long":     {strings.Repeat("n", models.MaxPropertyNameLength+1): "x"},
		"an empty text":       {"Engraving": "   "},
		"a text too long":     {"Engraving": strings.Repeat("x", models.MaxPropertyValueLength+1)},
		"a control character": {"Engraving": "line\nbreak"},
		"a name twice":        twice,
	} {
		_, err := models.NormalizeLineProperties(properties)
		require.Error(t, err, name)
		assert.Equal(t, models.CodePropertiesInvalid, errors.CodeOf(err), name)
	}
}
