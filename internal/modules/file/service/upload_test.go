package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// testMaxBytes is the size bound used in the unit tests; its being small makes a
// body that exceeds the bound constructible in a single line.
const testMaxBytes int64 = 32

// newService sets up a service working over a fake store and a fake provider.
func newService(t *testing.T, store *fakeStore, prov coreprovider.FileProvider) *service.Service {
	t.Helper()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{
		Store:          store,
		Providers:      registry,
		ProviderID:     prov.ID(),
		MaxUploadBytes: testMaxBytes,
		AllowedTypes: []string{
			coreprovider.ContentTypePNG,
			coreprovider.ContentTypeJPEG,
		},
	})
	require.NoError(t, err)

	return svc
}

// TestTheUploadRecordStoresTheDETECTEDType pins what the ledger writes.
func TestTheUploadRecordStoresTheDETECTEDType(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	prov := &fakeProvider{id: "fake"}
	svc := newService(t, store, prov)

	record, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType:  coreprovider.ContentTypePNG,
		Body:         strings.NewReader("body"),
		OriginalName: "product.png",
		UploadedBy:   "user_1",
	})

	require.NoError(t, err)
	assert.Equal(t, coreprovider.ContentTypePNG, record.ContentType)
	assert.Equal(t, "fake", record.ProviderID)
	assert.Equal(t, "product.png", record.OriginalName)
	assert.Equal(t, "user_1", record.UploadedBy)
	assert.NotEmpty(t, record.StorageKey)
	assert.NotEmpty(t, record.URL)

	digest := sha256.Sum256([]byte("body"))
	assert.Equal(t, hex.EncodeToString(digest[:]), record.Checksum,
		"the digest must be computed over the bytes FLOWING to the provider")
	assert.Equal(t, int64(len("body")), record.Size)
	assert.Equal(t, []string{"body"}, prov.uploaded, "the body must reach the provider in full")
}

// TestATypeOUTSIDETheAllowListIsRejected verifies that an allow list is used
// instead of a deny list.
//
// The real claim is NOT the rejection, it is WHEN the rejection happens: the
// provider must NOT be gone to at all. Had the check been done after the write,
// every rejected file would need a delete call, and when that delete failed the
// file would stay in the store.
func TestATypeOUTSIDETheAllowListIsRejected(t *testing.T) {
	t.Parallel()

	types := map[string]string{
		// DetectContentType returns "text/xml" or "text/plain" for an SVG; both
		// names are tested all the same — the allow list recognizes neither and
		// no path must be left through which an SVG could pass.
		"SVG (declared name)": "image/svg+xml",
		"SVG (detected)":      "text/xml",
		"text":                "text/plain",
		"HTML":                "text/html",
		"unknown binary":      "application/octet-stream",
		"not in allow list":   coreprovider.ContentTypeGIF,
		"could not detect":    "",
		"png with parameter":  "image/png; charset=utf-8",
		"uppercase png":       "IMAGE/PNG",
	}

	for name, contentType := range types {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			prov := &fakeProvider{}
			svc := newService(t, store, prov)

			_, err := svc.Upload(context.Background(), service.UploadInput{
				ContentType: contentType,
				Body:        strings.NewReader("content"),
			})

			require.Error(t, err, "%q must not be accepted", contentType)
			assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
			assert.Empty(t, prov.uploaded, "the provider must NOT be gone to at all")
			assert.Zero(t, store.count(), "no record must be written into the ledger")
		})
	}
}

// TestTheSVGRejectionIsDiagnosableByCode verifies that the rejection is
// machine-readable.
//
// This is the only path the client can see: the status code is 422 and 422 comes
// from a great many reasons; the answer to "which formats are accepted" has to
// be written into the code and the message.
func TestTheSVGRejectionIsDiagnosableByCode(t *testing.T) {
	t.Parallel()

	svc := newService(t, newFakeStore(), &fakeProvider{})

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: "image/svg+xml",
		Body:        strings.NewReader("<svg onload=\"alert(1)\"/>"),
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeTypeNotAllowed, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "image/svg+xml", "the rejected type must be written")
	assert.Contains(t, err.Error(), coreprovider.ContentTypePNG, "the accepted ones must be written")
}

// TestABodyExceedingTheSizeBoundIsRejected verifies that the bound is really
// enforced.
//
// The bound is applied ON THE STREAM: the length of the body is not known
// beforehand (there is no Content-Length in a chunked request) and it cannot be
// rejected without being counted.
func TestABodyExceedingTheSizeBoundIsRejected(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	prov := &fakeProvider{}
	svc := newService(t, store, prov)

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(strings.Repeat("A", int(testMaxBytes)+1)),
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	assert.Equal(t, service.CodeTooLarge, coreerrors.CodeOf(err))
	assert.Zero(t, store.count(), "an upload exceeding the bound must NOT ENTER the ledger")
}

// TestABodyExactlyAtTheBoundIsAccepted verifies that the bound rejects when it
// is "exceeded", not when it is "reached".
//
// Had the counter been started one short, every file exactly at the bound would
// be rejected and the bound would be one byte smaller than what the
// documentation says — a drift nobody would notice, but one that makes the
// documentation a liar.
func TestABodyExactlyAtTheBoundIsAccepted(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := newService(t, store, &fakeProvider{})

	record, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(strings.Repeat("A", int(testMaxBytes))),
	})

	require.NoError(t, err)
	assert.Equal(t, testMaxBytes, record.Size)
}

