package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// fixedClock is the deterministic time source of the tests.
var fixedClock = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// newTestService builds a service that works over the fake repository.
func newTestService(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	return New(repo, Options{Now: func() time.Time { return fixedClock }}), repo
}

// TestTheEmailIsNormalizedOnStorage proves that the e-mail is normalized AT
// STORAGE.
//
// Had normalization been left to the moment of reading, "Ali@X.com" and
// "ali@x.com" would be two separate accounts, because the uniqueness index
// works on the raw column.
func TestTheEmailIsNormalizedOnStorage(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)

	created, err := svc.CreateCustomer(ctx, CustomerInput{Email: "  Ali.Veli@Example.COM  "})
	require.NoError(t, err)
	assert.Equal(t, "ali.veli@example.com", created.Email, "the e-mail has to be lower-cased and trimmed")

	// The value written to the repository has to be normalized too; a value
	// corrected in the DTO but sent to the table raw would leave the index
	// useless.
	stored, ok := repo.customers[created.ID]
	require.True(t, ok)
	assert.Equal(t, "ali.veli@example.com", stored.Email)

	// The read path applies the same normalization.
	found, err := svc.GetCustomerByEmail(ctx, "ALI.VELI@EXAMPLE.COM")
	require.NoError(t, err)
	assert.Equal(t, created.ID, found.ID)
}

// TestAnInvalidEmailIsRejected proves that validation weeds an invalid e-mail
// out before it reaches the database.
func TestAnInvalidEmailIsRejected(t *testing.T) {
	ctx := context.Background()

	cases := map[string]string{
		"empty":                      "",
		"whitespace only":            "   ",
		"no @":                       "aliexample.com",
		"no local part":              "@example.com",
		"no domain":                  "ali@",
		"no dot in the domain":       "ali@example",
		"the domain ends with a dot": "ali@example.",
		"two @":                      "ali@veli@example.com",
		"contains a space":           "ali veli@example.com",
		"too long":                   strings.Repeat("a", 320) + "@example.com",
	}

	for name, email := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)

			_, err := svc.CreateCustomer(ctx, CustomerInput{Email: email})
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Zero(t, repo.calls["CreateCustomer"], "invalid input must not reach the repository")
		})
	}
}

// TestGuestsAndAccountsAreDistinct proves that the two registration paths set
// the has_account field differently.
//
// Had the distinction hung on a body flag, a request to the admin endpoint
// would silently fall outside the uniqueness rule.
func TestGuestsAndAccountsAreDistinct(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	account, err := svc.CreateCustomer(ctx, CustomerInput{Email: "account@example.com"})
	require.NoError(t, err)
	assert.True(t, account.HasAccount, "CreateCustomer has to open a registered account")
	assert.False(t, account.IsGuest())

	guest, err := svc.RegisterGuest(ctx, CustomerInput{Email: "guest@example.com"})
	require.NoError(t, err)
	assert.False(t, guest.HasAccount, "RegisterGuest has to open a guest record")
	assert.True(t, guest.IsGuest())

	assert.True(t, strings.HasPrefix(account.ID, models.CustomerIDPrefix), "the id has to carry the prefix")
}

// TestManyGuestsShareAnEmail proves the guest scenario of Phase
// 5's DoD.
//
// A second guest record with the same e-mail MUST NOT BE REJECTED: a guest
// record is not an identity but the contact details of a one-off purchase.
func TestManyGuestsShareAnEmail(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	first, err := svc.RegisterGuest(ctx, CustomerInput{Email: "same@example.com"})
	require.NoError(t, err)

	second, err := svc.RegisterGuest(ctx, CustomerInput{Email: "SAME@example.com"})
	require.NoError(t, err, "a second guest record with the same e-mail has to be accepted")

	assert.NotEqual(t, first.ID, second.ID, "there have to be two separate records")
	assert.Equal(t, first.Email, second.Email, "normalization has to give both records the same result")
}

