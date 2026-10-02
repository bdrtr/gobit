package accounts

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/query"
)

// accountCatalog answers a customer only when the query asks for an account.
type accountCatalog struct{ asked query.GraphSpec }

func (c *accountCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	c.asked = spec
	if spec.Filters["has_account"] != true {
		// A catalog that would answer the guest row the address left.
		return []query.Record{{query.IDField: "cust_GUEST"}}, nil
	}

	return nil, nil
}

// customerRecords records what was opened and converted.
type customerRecords struct {
	opened    []string
	converted []string
	err       error
}

func (r *customerRecords) RegisterGuestCustomer(_ context.Context, email, _, _, _ string) (string, error) {
	r.opened = append(r.opened, email)

	return "cust_NEW", nil
}

func (r *customerRecords) ConvertGuestToAccount(_ context.Context, customerID string) error {
	r.converted = append(r.converted, customerID)

	return r.err
}

func boundModule(t *testing.T, catalog query.Query, records customers) *Module {
	t.Helper()

	c := container.New(nil)
	require.NoError(t, c.Provide(queryCatalog, catalog))
	require.NoError(t, c.Provide(customerService, records))
	m := New(nil)
	require.NoError(t, m.Register(t.Context(), c))

	return m
}

// TestAGuestRecordIsNotAnAccount is D221: an address a guest checkout left
// behind is not one with an account, so its shopper can register.
func TestAGuestRecordIsNotAnAccount(t *testing.T) {
	t.Parallel()

	catalog := &accountCatalog{}
	m := boundModule(t, catalog, &customerRecords{})

	id, err := m.CustomerIDForEmail(t.Context(), "once-a-guest@example.test")
	require.NoError(t, err)
	assert.Empty(t, id, "a guest row is not an account")
	assert.Equal(t, true, catalog.asked.Filters["has_account"])
}

// TestAnOpenedAccountIsAnAccount is D221's other half: the record opened for a
// proven address is converted to an account, and a refused conversion fails
// the opening.
func TestAnOpenedAccountIsAnAccount(t *testing.T) {
	t.Parallel()

	records := &customerRecords{}
	id, err := boundModule(t, &accountCatalog{}, records).OpenAccount(t.Context(), "new@example.test")
	require.NoError(t, err)
	assert.Equal(t, "cust_NEW", id)
	assert.Equal(t, []string{"cust_NEW"}, records.converted)

	refused := &customerRecords{err: errors.New("another account has this address")}
	_, err = boundModule(t, &accountCatalog{}, refused).OpenAccount(t.Context(), "taken@example.test")
	require.Error(t, err)
}
