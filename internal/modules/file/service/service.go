// Package service is the business logic of the file module.
//
// The module's responsibility in a single sentence: to check the ARBITRARY
// BYTES coming from the client, have them written into a store, and enter what
// was written into a durable ledger. The side that keeps the bytes is not this
// module but a PROVIDER satisfying the FileProvider contract in the core.
//
// # WHERE the checking is done
//
// The content type is detected FROM THE CONTENT and that detection is done in
// the HTTP layer — that is the only place able to read the first bytes. The
// allow list, on the other hand, is applied HERE and BEFORE going to the
// provider: a file rejected without a single byte being written into the store
// does not have to be cleaned up. Leaving the checking to the provider would
// have meant every provider writing the same rule again and one of them
// forgetting it.
//
// # Module isolation
//
// This module knows no other module (Principles 2.1/2.4, ADR 0001).
// [models.Upload.UploadedBy] is a user or API key identifier; it is stored as
// free text and its existence is not validated here.
package service

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// Error codes. Clients may branch on these; the messages may change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "file_invalid_input"
	// CodeProviderNotFound reports that the selected provider is not
	// registered.
	CodeProviderNotFound = "file_provider_not_found"
	// CodeProviderExists reports that a second provider with the same
	// identifier was asked to be registered.
	CodeProviderExists = "file_provider_already_registered"
	// CodeTypeNotAllowed reports that the detected content type is not in the
	// allow list.
	CodeTypeNotAllowed = "file_content_type_not_allowed"
	// CodeTooLarge reports that the body exceeded the maximum size.
	CodeTooLarge = "file_upload_too_large"
	// CodeUploadFailed reports that the provider could not complete the write.
	CodeUploadFailed = "file_upload_failed"
	// CodeNotServable reports that the record's provider does not support
	// reading files.
	CodeNotServable = "file_not_servable"
	// CodeNotReady reports that the service was built with a missing
	// dependency.
	CodeNotReady = "file_service_not_ready"
)

// Pagination bounds (plan Section 8: limit/offset).
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that may be asked for in a single
	// request.
	MaxLimit int64 = 100
)

// Store is the persistence surface the service needs.
//
// The interface is defined on the CONSUMING side, that is, here (the pattern of
// ADR 0001). The service does NOT import the repository package; the concrete
// store satisfies these signatures structurally and the wiring is done in
// module.go. This is what lets unit tests be written without a real database,
// against a fake store a few lines long.
type Store interface {
	// CreateUpload writes the upload record.
	CreateUpload(ctx context.Context, u models.Upload) (models.Upload, error)
	// GetUpload returns the record by its identifier; NotFound if absent.
	GetUpload(ctx context.Context, id string) (models.Upload, error)
	// GetUploadByKey returns the record by its storage key; NotFound if absent.
	GetUploadByKey(ctx context.Context, key string) (models.Upload, error)
	// ListUploads paginates the records; the second value is the count of ALL
	// rows.
	ListUploads(ctx context.Context, filter models.UploadFilter) ([]models.Upload, int64, error)
	// DeleteUpload deletes the record; the second value is whether the row was
	// really deleted or not, and an identifier that does not exist IS NOT AN
	// ERROR.
	DeleteUpload(ctx context.Context, id string) (bool, error)
}

// Options holds the setup dependencies of the service.
type Options struct {
	// Store is the persistence surface; it is required.
	Store Store
	// Providers are the registered file providers; they are required.
	Providers *ProviderRegistry
	// ProviderID is the identifier of the provider to be used ON UPLOAD
	// (FILE_PROVIDER); it is required.
	//
	// Whether the provider is registered is NOT validated here and cannot be:
	// the providers brought by the plugins are registered AFTER the modules
	// come up (see the two phases of coreplugin.Registry). The check for once
	// the whole setup has finished is in the composition root (cmd/server).
	ProviderID string
	// MaxUploadBytes is the maximum size of a single upload; it is required.
	MaxUploadBytes int64
	// AllowedTypes are the accepted CONTENT types; at least one type is
	// required.
	AllowedTypes []string
	// Logger discards the logs when it is given as nil.
	Logger *slog.Logger
}

