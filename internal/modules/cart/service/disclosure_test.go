package service_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// These tests prove what the DISCLOSURE decides — which rows become records,
// which columns may appear on them, what the answer says when it found nothing
// and what it says when the bound cut — against the fake store.
//
// What they cannot prove is the same thing the erasure's unit tests cannot: the
// GROUND. The statements are text, and a SELECT list that quietly dropped a
// column or a WHERE clause that reached another person's carts would pass every
// assertion here, because the fake answers from a map that a reader of
// queries/disclosure.sql filled in by hand. disclosure_internal_test.go holds
// the statements to the declaration column by column, and
// internal/modules/cart/disclosure_integration_test.go runs them against a
// database.
//
// The strongest assertion below is derived rather than written out: it walks
// the module's own declaration and requires the disclosure of a fully populated
// cart to carry exactly that set of columns, with exactly the Kind the
// declaration gave each one. A column added to the declaration and forgotten in
// the disclosure fails there on the day it is added.

// noteMetadata is the free-form document the tests put on a line and on a
// shipping method.
//
// It is a name typed by a shopper on purpose. The two Open columns are exactly
// where a person's data ends up without gobit ever knowing — an engraving, a
// gift message, a locker beside somebody's home — and a disclosure that skipped
// them would tell that person the framework holds nothing of theirs there.
var noteMetadata = map[string]any{"engraving": "Ay\u015fe"}

// newDisclosureCart opens a cart carrying something in every declared column:
// the contact on the header, both addresses, a line with a note and a shipping
// method with provider data.
func newDisclosureCart(ctx context.Context, t *testing.T, svc *service.Service, owner string) models.Cart {
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
	_, err = svc.SetBillingAddress(ctx, cart.ID, fullAddress)
	require.NoError(t, err)

	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1, UnitPrice: 500,
		Metadata: noteMetadata,
	})
	require.NoError(t, err)
	// A second line WITHOUT a note: a row that holds nothing about the person
	// must not become a record, and a cart with only noted lines could not tell
	// the difference.
	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantB, Title: "Mug", Quantity: 2, UnitPrice: 300,
	})
	require.NoError(t, err)

	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "standard", Amount: 100, Data: noteMetadata,
	})
	require.NoError(t, err)

	return cart
}

// disclosedColumns collects the "table.column" entries of a disclosure with the
// Kind each one arrived with.
func disclosedColumns(d personaldata.Disclosure) map[string]personaldata.Kind {
	out := map[string]personaldata.Kind{}
	for _, record := range d.Records {
		for _, field := range record.Fields {
			out[record.Table+"."+field.Column] = field.Kind
		}
	}

	return out
}

// declaredColumns is the same map, taken from the module's declaration.
func declaredColumns() map[string]personaldata.Kind {
	out := map[string]personaldata.Kind{}
	for _, holding := range service.PersonalDataHoldings() {
		out[holding.Table+"."+holding.Column] = holding.Kind
	}

	return out
}

// TestPersonalDataOfDisclosesExactlyTheDeclaredColumns is the assertion the
// whole file exists for.
//
// It is written as an equality between two maps rather than as a list of
// expected columns, and both directions of that equality are a rule. A column
// the declaration names and the disclosure does not produce is a promise the
// module made to a controller and did not keep; a column the disclosure
// produces and the declaration does not name is data handed to somebody after
// the declaration told the controller it was not there. The Kind travels with
// the value for the same reason and is compared with it: whoever reads the
// document has to know which values gobit can vouch for and which are the
// embedder's.
func TestPersonalDataOfDisclosesExactlyTheDeclaredColumns(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newDisclosureCart(ctx, t, svc, customerID)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.Equal(t, service.ErasureHolder, disclosure.Holder)
	assert.Equal(t, declaredColumns(), disclosedColumns(disclosure),
		"the disclosure and the declaration have to name the same columns: one is what the "+
			"controller was told is held, the other is what the person is handed")
}

