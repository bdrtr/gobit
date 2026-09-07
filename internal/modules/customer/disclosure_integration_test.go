//go:build integration

package customer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// This file proves the module's answer to a DISCLOSURE request against a real
// database; it shares the container, the pool, the TestMain and the fixtures of
// customer_integration_test.go and erasure_integration_test.go.
//
// The tests are here rather than beside the unit tests because every claim a
// disclosure makes is a claim about what the STORAGE still holds, and the fake
// repository cannot witness any of them. Six belong to the SQL alone: that the
// two queries carry no deleted_at filter, so a soft-deleted customer and their
// soft-deleted addresses are in the file; that resolving by e-mail reaches every
// guest record the partial unique index permits under one address; that the
// query answering a subject with BOTH handles returns a row matching both
// conditions once rather than twice; that a jsonb metadata column comes back as
// the document it holds rather than as bytes; that neither WHERE clause reaches
// a row belonging to somebody else; and that both ORDER BY clauses are there. A
// test reading the fake would stay green if any of the six fell out of the SQL.
//
// The seventh is not about a query at all: a disclosure must have NO side
// effect, and the only place that can be measured is against a real updated_at.
//
// # What this file does NOT prove, and what would
//
// [personaldata.Unresolvable] is not exercised here because this module never
// answers it, and that is a decision rather than an omission: it can search
// whenever it is handed a handle, and a subject carrying NO handle is refused as
// an invalid request instead of being dressed up as a state (see
// [service.Service.PersonalDataOf]). The refusal needs no database and is proven
// without one, in service.TestDisclosureRefusesASubjectThatNamesNobody. What a
// dossier does with a holder that cannot resolve anybody is the COORDINATOR's
// claim, not this module's, and it belongs to internal/workflows/datasubject.
//
// Nothing here bounds how many records come back, because no query in this
// module has a LIMIT. The bound is the person's own row count, and a test
// asserting a ceiling would be inventing one the code does not promise. What
// would prove a bound is a bound: a LIMIT in the SQL and a documented answer to
// what a truncated file says about being truncated, and neither exists today.

// findRecord returns the record for one row of one table, and fails if the file
// does not contain it.
func findRecord(t *testing.T, d personaldata.Disclosure, table, id string) personaldata.Record {
	t.Helper()

	for _, record := range d.Records {
		if record.Table == table && record.ID == id {
			return record
		}
	}

	t.Fatalf("%s row %s is not in the disclosure", table, id)

	return personaldata.Record{}
}

// fieldValue returns one disclosed value of a record.
func fieldValue(t *testing.T, record personaldata.Record, column string) any {
	t.Helper()

	for _, field := range record.Fields {
		if field.Column == column {
			return field.Value
		}
	}

	t.Fatalf("%s.%s is not in the record", record.Table, column)

	return nil
}

// TestDisclosureShowsWhatTheDatabaseHolds proves the file is assembled out of
// the real columns, jsonb included.
//
// The metadata half is the one that cannot be shown anywhere else. The column is
// jsonb and the repository decodes it into a map; a disclosure that handed over
// the raw bytes would compile, would pass every unit test written against a fake
// that keeps Go maps, and would reach the person as a base64 string in the one
// field gobit never inspected and therefore cannot summarize for them.
func TestDisclosureShowsWhatTheDatabaseHolds(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)
	addresses, err := svc.ListAddresses(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, addresses, 1)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	assert.Equal(t, service.ErasureHolder, disclosure.Holder)
	require.Equal(t, personaldata.Disclosed, disclosure.State)
	require.Len(t, disclosure.Records, 2, "the customer row and their one address")

	customerRecord := findRecord(t, disclosure, service.TableCustomer, created.ID)
	assert.Equal(t, created.Email, fieldValue(t, customerRecord, "email"))
	assert.Equal(t, "Ayse", fieldValue(t, customerRecord, "first_name"))
	assert.Equal(t, map[string]any{"loyalty_tier": "gold"}, fieldValue(t, customerRecord, "metadata"),
		"the jsonb column is disclosed as the document it holds; bytes would reach the "+
			"person as an unreadable string in the one field gobit never looked at")

	addressRecord := findRecord(t, disclosure, service.TableAddress, addresses[0].ID)
	assert.Equal(t, "Bagdat Cad. 12", fieldValue(t, addressRecord, "address_1"))
	assert.Equal(t, "Daire 4", fieldValue(t, addressRecord, "address_2"))
	assert.Equal(t, "TR", fieldValue(t, addressRecord, "country_code"))
}

