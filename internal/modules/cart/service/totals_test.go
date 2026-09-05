package service_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// cartWithTotals builds a cart with two lines and returns the lines.
//
// The fixture is deliberate: the quantities and the unit prices of the lines are
// DIFFERENT, that is, only the right multiplication and the right summing can
// hit the subtotal.
func cartWithTotals(ctx context.Context, t *testing.T, svc *service.Service) (cart models.Cart, first, second models.LineItem) {
	t.Helper()

	var err error
	cart, err = svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	first, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 3,
	})
	require.NoError(t, err)
	second, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantB, Title: "Trousers", Quantity: 2,
	})
	require.NoError(t, err)

	// The cart is returned in its CURRENT state: SetTotals asks the caller for
	// the shape counter the calculation rests on, and the real workflow reads the
	// cart before writing as well.
	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	return detail.Cart, first, second
}

// TestSetTotalsWritesAndStampsOnSuccess verifies that a consistent calculation
// is written and that the totals are NO LONGER stale.
func TestSetTotalsWritesAndStampsOnSuccess(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	// 3 x 1000 = 3000, 2 x 2500 = 5000 -> subtotal 8000.
	// Discount 500, tax 1500, shipping 2000 -> total 11000.
	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision:      cart.Revision,
		Subtotal:      8000,
		DiscountTotal: 500,
		TaxTotal:      1500,
		ShippingTotal: 2000,
		Total:         11000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, TaxTotal: 540, Total: 3540},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, DiscountTotal: 500, TaxTotal: 810, Total: 5310},
		},
	})

	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(8000), detail.Subtotal)
	assert.Equal(t, int64(500), detail.DiscountTotal)
	assert.Equal(t, int64(1500), detail.TaxTotal)
	assert.Equal(t, int64(2000), detail.ShippingTotal)
	assert.Equal(t, int64(11000), detail.Total)
	assert.True(t, detail.TotalsConsistent())
	assert.False(t, detail.TotalsStale(), "the written totals must belong to the cart's current shape")

	require.Len(t, detail.Items, 2)
	assert.Equal(t, int64(1000), detail.Items[0].UnitPrice)
	assert.Equal(t, int64(3540), detail.Items[0].Total)
	assert.Equal(t, int64(5310), detail.Items[1].Total)
}

