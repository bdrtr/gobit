// Package audit records who called which admin write, and what came back.
//
// # What it records, and what it deliberately does not
//
// The REQUEST, not the change. A diff would mean every module producing a
// before-and-after for every write — a contract in seventeen places and a cost
// on every request — while a bare "a product was updated" would be cheaper and
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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
)

// MigrationOwner is the name of the core component that owns this schema.
const MigrationOwner = "audit"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// CodeWriteFailed reports that the audit row could not be written.
const CodeWriteFailed = "audit_write_failed"

// CodeReadFailed reports that the audit log could not be read.
const CodeReadFailed = "audit_read_failed"

// MaxLimit is the most rows one List call returns.
//
// It is a CAP rather than a default: a caller asking for more is refused rather
// than quietly served fewer, because a listing silently truncated is one a
// reader believes is complete. The number is the same as the modules' bulk-read
// limit and is repeated by hand for the same reason theirs are — this package
// cannot import a module.
const MaxLimit = 100

// DefaultLimit is how many rows come back when a caller names no limit.
const DefaultLimit = 50

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

// Record is one audit row as a reader sees it.
//
// It embeds [Entry] rather than repeating its fields: what is stored and what is
// read back are the same facts, and two flat structs would be two places for a
// field to be added to.
type Record struct {
	// ID is the row's identifier, and it is also half of the paging position.
	ID string
	// Entry is what was recorded.
	Entry
	// CreatedAt is when the row was written, from the DATABASE clock.
	CreatedAt time.Time
}

// Filter narrows and positions a listing.
//
// # The two filters are two of the three INDEXES, and that is not a coincidence
//
// The schema carries three indexes and its own comments name the three questions
// they answer: "what did this person do" (actor_id), "what happened to this
// endpoint" (path) and "what happened most recently" (created_at). The first two
// are the two fields here; the THIRD is what a listing with neither field set
// reaches, and it arrived with the reader (migration 000002). A filter with no
// index behind it would look identical to a caller and scan the whole table,
// which on an append-only log is the failure that arrives quietly and late.
//
// # The position is a TIME and an ID, not an opaque string
//
// Encoding a cursor is an HTTP concern and the code for it lives in an internal
// package a published one may not import. More importantly the split is right:
// this store's business is a keyset position, and what a caller wraps it in is
// theirs. The composition root's endpoint encodes both halves into one opaque
// value; an embedder is free not to.
type Filter struct {
	// ActorID limits the listing to one caller. Empty means every caller.
	ActorID string
	// Path limits the listing to one endpoint. Empty means every endpoint.
	//
	// It matches EXACTLY. A prefix match would need a different index and would
	// invite "/admin/v1/" as an argument, which is the whole table with extra
	// steps.
	Path string
	// AfterAt and AfterID are the position the page starts BELOW, in the
	// listing's own order (newest first). Both zero means the first page.
	//
	// They are used together and never apart: created_at alone is not unique —
	// two rows written in the same microsecond would make a page boundary drop
	// or repeat a row — which is why the indexes carry the id as their last
	// column.
	AfterAt time.Time
	AfterID string
	// Limit is how many rows to return; zero means [DefaultLimit].
	Limit int
}

// listSQL reads a page of audit rows, newest first.
//
// # Why one statement with two optional filters
//
// The two filters are independent and either may be absent, so the alternative
// is four hand-written statements. The empty-string guards below let one
// statement serve all four shapes, and the plan still reaches the right index
// because a comparison against a constant empty string is folded away.
//
// That the index is really used is not a claim here: it is measured in
// audit_integration_test.go with EXPLAIN, on all three shapes. This repository
// has already shipped a godoc asserting "the index is used" that turned out to
// be false, which is why the claim lives in a test rather than in a sentence.
//
// # The keyset comparison is a ROW comparison
//
// (created_at, id) < (a, b) is one comparison against the index's own column
// order, and PostgreSQL turns it into an index condition. Writing it as
// "created_at < a OR (created_at = a AND id < b)" is the same truth and a worse
// plan: the OR is not an index condition and becomes a filter over the rows the
// index returns.
const listSQL = `
SELECT id, actor_id, actor_kind, method, path, status, request_id, created_at
FROM audit_log
WHERE ($1::text = '' OR actor_id = $1::text)
  AND ($2::text = '' OR path = $2::text)
  AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::text))
ORDER BY created_at DESC, id DESC
LIMIT $5`

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

// List reads a page of audit rows, newest first.
//
// # Why the audit log needed a reader at all
//
// It did not have one. The table was built with two indexes for two named
// operator questions and nothing in the repository ever read a row — while four
// other places (the outbox, the webhook plugin's migration and its module, and
// the relay job) cite this very table as "the write-only ledger this repository
// has already built once". The lesson was learned everywhere except where it was
// learned. See ADR 0037.
//
// # Paging is KEYSET, not offset
//
// An audit log is append-only and read newest-first, which is the shape offset
// is worst at: the deeper the page, the more rows the database walks and
// discards, and a row written during the walk shifts every later page by one —
// so a reader following an incident can miss a row or see it twice. The keyset
// position is exact and its cost does not grow with depth.
//
// A page shorter than the limit is the last page. The caller reads the position
// of the next page off the LAST record it received; there is no separate
// "has more" flag, because computing one means asking for a row you then throw
// away.
func (s *Store) List(ctx context.Context, f Filter) ([]Record, error) {
	limit := f.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0:
		return nil, errors.Invalid(CodeReadFailed, "a page size cannot be negative, %d given", limit)
	case limit > MaxLimit:
		return nil, errors.Invalid(CodeReadFailed,
			"a page can hold at most %d rows, %d requested", MaxLimit, limit)
	}

	// The two halves of the position travel together or not at all. A time with
	// no id would make the boundary ambiguous between rows sharing a timestamp,
	// and an id with no time has nothing to compare against.
	if f.AfterAt.IsZero() != (f.AfterID == "") {
		return nil, errors.Invalid(CodeReadFailed,
			"a paging position needs both a moment and an id, or neither")
	}

	var after any
	if !f.AfterAt.IsZero() {
		after = f.AfterAt
	}

	rows, err := s.pool.Query(ctx, listSQL, f.ActorID, f.Path, after, f.AfterID, limit)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the audit log could not be read")
	}
	defer rows.Close()

	out := make([]Record, 0, limit)

	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.ActorID, &r.ActorKind,
			&r.Method, &r.Path, &r.Status, &r.RequestID, &r.CreatedAt); err != nil {
			return nil, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
				"an audit row could not be read")
		}

		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the audit log page could not be completed")
	}

	return out, nil
}

// ListSQLForTest exposes the listing statement to this package's integration
// test.
//
// It is exported for one reason and the reason is worth the ugliness: the claim
// that each listing shape reaches its index can only be checked by handing the
// REAL statement to EXPLAIN. A test that retyped the SQL would measure the
// planner's opinion of a copy, and the copy would go on passing after the
// original changed — which is precisely the class of defect this package has
// already shipped once, in a godoc that said "the index is used".
//
// It returns the statement and nothing else; there is nothing here a caller can
// use to reach the database.
func ListSQLForTest() string { return listSQL }