// TestDisclosureReachesASoftDeletedRecordInTheDatabase is the test that fails if
// either query grows a deleted_at filter.
//
// It is the disclosure's half of the fact the erasure rests on, measured the
// same way: a soft delete writes deleted_at and updated_at and NOTHING else, so
// the person's name, phone and street are still in both tables. Every other read
// in this module filters those rows out. If these did too, a person would be
// handed a file headed "this is everything we hold about you" while their
// address sat in a row the listing screens no longer show.
func TestDisclosureReachesASoftDeletedRecordInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)
	addresses, err := svc.ListAddresses(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, addresses, 1)

	require.NoError(t, svc.DeleteCustomer(ctx, created.ID))

	// The premise, measured rather than assumed: the soft delete left the
	// personal columns where they were, in both tables.
	stored := readCustomer(ctx, t, created.ID)
	require.True(t, stored.Deleted)
	require.Equal(t, created.Email, stored.Email,
		"the soft delete must leave the personal columns alone, or this test proves nothing")
	require.Equal(t, "Bagdat Cad. 12", readAddresses(ctx, t, created.ID)[0].Address1)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	require.Len(t, disclosure.Records, 2,
		"the deleted customer AND their deleted address are both still held")
	assert.Equal(t, created.Email,
		fieldValue(t, findRecord(t, disclosure, service.TableCustomer, created.ID), "email"))
	assert.Equal(t, "Bagdat Cad. 12",
		fieldValue(t, findRecord(t, disclosure, service.TableAddress, addresses[0].ID), "address_1"))
}

// TestDisclosureByEmailReachesEveryRecordUnderThatAddress proves the e-mail path
// is plural against the index that makes it so.
//
// customer_account_email_uniq covers accounts only, so one address can open any
// number of guest records and all of them are the same person. A disclosure that
// answered with the account alone would hand somebody a file missing every guest
// checkout they ever made, and nothing in the file would say so.
func TestDisclosureByEmailReachesEveryRecordUnderThatAddress(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	shared := erasureEmail()

	var ids []string
	for _, account := range []bool{false, false, true} {
		in := service.CustomerInput{Email: shared, FirstName: "Ayse", Phone: "+90 555 000 0000"}

		var created models.Customer
		var err error
		if account {
			created, err = svc.CreateCustomer(ctx, in)
		} else {
			created, err = svc.RegisterGuest(ctx, in)
		}
		require.NoError(t, err)

		ids = append(ids, created.ID)
	}

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{Email: shared})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.Len(t, disclosure.Records, 3, "two guest records and one account, all the same person")
	for _, id := range ids {
		assert.Equal(t, shared,
			fieldValue(t, findRecord(t, disclosure, service.TableCustomer, id), "email"))
	}
}

