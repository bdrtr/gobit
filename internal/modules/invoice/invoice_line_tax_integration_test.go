//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the SCHEMA's half of a document row's tax breakdown: the
// constraints that hold one component at a time, and the ordering a printed
// document depends on. The unit tests prove the service's decisions against a
// fake, and a fake cannot disagree with a constraint it does not have.
package invoice_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// stackedIssueFor is issueFor with the row taxed at 5% + 8% compound.
//
// The amounts are the ones the tax module produces on a base of 2000: 100 and
// 168, adding to the 268 the row then carries.
func stackedIssueFor(prefix string) service.IssueInput {
	in := issueFor(prefix)
	in.Lines[0].TaxRateBps = 500
	in.Lines[0].TaxTotal = 268
	in.Lines[0].Total = 2268
	in.Lines[0].TaxComponents = []service.LineTaxInput{
		{RateID: "txr_base", RateBps: 500, TaxableAmount: 2000, TaxAmount: 100},
		{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 2100, TaxAmount: 168},
	}
	in.TaxTotal = 268
	in.Total = 2268
	return in
}

// countOfRows runs a single-column counting query.
func countOfRows(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&count))
	return count
}

// TestABreakdownSurvivesTheRoundTripOnTheRealSchema proves the components are
// written with their row, read back in printed order, and hung on the right
// row.
func TestABreakdownSurvivesTheRoundTripOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	issued, err := svc.Issue(ctx, stackedIssueFor("LTX"))
	require.NoError(t, err)

	read, err := svc.GetInvoice(ctx, issued.ID)
	require.NoError(t, err)
	require.Len(t, read.Lines, 1)

	components := read.Lines[0].TaxComponents
	require.Len(t, components, 2)

	assert.Equal(t, int32(1), components[0].Position, "a document counts from one")
	assert.Equal(t, "txr_base", components[0].RateID)
	assert.Equal(t, int64(100), components[0].TaxAmount)
	assert.False(t, components[0].Compound)

	assert.Equal(t, int32(2), components[1].Position)
	assert.Equal(t, int32(800), components[1].RateBps)
	assert.Equal(t, int64(2100), components[1].TaxableAmount)
	assert.Equal(t, int64(168), components[1].TaxAmount)
	assert.True(t, components[1].Compound)

	assert.Equal(t, read.Lines[0].TaxTotal,
		components[0].TaxAmount+components[1].TaxAmount)
}

// TestARowWithOneRateStoresNoComponentRow proves the absence is real rather
// than hidden by the reader.
func TestARowWithOneRateStoresNoComponentRow(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	issued, err := svc.Issue(ctx, issueFor("LT1"))
	require.NoError(t, err)

	read, err := svc.GetInvoice(ctx, issued.ID)
	require.NoError(t, err)
	require.Len(t, read.Lines, 1)
	assert.Empty(t, read.Lines[0].TaxComponents)

	assert.Zero(t, countOfRows(ctx, t,
		`SELECT count(*) FROM invoice_line_taxes WHERE invoice_line_id = $1`,
		read.Lines[0].ID), "no row may be written for a single-rate line")
}

// rowOfADocument returns the id of the single row of a fresh document.
func rowOfADocument(ctx context.Context, t *testing.T, prefix string) string {
	t.Helper()

	issued, err := newService(t).Issue(ctx, issueFor(prefix))
	require.NoError(t, err)
	require.Len(t, issued.Lines, 1)
	return issued.Lines[0].ID
}

// TestTheComponentConstraintsHoldAgainstDirectSQL proves the service check is
// not the last defence.
func TestTheComponentConstraintsHoldAgainstDirectSQL(t *testing.T) {
	ctx := context.Background()

	for name, tc := range map[string]struct {
		prefix     string
		position   int32
		rateBps    int32
		compound   bool
		taxable    int64
		tax        int64
		constraint string
	}{
		"a rate above 100%": {
			prefix: "LC1", position: 1, rateBps: 10_001, taxable: 2000, tax: 100,
			constraint: "invoice_line_taxes_rate_bps_range",
		},
		"a component taking more than its base": {
			prefix: "LC2", position: 1, rateBps: 500, taxable: 50, tax: 900,
			constraint: "invoice_line_taxes_within_base",
		},
		"a first component that compounds": {
			prefix: "LC3", position: 1, rateBps: 500, compound: true, taxable: 2000, tax: 100,
			constraint: "invoice_line_taxes_compound_needs_base",
		},
		"a position below one": {
			prefix: "LC4", position: 0, rateBps: 500, taxable: 2000, tax: 100,
			constraint: "invoice_line_taxes_position_positive",
		},
	} {
		t.Run(name, func(t *testing.T) {
			lineID := rowOfADocument(ctx, t, tc.prefix)

			_, err := testPool.Pool().Exec(ctx,
				`INSERT INTO invoice_line_taxes
                 (id, invoice_line_id, position, rate_id, rate_bps, compound, taxable_amount, tax_amount)
                 VALUES ($1, $2, $3, '', $4, $5, $6, $7)`,
				models.NewLineTaxID(), lineID, tc.position, tc.rateBps, tc.compound,
				tc.taxable, tc.tax)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.constraint)
		})
	}
}

// TestTwoComponentsCannotShareAPosition proves the printed order cannot be made
// ambiguous.
func TestTwoComponentsCannotShareAPosition(t *testing.T) {
	ctx := context.Background()
	lineID := rowOfADocument(ctx, t, "LP1")

	insert := func() error {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO invoice_line_taxes
             (id, invoice_line_id, position, rate_id, rate_bps, compound, taxable_amount, tax_amount)
             VALUES ($1, $2, 1, '', 500, FALSE, 2000, 100)`,
			models.NewLineTaxID(), lineID)
		return err
	}

	require.NoError(t, insert())
	err := insert()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invoice_line_taxes_position_uniq")
}

// TestTheBreakdownHangsFromItsRow proves the foreign key is really built and
// that it points INSIDE the module.
func TestTheBreakdownHangsFromItsRow(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO invoice_line_taxes
         (id, invoice_line_id, position, rate_id, rate_bps, compound, taxable_amount, tax_amount)
         VALUES ($1, 'invline_does_not_exist', 1, '', 500, FALSE, 2000, 100)`,
		models.NewLineTaxID())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invoice_line_taxes_invoice_line_id_fkey")
}
