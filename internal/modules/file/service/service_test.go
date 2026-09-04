package service_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// uploadedRecord uploads a single file and returns its record.
func uploadedRecord(t *testing.T, svc *service.Service) string {
	t.Helper()

	record, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("body"),
	})
	require.NoError(t, err)

	return record.ID
}

// TestDeleteIsIDEMPOTENT verifies that a second delete does not give an error.
//
// A delete is an END STATE claim ("this upload no longer exists"). The second
// call returning 404 would count as an error a cleanup flow that is retried
// WHILE the wanted end state HOLDS — that is, it would make the very thing it
// has to clean up impossible to clean up.
func TestDeleteIsIDEMPOTENT(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	prov := &fakeProvider{}
	svc := newService(t, store, prov)
	id := uploadedRecord(t, svc)
	ctx := context.Background()

	require.NoError(t, svc.DeleteUpload(ctx, id), "the first delete")
	require.NoError(t, svc.DeleteUpload(ctx, id), "the SECOND delete must not give an error either")
	require.NoError(t, svc.DeleteUpload(ctx, "upl_NEVEREXISTED"), "an identifier that never existed")

	assert.Zero(t, store.count())
	assert.Len(t, prov.deletedKeys(), 1,
		"since no file is left to delete on the second round, the provider must not be gone to")
}

// TestDeleteTakesTheFILEFirstAndTheRECORDSecond verifies that the order is the
// converging side.
//
// The two sides are in separate systems and cannot be taken into a single
// transaction; all that is left is the order. If the store delete blows up THE
// RECORD MUST NOT BE DELETED EITHER: once the record is gone nobody is left who
// knows the file's key and that file would be unreachable garbage. In this
// order, on the other hand, a retry closes everything.
func TestDeleteTakesTheFILEFirstAndTheRECORDSecond(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	prov := &fakeProvider{deleteErr: coreerrors.Unavailable("disk_down", "the disk could not be reached")}
	svc := newService(t, store, prov)
	id := uploadedRecord(t, svc)

	err := svc.DeleteUpload(context.Background(), id)

	require.Error(t, err)
	assert.Equal(t, 1, store.count(),
		"if the file could not be deleted the record must NOT be deleted either; otherwise the file would be unreachable garbage")

	// Once the store recovers, the same call finishes the job: that is the
	// convergence claim.
	prov.deleteErr = nil
	require.NoError(t, svc.DeleteUpload(context.Background(), id))
	assert.Zero(t, store.count())
}

// TestDeleteUsesTheRECORDSProvider verifies that the old files can be deleted
// even if the configuration changes.
//
// The day the installation moves to an object store, the old records still sit
// on the local disk. Had the provider configured at that moment been asked, the
// delete call would delete a key that does not exist in the wrong store and the
// real file would stay forever — and, because of the idempotent delete, without
// giving an error at all.
func TestDeleteUsesTheRECORDSProvider(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	older := &fakeProvider{id: "old"}
	newer := &fakeProvider{id: "new"}

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(older))
	require.NoError(t, registry.Register(newer))

	// The service uploads with "new", but in the ledger there is a record
	// written with "old"; the delete has to find it.
	svc, err := service.New(service.Options{
		Store:          store,
		Providers:      registry,
		ProviderID:     newer.ID(),
		MaxUploadBytes: testMaxBytes,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, err)

	oldRecord, err := store.CreateUpload(context.Background(), newStoredRecord("old"))
	require.NoError(t, err)

	require.NoError(t, svc.DeleteUpload(context.Background(), oldRecord.ID))

	assert.Equal(t, []string{"OLD_KEY.png"}, older.deletedKeys(),
		"the file must be deleted from the provider that WROTE it")
	assert.Empty(t, newer.deletedKeys(), "the configured provider must not be gone to at all")
}

