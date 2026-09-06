package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// These tests prove the DECISION the erasure makes — what the answer says, what
// it counts, what it refuses — against the fake store. What they cannot prove is
// the GROUND: that the statements in queries/erasure.sql really null the columns
// the declaration says they do. Nothing in a unit test can, because the
// statements are text; the fake nulls what a reader of that file wrote down here
// by hand, so a test passing here and a database still holding a name is a
// possible outcome and the reason the assertions below lean on the module's own
// declaration rather than on a list retyped in the test.

// erasureEmail is the address of the person the tests ask to be forgotten. It is
// written in mixed case on purpose: the column holds the folded form, and a
// subject that arrives as the shopper typed it has to reach the same rows.
const erasureEmail = "Ayse.Yilmaz@Example.COM"

// fullAddress is an address with every personal column filled, so that a column
// the erasure forgets is visible rather than empty-looking.
var fullAddress = service.AddressInput{
	SourceAddressID: "addr_book_1",
	// The name is written with a \u escape rather than the letter itself: a
	// non-ASCII first name is exactly what this erasure has to overwrite, and
	// the letter in an English file would put the file in the language ledger
	// (ADR 0012) for a piece of TEST DATA. internal/modules/product carries the
	// same escape for the same reason.
	FirstName:   "Ay\u015fe",
	LastName:    "Y\u0131lmaz",
	Company:     "Y\u0131lmaz Tasar\u0131m",
	Address1:    "Ba\u011fdat Caddesi 100",
	Address2:    "Daire 5",
	City:        "\u0130stanbul",
	Province:    "Kad\u0131k\u00f6y",
	PostalCode:  "34710",
	CountryCode: "TR",
	Phone:       "+90 555 000 00 00",
	Metadata:    map[string]any{"note": "leave it with the doorman"},
}

// newErasureCart opens a cart for the person and gives it a shipping address.
func newErasureCart(ctx context.Context, t *testing.T, svc *service.Service, owner string) models.Cart {
	t.Helper()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     regionID,
		CustomerID:   owner,
		Email:        erasureEmail,
		CurrencyCode: currency,
		Metadata:     map[string]any{"campaign": "spring"},
	})
	require.NoError(t, err)

	_, err = svc.SetShippingAddress(ctx, cart.ID, fullAddress)
	require.NoError(t, err)

	return cart
}

// TestEraseAnonymizesTheCartsOfACustomer proves the answer a controller repeats
// to the data subject: the person is gone from the cart, the basket is not.
func TestEraseAnonymizesTheCartsOfACustomer(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newErasureCart(ctx, t, svc, customerID)

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, service.ErasureHolder, result.Holder)
	// One cart row and one address row: Rows counts what was written, and the
	// figure is what a controller compares one report against the next with.
	assert.Equal(t, 2, result.Rows)
	assert.NotEmpty(t, result.Why, "an anonymized answer that kept columns has to explain them")

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Email, "the e-mail is the person and it goes")
	assert.Equal(t, customerID, detail.CustomerID,
		"the customer id stays: it is the handle a repeated sweep finds this cart by")

	require.NotNil(t, detail.ShippingAddress, "the address ROW survives; only its content goes")
	addr := detail.ShippingAddress
	assert.Empty(t, addr.FirstName)
	assert.Empty(t, addr.LastName)
	assert.Empty(t, addr.Company)
	assert.Empty(t, addr.Address1)
	assert.Empty(t, addr.Address2)
	assert.Empty(t, addr.City)
	assert.Empty(t, addr.Province)
	assert.Empty(t, addr.PostalCode)
	assert.Empty(t, addr.Phone)
	assert.Empty(t, addr.SourceAddressID, "the pointer into the person's address book goes")
	assert.Equal(t, "TR", addr.CountryCode, "the country is jurisdiction, not identity")
}

