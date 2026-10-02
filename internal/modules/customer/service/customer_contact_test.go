package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// ReviseContact mirrors the query: the name and the phone are written only
// while they are the ones the caller read, on a live customer.
func (m *memRepo) ReviseContact(
	_ context.Context, id string, read, next models.ContactTerms, now time.Time,
) (models.Customer, bool, error) {
	m.record("ReviseContact")
	c, live := m.liveCustomer(id)
	if !live || c.Contact() != read {
		return models.Customer{}, false, nil
	}
	c.FirstName, c.LastName, c.Phone, c.UpdatedAt = next.FirstName, next.LastName, next.Phone, now
	m.customers[id] = c

	return c, true, nil
}

// TestACustomersContactIsCorrectedFromWhatWasRead is ADR 0337: the name and
// the phone, trimmed, are written from the ones read and the e-mail is kept;
// a customer corrected since is refused by what they are now; a name too
// long is refused before the store is asked; an unknown customer is not
// found.
func TestACustomersContactIsCorrectedFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	svc := New(repo, Options{})
	created, err := svc.CreateCustomer(ctx, CustomerInput{Email: "ada@example.com", FirstName: "Ada", Phone: "555"})
	require.NoError(t, err)

	corrected, err := svc.ReviseContact(ctx, created.ID, created.Contact(),
		models.ContactTerms{FirstName: " Ada ", LastName: " Lovelace ", Phone: " +90 555 0000 "})
	require.NoError(t, err)
	assert.Equal(t, models.ContactTerms{FirstName: "Ada", LastName: "Lovelace", Phone: "+90 555 0000"}, corrected.Contact())
	assert.Equal(t, "ada@example.com", corrected.Email, "the e-mail is kept")

	_, err = svc.ReviseContact(ctx, created.ID, created.Contact(), models.ContactTerms{FirstName: "Augusta"})
	require.Error(t, err)
	assert.Equal(t, CodeContactRevised, errors.CodeOf(err), "read before the correction: %v", err)
	assert.Contains(t, err.Error(), `the name is "Ada Lovelace" and the phone "+90 555 0000" now`)

	calls := repo.calls["ReviseContact"]
	_, err = svc.ReviseContact(ctx, created.ID, corrected.Contact(),
		models.ContactTerms{FirstName: strings.Repeat("a", models.MaxNameLen+1)})
	assert.True(t, errors.IsInvalid(err), "a name too long: %v", err)
	assert.Equal(t, calls, repo.calls["ReviseContact"], "a refused input never reaches the store")

	_, err = svc.ReviseContact(ctx, "cust_missing", corrected.Contact(), models.ContactTerms{FirstName: "X"})
	assert.True(t, errors.IsNotFound(err), "an unknown customer: %v", err)
}

// TestThePanelCorrectsACustomersContact is ADR 0337 through the surface: the
// read and the written contact cross as JSON with their fields named, and a
// stale read is refused.
func TestThePanelCorrectsACustomersContact(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	surface := NewAdminSurface(svc)
	created, err := svc.CreateCustomer(ctx, CustomerInput{Email: "ada@example.com", FirstName: "Ada"})
	require.NoError(t, err)

	read := json.RawMessage(`{"first_name":"Ada","last_name":"","phone":""}`)
	require.NoError(t, surface.ReviseCustomerContact(ctx, created.ID, read,
		json.RawMessage(`{"first_name":"Ada","last_name":"Lovelace","phone":"555"}`)))
	stored, err := svc.GetCustomer(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ContactTerms{FirstName: "Ada", LastName: "Lovelace", Phone: "555"}, stored.Contact())

	err = surface.ReviseCustomerContact(ctx, created.ID, read, json.RawMessage(`{"first_name":"Augusta"}`))
	assert.Equal(t, CodeContactRevised, errors.CodeOf(err))
	err = surface.ReviseCustomerContact(ctx, created.ID, json.RawMessage(`[`), read)
	assert.True(t, errors.IsInvalid(err), "a read that is not JSON: %v", err)
}
