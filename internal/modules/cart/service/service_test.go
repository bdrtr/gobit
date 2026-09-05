package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// The fixed identifiers used in the tests.
const (
	regionID    = "reg_TEST"
	regionOther = "reg_OTHER"
	customerID  = "cust_TEST"
	variantA    = "variant_A"
	variantB    = "variant_B"
	currency    = "TRY"
)

// newService builds a service that works over the fake store.
func newService(t *testing.T) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	svc, err := service.New(service.Options{Repo: store})
	require.NoError(t, err)
	return svc, store
}

// newCart creates a guest cart for the test.
func newCart(ctx context.Context, t *testing.T, svc *service.Service) models.Cart {
	t.Helper()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     regionID,
		CurrencyCode: currency,
	})
	require.NoError(t, err)
	return cart
}

// TestNewFailsAtBuildTimeOnAMissingDependency verifies that the service cannot
// be built with a missing dependency.
//
// A service without a store would produce a panic on every call; there is no
// reason at all for the gap to show up on the first request rather than at
// startup.
func TestNewFailsAtBuildTimeOnAMissingDependency(t *testing.T) {
	_, err := service.New(service.Options{})
	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
}

// TestCreateCartRequiresARegion verifies that a cart without a region is
// rejected.
func TestCreateCartRequiresARegion(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		CurrencyCode: currency,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestCreateCartValidatesAndUnifiesTheCurrency verifies that the currency passes
// the shape validation and is unified to UPPERCASE.
//
// Without the unification "try" and "TRY" would be stored as two separate
// currencies and the amount comparisons would silently give the wrong result.
func TestCreateCartValidatesAndUnifiesTheCurrency(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: " try ",
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", cart.CurrencyCode)

	for _, invalid := range []string{"", "TR", "TRYY", "TR1"} {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CurrencyCode: invalid,
		})
		require.Error(t, err, "an invalid currency must not be accepted: %q", invalid)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}
}

// TestCreateCartOpensAGuestCartWithoutACustomer verifies that a cart given no
// customer id is opened as a GUEST cart.
//
// An empty identifier is not stored, ABSENCE is stored: had the distinction
// between "has no customer" and "has an empty string for a customer" been lost,
// carts_customer_idx would fill up with carts written to the empty string.
func TestCreateCartOpensAGuestCartWithoutACustomer(t *testing.T) {
	svc, _ := newService(t)

	cart, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})

	require.NoError(t, err)
	assert.True(t, cart.Guest(), "a cart without a customer must count as a guest cart")
	assert.Empty(t, cart.CustomerID)
	assert.Equal(t, regionID, cart.RegionID)
}

// TestCreateCartWritesTheRegisteredCustomerToTheColumns verifies that the region
// and the customer of a registered customer's cart sit IN THEIR OWN COLUMNS.
//
// That is the only place of the relation; the cart is not also written to a link
// table. The assertion is the guardian of that decision: if a second copy is
// added, the column and the link can drift apart.
func TestCreateCartWritesTheRegisteredCustomerToTheColumns(t *testing.T) {
	svc, _ := newService(t)

	cart, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
		Email: "  Customer@Example.COM ",
	})

	require.NoError(t, err)
	assert.False(t, cart.Guest())
	assert.Equal(t, "customer@example.com", cart.Email, "the email must be lowercased")
	assert.Equal(t, regionID, cart.RegionID)
	assert.Equal(t, customerID, cart.CustomerID)
}

// TestCreateCartAllowsTwoCartsInTheSameRegion verifies that more than one cart
// can be opened in one region (and for one customer).
//
// The rule is the cart's own nature: a customer has more than one cart over
// time, and there are thousands of carts in a region. A constraint at any layer
// that imposed UNIQUENESS per region or per customer would fail this test.
func TestCreateCartAllowsTwoCartsInTheSameRegion(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	first, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	second, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err, "the same customer must be able to open a second cart in the same region")

	assert.NotEqual(t, first.ID, second.ID)

	listed, listErr := svc.ListCarts(ctx, service.ListCartsInput{})
	count := listed.Count
	require.NoError(t, listErr)
	assert.Equal(t, int64(2), count)
}

