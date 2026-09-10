//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// # Why the disclosure can ONLY be proven here
//
// The service tests run against a fake store and prove the DECISION: which rows
// become records, what the answer says when nothing was found, what it says when
// the bound cut. service/disclosure_internal_test.go proves that the statements
// and the declaration name the same columns. Neither can prove that the
// statements FIND the right rows — a WHERE clause that matched nothing would
// pass every one of them and report a tidy "we hold nothing about you", which is
// the single worst answer this mechanism can give.
//
// Five things below exist only here. That the search reaches the soft-deleted
// and completed carts every other read in the module hides. That it does NOT
// reach another person's carts. That the OR resolving the two handles really
// finds a row matching EITHER of them, which no fixture whose rows carry both
// can distinguish from an AND. That the columns coming back are the declared
// ones and no others, on a round trip where the statement, the generated struct
// and the readers are all in play at once. And that the count behind the
// truncation notice — a window function evaluated before the LIMIT — really
// counts what matched rather than what was returned; no fake can be wrong about
// that in the way a database can.
//
// # The one state this file does not prove
//
// [personaldata.Unresolvable] is not asserted anywhere below, and that is not
// an omission: this module never answers it. Every column it declares hangs off
// a cart, and a cart carries at least one of the two handles from the moment it
// holds anything at all, so there is no row here whose owner cannot be named.
// The state belongs to a holder that declares and cannot disclose, and it is
// entered by the COORDINATOR rather than by any module — what proves it is
// internal/workflows/datasubject's own test over a declaring non-discloser,
// which is where such a holder exists. Writing a cart test that produced
// Unresolvable would mean faking the defect the state describes.
package cart_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	cartmod "github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// disclosureNote is the free-form document the tests put on a line and on a
// shipping method: the two Open columns, holding a name somebody typed.
var disclosureNote = map[string]any{"engraving": "Ay\u015fe"}

// newDisclosureCart opens a cart carrying something in EVERY declared column —
// the contact on the header, both addresses, a line with a note and a shipping
// method with provider data — so that a column the disclosure forgets is visible
// rather than merely empty.
func newDisclosureCart(
	ctx context.Context, t *testing.T, svc *service.Service, customerID, email string,
) models.Cart {
	t.Helper()

	cart := newErasureCart(ctx, t, svc, customerID, email)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_DISCLOSURE", Title: "T-shirt", Quantity: 1, UnitPrice: 500,
		Metadata: disclosureNote,
	})
	require.NoError(t, err)
	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "standard", Amount: 100, Data: disclosureNote,
	})
	require.NoError(t, err)

	return cart
}

// disclosedValues flattens a disclosure into "table.column" -> value.
func disclosedValues(d personaldata.Disclosure) map[string]any {
	out := map[string]any{}
	for _, record := range d.Records {
		for _, field := range record.Fields {
			out[record.Table+"."+field.Column] = field.Value
		}
	}

	return out
}

// TestDisclosureCarriesEveryDeclaredColumnFromTheDatabase is the ground the
// document stands on.
//
// It asks the module what it declares and requires the disclosure of a fully
// populated cart to produce every one of those columns, with the value the
// database holds. Nothing is retyped: a column added to the declaration is
// checked from the moment it is added, and a SELECT list that stopped fetching
// one fails here even though the Go code around it still compiles.
func TestDisclosureCarriesEveryDeclaredColumnFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_DECLARED"
	const email = "declared.disclosure@example.com"
	cart := newDisclosureCart(ctx, t, svc, customerID, email)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.Equal(t, service.ErasureHolder, disclosure.Holder)

	values := disclosedValues(disclosure)
	for _, holding := range cartmod.New(cartmod.Options{}).PersonalData().Holdings {
		key := holding.Table + "." + holding.Column
		assert.Contains(t, values, key,
			"%s is declared and the disclosure did not carry it; the person receives a "+
				"document short of what the declaration promised", key)
	}

	// The values are the DATABASE's, not the request's: the e-mail comes back
	// folded because that is how the column holds it.
	assert.Equal(t, customerID, values["carts.customer_id"])
	assert.Equal(t, email, values["carts.email"])
	assert.Equal(t, "Ba\u011fdat Caddesi 100", values["cart_addresses.address_1"])
	assert.Equal(t, "TR", values["cart_addresses.country_code"])
	assert.Equal(t, disclosureNote, values["cart_line_items.metadata"])
	assert.Equal(t, disclosureNote, values["cart_shipping_methods.data"])

	// One record per row: the cart, its two addresses, the one noted line and the
	// shipping method. The cart is first, because the document is read as a
	// session rather than as four tables.
	require.Len(t, disclosure.Records, 5)
	assert.Equal(t, "carts", disclosure.Records[0].Table)
	assert.Equal(t, cart.ID, disclosure.Records[0].ID)
}