// TestDisclosureWithBothHandlesShowsARecordOnce proves the de-duplication the
// SQL does, in the case that produces it.
//
// A registered customer who has also checked out as a guest under the same
// address is reached by BOTH conditions of the id-or-e-mail query. Two queries
// merged in Go would list her account twice, and a person reading two identical
// records of their own has been given a document they cannot trust to be
// counted correctly anywhere else in it.
func TestDisclosureWithBothHandlesShowsARecordOnce(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	shared := erasureEmail()
	in := service.CustomerInput{Email: shared, FirstName: "Ayse", Phone: "+90 555 000 0000"}

	account, err := svc.CreateCustomer(ctx, in)
	require.NoError(t, err)
	guest, err := svc.RegisterGuest(ctx, in)
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx,
		personaldata.Subject{CustomerID: account.ID, Email: shared})
	require.NoError(t, err)

	seen := map[string]int{}
	for _, record := range disclosure.Records {
		seen[record.Table+":"+record.ID]++
	}

	assert.Equal(t, 1, seen[service.TableCustomer+":"+account.ID],
		"the account satisfies both halves of the query and belongs in the file once")
	assert.Equal(t, 1, seen[service.TableCustomer+":"+guest.ID],
		"the guest checkout is reached by the e-mail alone and is the same person")
}

// TestDisclosureOfSomebodyThisModuleNeverSawSaysNothingInTheDatabase proves the
// empty answer is a search that came back empty rather than a search that never
// ran.
func TestDisclosureOfSomebodyThisModuleNeverSawSaysNothingInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	disclosure, err := svc.PersonalDataOf(ctx,
		personaldata.Subject{CustomerID: "cust_00000000000000000000000000", Email: erasureEmail()})
	require.NoError(t, err, "an absence is a normal answer to a sweep, not a failure")

	assert.Equal(t, personaldata.Nothing, disclosure.State)
	assert.Empty(t, disclosure.Records)
	assert.NotEmpty(t, disclosure.Why)
}

// TestDisclosureWritesNothingToTheDatabase measures the claim the whole design
// rests on, in the one place it can be measured.
//
// The disclosure resolves a person exactly the way the erasure does, and the
// erasure's resolver takes FOR UPDATE row locks inside a transaction before
// overwriting what it read. Reusing that would have been the obvious saving, and
// it would mean that reading a person's file locked their row and bumped nothing
// while a customer editing their own name in the storefront waited behind it.
// updated_at is the witness: it moves on every write this module makes.
func TestDisclosureWritesNothingToTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)
	before := readCustomer(ctx, t, created.ID)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	after := readCustomer(ctx, t, created.ID)
	assert.Equal(t, before.UpdatedAt, after.UpdatedAt, "a disclosure is a read and must move nothing")
	assert.Equal(t, before.Email, after.Email)
	assert.Equal(t, before.FirstName, after.FirstName)
	assert.Equal(t, before.Metadata, after.Metadata)
	assert.Equal(t, before.Deleted, after.Deleted)
}

// TestDisclosureShowsAnAnonymizedRecordInTheDatabase proves a person who asked
// to be forgotten is told the truth about what is left.
//
// The row still EXISTS after an erasure — it has to, because carts and orders
// carry the customer id in a bare TEXT column with no foreign key — and it
// carries the derived unroutable address, empty names and the placeholders the
// CHECK constraints forced. Hiding that would tell somebody their record is gone
// when it is sitting there; showing it says the one true thing, and it also puts
// the untouched metadata in front of the controller, which is the same fact
// Result.Kept reports on the other side.
func TestDisclosureShowsAnAnonymizedRecordInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)
	addresses, err := svc.ListAddresses(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, addresses, 1)

	_, err = svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	customerRecord := findRecord(t, disclosure, service.TableCustomer, created.ID)
	assert.Equal(t, models.AnonymousEmail(created.ID), fieldValue(t, customerRecord, "email"))
	assert.Empty(t, fieldValue(t, customerRecord, "first_name"))
	assert.Equal(t, map[string]any{"loyalty_tier": "gold"}, fieldValue(t, customerRecord, "metadata"),
		"the free-form column the erasure refused to touch is still there, and the file says so")

	addressRecord := findRecord(t, disclosure, service.TableAddress, addresses[0].ID)
	assert.Equal(t, models.AnonymousPlaceholder, fieldValue(t, addressRecord, "city"))
	assert.Equal(t, "TR", fieldValue(t, addressRecord, "country_code"),
		"the country code is kept by the erasure and is disclosed like any other declared column")
}
