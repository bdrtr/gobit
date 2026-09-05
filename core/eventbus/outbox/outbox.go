// Package outbox makes an event part of the transaction that promised it.
//
// # The window
//
// A module commits its work and then publishes. Between those two moments the
// process can die, and the work then exists while the event never happened:
// `internal/workflows/checkout/doc.go` and ADR 0020 argue the same shape one
// level down, about money. Here it costs a confirmation mail nobody sends and
// nothing that records one is owed.
//
// The event bus documents its own guarantees honestly — the in-memory backend
// loses events when the process dies, the Redis one resumes — but neither
// covers this window, because the publish is not part of the transaction.
//
// # The shape, and why it is this one
//
// A row written INSIDE the caller's transaction, and a relay that sends it
// afterwards ([internal/jobs/outboxrelay]).
//
// The writer takes the transaction as an ARGUMENT rather than reading it from
// the context, and that is forced rather than chosen: every module keeps its
// transaction under its own unexported context key, so the core cannot see it.
// Passing the executor is what lets a core-owned table be written inside a
// module-owned transaction without either side learning the other's internals.
package outbox

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// MigrationOwner is the name of the core component that owns this schema.
//
// It sits beside "workflow" and "job" in the core's migration list; each owner
// keeps its own version ledger, so rolling one back never touches another.
const MigrationOwner = "outbox"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns this schema's migration files with the directory prefix
// stripped, the way db.Migrate expects them.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		// The directory name is a compile-time constant and the embed directive
		// has already verified the files exist; returning nil silently would
		// mean the relay coming up without its table.
		panic("outbox: could not open the embedded migrations directory: " + err.Error())
	}

	return sub
}

// Error codes.
const (
	// CodeWriteFailed reports that the event could not be written.
	CodeWriteFailed = "outbox_write_failed"
	// CodeInvalidEvent reports that the event cannot be stored.
	CodeInvalidEvent = "outbox_invalid_event"
)

// Execer is the part of a database handle this package needs.
//
// Both a pool and a transaction satisfy it, which is the point: the caller
// hands in whichever it is inside, and this package never decides for it.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// insertSQL writes one pending event.
//
// ON CONFLICT DO NOTHING makes a repeated write with the same id a no-op rather
// than an error: a retried step that already wrote its event must not fail on
// the second pass, and an event is identified by its id exactly so that this is
// decidable.
const insertSQL = `
INSERT INTO event_outbox (id, name, data)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING`

// Write records an event inside the caller's transaction.
//
// # Why it does not publish
//
// Publishing from inside a transaction would put a network call in a critical
// section and, worse, would send an event for work that can still roll back.
// The row commits with the work or disappears with it; the relay is what turns
// it into a published event.
//
// # An id is required
//
// The caller supplies it, because the caller is the only side that can make a
// retry write the SAME row rather than a second one. An event with no id is
// refused rather than given one here: a generated id would make every retry a
// new event, which is the duplicate this table exists to prevent.
func Write(ctx context.Context, exec Execer, e eventbus.Event) error {
	if exec == nil {
		return errors.Internal(CodeWriteFailed,
			"the outbox needs a database handle; without one the event would be lost silently")
	}
	if e.Name == "" {
		return errors.Invalid(CodeInvalidEvent, "an outbox event needs a name")
	}
	if e.ID == "" {
		return errors.Invalid(CodeInvalidEvent,
			"an outbox event needs an id; without one a retry writes a SECOND event instead of "+
				"the same one")
	}

	payload, err := json.Marshal(e.Data)
	if err != nil {
		return errors.Wrap(err, errors.KindInvalid, CodeInvalidEvent,
			"the payload of event %q could not be encoded", e.Name)
	}

	if _, err := exec.Exec(ctx, insertSQL, e.ID, e.Name, payload); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeWriteFailed,
			"event %q could not be written to the outbox", e.Name)
	}

	return nil
}

// Pending is one unpublished event as the relay reads it.
type Pending struct {
	// ID and Name identify the event.
	ID   string
	Name string
	// Data is the payload.
	Data map[string]any
	// Attempts is how many times the relay has already tried.
	Attempts int64
	// CreatedAt is when the transaction that promised it committed.
	CreatedAt time.Time
}

// Event turns the row back into what the bus takes.
func (p Pending) Event() eventbus.Event {
	return eventbus.Event{ID: p.ID, Name: p.Name, Data: p.Data}
}