// TestDisclosureReachesHiddenAndClosedCarts proves the two filters this read
// deliberately omits, against the database that enforces them everywhere else.
//
// A soft-deleted cart is out of every other read in the module and a completed
// one refuses every other write, and both still hold an address. Answering "we
// hold nothing" while either of them carried the person's name is exactly the
// false answer the disclosure exists to prevent.
func TestDisclosureReachesHiddenAndClosedCarts(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_HIDDEN"
	const email = "hidden.disclosure@example.com"

	deleted := newErasureCart(ctx, t, svc, customerID, email)
	require.NoError(t, svc.DeleteCart(ctx, deleted.ID))

	completed := newErasureCart(ctx, t, svc, customerID, email)
	item, err := svc.AddLineItem(ctx, completed.ID, service.AddLineItemInput{
		VariantID: "variant_DISCLOSE_HIDDEN", Title: "T-shirt", Quantity: 1, UnitPrice: 500,
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

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	found := map[string]bool{}
	for _, record := range disclosure.Records {
		if record.Table == "carts" {
			found[record.ID] = true
		}
	}
	assert.True(t, found[deleted.ID], "a hidden row holds an address like any other")
	assert.True(t, found[completed.ID], "a closed cart holds one too")
}

// TestDisclosureFindsAGuestCartByEmail proves the half of the resolution a
// customer id cannot reach.
//
// The erasure can empty a guest's cart, so the disclosure has to be able to show
// it; a module that could find more rows to delete than to show would reopen, in
// a smaller form, the asymmetry this interface was added to close.
func TestDisclosureFindsAGuestCartByEmail(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const email = "guest.disclosure@example.com"
	cart := newDisclosureCart(ctx, t, svc, "", email)

	// The subject arrives as the shopper typed it; the column holds the folded
	// form, and matching with anything else would silently miss every row.
	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{Email: "Guest.Disclosure@Example.COM"})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	require.NotEmpty(t, disclosure.Records)
	assert.Equal(t, cart.ID, disclosure.Records[0].ID)
	assert.Contains(t, disclosure.Why, "e-mail address alone")
}

// TestDisclosureLeavesOtherPeopleOut is the failure with the worst consequence
// in this file.
//
// A WHERE clause matching too much would hand one person another person's name,
// address and phone number in a document a controller sends out — the mirror of
// the erasure's "swept the whole table", and harder to notice afterwards.
func TestDisclosureLeavesOtherPeopleOut(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_ONE"
	mine := newDisclosureCart(ctx, t, svc, customerID, "one.disclosure@example.com")
	somebodyElse := newDisclosureCart(ctx, t, svc, "cust_DISCLOSE_OTHER", "other.disclosure@example.com")

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	for _, record := range disclosure.Records {
		if record.Table == "carts" {
			assert.Equal(t, mine.ID, record.ID, "only this person's carts may appear")
			assert.NotEqual(t, somebodyElse.ID, record.ID)
		}
	}
	values := disclosedValues(disclosure)
	assert.Equal(t, "one.disclosure@example.com", values["carts.email"])
}

// TestDisclosureSaysNothingRatherThanAnEmptyList proves the state that keeps a
// dossier honest, against the database.
//
// "Searched and found nothing" and "could not search" would be the same document
// to a reader if both came back as an empty list, and only the first is true
// here — which is why the answer is Nothing and carries the sentence saying
// where it looked.
func TestDisclosureSaysNothingRatherThanAnEmptyList(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: "cust_DISCLOSE_ABSENT"})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Nothing, disclosure.State)
	assert.Empty(t, disclosure.Records)
	assert.Contains(t, disclosure.Why, "soft-deleted")
}

