package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// CustomersForDisclosure is the in-memory twin of the real repository method.
//
// It resolves the subject through [memRepo.erasureTargets] — the same helper
// the anonymization fake uses — and that sharing is the point rather than a
// saving. The real pair are twins too: the disclosure resolves a person on
// exactly the erasure's terms and differs only in taking no lock and writing
// nothing. A fake that resolved them differently would let a test prove the
// erasure reaches every guest record and say nothing about whether the
// disclosure does.
//
// What it inherits from that helper is what matters here: SOFT DELETED rows are
// reached, an e-mail resolves to EVERY row under it, and a subject carrying both
// handles resolves to the union of the two, de-duplicated.
func (m *memRepo) CustomersForDisclosure(
	_ context.Context,
	customerID, email string,
) ([]models.Customer, error) {
	m.record("CustomersForDisclosure")

	ids := m.erasureTargets(customerID, email)

	out := make([]models.Customer, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.customers[id])
	}

	return out, nil
}

// AddressesForDisclosure is the in-memory twin of the address read.
//
// It reaches soft-deleted addresses, for the reason the SQL does: a deleted
// address row still holds the street and the phone. The order mirrors the
// query's — by customer, then newest first — because a dossier assembled twice
// for the same person has to read the same way twice, and a fake that returned
// map order would let a test pass on Tuesday and fail on Wednesday.
func (m *memRepo) AddressesForDisclosure(
	_ context.Context,
	customerIDs []string,
) ([]models.CustomerAddress, error) {
	m.record("AddressesForDisclosure")

	wanted := map[string]bool{}
	for _, id := range customerIDs {
		wanted[id] = true
	}

	out := make([]models.CustomerAddress, 0, len(m.addresses))
	for id := range m.addresses {
		if wanted[m.addresses[id].CustomerID] {
			out = append(out, m.addresses[id])
		}
	}

	slices.SortFunc(out, func(a, b models.CustomerAddress) int {
		if a.CustomerID != b.CustomerID {
			return strings.Compare(a.CustomerID, b.CustomerID)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return b.CreatedAt.Compare(a.CreatedAt)
		}

		return strings.Compare(b.ID, a.ID)
	})

	return out, nil
}

// newDisclosureService builds a service over the in-memory repository.
func newDisclosureService(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()

	return New(repo, Options{Now: func() time.Time { return erasureClock }}), repo
}

// columnsOf lists the columns of a record, in the order the record carries
// them.
func columnsOf(record personaldata.Record) []string {
	out := make([]string, 0, len(record.Fields))
	for _, field := range record.Fields {
		out = append(out, field.Column)
	}

	return out
}

// declaredColumnsOf lists the declared columns of a table, in declaration
// order.
func declaredColumnsOf(table string) []string {
	holdings := holdingsOf(table)

	out := make([]string, 0, len(holdings))
	for _, holding := range holdings {
		out = append(out, holding.Column)
	}

	return out
}

// valueOf returns the disclosed value of one column.
func valueOf(t *testing.T, record personaldata.Record, column string) any {
	t.Helper()

	for _, field := range record.Fields {
		if field.Column == column {
			return field.Value
		}
	}

	t.Fatalf("%s.%s is not in the record", record.Table, column)

	return nil
}

// TestEveryDeclaredColumnHasAValue is the test that closes the one silent
// failure the derivation leaves open.
//
// [recordOf] builds a record's fields from the DECLARATION and reads each value
// out of a map keyed by column. A declared column the map has no entry for
// therefore yields a nil value rather than an error — the person's file would
// carry the column with nothing in it, and would look exactly like a column
// that is genuinely empty. This test holds the two key sets equal in both
// directions, so a column added to the declaration fails here until somebody
// gives it a source, and a value the row can produce and the declaration does
// not name is caught before it can be handed over.
//
// It needs no database: both halves are properties of the code.
func TestEveryDeclaredColumnHasAValue(t *testing.T) {
	sources := map[string]map[string]any{
		TableCustomer: customerValues(models.Customer{}),
		TableAddress:  addressValues(models.CustomerAddress{}),
	}

	for table, values := range sources {
		declared := declaredColumnsOf(table)
		require.NotEmpty(t, declared, "%s declares nothing; the derivation has gone blind", table)

		for _, column := range declared {
			_, ok := values[column]
			assert.True(t, ok,
				"%s.%s is declared and no row value is mapped to it, so the disclosure would "+
					"hand it over empty and a reader could not tell that from a column that "+
					"really is empty", table, column)
		}

		for column := range values {
			assert.Contains(t, declared, column,
				"%s.%s has a row value and is not declared; a disclosure may not reach past "+
					"the declaration, which told the controller this column was not there",
				table, column)
		}
	}
}

