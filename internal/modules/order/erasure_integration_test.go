//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// # Why the erasure can ONLY be proven here
//
// Nothing else in this module can catch a wrong anonymization. No DTO reads the
// address columns — the admin and store responses do not carry the address at
// all — so an erasure that missed a column, or one that emptied the Go model
// while leaving the database row populated, is invisible to every unit test and
// to every API test. The service tests run against a fake store, and a fake
// that agreed with a broken statement would agree with it silently: what those
// tests prove is the DECISION (which order is settled, what the report says),
// while what has to be proven here is the GROUND — that the columns really are
// NULL in the database, that the ones that must survive really did, and that a
// second sweep really answers the same thing.
//
// The strongest assertion in the file is derived rather than written out: it
// walks the module's own declaration ([order.Module.PersonalData]) and requires
// every named column that the report does not list as kept to be NULL. A column
// added to the declaration and forgotten in queries/erasure.sql fails here, and
// that pair is the one place where the report can start lying while everything
// still compiles.
package order_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// erasureKeyColumns says which column ties a table to the order.
//
// It is a map rather than an if because the assertion that walks the
// declaration has to FAIL on a table it does not know: a Named column declared
// on a table nobody taught this test about would otherwise be skipped in
// silence, which is the same defect the declaration exists to prevent.
var erasureKeyColumns = map[string]string{
	"orders":          "id",
	"order_addresses": "order_id",
}

// erasureAddress produces one address of an order, filled in every personal
// column so that a column left behind is visible.
func erasureAddress(kind models.AddressType, source string) models.OrderAddress {
	return models.OrderAddress{
		Type:            kind,
		SourceAddressID: source,
		FirstName:       "Ayse",
		LastName:        "Yilmaz",
		Company:         "Gobit Muhasebe",
		Address1:        "Ataturk Cad. 12",
		Address2:        "Daire 3",
		City:            "Istanbul",
		Province:        "Kadikoy",
		PostalCode:      "34710",
		CountryCode:     "TR",
		Phone:           "+90 555 000 00 00",
		Metadata:        map[string]any{"delivery_note": "leave with the doorman"},
	}
}

// placeOrderFor opens an order for the given person, with both addresses.
//
// Every test in this file uses its OWN customer id and e-mail. That is not
// tidiness: an erasure sweeps by customer id, so a test reusing the shared
// testCustomerID would anonymize the orders the other integration tests in this
// package placed, and the failure would land in a file that has nothing to do
// with erasure.
func placeOrderFor(t *testing.T, svc *service.Service, customerID, email string) models.Order {
	t.Helper()

	in := validInput()
	in.CustomerID = customerID
	in.Email = email
	in.Metadata = map[string]any{"channel": "web"}
	in.Items[0].Metadata = map[string]any{"engraving": "for Ayse"}
	in.Addresses = []models.OrderAddress{
		erasureAddress(models.AddressShipping, "addr_SHIP"),
		erasureAddress(models.AddressBilling, "addr_BILL"),
	}

	ord, err := svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)

	return ord
}

// settleOrder completes the order and records the collection, which is what
// makes it erasable: a completed order with nothing outstanding.
func settleOrder(t *testing.T, svc *service.Service, ord models.Order) {
	t.Helper()

	ctx := context.Background()
	_, err := svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)
	_, err = svc.SetOrderSummaryTotals(ctx, ord.ID, service.SummaryTotalsInput{PaidTotal: ord.Total})
	require.NoError(t, err)
}

// erasureStamp reads the moment the order's personal columns were rewritten.
//
// It goes to the database directly because no model carries the stamp: nothing
// in the module READS it, so putting it on models.Order would have added a
// field with no reader. The one thing that has to see it is this test.
func erasureStamp(t *testing.T, orderID string) *time.Time {
	t.Helper()

	var stamp *time.Time
	require.NoError(t, testPool.Pool().QueryRow(context.Background(),
		`SELECT personal_data_erased_at FROM orders WHERE id = $1`, orderID).Scan(&stamp))

	return stamp
}