// TestAddLineItemRequiresAPositiveQuantity verifies that a zero or negative
// quantity is rejected.
func TestAddLineItemRequiresAPositiveQuantity(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	for _, quantity := range []int64{0, -1, -1000} {
		_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
			VariantID: variantA, Title: "T-shirt", Quantity: quantity,
		})
		require.Error(t, err, "the quantity %d must not be accepted", quantity)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}

	items, err := store.ListLineItems(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, items, "a rejected addition must not write a line")
}

// TestAddLineItemCannotExceedTheQuantityCeiling verifies that the quantity upper
// bound is applied.
func TestAddLineItemCannotExceedTheQuantityCeiling(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: models.MaxQuantity + 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestAddLineItemSameVariantIncrementsTheQuantity verifies that adding the same
// variant a second time DOES NOT OPEN A NEW LINE but increments the existing
// line's quantity.
//
// The decision comes from the price tiers: if 3 + 2 is split into two lines,
// pricing prices both lines from the "1-4" tier and the customer does not get
// the "5+" price (see Service.AddLineItem).
func TestAddLineItemSameVariantIncrementsTheQuantity(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	first, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 3, UnitPrice: 1000,
	})
	require.NoError(t, err)

	second, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt (new title)", Quantity: 2, UnitPrice: 9999,
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "the same line must be updated")
	assert.Equal(t, int64(5), second.Quantity, "3 + 2 = 5")
	assert.Equal(t, "T-shirt", second.Title, "only the quantity is carried over in the merge")
	assert.Equal(t, int64(1000), second.UnitPrice, "the existing line's unit price must be preserved")

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "a second line must not be opened for the same variant")
}

// TestAddLineItemDifferentVariantOpensANewLine verifies that different variants
// are separate lines.
func TestAddLineItemDifferentVariantOpensANewLine(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantB, Title: "Trousers", Quantity: 1,
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, 2)
}

// TestAddLineItemIsRejectedWhenTheMergeExceedsTheCeiling verifies that if the
// merged quantity exceeds the ceiling the request is rejected and the line DOES
// NOT CHANGE.
func TestAddLineItemIsRejectedWhenTheMergeExceedsTheCeiling(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: models.MaxQuantity,
	})
	require.NoError(t, err)

	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, models.MaxQuantity, detail.Items[0].Quantity, "the line must not change")
}

// TestUpdateLineItemQuantityRejectsZero verifies that a zero quantity DOES NOT
// DELETE the line but is rejected.
//
// "Set the quantity to zero" and "remove the line" are separate intents; turning
// one into the other would mean that a bug sending zero into the quantity field
// silently deletes data.
func TestUpdateLineItemQuantityRejectsZero(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 2,
	})
	require.NoError(t, err)

	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 0)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "the line must not be deleted")
	assert.Equal(t, int64(2), detail.Items[0].Quantity, "the quantity must not change")
}

// TestUpdateLineItemQuantityWritesAnAbsoluteValue verifies that the quantity is
// written as an ABSOLUTE value (that it is not incremental).
func TestUpdateLineItemQuantityWritesAnAbsoluteValue(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 5,
	})
	require.NoError(t, err)

	updated, err := svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 2)

	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Quantity)
}

// TestRemoveLineItemCannotDeleteAnotherCartsLine verifies that another cart's
// line cannot be deleted even if its line identifier is known.
func TestRemoveLineItemCannotDeleteAnotherCartsLine(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	mine := newCart(ctx, t, svc)
	other, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionOther, CurrencyCode: currency,
	})
	require.NoError(t, err)

	item, err := svc.AddLineItem(ctx, other.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)

	err = svc.RemoveLineItem(ctx, mine.ID, item.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	detail, err := svc.GetCart(ctx, other.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, 1, "the other cart's line must stay")
}