// TestSetTotalsEnforcesTheCartIdentity verifies that a calculation that does not
// satisfy the cart total identity is rejected.
//
// This is SetTotals's reason for existing: a calculation error in the workflow
// cannot be written to the database silently.
func TestSetTotalsEnforcesTheCartIdentity(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	consistent := []service.LineTotals{
		{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
		{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
	}

	cases := map[string]service.Totals{
		"the total is short": {
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 500, TaxTotal: 1500, ShippingTotal: 2000,
			Total: 10999, Lines: consistent,
		},
		"the discount was added instead of subtracted": {
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 500, Total: 8500, Lines: consistent,
		},
		"the tax was forgotten": {
			Revision: cart.Revision,
			Subtotal: 8000, TaxTotal: 1440, Total: 8000, Lines: consistent,
		},
		"the shipping was forgotten": {
			Revision: cart.Revision,
			Subtotal: 8000, ShippingTotal: 2000, Total: 8000, Lines: consistent,
		},
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			err := svc.SetTotals(ctx, cart.ID, input)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
		})
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Zero(t, detail.Total, "a rejected calculation must not be written")
	assert.Zero(t, detail.Items[0].UnitPrice, "a rejected calculation must not be written to the lines either")
}

// TestSetTotalsEnforcesTheLineIdentity verifies that a calculation that does not
// satisfy the line total identity is rejected.
func TestSetTotalsEnforcesTheLineIdentity(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			// It should be 3000 - 100 + 200 = 3100, 3000 was given.
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, DiscountTotal: 100, TaxTotal: 200, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
}

// TestSetTotalsEnforcesTheLineSubtotalMultiplication verifies that the line
// subtotal is the unit price x the quantity.
//
// The quantity is the cart's OWN data; this is the only place that can validate
// this multiplication. A line priced with the wrong quantity would be caught at
// no other gate.
func TestSetTotalsEnforcesTheLineSubtotalMultiplication(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 6000, Total: 6000,
		Lines: []service.LineTotals{
			// The line's quantity is 3; it should be 1000 x 3 = 3000, 1000 was
			// given (the quantity was assumed to be 1).
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "quantity", "the error must say which multiplication did not hold")
}

// TestSetTotalsSubtotalMustBeTheSumOfTheLines verifies that the cart subtotal is
// forced to equal the sum of the line subtotals.
func TestSetTotalsSubtotalMustBeTheSumOfTheLines(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		// The lines come to 3000 + 5000 = 8000; 7999 was given.
		Subtotal: 7999, Total: 7999,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
}

// TestSetTotalsAnUnpricedLineCannotPassWithAZeroAmount verifies that covering
// ALL of the cart's lines is mandatory.
//
// Had the coverage not been mandatory, the STORED values of a line whose amount
// was not given would be preserved; and the stored subtotal of a newly opened
// line is ZERO. That is, a calculation round that forgets to send the lines
// shows a cart worth 300000 as CONSISTENT with "subtotal 0, total 0" and the
// cart would be completed for free.
func TestSetTotalsAnUnpricedLineCannotPassWithAZeroAmount(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 3, UnitPrice: 100000,
	})
	require.NoError(t, err)
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision, Subtotal: 0, Total: 0,
	})

	require.Error(t, err, "a calculation that skips an unpriced line must not be accepted")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
	assert.Contains(t, err.Error(), item.ID, "the error must say which line was skipped")

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "a rejected round must not stamp the cart fresh")
}

// TestSetTotalsRejectsALinelessWriteAfterAQuantityChange verifies that a
// calculation that does not send the lines after a quantity change is rejected.
//
// The scenario is real: the line is priced correctly ONCE, then the customer
// increases the quantity and the new calculation round forgets to send the
// lines. Had the stored amounts been trusted, the calculation would look
// consistent because Σ did not change, and the cart would be completed with a
// FRESH stamp, charging 1 item's price for 10 items of goods.
func TestSetTotalsRejectsALinelessWriteAfterAQuantityChange(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	}))

	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 10)
	require.NoError(t, err)
	current, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision, Subtotal: 1000, ShippingTotal: 500, Total: 1500,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "a rejected round must not stamp the cart fresh")
	assert.Equal(t, int64(1000), detail.Total, "the previous valid calculation must be preserved")
}

// TestSetTotalsAShippingUpdateAlsoRequiresAllTheLines verifies that no form of
// partial update is accepted.
//
// "Write the shipping only" is the call that looks reasonable but pierces the
// contract: an unpriced line goes through the same door. A shipping change
// already changes the cart's shape and requires the calculation to run from the
// start.
func TestSetTotalsAShippingUpdateAlsoRequiresAllTheLines(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	allLines := []service.LineTotals{
		{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
		{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
	}
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision, Subtotal: 8000, Total: 8000, Lines: allLines,
	}))

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision, Subtotal: 8000, ShippingTotal: 2000, Total: 10000,
	})
	require.Error(t, err, "a partial update without lines must not be accepted")
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))

	// The same round passes once it is sent together with the lines.
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision, Subtotal: 8000, ShippingTotal: 2000, Total: 10000,
		Lines: allLines,
	}))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(10000), detail.Total)
	assert.False(t, detail.TotalsStale())
}