// columnIsNullEverywhere reports whether the column is NULL on every row that
// belongs to the order.
//
// COALESCE turns "no rows at all" into false rather than into NULL, so a query
// that matched nothing FAILS the assertion instead of passing it. A test that
// looked for a value in a table it never wrote to would otherwise report the
// erasure as complete without having checked anything.
func columnIsNullEverywhere(t *testing.T, table, column, orderID string) bool {
	t.Helper()

	key, known := erasureKeyColumns[table]
	require.True(t, known,
		"the declaration names table %q and this test does not know how to reach it from an "+
			"order; teach it the key column rather than letting the column go unchecked", table)

	var allNull bool
	query := fmt.Sprintf(
		`SELECT COALESCE(bool_and(%s IS NULL), false) FROM %s WHERE %s = $1`, column, table, key)
	require.NoError(t, testPool.Pool().QueryRow(context.Background(), query, orderID).Scan(&allNull))

	return allNull
}

// textColumn reads one text column of the orders row.
func textColumn(t *testing.T, column, orderID string) *string {
	t.Helper()

	var value *string
	query := fmt.Sprintf(`SELECT %s FROM orders WHERE id = $1`, column)
	require.NoError(t, testPool.Pool().QueryRow(context.Background(), query, orderID).Scan(&value))

	return value
}

// TestErasureAnonymizesASettledOrder is the whole claim of the module's answer,
// asserted against the database rather than against the model.
func TestErasureAnonymizesASettledOrder(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_ERASE_SETTLED"
		email      = "settled.person@example.com"
	)

	ord := placeOrderFor(t, svc, customerID, email)
	settleOrder(t, svc, ord)

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Anonymized, result.Outcome)
	assert.Equal(t, order.ModuleName, result.Holder,
		"the holder has to be the module's name; the sweep prints it next to the other holders")
	assert.Equal(t, 3, result.Rows, "one order row and its two address rows were written")
	assert.NotEmpty(t, result.Why, "an anonymized answer that kept columns has to say why")

	// The declaration and the erasure have to agree. Every column the module
	// says it holds is either listed as kept or really NULL; there is no third
	// state, and the third state is exactly what a partial anonymization looks
	// like from the outside.
	declaration := order.New().PersonalData()
	require.Equal(t, order.ModuleName, declaration.Holder)
	require.NotEmpty(t, declaration.Holdings)

	declared := make(map[string]struct{}, len(declaration.Holdings))
	for _, holding := range declaration.Holdings {
		declared[holding.Table+"."+holding.Column] = struct{}{}
	}
	for _, kept := range result.Kept {
		assert.Contains(t, declared, kept,
			"the report keeps %q, which the declaration does not mention at all", kept)
	}

	kept := make(map[string]struct{}, len(result.Kept))
	for _, entry := range result.Kept {
		kept[entry] = struct{}{}
	}

	checked := 0
	for _, holding := range declaration.Holdings {
		path := holding.Table + "." + holding.Column
		if _, stays := kept[path]; stays {
			continue
		}

		require.Equal(t, personaldata.Named, holding.Kind,
			"%s is erased and declared open; gobit does not rewrite a free-form column", path)
		assert.True(t, columnIsNullEverywhere(t, holding.Table, holding.Column, ord.ID),
			"%s is neither kept nor NULL: the report says this person was anonymized and the "+
				"column still holds them", path)
		checked++
	}
	require.Positive(t, checked,
		"no column was checked; the declaration or the kept list has gone empty and this "+
			"assertion approves anything")

	// The free-form columns the answer promised to leave alone really were left
	// alone. This is the half that makes the word "anonymized" honest.
	assert.Contains(t, result.Kept, "orders.metadata")
	assert.Contains(t, result.Kept, "orders.cancel_reason")
	assert.Contains(t, result.Kept, "order_line_items.metadata")
	assert.Contains(t, result.Kept, "order_addresses.metadata")
	assert.Contains(t, result.Kept, "orders.customer_id")
	assert.Contains(t, result.Kept, "order_addresses.country_code")

	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)

	assert.Empty(t, detail.Email, "the contact address is gone")
	assert.Equal(t, customerID, detail.CustomerID,
		"the customer id stays: it is the only indexed handle a repeated request has")
	assert.Equal(t, ord.DisplayID, detail.DisplayID, "the order number is not personal data")
	assert.Equal(t, models.OrderCompleted, detail.Status, "the sale still stands")
	assert.Equal(t, ord.Total, detail.Total, "the amounts are untouched")
	assert.Equal(t, map[string]any{"channel": "web"}, detail.Metadata,
		"the caller's own data is not rewritten (ADR 0029)")
	require.Len(t, detail.Items, 1)
	assert.Equal(t, map[string]any{"engraving": "for Ayse"}, detail.Items[0].Metadata)

	require.NotNil(t, detail.ShippingAddress,
		"the address ROW survives; an absent row would read as 'this order never had one'")
	require.NotNil(t, detail.BillingAddress)
	assert.Equal(t, models.AddressShipping, detail.ShippingAddress.Type)
	assert.Equal(t, "TR", detail.ShippingAddress.CountryCode)
	assert.Equal(t, map[string]any{"delivery_note": "leave with the doorman"},
		detail.ShippingAddress.Metadata)
	assert.Empty(t, detail.ShippingAddress.FirstName)
	assert.Empty(t, detail.ShippingAddress.LastName)
	assert.Empty(t, detail.ShippingAddress.Company)
	assert.Empty(t, detail.ShippingAddress.Address1)
	assert.Empty(t, detail.ShippingAddress.Address2)
	assert.Empty(t, detail.ShippingAddress.City)
	assert.Empty(t, detail.ShippingAddress.Province)
	assert.Empty(t, detail.ShippingAddress.PostalCode)
	assert.Empty(t, detail.ShippingAddress.Phone)
	assert.Empty(t, detail.ShippingAddress.SourceAddressID)
	assert.Empty(t, detail.BillingAddress.Phone)

	stamp := erasureStamp(t, ord.ID)
	require.NotNil(t, stamp, "the erasure has to be recorded; without the stamp a second sweep "+
		"cannot tell an erased order from one that never had an e-mail")
}