// TestWriteFlowsLockTheCart verifies that every write flow LOCKS the cart.
//
// The fake store returns an error in a flow that calls the lock method outside a
// transaction; therefore a flow that skips WithTx blows up here. The lock count
// also shows that the lock was really taken.
func TestWriteFlowsLockTheCart(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 2)
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Istanbul"})
	require.NoError(t, err)
	method, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standard", Amount: 0,
	})
	require.NoError(t, err)
	require.NoError(t, svc.RemoveShippingMethod(ctx, cart.ID, method.ID))
	require.NoError(t, svc.RemoveLineItem(ctx, cart.ID, item.ID))
	// The cart is empty now; the calculation still has to declare the shape it
	// rests on.
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{Revision: current.Revision}))

	assert.Len(t, store.lockedCarts, 7,
		"every write flow must lock the cart exactly once")
	for _, locked := range store.lockedCarts {
		assert.Equal(t, cart.ID, locked)
	}
	// SetTotals does not change the cart's SHAPE, it only writes the
	// calculation; that is why only six of the seven flows that take the lock
	// increment the shape counter. Had the counter been incremented in SetTotals
	// too, the totals would count as stale the moment they were written and no
	// cart could ever be completed.
	assert.Equal(t, 6, store.bumpCalls,
		"the structural changes must increment the counter, SetTotals must not")
}

// TestAStructuralChangeMakesTheTotalsStale verifies that every operation that
// changes the cart's shape marks the totals stale.
//
// The staleness being visible is the only thing that prevents the cart from
// being completed without calculate_totals running (see Service.MarkCompleted).
func TestAStructuralChangeMakesTheTotalsStale(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	assert.False(t, cart.TotalsStale(), "the totals of an empty cart count as current")

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 2, UnitPrice: 1000,
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "the totals must be stale after a line is added")
	assert.Equal(t, int64(1), detail.Revision)

	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: 2000, Total: 2000,
		Lines: []service.LineTotals{{
			LineItemID: detail.Items[0].ID, UnitPrice: 1000, Subtotal: 2000, Total: 2000,
		}},
	}))

	detail, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.False(t, detail.TotalsStale(), "the totals must be current after the calculation")

	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Ankara"})
	require.NoError(t, err)

	detail, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "a change of address affects the tax, the totals must go stale")
}

// TestACompletedCartCannotBeChanged verifies that on a completed cart EVERY
// write path returns errors.Conflict.
//
// The list deliberately counts all of the writing methods: if a new write path
// is added and the check is forgotten, this test will not catch it — but it does
// guarantee that none of the existing ones has loosened.
func TestACompletedCartCannotBeChanged(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)
	method, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standard",
	})
	require.NoError(t, err)
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision,
		Subtotal: 500, Total: 500,
		Lines: []service.LineTotals{{
			LineItemID: item.ID, UnitPrice: 500, Subtotal: 500, Total: 500,
		}},
	}))

	completed, err := svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)
	require.True(t, completed.Completed())

	writes := map[string]func() error{
		"AddLineItem": func() error {
			_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
				VariantID: variantB, Title: "Trousers", Quantity: 1,
			})
			return err
		},
		"UpdateLineItemQuantity": func() error {
			_, err := svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 3)
			return err
		},
		"RemoveLineItem": func() error {
			return svc.RemoveLineItem(ctx, cart.ID, item.ID)
		},
		"SetShippingAddress": func() error {
			_, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Izmir"})
			return err
		},
		"SetBillingAddress": func() error {
			_, err := svc.SetBillingAddress(ctx, cart.ID, service.AddressInput{City: "Izmir"})
			return err
		},
		"AddShippingMethod": func() error {
			_, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{Name: "Express"})
			return err
		},
		"RemoveShippingMethod": func() error {
			return svc.RemoveShippingMethod(ctx, cart.ID, method.ID)
		},
		"SetTotals": func() error {
			return svc.SetTotals(ctx, cart.ID, service.Totals{Subtotal: 500, Total: 500})
		},
		"DeleteCart": func() error {
			return svc.DeleteCart(ctx, cart.ID)
		},
		"MarkCompleted": func() error {
			_, err := svc.MarkCompleted(ctx, cart.ID)
			return err
		},
	}

	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			err := write()
			require.Error(t, err, "%s must return an error on a completed cart", name)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err),
				"%s must return Conflict, it got: %v", name, err)
		})
	}

	// The cart's content must really not have changed.
	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, int64(1), detail.Items[0].Quantity)
	assert.Len(t, detail.ShippingMethods, 1)
	assert.Nil(t, detail.ShippingAddress)
}