// TestAnAccountEmailIsUnique proves that the e-mail of an account is unique.
func TestAnAccountEmailIsUnique(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	_, err := svc.CreateCustomer(ctx, CustomerInput{Email: "unique@example.com"})
	require.NoError(t, err)

	_, err = svc.CreateCustomer(ctx, CustomerInput{Email: "UNIQUE@example.com"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"a second account with the same e-mail has to be a conflict")
}

// TestAGuestBecomesAnAccount proves all three outcomes of the conversion:
// success, an e-mail conflict, and already being an account.
func TestAGuestBecomesAnAccount(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		svc, _ := newTestService(t)

		guest, err := svc.RegisterGuest(ctx, CustomerInput{Email: "convert@example.com"})
		require.NoError(t, err)

		require.NoError(t, svc.ConvertGuestToAccount(ctx, guest.ID))

		reread, err := svc.GetCustomer(ctx, guest.ID)
		require.NoError(t, err)
		assert.True(t, reread.HasAccount, "after the conversion the record has to be an account")
	})

	t.Run("e-mail on another account", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.CreateCustomer(ctx, CustomerInput{Email: "taken@example.com"})
		require.NoError(t, err)
		guest, err := svc.RegisterGuest(ctx, CustomerInput{Email: "taken@example.com"})
		require.NoError(t, err)

		err = svc.ConvertGuestToAccount(ctx, guest.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))

		reread, getErr := svc.GetCustomer(ctx, guest.ID)
		require.NoError(t, getErr)
		assert.False(t, reread.HasAccount, "a conflicting conversion MUST NOT CHANGE the record")
	})

	t.Run("already an account", func(t *testing.T) {
		svc, _ := newTestService(t)

		account, err := svc.CreateCustomer(ctx, CustomerInput{Email: "already@example.com"})
		require.NoError(t, err)

		err = svc.ConvertGuestToAccount(ctx, account.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err),
			"a record that is already an account has to get a conflict, NOT a silent no-op")
	})

	t.Run("missing customer", func(t *testing.T) {
		svc, _ := newTestService(t)

		err := svc.ConvertGuestToAccount(ctx, models.NewCustomerID(fixedClock))
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	})
}

// TestTheIDPrefixIsValidated proves that an id of the wrong kind is weeded out
// without ever reaching the database.
func TestTheIDPrefixIsValidated(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)

	_, err := svc.GetCustomer(ctx, models.NewCustomerGroupID(fixedClock))
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.calls["GetCustomer"], "a wrong prefix must not reach the repository")
}

// TestThePagingBounds proves the limit/offset validation.
func TestThePagingBounds(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	_, err := svc.ListCustomers(ctx, ListCustomersInput{Limit: MaxLimit + 1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "the maximum limit cannot be exceeded")

	_, err = svc.ListCustomers(ctx, ListCustomersInput{Offset: -1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "a negative offset has to be rejected")

	page, err := svc.ListCustomers(ctx, ListCustomersInput{})
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, page.Limit, "when no limit is given the default has to apply")
}

// TestTheListFilters proves the guest/account and group filters.
func TestTheListFilters(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	_, err := svc.CreateCustomer(ctx, CustomerInput{Email: "a1@example.com"})
	require.NoError(t, err)
	guest, err := svc.RegisterGuest(ctx, CustomerInput{Email: "g1@example.com"})
	require.NoError(t, err)

	hasAccount := true
	page, err := svc.ListCustomers(ctx, ListCustomersInput{HasAccount: &hasAccount})
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Count)
	require.Len(t, page.Items, 1)
	assert.True(t, page.Items[0].HasAccount)

	noAccount := false
	page, err = svc.ListCustomers(ctx, ListCustomersInput{HasAccount: &noAccount})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, guest.ID, page.Items[0].ID)

	// The e-mail filter is normalized too.
	email := "G1@EXAMPLE.COM"
	page, err = svc.ListCustomers(ctx, ListCustomersInput{Email: &email})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, guest.ID, page.Items[0].ID)
}

// TestADeletedCustomerCannotBeRead proves that a soft delete takes the
// customer out of the read paths.
func TestADeletedCustomerCannotBeRead(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	customer, err := svc.CreateCustomer(ctx, CustomerInput{Email: "to-be-deleted@example.com"})
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, customer.ID, validAddress())
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, customer.ID))

	_, err = svc.GetCustomer(ctx, customer.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	page, err := svc.ListCustomers(ctx, ListCustomersInput{})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "a soft-deleted customer must not appear in the list")

	// The addresses go too: a cascade runs only on a real delete, and since a
	// soft delete is an UPDATE it does not take the addresses with it by
	// itself.
	_, err = svc.ListAddresses(ctx, customer.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// validAddress is the input of a valid address the tests use.
func validAddress() AddressInput {
	return AddressInput{
		FirstName:   "Ali",
		LastName:    "Veli",
		Address1:    "1 Main St",
		City:        "Istanbul",
		CountryCode: "tr",
		PostalCode:  "34000",
	}
}
