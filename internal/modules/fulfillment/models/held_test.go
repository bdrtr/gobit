package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// TestWhatALineHolds is the held rule (ADR 0423): every live unit, and of the
// units that came back, as many as are spoken for, never more than came
// back and never fewer than none.
func TestWhatALineHolds(t *testing.T) {
	t.Parallel()

	units := models.HeldUnits{Live: 3, Back: 2}
	for spoken, want := range map[int64]int64{-1: 3, 0: 3, 1: 4, 2: 5, 7: 5} {
		assert.Equal(t, want, units.Held(spoken), "spoken for %d", spoken)
	}
}