// TestMarkCompletedRejectsStaleTotals verifies that a cart whose totals are not
// current cannot be completed.
//
// The scenario is real: a line is added to the cart while the checkout page is
// open. Applying the stamp would turn the WRONG amount of that moment into the
// order amount.
func TestMarkCompletedRejectsStaleTotals(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)

	_, err = svc.MarkCompleted(ctx, cart.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.False(t, detail.Completed(), "a rejected completion must not apply the stamp")
}

// TestMarkCompletedRejectsANeverCalculatedCart verifies that the cart cannot be
// completed without calculate_totals ever having run.
//
// The staleness criterion is totals_revision != revision and on a new cart both
// are ZERO; "never calculated" and "calculated for the zeroth shape" cannot be
// told apart. Because the cart is without lines, the real gate is that one: the
// order that would be born from a cart without lines is an order in which
// nothing was sold.
func TestMarkCompletedRejectsANeverCalculatedCart(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	require.False(t, cart.TotalsStale(), "on a new cart the staleness criterion is silent")

	_, err := svc.MarkCompleted(ctx, cart.ID)

	require.Error(t, err, "a cart that was never calculated must not be completable")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCartEmpty, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.False(t, detail.Completed(), "a rejected completion must not apply the stamp")
}

// TestMarkCompletedRejectsACartWithoutLines verifies that a cart whose lines
// have been REMOVED and whose totals have been recalculated cannot be completed
// either.
//
// This path does not go through the staleness: deleting the line increments the
// counter, and the calculation round that runs afterwards stamps the cart FRESH
// again. What is left is a "current" cart with an amount of zero and no lines.
func TestMarkCompletedRejectsACartWithoutLines(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)
	require.NoError(t, svc.RemoveLineItem(ctx, cart.ID, item.ID))

	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{Revision: current.Revision}))

	current, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.False(t, current.TotalsStale(), "the cart is NOT stale; the gate cannot be the staleness gate")

	_, err = svc.MarkCompleted(ctx, cart.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCartEmpty, errors.CodeOf(err))
}

// TestGetCartDoesNotReturnATornView verifies that the cart's four reads run on a
// SINGLE snapshot.
//
// The fake store freezes the read-only transaction's view and can slip a write
// into the middle of the reads. Without the transaction the cart HEADER is read
// old and the line list new; the customer would be shown a cart that is
// inconsistent with itself, like "the total is 1000 but the lines come to 3000".
func TestGetCartDoesNotReturnATornView(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	first, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	}))

	// A complete calculation round comes in AFTER the cart header is read and
	// BEFORE the lines are read.
	store.hookListLineItems = func() {
		second, addErr := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
			VariantID: variantB, Title: "Trousers", Quantity: 1,
		})
		require.NoError(t, addErr)
		between, getErr := svc.GetCart(ctx, cart.ID)
		require.NoError(t, getErr)
		require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: between.Revision, Subtotal: 3000, Total: 3000,
			Lines: []service.LineTotals{
				{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
				{LineItemID: second.ID, UnitPrice: 2000, Subtotal: 2000, Total: 2000},
			},
		}))
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	var lineSum int64
	for i := range detail.Items {
		lineSum += detail.Items[i].Subtotal
	}
	assert.Equal(t, detail.Subtotal, lineSum,
		"the cart header and the lines must belong to the SAME moment")
}

