package service_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// fakeUploads is the in-memory counterpart of [service.UploadReader].
//
// It carries the THREE answers the real surface can give, because the service
// treats each one differently and a fake that could only say yes or no would
// leave two of the three branches untested:
//
//   - a record: the id names an upload;
//   - errors.NotFound: the id names none;
//   - a nil body with a nil error: the file module is not installed at all.
type fakeUploads struct {
	mu sync.Mutex
	// known are the upload ids that exist.
	known map[string]bool
	// absent makes every answer the "there is no file module" one.
	absent bool
	// err, when set, is returned by every call.
	err error
	// asked records the ids that were asked about, IN ORDER. It is the evidence
	// for the claim that the same upload is not read twice.
	asked []string
}

// That the fake satisfies the surface the service expects is pinned at compile
// time; the real one lives in another module and cannot be imported.
var _ service.UploadReader = (*fakeUploads)(nil)

// newFakeUploads builds a reader that knows the given upload ids.
func newFakeUploads(ids ...string) *fakeUploads {
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	return &fakeUploads{known: known}
}

// UploadJSON answers for the given id.
func (f *fakeUploads) UploadJSON(_ context.Context, uploadID string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, uploadID)

	switch {
	case f.err != nil:
		return nil, f.err
	case f.absent:
		return nil, nil
	case !f.known[uploadID]:
		return nil, errors.NotFound("file_upload_not_found", "no such upload: %s", uploadID)
	}
	return json.RawMessage(`{"id":"` + uploadID + `","url":"/files/K.png"}`), nil
}

// askedIDs returns the ids the service asked about.
func (f *fakeUploads) askedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

// newServiceWithUploads builds a service wired to a fake upload reader.
func newServiceWithUploads(
	t *testing.T, store *memStore, links service.Linker, uploads service.UploadReader,
) *service.Service {
	t.Helper()
	if fake, ok := links.(*fakeLinker); ok && store != nil {
		store.links = fake
	}
	svc, err := service.New(service.Options{Repo: store, Links: links, Uploads: uploads})
	require.NoError(t, err)
	return svc
}

// productWithUpload creates a product carrying one image made from uploadID.
func productWithUpload(
	t *testing.T, svc *service.Service, handle, uploadID string,
) models.Product {
	t.Helper()
	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: handle,
		Title:  "Shirt",
		Status: models.StatusPublished,
		Images: []service.CreateImageInput{
			{URL: "https://cdn.example/1.png", UploadID: uploadID},
		},
	})
	require.NoError(t, err)
	return product
}

// TestTheImageUploadBindingSURVIVESAROUNDTRIP verifies that an image written
// with an upload id comes back carrying it, and that the reverse direction can
// be read as well.
//
// This is the question the whole change exists to answer. The two halves are
// asserted TOGETHER on purpose: the column alone would answer "which upload is
// this image" and leave "which images use this upload" unanswerable outside
// this module, while the link alone would answer the reverse and make every
// catalog read pay for a join it does not need.
func TestTheImageUploadBindingSURVIVESAROUNDTRIP(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))

	created := productWithUpload(t, svc, "shirt", "upl_1")
	require.Len(t, created.Images, 1)

	// The record that comes back from the create.
	require.NotNil(t, created.Images[0].UploadID, "the created image must carry its upload")
	assert.Equal(t, "upl_1", *created.Images[0].UploadID)

	// The record that comes back from a SEPARATE read: the create returns what
	// GetProduct assembled, so a field lost between the write and the read would
	// be invisible if only the create's return value were checked.
	read, err := svc.GetProduct(context.Background(), created.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 1)
	require.NotNil(t, read.Images[0].UploadID)
	assert.Equal(t, "upl_1", *read.Images[0].UploadID)

	// The reverse direction: from the upload to the images made from it.
	assert.Equal(t, []string{created.Images[0].ID},
		links.linked(service.LinkUploadProductImage, "upl_1"),
		"the binding must be readable from the upload's side, which is the side that cannot see product_image")
}

// TestAnImageWithoutAnUploadBindsNothing verifies that the old shape still
// works untouched.
//
// An address that was never uploaded here — an imported catalog, a hand-typed
// CDN address — is a legitimate image and must not be forced to invent an
// upload. Neither the file module nor the link service is to be touched for it;
// had the service asked anyway, every installation without a file module would
// pay for a read that can only fail.
func TestAnImageWithoutAnUploadBindsNothing(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	uploads := newFakeUploads()
	svc := newServiceWithUploads(t, newMemStore(), links, uploads)

	created := productWithUpload(t, svc, "shirt", "")
	require.Len(t, created.Images, 1)

	assert.Nil(t, created.Images[0].UploadID, "no upload was named, so none is recorded")
	assert.Empty(t, uploads.askedIDs(), "the file module must not be asked about an image that names no upload")
	assert.Empty(t, links.linked(service.LinkUploadProductImage, ""), "nothing may be bound to an empty id")
}