// TestSetTotalsRejectsAStaleCalculation verifies that if the cart shape the
// calculation rests on has changed, the write is rejected.
//
// This is the race the module claims to defend against: the workflow reads the
// cart, does the calculation OUTSIDE THE LOCK, a line comes in between, and the
// stale calculation is offered for writing. Had the stamp been taken from the
// shape at the moment of the write, the stale calculation would be stamped as
// CURRENT, MarkCompleted's staleness gate would open and the customer would pay
// less than the goods in their cart.
func TestSetTotalsRejectsAStaleCalculation(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	a, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "A", Quantity: 1,
	})
	require.NoError(t, err)

	// The workflow reads the cart and does its calculation for this shape.
	calculated, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	// A second line comes in between.
	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantB, Title: "B", Quantity: 1,
	})
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: calculated.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: a.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Zero(t, detail.Total, "a stale calculation must not be written")
	assert.True(t, detail.TotalsStale(), "the cart must stay stale")

	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.Error(t, err, "a stale cart must not be completable")
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))
}

// TestSetTotalsCatchesAChangeOutsideTheLinesToo verifies that the race is caught
// even when the line set does not change.
//
// Adding a shipping method does not touch the lines; the coverage check would
// let this round through. The only thing that catches it is that the shape the
// calculation rests on is taken from the caller.
func TestSetTotalsCatchesAChangeOutsideTheLinesToo(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	calculated, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	// The customer picks a shipping method while the calculation is running; the
	// line set stays THE SAME.
	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standard", Amount: 2500,
	})
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: calculated.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	})

	require.Error(t, err, "a stale calculation whose shipping is not paid must not be accepted")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))
}

// TestSetTotalsAZeroRevisionDoesNotFallBackToTheOldBehavior verifies that a
// Revision field that was not filled in does not count as "not given".
//
// Zero is a REAL shape value (a cart that has never been changed). Had there
// been a fallback to the old behavior at zero for the sake of an easy migration,
// every caller who forgot to fill the field in would bring back exactly the race
// that was meant to be closed.
func TestSetTotalsAZeroRevisionDoesNotFallBackToTheOldBehavior(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)
	require.Positive(t, cart.Revision, "the fixture must have changed the cart")

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))
}

// TestSetTotalsSubtotalMustBeZeroOnAnEmptyCart verifies that on a cart without
// lines a subtotal different from zero is rejected.
func TestSetTotalsSubtotalMustBeZeroOnAnEmptyCart(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{Subtotal: 100, Total: 100})

	require.Error(t, err)
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))

	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		ShippingTotal: 2500, Total: 2500,
	}), "it must be possible to write only the shipping to a cart without lines")
}

// TestSetTotalsRejectsAnUnknownLine verifies that the amount of another cart's
// line (or of a line that does not exist) cannot be written.
func TestSetTotalsRejectsAnUnknownLine(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, _ := cartWithTotals(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 3000, Total: 3000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: "li_OTHER", UnitPrice: 100, Subtotal: 100, Total: 100},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, service.CodeLineItemNotFound, errors.CodeOf(err))
}