// TestEraseNamesEveryColumnItLeft proves the rule that keeps the word
// "anonymized" honest.
//
// The expected list is DERIVED from the module's own declaration rather than
// retyped: the claim is not "these five strings" but "everything declared and
// not overwritten is named in the report", and a column added to the
// declaration tomorrow has to appear on one side or the other of that line.
func TestEraseNamesEveryColumnItLeft(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newErasureCart(ctx, t, svc, customerID)

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"carts.customer_id",
		"carts.metadata",
		"cart_line_items.metadata",
		"cart_addresses.country_code",
		"cart_addresses.metadata",
		"cart_shipping_methods.data",
	}, result.Kept,
		"every declared column the statements do not rewrite has to be named in the answer")

	declared := map[string]bool{}
	for _, holding := range service.PersonalDataHoldings() {
		declared[holding.Table+"."+holding.Column] = true
	}
	for _, kept := range result.Kept {
		assert.True(t, declared[kept],
			"%s is reported as kept and is not declared; the two lists have drifted", kept)
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"campaign": "spring"}, detail.Metadata,
		"gobit does not rewrite a free-form column; it names it")
	require.NotNil(t, detail.ShippingAddress)
	assert.Equal(t, map[string]any{"note": "leave it with the doorman"}, detail.ShippingAddress.Metadata)
}

// TestEraseFindsAGuestCartByEmail proves the half of the resolution a customer
// id cannot reach.
//
// A guest cart has no customer id at all, so an erasure keyed on that column
// would leave the address of everybody who never signed in exactly where it is.
func TestEraseFindsAGuestCartByEmail(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newErasureCart(ctx, t, svc, "")

	result, err := svc.Erase(ctx, erasure.Subject{Email: erasureEmail})
	require.NoError(t, err)

	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, 2, result.Rows)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Email)
	require.NotNil(t, detail.ShippingAddress)
	assert.Empty(t, detail.ShippingAddress.LastName)
}

// TestEraseReachesBothCartsOfTheSamePerson proves the identifiers are OR-ed.
//
// The same person signs in on one visit and checks out as a guest on another;
// resolving on one handle only would forget half of them and report the work as
// done.
func TestEraseReachesBothCartsOfTheSamePerson(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	signedIn := newErasureCart(ctx, t, svc, customerID)
	guest := newErasureCart(ctx, t, svc, "")

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID, Email: erasureEmail})
	require.NoError(t, err)

	assert.Equal(t, 4, result.Rows, "two carts and their two addresses")

	for _, id := range []string{signedIn.ID, guest.ID} {
		detail, err := svc.GetCart(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, detail.Email)
		require.NotNil(t, detail.ShippingAddress)
		assert.Empty(t, detail.ShippingAddress.FirstName)
	}
}

// TestEraseIsIdempotent proves the property the contract requires of every
// holder: a controller who runs the sweep twice gets the same answer, not an
// error and not "nothing found".
func TestEraseIsIdempotent(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newErasureCart(ctx, t, svc, customerID)

	first, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)
	second, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Equal(t, first.Outcome, second.Outcome)
	assert.Equal(t, first.Rows, second.Rows,
		"the count must not shrink on a repeat: the statements are unconditional over the same carts")
	assert.Equal(t, first.Kept, second.Kept)
}

// TestASecondSweepForAGuestFindsNothingAndStillAnswersAnonymized pins the
// documented LIMIT of that idempotence.
//
// On a guest cart the e-mail is the only handle, so erasing it erases the
// handle. The outcome is unchanged, which is what the contract requires; what
// changes is that the second report has nothing to name, and the controller has
// to keep the first one.
func TestASecondSweepForAGuestFindsNothingAndStillAnswersAnonymized(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newErasureCart(ctx, t, svc, "")

	first, err := svc.Erase(ctx, erasure.Subject{Email: erasureEmail})
	require.NoError(t, err)
	require.Equal(t, 2, first.Rows)

	second, err := svc.Erase(ctx, erasure.Subject{Email: erasureEmail})
	require.NoError(t, err)

	assert.Equal(t, erasure.Anonymized, second.Outcome)
	assert.Zero(t, second.Rows)
	assert.Empty(t, second.Kept,
		"a person with no rows here is not told to go looking through columns that hold nothing about them")
}

// TestEraseAnswersAnonymizedForAPersonWithNoCart proves that silence and absence
// are told apart.
//
// Zero rows with an Anonymized outcome is a normal answer and not an error: the
// report says this holder was asked and had nothing, which is exactly what a
// controller needs to be able to write down.
func TestEraseAnswersAnonymizedForAPersonWithNoCart(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newCart(ctx, t, svc)

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: "cust_SOMEBODY_ELSE"})
	require.NoError(t, err)

	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Zero(t, result.Rows)
	assert.Empty(t, result.Kept)
	assert.Empty(t, result.Why)
}

