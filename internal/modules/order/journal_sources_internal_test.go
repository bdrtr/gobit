package order

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
)

// TestTheJournalsOtherModulesAreReadOnlyWhenInstalled holds the two wrappers
// the order journal reads other modules through (ADR 0189, ADR 0419): without
// the module the answer is an empty list and no error, so the journal books no
// refund and no tax correction, and a registration that does not satisfy the
// surface is this module's own setup fault, so the books are not read as if
// the module were absent.
func TestTheJournalsOtherModulesAreReadOnlyWhenInstalled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	from := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)

	raw, err := (&documentedTax{c: container.New(nil), log: log}).DocumentedTaxJSON(ctx, from, to, "")
	require.NoError(t, err, "without the invoice module the journal books no correction")
	assert.JSONEq(t, `[]`, string(raw))
	raw, err = (&causedRefunds{c: container.New(nil), log: log}).CausedRefundsByIDJSON(ctx, []string{"re_1"})
	require.NoError(t, err, "without the payment module no refund was made")
	assert.JSONEq(t, `[]`, string(raw))

	c := container.New(nil)
	require.NoError(t, c.Provide(DocumentedTaxName, "not an invoice surface"))
	_, err = (&documentedTax{c: c, log: log}).DocumentedTaxJSON(ctx, from, to, "")
	require.Error(t, err)
	assert.Equal(t, codeSetupFailed, errors.CodeOf(err), "a wrong registration names this module")

	c = container.New(nil)
	require.NoError(t, c.Provide(CausedRefundsName, "not a payment surface"))
	_, err = (&causedRefunds{c: c, log: log}).CausedRefundsByIDJSON(ctx, []string{"re_1"})
	require.Error(t, err)
	assert.Equal(t, codeSetupFailed, errors.CodeOf(err), "a wrong registration names this module")
}