// TestDeleteCartCleansUpTheChildren verifies that the deletion cleans up the
// cart and its children together.
func TestDeleteCartCleansUpTheChildren(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Istanbul"})
	require.NoError(t, err)
	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{Name: "Standard"})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCart(ctx, cart.ID))

	_, err = svc.GetCart(ctx, cart.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a deleted cart must not be readable")

	items, err := store.ListLineItems(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, items, "the lines must be deleted too")
	addresses, err := store.ListCartAddresses(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, addresses)
	methods, err := store.ListShippingMethods(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, methods)
}

// TestAddLineItemWritesNothingOnError verifies that when a store error arrives
// the whole transaction is rolled back.
//
// The shape counter in particular: had the counter been incremented while the
// line could not be written, the cart's totals would look stale for no reason at
// all.
func TestAddLineItemWritesNothingOnError(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	store.failCreateLineItem = errors.Internal("cart_query_failed", "the database went down")

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})

	require.Error(t, err)
	detail, getErr := svc.GetCart(ctx, cart.ID)
	require.NoError(t, getErr)
	assert.Empty(t, detail.Items)
	assert.Equal(t, int64(0), detail.Revision, "a failed transaction must not increment the shape counter")
}

// TestGetCartReturnsWithItsChildren verifies that the full cart comes back
// together with its line, its address and its shipping method.
func TestGetCartReturnsWithItsChildren(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
		City: "Istanbul", CountryCode: "tr",
	})
	require.NoError(t, err)
	_, err = svc.SetBillingAddress(ctx, cart.ID, service.AddressInput{City: "Ankara"})
	require.NoError(t, err)
	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standard", Amount: 2500,
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)

	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	require.NotNil(t, detail.ShippingAddress)
	require.NotNil(t, detail.BillingAddress)
	assert.Equal(t, "Istanbul", detail.ShippingAddress.City)
	assert.Equal(t, "TR", detail.ShippingAddress.CountryCode, "the country code must be uppercased")
	assert.Equal(t, "Ankara", detail.BillingAddress.City)
	require.Len(t, detail.ShippingMethods, 1)
	assert.Equal(t, int64(2500), detail.ShippingMethods[0].Amount)
}

// TestSetAddressDoesNotOpenASecondRecordOfTheSameType verifies that writing a
// second address of the same type DOES NOT OPEN a new record but updates the
// existing one.
func TestSetAddressDoesNotOpenASecondRecordOfTheSameType(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	first, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Istanbul"})
	require.NoError(t, err)
	second, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Bursa"})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "the address identifier must stay fixed")
	assert.Equal(t, "Bursa", second.City)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NotNil(t, detail.ShippingAddress)
	assert.Equal(t, "Bursa", detail.ShippingAddress.City)
	assert.Nil(t, detail.BillingAddress, "the shipping address must not be written to the billing address")
}

// TestSetAddressRequiresTheCountryCodeToBeLetters verifies that not only the
// length of the country code but also its LETTERS are looked at.
//
// In Phase 7 the country code is the KEY of the tax region and shipping option
// mapping; a malformed code like "12" or "T1" would sit silently in the cart and
// give its error much later, at the mapping stage.
func TestSetAddressRequiresTheCountryCodeToBeLetters(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	for _, code := range []string{"12", "T1", "1R", "t-"} {
		t.Run(code, func(t *testing.T) {
			_, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
				City: "Istanbul", CountryCode: code,
			})

			require.Error(t, err, "the country code %q must not be accepted", code)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
		})
	}

	// A valid code is still unified to uppercase.
	addr, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
		City: "Istanbul", CountryCode: "tr",
	})
	require.NoError(t, err)
	assert.Equal(t, "TR", addr.CountryCode)
}

