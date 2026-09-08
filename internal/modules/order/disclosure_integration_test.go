//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// # Why the disclosure can ONLY be proven here
//
// The unit tests prove the ANSWER — which rows become records, which columns
// they carry, what the two empty states say — against a fake store, and a fake
// that agreed with a broken statement would agree with it silently. What has to
// be proven against a real database is the GROUND: that the six statements
// behind one dossier really reach the rows, and that a read really is a read.
//
// One claim left this file with ADR 0054. The disclosure used to be the only
// read that reached a SOFT-DELETED row, and there are none: an order retires by
// status and the module's six deleted_at columns are gone. What made the
// statements special is now their SHAPE — no lock, no write, many orders at a
// time — and that is what the tests below still hold them to.
//
// The last of those is the reason this file exists at all rather than trusting
// the SQL by inspection. queries/disclosure.sql takes no lock and writes
// nothing, and nothing in Go can check that: the statements are text. Here they
// run, and the row's updated_at is read before and after.
package order_test

import (
	"context"
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

// disclosureRecordsOf returns the records of one table.
func disclosureRecordsOf(disclosure personaldata.Disclosure, table string) []personaldata.Record {
	var out []personaldata.Record
	for _, record := range disclosure.Records {
		if record.Table == table {
			out = append(out, record)
		}
	}

	return out
}

// disclosedField returns the value of one column of a record, and whether the
// record mentioned the column at all.
//
// The two answers stay apart because a field that is present and nil says
// "nothing is kept here" while an absent one says the disclosure never mentioned
// the column, and those are the two halves of the promise the declaration makes.
func disclosedField(record personaldata.Record, column string) (any, bool) {
	for _, field := range record.Fields {
		if field.Column == column {
			return field.Value, true
		}
	}

	return nil, false
}

// orderUpdatedAt reads the row's stamp, which is what proves a read wrote
// nothing.
func orderUpdatedAt(t *testing.T, orderID string) time.Time {
	t.Helper()

	var stamp time.Time
	require.NoError(t, testPool.Pool().QueryRow(context.Background(),
		`SELECT updated_at FROM orders WHERE id = $1`, orderID).Scan(&stamp))

	return stamp
}

// TestDisclosureShowsEveryDeclaredColumnOfEveryRow is the whole claim of the
// read, run against the real statements.
//
// It walks the module's own declaration and requires the dossier to carry
// exactly that column list per table — the same derivation the erasure test
// makes about what was NULLED, pointed at what is SHOWN.
func TestDisclosureShowsEveryDeclaredColumnOfEveryRow(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_DISCLOSE_FULL"
		email      = "disclosed.person@example.com"
	)

	ord := placeOrderFor(t, svc, customerID, email)
	_, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      ord.ID,
		RefundAmount: 100,
		Reason:       "the shirt did not fit Ayse",
		Note:         "she said she would come by the shop on Friday",
		Metadata:     map[string]any{"channel": "phone"},
	})
	require.NoError(t, err)
	_, err = svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: ord.ID,
		Type:    models.ClaimRefund,
		Reason:  "one shirt was missing from the parcel",
	})
	require.NoError(t, err)
	_, err = svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: ord.ID,
		Note:    "she wants the same shirt one size larger",
	})
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx,
		personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	assert.Equal(t, order.ModuleName, disclosure.Holder)
	assert.Equal(t, personaldata.Disclosed, disclosure.State)
	require.NotEmpty(t, disclosure.Records,
		"a Disclosed state with no records is not a state this contract has")

	// The declaration and the disclosure have to agree, column for column and in
	// order. A record short of a declared column is indistinguishable, to the
	// person reading it, from a column of hers that is empty.
	declaration := order.New().PersonalData()
	require.NotEmpty(t, declaration.Holdings)

	declared := make(map[string][]string)
	for _, holding := range declaration.Holdings {
		declared[holding.Table] = append(declared[holding.Table], holding.Column)
	}

	seen := make(map[string]struct{}, len(declared))
	for _, record := range disclosure.Records {
		columns := make([]string, 0, len(record.Fields))
		for _, field := range record.Fields {
			columns = append(columns, field.Column)
		}
		require.Equal(t, declared[record.Table], columns,
			"the %s record does not carry the columns the module declares for that table",
			record.Table)
		seen[record.Table] = struct{}{}
	}

	// Every table that has a row here has to be represented. A statement that
	// silently returned nothing would otherwise pass every assertion above.
	for _, table := range []string{
		"orders", "order_line_items", "order_addresses",
		"order_returns", "order_exchanges", "order_claims",
	} {
		assert.Contains(t, seen, table,
			"the person has a row in %s and the dossier does not mention it", table)
	}

	orders := disclosureRecordsOf(disclosure, "orders")
	require.Len(t, orders, 1)
	assert.Equal(t, ord.ID, orders[0].ID)

	contact, present := disclosedField(orders[0], "email")
	require.True(t, present)
	assert.Equal(t, email, contact)

	addresses := disclosureRecordsOf(disclosure, "order_addresses")
	require.Len(t, addresses, 2, "the shipping and the billing address are separate records")
	street, present := disclosedField(addresses[0], "address_1")
	require.True(t, present)
	assert.Equal(t, "Ataturk Cad. 12", street)
	instructions, present := disclosedField(addresses[0], "metadata")
	require.True(t, present)
	assert.Equal(t, map[string]any{"delivery_note": "leave with the doorman"}, instructions,
		"the caller's own document is handed over whole; gobit never inspected it")

	returns := disclosureRecordsOf(disclosure, "order_returns")
	require.Len(t, returns, 1)
	note, present := disclosedField(returns[0], "note")
	require.True(t, present)
	assert.Equal(t, "she said she would come by the shop on Friday", note)

	// The Kind rides on the value. An Open field is one gobit has never looked
	// inside, and the controller reviewing this dossier has to know which ones
	// those are.
	for _, field := range returns[0].Fields {
		assert.Equal(t, personaldata.Open, field.Kind,
			"%s on a return is text somebody typed and gobit does not read it", field.Column)
	}
}

