//go:build integration

package customer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestACustomersContactIsCorrectedOnlyAsItWasRead is ADR 0337 against a real
// PostgreSQL: the name and the phone are written from the ones read and the
// e-mail is kept; a stale first name, last name or phone writes nothing and
// is refused by what the customer is now; a deleted customer is not found.
func TestACustomersContactIsCorrectedOnlyAsItWasRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	email := newEmail(t)
	created, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: email, FirstName: "Ada", Phone: "555"})
	require.NoError(t, err)

	corrected, err := svc.ReviseContact(ctx, created.ID, created.Contact(),
		models.ContactTerms{FirstName: "Ada", LastName: "Lovelace", Phone: "+90 555 0000"})
	require.NoError(t, err)
	stored, err := svc.GetCustomer(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, corrected.Contact(), stored.Contact())
	assert.Equal(t, email, stored.Email, "the e-mail is kept")

	for label, read := range map[string]models.ContactTerms{
		"a first name read before": {FirstName: "Augusta", LastName: "Lovelace", Phone: "+90 555 0000"},
		"a last name read before":  {FirstName: "Ada", Phone: "+90 555 0000"},
		"a phone read before":      {FirstName: "Ada", LastName: "Lovelace", Phone: "555"},
	} {
		_, err = svc.ReviseContact(ctx, created.ID, read, models.ContactTerms{FirstName: "Changed"})
		require.Error(t, err, label)
		assert.Equal(t, service.CodeContactRevised, errors.CodeOf(err), "%s: %v", label, err)
	}
	stored, err = svc.GetCustomer(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Ada", stored.FirstName, "a stale read writes nothing")

	require.NoError(t, svc.DeleteCustomer(ctx, created.ID))
	_, err = svc.ReviseContact(ctx, created.ID, stored.Contact(), models.ContactTerms{FirstName: "Gone"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted customer: %v", err)
}
