package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// AnonymizeCustomers is the in-memory twin of the real repository method.
//
// It lives in this file rather than beside the other fakes because it has to be
// read next to the test that trusts it. The fake imitates FIVE properties of
// the real one, and each was chosen because a test here would otherwise pass
// for the wrong reason:
//
//   - It reaches SOFT DELETED rows. Every other read in this fake filters
//     DeletedAt, exactly as the SQL does; the erasure path deliberately does
//     not, and a fake that filtered would make TestErasureReachesADeletedRecord
//     green while the real query walked past the row.
//   - Resolving by e-mail returns EVERY matching row, guests included.
//   - A subject carrying BOTH handles resolves to the union of both, because
//     the id and the e-mail reach different rows of the same person.
//   - A row is written only when a column would actually change, which is what
//     makes a second call report zero — and the comparison is against every
//     column the erasure writes, not against the e-mail alone. That mirrors the
//     UPDATE's WHERE clause; a fake that read the e-mail as a flag would stay
//     green on a row whose name was written back after an erasure.
//   - Addresses are rewritten on the same terms, because an address can be
//     added after an erasure.
//
// What it cannot imitate is the transaction and the row locks. Those are
// UNPROVED: nothing in this repository exercises two concurrent erasures of one
// subject or a failure between the two UPDATEs. Proving them needs a real
// database — two Erase calls on the same subject from two connections, showing
// the second waits rather than reading a half-written row, and an erasure
// aborted between the customer write and the address write, showing the address
// still holds the street. Until such a test exists, read the atomicity claim in
// repository/erasure.go as an argument about the code, not as a measurement.
func (m *memRepo) AnonymizeCustomers(
	_ context.Context,
	customerID, email string,
	now time.Time,
) (models.ErasureCount, error) {
	m.record("AnonymizeCustomers")

	var count models.ErasureCount
	for _, id := range m.erasureTargets(customerID, email) {
		count.Matched++
		customer := m.customers[id]

		if customer.Email != models.AnonymousEmail(id) ||
			customer.FirstName != "" || customer.LastName != "" || customer.Phone != "" {
			customer.Email = models.AnonymousEmail(id)
			customer.FirstName = ""
			customer.LastName = ""
			customer.Phone = ""
			customer.UpdatedAt = now
			m.customers[id] = customer
			count.Rewritten++
		}

		for addressID := range m.addresses {
			address := m.addresses[addressID]
			if address.CustomerID != id {
				continue
			}
			anonymous := models.CustomerAddress{
				ID:                address.ID,
				CustomerID:        address.CustomerID,
				Address1:          models.AnonymousPlaceholder,
				City:              models.AnonymousPlaceholder,
				CountryCode:       address.CountryCode,
				IsDefaultShipping: address.IsDefaultShipping,
				IsDefaultBilling:  address.IsDefaultBilling,
				CreatedAt:         address.CreatedAt,
				UpdatedAt:         address.UpdatedAt,
				DeletedAt:         address.DeletedAt,
			}
			if anonymous == address {
				continue
			}
			anonymous.UpdatedAt = now
			m.addresses[addressID] = anonymous
			count.Rewritten++
		}
	}
	return count, nil
}