// TestDisclosureShowsAnAlreadyErasedOrderWithItsEmptiedFields is the decision
// service/disclosure.go argues, checked against the database that was really
// rewritten.
//
// An anonymized order still has its rows. Reporting nothing about them would be
// false, and it would take from the person the only way she has to SEE that the
// erasure happened: a field that is present and empty says the column was
// searched, while an absent one is indistinguishable from a column nobody read.
func TestDisclosureShowsAnAlreadyErasedOrderWithItsEmptiedFields(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_DISCLOSE_ERASED"
		email      = "erased.person@example.com"
	)

	ord := placeOrderFor(t, svc, customerID, email)
	settleOrder(t, svc, ord)

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)
	require.Equal(t, personaldata.Anonymized, result.Outcome)

	// The customer id survives the erasure by decision, so the person is still
	// reachable. A guest whose only handle was the e-mail is not, and the
	// Nothing sentence is where that is said.
	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	orders := disclosureRecordsOf(disclosure, "orders")
	require.Len(t, orders, 1)

	contact, present := disclosedField(orders[0], "email")
	require.True(t, present, "the column is named even though it is empty")
	assert.Nil(t, contact, "the erasure nulled it")

	metadata, present := disclosedField(orders[0], "metadata")
	require.True(t, present)
	assert.Equal(t, map[string]any{"channel": "web"}, metadata,
		"gobit never rewrites a free-form column, so an erased order is exactly where the "+
			"remaining free text lives")

	addresses := disclosureRecordsOf(disclosure, "order_addresses")
	require.Len(t, addresses, 2,
		"the address ROWS survive the erasure; dropping them would answer that this order "+
			"never had an address")

	for _, record := range addresses {
		name, present := disclosedField(record, "first_name")
		require.True(t, present)
		assert.Nil(t, name)

		country, present := disclosedField(record, "country_code")
		require.True(t, present)
		assert.Equal(t, "TR", country, "the one address column the erasure keeps")
	}
}