// TestDisclosureWritesNothing is the "this is a READ" claim, made against the
// rows themselves.
//
// updated_at is the witness: every writing path in this module stamps it, so a
// statement that had quietly become an UPDATE — or a transaction that touched a
// row on its way past — moves it. A disclosure that changed the data it was
// describing would also be changing the answer to the next request.
func TestDisclosureWritesNothing(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_READONLY"
	cart := newDisclosureCart(ctx, t, svc, customerID, "readonly.disclosure@example.com")

	var before, after string
	const stamps = `SELECT (SELECT max(updated_at) FROM carts WHERE id = $1)::text ||
		' ' || (SELECT max(updated_at) FROM cart_addresses WHERE cart_id = $1)::text`
	require.NoError(t, testPool.Pool().QueryRow(ctx, stamps, cart.ID).Scan(&before))

	_, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	require.NoError(t, testPool.Pool().QueryRow(ctx, stamps, cart.ID).Scan(&after))
	assert.Equal(t, before, after, "a disclosure leaves every row exactly as it found it")

	// And the person is still there: a read that emptied what it reported would
	// be an erasure nobody asked for.
	assert.Equal(t, 1, countNotNull(ctx, t, "carts", "email", cart.ID))
	assert.Equal(t, 2, countNotNull(ctx, t, "cart_addresses", "first_name", cart.ID))
}

// TestDisclosureBoundsTheDossierAndCountsWhatItLeftOut proves the one number no
// fake can be trusted about.
//
// The truncation notice rests on count(*) OVER (), a window function PostgreSQL
// evaluates after the WHERE and before the LIMIT. If that were ever wrong — or
// were replaced by a second COUNT statement, or by the length of the returned
// slice — the notice would report the bound back to itself and a person would be
// told the document is complete when it is not. That is the whole reason this
// test creates more carts than the bound.
func TestDisclosureBoundsTheDossierAndCountsWhatItLeftOut(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_BOUND"
	const extra = 2
	held := int(service.MaxDisclosedCarts) + extra

	newest := ""
	for i := range held {
		cart, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID:     testRegionID,
			CustomerID:   customerID,
			CurrencyCode: testCurrency,
			Metadata:     map[string]any{"visit": i},
		})
		require.NoError(t, err)
		newest = cart.ID
	}

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.Len(t, disclosure.Records, int(service.MaxDisclosedCarts),
		"these carts have no children, so one record each")
	assert.Equal(t, newest, disclosure.Records[0].ID, "the most recent carts are the ones kept")

	assert.Contains(t, disclosure.Why, fmt.Sprintf("found %d carts", held))
	assert.Contains(t, disclosure.Why, fmt.Sprintf("the remaining %d", extra))
	assert.Contains(t, disclosure.Why, "are still held here and are not listed")
}

// TestDisclosureResolvesBothHandlesOfOnePerson proves the OR that every other
// test in this file is blind to.
//
// The fixtures above give each cart BOTH handles, so an AND, a COALESCE or a
// clause that quietly preferred one identifier would pass all of them. Here the
// same person has one cart carrying only a customer id — opened while signed in
// — and one carrying only an e-mail — opened as a guest — and a single request
// naming both handles has to bring back both rows. It is the shape the erasure
// resolves, and a disclosure that resolved less would show a person fewer carts
// than gobit would empty on the same request.
func TestDisclosureResolvesBothHandlesOfOnePerson(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_BOTH"
	const email = "both.disclosure@example.com"

	// No e-mail on this one: the customer id is the only thing that can find it.
	signedIn, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CustomerID: customerID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	// And no customer id on this one.
	guest := newDisclosureCart(ctx, t, svc, "", email)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{
		CustomerID: customerID, Email: email,
	})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	var carts []string
	for _, record := range disclosure.Records {
		if record.Table == "carts" {
			carts = append(carts, record.ID)
		}
	}
	assert.ElementsMatch(t, []string{signedIn.ID, guest.ID}, carts,
		"each of these carts answers to exactly one of the two handles; a clause that "+
			"required both, or that used only one, returns a document missing half of "+
			"this person's shopping")

	// The sentence says both handles were available, which is what tells the
	// person there is no unsearched half to come back and ask about.
	assert.Contains(t, disclosure.Why, "customer id and e-mail address")
}