// TestDisclosureRefusesASubjectThatNamesNobody proves that an empty subject is
// a refusal rather than everybody's file.
//
// It matters more here than on the erasure side. An erasure of "everyone" is a
// catastrophe somebody would notice; a DISCLOSURE of everyone is a quiet one —
// a single request that returns the whole customer table to whoever asked, and
// nothing in the answer says it was not one person's data.
func TestDisclosureRefusesASubjectThatNamesNobody(t *testing.T) {
	ctx := context.Background()
	svc, repo := newDisclosureService(t)

	_, err := svc.PersonalDataOf(ctx, personaldata.Subject{})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, CodeDisclosureSubjectEmpty, errors.CodeOf(err))
	assert.Zero(t, repo.calls["CustomersForDisclosure"], "the storage must not be asked at all")
}

// TestDisclosureListsExactlyTheDeclaredColumns is the central claim of the
// whole capability.
//
// A disclosure that reached PAST the declaration would hand over data the
// declaration told the controller was not there; one that stopped SHORT of it
// would contradict the same document. So the columns are compared against the
// declaration itself — not against a list written in this test, which would
// drift from the declaration the same way a second list in the code would.
func TestDisclosureListsExactlyTheDeclaredColumns(t *testing.T) {
	ctx := context.Background()
	svc, _ := newDisclosureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	address, err := svc.CreateAddress(ctx, created.ID, personalAddress())
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	assert.Equal(t, ErasureHolder, disclosure.Holder)
	assert.Equal(t, personaldata.Disclosed, disclosure.State)
	require.Len(t, disclosure.Records, 2,
		"the customer's own row and their address are two records; merging them would "+
			"lose which table a value came from")

	customerRecord, addressRecord := disclosure.Records[0], disclosure.Records[1]

	assert.Equal(t, TableCustomer, customerRecord.Table)
	assert.Equal(t, created.ID, customerRecord.ID)
	assert.Equal(t, declaredColumnsOf(TableCustomer), columnsOf(customerRecord),
		"the fields are the declared columns of the table, in the declared order")

	assert.Equal(t, TableAddress, addressRecord.Table)
	assert.Equal(t, address.ID, addressRecord.ID,
		"the address record is identified by the ADDRESS id; naming it by the customer "+
			"would make two addresses indistinguishable in the file")
	assert.Equal(t, declaredColumnsOf(TableAddress), columnsOf(addressRecord))

	// The values are the person's own, not a shape with the right column names.
	assert.Equal(t, created.Email, valueOf(t, customerRecord, "email"))
	assert.Equal(t, "Ayse", valueOf(t, customerRecord, "first_name"))
	assert.Equal(t, "Kaya", valueOf(t, customerRecord, "last_name"))
	assert.Equal(t, "+90 555 000 0000", valueOf(t, customerRecord, "phone"))
	assert.Equal(t, map[string]any{"loyalty_tier": "gold"}, valueOf(t, customerRecord, "metadata"),
		"the free-form column is handed over as it is stored: gobit never inspected it, "+
			"and the controller reviewing the file is the one who can")
	assert.Equal(t, "Bagdat Cad. 12", valueOf(t, addressRecord, "address_1"))
	assert.Equal(t, "TR", valueOf(t, addressRecord, "country_code"),
		"the one column the erasure refuses to overwrite is still DISCLOSED; what may "+
			"be destroyed and what may be shown are different questions")
}