// erasureTargets resolves an erasure subject to customer ids, soft-deleted rows
// included; the ids come back in a stable order so a count can be asserted.
//
// Both handles select, and a subject carrying both selects the union of what
// each reaches — deduplicated, since the row named by the id is usually also
// one of the rows under the address.
func (m *memRepo) erasureTargets(customerID, email string) []string {
	seen := map[string]bool{}
	if customerID != "" {
		if _, ok := m.customers[customerID]; ok {
			seen[customerID] = true
		}
	}
	if email != "" {
		for id := range m.customers {
			if m.customers[id].Email == email {
				seen[id] = true
			}
		}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// erasureClock is the deterministic time source of the tests in this file. It
// is AFTER the fixed clock the other tests use, so an updated_at that moved can
// be told from one that did not.
var erasureClock = time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

// newErasureService builds a service over the in-memory repository.
func newErasureService(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	return New(repo, Options{Now: func() time.Time { return erasureClock }}), repo
}

// personalCustomer is a customer whose every named column holds something, so a
// column left untouched by the erasure shows up as a difference rather than as
// two empty strings that happen to match.
func personalCustomer() CustomerInput {
	return CustomerInput{
		Email:     "ayse@example.com",
		FirstName: "Ayse",
		LastName:  "Kaya",
		Phone:     "+90 555 000 0000",
		Metadata:  map[string]any{"loyalty_tier": "gold"},
	}
}

// personalAddress is the address counterpart of [personalCustomer].
func personalAddress() AddressInput {
	return AddressInput{
		FirstName:   "Ayse",
		LastName:    "Kaya",
		Company:     "Kaya Ltd",
		Address1:    "Bagdat Cad. 12",
		Address2:    "Daire 4",
		City:        "Istanbul",
		CountryCode: "tr",
		PostalCode:  "34000",
		Phone:       "+90 555 000 0001",
	}
}

// TestErasureRefusesASubjectThatNamesNobody proves that an empty subject is a
// refusal and not an erasure of everybody.
//
// The check is worth a test of its own because the failure is silent in the
// other direction: a subject with no identifier would match every row of a
// query written with two optional conditions, and the sweep would report having
// anonymized the whole customer table.
func TestErasureRefusesASubjectThatNamesNobody(t *testing.T) {
	ctx := context.Background()
	svc, repo := newErasureService(t)

	_, err := svc.Erase(ctx, erasure.Subject{})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, CodeErasureSubjectEmpty, errors.CodeOf(err))
	assert.Zero(t, repo.calls["AnonymizeCustomers"], "the storage must not be asked at all")
}

// TestErasureAnonymizesEveryNamedColumn proves the outcome and the columns
// behind it in one pass: the answer is Anonymized, the named columns are gone,
// the free-form column is untouched and the record still exists.
func TestErasureAnonymizesEveryNamedColumn(t *testing.T) {
	ctx := context.Background()
	svc, repo := newErasureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	address, err := svc.CreateAddress(ctx, created.ID, personalAddress())
	require.NoError(t, err)

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, ErasureHolder, result.Holder)
	assert.Equal(t, 2, result.Rows, "one customer row and one address row were written")

	stored := repo.customers[created.ID]
	require.NotZero(t, stored.ID, "the row must still exist; carts and orders point at this id")
	assert.Equal(t, models.AnonymousEmail(created.ID), stored.Email)
	assert.Empty(t, stored.FirstName)
	assert.Empty(t, stored.LastName)
	assert.Empty(t, stored.Phone)
	assert.Nil(t, stored.DeletedAt, "an erasure is not a delete")
	assert.Equal(t, map[string]any{"loyalty_tier": "gold"}, stored.Metadata,
		"the free-form column is the controller's judgement and gobit does not rewrite it")

	storedAddress := repo.addresses[address.ID]
	assert.Equal(t, models.AnonymousPlaceholder, storedAddress.Address1,
		"address_1 is NOT NULL and CHECKed non-empty, so it takes a placeholder")
	assert.Equal(t, models.AnonymousPlaceholder, storedAddress.City)
	assert.Empty(t, storedAddress.FirstName)
	assert.Empty(t, storedAddress.LastName)
	assert.Empty(t, storedAddress.Company)
	assert.Empty(t, storedAddress.Address2)
	assert.Empty(t, storedAddress.PostalCode)
	assert.Empty(t, storedAddress.Phone)
	assert.Equal(t, "TR", storedAddress.CountryCode,
		"the country is a jurisdiction rather than a person and is kept on purpose")
}