// TestPersonalDataOfProducesOneRecordPerRow proves the shape of the document: a
// session at a time, a row at a time.
func TestPersonalDataOfProducesOneRecordPerRow(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newDisclosureCart(ctx, t, svc, customerID)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	tables := make([]string, 0, len(disclosure.Records))
	for _, record := range disclosure.Records {
		assert.NotEmpty(t, record.ID, "every row this module discloses can be named")
		tables = append(tables, record.Table)
	}
	// The cart, its two addresses, the ONE line that carries a note and the
	// shipping method. The second line is absent because it holds nothing about
	// the person.
	assert.Equal(t, []string{
		"carts", "cart_addresses", "cart_addresses", "cart_line_items", "cart_shipping_methods",
	}, tables)
	assert.Equal(t, cart.ID, disclosure.Records[0].ID)
}

// TestPersonalDataOfCarriesTheValuesThemselves checks that the fields hold what
// the shopper typed, not just the right column names.
func TestPersonalDataOfCarriesTheValuesThemselves(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newDisclosureCart(ctx, t, svc, customerID)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	values := map[string]any{}
	for _, record := range disclosure.Records {
		for _, field := range record.Fields {
			values[record.Table+"."+field.Column] = field.Value
		}
	}

	// The e-mail comes back FOLDED, because that is what the column holds; a
	// disclosure that echoed the subject's spelling would be showing the person
	// the request rather than the row.
	assert.Equal(t, "ayse.yilmaz@example.com", values["carts.email"])
	assert.Equal(t, customerID, values["carts.customer_id"])
	assert.Equal(t, fullAddress.FirstName, values["cart_addresses.first_name"])
	assert.Equal(t, fullAddress.Phone, values["cart_addresses.phone"])
	assert.Equal(t, fullAddress.CountryCode, values["cart_addresses.country_code"])
	// The free-form documents are handed over exactly as they are stored: gobit
	// does not inspect them, so it cannot summarize them either.
	assert.Equal(t, map[string]any{"campaign": "spring"}, values["carts.metadata"])
	assert.Equal(t, noteMetadata, values["cart_line_items.metadata"])
	assert.Equal(t, noteMetadata, values["cart_shipping_methods.data"])
}

// TestPersonalDataOfWritesNothingAndTakesNoLock is the "this is a READ" claim,
// made as an assertion.
//
// The lock list is the fake's record of every cart a caller asked to lock, and
// a disclosure appearing in it would mean the read had joined the queue behind
// a checkout — and could block one. The row content is checked afterwards
// because a read that changed what it describes would be the worse half of the
// same mistake.
func TestPersonalDataOfWritesNothingAndTakesNoLock(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	cart := newDisclosureCart(ctx, t, svc, customerID)
	// The locks the FIXTURE took are counted first and not cleared: building the
	// cart legitimately locks it on every write, and a test that reset the list
	// would also hide a disclosure that had started taking one under a helper.
	locksBefore := len(store.lockedCarts)

	_, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Len(t, store.lockedCarts, locksBefore, "a disclosure locks nothing")

	after, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, "ayse.yilmaz@example.com", after.Email)
	require.NotNil(t, after.ShippingAddress)
	assert.Equal(t, fullAddress.FirstName, after.ShippingAddress.FirstName)
}

// TestPersonalDataOfFindsAGuestCartByEmail proves the half of the resolution a
// customer id cannot reach.
//
// It is the same claim the erasure makes and it has to hold in both directions:
// a module able to find a guest's cart in order to empty it and unable to find
// it in order to show it would be the exact asymmetry this interface closes.
func TestPersonalDataOfFindsAGuestCartByEmail(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newDisclosureCart(ctx, t, svc, "")

	// The subject arrives as the shopper typed it; the column holds the folded
	// form.
	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{Email: erasureEmail})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.NotEmpty(t, disclosure.Records)
	assert.Contains(t, disclosure.Why, "e-mail address alone",
		"a search by one handle says which carts it could not have found")
}

// TestPersonalDataOfLeavesOtherPeopleOut is the failure with the worst
// consequence in this file: a disclosure that over-matched would hand one person
// another person's address.
func TestPersonalDataOfLeavesOtherPeopleOut(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	mine := newDisclosureCart(ctx, t, svc, customerID)
	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: "cust_OTHER", Email: "other@example.com",
		CurrencyCode: currency,
	})
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	for _, record := range disclosure.Records {
		if record.Table == "carts" {
			assert.Equal(t, mine.ID, record.ID, "only this person's carts may appear")
		}
	}
}