// TestAnUnknownUploadIsREJECTEDAndNothingIsWritten verifies that a dangling
// binding cannot be created.
//
// Nothing else can catch this: the database cannot (a cross-module foreign key
// is banned) and the link service cannot (it sees the ends as free-form
// strings). If the write went through, the fault would surface only at the far
// end — in the caller that wanted the file behind the image and got nothing —
// where it can no longer be corrected.
func TestAnUnknownUploadIsREJECTEDAndNothingIsWritten(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt",
		Title:  "Shirt",
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png", UploadID: "upl_TYPO"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a bad id in the request body is the CLIENT's error, got: %v", err)

	assert.Zero(t, store.callCount("InTx"), "the product must not be written at all")
	assert.Empty(t, links.linked(service.LinkUploadProductImage, "upl_TYPO"),
		"no binding may be left behind for an upload that does not exist")
}

// TestAnAbsentFileModuleRecordsTheIdUNVERIFIED verifies the third answer of the
// read-back.
//
// gobit is a library and its modules are chosen one by one (ADR 0025); an
// installation that stores its files elsewhere still has to be able to record
// the foreign system's id. Treating "there is no file module" the same way as
// "there is no such upload" would reject every one of those ids.
func TestAnAbsentFileModuleRecordsTheIdUNVERIFIED(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	uploads := newFakeUploads()
	uploads.absent = true
	svc := newServiceWithUploads(t, newMemStore(), links, uploads)

	created := productWithUpload(t, svc, "shirt", "upl_FOREIGN")

	require.NotNil(t, created.Images[0].UploadID)
	assert.Equal(t, "upl_FOREIGN", *created.Images[0].UploadID)
	assert.Equal(t, []string{created.Images[0].ID},
		links.linked(service.LinkUploadProductImage, "upl_FOREIGN"),
		"the binding is still written; it is the VERIFICATION that is missing, not the relation")
}

// TestABrokenReadBackFailsTheWrite verifies that an error from the file module
// is not read as "unverified".
//
// The distinction is the point: "there is no file module" is a setup decision,
// while "the file module answered with an error" is a fault. Falling back to
// recording the id unchecked would skip the check on exactly the day something
// is already wrong.
func TestABrokenReadBackFailsTheWrite(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	store := newMemStore()
	uploads := newFakeUploads("upl_1")
	uploads.err = errors.Internal("file_broken", "the file module is having a bad day")
	svc := newServiceWithUploads(t, store, links, uploads)

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt",
		Title:  "Shirt",
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png", UploadID: "upl_1"}},
	})
	require.Error(t, err)
	assert.False(t, errors.IsInvalid(err), "a broken dependency is not the client's fault, got: %v", err)
	assert.Zero(t, store.callCount("InTx"), "nothing may be written while the id is unchecked")
}

// TestTheSameUploadIsReadONCE verifies that a product using one file for
// several images does not produce one cross-module read per image.
//
// The read leaves this module; repeating it per image would turn a create with
// eight images of one photograph into eight calls that can only give the same
// answer.
func TestTheSameUploadIsReadONCE(t *testing.T) {
	t.Parallel()

	uploads := newFakeUploads("upl_1")
	svc := newServiceWithUploads(t, newMemStore(), newFakeLinker(), uploads)

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt",
		Title:  "Shirt",
		Images: []service.CreateImageInput{
			{URL: "https://cdn.example/1.png", UploadID: "upl_1"},
			{URL: "https://cdn.example/2.png", UploadID: "upl_1"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"upl_1"}, uploads.askedIDs(),
		"the same upload must be asked about once, however many images point at it")
}

// TestTheBindingIsWrittenBEFORETheImage verifies the write order.
//
// The image row goes in inside a transaction the link table cannot join, so one
// of the two writes has to go first. In this order the only possible residue is
// a link row for an image that was never committed — harmless, because ids are
// never reused. The reverse order would leave the opposite residue: a committed
// image whose binding is missing, which reads perfectly well and makes the
// reverse question answer "nobody uses this upload" about a file that is on a
// product page.
//
// The fake repository does NOT roll back (it is a map, not a database), so what
// is asserted here is the ORDER — that the link was already written by the time
// the image write was attempted — not the rollback itself.
func TestTheBindingIsWrittenBEFORETheImage(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	store := newMemStore()
	store.fail("CreateImage", errors.Internal("boom", "the image could not be written"))
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt",
		Title:  "Shirt",
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png", UploadID: "upl_1"}},
	})
	require.Error(t, err)

	assert.Len(t, links.linked(service.LinkUploadProductImage, "upl_1"), 1,
		"the binding must already be in place when the image write is attempted")
}

// TestAFailedBindingWritesNOTHING verifies that the create stops before the
// transaction when the link cannot be created.
//
// This is the other half of the write order: because the link goes first, its
// failure can still abort the request with nothing written — as opposed to
// leaving a committed image whose binding silently never happened.
func TestAFailedBindingWritesNOTHING(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	links.createErr = errors.Conflict("link_cardinality_violation", "already bound")
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt",
		Title:  "Shirt",
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png", UploadID: "upl_1"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the link service's kind must be preserved, got: %v", err)
	assert.Zero(t, store.callCount("InTx"), "no product may be written when its image cannot be bound")
}