// TestSetTotalsRejectsARepeatedLine verifies that giving two amounts for the
// same line is rejected.
//
// Had the last one silently won, the difference between two calculations
// overwriting each other would depend on the order alone.
func TestSetTotalsRejectsARepeatedLine(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSetTotalsRejectsANegativeAmount verifies that negative totals are
// rejected.
func TestSetTotalsRejectsANegativeAmount(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	cases := map[string]service.Totals{
		"negative subtotal": {Subtotal: -1, Total: -1},
		"negative discount": {DiscountTotal: -100, Total: 100},
		"negative tax":      {TaxTotal: -100, Total: -100},
		"negative shipping": {ShippingTotal: -100, Total: -100},
		"negative total":    {Total: -1},
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			err := svc.SetTotals(ctx, cart.ID, input)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestSetTotalsRejectsAnAmountAboveTheCeiling verifies that amounts exceeding
// the upper bound are rejected; the bound makes overflow structurally
// impossible.
func TestSetTotalsRejectsAnAmountAboveTheCeiling(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		ShippingTotal: models.MaxTotal + 1,
		Total:         models.MaxTotal + 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSetTotalsWritesNoLineOnError verifies that if an error comes up while the
// lines are being written, what was written earlier IS ROLLED BACK.
//
// A partially written calculation round would lead to a state in which the
// cart's subtotal and its lines do not agree with each other.
func TestSetTotalsWritesNoLineOnError(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)
	store.failSetLineItemTotals = errors.Internal("cart_query_failed", "the database went down")

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	detail, getErr := svc.GetCart(ctx, cart.ID)
	require.NoError(t, getErr)
	assert.Zero(t, detail.Subtotal, "the cart total must not be written")
	for _, item := range detail.Items {
		assert.Zero(t, item.Subtotal, "no line must be written")
	}
}

// TestSetTotalsMissingCartReturnsNotFound verifies that writing to a cart that
// does not exist returns NotFound.
func TestSetTotalsMissingCartReturnsNotFound(t *testing.T) {
	svc, _ := newService(t)

	err := svc.SetTotals(context.Background(), "cart_MISSING", service.Totals{})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestSetTotalsDiscountCannotExceedTheSubtotal pins down the rule that prevents
// an excessive discount from passing the identity check by being SWALLOWED by
// the tax/shipping.
//
// Regression: the validation was checking only (a) the range and (b) the
// identity (total = subtotal - discount + tax + shipping). When an excessive
// discount is swallowed by the shipping, the identity HOLDS and the total does
// not even go negative: subtotal=1000, discount=3000, shipping=2500 -> total=500
// was being accepted. The customer pays 500 for 1000 worth of goods together
// with 2500 worth of shipping, and neither the service nor the
// carts_totals_consistent constraint would see it.
func TestSetTotalsDiscountCannotExceedTheSubtotal(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, first, second := cartWithTotals(ctx, t, svc)

	consistent := []service.LineTotals{
		{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
		{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
	}

	t.Run("an excessive discount at the cart level is rejected", func(t *testing.T) {
		err := svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 20000, ShippingTotal: 15000,
			Total: 3000, // the identity HOLDS: 8000 - 20000 + 0 + 15000 = 3000
			Lines: consistent,
		})
		require.Error(t, err, "the discount cannot exceed the subtotal even if the identity holds")
		assert.True(t, errors.IsInvalid(err), "the kind must be Invalid: %v", err)
		assert.Contains(t, err.Error(), "discount")
	})

	t.Run("an excessive discount at the line level is rejected", func(t *testing.T) {
		err := svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: cart.Revision,
			Subtotal: 8000, Total: 8000,
			Lines: []service.LineTotals{
				// A discount of 9000 on a line of 3000; the tax swallows it and
				// the identity holds.
				{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000,
					DiscountTotal: 9000, TaxTotal: 9000, Total: 3000},
				{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
			},
		})
		require.Error(t, err, "the line discount cannot exceed the subtotal either")
		assert.True(t, errors.IsInvalid(err), "the kind must be Invalid: %v", err)
	})

	t.Run("a discount equal to the subtotal is valid", func(t *testing.T) {
		err := svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 8000, ShippingTotal: 2000,
			Total: 2000,
			Lines: []service.LineTotals{
				{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, DiscountTotal: 3000, Total: 0},
				{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, DiscountTotal: 5000, Total: 0},
			},
		})
		assert.NoError(t, err, "a discount EQUAL to the subtotal is at the boundary and is valid")
	})
}

// TestSetTotalsOneRoundIsASingleWriteCall verifies that one calculation round
// produces a single write call INDEPENDENTLY of the number of lines.
//
// The number is not a performance ornament, it is the cart's LOCK duration: the
// write runs under the cart's FOR UPDATE lock and that lock queues up every flow
// that writes to the same cart. It used to run one UPDATE per line; measured
// (local container, 100 lines, from the taking of the lock to the commit, p50)
// 8.0 ms, with a single statement 0.55 ms. A change that goes back to the loop is
// caught here, without a real database.
func TestSetTotalsOneRoundIsASingleWriteCall(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	const lineCount = 40
	lines := make([]service.LineTotals, 0, lineCount)
	for i := range lineCount {
		item, addErr := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
			VariantID: "variant_" + strconv.Itoa(i), Title: "Product", Quantity: 1,
		})
		require.NoError(t, addErr)
		lines = append(lines, service.LineTotals{
			LineItemID: item.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000,
		})
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	store.setLineTotalsCalls, store.setLineTotalsRows = 0, 0

	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: 1000 * lineCount, Total: 1000 * lineCount,
		Lines: lines,
	}))

	assert.Equal(t, 1, store.setLineTotalsCalls,
		"however many lines the round carries there must be a SINGLE write call; "+
			"a call per line holds the lock proportionally to the number of lines")
	assert.Equal(t, lineCount, store.setLineTotalsRows,
		"the single call must carry ALL of the cart's lines")
}