// TestPersonalDataOfSaysNothingRatherThanAnEmptyList proves the state that keeps
// the dossier honest.
//
// "Disclosed with no records" and "we could not look" would be the same document
// to a reader. The contract has three states so that a holder which searched and
// found nothing can say so, and it requires the sentence that makes the claim
// checkable — which is why the Why is asserted to name where it looked.
func TestPersonalDataOfSaysNothingRatherThanAnEmptyList(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newDisclosureCart(ctx, t, svc, customerID)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: "cust_NOBODY"})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Nothing, disclosure.State)
	assert.Empty(t, disclosure.Records)
	assert.NotEmpty(t, disclosure.Why)
	assert.Contains(t, disclosure.Why, "soft-deleted",
		"the sentence has to say that the search covered the rows every other read hides")
}

// TestPersonalDataOfRefusesASubjectWithNoHandle guards the request that names
// nobody.
//
// Answering "nothing found" to it would report a search that never looked
// anywhere, and the code is its own so that an operator is not sent to the
// erasure path to find out what went wrong.
func TestPersonalDataOfRefusesASubjectWithNoHandle(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.PersonalDataOf(context.Background(), personaldata.Subject{})
	require.Error(t, err)
	assert.Equal(t, service.CodeDisclosureSubjectEmpty, errors.CodeOf(err))
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestPersonalDataOfDropsARowThatHoldsNothingLeft proves the rule about child
// records, using the state an erasure leaves behind.
//
// An address with no country and no metadata is empty in every declared column
// once the person has been forgotten. Emitting a record with no fields would
// tell the reader "there is a row here" and nothing about them; the cart record
// stays, because the cart's existence is itself a fact about the person.
func TestPersonalDataOfDropsARowThatHoldsNothingLeft(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, Email: erasureEmail, CurrencyCode: currency,
	})
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
		FirstName: "Ay\u015fe", LastName: "Y\u0131lmaz", Phone: "+90 555 000 00 00",
	})
	require.NoError(t, err)

	_, err = svc.Erase(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	require.Len(t, disclosure.Records, 1, "the emptied address row is not a record")
	assert.Equal(t, "carts", disclosure.Records[0].Table)
	// The customer id is what the erasure deliberately keeps, and it is the one
	// column still holding the person here; the report of the erasure names it
	// as kept and this document shows it.
	require.Len(t, disclosure.Records[0].Fields, 1)
	assert.Equal(t, "customer_id", disclosure.Records[0].Fields[0].Column)
}

// TestPersonalDataOfBoundsTheDossierAndSaysWhatItLeftOut is the test for the
// decision a shop's cart table forces.
//
// Three claims, and the third is the one that matters: the answer carries at
// most the bound, it carries the NEWEST carts, and it states in its own Why how
// many are held, how many are listed and where the cut fell. A silent
// truncation would be worse than a big document, because the person cannot tell
// an omission from an absence.
func TestPersonalDataOfBoundsTheDossierAndSaysWhatItLeftOut(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	const extra = 3
	held := int(service.MaxDisclosedCarts) + extra
	newest := ""
	for i := range held {
		cart, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
			Metadata: map[string]any{"visit": strconv.Itoa(i)},
		})
		require.NoError(t, err)
		newest = cart.ID
	}

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.Len(t, disclosure.Records, int(service.MaxDisclosedCarts))
	assert.Equal(t, newest, disclosure.Records[0].ID, "the most recent carts are the ones kept")

	assert.Contains(t, disclosure.Why, fmt.Sprintf("found %d carts", held))
	assert.Contains(t, disclosure.Why, fmt.Sprintf("the %d most recent", service.MaxDisclosedCarts))
	assert.Contains(t, disclosure.Why, fmt.Sprintf("the remaining %d", extra))
	assert.Contains(t, disclosure.Why, "are still held here and are not listed")
}

// TestPersonalDataOfSaysSoWhenNothingWasLeftOut is the other half of the
// previous test: an answer that is complete has to say THAT, not stay silent
// and let a reader guess which kind of answer they are holding.
func TestPersonalDataOfSaysSoWhenNothingWasLeftOut(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	newDisclosureCart(ctx, t, svc, customerID)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	assert.Contains(t, disclosure.Why, "carries every cart it found")
	assert.NotContains(t, disclosure.Why, "not listed")
}
