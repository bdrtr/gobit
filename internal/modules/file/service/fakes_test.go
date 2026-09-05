package service_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// The fakes are here so that the service's DECISIONS can be tested without a
// real disk and a real database. What is tested is not what the service does but
// WHAT IT DECIDES: in which order it calls, what it rejects, what it cleans up.

// fakeProvider is a file provider that records what was uploaded.
type fakeProvider struct {
	mu sync.Mutex

	// id is the provider's identifier.
	id string
	// uploaded are the bodies that were read.
	uploaded []string
	// deleted are the keys of the Delete calls; the ORDER is preserved.
	deleted []string
	// uploadErr, when given, is the error Upload returns.
	uploadErr error
	// deleteErr, when given, is the error Delete returns.
	deleteErr error
}

// That fakeProvider satisfies the core contract is pinned at compile time.
var _ coreprovider.FileProvider = (*fakeProvider)(nil)

// ID returns the provider's identifier.
func (p *fakeProvider) ID() string {
	if p.id == "" {
		return "fake"
	}

	return p.id
}

// Upload reads the body into memory and returns a fake file record.
//
// The body is REALLY read: the service's digest and size bound chains only work
// if the bytes flow, and a fake that did not read would make those chains
// untestable.
func (p *fakeProvider) Upload(_ context.Context, in coreprovider.UploadInput) (coreprovider.File, error) {
	if p.uploadErr != nil {
		return coreprovider.File{}, p.uploadErr
	}

	raw, err := io.ReadAll(in.Body)
	if err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindInternal, "fake_write",
			"the fake provider could not read the body")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.uploaded = append(p.uploaded, string(raw))

	return coreprovider.File{
		Key:         "KEY" + string(rune('0'+len(p.uploaded))) + ".png",
		URL:         "/files/KEY" + string(rune('0'+len(p.uploaded))) + ".png",
		ContentType: in.ContentType,
		Size:        int64(len(raw)),
	}, nil
}

// Delete records the deleted key.
func (p *fakeProvider) Delete(_ context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.deleted = append(p.deleted, key)

	return p.deleteErr
}

// deletedKeys returns the recorded delete keys.
func (p *fakeProvider) deletedKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.deleted...)
}

// fakeOpenableProvider is the fake that satisfies the read surface too.
//
// Its being a SEPARATE type is deliberate: the service understands whether the
// provider supports reading or not BY A TYPE ASSERTION, and without two separate
// fakes the two directions of that branch could not be tested.
type fakeOpenableProvider struct {
	*fakeProvider

	// content is the body Open will return.
	content string
}

// Open opens the file for reading.
func (p *fakeOpenableProvider) Open(
	_ context.Context, _ string,
) (io.ReadSeekCloser, time.Time, error) {
	return nopCloser{strings.NewReader(p.content)}, time.Unix(0, 0).UTC(), nil
}

// nopCloser adds an empty Close to a reader.
type nopCloser struct {
	*strings.Reader
}

// Close satisfies io.Closer and does nothing.
func (nopCloser) Close() error { return nil }

// fakeStore is the in-memory counterpart of the upload ledger.
type fakeStore struct {
	mu sync.Mutex

	// records are the uploads by identifier.
	records map[string]models.Upload
	// order is the insertion order; listing uses this.
	order []string
	// writeErr, when given, is the error CreateUpload returns.
	writeErr error
	// deleteErr, when given, is the error DeleteUpload returns.
	deleteErr error
}

// That fakeStore satisfies the surface the service expects is pinned at compile
// time.
var _ service.Store = (*fakeStore)(nil)

// newFakeStore produces an empty fake store.
func newFakeStore() *fakeStore {
	return &fakeStore{records: make(map[string]models.Upload)}
}

// CreateUpload writes the record into memory.
func (s *fakeStore) CreateUpload(_ context.Context, u models.Upload) (models.Upload, error) {
	if s.writeErr != nil {
		return models.Upload{}, s.writeErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u.CreatedAt = time.Unix(0, 0).UTC()
	u.UpdatedAt = u.CreatedAt
	s.records[u.ID] = u
	s.order = append(s.order, u.ID)

	return u, nil
}

// GetUpload returns the record by its identifier.
func (s *fakeStore) GetUpload(_ context.Context, id string) (models.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.records[id]
	if !ok {
		return models.Upload{}, coreerrors.NotFound("file_upload_not_found",
			"the upload could not be found (id: %s)", id)
	}

	return u, nil
}

// GetUploadByKey returns the record by its storage key.
func (s *fakeStore) GetUploadByKey(_ context.Context, key string) (models.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range s.order {
		if s.records[id].StorageKey == key {
			return s.records[id], nil
		}
	}

	return models.Upload{}, coreerrors.NotFound("file_upload_not_found",
		"the upload could not be found (key: %s)", key)
}

// ListUploads paginates the records.
func (s *fakeStore) ListUploads(
	_ context.Context, filter models.UploadFilter,
) ([]models.Upload, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]models.Upload, 0, len(s.order))
	for i, id := range s.order {
		if int64(i) < filter.Offset || int64(len(out)) >= filter.Limit {
			continue
		}
		out = append(out, s.records[id])
	}

	return out, int64(len(s.order)), nil
}

// DeleteUpload deletes the record; an identifier that does not exist is not an
// error.
func (s *fakeStore) DeleteUpload(_ context.Context, id string) (bool, error) {
	if s.deleteErr != nil {
		return false, s.deleteErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.records[id]; !ok {
		return false, nil
	}

	delete(s.records, id)
	s.order = removeFromSlice(s.order, id)

	return true, nil
}

// count returns the number of records in the ledger.
func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.records)
}

// removeFromSlice takes a value out of the slice.
func removeFromSlice(slice []string, value string) []string {
	out := slice[:0]
	for _, v := range slice {
		if v != value {
			out = append(out, v)
		}
	}

	return out
}

// newStoredRecord produces a fake upload record belonging to a particular
// provider.
//
// It is written straight into the store: the aim is to test how a record the
// service DID NOT UPLOAD (that is, one formed under another configuration) is
// handled.
func newStoredRecord(provider string) models.Upload {
	return models.Upload{
		ID:          "upl_OLD",
		StorageKey:  "OLD_KEY.png",
		ProviderID:  provider,
		ContentType: coreprovider.ContentTypePNG,
		Size:        5,
		Checksum:    "abc",
		URL:         "/files/OLD_KEY.png",
	}
}
