package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// These tests are about the ANSWER: which rows become records, which columns
// they carry, what the two empty states say, and that a read stays a read. What
// a fake store cannot prove is that the statements really reach the rows, and
// that is disclosure_integration_test.go's half, for the same reason the erasure
// is split that way: a fake that agreed with a broken statement would agree with
// it silently.

// disclosableInput produces an order filled in everything the module declares,
// so that a column the disclosure drops is visible as a missing field rather
// than as an empty one.
func disclosableInput(customerID, email string) service.CreateOrderInput {
	in := validInput()
	in.CustomerID = customerID
	in.Email = email
	in.IdempotencyKey = "checkout-" + customerID
	in.Metadata = map[string]any{"channel": "web"}
	in.Items[0].Metadata = map[string]any{"engraving": "for Ayse"}
	in.Addresses = []models.OrderAddress{{
		Type:            models.AddressShipping,
		SourceAddressID: "addr_SHIP",
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
	}}

	return in
}

// recordsOf returns the disclosed records of one table.
func recordsOf(disclosure personaldata.Disclosure, table string) []personaldata.Record {
	var out []personaldata.Record
	for _, record := range disclosure.Records {
		if record.Table == table {
			out = append(out, record)
		}
	}

	return out
}

// fieldValue returns the value of one column of a record, and whether the record
// carried the column at all.
//
// The two answers are separated on purpose: a field that is present and nil says
// "nothing is kept here", a field that is absent says the disclosure never
// mentioned the column, and a test that could not tell them apart would pass on
// either.
func fieldValue(record personaldata.Record, column string) (any, bool) {
	for _, field := range record.Fields {
		if field.Column == column {
			return field.Value, true
		}
	}

	return nil, false
}

// TestPersonalDataOfDisclosesTheOrderItsAddressAndTheTextTheShopTyped is the
// whole claim of the read, on one person with one of everything.
func TestPersonalDataOfDisclosesTheOrderItsAddressAndTheTextTheShopTyped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	ord, err := e.svc.CreateOrder(ctx, disclosableInput(testCustomerID, "ayse@example.com"))
	require.NoError(t, err)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      ord.ID,
		RefundAmount: 100,
		Reason:       "the shirt did not fit Ayse",
		Note:         "she said she would come by the shop on Friday",
	})
	require.NoError(t, err)

	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{
		CustomerID: testCustomerID,
		Email:      "ayse@example.com",
	})
	require.NoError(t, err)

	assert.Equal(t, service.ErasureHolder, disclosure.Holder,
		"the holder is the module; it is what names this part of the dossier")
	assert.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.Empty(t, disclosure.Why,
		"Why explains a state that is not Disclosed; here the Kind on every field carries what "+
			"there is to say")
	require.NotEmpty(t, disclosure.Records,
		"a Disclosed state with no records is not a state this contract has")

	orders := recordsOf(disclosure, "orders")
	require.Len(t, orders, 1)
	assert.Equal(t, ord.ID, orders[0].ID)

	customerID, present := fieldValue(orders[0], "customer_id")
	require.True(t, present)
	assert.Equal(t, testCustomerID, customerID)

	email, present := fieldValue(orders[0], "email")
	require.True(t, present)
	assert.Equal(t, "ayse@example.com", email)

	metadata, present := fieldValue(orders[0], "metadata")
	require.True(t, present)
	assert.Equal(t, map[string]any{"channel": "web"}, metadata,
		"the caller's own document is handed over whole; gobit never inspected it")

	cancelReason, present := fieldValue(orders[0], "cancel_reason")
	require.True(t, present,
		"a column the declaration names is mentioned even when it holds nothing, or the person "+
			"cannot tell an empty column from one nobody looked in")
	assert.Nil(t, cancelReason, "this order was never canceled")

	addresses := recordsOf(disclosure, "order_addresses")
	require.Len(t, addresses, 1)
	street, present := fieldValue(addresses[0], "address_1")
	require.True(t, present)
	assert.Equal(t, "Ataturk Cad. 12", street)

	lines := recordsOf(disclosure, "order_line_items")
	require.Len(t, lines, 1, "the line carries the engraving the customer typed")
	engraving, present := fieldValue(lines[0], "metadata")
	require.True(t, present)
	assert.Equal(t, map[string]any{"engraving": "for Ayse"}, engraving)

	returns := recordsOf(disclosure, "order_returns")
	require.Len(t, returns, 1)
	reason, present := fieldValue(returns[0], "reason")
	require.True(t, present)
	assert.Equal(t, "the shirt did not fit Ayse", reason)
	note, present := fieldValue(returns[0], "note")
	require.True(t, present)
	assert.Equal(t, "she said she would come by the shop on Friday", note,
		"an operator's note about what the customer said is the person's data too")
}

