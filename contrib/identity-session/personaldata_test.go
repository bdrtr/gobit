package identitysession_test

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/personaldata"
)

// These are the ANSWERS a controller gets, not the SQL.
//
// What the statements do against the real table is in store_integration_test.go.
// What is here is what a store cannot decide: which outcome is reported, and
// whether "could not look" stays distinct from "looked and found nothing".

// TestTheDeclarationNamesEveryColumnOfTheTable is the gate under the other two.
//
// A declaration is what makes the other obligations honest (ADR 0029). The risk is
// not that it says something false but that it goes SHORT: a column added to the
// table and not here is invisible to every sweep from then on, and the report
// still looks complete.
func TestTheDeclarationNamesEveryColumnOfTheTable(t *testing.T) {
	t.Parallel()

	declared := map[string][]string{}
	for _, holding := range moduleWithStore(t, elsewhere{}).PersonalData().Holdings {
		assert.NotEmpty(t, holding.Why, "a holding with no reason tells a controller nothing")
		declared[holding.Table] = append(declared[holding.Table], holding.Column)
	}
	for table := range declared {
		sort.Strings(declared[table])
	}

	// BOTH tables. The registration table was missing here until the personal-data
	// audit failed on it, and the assertion is keyed by table for that reason: an
	// assertion that named one table would go on passing when a second arrived.
	assert.Equal(t, map[string][]string{
		"customer_credentials": {
			"created_at", "customer_id", "email", "password_hash", "updated_at",
		},
		"customer_registrations": {
			"created_at", "email", "expires_at", "password_hash", "token_hash",
		},
	}, declared, "every column of every table, because every one of them is about somebody")
}

// TestAStoreThatCannotEraseSaysSOAndSaysWhat is the answer an installation binding
// LDAP or its own users table gets.
//
// The capability is optional because such a store must not be asked to delete rows
// out of a table it does not own. What matters is that the sweep is TOLD: an
// outcome of "deleted, 0 rows" would report a deletion that never happened and the
// person would be told they were forgotten.
func TestAStoreThatCannotEraseSaysSOAndSaysWhat(t *testing.T) {
	t.Parallel()

	m := moduleWithStore(t, elsewhere{})

	result, err := m.Erase(t.Context(), personaldata.Subject{CustomerID: "cust_06G8ELSEWHERE00000000"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Retained, result.Outcome)
	assert.Zero(t, result.Rows)
	assert.NotEmpty(t, result.Why)
	assert.Contains(t, result.Kept, "customer_credentials.password_hash",
		"what is kept is listed from the declaration, so the two cannot drift apart")

	disclosure, err := m.PersonalDataOf(t.Context(),
		personaldata.Subject{CustomerID: "cust_06G8ELSEWHERE00000000"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Unresolvable, disclosure.State,
		"could not look is NOT looked and found nothing; folding them lets the first "+
			"hide inside the second")
}

// TestARequestThatNamesNobodyIsRefused keeps an empty subject from reading as a
// clean sweep.
func TestARequestThatNamesNobodyIsRefused(t *testing.T) {
	t.Parallel()

	m := moduleWithStore(t, elsewhere{})

	_, err := m.Erase(t.Context(), personaldata.Subject{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), identitysession.CodeSubjectEmpty)

	_, err = m.PersonalDataOf(t.Context(), personaldata.Subject{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), identitysession.CodeSubjectEmpty)
}

// moduleWithStore is a registered module over the given credential store.
func moduleWithStore(t *testing.T, store identitysession.Credentials) *identitysession.Module {
	t.Helper()

	m := identitysession.New(identitysession.Options{
		Secret:      []byte("a personal-data test signing secret of 32"),
		Insecure:    true,
		Credentials: store,
	})
	require.NoError(t, m.Register(t.Context(), container.New(nil)))

	return m
}

// elsewhere is a credential store that keeps its rows somewhere this module does
// not own — the shape of every installation that binds LDAP or its own table.
type elsewhere struct{}

// Credential answers nothing.
func (elsewhere) Credential(
	context.Context, string,
) (customerID, passwordHash string, err error) {
	return "", "", identitysession.ErrPasswordMismatch
}

// Put writes nothing.
func (elsewhere) Put(context.Context, string, string, string) error { return nil }

var _ identitysession.Credentials = elsewhere{}
