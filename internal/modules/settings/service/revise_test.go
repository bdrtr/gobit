package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/settings/models"
	"github.com/bdrtr/gobit/internal/modules/settings/service"
)

// ReviseProfile mirrors the queries: the profile is written while it is the
// one read, named by its last write, or, read as never written, while there
// is none; each write moves the moment a second on.
func (m *memStore) ReviseProfile(
	_ context.Context, readUpdatedAt *time.Time, profile models.StoreProfile,
) (models.StoreProfile, bool, error) {
	switch {
	case readUpdatedAt == nil && m.profile != nil,
		readUpdatedAt != nil && (m.profile == nil || !m.profile.UpdatedAt.Equal(*readUpdatedAt)):
		return models.StoreProfile{}, false, nil
	}
	m.writes++
	stored := profile
	stored.UpdatedAt = time.Date(2026, 10, 2, 9, 0, m.writes, 0, time.UTC)
	m.profile = &stored

	return stored, true, nil
}

// TestTheStoreProfileIsRevisedFromWhatWasRead is ADR 0336: a shop that has
// not said who it is writes its first profile from none; the profile is then
// written from the moment it was read; a profile written since, a first
// profile written in between, and a profile that breaks a rule are refused,
// the last before the store is asked.
func TestTheStoreProfileIsRevisedFromWhatWasRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := &memStore{}
	svc := service.New(store, service.Options{})

	first, err := svc.ReviseProfile(ctx, nil, validProfile())
	require.NoError(t, err)
	_, err = svc.ReviseProfile(ctx, nil, validProfile())
	require.Error(t, err)
	assert.Equal(t, service.CodeProfileRevised, errors.CodeOf(err), "a first profile written in between: %v", err)

	next := validProfile()
	next.LegalName = " Gobit Ltd "
	revised, err := svc.ReviseProfile(ctx, &first.UpdatedAt, next)
	require.NoError(t, err)
	assert.Equal(t, "Gobit Ltd", revised.LegalName, "checked and trimmed as a set profile is")
	_, err = svc.ReviseProfile(ctx, &first.UpdatedAt, validProfile())
	require.Error(t, err)
	assert.Equal(t, service.CodeProfileRevised, errors.CodeOf(err), "read before the revision: %v", err)
	assert.Contains(t, err.Error(), "draw the page again")

	writes := store.writes
	broken := validProfile()
	broken.CountryCode = "Turkey"
	_, err = svc.ReviseProfile(ctx, &revised.UpdatedAt, broken)
	assert.True(t, errors.IsInvalid(err), "a country that is not a code: %v", err)
	assert.Equal(t, writes, store.writes, "a broken profile never reaches the store")
}

// TestThePanelReadsAndWritesTheStoreProfile is ADR 0336 through the surface:
// a shop that has not said who it is reads as null, a profile written reads
// with every field and the moment it was written, which the next write names;
// a moment that is not one is refused.
func TestThePanelReadsAndWritesTheStoreProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	surface := service.NewAdminSurface(service.New(&memStore{}, service.Options{}))

	raw, err := surface.StoreProfileJSON(ctx)
	require.NoError(t, err)
	assert.JSONEq(t, `null`, string(raw), "a shop that has not said who it is")

	require.NoError(t, surface.ReviseStoreProfile(ctx, "", json.RawMessage(`{
		"legal_name":"Gobit","tax_number":"1234567890","tax_office":"Kadikoy",
		"email":"shop@example.com","address":"12 Main St","country_code":"tr"}`)))
	raw, err = surface.StoreProfileJSON(ctx)
	require.NoError(t, err)
	var read struct {
		LegalName   string    `json:"legal_name"`
		TaxNumber   string    `json:"tax_number"`
		TaxOffice   string    `json:"tax_office"`
		Email       string    `json:"email"`
		Address     string    `json:"address"`
		CountryCode string    `json:"country_code"`
		UpdatedAt   time.Time `json:"updated_at"`
	}
	require.NoError(t, json.Unmarshal(raw, &read))
	assert.Equal(t, "Gobit|1234567890|Kadikoy|shop@example.com|12 Main St|TR",
		read.LegalName+"|"+read.TaxNumber+"|"+read.TaxOffice+"|"+read.Email+"|"+read.Address+"|"+read.CountryCode)

	stamp := read.UpdatedAt.Format(time.RFC3339Nano)
	require.NoError(t, surface.ReviseStoreProfile(ctx, stamp, json.RawMessage(`{"legal_name":"Gobit Ltd","country_code":"TR"}`)))
	err = surface.ReviseStoreProfile(ctx, stamp, json.RawMessage(`{"legal_name":"Gobit","country_code":"TR"}`))
	assert.Equal(t, service.CodeProfileRevised, errors.CodeOf(err), "a stale moment: %v", err)
	err = surface.ReviseStoreProfile(ctx, "yesterday", json.RawMessage(`{"legal_name":"Gobit","country_code":"TR"}`))
	assert.True(t, errors.IsInvalid(err), "a moment that is not one: %v", err)
}