// TestPersonalDataOfListsExactlyTheDeclaredColumns is the rule of ADR 0034 point
// 4, checked against the declaration itself.
//
// Reaching PAST the declaration would hand the person data the declaration told
// the controller was not there; stopping SHORT of it would contradict the same
// document. Both are one assertion here because both are one mistake.
func TestPersonalDataOfListsExactlyTheDeclaredColumns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	ord, err := e.svc.CreateOrder(ctx, disclosableInput(testCustomerID, "ayse@example.com"))
	require.NoError(t, err)
	_, err = e.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: ord.ID,
		Type:    models.ClaimRefund,
		Reason:  "one of the three shirts was missing from the parcel",
	})
	require.NoError(t, err)

	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	declared := make(map[string][]string)
	for _, holding := range service.PersonalDataHoldings() {
		declared[holding.Table] = append(declared[holding.Table], holding.Column)
	}

	checked := 0
	for _, record := range disclosure.Records {
		columns := make([]string, 0, len(record.Fields))
		for _, field := range record.Fields {
			columns = append(columns, field.Column)
		}

		assert.Equal(t, declared[record.Table], columns,
			"the %s record carries a different column list from the one the module declares",
			record.Table)
		checked++
	}
	require.Positive(t, checked, "no record was checked; this assertion approves anything")
}

// TestPersonalDataOfResolvesBOTHHandles is the guest half of the subject.
//
// The two identifiers are OR-ed and not AND-ed. A guest order carries an e-mail
// with no customer id and the same person may also have a registered order; a
// disclosure that required both would find neither, and one that took only the
// first would leave the guest's own purchase out of her file.
func TestPersonalDataOfResolvesBOTHHandles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	const (
		email      = "ayse@example.com"
		otherEmail = "ayse.work@example.com"
	)

	guest, err := e.svc.CreateOrder(ctx, disclosableInput("", email))
	require.NoError(t, err)
	require.Empty(t, guest.CustomerID, "this one is a guest order")

	registered, err := e.svc.CreateOrder(ctx, disclosableInput(testCustomerID, otherEmail))
	require.NoError(t, err)

	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{
		CustomerID: testCustomerID,
		Email:      email,
	})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	found := make(map[string]struct{})
	for _, record := range recordsOf(disclosure, "orders") {
		found[record.ID] = struct{}{}
	}

	assert.Contains(t, found, guest.ID, "the guest order is reached by the address alone")
	assert.Contains(t, found, registered.ID,
		"the registered order is reached by the customer id, whose address is a different one")
}

// TestPersonalDataOfAnswersNothingWhenThePersonIsNotHere covers the state that
// is easiest to get wrong, because an empty answer looks the same either way.
func TestPersonalDataOfAnswersNothingWhenThePersonIsNotHere(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.CreateOrder(ctx, disclosableInput(testCustomerID, "ayse@example.com"))
	require.NoError(t, err)

	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{
		CustomerID: "cus_SOMEBODY_ELSE",
		Email:      "nobody@example.com",
	})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Nothing, disclosure.State,
		"the holder searched and this person is not in it; that is not Disclosed with no records")
	assert.Empty(t, disclosure.Records)
	require.NotEmpty(t, disclosure.Why,
		"'we found nothing' is an answer a person may dispute; the sentence that says where the "+
			"search went is what makes it checkable")
	assert.Contains(t, disclosure.Why, "every order the database still holds",
		"the sentence has to say how far the search went, not merely that it happened")
	assert.Contains(t, disclosure.Why, "guest",
		"the one thing this module cannot reach is an erased guest order; leaving it unsaid "+
			"would make the answer sound more complete than it is")
}

// TestPersonalDataOfRefusesASubjectThatNamesNobody is the last defense behind
// the sweep's own check.
//
// A zero-value subject reaching this module would match every order in the
// installation and hand the caller a copy of it. It is the mirror image of the
// erasure's refusal and it carries its own code, because the two accidents are
// opposites: one destroys everybody's data, this one discloses it.
func TestPersonalDataOfRefusesASubjectThatNamesNobody(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	_, err := e.svc.PersonalDataOf(context.Background(), personaldata.Subject{})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "it is the caller's mistake, not a fault: %v", err)
	assert.Equal(t, service.CodeDisclosureSubjectEmpty, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "a disclosure request",
		"the refusal has to say WHICH request it refused")
}

// TestPersonalDataOfNormalizesTheSubjectEMail verifies that the address is
// matched in the form the column HOLDS.
//
// CreateOrder folds the e-mail to lower case on the way in, so a disclosure that
// matched the raw string would answer a confident "we hold nothing about you" to
// every person whose address reached it capitalized — which is the worst shape a
// bug on this path can take, because it is indistinguishable from the truth.
func TestPersonalDataOfNormalizesTheSubjectEMail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	const email = "ayse@example.com"

	_, err := e.svc.CreateOrder(ctx, disclosableInput("", email))
	require.NoError(t, err)

	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{Email: strings.ToUpper(email)})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Disclosed, disclosure.State,
		"the capitalized address had to find the same order")
}

