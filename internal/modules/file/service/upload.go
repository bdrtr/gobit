package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// ErrTooLarge is the sentinel error reporting that the body exceeded the
// maximum size.
//
// It has to be a sentinel because it passes through the provider's read chain
// and comes back: the error arrives INSIDE the provider's own wrapper and the
// only way to recognize it is errors.Is. Recognizing it by string comparison
// would be a link that silently breaks every time the message is edited.
var ErrTooLarge = errors.Invalid(CodeTooLarge, "the upload exceeded the size bound")

// UploadInput is the input of a new upload.
//
// # THE FILE NAME IS NOT A PATH HERE EITHER
//
// [UploadInput.OriginalName] is only written into the ledger and shown in the
// response; the storage key is produced by the provider and the name does not
// enter any path expression. The existence of the field is therefore not a
// risk — what would create the risk is the name BEING USED AS A PATH, and that
// path does not exist at all.
type UploadInput struct {
	// ContentType is the type detected FROM THE CONTENT of the file
	// ([net/http.DetectContentType]); the client's Content-Type header is NEVER
	// written here.
	//
	// The reason the detection is done IN THE CALLER is that the only place able
	// to know the type is the layer that reads the first bytes (see the api
	// package). The service does not repeat the detection, it CHECKS it.
	ContentType string
	// Body is the body of the file and it is read as a STREAM; the caller is
	// obliged to close it.
	Body io.Reader
	// OriginalName is the file name the client reported; it may be empty.
	OriginalName string
	// UploadedBy is the identifier of the caller doing the upload; it may be
	// empty.
	UploadedBy string
}

// Upload checks the body, has it written into the store and records it in the
// ledger.
//
// # The ORDER of the steps
//
//  1. The allow list — before A SINGLE BYTE is written into the store. A
//     rejected file does not have to be cleaned up; every design that requires
//     that cleanup also has a branch in which the cleanup failed.
//  2. Writing to the provider — the body passes as a stream, it is not taken
//     into memory.
//  3. Recording in the ledger — AFTER the file has been written. The reverse
//     order would leave a window in which the file the record points at does not
//     exist yet.
//
// If the third step blows up, a file with no record is left in the store and
// that file IS CLEANED UP: an unreachable object would take up space forever,
// because nobody knows its key.
func (s *Service) Upload(ctx context.Context, in UploadInput) (models.Upload, error) {
	if err := s.validate(in); err != nil {
		return models.Upload{}, err
	}

	prov, err := s.providers.Get(s.providerID)
	if err != nil {
		return models.Upload{}, err
	}

	// The chain runs OUTSIDE IN: the bound is applied first (a byte exceeding
	// the bound must not even enter the digest), the digest is taken second, and
	// the provider reads at the outermost layer.
	digest := sha256.New()
	body := io.TeeReader(&boundedReader{r: in.Body, remaining: s.maxBytes + 1}, digest)

	file, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: in.ContentType,
		Body:        body,
	})
	if err != nil {
		// The bound error arrives inside the provider's wrapper; its class is
		// given HERE, because the side that knows the bound is this one.
		if errors.Is(err, ErrTooLarge) {
			return models.Upload{}, errors.Invalid(CodeTooLarge,
				"the file can be at most %d bytes", s.maxBytes)
		}

		return models.Upload{}, errors.Wrap(err, errors.KindOf(err), CodeUploadFailed,
			"the file could not be written into the store")
	}

	record, err := s.store.CreateUpload(ctx, models.Upload{
		ID:           models.NewUploadID(time.Now()),
		StorageKey:   file.Key,
		ProviderID:   prov.ID(),
		ContentType:  file.ContentType,
		Size:         file.Size,
		Checksum:     hex.EncodeToString(digest.Sum(nil)),
		OriginalName: in.OriginalName,
		URL:          file.URL,
		UploadedBy:   in.UploadedBy,
	})
	if err != nil {
		s.cleanUpWrittenFile(ctx, prov, file.Key)

		return models.Upload{}, err
	}

	s.log.DebugContext(ctx, "file uploaded",
		"upload_id", record.ID,
		"provider", record.ProviderID,
		"content_type", record.ContentType,
		"size", record.Size)

	return record, nil
}

// validate applies the checks the input has to pass before going to the store.
func (s *Service) validate(in UploadInput) error {
	if in.Body == nil {
		return errors.Invalid(CodeInvalidInput, "the upload body cannot be empty")
	}

	contentType := strings.TrimSpace(in.ContentType)
	if contentType == "" {
		return errors.Invalid(CodeInvalidInput, "the content type could not be detected")
	}
	if !slices.Contains(s.allowedTypes, contentType) {
		// The rejected type is WRITTEN into the message: it is the only thing
		// the person uploading can correct, and saying "it was not accepted"
		// does not tell them which file to pick. The value does not come from
		// the client, it was detected FROM THE CONTENT; that is, what is put
		// into the response is not a string of the attacker's choosing.
		return errors.Invalid(CodeTypeNotAllowed,
			"the %q content type is not accepted; the accepted ones are: %s",
			contentType, strings.Join(s.allowedTypes, ", "))
	}

	// The LENGTH of the name is checked but its CONTENT is not sanitized: the
	// name enters no path expression and no HTTP header, it is only returned in
	// the JSON body. The reason for the length bound is not security either, it
	// is the ledger — a megabyte-long "file name" would bloat the row.
	if utf8.RuneCountInString(in.OriginalName) > models.MaxOriginalNameLen {
		return errors.Invalid(CodeInvalidInput,
			"the file name can be at most %d characters", models.MaxOriginalNameLen)
	}

	return nil
}

// cleanUpWrittenFile deletes from the store a file whose record could not be
// opened.
//
// The error IS NOT SWALLOWED, it is LOGGED: the real error going back to the
// caller is the record error, and replacing it with the cleanup error would be
// hiding the cause of the fault. But it is not passed over in silence either —
// this line is the only place that knows the key of the file left behind.
func (s *Service) cleanUpWrittenFile(ctx context.Context, prov coreprovider.FileProvider, key string) {
	// The context may have been canceled (the client closed the connection);
	// the cleanup has to be attempted anyway, otherwise every canceled request
	// would leave a garbage file behind.
	cleanupCtx := context.WithoutCancel(ctx)

	if err := prov.Delete(cleanupCtx, key); err != nil {
		s.log.ErrorContext(ctx, "the file whose record could not be opened could not be deleted from the store",
			"error", err,
			"provider", prov.ID(),
			"storage_key", key,
			"meaning", "a file that no record points at is left in the store; it has to be cleaned up by hand")
	}
}

// boundedReader returns [ErrTooLarge] WHEN the number of bytes read exceeds the
// bound.
//
// [io.LimitReader] would be WRONG here: it returns io.EOF once it reaches the
// bound, that is, it silently TRUNCATES the body. The provider reads that as
// "the file has ended" and a half image gets recorded successfully — a request
// exceeding the bound would produce corrupt data instead of being rejected.
//
// remaining is started ONE MORE than the bound: a body carrying exactly the
// bound in bytes has to pass, one byte more has to be rejected. Had it been
// started at the bound, an extra read attempt would produce an error for a file
// sitting exactly at the bound.
type boundedReader struct {
	r         io.Reader
	remaining int64
}

// Read satisfies io.Reader.
func (s *boundedReader) Read(p []byte) (int, error) {
	if s.remaining <= 0 {
		return 0, ErrTooLarge
	}

	if int64(len(p)) > s.remaining {
		p = p[:s.remaining]
	}

	n, err := s.r.Read(p)
	s.remaining -= int64(n)

	return n, err
}
