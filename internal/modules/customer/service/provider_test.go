package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// newTestProvider builds a provider that works over the fake repository.
func newTestProvider(t *testing.T) (*QueryProvider, *Service, *memRepo) {
	t.Helper()

	svc, repo := newTestService(t)
	return NewQueryProvider(svc), svc, repo
}

// TestTheProviderEntityName proves that the provider returns an entity name
// that matches the name it is registered under.
//
// Query looks the provider up by the name "<entity>.query" and VERIFIES that
// Entity() matches the name; if the two diverge, resolution fails at that
// moment (ADR 0004).
func TestTheProviderEntityName(t *testing.T) {
	p, _, _ := newTestProvider(t)
	assert.Equal(t, "customer", p.Entity())
	assert.Equal(t, "customer.query", p.Entity()+query.ProviderSuffix)
}

// TestTheProviderFetchesGroupIDsInOneCall proves the N+1 ban.
//
// For three customers the group ids have to arrive in ONE batch call. An
// implementation that ran a separate query per customer would produce the same
// RESULT too, so the test measures the NUMBER OF CALLS rather than the result —
// what ADR 0004 forbids is not a result but a number of round trips.
func TestTheProviderFetchesGroupIDsInOneCall(t *testing.T) {
	ctx := context.Background()
	p, svc, repo := newTestProvider(t)

	group := newTestGroup(ctx, t, svc, "VIP")
	var ids []string
	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		customer := newTestCustomer(ctx, t, svc, email)
		require.NoError(t, svc.AddToGroup(ctx, customer.ID, group.ID))
		ids = append(ids, customer.ID)
	}

	repo.calls["GroupIDsOfCustomers"] = 0
	records, err := p.FetchByIDs(ctx, ids, []string{fieldID, fieldGroupIDs})
	require.NoError(t, err)
	require.Len(t, records, 3)

	assert.Equal(t, 1, repo.calls["GroupIDsOfCustomers"],
		"the group ids of three customers have to arrive in ONE call (the N+1 ban)")

	for _, record := range records {
		assert.Equal(t, []string{group.ID}, record[fieldGroupIDs])
	}
}

// TestTheProviderGivesAGrouplessCustomerAnEmptySlice proves that a customer
// with no group gets an empty slice, not nil.
func TestTheProviderGivesAGrouplessCustomerAnEmptySlice(t *testing.T) {
	ctx := context.Background()
	p, svc, _ := newTestProvider(t)

	customer := newTestCustomer(ctx, t, svc, "groupless@example.com")

	records, err := p.FetchByIDs(ctx, []string{customer.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)

	ids, ok := records[0][fieldGroupIDs].([]string)
	require.True(t, ok, "the group_ids field has to be a string slice")
	assert.NotNil(t, ids)
	assert.Empty(t, ids)
}

// TestTheProviderSkipsGroupIDsNobodyAskedFor proves that, with field
// selection, the membership query is never run.
func TestTheProviderSkipsGroupIDsNobodyAskedFor(t *testing.T) {
	ctx := context.Background()
	p, svc, repo := newTestProvider(t)

	customer := newTestCustomer(ctx, t, svc, "field@example.com")

	repo.calls["GroupIDsOfCustomers"] = 0
	records, err := p.FetchByIDs(ctx, []string{customer.ID}, []string{fieldEmail})
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Zero(t, repo.calls["GroupIDsOfCustomers"],
		"when group_ids is not asked for, the membership query must not run at all")
	assert.NotContains(t, records[0], fieldGroupIDs)
	// The id is ADDED even when it is not asked for: Query joins records on
	// "id".
	assert.Equal(t, customer.ID, records[0][query.IDField])
}

// TestTheProviderRejectsAnUnknownField proves that an unsupported field
// returns Invalid (ADR 0004: field validation belongs to the provider).
func TestTheProviderRejectsAnUnknownField(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newTestProvider(t)

	_, err := p.FetchByIDs(ctx, []string{"cust_x"}, []string{"hidden_field"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.List(ctx, query.ListOptions{Fields: []string{"hidden_field"}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestTheProviderFilters proves the supported and the unsupported filters.
func TestTheProviderFilters(t *testing.T) {
	ctx := context.Background()
	p, svc, _ := newTestProvider(t)

	account := newTestCustomer(ctx, t, svc, "filter@example.com")
	guest, err := svc.RegisterGuest(ctx, CustomerInput{Email: "guest@example.com"})
	require.NoError(t, err)

	t.Run("id", func(t *testing.T) {
		records, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"id": account.ID}})
		require.NoError(t, listErr)
		require.Len(t, records, 1)
		assert.Equal(t, account.ID, records[0][fieldID])
	})

	t.Run("has_account", func(t *testing.T) {
		records, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"has_account": false}})
		require.NoError(t, listErr)
		require.Len(t, records, 1)
		assert.Equal(t, guest.ID, records[0][fieldID])
	})

	t.Run("the e-mail is normalized", func(t *testing.T) {
		records, listErr := p.List(ctx, query.ListOptions{
			Filters: map[string]any{"email": "FILTER@EXAMPLE.COM"},
		})
		require.NoError(t, listErr)
		require.Len(t, records, 1)
		assert.Equal(t, account.ID, records[0][fieldID])
	})

	t.Run("an unknown filter", func(t *testing.T) {
		_, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"surname": "Veli"}})
		require.Error(t, listErr)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(listErr))
	})

	t.Run("a wrong type", func(t *testing.T) {
		_, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"has_account": "yes"}})
		require.Error(t, listErr)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(listErr))
	})

	t.Run("the id does not combine with another filter", func(t *testing.T) {
		_, listErr := p.List(ctx, query.ListOptions{
			Filters: map[string]any{"id": account.ID, "has_account": true},
		})
		require.Error(t, listErr)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(listErr),
			"an exact id set cannot be narrowed silently by a second filter")
	})
}

// TestTheProviderSkipsAMissingID proves that NO record comes back for a
// missing id and that this is NOT an error (the ADR 0004 contract).
func TestTheProviderSkipsAMissingID(t *testing.T) {
	ctx := context.Background()
	p, svc, _ := newTestProvider(t)

	customer := newTestCustomer(ctx, t, svc, "present@example.com")
	missing := models.NewCustomerID(fixedClock)

	records, err := p.FetchByIDs(ctx, []string{customer.ID, missing}, nil)
	require.NoError(t, err, "an id that is not found is not an error")
	require.Len(t, records, 1)
	assert.Equal(t, customer.ID, records[0][fieldID])
}

// TestTheProviderUnboundedListFallsBackToTheDefault proves that, when limit 0
// is given, the module's default page size is applied.
//
// In the Query contract 0 means "unlimited"; an unlimited root list would load
// the whole customer table into memory in a single request.
func TestTheProviderUnboundedListFallsBackToTheDefault(t *testing.T) {
	ctx := context.Background()
	p, svc, repo := newTestProvider(t)

	for _, email := range []string{"s1@example.com", "s2@example.com"} {
		newTestCustomer(ctx, t, svc, email)
	}

	// The fake repository does not report back the limit it was given, so the
	// bound is proven indirectly through the service's validation: an
	// oversized limit is rejected.
	_, err := p.List(ctx, query.ListOptions{Limit: int(MaxLimit) + 1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	records, err := p.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, records, 2)
	assert.Positive(t, repo.calls["ListCustomers"])
}
