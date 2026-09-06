//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so `make test` stays
// fast. To run them: make test-integration
//
// # Why the refusal has to be tested against a real database
//
// ADR 0032 decides that an issued invoice is retained and that the refusal
// lives in the SCHEMA rather than in Go, "because a refusal that lives in Go
// stops the caller while a refusal that lives in the database stops the
// STATEMENT". Nothing in the Go code can show that. The module writes no DELETE
// at all, so a unit test proving the module does not delete proves only that
// the module does not delete — the same green build the module had on the day
// ADR 0032 was written, when a single statement was all it took to put a hole
// in a numbered series.
//
// These tests issue the statements the trigger exists to stop, through the real
// pool, and require the SQLSTATE back. They are the only thing in the tree that
// would notice if migration 000002 were rolled back, edited or never applied —
// and noticing is the whole difference between this mechanism and the REVOKE
// DELETE candidate the migration header rejected, which reported success,
// changed the catalog and stopped nothing.
package invoice_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository"
)

// issueForBuyer issues one document to the given address and returns it.
func issueForBuyer(t *testing.T, prefix, email string) models.Invoice {
	t.Helper()

	in := issueFor(prefix)
	in.Buyer = models.Party{Name: "A Customer", Email: email, CountryCode: "TR"}

	issued, err := newService(t).Issue(context.Background(), in)
	require.NoError(t, err)
	require.NotEmpty(t, issued.Lines, "the document needs a line for the child trigger to have work")

	return issued
}

// requireRetentionRefusal asserts that err is the database refusing, by CODE.
//
// The code and not the message: a message is prose somebody will improve, and a
// test that matched it would fail on the improvement and pass on a different
// error that happened to read the same way. The SQLSTATE is the contract an
// embedder maps.
func requireRetentionRefusal(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr, "the refusal has to arrive as a PostgreSQL error, not as a Go guard")
	assert.Equal(t, repository.SQLStateRetained, pgErr.Code,
		"the refusal has to carry the module's own SQLSTATE (got %q: %s)", pgErr.Code, pgErr.Message)
}

// TestARawDeleteOfAnInvoiceIsRefused is the statement ADR 0032 exists to stop.
//
// It is written as raw SQL through the pool rather than through the repository
// on purpose: the repository has no delete method, and the hazard the ADR
// describes is precisely the one statement somebody adds tomorrow. The message
// is checked for the invoice NUMBER as well, because that is what the row
// trigger buys over a statement trigger and what a Retained answer owes — an
// operator who cannot tell WHICH document was refused has been told nothing.
func TestARawDeleteOfAnInvoiceIsRefused(t *testing.T) {
	ctx := context.Background()
	issued := issueForBuyer(t, "RDI", "delete-me@example.com")

	_, err := testPool.Pool().Exec(ctx, `DELETE FROM invoices WHERE id = $1`, issued.ID)
	requireRetentionRefusal(t, err)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Contains(t, pgErr.Message, issued.Number,
		"the row trigger has OLD, so the refusal names the document it refused")

	survived, err := newService(t).GetInvoice(ctx, issued.ID)
	require.NoError(t, err, "the document has to still be there after the refusal")
	assert.Equal(t, issued.Number, survived.Number)
}

// TestARawDeleteOfTheLinesIsRefused closes the hole that guarding the parent
// alone leaves open.
//
// Measured while migration 000002 was being written: with the invoices trigger
// in place and nothing on the child table, this exact statement returned
// DELETE 1. That takes the retained description text off the document and
// leaves the invoice standing with the STORED totals 000001 keeps precisely so
// they cannot be recomputed away — against lines that no longer exist.
func TestARawDeleteOfTheLinesIsRefused(t *testing.T) {
	ctx := context.Background()
	issued := issueForBuyer(t, "RDL", "lines@example.com")

	_, err := testPool.Pool().Exec(ctx,
		`DELETE FROM invoice_lines WHERE invoice_id = $1`, issued.ID)
	requireRetentionRefusal(t, err)

	still, err := newService(t).GetInvoice(ctx, issued.ID)
	require.NoError(t, err)
	assert.Len(t, still.Lines, len(issued.Lines), "the lines have to still be there")
}