// Service is the outward-facing service of the file module.
// It is safe for concurrent use.
type Service struct {
	store        Store
	providers    *ProviderRegistry
	providerID   string
	maxBytes     int64
	allowedTypes []string
	log          *slog.Logger
}

// New produces a service with the given dependencies.
//
// A missing dependency is a setup error and it is returned EXPLICITLY: a
// service built with a nil store would produce a panic on the first upload, and
// the error would have surfaced long after the setup.
//
// An EMPTY allow list is rejected too. "If the list is empty, accept
// everything" would be the most dangerous default: a single typo in the
// configuration would silently remove the checking.
func New(opts Options) (*Service, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.Internal(CodeNotReady, "the file service cannot be built without a store")
	case opts.Providers == nil:
		return nil, errors.Internal(CodeNotReady, "the file service cannot be built without a provider registry")
	case opts.ProviderID == "":
		return nil, errors.Internal(CodeNotReady, "the file service cannot be built without a provider id")
	case opts.MaxUploadBytes <= 0:
		return nil, errors.Internal(CodeNotReady,
			"the file service cannot be built without a positive size bound, %d given", opts.MaxUploadBytes)
	case len(opts.AllowedTypes) == 0:
		return nil, errors.Internal(CodeNotReady,
			"the file service cannot be built with an empty allow list; the accepted types have to be counted out explicitly")
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	// The list is COPIED and sorted: keeping the caller's slice as it is would
	// have meant that a piece of code changing it afterwards could widen the
	// allow list while running. The order, in turn, makes the error message
	// stable.
	types := slices.Clone(opts.AllowedTypes)
	slices.Sort(types)

	return &Service{
		store:        opts.Store,
		providers:    opts.Providers,
		providerID:   opts.ProviderID,
		maxBytes:     opts.MaxUploadBytes,
		allowedTypes: types,
		log:          log,
	}, nil
}

// ProviderID returns the identifier of the provider used on upload.
func (s *Service) ProviderID() string { return s.providerID }

// MaxUploadBytes returns the maximum size of a single upload.
//
// The HTTP layer reads this: the [net/http.MaxBytesReader] wrapping the body
// applies the bound to the WHOLE of the request, and writing the bound in two
// separate places would have meant the two of them silently drifting apart.
func (s *Service) MaxUploadBytes() int64 { return s.maxBytes }

// AllowedTypes returns the accepted content types, in order.
func (s *Service) AllowedTypes() []string { return slices.Clone(s.allowedTypes) }

// Page holds the pagination parameters of list requests.
type Page struct {
	// Limit is the maximum number of rows to return; if 0, [DefaultLimit] is
	// applied.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// normalize validates the pagination parameters and applies the defaults.
func (p Page) normalize() (Page, error) {
	switch {
	case p.Limit < 0:
		return Page{}, errors.Invalid(CodeInvalidInput, "limit negatif olamaz: %d", p.Limit)
	case p.Offset < 0:
		return Page{}, errors.Invalid(CodeInvalidInput, "offset negatif olamaz: %d", p.Offset)
	case p.Limit > MaxLimit:
		return Page{}, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d: %d", MaxLimit, p.Limit)
	case p.Limit == 0:
		p.Limit = DefaultLimit
	}

	return p, nil
}

// ListUploads returns the upload ledger, paginated.
// The second return value is the count of ALL records.
func (s *Service) ListUploads(ctx context.Context, page Page) ([]models.Upload, int64, error) {
	normal, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}

	return s.store.ListUploads(ctx, models.UploadFilter{Limit: normal.Limit, Offset: normal.Offset})
}

// GetUpload returns a single upload record; errors.NotFound if absent.
func (s *Service) GetUpload(ctx context.Context, id string) (models.Upload, error) {
	if id == "" {
		return models.Upload{}, errors.Invalid(CodeInvalidInput, "the upload id cannot be empty")
	}

	return s.store.GetUpload(ctx, id)
}