// TestTheBODYIsNOTTRUNCATEDWhenTheBoundIsExceeded verifies that the bound does
// not silently clip.
//
// Had io.LimitReader been used, io.EOF would be returned once the bound was
// reached: the provider reads that as "the file has ended" and a HALF image
// would be recorded successfully. That is, a request exceeding the bound would
// produce corrupt data instead of being rejected. The claim is that the provider
// has not recorded a file.
func TestTheBODYIsNOTTRUNCATEDWhenTheBoundIsExceeded(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{}
	svc := newService(t, newFakeStore(), prov)

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(strings.Repeat("A", int(testMaxBytes)*4)),
	})

	require.Error(t, err)
	assert.Empty(t, prov.uploaded, "a truncated body must not be recorded SUCCESSFULLY")
}

// TestALongFileNameIsRejectedAndNOTTRUNCATED verifies that the name is not
// silently changed.
//
// Truncating would be changing the data the client sent without telling them;
// the field's only job is anyway "to show what the user saw".
func TestALongFileNameIsRejectedAndNOTTRUNCATED(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := newService(t, store, &fakeProvider{})

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType:  coreprovider.ContentTypePNG,
		Body:         strings.NewReader("b"),
		OriginalName: strings.Repeat("a", 256),
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	assert.Zero(t, store.count())
}

// TestAPathLikeFileNameDoesNOTREACHTheProvider verifies that the client's name
// does not affect the storage key.
//
// The claim is structural: the UploadInput in the core contract HAS NO file name
// FIELD, so there is no channel through which the name could reach the provider
// either. The test makes that observable — the name is written into the ledger
// but never appears in the key and in the address.
func TestAPathLikeFileNameDoesNOTREACHTheProvider(t *testing.T) {
	t.Parallel()

	const badName = "../../etc/passwd"

	store := newFakeStore()
	prov := &fakeProvider{}
	svc := newService(t, store, prov)

	record, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType:  coreprovider.ContentTypePNG,
		Body:         strings.NewReader("body"),
		OriginalName: badName,
	})

	require.NoError(t, err, "since the name is not a path the upload IS NOT REJECTED, the name is only data")
	assert.Equal(t, badName, record.OriginalName, "the name is stored AS IS, for display")
	assert.NotContains(t, record.StorageKey, "..", "the key IS NOT DERIVED from the client's name")
	assert.NotContains(t, record.StorageKey, "/")
	assert.NotContains(t, record.URL, "passwd")
}

// TestTheFileIsCLEANEDUPIfTheRecordCannotBeWritten verifies that no unreachable
// object is left behind.
//
// If the record blows up after the file has been written, an object whose key
// nobody knows is left behind: it appears in no listing, no delete endpoint can
// reach it and it takes up space forever.
func TestTheFileIsCLEANEDUPIfTheRecordCannotBeWritten(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.writeErr = coreerrors.Unavailable("db_down", "the database could not be reached")
	prov := &fakeProvider{}
	svc := newService(t, store, prov)

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("body"),
	})

	require.Error(t, err)
	assert.Len(t, prov.deletedKeys(), 1, "the written file must be rolled back")
	assert.Equal(t, prov.uploaded[0], "body")
}

// TestAnEmptyBodyIsRejected verifies that a zero-byte upload is not accepted; a
// file without a body has neither a type that can be detected nor content to be
// served.
func TestAnEmptyBodyIsRejected(t *testing.T) {
	t.Parallel()

	svc := newService(t, newFakeStore(), &fakeProvider{})

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        nil,
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
}

// TestTheServiceCANNOTBeBuiltWithAnEmptyAllowList verifies that the most
// dangerous default is switched off.
//
// Had "if the list is empty, accept everything" been written, a single typo in
// the configuration would SILENTLY REMOVE the checking.
func TestTheServiceCANNOTBeBuiltWithAnEmptyAllowList(t *testing.T) {
	t.Parallel()

	_, err := service.New(service.Options{
		Store:          newFakeStore(),
		Providers:      service.NewProviderRegistry(),
		ProviderID:     "fake",
		MaxUploadBytes: testMaxBytes,
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, coreerrors.CodeOf(err))
}

// TestTheAllowListIsCOPIED verifies that the caller changing its slice cannot
// widen the list.
//
// Had the slice been kept as it is, a piece of code holding the value coming
// from the config could grow the allow list while running — a check that can be
// changed from the outside means there is no check.
func TestTheAllowListIsCOPIED(t *testing.T) {
	t.Parallel()

	types := []string{coreprovider.ContentTypePNG}

	svc, err := service.New(service.Options{
		Store:          newFakeStore(),
		Providers:      service.NewProviderRegistry(),
		ProviderID:     "fake",
		MaxUploadBytes: testMaxBytes,
		AllowedTypes:   types,
	})
	require.NoError(t, err)

	types[0] = "text/html"

	assert.Equal(t, []string{coreprovider.ContentTypePNG}, svc.AllowedTypes())
}
