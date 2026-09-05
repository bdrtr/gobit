// Package repository is the database access of the file module.
//
// It touches ONLY this module's table (plan Section 4). The sqlc generated
// code is under repository/filedb and is not edited by hand; this package adds
// two things on top of it:
//
//   - Conversion: pgtype and the generated row types DO NOT LEAVE THIS
//     PACKAGE, they are converted into models types.
//   - Classification: driver errors are converted into errors typed by
//     core/errors; a missing row becomes NotFound, a uniqueness violation
//     Conflict (plan Section 2.7 — the handler does not choose the status
//     code).
//
// # THERE IS NO TRANSACTION, and none is needed
//
// The repositories of the other modules carry WithTx; here there is none. An
// upload has two sides — the file IN THE STORE and the row IN THE LEDGER — and
// the two sit in separate systems; a database transaction cannot take the file
// back. Consistency is therefore established not with a transaction but with
// the ORDER: when writing, the file first and then the row; when deleting, the
// file first and then the row. The reason for the order sits next to the
// relevant calls (see the service package).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/repository/filedb"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeUploadNotFound reports that the requested upload record could not be
	// found.
	CodeUploadNotFound = "file_upload_not_found"
	// CodeUploadExists reports that a second record was to be opened with the
	// same storage key.
	CodeUploadExists = "file_upload_already_exists"
	// CodeConstraintViolation reports that a database constraint was violated.
	CodeConstraintViolation = "file_constraint_violation"
	// CodeQueryFailed reports an unexpected database error.
	CodeQueryFailed = "file_query_failed"
	// CodeCanceled reports a context cancellation.
	CodeCanceled = "file_canceled"
	// CodeNotReady reports that the repository was constructed without a pool.
	CodeNotReady = "file_repository_not_ready"
)

// PostgreSQL SQLSTATE codes (the ones that are needed).
const (
	sqlstateUniqueViolation      = "23505"
	sqlstateCheckViolation       = "23514"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// constraintStorageKeyUniq is the name of the index that makes the storage key
// unique; it is EXACTLY the same as the name in the migration.
const constraintStorageKeyUniq = "file_uploads_storage_key_uniq"

// Repo provides access to the upload ledger. It is safe for concurrent use.
type Repo struct {
	q *filedb.Queries
}

// New produces a repository working on the given pool.
//
// If pool is nil, that is reported not at setup time but on the first call, as
// a typed error; the setup path produces no panic.
func New(pool *pgxpool.Pool) *Repo {
	if pool == nil {
		return &Repo{}
	}

	return &Repo{q: filedb.New(pool)}
}

// ready verifies that the pool is usable.
func (r *Repo) ready() error {
	if r == nil || r.q == nil {
		return errors.Unavailable(CodeNotReady, "the file database pool has not been set up")
	}

	return nil
}

// CreateUpload writes the upload record.
//
// A second record with the same storage key returns errors.Conflict. It cannot
// occur in the normal flow — the key is produced by the provider and its
// randomness is the same as a ULID's — but it has to be mapped: had it not
// been mapped, on the day key generation broke the failure would look like a
// "server error" and its cause would be lost.
func (r *Repo) CreateUpload(ctx context.Context, u models.Upload) (models.Upload, error) {
	if err := r.ready(); err != nil {
		return models.Upload{}, err
	}

	row, err := r.q.CreateFileUpload(ctx, filedb.CreateFileUploadParams{
		ID:           u.ID,
		StorageKey:   u.StorageKey,
		ProviderID:   u.ProviderID,
		ContentType:  u.ContentType,
		Size:         u.Size,
		Checksum:     u.Checksum,
		OriginalName: u.OriginalName,
		Url:          u.URL,
		UploadedBy:   u.UploadedBy,
	})
	if err != nil {
		return models.Upload{}, classify(err, "the upload record could not be written: %s", u.StorageKey)
	}

	return toUpload(row), nil
}

// GetUpload returns the record by its id; NotFound if there is none.
func (r *Repo) GetUpload(ctx context.Context, id string) (models.Upload, error) {
	if err := r.ready(); err != nil {
		return models.Upload{}, err
	}

	row, err := r.q.GetFileUpload(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Upload{}, uploadNotFound("id", id)
		}

		return models.Upload{}, classify(err, "the upload record could not be read: %s", id)
	}

	return toUpload(row), nil
}