// TestErasureAlwaysReportsWhatItKept proves that the two columns this module
// refuses to touch are named in every answer, including the answer given about
// somebody who was never here.
//
// The word "anonymized" is only worth what the Kept list says: a report that
// stayed silent about the jsonb beside the erased columns would be covering a
// field nobody looked at.
func TestErasureAlwaysReportsWhatItKept(t *testing.T) {
	ctx := context.Background()
	svc, _ := newErasureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)

	erased, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	stranger, err := svc.Erase(ctx, erasure.Subject{CustomerID: "cust_NOBODY"})
	require.NoError(t, err)

	for name, result := range map[string]erasure.Result{"erased": erased, "stranger": stranger} {
		assert.Equal(t,
			[]string{"customer.metadata", "customer_group.metadata", "customer_address.country_code"},
			result.Kept, "%s: the refusals belong to the module, not to one person's data", name)
		assert.NotEmpty(t, result.Why, "%s: a Kept list nobody can explain is not an answer", name)
	}

	// The report is handed to whoever answers the data subject; if the slice
	// were shared, one caller sorting it would edit this module's declaration.
	erased.Kept[0] = "edited by the caller"
	again, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, "customer.metadata", again.Kept[0])
}

// TestErasureIsIdempotent proves the contract's hardest requirement: the second
// call answers Anonymized rather than an error or a silent "nothing found".
//
// The recognition costs no schema change, and that is the point of deriving the
// anonymous address from the id — the row itself says it has been here before.
func TestErasureIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, repo := newErasureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, created.ID, personalAddress())
	require.NoError(t, err)

	first, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	require.Equal(t, erasure.Anonymized, first.Outcome)
	firstState := repo.customers[created.ID]

	second, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, second.Outcome, "the outcome is the answer and it does not change")
	assert.Equal(t, 0, second.Rows, "nothing was written, and the count is the receipt for this call")
	assert.Equal(t, firstState, repo.customers[created.ID],
		"a second erasure must not even move updated_at")
}

// TestErasureUsesBothHandlesOfOneSubject proves that an id and an e-mail in the
// same subject are not an either-or.
//
// The case is the ordinary one, not a corner: a registered customer whose
// address has also opened guest checkouts has one row under their id and
// further rows under their e-mail, and the admin endpoint passes both fields
// through untouched. A resolver that returned as soon as the id matched would
// anonymize the account and leave every guest row fully personal — with the
// report saying the person had been forgotten.
func TestErasureUsesBothHandlesOfOneSubject(t *testing.T) {
	ctx := context.Background()
	svc, repo := newErasureService(t)

	account, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	firstGuest, err := svc.RegisterGuest(ctx, personalCustomer())
	require.NoError(t, err)
	secondGuest, err := svc.RegisterGuest(ctx, personalCustomer())
	require.NoError(t, err)

	result, err := svc.Erase(ctx, erasure.Subject{
		CustomerID: account.ID,
		Email:      "ayse@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)

	for _, id := range []string{account.ID, firstGuest.ID, secondGuest.ID} {
		assert.Equal(t, models.AnonymousEmail(id), repo.customers[id].Email,
			"the row under %s was reached by one handle or the other", id)
		assert.Empty(t, repo.customers[id].FirstName)
	}

	// Three rows and no more: the account matches BOTH handles and must not be
	// counted, or written, twice.
	assert.Equal(t, 3, result.Rows, "three customer rows, each written once")
}

// TestErasureClearsPersonalColumnsWrittenAfterAnErasure proves that the second
// pass looks at the columns and not at the e-mail.
//
// An erased record is not gone: it stays live, and UpdateCustomer patches one
// column at a time, so an admin or the storefront can write a fresh first name
// and phone onto it without ever touching the anonymous address. A guard that
// asked "is the e-mail already the derived one?" would answer yes, skip the
// UPDATE entirely, and report Anonymized with the person's name sitting in the
// row.
func TestErasureClearsPersonalColumnsWrittenAfterAnErasure(t *testing.T) {
	ctx := context.Background()
	svc, repo := newErasureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)

	first, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	require.Equal(t, erasure.Anonymized, first.Outcome)

	// The record is still live and still writable; this is the storefront or an
	// admin filling the row back in, with no idea it was erased.
	name, phone := "Ayse", "+90 555 111 2222"
	_, err = svc.UpdateCustomer(ctx, created.ID, UpdateCustomerInput{FirstName: &name, Phone: &phone})
	require.NoError(t, err, "an anonymized record stays live, or this test proves nothing")
	require.Equal(t, models.AnonymousEmail(created.ID), repo.customers[created.ID].Email,
		"the patch must leave the e-mail alone, which is exactly what makes it dangerous")

	second, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, second.Outcome)
	assert.Equal(t, 1, second.Rows, "the customer row held a name again and had to be written")

	stored := repo.customers[created.ID]
	assert.Empty(t, stored.FirstName, "the name written after the first erasure must be gone")
	assert.Empty(t, stored.Phone)
}