// TestAServiceWithoutALinkerRefusesAnImageThatNamesAnUpload verifies that the
// id is not quietly recorded half-bound.
//
// The request explicitly asked for a binding. Writing the image anyway would
// drop the caller's id from the half of the binding other modules read, and
// drop it without a word — the same silent loss the link cleanups are written
// to avoid. An image that names NO upload is unaffected; that path never
// touches the link service.
func TestAServiceWithoutALinkerRefusesAnImageThatNamesAnUpload(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newServiceWithUploads(t, store, nil, newFakeUploads("upl_1"))

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt",
		Title:  "Shirt",
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png", UploadID: "upl_1"}},
	})
	require.Error(t, err)
	assert.Equal(t, "product_service_not_ready", errors.CodeOf(err))
	assert.Zero(t, store.callCount("InTx"))

	// The same service writes an image that names no upload without complaint.
	_, err = svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt-2",
		Title:  "Shirt",
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png"}},
	})
	require.NoError(t, err)
}

// TestDeletingAProductCleansUpItsImageBindings verifies that the reverse read
// stops naming images nobody can see any more.
//
// The reverse direction exists so that a cleanup can ask whether an upload is
// still in use. A binding left behind by a deleted product would answer "yes"
// forever, and the file it protects could never be removed.
func TestDeletingAProductCleansUpItsImageBindings(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))

	created := productWithUpload(t, svc, "shirt", "upl_1")
	require.Len(t, links.linked(service.LinkUploadProductImage, "upl_1"), 1)

	require.NoError(t, svc.DeleteProduct(context.Background(), created.ID))

	assert.Empty(t, links.linked(service.LinkUploadProductImage, "upl_1"),
		"the deleted product's images must not go on claiming the upload")
}

// TestImagesOfUploadReadsTheBindingBackwards verifies the reverse direction end
// to end.
//
// This is the question the link exists for and the column cannot answer outside
// this module: the file module can see neither product_image nor its columns,
// while the link table is the core's and is readable by anyone holding it.
func TestImagesOfUploadReadsTheBindingBackwards(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))

	first := productWithUpload(t, svc, "shirt", "upl_1")
	second := productWithUpload(t, svc, "mug", "upl_1")

	images, err := svc.ImagesOfUpload(context.Background(), "upl_1")
	require.NoError(t, err)
	require.Len(t, images, 2, "one file may be used by several products; that is why the cardinality is OneToMany")

	ids := []string{images[0].ID, images[1].ID}
	assert.ElementsMatch(t, []string{first.Images[0].ID, second.Images[0].ID}, ids)
	for _, img := range images {
		require.NotNil(t, img.UploadID)
		assert.Equal(t, "upl_1", *img.UploadID,
			"the record read backwards must agree with the column read forwards")
	}
}

// TestImagesOfUploadSkipsAnImageThatIsNotThere verifies that the residue of the
// write order does not turn into an error.
//
// A binding whose image was never committed is the ONE inconsistency the write
// order allows (see the link's godoc). The reverse read has to treat it as
// "there is nothing to show" — reporting a missing record would turn a harmless
// orphan into a broken endpoint that no cleanup can fix.
func TestImagesOfUploadSkipsAnImageThatIsNotThere(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))

	require.NoError(t, links.Create(context.Background(),
		service.LinkUploadProductImage, "upl_1", "pimg_NEVER_WRITTEN"))

	images, err := svc.ImagesOfUpload(context.Background(), "upl_1")
	require.NoError(t, err)
	assert.Empty(t, images)
}

// TestImagesOfUploadDoesNotClaimTheUploadExists verifies the meaning of the
// empty answer.
//
// An id belonging to no upload at all and an upload nobody uses give the SAME
// answer here, and they have to: this module cannot see the file module's
// records, so a 404 would be a statement it has no way to make.
func TestImagesOfUploadDoesNotClaimTheUploadExists(t *testing.T) {
	t.Parallel()

	svc := newServiceWithUploads(t, newMemStore(), newFakeLinker(), newFakeUploads())

	images, err := svc.ImagesOfUpload(context.Background(), "upl_NOTHING")
	require.NoError(t, err)
	assert.Empty(t, images)
}

// TestAnUploadIdWithWhitespaceIsREJECTED verifies that the id is not corrected
// silently.
//
// A trimmed id would land on one row in the image table and on a different one
// in the link table; the two halves of the binding would then name different
// records and nothing would report it. The rule is the repository's own (see
// requireID): ids are rejected, not repaired.
func TestAnUploadIdWithWhitespaceIsREJECTED(t *testing.T) {
	t.Parallel()

	svc := newServiceWithUploads(t, newMemStore(), newFakeLinker(), newFakeUploads(" upl_1 "))

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt",
		Title:  "Shirt",
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png", UploadID: " upl_1 "}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "got: %v", err)
}