// GetUploadByKey returns the record BY ITS STORAGE KEY; NotFound if there is
// none.
//
// It is the serving path's only query: the key coming from the address bar is
// asked here first, and if there is no row the file system is not touched at
// all.
func (r *Repo) GetUploadByKey(ctx context.Context, key string) (models.Upload, error) {
	if err := r.ready(); err != nil {
		return models.Upload{}, err
	}

	row, err := r.q.GetFileUploadByKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Upload{}, uploadNotFound("key", key)
		}

		return models.Upload{}, classify(err, "the upload record could not be read")
	}

	return toUpload(row), nil
}

// ListUploads returns the records with pagination.
// The second return value is the count of ALL rows.
func (r *Repo) ListUploads(ctx context.Context, filter models.UploadFilter) ([]models.Upload, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListFileUploads(ctx, filedb.ListFileUploadsParams{
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, "the upload list could not be fetched")
	}

	total, err := r.q.CountFileUploads(ctx)
	if err != nil {
		return nil, 0, classify(err, "the uploads could not be counted")
	}

	out := make([]models.Upload, 0, len(rows))
	// The slice is walked BY INDEX: walking it by value would copy the whole
	// row struct on every iteration.
	for i := range rows {
		out = append(out, toUpload(rows[i]))
	}

	return out, total, nil
}

// DeleteUpload deletes the record. The second return value is whether the row
// was REALLY deleted.
//
// An id that does not exist is NOT an error: deleting is a claim about an end
// state and the caller must be able to tell "it was not there anyway" from "I
// have just deleted it" — but both of them are success.
func (r *Repo) DeleteUpload(ctx context.Context, id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}

	deleted, err := r.q.DeleteFileUpload(ctx, id)
	if err != nil {
		return false, classify(err, "the upload record could not be deleted: %s", id)
	}

	return deleted > 0, nil
}

// toUpload converts the generated row into the domain model.
func toUpload(row filedb.FileUpload) models.Upload {
	return models.Upload{
		ID:           row.ID,
		StorageKey:   row.StorageKey,
		ProviderID:   row.ProviderID,
		ContentType:  row.ContentType,
		Size:         row.Size,
		Checksum:     row.Checksum,
		OriginalName: row.OriginalName,
		URL:          row.Url,
		UploadedBy:   row.UploadedBy,
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
	}
}

// toTime converts a NOT NULL timestamp into a UTC time.Time.
//
// An invalid (NULL) stamp returns the zero time: on NOT NULL columns this
// situation cannot arise, and if it did the zero time is a value that produces
// no panic and that catches the eye in a test.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}

	return ts.Time.UTC()
}

// uploadNotFound produces the typed error for a record that was not found.
//
// The name of the FIELD that was searched goes into the message: the same
// error can come from a lookup by id and from a lookup by storage key, and the
// fix for the two is different.
func uploadNotFound(field, value string) error {
	return errors.NotFound(CodeUploadNotFound, "the upload was not found (%s: %s)", field, value)
}

// sprintf formats the error message once.
//
// On calls without arguments the format is returned UNCHANGED; otherwise a
// percent sign in the message (e.g. "%!d(MISSING)") would corrupt the
// diagnostic text.
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}

	return fmt.Sprintf(format, a...)
}

// classify converts a raw database error into a typed error.
//
// The classification is deliberate: a uniqueness violation is a CONFLICT
// (409), a constraint violation is a client error (422), a cancellation is
// temporary unavailability (503); everything else is a server error and its
// message IS NOT LEAKED to the client (see core/http).
func classify(err error, format string, a ...any) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlstateUniqueViolation:
			if pgErr.ConstraintName == constraintStorageKeyUniq {
				return errors.Wrap(err, errors.KindConflict, CodeUploadExists,
					"an upload record already exists for this storage key")
			}
		case sqlstateCheckViolation, sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (constraint: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}
