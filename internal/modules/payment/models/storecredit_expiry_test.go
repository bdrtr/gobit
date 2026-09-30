package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// TestTheExpiryTakesBackWhatTheUnexpiredMoneyCannotAccountFor is ADR 0258's
// rule: credit is spent soonest-expiring first, so what the balance holds
// beyond the unexpired issues and the refunds is expired credit, up to what
// the expired issues gave and has not been taken back.
func TestTheExpiryTakesBackWhatTheUnexpiredMoneyCannotAccountFor(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		figures models.StoreCreditExpiryFigures
		due     int64
	}{
		"an expired 1000 beside an unexpired 500, 300 spent": {
			figures: models.StoreCreditExpiryFigures{Balance: 1_200, Unexpired: 500, Expired: 1_000},
			due:     700,
		},
		"the same balance once its expiry is written": {
			figures: models.StoreCreditExpiryFigures{Balance: 500, Unexpired: 500, Expired: 1_000, Written: 700},
			due:     0,
		},
		"spent beyond the expired credit, nothing left of it": {
			figures: models.StoreCreditExpiryFigures{Balance: 400, Unexpired: 500, Expired: 1_000},
			due:     0,
		},
		"a hold of 600 standing: only what is not held expires": {
			figures: models.StoreCreditExpiryFigures{Balance: 400, Expired: 1_000},
			due:     400,
		},
		"that hold released after the expiry took 400": {
			figures: models.StoreCreditExpiryFigures{Balance: 600, Expired: 1_000, Written: 400},
			due:     600,
		},
		"a refund after the expiry never expires": {
			figures: models.StoreCreditExpiryFigures{Balance: 200, Unexpired: 200, Expired: 1_000, Written: 1_000},
			due:     0,
		},
		"never more than the expired issues gave": {
			figures: models.StoreCreditExpiryFigures{Balance: 5_000, Expired: 1_000},
			due:     1_000,
		},
		"nothing expired": {
			figures: models.StoreCreditExpiryFigures{Balance: 800, Unexpired: 800},
			due:     0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.due, tc.figures.Due())
		})
	}
}