// TestErasureForgetsAnOrderThatWasRefundedInFull proves the settlement rule
// against the columns that actually maintain the money.
//
// It belongs here and not only in the service tests because the defect it
// guards was a disagreement between a Go expression and the SQL beside it:
// SetOrderSummaryTotals merges paid_total with GREATEST, so paid_total never
// shrinks and a refund grows refunded_total beside it. Reading the pair as
// total - (paid - refunded) — the difference a payment screen wants — made this
// order owe its whole total for ever, and the module answered RETAINED for it on
// every sweep. A person who had been given all of their money back was the one
// person the order module could never forget.
func TestErasureForgetsAnOrderThatWasRefundedInFull(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_ERASE_REFUNDED"
		email      = "refunded.person@example.com"
	)

	ord := placeOrderFor(t, svc, customerID, email)
	_, err := svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)

	// Paid in full and then refunded in full. The two writes are what a capture
	// event and a refund event leave behind, in that order.
	_, err = svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: ord.Total})
	require.NoError(t, err)
	summary, err := svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: ord.Total, RefundedTotal: ord.Total})
	require.NoError(t, err)
	require.Equal(t, ord.Total, summary.PaidTotal,
		"the collected amount is a LIFETIME total and the refund does not take it back down")
	require.Equal(t, ord.Total, summary.RefundedTotal)

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Anonymized, result.Outcome,
		"nothing is owed in either direction on a sale that was collected and given back: %s",
		result.Why)
	assert.Equal(t, 3, result.Rows, "one order row and its two address rows were written")

	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Email, "the contact address is gone")
	assert.Equal(t, ord.Total, detail.Summary.PaidTotal,
		"the money the sale moved is the sale's own record and the erasure does not touch it")
	assert.Equal(t, ord.Total, detail.Summary.RefundedTotal)

	require.NotNil(t, erasureStamp(t, ord.ID))
}