// TestErasureByEmailReachesEveryGuestRecord proves that the e-mail path is
// plural.
//
// It has to be: the same address can open any number of guest records — the
// unique index covers accounts only — and all of them are the same person. A
// path built on GetAccountByEmail would have left the guest rows behind.
func TestErasureByEmailReachesEveryGuestRecord(t *testing.T) {
	ctx := context.Background()
	svc, repo := newErasureService(t)

	first, err := svc.RegisterGuest(ctx, personalCustomer())
	require.NoError(t, err)
	second, err := svc.RegisterGuest(ctx, personalCustomer())
	require.NoError(t, err)
	account, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	// The subject is given in the case the person typed it, not in storage form.
	result, err := svc.Erase(ctx, erasure.Subject{Email: "  Ayse@Example.COM  "})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, 3, result.Rows)

	for _, id := range []string{first.ID, second.ID, account.ID} {
		assert.Equal(t, models.AnonymousEmail(id), repo.customers[id].Email,
			"every record under the address must be reached, guest or account")
	}

	// Nothing points back at the old address any more, which is why the second
	// pass by e-mail finds nobody — and still answers Anonymized.
	repeat, err := svc.Erase(ctx, erasure.Subject{Email: "ayse@example.com"})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, repeat.Outcome)
	assert.Equal(t, 0, repeat.Rows)
}

// TestErasureOfAnUnknownSubjectIsNotAnError proves that an absence is a normal
// answer.
//
// The sweep hands every holder the same subject and most holders have never
// seen the person; if "not mine" were an error, an ordinary sweep would report
// a failure for every module the customer never touched.
func TestErasureOfAnUnknownSubjectIsNotAnError(t *testing.T) {
	ctx := context.Background()
	svc, _ := newErasureService(t)

	byID, err := svc.Erase(ctx, erasure.Subject{CustomerID: "cust_0000000000000000000000000"})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, byID.Outcome)
	assert.Equal(t, 0, byID.Rows)

	// An id that is not even shaped like this module's is the same answer: it
	// names a person some other holder knows, not a bad request.
	byForeignID, err := svc.Erase(ctx, erasure.Subject{CustomerID: "some-other-system-id"})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, byForeignID.Outcome)

	// A malformed address is not rejected either; it is simply an address this
	// module stores nothing under.
	byEmail, err := svc.Erase(ctx, erasure.Subject{Email: "not-an-address"})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, byEmail.Outcome)
	assert.Equal(t, 0, byEmail.Rows)
}

// TestErasureReachesADeletedRecord proves that erasure and soft delete are two
// different acts.
//
// DeleteCustomer writes deleted_at and updated_at and clears not one personal
// column, so a deleted customer's e-mail, name and phone are still in the
// table. An erasure that skipped deleted rows would answer "anonymized" while
// the data sat where it was.
func TestErasureReachesADeletedRecord(t *testing.T) {
	ctx := context.Background()
	svc, repo := newErasureService(t)

	created, err := svc.CreateCustomer(ctx, personalCustomer())
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCustomer(ctx, created.ID))
	require.Equal(t, "ayse@example.com", repo.customers[created.ID].Email,
		"the soft delete must leave the personal columns alone, or this test proves nothing")

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)

	stored := repo.customers[created.ID]
	assert.Equal(t, models.AnonymousEmail(created.ID), stored.Email)
	assert.Empty(t, stored.FirstName)
	assert.NotNil(t, stored.DeletedAt, "the erasure must not resurrect the record either")
}

// TestErasureIsRefusedByAnUnconfiguredService proves that a module whose
// Register never ran says so instead of answering as if it held nothing.
func TestErasureIsRefusedByAnUnconfiguredService(t *testing.T) {
	var svc *Service

	_, err := svc.Erase(context.Background(), erasure.Subject{CustomerID: "cust_1"})
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