// TestServingGivesTheSTOREDType verifies that the serving path reads from the
// record.
//
// The type the client reported while uploading is stored nowhere; the type
// served is always the one detected FROM THE CONTENT at upload time.
func TestServingGivesTheSTOREDType(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	prov := &fakeOpenableProvider{
		fakeProvider: &fakeProvider{id: "openable"},
		content:      "raw bytes",
	}

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{
		Store:          store,
		Providers:      registry,
		ProviderID:     prov.ID(),
		MaxUploadBytes: testMaxBytes,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, err)

	uploaded, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("body"),
	})
	require.NoError(t, err)

	opened, err := svc.OpenByKey(context.Background(), uploaded.StorageKey)
	require.NoError(t, err)
	defer func() { _ = opened.Content.Close() }()

	assert.Equal(t, coreprovider.ContentTypePNG, opened.Upload.ContentType)

	got, err := io.ReadAll(opened.Content)
	require.NoError(t, err)
	assert.Equal(t, "raw bytes", string(got))
}

// TestServingLooksAtTheLEDGERFirst verifies that the store is not gone to at all
// for a key that has no record.
//
// This is the structure carrying the "the only things served are uploaded files"
// claim: the endpoint is not a "read a file" endpoint, it is a "serve this
// record" endpoint, and a key not written into the ledger cannot reach the
// store.
func TestServingLooksAtTheLEDGERFirst(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	prov := &fakeOpenableProvider{
		fakeProvider: &fakeProvider{id: "openable"},
		content:      "secret",
	}

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{
		Store:          store,
		Providers:      registry,
		ProviderID:     prov.ID(),
		MaxUploadBytes: testMaxBytes,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, err)

	_, err = svc.OpenByKey(context.Background(), "../../etc/passwd")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
}

// TestAnUnservableProviderReturnsNOTFOUND pins what the serving path says in an
// installation that writes into an object store.
//
// Saying "not implemented" (500) would be wrong: from the client's point of view
// there really is nothing at that address — the file's real address is on the
// CDN.
func TestAnUnservableProviderReturnsNOTFOUND(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := newService(t, store, &fakeProvider{id: "fake"})

	uploaded, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("body"),
	})
	require.NoError(t, err)

	_, err = svc.OpenByKey(context.Background(), uploaded.StorageKey)

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
	assert.Equal(t, service.CodeNotServable, coreerrors.CodeOf(err))
}

// TestListPaginationBoundsAreEnforced pins the pagination validation.
func TestListPaginationBoundsAreEnforced(t *testing.T) {
	t.Parallel()

	svc := newService(t, newFakeStore(), &fakeProvider{})
	ctx := context.Background()

	tests := map[string]service.Page{
		"negative limit":  {Limit: -1},
		"negative offset": {Offset: -1},
		"excessive limit": {Limit: service.MaxLimit + 1},
	}

	for name, page := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.ListUploads(ctx, page)

			require.Error(t, err)
			assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
		})
	}

	_, _, err := svc.ListUploads(ctx, service.Page{})
	require.NoError(t, err, "an empty page must fall back to the default")
}

// TestRegistryASecondRegistrationWithTheSameIDConflictsAndKeepsTheExisting
// verifies that overwriting silently is refused.
//
// In the file module the price of that is concrete: the provider identifier is
// written INTO THE RECORDS and the only thing able to read a file is the
// provider that wrote it. Had the registration order been able to change, the
// files written yesterday would become unreadable today.
func TestRegistryASecondRegistrationWithTheSameIDConflictsAndKeepsTheExisting(t *testing.T) {
	t.Parallel()

	registry := service.NewProviderRegistry()
	first := &fakeProvider{id: "local"}
	second := &fakeProvider{id: "local"}

	require.NoError(t, registry.Register(first))
	err := registry.Register(second)

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "error: %v", err)
	assert.Equal(t, service.CodeProviderExists, coreerrors.CodeOf(err))

	resolved, getErr := registry.Get("local")
	require.NoError(t, getErr)
	assert.Same(t, first, resolved, "the existing provider MUST BE PRESERVED")
}

// TestRegistryAnUnknownIDGivesADiagnosableError verifies that the setup fault is
// readable (ADR 0002).
func TestRegistryAnUnknownIDGivesADiagnosableError(t *testing.T) {
	t.Parallel()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(&fakeProvider{id: "local"}))

	_, err := registry.Get("s3")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
	assert.Contains(t, err.Error(), "s3", "the sought identifier must be written")
	assert.Contains(t, err.Error(), "local", "the registered identifiers must be written")
}