// TestEveryDisclosedFieldCarriesTheDeclaredKind proves the Kind cannot drift
// from the declaration, because it is not copied from anywhere else.
//
// The distinction it carries is the one a reader of a dossier needs most: a
// Named value is one gobit wrote and can vouch for, an Open one is the
// embedder's and was never inspected. A file that called the metadata blob
// Named would be telling a controller that the framework knows what is in it.
func TestEveryDisclosedFieldCarriesTheDeclaredKind(t *testing.T) {
	ctx := context.Background()
	svc, _ := newDisclosureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, created.ID, personalAddress())
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	declared := map[string]personaldata.Kind{}
	for _, holding := range PersonalDataHoldings() {
		declared[holding.Table+"."+holding.Column] = holding.Kind
	}

	var open int
	for _, record := range disclosure.Records {
		for _, field := range record.Fields {
			key := record.Table + "." + field.Column
			assert.Equal(t, declared[key], field.Kind, "%s: the kind is not the declared one", key)

			if field.Kind == personaldata.Open {
				open++
			}
		}
	}

	assert.Equal(t, 1, open,
		"exactly one disclosed column is Open — customer.metadata. If this count moved, "+
			"either a free-form column stopped being marked as one or a new one arrived "+
			"unnoticed, and both change what the reader is being told they can trust")
}

// TestDisclosureOfSomebodyThisModuleNeverSawSaysNothing proves the module tells
// the two kinds of empty apart.
//
// "Searched and not here" is a true and useful answer: the sweep asks every
// holder about the same person and most holders have never seen them. Answering
// Disclosed with an empty list would say the same words as a holder that could
// not look at all, and the state exists precisely so those two cannot be
// confused.
func TestDisclosureOfSomebodyThisModuleNeverSawSaysNothing(t *testing.T) {
	ctx := context.Background()
	svc, repo := newDisclosureService(t)

	disclosure, err := svc.PersonalDataOf(ctx,
		personaldata.Subject{CustomerID: "cust_nobody", Email: "nobody@example.com"})
	require.NoError(t, err, "an absence is a normal answer to a sweep, not a failure")

	assert.Equal(t, personaldata.Nothing, disclosure.State)
	assert.Empty(t, disclosure.Records)
	assert.NotEmpty(t, disclosure.Why,
		"'we found nothing' is an answer a person may query, and the sentence saying "+
			"WHERE it was looked for is the only checkable part of it")
	assert.Contains(t, disclosure.Why, "soft-deleted",
		"the sentence has to say that the search went past the deleted rows, since that "+
			"is the part a reader would otherwise assume was skipped")
	assert.Zero(t, repo.calls["AddressesForDisclosure"],
		"an address hangs off a customer row; with no customer row there is nothing to ask for")
}

// TestDisclosureUsesBothHandlesOfOneSubject proves the subject is resolved the
// way the erasure resolves it.
//
// A registered customer who has also checked out as a guest under the same
// address is one person with several rows. Preferring the id would leave the
// guest records out of her own file — and she would have no way to know, because
// the file would look complete.
func TestDisclosureUsesBothHandlesOfOneSubject(t *testing.T) {
	ctx := context.Background()
	svc, _ := newDisclosureService(t)

	account, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)

	guest, err := svc.RegisterGuest(ctx, personalCustomer())
	require.NoError(t, err)

	other, err := svc.CreateCustomer(ctx, CustomerInput{Email: "someone.else@example.com"})
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx,
		personaldata.Subject{CustomerID: account.ID, Email: personalCustomer().Email})
	require.NoError(t, err)

	ids := map[string]int{}
	for _, record := range disclosure.Records {
		ids[record.ID]++
	}

	assert.Equal(t, 1, ids[account.ID], "the account is in the file exactly once")
	assert.Equal(t, 1, ids[guest.ID],
		"the guest checkout made under the same address is the same person")
	assert.Zero(t, ids[other.ID], "somebody else's record is not in this person's file")
}

// TestDisclosureReachesADeletedRecord proves the read goes where the erasure
// goes.
//
// A soft delete writes deleted_at and clears not one personal column, so the
// person's name and phone are still in the table. A disclosure that filtered
// them would tell somebody "this is everything we hold about you" while their
// data sat in a row the listing screens no longer show — the same falsehood the
// erasure avoids by reaching the row and overwriting it.
func TestDisclosureReachesADeletedRecord(t *testing.T) {
	ctx := context.Background()
	svc, _ := newDisclosureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, created.ID, personalAddress())
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCustomer(ctx, created.ID))

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	require.Len(t, disclosure.Records, 2, "the deleted customer AND their deleted address are held")
	assert.Equal(t, "Ayse", valueOf(t, disclosure.Records[0], "first_name"))
	assert.Equal(t, "Bagdat Cad. 12", valueOf(t, disclosure.Records[1], "address_1"))
}