// TestATruncateIsRefused covers the statement no row trigger can see.
//
// TRUNCATE removes rows without visiting them, so the BEFORE DELETE row
// triggers are silent on it; the BEFORE TRUNCATE statement triggers are what
// answer. Both spellings that reach them are exercised, and the third is
// asserted for what it really is: "TRUNCATE invoices" ALONE never reaches the
// trigger at all, because PostgreSQL refuses it first with 0A000 for the
// foreign key on invoice_lines. That refusal is a side effect of the child
// table's existence rather than a decision anybody made, which is exactly why
// the companion triggers are not redundant — remove them and CASCADE works.
func TestATruncateIsRefused(t *testing.T) {
	ctx := context.Background()
	issueForBuyer(t, "TRC", "truncate@example.com")

	_, err := testPool.Pool().Exec(ctx, `TRUNCATE invoices, invoice_lines`)
	requireRetentionRefusal(t, err)

	_, err = testPool.Pool().Exec(ctx, `TRUNCATE invoices CASCADE`)
	requireRetentionRefusal(t, err)

	_, err = testPool.Pool().Exec(ctx, `TRUNCATE invoice_lines`)
	requireRetentionRefusal(t, err)

	// The spelling that is stopped by something else, recorded so the next
	// reader does not mistake it for evidence that the companion works.
	_, err = testPool.Pool().Exec(ctx, `TRUNCATE invoices`)
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "0A000", pgErr.Code,
		"TRUNCATE invoices alone is stopped by the foreign key, before any trigger runs")
}

// TestDeletingNothingIsStillNothing is why the trigger is FOR EACH ROW.
//
// A statement trigger fires whether or not the statement matched anything, so
// it would turn "there was nothing to delete" into a refusal — an answer that
// is not true, and one a cleanup script cannot tell apart from a real one. The
// row trigger visits rows, so a DELETE that matches none stays a harmless
// DELETE 0.
func TestDeletingNothingIsStillNothing(t *testing.T) {
	ctx := context.Background()

	tag, err := testPool.Pool().Exec(ctx,
		`DELETE FROM invoices WHERE id = $1`, "inv_this_id_does_not_exist")
	require.NoError(t, err, "a DELETE that matches nothing is not a refusal")
	assert.Equal(t, int64(0), tag.RowsAffected())
}

// TestTheSanctionedEscapeNeedsBothTriggersDisabled pins the escape ADR 0032
// accepts, and the reason it costs two statements rather than one.
//
// invoice_lines.invoice_id keeps its ON DELETE CASCADE, so a deliberate removal
// of a document still takes its lines — and the CHILD trigger fires on those
// cascaded rows. Disabling the parent's trigger alone therefore still fails,
// which is the shape of the escape the migration header documents. The whole
// thing runs inside a transaction that rolls back: this test describes an act a
// DBA performs, it does not perform one.
func TestTheSanctionedEscapeNeedsBothTriggersDisabled(t *testing.T) {
	ctx := context.Background()
	issued := issueForBuyer(t, "ESC", "escape@example.com")

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `ALTER TABLE invoices DISABLE TRIGGER invoices_no_delete`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, issued.ID)
	requireRetentionRefusal(t, err)

	// The failed statement aborted the transaction, so the second half runs in
	// a fresh one.
	require.NoError(t, tx.Rollback(ctx))

	tx2, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx2.Rollback(ctx) }()

	_, err = tx2.Exec(ctx, `ALTER TABLE invoices DISABLE TRIGGER invoices_no_delete`)
	require.NoError(t, err)
	_, err = tx2.Exec(ctx, `ALTER TABLE invoice_lines DISABLE TRIGGER invoice_lines_no_delete`)
	require.NoError(t, err)

	tag, err := tx2.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, issued.ID)
	require.NoError(t, err, "with both triggers disabled a DBA can remove the document")
	assert.Equal(t, int64(1), tag.RowsAffected())

	require.NoError(t, tx2.Rollback(ctx))

	survived, err := newService(t).GetInvoice(ctx, issued.ID)
	require.NoError(t, err, "the rollback puts the document back; this test removes nothing")
	assert.Equal(t, issued.Number, survived.Number)
}