// TestSetTotalsEachLineGetsItsOwnAmounts verifies that the amounts MATCH the
// lines correctly.
//
// The bulk write sends six parallel arrays and the matching rests only on the
// ORDER of the arrays. If the order shifts, the cart's subtotal still holds, the
// identity checks still pass, the database constraints are still satisfied — the
// only thing that breaks is the money taken from the customer, and there is no
// gate downstream that would see it. That is why every line is given a DIFFERENT
// quadruple of amounts: even if the order shifts by one step, the value of every
// line changes.
func TestSetTotalsEachLineGetsItsOwnAmounts(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	// The quantity and the unit price of every line are DIFFERENT; because the
	// subtotal = quantity x unit price, no line's quadruple of amounts is the
	// same as another's.
	const lineCount = 8
	type expectation struct {
		id                                            string
		unitPrice, subtotal, discount, tax, lineTotal int64
	}
	expectations := make([]expectation, 0, lineCount)
	lines := make([]service.LineTotals, 0, lineCount)
	var cartSubtotal, cartDiscount, cartTax int64
	for i := range lineCount {
		quantity := int64(i + 1)
		item, addErr := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
			VariantID: "variant_" + strconv.Itoa(i), Title: "Product", Quantity: quantity,
		})
		require.NoError(t, addErr)

		unitPrice := int64(100 * (i + 1))
		subtotal := unitPrice * quantity
		discount := int64(i)
		tax := int64(7 * (i + 1))
		lineTotal := subtotal - discount + tax

		expectations = append(expectations, expectation{item.ID, unitPrice, subtotal, discount, tax, lineTotal})
		lines = append(lines, service.LineTotals{
			LineItemID: item.ID, UnitPrice: unitPrice, Subtotal: subtotal,
			DiscountTotal: discount, TaxTotal: tax, Total: lineTotal,
		})
		cartSubtotal += subtotal
		cartDiscount += discount
		cartTax += tax
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: cartSubtotal, DiscountTotal: cartDiscount, TaxTotal: cartTax,
		Total: cartSubtotal - cartDiscount + cartTax,
		Lines: lines,
	}))

	written, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, written.Items, lineCount)
	stored := make(map[string]models.LineItem, lineCount)
	for _, item := range written.Items {
		stored[item.ID] = item
	}
	for _, want := range expectations {
		item, ok := stored[want.id]
		require.True(t, ok, "the line must not be lost: %s", want.id)
		assert.Equal(t, want.unitPrice, item.UnitPrice, "%s unit price", want.id)
		assert.Equal(t, want.subtotal, item.Subtotal, "%s subtotal", want.id)
		assert.Equal(t, want.discount, item.DiscountTotal, "%s discount", want.id)
		assert.Equal(t, want.tax, item.TaxTotal, "%s tax", want.id)
		assert.Equal(t, want.lineTotal, item.Total, "%s total", want.id)
	}
}
