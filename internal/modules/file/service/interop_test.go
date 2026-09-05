package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// newInteropService builds a service holding one record, plus the cross-module
// surface over it.
//
// The record is written STRAIGHT INTO the store rather than uploaded, so that
// every field the schema publishes has a value that is recognizable in the
// assertions: an upload through the fake provider would leave the checksum and
// the original name empty and the test could not tell "not carried" from "not
// set".
func newInteropService(t *testing.T) (*service.Interop, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(&fakeProvider{id: "local"}))

	svc, err := service.New(service.Options{
		Store:          store,
		Providers:      registry,
		ProviderID:     "local",
		MaxUploadBytes: testMaxBytes,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, err)

	return service.NewInterop(svc), store
}

// TestUploadJSONCarriesWhatTheADDRESSCannotSay verifies the fields of the
// cross-module record.
//
// This is the whole reason the surface exists: a consumer holding an upload id
// (a product image, today) has the address already and learns NOTHING from it —
// not the type the system detected, not the size, not the checksum, not the
// provider. Each of those is asserted by value, because a field silently
// dropped from the schema would leave the consumer with a decodable body that
// answers none of its questions.
func TestUploadJSONCarriesWhatTheADDRESSCannotSay(t *testing.T) {
	t.Parallel()

	interop, store := newInteropService(t)
	record := newStoredRecord("local")
	record.OriginalName = "product-red-front.png"
	record.UploadedBy = "admin_1"
	stored, err := store.CreateUpload(context.Background(), record)
	require.NoError(t, err)

	body, err := interop.UploadJSON(context.Background(), stored.ID)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))

	assert.Equal(t, stored.ID, got["id"])
	assert.Equal(t, "/files/OLD_KEY.png", got["url"])
	assert.Equal(t, coreprovider.ContentTypePNG, got["content_type"],
		"the type is the DETECTED one; a consumer deciding whether it can read the file branches on it")
	assert.InDelta(t, 5, got["size"], 0)
	assert.Equal(t, "abc", got["checksum"])
	assert.Equal(t, "local", got["provider_id"],
		"which provider holds the bytes cannot be derived from the address")
	assert.Equal(t, "product-red-front.png", got["original_name"])
	assert.Contains(t, got, "created_at")
}

// TestUploadJSONPublishesNeitherTheKeyNorTheUploader verifies the two
// deliberate omissions.
//
// The storage key is this module's internal handle on the provider: a consumer
// holding one could ask a provider for a file whose record it never read, and it
// would also be a second promise about how to reach the file, one that an object
// store signing its addresses breaks. uploaded_by is the auth module's identity,
// and no reader of "which file is behind this image" needs it.
//
// The assertion is written on the KEYS rather than on the values: a field that
// appears empty today and filled tomorrow would slip past a value check.
//
// What it deliberately does NOT assert is that the key is nowhere in the body:
// the local provider DERIVES the address from the key, so "/files/OLD_KEY.png"
// contains it and always will. That is a property of one provider's address
// shape, not a promise of this schema — which is exactly why a consumer must
// not read the key out of the URL either.
func TestUploadJSONPublishesNeitherTheKeyNorTheUploader(t *testing.T) {
	t.Parallel()

	interop, store := newInteropService(t)
	record := newStoredRecord("local")
	record.UploadedBy = "admin_1"
	stored, err := store.CreateUpload(context.Background(), record)
	require.NoError(t, err)

	body, err := interop.UploadJSON(context.Background(), stored.ID)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))

	assert.NotContains(t, got, "storage_key")
	assert.NotContains(t, got, "uploaded_by")
}

// TestUploadJSONSeparatesAnUnknownIdFromAnEmptyOne verifies the two error
// classes.
//
// The consumer branches on them: NotFound is a fact about the id the client
// sent and belongs in a validation message, while Invalid is a fault in the
// caller's own code. Collapsing them into one would make a product image write
// report a client's typo the same way it reports its own.
func TestUploadJSONSeparatesAnUnknownIdFromAnEmptyOne(t *testing.T) {
	t.Parallel()

	interop, _ := newInteropService(t)

	_, err := interop.UploadJSON(context.Background(), "upl_MISSING")
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "an id that belongs to no record is NotFound, got: %v", err)

	_, err = interop.UploadJSON(context.Background(), "")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "an empty id is a caller fault, got: %v", err)
}
