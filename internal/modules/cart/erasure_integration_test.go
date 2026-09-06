//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// # Why the erasure can ONLY be proven here
//
// The service tests run against a fake store, and a fake that agreed with a
// broken statement would agree with it silently: what those tests prove is the
// DECISION (what the report says, what it counts, what it refuses), and
// erasure_internal_test.go proves that the declaration and the text of the
// statements describe the same columns. Neither can prove that the statements
// FIND the right rows and empty them — a WHERE clause that matches nothing
// passes every test above and reports a clean erasure over an untouched
// database.
//
// The strongest assertion in the file is derived rather than written out: it
// walks the module's own declaration — the PersonalData method — and requires
// every Named column that the report does not list as kept to be NULL in the
// database. A column added to the declaration and forgotten in
// queries/erasure.sql fails here, and that pair is the one place where the
// report can start lying while everything still compiles.
package cart_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	cartmod "github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// erasureKeyColumns says which column ties a table to the cart.
//
// It is a map rather than an if because the assertion that walks the
// declaration has to FAIL on a table it does not know: a Named column declared
// on a table nobody taught this test about would otherwise be skipped in
// silence, which is the same defect the declaration exists to prevent.
var erasureKeyColumns = map[string]string{
	"carts":          "id",
	"cart_addresses": "cart_id",
}

// erasureAddress produces one address, filled in every personal column so that
// a column left behind is visible rather than empty-looking.
func erasureAddress(source string) service.AddressInput {
	return service.AddressInput{
		SourceAddressID: source,
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
}

// newErasureCart opens a cart for the given person and fills both addresses.
//
// Both are written on purpose: the address statement works from the cart id and
// not from an address id, so a cart with a shipping AND a billing address is
// what catches a statement that only ever reaches one row.
func newErasureCart(
	ctx context.Context, t *testing.T, svc *service.Service, customerID, email string,
) models.Cart {
	t.Helper()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     testRegionID,
		CustomerID:   customerID,
		Email:        email,
		CurrencyCode: testCurrency,
		Metadata:     map[string]any{"campaign": "spring"},
	})
	require.NoError(t, err)

	_, err = svc.SetShippingAddress(ctx, cart.ID, erasureAddress("addr_book_1"))
	require.NoError(t, err)
	_, err = svc.SetBillingAddress(ctx, cart.ID, erasureAddress("addr_book_2"))
	require.NoError(t, err)

	return cart
}

// countNotNull returns how many of the cart's rows in the table still hold a
// value in the column.
func countNotNull(ctx context.Context, t *testing.T, table, column, cartID string) int {
	t.Helper()

	key, ok := erasureKeyColumns[table]
	require.True(t, ok, "%s is declared and this test does not know how to reach it", table)

	// The table and column names are interpolated because they come from the
	// module's own declaration rather than from any input, and asking a
	// declaration-driven question is not possible with a placeholder.
	var count int
	query := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s = $1 AND %s IS NOT NULL", table, key, column)
	require.NoError(t, testPool.Pool().QueryRow(ctx, query, cartID).Scan(&count))

	return count
}

// TestErasureEmptiesEveryDeclaredColumnItDoesNotKeep is the ground the report
// stands on.
//
// It asks the module what it holds, subtracts what the answer said it kept, and
// requires the database to be empty of the rest. Nothing is retyped here: a
// column added to the declaration is checked from the moment it is added.
func TestErasureEmptiesEveryDeclaredColumnItDoesNotKeep(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_ERASURE_DECLARED"
	cart := newErasureCart(ctx, t, svc, customerID, "declared.erasure@example.com")

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)
	require.Equal(t, erasure.Anonymized, result.Outcome)
	// One cart row and its two addresses.
	assert.Equal(t, 3, result.Rows)

	kept := map[string]bool{}
	for _, entry := range result.Kept {
		kept[entry] = true
	}

	for _, holding := range cartmod.New().PersonalData().Holdings {
		key := holding.Table + "." + holding.Column
		if kept[key] {
			continue
		}
		require.Equal(t, erasure.Named, holding.Kind,
			"%s is an OPEN column and was not reported as kept; gobit does not rewrite "+
				"free-form data, so an answer that does not name it is claiming something "+
				"it did not do", key)
		assert.Zero(t, countNotNull(ctx, t, holding.Table, holding.Column, cart.ID),
			"%s is not in Result.Kept and the database still holds a value in it", key)
	}

	// The kept Named columns are the other half of the claim: they have to be
	// STILL THERE, because the report told the controller they are.
	assert.Equal(t, 1, countNotNull(ctx, t, "carts", "customer_id", cart.ID),
		"the customer id is reported as kept and is the handle a repeated sweep needs")
	assert.Equal(t, 2, countNotNull(ctx, t, "cart_addresses", "country_code", cart.ID),
		"the country is reported as kept: it is jurisdiction, and it keeps the row readable as an address")
}