// TestUpdateCartHandsAGuestCartOverToACustomer verifies the handover of a guest
// cart to a registered customer.
//
// That is the real flow: the customer opens the cart as a guest, enters their
// email at the checkout step and/or signs in along the way. Without this path
// the cart would have to be built from scratch and the lines would be lost.
func TestUpdateCartHandsAGuestCartOverToACustomer(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	require.True(t, cart.Guest())

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)

	email := "Customer@Example.COM"
	updated, err := svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{
		Email: &email, CustomerID: customerID,
	})

	require.NoError(t, err)
	assert.Equal(t, "customer@example.com", updated.Email, "the email must be unified")
	assert.Equal(t, customerID, updated.CustomerID)
	assert.False(t, updated.Guest())

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "the handover must not lose the lines")
	assert.True(t, detail.TotalsStale(),
		"the price of a cart whose owner changed can change too; the totals must go stale")
}

// TestUpdateCartCannotHandOverToAnotherCustomer verifies that a cart that
// already has an owner cannot be moved to another customer.
//
// Two different customers owning the same cart would leave the question of who
// the order is written to unanswered.
func TestUpdateCartCannotHandOverToAnotherCustomer(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	_, err = svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{CustomerID: "cust_OTHER"})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCustomerMismatch, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, customerID, detail.CustomerID, "a rejected handover must not be written")

	// Writing the same customer again is valid.
	_, err = svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{CustomerID: customerID})
	require.NoError(t, err)
}

// TestUpdateCartRejectsAnEmptyInput verifies that an update carrying no field is
// rejected; had it counted as silently successful, the caller would think the
// change they believed they had sent was applied.
func TestUpdateCartRejectsAnEmptyInput(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	_, err := svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestAddShippingMethodAmountCannotBeNegative verifies that a negative shipping
// amount is rejected.
func TestAddShippingMethodAmountCannotBeNegative(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)

	_, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standard", Amount: -1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestWritingToAMissingCartReturnsNotFound verifies that writing to a cart that
// does not exist returns NotFound.
func TestWritingToAMissingCartReturnsNotFound(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	_, err := svc.AddLineItem(ctx, "cart_MISSING", service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestListCartsFiltersAndPaginates verifies that the listing applies the filter
// and the pagination.
func TestListCartsFiltersAndPaginates(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	for range 3 {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
		})
		require.NoError(t, err)
	}
	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionOther, CurrencyCode: currency,
	})
	require.NoError(t, err)

	customer := customerID
	listed, err := svc.ListCarts(ctx, service.ListCartsInput{
		CustomerID: &customer,
		Page:       service.Page{Limit: 2},
	})

	require.NoError(t, err)

	carts, count := listed.Items, listed.Count
	assert.Equal(t, int64(3), count, "the count must be the filter's count, not the page's")
	assert.Len(t, carts, 2, "the page size must be applied")

	region := regionOther
	listed, err = svc.ListCarts(ctx, service.ListCartsInput{RegionID: &region})
	count = listed.Count
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestListCartsCannotExceedTheLimitCeiling verifies that the page size ceiling
// is applied.
func TestListCartsCannotExceedTheLimitCeiling(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.ListCarts(context.Background(), service.ListCartsInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestAnIdentifierArrivingWithWhitespaceIsRejected verifies that an identifier
// carrying leading/trailing whitespace IS NOT TRIMMED but rejected.
//
// Trimming pulls the identifier the caller sent apart from the identifier that
// is stored, and the difference only becomes visible after the data is
// corrupted; core/link applies the same contract.
func TestAnIdentifierArrivingWithWhitespaceIsRejected(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: " reg_TEST\n", CurrencyCode: currency,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}
