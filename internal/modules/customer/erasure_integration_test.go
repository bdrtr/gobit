//go:build integration

package customer_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// This file proves the module's answer to an erasure request against a real
// database; it shares the container, the pool and the TestMain of
// customer_integration_test.go.
//
// The tests are here rather than beside the unit tests because every claim an
// erasure makes is a claim about STORAGE, and the fake repository cannot
// witness any of them. Four belong to the schema alone: that the anonymized
// address satisfies three CHECK constraints and a length limit; that two erased
// accounts do not collide on the partial unique index; that a soft-deleted row
// is reached at all, since the erasure path is the one place in this module
// whose SQL carries no deleted_at filter; and that the untouched jsonb is
// untouched in the COLUMN rather than in a Go map the fake happens to keep. A
// test that read the fake would stay green if any of the four fell out of the
// SQL.

// erasureEmailSeq gives every record in this file its own address, so a test
// resolving a subject by e-mail cannot reach a record another test opened.
var erasureEmailSeq atomic.Int64

// erasureEmail returns an address no other test in this file uses.
func erasureEmail() string {
	return fmt.Sprintf("erasure%d@example.com", erasureEmailSeq.Add(1))
}

// erasureService builds a service over the real repository.
func erasureService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// personalRecord opens a customer whose every named column holds something and
// gives it an address in the same shape.
//
// Filling every column matters: a column the erasure forgets is only visible if
// it held something in the first place, and a test built on empty strings would
// pass whether or not the UPDATE names them.
func personalRecord(ctx context.Context, t *testing.T, svc *service.Service, account bool) models.Customer {
	t.Helper()

	in := service.CustomerInput{
		Email:     erasureEmail(),
		FirstName: "Ayse",
		LastName:  "Kaya",
		Phone:     "+90 555 000 0000",
		Metadata:  map[string]any{"loyalty_tier": "gold"},
	}

	var created models.Customer
	var err error
	if account {
		created, err = svc.CreateCustomer(ctx, in)
	} else {
		created, err = svc.RegisterGuest(ctx, in)
	}
	require.NoError(t, err)

	_, err = svc.CreateAddress(ctx, created.ID, service.AddressInput{
		FirstName:   "Ayse",
		LastName:    "Kaya",
		Company:     "Kaya Ltd",
		Address1:    "Bagdat Cad. 12",
		Address2:    "Daire 4",
		City:        "Istanbul",
		CountryCode: "tr",
		PostalCode:  "34000",
		Phone:       "+90 555 000 0001",
	})
	require.NoError(t, err)

	return created
}

// customerColumns is what one customer row holds, read straight from the table
// so that no read-path filter can hide a value that is still there.
type customerColumns struct {
	Email     string
	FirstName string
	LastName  string
	Phone     string
	Metadata  string
	Deleted   bool
	UpdatedAt time.Time
}

// readCustomer reads the row by primary key, deleted or not.
func readCustomer(ctx context.Context, t *testing.T, id string) customerColumns {
	t.Helper()

	var row customerColumns
	err := testPool.Pool().QueryRow(ctx,
		`SELECT email, first_name, last_name, phone, metadata::text,
                deleted_at IS NOT NULL, updated_at
         FROM customer WHERE id = $1`, id).
		Scan(&row.Email, &row.FirstName, &row.LastName, &row.Phone,
			&row.Metadata, &row.Deleted, &row.UpdatedAt)
	require.NoError(t, err, "the row must still exist: orders and carts point at this id")
	return row
}

// addressColumns is what one address row holds.
type addressColumns struct {
	FirstName   string
	LastName    string
	Company     string
	Address1    string
	Address2    string
	City        string
	CountryCode string
	PostalCode  string
	Phone       string
}