// TestDisclosureCarriesNoColumnOutsideTheDeclaration is the other half of
// TestDisclosureCarriesEveryDeclaredColumnFromTheDatabase.
//
// That test requires the declaration to be a subset of what came back; this one
// requires what came back to be a subset of the declaration, and the two
// together are ADR 0034's "exactly". Reaching past the declaration is the worse
// direction: the declaration is the document that told the controller where
// this person's data is NOT, and a dossier carrying a column it does not name
// contradicts an answer somebody has already given.
//
// The undeclared values checked for by name are real values on the very rows
// these statements read — the cart's currency and region, the line's title, the
// shipping method's name, the address's type. A SELECT * with a generous reader
// would carry every one of them, and only a database has them to carry.
func TestDisclosureCarriesNoColumnOutsideTheDeclaration(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_ONLY_DECLARED"
	newDisclosureCart(ctx, t, svc, customerID, "onlydeclared.disclosure@example.com")

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	declared := map[string]personaldata.Kind{}
	for _, holding := range cartmod.New(cartmod.Options{}).PersonalData().Holdings {
		declared[holding.Table+"."+holding.Column] = holding.Kind
	}

	for _, record := range disclosure.Records {
		for _, field := range record.Fields {
			key := record.Table + "." + field.Column
			kind, ok := declared[key]
			require.True(t, ok,
				"%s came back in a dossier and the declaration does not name it; the "+
					"controller has been told this person's data is not there", key)
			// The judgement rides ON the value (ADR 0034), so it has to be the
			// judgement the module published about that column: an Open value is
			// one gobit never looked inside, and a reader who is told it is Named
			// would take the framework's word for something nobody checked.
			assert.Equal(t, kind, field.Kind, "%s carries a kind the declaration does not give it", key)
			assert.NotNil(t, field.Value, "%s was carried with no value at all", key)
		}
	}

	// These sit in the same rows and are not about the person: the currency and
	// region describe the shop, the title and method name describe what was
	// offered, the address type describes what the address was for.
	values := disclosedValues(disclosure)
	undeclared := []any{testCurrency, testRegionID, "T-shirt", "variant_DISCLOSURE", "standard"}
	for _, value := range values {
		assert.NotContains(t, undeclared, value,
			"a value the module does not declare as personal data left the database")
	}
}

// TestDisclosureShowsExactlyWhatTheErasureReportedAsKept holds the two halves of
// this module's personal-data answer against each other, over one database.
//
// The erasure tells a controller which columns it KEPT, and the controller
// repeats that to the person. If the disclosure then omitted one of them, the
// person would be told a column still holds her data and shown a document
// without it; if it produced one the erasure claimed to have emptied, the
// erasure's report was false. Both lists are read from the module itself — the
// report's own Kept against the columns the dossier actually carried — so
// neither can be retyped into agreement, and this is the only place the pair
// meets, because a fake store answers both from the same hand-filled map.
func TestDisclosureShowsExactlyWhatTheErasureReportedAsKept(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	const customerID = "cust_DISCLOSE_AFTER_ERASURE"
	newDisclosureCart(ctx, t, svc, customerID, "aftererasure.disclosure@example.com")

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Anonymized, result.Outcome)
	require.NotEmpty(t, result.Kept)

	// The cart is still findable because the customer id is one of the columns
	// the erasure keeps; that is the same handle a repeated sweep uses.
	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	seen := map[string]bool{}
	carried := make([]string, 0, len(result.Kept))
	for _, record := range disclosure.Records {
		for _, field := range record.Fields {
			key := record.Table + "." + field.Column
			if seen[key] {
				continue
			}
			seen[key] = true
			carried = append(carried, key)
		}
	}

	assert.ElementsMatch(t, result.Kept, carried,
		"the cart was filled in every declared column before the erasure, so what the "+
			"database still holds is exactly what the report named as kept, and the "+
			"dossier has to be that same list")
}