// TestDisclosureShowsAnAlreadyAnonymizedRecord proves the file says what is
// there rather than what somebody would prefer to be there.
//
// After an erasure the row still EXISTS, carrying placeholders and an
// unroutable address. Hiding it would tell a person who asked to be forgotten
// that their record is gone; showing it says the truthful thing — the row is
// still here, and this is all that is left in it.
func TestDisclosureShowsAnAlreadyAnonymizedRecord(t *testing.T) {
	ctx := context.Background()
	svc, _ := newDisclosureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)

	_, err = svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	require.Len(t, disclosure.Records, 1)
	assert.Equal(t, models.AnonymousEmail(created.ID), valueOf(t, disclosure.Records[0], "email"))
	assert.Empty(t, valueOf(t, disclosure.Records[0], "first_name"))
	assert.Equal(t, map[string]any{"loyalty_tier": "gold"},
		valueOf(t, disclosure.Records[0], "metadata"),
		"the free-form column the erasure refused to touch is still in the file, which is "+
			"the same fact Result.Kept reports on the other side")
}

// TestDisclosureLeavesTheSegmentOutAndSaysSo pins the one judgement in this
// module's disclosure that is not forced by the declaration.
//
// customer_group.metadata is DECLARED and produces no record for anybody: it is
// written about a segment and shared by everyone in it, so handing it to whoever
// asks first would disclose to this person whatever the shop wrote about the
// others. The membership row is left out on a stricter rule — no column of it is
// declared at all. Neither absence may be silent, because a declared place that
// yields nothing reads as "we hold nothing of yours there".
func TestDisclosureLeavesTheSegmentOutAndSaysSo(t *testing.T) {
	ctx := context.Background()
	svc, _ := newDisclosureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)

	group, err := svc.CreateGroup(ctx, GroupInput{
		Name:     "wholesalers",
		Metadata: map[string]any{"negotiated_by": "Mehmet"},
	})
	require.NoError(t, err)
	require.NoError(t, svc.AddToGroup(ctx, created.ID, group.ID))

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	for _, record := range disclosure.Records {
		assert.NotEqual(t, TableGroup, record.Table,
			"a segment's metadata is not this person's data, and it may name the OTHER "+
				"people in the segment")
		assert.NotEqual(t, "customer_group_customer", record.Table,
			"no column of the membership table is declared; disclosing from it would "+
				"contradict the declaration in the direction nobody watches")
	}

	assert.Contains(t, disclosure.Why, "metadata",
		"a declared place that produced no record has to be named, or its silence reads "+
			"as an absence of data rather than a decision")
	assert.Contains(t, disclosure.Why, "membership")
}

// TestDisclosureWritesNothing proves the claim the godoc makes about side
// effects, in the only way a unit test can: by counting what the storage was
// asked to do.
//
// The claim is not decoration. This method resolves a person exactly the way the
// erasure does, and the erasure's resolver locks rows inside a transaction. A
// disclosure that reused it would take write locks on a customer's row every
// time somebody read a report, and the customer editing their own name in the
// storefront would wait behind it.
func TestDisclosureWritesNothing(t *testing.T) {
	ctx := context.Background()
	svc, repo := newDisclosureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, created.ID, personalAddress())
	require.NoError(t, err)

	// Everything the fixture did is forgotten, so what is left is what the
	// disclosure itself asked for.
	repo.calls = map[string]int{}

	_, err = svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	called := make([]string, 0, len(repo.calls))
	for name := range repo.calls {
		called = append(called, name)
	}
	slices.Sort(called)

	assert.Equal(t, []string{"AddressesForDisclosure", "CustomersForDisclosure"}, called,
		"a disclosure reads two things and does nothing else; any other method here is "+
			"either a write or a second resolution of the same person")
}

// TestDisclosureIsRefusedByAnUnconfiguredService proves that a module whose
// Register never ran fails loudly.
//
// The silent alternative is the dangerous one, and more so than on the erasure
// side: an unconfigured module answering "nothing here" would be entered in a
// person's dossier as a holder that searched and found her absent, which is a
// statement nobody made.
func TestDisclosureIsRefusedByAnUnconfiguredService(t *testing.T) {
	var svc *Service

	_, err := svc.PersonalDataOf(context.Background(),
		personaldata.Subject{CustomerID: "cust_1"})
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