// TestTheReplicationRoleEscapeIsClosed pins the guard that ENABLE ALWAYS buys.
//
// # The hole this replaced
//
// An ordinary user trigger does not fire while session_replication_role is
// replica, and gobit ships ONE role that both owns these tables and serves
// requests — so the application's own connection could set it. Measured on the
// first draft of migration 000002: a single session-level SET followed by a
// DELETE removed the document, and ON DELETE CASCADE, being an internal
// trigger, went quiet with the rest, so the lines were left ORPHANED in a state
// the foreign key would never have permitted. One SET, no catalog trace.
//
// # Why this test asserts the opposite now
//
// The four triggers are declared ENABLE ALWAYS, which makes them fire in
// replica mode too. The refusal below is the whole point of that clause, and it
// is the reason this test is worth its cost: if somebody drops ENABLE ALWAYS
// from the migration — it reads like a redundant line — nothing else in the
// repository notices, and the guard silently becomes advisory.
//
// What ENABLE ALWAYS does NOT buy is a guard against the role that owns the
// table: ALTER TABLE ... DISABLE TRIGGER still works, and the sibling test
// above pins that as the sanctioned escape. That limit is stated in the
// migration header and in ADR 0032 rather than papered over.
//
// Everything runs inside a transaction that rolls back.
func TestTheReplicationRoleEscapeIsClosed(t *testing.T) {
	ctx := context.Background()
	issued := issueForBuyer(t, "REP", "replica@example.com")

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err,
		"the single role gobit ships is a superuser, so it may set this parameter itself")

	_, err = tx.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, issued.ID)
	requireRetentionRefusal(t, err)

	// The refusal aborted the transaction, so the document is checked from a
	// fresh connection rather than from this one.
	require.NoError(t, tx.Rollback(ctx))

	survived, err := newService(t).GetInvoice(ctx, issued.ID)
	require.NoError(t, err, "replica mode must not be able to remove an issued document")
	assert.Equal(t, issued.Number, survived.Number)
}

// TestTheBuyerEmailIndexExistsAndMatchesTheQuery keeps an index and the
// predicate that needs it spelled the same way.
//
// An index on lower(buyer_email) is usable ONLY by a predicate written
// lower(buyer_email) = ..., so the two are one decision written in two files.
// The PLAN is deliberately not asserted: the test database holds a few dozen
// rows and Postgres is right to scan them sequentially, so a plan assertion
// here would measure the row count rather than the schema and would have to be
// weakened until it measured nothing. What can be asserted without lying is
// that the index is there and that its definition is the expression the query
// uses.
func TestTheBuyerEmailIndexExistsAndMatchesTheQuery(t *testing.T) {
	ctx := context.Background()

	var definition string

	err := testPool.Pool().QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
		  WHERE schemaname = 'public' AND indexname = 'invoices_buyer_email_idx'`).
		Scan(&definition)
	require.NoError(t, err, "migration 000002 has to create the buyer address index")
	assert.Contains(t, definition, "lower(buyer_email)")
}

// TestTheRetainedCountComesFromTheDatabase is the erasure answer end to end.
//
// The count is what a controller repeats to a data subject, so it is taken
// against real rows and against a real case difference: buyer_email has no
// CHECK forcing lower case here (customer and auth both have one), so a
// case-sensitive match would answer "nothing retained" about a person whose
// document is in the table under one capital letter.
func TestTheRetainedCountComesFromTheDatabase(t *testing.T) {
	ctx := context.Background()

	issueForBuyer(t, "ERA", "Ada.Lovelace@Example.COM")
	issueForBuyer(t, "ERA", "ada.lovelace@example.com")
	issueForBuyer(t, "ERA", "someone.else@example.com")

	svc := newService(t)

	result, err := svc.Erase(ctx, erasure.Subject{Email: "ADA.LOVELACE@example.com"})
	require.NoError(t, err)

	assert.Equal(t, invoice.ModuleName, result.Holder)
	assert.Equal(t, erasure.Retained, result.Outcome)
	assert.Equal(t, 2, result.Rows, "both spellings of the address are the same person")
	assert.NotEmpty(t, result.Why)
	assert.Contains(t, result.Kept, "invoices.metadata",
		"the free-form columns gobit never rewrites have to be named")

	// Idempotency, against the real database rather than against a fake: the
	// contract requires a second sweep to give the same answer, and here it is
	// a property of the method reading rather than writing.
	again, err := svc.Erase(ctx, erasure.Subject{Email: "ADA.LOVELACE@example.com"})
	require.NoError(t, err)
	assert.Equal(t, result, again)
}
