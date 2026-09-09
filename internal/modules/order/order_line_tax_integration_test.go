//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the SCHEMA's half of a line's tax breakdown: the
// constraints that hold one component at a time, and the ordering the read
// depends on. The unit tests prove the service's decisions against a fake, and a
// fake cannot disagree with a constraint it does not have.
package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// stackedInput is validInput with the line taxed at 5% + 8% compound.
//
// The amounts are the ones the tax module produces on a base of 3000: 150 and
// 252, adding to the 402 the line then carries.
func stackedInput() service.CreateOrderInput {
	in := validInput()
	in.TaxTotal = 402
	in.Total = 3000 + 402 + 2500
	in.Items[0].TaxTotal = 402
	in.Items[0].TaxRateBps = 500
	in.Items[0].Total = 3402
	in.Items[0].TaxComponents = []service.CreateOrderLineTaxInput{
		{RateID: "txr_base", RateBps: 500, TaxableAmount: 3000, TaxAmount: 150},
		{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 3150, TaxAmount: 252},
	}
	return in
}

// TestABreakdownSurvivesTheRoundTripOnTheRealSchema proves the components are
// written, read back in stack order, and hung on the right line.
//
// The ORDER is the point: a compound component's base is everything below it, so
// a breakdown read out of order cannot be reproduced, and the ordering lives in
// the query rather than in the caller.
func TestABreakdownSurvivesTheRoundTripOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, stackedInput())
	require.NoError(t, err)

	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)

	components := detail.Items[0].TaxComponents
	require.Len(t, components, 2)

	assert.Equal(t, int32(0), components[0].Position)
	assert.Equal(t, "txr_base", components[0].RateID)
	assert.Equal(t, int64(150), components[0].TaxAmount)
	assert.False(t, components[0].Compound)

	assert.Equal(t, int32(1), components[1].Position)
	assert.Equal(t, "txr_top", components[1].RateID)
	assert.Equal(t, int64(3150), components[1].TaxableAmount)
	assert.Equal(t, int64(252), components[1].TaxAmount)
	assert.True(t, components[1].Compound)

	assert.Equal(t, detail.Items[0].TaxTotal,
		components[0].TaxAmount+components[1].TaxAmount)
}

// TestALineWithOneRateStoresNoComponentRow proves the absence is real and not
// just hidden by the reader.
func TestALineWithOneRateStoresNoComponentRow(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Empty(t, detail.Items[0].TaxComponents)

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM order_line_taxes WHERE order_line_item_id = $1`,
		detail.Items[0].ID), "no row may be written for a single-rate line")
}

// countOf runs a single-column counting query.
func countOf(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&count))
	return count
}

// lineOfAnOrder returns the id of the single line of a freshly placed order.
func lineOfAnOrder(ctx context.Context, t *testing.T) string {
	t.Helper()

	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	return detail.Items[0].ID
}

// TestTheComponentConstraintsHoldAgainstDirectSQL proves the service check is
// not the last defence.
func TestTheComponentConstraintsHoldAgainstDirectSQL(t *testing.T) {
	ctx := context.Background()

	for name, tc := range map[string]struct {
		position   int32
		rateBps    int32
		compound   bool
		taxable    int64
		tax        int64
		constraint string
	}{
		"a rate above 100%": {
			position: 0, rateBps: 10_001, taxable: 3000, tax: 100,
			constraint: "order_line_taxes_rate_bps_range",
		},
		"a component taking more than its base": {
			position: 0, rateBps: 500, taxable: 100, tax: 900,
			constraint: "order_line_taxes_within_base",
		},
		"a first component that compounds": {
			position: 0, rateBps: 500, compound: true, taxable: 3000, tax: 150,
			constraint: "order_line_taxes_compound_needs_base",
		},
		"a negative position": {
			position: -1, rateBps: 500, taxable: 3000, tax: 150,
			constraint: "order_line_taxes_position_nonneg",
		},
	} {
		t.Run(name, func(t *testing.T) {
			lineID := lineOfAnOrder(ctx, t)

			_, err := testPool.Pool().Exec(ctx,
				`INSERT INTO order_line_taxes
                 (id, order_line_item_id, position, rate_id, rate_bps, compound, taxable_amount, tax_amount)
                 VALUES ($1, $2, $3, '', $4, $5, $6, $7)`,
				models.NewLineTaxID(), lineID, tc.position, tc.rateBps, tc.compound,
				tc.taxable, tc.tax)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.constraint)
		})
	}
}

// TestTwoComponentsCannotShareAPosition proves the ordering cannot be made
// ambiguous.
//
// A compound component's base is defined by what is below it, so two components
// in the same place would leave the figures unreproducible.
func TestTwoComponentsCannotShareAPosition(t *testing.T) {
	ctx := context.Background()
	lineID := lineOfAnOrder(ctx, t)

	insert := func() error {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO order_line_taxes
             (id, order_line_item_id, position, rate_id, rate_bps, compound, taxable_amount, tax_amount)
             VALUES ($1, $2, 0, '', 500, FALSE, 3000, 150)`,
			models.NewLineTaxID(), lineID)
		return err
	}

	require.NoError(t, insert())
	err := insert()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order_line_taxes_position_uniq")
}

// TestTheBreakdownHangsFromItsLine proves the foreign key is really built and
// that it points INSIDE the module.
func TestTheBreakdownHangsFromItsLine(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO order_line_taxes
         (id, order_line_item_id, position, rate_id, rate_bps, compound, taxable_amount, tax_amount)
         VALUES ($1, 'oli_does_not_exist', 0, '', 500, FALSE, 3000, 150)`,
		models.NewLineTaxID())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "order_line_taxes_order_line_item_id_fkey")
}