// TestErasureIsIdempotent verifies the contract's own requirement: the second
// sweep answers the same thing and does not move the moment of the first.
func TestErasureIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_ERASE_TWICE"
		email      = "twice.person@example.com"
	)

	ord := placeOrderFor(t, svc, customerID, email)
	settleOrder(t, svc, ord)

	first, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)
	require.Equal(t, personaldata.Anonymized, first.Outcome)

	firstStamp := erasureStamp(t, ord.ID)
	require.NotNil(t, firstStamp)

	second, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	assert.Equal(t, first.Outcome, second.Outcome,
		"a repeated sweep must not turn into 'nothing found'")
	assert.Equal(t, first.Rows, second.Rows,
		"the row count is what a controller compares two reports by; it must not shrink")
	assert.Equal(t, first.Kept, second.Kept)
	assert.Equal(t, first.Why, second.Why)

	secondStamp := erasureStamp(t, ord.ID)
	require.NotNil(t, secondStamp)
	assert.True(t, firstStamp.Equal(*secondStamp),
		"the FIRST erasure keeps its moment; re-running a report must not make the record claim "+
			"the person was forgotten today")
}

// TestErasureRetainsAnUnsettledOrder covers the one carve-out, once per fact
// that can produce it.
//
// The reason string is asserted and not only the outcome: "retained" with no
// reason is useless to whoever has to answer the person, and a refusal that
// fires on the wrong fact still produces a perfectly well-formed report.
func TestErasureRetainsAnUnsettledOrder(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	cases := []struct {
		name       string
		customerID string
		email      string
		// unsettle brings the order into the state under test and returns the
		// fragment the reason has to name.
		unsettle func(t *testing.T, ord models.Order) string
	}{
		{
			name:       "a pending order is held by its status",
			customerID: "cus_ERASE_PENDING",
			email:      "pending.person@example.com",
			unsettle: func(_ *testing.T, _ models.Order) string {
				// A freshly placed order is already pending; nothing to do.
				return "still pending"
			},
		},
		{
			name:       "a completed order that was never paid is held by the money",
			customerID: "cus_ERASE_OWED",
			email:      "owed.person@example.com",
			unsettle: func(t *testing.T, ord models.Order) string {
				_, err := svc.CompleteOrder(ctx, ord.ID)
				require.NoError(t, err)

				return "money is still outstanding"
			},
		},
		{
			name:       "an open return holds a settled order",
			customerID: "cus_ERASE_RETURN",
			email:      "return.person@example.com",
			unsettle: func(t *testing.T, ord models.Order) string {
				settleOrder(t, svc, ord)
				_, err := svc.CreateReturn(ctx, service.CreateReturnInput{
					OrderID:      ord.ID,
					RefundAmount: 100,
					Reason:       "the shirt did not fit Ayse",
				})
				require.NoError(t, err)

				return "a return has been requested"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ord := placeOrderFor(t, svc, tc.customerID, tc.email)
			fragment := tc.unsettle(t, ord)

			result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: tc.customerID, Email: tc.email})
			require.NoError(t, err)

			assert.Equal(t, personaldata.Retained, result.Outcome)
			assert.Equal(t, 0, result.Rows, "nothing was rewritten")
			assert.Contains(t, result.Why, fragment,
				"the reason has to name WHICH fact refused the erasure")
			assert.Contains(t, result.Why, strconv.FormatInt(ord.DisplayID, 10),
				"the reason has to name the order the person can look up")
			assert.NotEmpty(t, result.Kept,
				"a retained answer without a kept list is a refusal a controller cannot pass on")

			// A retained order keeps EVERYTHING: the point of the carve-out is
			// that the order is still being performed with these fields.
			contact := textColumn(t, "email", ord.ID)
			require.NotNil(t, contact)
			assert.Equal(t, tc.email, *contact)
			assert.False(t, columnIsNullEverywhere(t, "order_addresses", "first_name", ord.ID))
			assert.Nil(t, erasureStamp(t, ord.ID), "an order that was not erased carries no stamp")
		})
	}
}