// DeleteUpload deletes the file from the store and the record from the ledger.
// It IS IDEMPOTENT: an identifier that does not exist IS NOT an error.
//
// # Why idempotent
//
// A delete is an END STATE claim ("this upload no longer exists") and the
// caller may retry it: a flow removing a product image calls this endpoint a
// second time on its second round. The second call returning 404 would count
// the flow as an error WHILE the wanted end state HOLDS — that is, it would
// make exactly the thing it has to clean up impossible to clean up.
//
// # Why the file FIRST and the record SECOND
//
// The two sides are in separate systems and cannot be taken into a single
// transaction; all that is left is the ORDER. The only inconsistency that can
// arise in this order is the "its record exists, its file does not" state, and
// a retry CLOSES it: the provider's delete is idempotent, it does not fail on
// the second round and the record gets deleted too. The reverse order would not
// converge — once the record is gone nobody is left who knows the file's key,
// and if the delete had failed, that file would be unreachable garbage forever.
func (s *Service) DeleteUpload(ctx context.Context, id string) error {
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "the upload id cannot be empty")
	}

	record, err := s.store.GetUpload(ctx, id)
	if err != nil {
		// If the record does not exist there is nothing to do either; the end
		// state already holds.
		if errors.IsNotFound(err) {
			return nil
		}

		return err
	}

	// The provider is the record's OWN provider, not the one configured at that
	// moment: on the day the installation moves to an object store the old
	// records still sit on the local disk, and the only thing able to delete
	// them is the provider that wrote them.
	prov, err := s.providers.Get(record.ProviderID)
	if err != nil {
		return err
	}

	if err := prov.Delete(ctx, record.StorageKey); err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeUploadFailed,
			"the file could not be deleted from the store: %s", record.StorageKey)
	}

	if _, err := s.store.DeleteUpload(ctx, id); err != nil {
		return err
	}

	s.log.DebugContext(ctx, "upload deleted",
		"upload_id", id, "saglayici", record.ProviderID)

	return nil
}

// OpenedFile is a file ready to be served.
type OpenedFile struct {
	// Upload is the file's record in the ledger. The Content-Type served is
	// written FROM HERE — not from the type the client declared while
	// uploading.
	Upload models.Upload
	// Content is the file's content; the caller is OBLIGED TO CLOSE it.
	//
	// It has to be an io.ReadSeeker: [net/http.ServeContent] can satisfy range
	// (Range) requests only over a seekable source.
	Content io.ReadSeekCloser
	// ModTime is the moment the file last changed; conditional requests
	// (If-Modified-Since) are answered over this one.
	ModTime time.Time
}

// fileOpener is the OPTIONAL surface reporting that a provider CAN READ files.
//
// It is not in the core contract ([coreprovider.FileProvider]) and it must not
// be: in an object store the file is served by the CDN, the application never
// reads it. Making the surface mandatory would have meant having providers that
// will not serve write a method that is never called.
//
// The interface is defined on the CONSUMING side (ADR 0001); local.Provider
// satisfies it structurally.
type fileOpener interface {
	Open(ctx context.Context, key string) (io.ReadSeekCloser, time.Time, error)
}

// OpenByKey opens a file by its storage key so that it can be served.
//
// # The ORDER MATTERS: the LEDGER first, the STORE second
//
// The key coming from the address bar is asked of the database first. If there
// is no row the store is not touched at all; that is, the only key able to
// reach the store is the key this module itself produced and wrote into the
// ledger. This is the structure carrying the "the only things served are
// uploaded files" claim — not a string check.
//
// If the provider does not support reading, errors.NotFound is returned: in an
// installation writing into an object store the file's real address is on the
// CDN and this path is empty. Saying "it is not here" instead of "it is not
// implemented" is the right thing — for the client it really does not exist.
func (s *Service) OpenByKey(ctx context.Context, key string) (OpenedFile, error) {
	if strings.TrimSpace(key) == "" {
		return OpenedFile{}, errors.Invalid(CodeInvalidInput, "the storage key cannot be empty")
	}

	record, err := s.store.GetUploadByKey(ctx, key)
	if err != nil {
		return OpenedFile{}, err
	}

	prov, err := s.providers.Get(record.ProviderID)
	if err != nil {
		return OpenedFile{}, err
	}

	opener, supported := prov.(fileOpener)
	if !supported {
		return OpenedFile{}, errors.NotFound(CodeNotServable,
			"the %q provider does not serve files from the application", record.ProviderID)
	}

	content, modTime, err := opener.Open(ctx, record.StorageKey)
	if err != nil {
		return OpenedFile{}, err
	}

	return OpenedFile{Upload: record, Content: content, ModTime: modTime}, nil
}