// readAddresses reads every address of the customer, deleted ones included.
func readAddresses(ctx context.Context, t *testing.T, customerID string) []addressColumns {
	t.Helper()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT first_name, last_name, company, address_1, address_2,
                city, country_code, postal_code, phone
         FROM customer_address WHERE customer_id = $1 ORDER BY id`, customerID)
	require.NoError(t, err)
	defer rows.Close()

	var out []addressColumns
	for rows.Next() {
		var row addressColumns
		require.NoError(t, rows.Scan(&row.FirstName, &row.LastName, &row.Company,
			&row.Address1, &row.Address2, &row.City, &row.CountryCode,
			&row.PostalCode, &row.Phone))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestErasureOverwritesEveryNamedColumn proves against a real database that the
// report the module produces is true of its storage.
//
// The e-mail is the load-bearing half. The column carries three CHECK
// constraints (non-empty, equal to lower(email), and a shape regex), a length
// limit of 320 and a partial unique index; a value that failed any of them
// would abort the transaction, so the write SUCCEEDING is the proof that the
// derived address satisfies all five. That cannot be shown anywhere but here.
func TestErasureOverwritesEveryNamedColumn(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, customer.ModuleName, result.Holder)
	assert.Equal(t, 2, result.Rows, "one customer row and one address row")
	assert.Equal(t,
		[]string{"customer.metadata", "customer_group.metadata", "customer_address.country_code"},
		result.Kept)
	assert.NotEmpty(t, result.Why)

	row := readCustomer(ctx, t, created.ID)
	assert.Equal(t, models.AnonymousEmail(created.ID), row.Email)
	assert.Empty(t, row.FirstName)
	assert.Empty(t, row.LastName)
	assert.Empty(t, row.Phone)
	assert.False(t, row.Deleted, "an erasure is not a delete; the row stays live")
	assert.JSONEq(t, `{"loyalty_tier": "gold"}`, row.Metadata,
		"the free-form column is the controller's judgement and gobit does not rewrite it")

	addresses := readAddresses(ctx, t, created.ID)
	require.Len(t, addresses, 1)
	assert.Equal(t, addressColumns{
		Address1:    models.AnonymousPlaceholder,
		City:        models.AnonymousPlaceholder,
		CountryCode: "TR",
	}, addresses[0], "every named column but the country must be gone")
}

// TestErasureIsIdempotentInTheDatabase proves the second call is an answer and
// not a repeat performance.
//
// updated_at is what makes the claim measurable: a pass that rewrote the same
// values again would leave the columns looking identical while moving the
// timestamp, and the row would carry a receipt for work that did not happen.
func TestErasureIsIdempotentInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)

	first, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	require.Equal(t, erasure.Anonymized, first.Outcome)
	afterFirst := readCustomer(ctx, t, created.ID)

	second, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, second.Outcome,
		"a controller who runs the sweep twice gets the same answer, not an error")
	assert.Equal(t, 0, second.Rows, "nothing was left to write")
	assert.Equal(t, afterFirst, readCustomer(ctx, t, created.ID),
		"not even updated_at may move on the second pass")
	assert.Equal(t, models.AnonymousPlaceholder, readAddresses(ctx, t, created.ID)[0].Address1)
}

// TestErasureReachesASoftDeletedRecord proves that deleting a customer and
// erasing one are two different acts.
//
// The measurement is in the first assertion: after DeleteCustomer the e-mail,
// name and phone are still in the table, because the soft delete writes
// deleted_at and updated_at and nothing else. Every other query in this module
// filters deleted_at IS NULL; if the erasure path did too, this person would be
// told their data was anonymized while it sat where they left it.
func TestErasureReachesASoftDeletedRecord(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)
	require.NoError(t, svc.DeleteCustomer(ctx, created.ID))

	deleted := readCustomer(ctx, t, created.ID)
	require.True(t, deleted.Deleted)
	require.Equal(t, created.Email, deleted.Email,
		"the soft delete must leave the personal columns alone, or this test proves nothing")

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)

	row := readCustomer(ctx, t, created.ID)
	assert.Equal(t, models.AnonymousEmail(created.ID), row.Email)
	assert.Empty(t, row.FirstName)
	assert.True(t, row.Deleted, "the erasure must not resurrect the record either")
	assert.Equal(t, models.AnonymousPlaceholder, readAddresses(ctx, t, created.ID)[0].City,
		"the addresses of a deleted customer are soft deleted too, and they are still reached")
}

// TestErasureByEmailReachesEveryRecordUnderThatAddress proves the e-mail path
// is plural against the index that makes it so.
//
// customer_account_email_uniq covers accounts only, so the same address can open
// any number of guest records — and all of them are the same person. The
// records opened here are exactly what the partial index permits, which is why
// the claim has to be made against the real schema.
func TestErasureByEmailReachesEveryRecordUnderThatAddress(t *testing.T) {
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

	result, err := svc.Erase(ctx, erasure.Subject{Email: shared})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, 3, result.Rows, "two guest records and one account")

	for _, id := range ids {
		assert.Equal(t, models.AnonymousEmail(id), readCustomer(ctx, t, id).Email)
	}

	// The address that pointed at these rows is exactly what the first pass
	// destroyed, so the second one reaches nobody — and still answers
	// Anonymized rather than reporting the person as unknown.
	repeat, err := svc.Erase(ctx, erasure.Subject{Email: shared})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, repeat.Outcome)
	assert.Equal(t, 0, repeat.Rows)
}

// TestTwoErasedAccountsDoNotCollide proves why the anonymous address is derived
// from the id instead of being a constant.
//
// customer_account_email_uniq is UNIQUE (email) WHERE has_account AND
// deleted_at IS NULL. One shared placeholder would therefore erase the first
// account and fail on the second with a uniqueness conflict — on a person who
// asked to be forgotten. Only a real index can show that this does not happen.
func TestTwoErasedAccountsDoNotCollide(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	first := personalRecord(ctx, t, svc, true)
	second := personalRecord(ctx, t, svc, true)

	for _, id := range []string{first.ID, second.ID} {
		result, err := svc.Erase(ctx, erasure.Subject{CustomerID: id})
		require.NoError(t, err, "the second erasure must not hit the unique index")
		require.Equal(t, erasure.Anonymized, result.Outcome)
	}

	assert.NotEqual(t,
		readCustomer(ctx, t, first.ID).Email,
		readCustomer(ctx, t, second.ID).Email,
		"two erased accounts must keep two distinct addresses")
}

// TestErasureOfSomebodyThisModuleNeverSawIsNotAnError proves that an absence is
// a normal answer.
//
// The sweep hands every holder the same subject, and most holders have never
// seen the person. If "not mine" were an error, an ordinary sweep would report
// a failure for every module the customer never touched, and the controller
// could not tell that from a broken query.
func TestErasureOfSomebodyThisModuleNeverSawIsNotAnError(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	byID, err := svc.Erase(ctx, erasure.Subject{CustomerID: models.NewCustomerID(nowUTC())})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, byID.Outcome)
	assert.Equal(t, 0, byID.Rows)

	byEmail, err := svc.Erase(ctx, erasure.Subject{Email: erasureEmail()})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, byEmail.Outcome)
	assert.Equal(t, 0, byEmail.Rows)

	// A subject naming nobody at all is the one refusal: erasing "everyone" is
	// not an erasure request.
	_, err = svc.Erase(ctx, erasure.Subject{})
	require.Error(t, err)
}

// TestErasureUsesBothHandlesAgainstTheDatabase proves that an id and an e-mail
// in the same subject reach the union of their rows in ONE locked query.
//
// This is the shape the admin endpoint produces (internal/app/erasure.go passes
// both request fields straight into the subject) and the shape a real person
// has: an account under their id, guest checkouts under the same address. The
// claim is made here as well as against the fake because the union is a
// property of the SQL — one query, OR'ed, ordered by id — and a resolver that
// returned after the primary-key lookup would leave the guest rows fully
// personal while the report said otherwise.
func TestErasureUsesBothHandlesAgainstTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	shared := erasureEmail()
	var accountID string
	var ids []string
	for _, account := range []bool{false, false, true} {
		in := service.CustomerInput{Email: shared, FirstName: "Ayse", Phone: "+90 555 000 0000"}

		var created models.Customer
		var err error
		if account {
			created, err = svc.CreateCustomer(ctx, in)
			accountID = created.ID
		} else {
			created, err = svc.RegisterGuest(ctx, in)
		}
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}

	result, err := svc.Erase(ctx, erasure.Subject{CustomerID: accountID, Email: shared})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, result.Outcome)
	assert.Equal(t, 3, result.Rows,
		"three rows, each written once: the account matches both handles and must not be written twice")

	for _, id := range ids {
		row := readCustomer(ctx, t, id)
		assert.Equal(t, models.AnonymousEmail(id), row.Email,
			"the row under %s was reached by one handle or the other", id)
		assert.Empty(t, row.FirstName)
	}
}

// TestErasureClearsColumnsWrittenAfterAnErasure proves that the second pass
// looks at the columns and not at the e-mail.
//
// The measurement needs a real database because the guard lives in the UPDATE's
// WHERE clause. An erased record stays LIVE and UpdateCustomer is a per-column
// COALESCE patch, so the storefront or an admin can write a fresh first name and
// phone onto it without touching the anonymous address — which is exactly what
// happens here. A guard that asked only "is the e-mail already derived?" would
// find yes, skip the UPDATE, and answer Anonymized with the person's name still
// in the row.
func TestErasureClearsColumnsWrittenAfterAnErasure(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)

	created := personalRecord(ctx, t, svc, true)

	first, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	require.Equal(t, erasure.Anonymized, first.Outcome)

	name, phone := "Ayse", "+90 555 111 2222"
	_, err = svc.UpdateCustomer(ctx, created.ID, service.UpdateCustomerInput{
		FirstName: &name,
		Phone:     &phone,
	})
	require.NoError(t, err, "an anonymized record stays live and writable, or this test proves nothing")
	require.Equal(t, models.AnonymousEmail(created.ID), readCustomer(ctx, t, created.ID).Email,
		"the patch leaves the e-mail alone, which is precisely what makes it dangerous")

	second, err := svc.Erase(ctx, erasure.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, erasure.Anonymized, second.Outcome)
	assert.Equal(t, 1, second.Rows, "the customer row held a name again and had to be written")

	row := readCustomer(ctx, t, created.ID)
	assert.Empty(t, row.FirstName, "the name written after the first erasure must be gone")
	assert.Empty(t, row.Phone)
}
