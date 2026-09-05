//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestTheLineTaxRateSurvivesTheDatabase covers the half a compiler cannot.
//
// The rate travels through four hands — the input, the model, the INSERT
// parameters and the row conversion — and a field left out of any one of them
// still compiles, still passes every unit test that uses a fake store, and
// silently writes zero. Zero is also a legitimate rate, so nothing downstream
// would look wrong; the first sign would be an invoice printing 0% KDV on a
// taxed line.
//
// That is not hypothetical: both the INSERT and the conversion were written
// without it, and everything built.
func TestTheLineTaxRateSurvivesTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validInput()
	// Two DIFFERENT rates, neither of them the zero value and neither equal to
	// the region default, so a line that lost its own rate cannot be rescued by
	// a fallback that happens to agree.
	in.Items = []service.CreateOrderItemInput{
		{
			VariantID: "variant_A", Title: "Standard rate",
			Quantity: 1, UnitPrice: 1000, Subtotal: 1000,
			TaxTotal: 200, TaxRateBps: 2000, Total: 1200,
		},
		{
			VariantID: "variant_B", Title: "Reduced rate",
			Quantity: 1, UnitPrice: 1000, Subtotal: 1000,
			TaxTotal: 100, TaxRateBps: 1000, Total: 1100,
		},
	}
	in.Subtotal = 2000
	in.TaxTotal = 300
	in.ShippingTotal = 0
	in.Total = 2300

	created, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	detail, err := svc.GetOrder(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)

	rates := map[string]int32{}
	for i := range detail.Items {
		rates[detail.Items[i].Title] = detail.Items[i].TaxRateBps
	}

	assert.Equal(t, int32(2000), rates["Standard rate"],
		"the rate the line was charged at has to come back from the database")
	assert.Equal(t, int32(1000), rates["Reduced rate"],
		"two lines of the same order must keep their OWN rates")
}