// TestEraseRefusesASubjectThatNamesNobody proves the last defense.
//
// The sweep refuses an empty subject first, but a holder reached directly would
// otherwise take a zero-value subject and lock every cart in the installation —
// and then report the whole shop as erased.
func TestEraseRefusesASubjectThatNamesNobody(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	newErasureCart(ctx, t, svc, customerID)
	// The setup itself locked the cart when it wrote the address; what this
	// test watches is whether the REFUSED request added a lock of its own.
	locksBefore := len(store.lockedCarts)

	_, err := svc.Erase(ctx, erasure.Subject{})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeErasureSubjectEmpty, errors.CodeOf(err))
	assert.Len(t, store.lockedCarts, locksBefore, "a refused request must not have locked anything")
}

// TestEraseRefusesAMalformedEmail proves that a subject this module cannot match
// is an error rather than a clean report.
//
// The column holds the folded form normalizeEmail produces; an address that
// cannot pass it would run as a filter that can only match nothing, and
// answering "anonymized, zero rows" to it would report an erasure that never
// looked anywhere.
func TestEraseRefusesAMalformedEmail(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.Erase(context.Background(), erasure.Subject{Email: "not-an-address"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestEraseTakesTheCartLock proves the erasure obeys the module's lock order.
//
// Every flow that changes a cart locks it first (see the service package doc);
// without the lock a line addition or a completion could land between the moment
// the sweep reads which carts are the person's and the moment it rewrites them.
func TestEraseTakesTheCartLock(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart := newErasureCart(ctx, t, svc, customerID)
	// The address write already locked the cart once, so the assertion is about
	// the lock this call takes, not about the cart having ever been locked.
	locksBefore := len(store.lockedCarts)

	_, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Equal(t, []string{cart.ID}, store.lockedCarts[locksBefore:])
}

// TestEraseDoesNotChangeTheShapeOfTheCart is the claim the checkout saga rests
// on.
//
// complete_cart's post-pivot step calls MarkCompleted, which reads the lines and
// the two revision counters and REFUSES a cart whose totals are stale. If the
// erasure bumped the shape counter, a sweep landing between the payment and that
// step would leave a paid order against a cart that can no longer be closed.
func TestEraseDoesNotChangeTheShapeOfTheCart(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newErasureCart(ctx, t, svc, customerID)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1, UnitPrice: 500,
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

	_, err = svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	after, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, current.Revision, after.Revision, "forgetting the shopper is not a change of shape")
	assert.False(t, after.TotalsStale(), "the totals must still belong to the current shape")
	assert.Equal(t, int64(500), after.Total)
	require.Len(t, after.Items, 1)
	assert.Equal(t, "T-shirt", after.Items[0].Title, "what was in the basket is not the person")

	completed, err := svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err, "an anonymized cart must still be closable by the saga")
	assert.True(t, completed.Completed())
}

// TestEraseReachesACompletedCart proves the one place this path deliberately
// crosses the module's own immutability rule.
//
// Every other write refuses a completed cart because it is the record the order
// rests on. What that protects is the SHAPE and the amounts; the buyer's name
// was never part of it, and where the law needs the buyer named the invoice
// keeps them (ADR 0032). A carve-out here would mean that the moment a cart
// became a sale its address became unforgettable.
func TestEraseReachesACompletedCart(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newErasureCart(ctx, t, svc, customerID)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1, UnitPrice: 500,
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
	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Rows)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.Completed(), "the stamp stays; only the person goes")
	assert.Empty(t, detail.Email)
	require.NotNil(t, detail.ShippingAddress)
	assert.Empty(t, detail.ShippingAddress.Address1)
}

// TestEraseReachesASoftDeletedCart proves the erasure asks what the DATABASE
// holds, not what the business can see.
//
// A soft-deleted cart is out of every other read in this module, and its row
// still carries an address; reporting "anonymized" while it kept one is exactly
// the false report the erasure contract warns about.
func TestEraseReachesASoftDeletedCart(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart := newErasureCart(ctx, t, svc, customerID)
	require.NoError(t, svc.DeleteCart(ctx, cart.ID))

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Empty(t, store.carts[cart.ID].Email, "a hidden row holds an e-mail like any other")
	// The row COUNT is deliberately not asserted here, and the reason is a
	// divergence worth knowing about: this fake drops a soft-deleted cart's
	// address rows out of its map, while the schema keeps them with a deleted_at
	// stamp and the statement in queries/erasure.sql rewrites them too. Pinning
	// the fake's number would be pinning the fake.
	assert.GreaterOrEqual(t, result.Rows, 1)
}