// TestErasureAnonymizesTheSettledOrdersOfARetainedPerson is the half of the
// carve-out that is easiest to get wrong.
//
// Retaining everything because ONE order is open would keep far more than the
// fact justifies; anonymizing everything would strand the open order. The
// module does both, in one transaction, and answers Retained — which is the
// only outcome that stays true of what it now holds.
func TestErasureAnonymizesTheSettledOrdersOfARetainedPerson(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_ERASE_MIXED"
		email      = "mixed.person@example.com"
	)

	settled := placeOrderFor(t, svc, customerID, email)
	settleOrder(t, svc, settled)
	open := placeOrderFor(t, svc, customerID, email)

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Retained, result.Outcome,
		"the person's data is still held, so the honest single answer is Retained")
	assert.Equal(t, 3, result.Rows, "the settled order and its two addresses were still written")
	assert.Contains(t, result.Why, strconv.FormatInt(open.DisplayID, 10))
	assert.Contains(t, result.Why, "1 settled order was anonymized",
		"the report has to say that the settled order WAS anonymized in the same transaction")

	assert.True(t, columnIsNullEverywhere(t, "orders", "email", settled.ID),
		"the settled order was anonymized")
	assert.True(t, columnIsNullEverywhere(t, "order_addresses", "phone", settled.ID))
	assert.NotNil(t, erasureStamp(t, settled.ID))

	stillThere := textColumn(t, "email", open.ID)
	require.NotNil(t, stillThere, "the pending order keeps the contact it is performed with")
	assert.Equal(t, email, *stillThere)
	assert.Nil(t, erasureStamp(t, open.ID))
}

// TestErasureLeavesTheCancellationReason proves the promise the kept list makes
// about caller-typed text, on the one such column that is not a jsonb.
//
// A canceled order is settled — the amount outstanding on it will never be
// collected by anybody, and reading it as unsettled would make a cancellation a
// permanent refusal to forget somebody — so it IS anonymized, and its
// cancel_reason survives that anonymization.
func TestErasureLeavesTheCancellationReason(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_ERASE_CANCELED"
		email      = "canceled.person@example.com"
		reason     = "the customer changed their mind on the phone"
	)

	ord := placeOrderFor(t, svc, customerID, email)
	require.NoError(t, svc.CancelOrder(ctx, ord.ID, reason))

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Anonymized, result.Outcome,
		"a canceled order carries an outstanding amount forever; that must not hold a person "+
			"forever with it")
	assert.True(t, columnIsNullEverywhere(t, "orders", "email", ord.ID))

	kept := textColumn(t, "cancel_reason", ord.ID)
	require.NotNil(t, kept, "the typed reason is free-form text and gobit does not rewrite it")
	assert.Equal(t, reason, *kept)
	assert.Contains(t, result.Kept, "orders.cancel_reason",
		"a column that survives has to be NAMED in the answer, or the word anonymized covers it")
}