// TestErasureLeavesTheFreeFormColumnsWhereTheyAre proves the rule that keeps the
// word "anonymized" honest, against the database rather than against a fake.
func TestErasureLeavesTheFreeFormColumnsWhereTheyAre(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_ERASURE_OPEN"
	cart := newErasureCart(ctx, t, svc, customerID, "open.erasure@example.com")

	_, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"campaign": "spring"}, detail.Metadata)
	require.NotNil(t, detail.ShippingAddress)
	assert.Equal(t, map[string]any{"note": "leave it with the doorman"}, detail.ShippingAddress.Metadata)
	require.NotNil(t, detail.BillingAddress)
	assert.Equal(t, map[string]any{"note": "leave it with the doorman"}, detail.BillingAddress.Metadata)
}

// TestErasureFindsAGuestCartByEmail proves the half of the resolution a customer
// id cannot reach: a guest cart has no customer id at all.
func TestErasureFindsAGuestCartByEmail(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const email = "guest.erasure@example.com"
	cart := newErasureCart(ctx, t, svc, "", email)

	// The subject arrives as the shopper typed it; the column holds the folded
	// form, and matching with anything else would silently miss every row.
	result, err := svc.Erase(ctx, erasure.Subject{Email: "Guest.Erasure@Example.COM"})
	require.NoError(t, err)

	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, 3, result.Rows)
	assert.Zero(t, countNotNull(ctx, t, "carts", "email", cart.ID))
	assert.Zero(t, countNotNull(ctx, t, "cart_addresses", "last_name", cart.ID))
}

// TestErasureIsIdempotentOnTheDatabase proves what the contract demands of a
// second sweep: the same outcome and the same count, not an error and not
// "nothing found".
func TestErasureIsIdempotentOnTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_ERASURE_TWICE"
	newErasureCart(ctx, t, svc, customerID, "twice.erasure@example.com")

	first, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)
	second, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Equal(t, first.Outcome, second.Outcome)
	assert.Equal(t, first.Rows, second.Rows,
		"the statements are unconditional over the same carts; a shrinking count would "+
			"look to a controller like data that had gone missing")
	assert.Equal(t, first.Kept, second.Kept)
}

// TestErasureReachesHiddenAndClosedCarts proves the two filters this read
// deliberately omits.
//
// A soft-deleted cart is out of every other read in the module and a completed
// one refuses every other write, and both still hold an address. Reporting
// "anonymized" while either kept one is exactly the false report the erasure
// contract warns about.
func TestErasureReachesHiddenAndClosedCarts(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_ERASURE_HIDDEN"

	deleted := newErasureCart(ctx, t, svc, customerID, "hidden.erasure@example.com")
	require.NoError(t, svc.DeleteCart(ctx, deleted.ID))

	completed := newErasureCart(ctx, t, svc, customerID, "hidden.erasure@example.com")
	item, err := svc.AddLineItem(ctx, completed.ID, service.AddLineItemInput{
		VariantID: "variant_ERASURE", Title: "T-shirt", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)
	current, err := svc.GetCart(ctx, completed.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, completed.ID, service.Totals{
		Revision: current.Revision,
		Subtotal: 500, Total: 500,
		Lines: []service.LineTotals{{
			LineItemID: item.ID, UnitPrice: 500, Subtotal: 500, Total: 500,
		}},
	}))
	_, err = svc.MarkCompleted(ctx, completed.ID)
	require.NoError(t, err)

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)

	for _, id := range []string{deleted.ID, completed.ID} {
		assert.Zero(t, countNotNull(ctx, t, "carts", "email", id),
			"a hidden or closed row holds an e-mail like any other")
		assert.Zero(t, countNotNull(ctx, t, "cart_addresses", "address_1", id))
		assert.Zero(t, countNotNull(ctx, t, "cart_addresses", "source_address_id", id))
	}

	// The closed cart is still the record the order rests on: its shape and its
	// amounts survive the person's departure, which is the whole argument for
	// anonymizing rather than deleting it.
	after, err := svc.GetCart(ctx, completed.ID)
	require.NoError(t, err)
	assert.True(t, after.Completed())
	assert.Equal(t, current.Revision, after.Revision)
	assert.False(t, after.TotalsStale())
	require.Len(t, after.Items, 1)
	assert.Equal(t, int64(500), after.Total)
}

// TestErasureLeavesOtherPeopleAlone is the assertion that would catch a WHERE
// clause matching too much.
//
// It is the failure with the worst consequence in this file: an erasure that
// swept the whole table would report a perfectly ordinary "anonymized" while
// emptying the addresses of every shopper in the installation.
func TestErasureLeavesOtherPeopleAlone(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_ERASURE_ONE"
	mine := newErasureCart(ctx, t, svc, customerID, "one.erasure@example.com")
	somebodyElse := newErasureCart(ctx, t, svc, "cust_ERASURE_OTHER", "other.erasure@example.com")

	_, err := svc.Erase(ctx, erasure.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Zero(t, countNotNull(ctx, t, "carts", "email", mine.ID))
	assert.Equal(t, 1, countNotNull(ctx, t, "carts", "email", somebodyElse.ID),
		"another person's cart must be untouched")
	assert.Equal(t, 2, countNotNull(ctx, t, "cart_addresses", "first_name", somebodyElse.ID))
}