// TestPersonalDataOfStillShowsAnOrderThatWasAlreadyErased is the decision this
// file argues at length, asserted.
//
// An anonymized order still HAS its rows, and the question a data subject asks
// is what the database still holds. Answering "nothing" about a row that exists
// would be false; and the emptied fields are the only way she can see that the
// erasure happened at all, rather than that the columns were never read.
func TestPersonalDataOfStillShowsAnOrderThatWasAlreadyErased(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	ord, err := e.svc.CreateOrder(ctx, disclosableInput(testCustomerID, "ayse@example.com"))
	require.NoError(t, err)
	_, err = e.svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: ord.Total})
	require.NoError(t, err)
	_, err = e.svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)

	result, err := e.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Anonymized, result.Outcome)

	// The customer id survives an erasure by decision, so the person is still
	// reachable here. That is the case this test needs; a guest whose only
	// handle was the e-mail is unreachable afterwards and the Nothing sentence
	// says so.
	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	orders := recordsOf(disclosure, "orders")
	require.Len(t, orders, 1)

	email, present := fieldValue(orders[0], "email")
	require.True(t, present,
		"the column has to be mentioned; it is how she sees that it was searched and is empty")
	assert.Nil(t, email, "the erasure nulled it")

	metadata, present := fieldValue(orders[0], "metadata")
	require.True(t, present)
	assert.Equal(t, map[string]any{"channel": "web"}, metadata,
		"gobit never rewrites a free-form column, so an erased order is exactly where the "+
			"remaining free text lives")

	addresses := recordsOf(disclosure, "order_addresses")
	require.Len(t, addresses, 1,
		"the address ROW survives the erasure and is still hers; dropping it would answer that "+
			"this order never had an address")

	firstName, present := fieldValue(addresses[0], "first_name")
	require.True(t, present)
	assert.Nil(t, firstName)

	country, present := fieldValue(addresses[0], "country_code")
	require.True(t, present)
	assert.Equal(t, "TR", country, "the one address column the erasure keeps")
}

// TestPersonalDataOfLeavesOutARowThatHoldsNothing is the other half of the rule.
//
// A line, a return, an exchange and a claim declare nothing but text somebody
// typed. A row where none of it was typed holds nothing about anybody, and
// twenty such records in a dossier would bury the one that has a note in it. The
// order and its address are the opposite case and are asserted here too, so that
// the rule is visible as one decision rather than two.
func TestPersonalDataOfLeavesOutARowThatHoldsNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	in := disclosableInput(testCustomerID, "ayse@example.com")
	in.Items[0].Metadata = nil
	in.Addresses[0] = models.OrderAddress{Type: models.AddressShipping}
	ord, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	_, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: ord.ID})
	require.NoError(t, err)

	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	assert.Empty(t, recordsOf(disclosure, "order_line_items"),
		"the line's only declared column is the metadata and nobody wrote any")
	assert.Empty(t, recordsOf(disclosure, "order_exchanges"),
		"an exchange with no note and no metadata holds nothing about anybody")
	assert.Len(t, recordsOf(disclosure, "orders"), 1)
	assert.Len(t, recordsOf(disclosure, "order_addresses"), 1,
		"an address row is a row gobit writes the person into, so it is disclosed even empty")
}

// TestPersonalDataOfNamesTheOrderOnEveryChildRecord pins the identifier shape.
//
// personaldata.Record has no parent field and order_id is not declared personal
// data, so it may not travel as a field. A return note that cannot be tied to a
// purchase is worth very little to the person reading it, so the child's
// identifier says which order it hangs off — which names the row, and names it
// with an id that is already in the dossier as another record's.
func TestPersonalDataOfNamesTheOrderOnEveryChildRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	ord, err := e.svc.CreateOrder(ctx, disclosableInput(testCustomerID, "ayse@example.com"))
	require.NoError(t, err)

	disclosure, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)

	for _, record := range disclosure.Records {
		if record.Table == "orders" {
			assert.Equal(t, ord.ID, record.ID)

			continue
		}
		assert.True(t, strings.HasPrefix(record.ID, ord.ID+"/"),
			"the %s record's identifier says nothing about which order it belongs to: %q",
			record.Table, record.ID)
	}
}

// TestPersonalDataOfWritesNothingAndLocksNothing is the property the whole file
// rests on.
//
// A disclosure runs on an installation that is selling: it must not take the row
// lock the erasure takes, must not write, and must not change what a later
// erasure would answer. The lock is the one of the three a fake can see directly,
// and the fake records every lock it is asked for.
func TestPersonalDataOfWritesNothingAndLocksNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)

	ord, err := e.svc.CreateOrder(ctx, disclosableInput(testCustomerID, "ayse@example.com"))
	require.NoError(t, err)

	before, err := e.svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	e.store.lockedOrders = nil

	_, err = e.svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)

	assert.Empty(t, e.store.lockedOrders,
		"a read that locked the order would make a person's question block the shop's checkout")

	after, err := e.svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Equal(t, before.Email, after.Email)
	assert.Equal(t, before.UpdatedAt, after.UpdatedAt,
		"nothing was written, so nothing was stamped")

	// And the erasure still has everything to do: a disclosure that had emptied
	// a column on the way past would show up here as a shrunken erasure.
	result, err := e.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Retained, result.Outcome,
		"the order is still pending, which is what it was before the disclosure looked at it")
}