// TestDisclosureFindsAGuestOrderByEMailAlone is the subject shape the contract
// was built around.
//
// A guest order carries an e-mail with a NULL customer_id. A resolution that
// required both handles would find neither, and a person who checked out as a
// guest would be told the shop holds nothing about her while her address sat in
// the table.
func TestDisclosureFindsAGuestOrderByEMailAlone(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const email = "guest.disclosed@example.com"

	ord := placeOrderFor(t, svc, "", email)
	require.Empty(t, ord.CustomerID, "this is a guest order")

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{Email: email})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	orders := disclosureRecordsOf(disclosure, "orders")
	require.Len(t, orders, 1)
	assert.Equal(t, ord.ID, orders[0].ID)

	customer, present := disclosedField(orders[0], "customer_id")
	require.True(t, present)
	assert.Nil(t, customer, "a guest order has none, and the column is still named")
}

// TestDisclosureAnswersNothingForAPersonWhoNeverBought covers the state that is
// easiest to get wrong, because a wrong one looks exactly like a right one.
func TestDisclosureAnswersNothingForAPersonWhoNeverBought(t *testing.T) {
	svc, _ := newService(t)

	disclosure, err := svc.PersonalDataOf(context.Background(), personaldata.Subject{
		CustomerID: "cus_DISCLOSE_ABSENT",
		Email:      "absent.person@example.com",
	})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Nothing, disclosure.State)
	assert.Empty(t, disclosure.Records)
	require.NotEmpty(t, disclosure.Why,
		"'we found nothing' is an answer a person may dispute; the sentence that says where the "+
			"search went is what makes it checkable")
}

// TestDisclosureWritesNothing is the property the whole file rests on, asserted
// against the row rather than against the code.
//
// A dossier is built on an installation that is selling. If any of the six
// statements had grown a write — or a FOR UPDATE, which is one edit away in a
// file that sits beside queries/erasure.sql — a person's question would start
// blocking the shop's checkout. The stamp is what the database itself says about
// it.
func TestDisclosureWritesNothing(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_DISCLOSE_READONLY"
		email      = "readonly.person@example.com"
	)

	ord := placeOrderFor(t, svc, customerID, email)
	before := orderUpdatedAt(t, ord.ID)

	_, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	assert.True(t, before.Equal(orderUpdatedAt(t, ord.ID)),
		"the row was stamped by a read; something on this path is writing")

	// And the erasure still has all of its work to do: a disclosure that had
	// emptied a column on the way past would show up here as a shorter erasure.
	stamp := erasureStamp(t, ord.ID)
	assert.Nil(t, stamp, "nothing was erased by a read")

	contact := textColumn(t, "email", ord.ID)
	require.NotNil(t, contact)
	assert.Equal(t, email, *contact)
}

// TestDisclosureRefusesASubjectThatNamesNobody is the last defense behind the
// coordinator's own check.
//
// A zero-value subject reaching this module would match every order in the
// installation and hand the caller a copy of it. It is the mirror image of the
// erasure's refusal, with its own code: one accident destroys everybody's data
// and the other discloses it.
func TestDisclosureRefusesASubjectThatNamesNobody(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.PersonalDataOf(context.Background(), personaldata.Subject{})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "it is the caller's mistake, not a fault: %v", err)
	assert.Equal(t, service.CodeDisclosureSubjectEmpty, errors.CodeOf(err))
}

// TestTheModuleRefusesToDiscloseBeforeItIsWired verifies the one branch that
// must not answer quietly.
//
// A module whose service was never registered has read nothing, and answering
// "nothing found" from it would put a confident "you have no orders with us"
// into a document handed to the person. The sweep turns an error into a named
// incompleteness in the dossier instead (ADR 0034), which is the answer that can
// be checked.
func TestTheModuleRefusesToDiscloseBeforeItIsWired(t *testing.T) {
	_, err := order.New().PersonalDataOf(context.Background(),
		personaldata.Subject{CustomerID: "cus_X"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"a module that was not wired is a setup fault, not a bad request: %v", err)
}
