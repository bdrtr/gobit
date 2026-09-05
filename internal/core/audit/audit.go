// Package audit records who called which admin write, and what came back.
//
// # What it records, and what it deliberately does not
//
// The REQUEST, not the change. A diff would mean every module producing a
// before-and-after for every write — a contract in fifteen places and a cost on
// every request — while a bare "a product was updated" would be cheaper and
// worth nothing. What a row answers is the question an incident starts with:
// who touched this surface, when, and did it succeed. The WHAT is then read
// from the record itself, which already carries its own updated_at.
//
// Before this existed the admin API authenticated and authorized every write
// and then forgot it happened; the only durable trace of any change was a
// timestamp on the row.
package audit

import (
	"context"
	"embed"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// MigrationOwner is the name of the core component that owns this schema.
const MigrationOwner = "audit"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// CodeWriteFailed reports that the audit row could not be written.
const CodeWriteFailed = "audit_write_failed"

// Migrations returns this schema's migration files with the directory prefix
// stripped, the way db.Migrate expects them.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		// The directory name is a compile-time constant and the embed directive
		// has already verified the files exist; returning nil silently would
		// mean the server coming up with no audit table.
		panic("audit: could not open the embedded migrations directory: " + err.Error())
	}

	return sub
}

// Entry is one audited request.
type Entry struct {
	// ActorID and ActorKind are the caller.
	ActorID   string
	ActorKind string
	// Method and Path are what they called.
	Method string
	Path   string
	// Status is what came back.
	Status int
	// RequestID ties the row to the log lines of the same request.
	RequestID string
}

// insertSQL writes one row.
const insertSQL = `
INSERT INTO audit_log (id, actor_id, actor_kind, method, path, status, request_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

// Store writes audit rows.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a store over the pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Write records one audited request.
//
// # Why it is written AFTER the handler
//
// The status is part of the record, and a row written before the work would
// have to guess it — or record an attempt that was then refused, which is a
// different fact and a noisier log.
//
// # Why a failure here does not fail the request
//
// The write already happened. Refusing the response because the audit row could
// not be stored would undo nothing — the change is committed — and would turn a
// logging fault into a customer-visible outage. The fault is reported instead,
// and the residual is stated plainly: a change whose row was lost is a change
// with no trail, and that is the same window the outbox closes for events. It
// is not closed here because closing it would mean the audit row joining every
// module's transaction, which is a coupling this record does not earn.
func (s *Store) Write(ctx context.Context, id string, e Entry) error {
	if e.Method == "" || e.Path == "" {
		return errors.Invalid(CodeWriteFailed, "an audit entry needs a method and a path")
	}

	_, err := s.pool.Exec(ctx, insertSQL,
		id, e.ActorID, e.ActorKind, e.Method, e.Path, e.Status, e.RequestID)
	if err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeWriteFailed,
			"the audit row for %s %s could not be written", e.Method, e.Path)
	}

	return nil
}
