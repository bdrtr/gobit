package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/settings/models"
	"github.com/bdrtr/gobit/internal/modules/settings/service"
)

// memStore is the in-memory counterpart of service.Store.
//
// It imitates only what the DATABASE does: an absent row answers NotFound, and a
// write replaces what was there. No rule that belongs to the SERVICE is repeated
// here — it does not trim, it does not refuse an empty name — because a fake that
// enforced them would let the service's own checks be deleted with every test
// still green.
type memStore struct {
	profile *models.StoreProfile
	// writes counts the writes, so a test can tell "wrote once" from "wrote
	// twice with the same values".
	writes int
}

// GetProfile returns the stored profile or NotFound.
func (m *memStore) GetProfile(_ context.Context) (models.StoreProfile, error) {
	if m.profile == nil {
		return models.StoreProfile{}, errors.NotFound("settings_store_profile_not_found",
			"the shop has not said who it is yet")
	}

	return *m.profile, nil
}

// SetProfile replaces the stored profile.
func (m *memStore) SetProfile(
	_ context.Context, profile models.StoreProfile,
) (models.StoreProfile, error) {
	m.writes++
	stored := profile
	m.profile = &stored

	return stored, nil
}

// validProfile is a request that passes every rule.
func validProfile() service.SetProfileInput {
	return service.SetProfileInput{
		LegalName:   "Example Trading Ltd",
		TaxNumber:   "1234567890",
		TaxOffice:   "Central",
		Email:       "billing@example.com",
		Address:     "1 Example Street",
		CountryCode: "TR",
	}
}

// newService builds the service over a fresh store.
func newService(t *testing.T) (*service.Service, *memStore) {
	t.Helper()

	store := &memStore{}

	return service.New(store, service.Options{}), store
}

// TestTheShopSaysWhoItIsAndItIsReadBack is the ordinary path.
func TestTheShopSaysWhoItIsAndItIsReadBack(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)

	written, err := svc.SetProfile(ctx, validProfile())
	require.NoError(t, err)
	assert.Equal(t, "Example Trading Ltd", written.LegalName)
	assert.Equal(t, 1, store.writes)

	read, err := svc.GetProfile(ctx)
	require.NoError(t, err)
	assert.Equal(t, written, read)
}

// TestAnUnwrittenProfileIsNotFound is the answer the invoicing flow acts on.
//
// A zero value would print an issuer of nobody, which is the one thing a
// document must never carry.
func TestAnUnwrittenProfileIsNotFound(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.GetProfile(context.Background())

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestTheValuesAreTrimmedBeforeTheyAreStored keeps a name that prints as nothing
// from passing a "not empty" check.
func TestTheValuesAreTrimmedBeforeTheyAreStored(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validProfile()
	in.LegalName = "  Example Trading Ltd  "
	in.TaxNumber = " 1234567890 "
	in.Address = "\t1 Example Street\n"

	written, err := svc.SetProfile(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, "Example Trading Ltd", written.LegalName)
	assert.Equal(t, "1234567890", written.TaxNumber)
	assert.Equal(t, "1 Example Street", written.Address)
}

// TestANameOfSpacesIsNoName is the case the trim exists for.
func TestANameOfSpacesIsNoName(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)

	in := validProfile()
	in.LegalName = "   "

	_, err := svc.SetProfile(ctx, in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, store.writes, "nothing may reach the table")
}

// TestTheCountryIsAnISOCodeAndItIsUppercased pins both halves of the rule.
func TestTheCountryIsAnISOCodeAndItIsUppercased(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validProfile()
	in.CountryCode = "tr"

	written, err := svc.SetProfile(ctx, in)
	require.NoError(t, err, "a lowercase code is accepted and folded")
	assert.Equal(t, "TR", written.CountryCode)

	for name, code := range map[string]string{
		"three letters": "TUR",
		"one letter":    "T",
		"digits":        "12",
		"empty":         "",
	} {
		t.Run(name, func(t *testing.T) {
			bad := validProfile()
			bad.CountryCode = code

			_, err := svc.SetProfile(ctx, bad)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
		})
	}
}

// TestTheOptionalFieldsMayBeEmpty keeps the rule that only two are required.
//
// A shop that is not registered for tax has no number, and a country outside
// Turkey has no tax office.
func TestTheOptionalFieldsMayBeEmpty(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	written, err := svc.SetProfile(ctx, service.SetProfileInput{
		LegalName: "Example Trading Ltd", CountryCode: "DE",
	})
	require.NoError(t, err)

	assert.Empty(t, written.TaxNumber)
	assert.Empty(t, written.TaxOffice)
	assert.Empty(t, written.Email)
	assert.Empty(t, written.Address)
}

// TestAFieldPastItsBoundIsRefused stops a broken client rather than a person.
func TestAFieldPastItsBoundIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	for name, in := range map[string]service.SetProfileInput{
		"legal name": {LegalName: strings.Repeat("x", service.MaxNameLen+1), CountryCode: "TR"},
		"address": {
			LegalName: "Example", CountryCode: "TR",
			Address: strings.Repeat("x", service.MaxAddressLen+1),
		},
		"e-mail": {
			LegalName: "Example", CountryCode: "TR",
			Email: strings.Repeat("x", service.MaxEmailLen+1),
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.SetProfile(ctx, in)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
		})
	}
}

// TestTheSecondWriteReplacesTheFirst is what PUT means here.
func TestTheSecondWriteReplacesTheFirst(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	_, err := svc.SetProfile(ctx, validProfile())
	require.NoError(t, err)

	second := validProfile()
	second.LegalName = "Renamed Trading Ltd"
	second.TaxOffice = ""

	written, err := svc.SetProfile(ctx, second)
	require.NoError(t, err)

	assert.Equal(t, "Renamed Trading Ltd", written.LegalName)
	assert.Empty(t, written.TaxOffice,
		"a field left out of a PUT is CLEARED; that is the difference from a patch")
}

// TestTheInteropAnswersWithThePrintedFields is the cross-module wire.
//
// The consumer maps legal_name onto a party's name, and the two spellings are
// exactly why the schema is pinned here rather than left to field matching.
func TestTheInteropAnswersWithThePrintedFields(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	_, err := svc.SetProfile(ctx, validProfile())
	require.NoError(t, err)

	raw, err := service.NewInterop(svc).StoreProfileJSON(ctx)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"legal_name": "Example Trading Ltd",
		"tax_number": "1234567890",
		"tax_office": "Central",
		"email": "billing@example.com",
		"address": "1 Example Street",
		"country_code": "TR"
	}`, string(raw), "the wire carries the printed fields and nothing else")
}

// TestTheInteropPassesTheAbsenceThrough keeps the flow able to refuse.
func TestTheInteropPassesTheAbsenceThrough(t *testing.T) {
	svc, _ := newService(t)

	_, err := service.NewInterop(svc).StoreProfileJSON(context.Background())

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"the flow turns this into a message naming the endpoint; a zero value here "+
			"would be printed instead")
}
