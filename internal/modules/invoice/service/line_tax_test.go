package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// stackedIssue is validIssue with the row taxed at 5% + 8% compound.
//
// The amounts are the ones the tax module produces on a base of 2000: 100 and
// 168, adding to the 268 the row then carries.
func stackedIssue() service.IssueInput {
	in := validIssue()
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

// TestADocumentPrintsEveryRateThatTaxedARow is what this whole chain was for.
//
// Before it, a row charged under 5% + 8% printed "5%" and the buyer's own
// arithmetic contradicted the document.
func TestADocumentPrintsEveryRateThatTaxedARow(t *testing.T) {
	t.Parallel()

	issued, err := newService(newFakeRepo()).Issue(context.Background(), stackedIssue())
	require.NoError(t, err)
	require.Len(t, issued.Lines, 1)

	components := issued.Lines[0].TaxComponents
	require.Len(t, components, 2)

	assert.True(t, strings.HasPrefix(components[0].ID, models.LineTaxIDPrefix))
	assert.Equal(t, issued.Lines[0].ID, components[0].InvoiceLineID)

	assert.Equal(t, int32(1), components[0].Position,
		"a document counts its components from one, like its rows")
	assert.Equal(t, "txr_base", components[0].RateID)
	assert.Equal(t, int32(500), components[0].RateBps)
	assert.False(t, components[0].Compound)
	assert.Equal(t, int64(2000), components[0].TaxableAmount)
	assert.Equal(t, int64(100), components[0].TaxAmount)

	assert.Equal(t, int32(2), components[1].Position)
	assert.Equal(t, int32(800), components[1].RateBps)
	assert.True(t, components[1].Compound)
	assert.Equal(t, int64(2100), components[1].TaxableAmount,
		"the compound component's base is the row plus the tax below it")
	assert.Equal(t, int64(168), components[1].TaxAmount)

	assert.Equal(t, issued.Lines[0].TaxTotal,
		components[0].TaxAmount+components[1].TaxAmount)
	assert.Equal(t, int32(500), issued.Lines[0].TaxRateBps,
		"the row keeps the stack's BASE rate beside the breakdown")
}

// TestARowWithOneRateCarriesNoBreakdown keeps the absence meaningful: a renderer
// reads "there are components" as "print the breakdown".
func TestARowWithOneRateCarriesNoBreakdown(t *testing.T) {
	t.Parallel()

	issued, err := newService(newFakeRepo()).Issue(context.Background(), validIssue())
	require.NoError(t, err)
	require.Len(t, issued.Lines, 1)
	assert.Nil(t, issued.Lines[0].TaxComponents)
}

// TestTheComponentsHaveToAddUpToTheRow is the LAST gate on the identity: what
// gets past it is printed and handed to a buyer.
func TestTheComponentsHaveToAddUpToTheRow(t *testing.T) {
	t.Parallel()

	in := stackedIssue()
	in.Lines[0].TaxComponents[1].TaxAmount = 167

	_, err := newService(newFakeRepo()).Issue(context.Background(), in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "do not add up to its tax")
}

// TestABreakdownOfOneIsRefused keeps the empty list the only way to say "one
// rate applied".
func TestABreakdownOfOneIsRefused(t *testing.T) {
	t.Parallel()

	in := validIssue()
	in.Lines[0].TaxComponents = []service.LineTaxInput{
		{RateBps: 2000, TaxableAmount: 2000, TaxAmount: 400},
	}

	_, err := newService(newFakeRepo()).Issue(context.Background(), in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "says nothing the row does not")
}

// TestMoreComponentsThanCanBePrintedAreRefused bounds one row's breakdown.
func TestMoreComponentsThanCanBePrintedAreRefused(t *testing.T) {
	t.Parallel()

	in := validIssue()
	for range 5 {
		in.Lines[0].TaxComponents = append(in.Lines[0].TaxComponents,
			service.LineTaxInput{RateBps: 400, TaxableAmount: 2000, TaxAmount: 80})
	}

	_, err := newService(newFakeRepo()).Issue(context.Background(), in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "at most 4 can be printed")
}

// TestAComponentOutsideTheRateRangeIsRefused applies the row's own bound one
// component at a time.
func TestAComponentOutsideTheRateRangeIsRefused(t *testing.T) {
	t.Parallel()

	in := stackedIssue()
	in.Lines[0].TaxComponents[1].RateBps = 10_001

	_, err := newService(newFakeRepo()).Issue(context.Background(), in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "outside [0, 10000] basis points")
}

// TestAComponentTakingMoreThanItsBaseIsRefused catches what the row-level check
// cannot see: the row's tax stays inside its subtotal while a component does
// not.
func TestAComponentTakingMoreThanItsBaseIsRefused(t *testing.T) {
	t.Parallel()

	in := stackedIssue()
	in.Lines[0].TaxComponents[0].TaxableAmount = 50

	_, err := newService(newFakeRepo()).Issue(context.Background(), in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "takes more than its own base")
}

// TestTheFirstComponentCannotCompound refuses a component standing on nothing.
func TestTheFirstComponentCannotCompound(t *testing.T) {
	t.Parallel()

	in := stackedIssue()
	in.Lines[0].TaxComponents[0].Compound = true

	_, err := newService(newFakeRepo()).Issue(context.Background(), in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "stands on nothing and cannot be compound")
}

// TestTheIssueSchemaCarriesTheBreakdown is the last of the four boundaries this
// field had to cross.
//
// This parse IGNORES unknown fields, so a caller that started sending
// "tax_components" before this schema knew them would have had the breakdown
// dropped IN SILENCE and the document would have printed the stack's base rate
// as though it were the whole story — with every total still adding up.
func TestTheIssueSchemaCarriesTheBreakdown(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	interop := service.NewInterop(newService(repo))

	invoiceID, number, err := interop.IssueJSON(context.Background(), json.RawMessage(`{
      "series_prefix": "GBT",
      "kind": "sale",
      "currency_code": "TRY",
      "seller": {"name": "Gobit Shop", "tax_number": "1234567890", "country_code": "TR"},
      "buyer": {"name": "A Customer", "country_code": "TR"},
      "lines": [{
        "description": "Red T-Shirt",
        "quantity": 2,
        "unit_price": 1000,
        "subtotal": 2000,
        "discount_total": 0,
        "tax_rate_bps": 500,
        "tax_total": 268,
        "total": 2268,
        "tax_components": [
          {"rate_id": "txr_base", "rate_bps": 500, "compound": false,
           "taxable_amount": 2000, "tax_amount": 100},
          {"rate_id": "txr_top", "rate_bps": 800, "compound": true,
           "taxable_amount": 2100, "tax_amount": 168}
        ]
      }],
      "subtotal": 2000,
      "discount_total": 0,
      "tax_total": 268,
      "total": 2268
    }`))
	require.NoError(t, err)
	require.NotEmpty(t, invoiceID)
	require.NotEmpty(t, number)

	stored, err := repo.GetInvoice(context.Background(), invoiceID)
	require.NoError(t, err)
	require.Len(t, stored.Lines, 1)
	require.Len(t, stored.Lines[0].TaxComponents, 2,
		"the breakdown the body carried must reach the row")

	assert.Equal(t, "txr_top", stored.Lines[0].TaxComponents[1].RateID)
	assert.Equal(t, int32(800), stored.Lines[0].TaxComponents[1].RateBps)
	assert.True(t, stored.Lines[0].TaxComponents[1].Compound)
	assert.Equal(t, int64(2100), stored.Lines[0].TaxComponents[1].TaxableAmount)
	assert.Equal(t, int64(168), stored.Lines[0].TaxComponents[1].TaxAmount)
}

// TestAnIssueBodyWithoutABreakdownStillIssues keeps every existing caller
// working.
func TestAnIssueBodyWithoutABreakdownStillIssues(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	interop := service.NewInterop(newService(repo))

	invoiceID, _, err := interop.IssueJSON(context.Background(), json.RawMessage(`{
      "series_prefix": "GBT", "kind": "sale", "currency_code": "TRY",
      "seller": {"name": "Gobit Shop", "country_code": "TR"},
      "buyer": {"name": "A Customer", "country_code": "TR"},
      "lines": [{
        "description": "Red T-Shirt", "quantity": 2, "unit_price": 1000,
        "subtotal": 2000, "discount_total": 0, "tax_rate_bps": 2000,
        "tax_total": 400, "total": 2400
      }],
      "subtotal": 2000, "discount_total": 0, "tax_total": 400, "total": 2400
    }`))
	require.NoError(t, err)

	stored, err := repo.GetInvoice(context.Background(), invoiceID)
	require.NoError(t, err)
	require.Len(t, stored.Lines, 1)
	assert.Empty(t, stored.Lines[0].TaxComponents)
}
