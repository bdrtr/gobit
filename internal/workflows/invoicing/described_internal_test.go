package invoicing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestARowIsDescribedByItsProductAndVariant is ADR 0365's rule for a row: the
// product and the variant together, the variant alone on a line that kept no
// product title, and a title the two share printed once.
func TestARowIsDescribedByItsProductAndVariant(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ product, variant, want string }{
		{"Kenya AA", "1 kg / Filter", "Kenya AA — 1 kg / Filter"},
		{"", "1 kg / Filter", "1 kg / Filter"},
		{"Chemex 6 Cup", "Chemex 6 Cup", "Chemex 6 Cup"},
		{" Kenya AA ", " ", "Kenya AA"},
	} {
		assert.Equal(t, c.want, describedAs(c.product, c.variant), "%q, %q", c.product, c.variant)
	}
}