// TestErasureFindsAGuestOrderByEMailAlone verifies the handle a guest order has
// and the consequence of erasing it.
//
// The second call is the interesting half: the e-mail WAS the handle, so once
// it is gone the module can no longer find the row. The outcome stays the same,
// which is what idempotence requires, and the row count does not — the FIRST
// report is the one that names what stayed.
func TestErasureFindsAGuestOrderByEMailAlone(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const email = "guest.person@example.com"

	ord := placeOrderFor(t, svc, "", email)
	require.Empty(t, ord.CustomerID, "this is a guest order")
	settleOrder(t, svc, ord)

	first, err := svc.Erase(ctx, personaldata.Subject{Email: email})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Anonymized, first.Outcome)
	assert.Equal(t, 3, first.Rows)
	assert.True(t, columnIsNullEverywhere(t, "order_addresses", "last_name", ord.ID))

	second, err := svc.Erase(ctx, personaldata.Subject{Email: email})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Anonymized, second.Outcome,
		"the outcome may not change; the person is still not in this module")
	assert.Equal(t, 0, second.Rows,
		"the handle itself was erased, so the second sweep finds nothing to write")
	assert.Empty(t, second.Kept,
		"nothing can be reported as kept for a row this module can no longer reach")
}

// TestErasureRefusesASubjectThatNamesNobody is the last defense behind the
// sweep's own check.
//
// A zero-value subject reaching the module would select every order in the
// installation and lock all of them. Erasing everybody is not an erasure
// request.
func TestErasureRefusesASubjectThatNamesNobody(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.Erase(context.Background(), personaldata.Subject{})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "it is the caller's mistake, not a fault: %v", err)
	assert.Equal(t, service.CodeErasureSubjectEmpty, errors.CodeOf(err))
}

// TestErasureNormalizesTheSubjectEMail verifies that the address is matched in
// the form the column HOLDS.
//
// CreateOrder folds the e-mail to lower case before writing it, so a sweep that
// matched the raw string would silently miss every person whose address reached
// it capitalized — and would report a clean "anonymized, zero rows" while doing
// so, which is the worst shape a bug in this path can take.
func TestErasureNormalizesTheSubjectEMail(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const email = "mixed.case.person@example.com"

	ord := placeOrderFor(t, svc, "", email)
	settleOrder(t, svc, ord)

	result, err := svc.Erase(ctx, personaldata.Subject{Email: strings.ToUpper(email)})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Anonymized, result.Outcome)
	assert.Equal(t, 3, result.Rows, "the capitalized address had to find the same order")
}

// TestTheModuleRefusesToEraseBeforeItIsWired verifies the one branch that must
// not answer quietly.
//
// A module whose service was never registered has erased nothing, and reporting
// "anonymized, zero rows" from it would reach the data subject as a completed
// erasure. The sweep turns an error into a PARTIAL report instead, which is the
// answer that is true.
func TestTheModuleRefusesToEraseBeforeItIsWired(t *testing.T) {
	_, err := order.New().Erase(context.Background(), personaldata.Subject{CustomerID: "cus_X"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"a module that was not wired is a setup fault, not a bad request: %v", err)
}

// TestThePersonalDataDeclarationIsReadable checks the declaration on its own,
// without a database.
//
// It is what an embedder enumerates in order to answer "where could this person
// be" (ADR 0029's third obligation), so an entry with no reason, or a table
// named twice with the same column, is a defect of the answer itself.
func TestThePersonalDataDeclarationIsReadable(t *testing.T) {
	declaration := order.New().PersonalData()

	require.Equal(t, order.ModuleName, declaration.Holder)
	require.NotEmpty(t, declaration.Holdings)

	seen := make(map[string]struct{}, len(declaration.Holdings))
	for _, holding := range declaration.Holdings {
		path := holding.Table + "." + holding.Column
		assert.NotContains(t, seen, path, "%s is declared twice", path)
		seen[path] = struct{}{}

		assert.NotEmpty(t, holding.Why, "%s is declared without saying what it holds", path)
		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
			"%s has no kind, so nothing says whether gobit wrote the person in there", path)
	}

	// The two columns the whole answer turns on: the one that is erased and the
	// one that deliberately is not.
	assert.Contains(t, seen, "orders.email")
	assert.Contains(t, seen, "orders.customer_id")
}
