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

	"github.com/bdrtr/gobit/core/personaldata"
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

// issueStackedForBuyer issues a document whose only row was taxed by a STACK.
//
// It exists so a test can reach the third guarded table: a single-rate row
// writes no breakdown at all, and a guard nothing writes to is a guard nothing
// can try.
func issueStackedForBuyer(t *testing.T, prefix, email string) models.Invoice {
	t.Helper()

	in := stackedIssueFor(prefix)
	in.Buyer = models.Party{Name: "A Customer", Email: email, CountryCode: "TR"}

	issued, err := newService(t).Issue(context.Background(), in)
	require.NoError(t, err)

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
// answer. The spellings that REACH them are exercised, and the ones stopped
// earlier are asserted for what they really are, so the next reader does not
// mistake a foreign key for evidence that a companion trigger works.
//
// # The line moved when the third table arrived
//
// PostgreSQL refuses a TRUNCATE of a table another table references, with
// 0A000, before any trigger runs. Until ADR 0097 that took "TRUNCATE invoices"
// alone; with invoice_line_taxes referencing invoice_lines it now takes
// "TRUNCATE invoice_lines" and "TRUNCATE invoices, invoice_lines" as well. The
// forms that still reach a trigger are the ones naming EVERY table of the
// document and the CASCADE — and the CASCADE is the one the companions exist
// for, because it is what an operator reaches for.
func TestATruncateIsRefused(t *testing.T) {
	ctx := context.Background()
	issueForBuyer(t, "TRC", "truncate@example.com")

	for name, statement := range map[string]string{
		"every table of the document": `TRUNCATE invoices, invoice_lines, invoice_line_taxes`,
		"the cascade":                 `TRUNCATE invoices CASCADE`,
		"the breakdown alone":         `TRUNCATE invoice_line_taxes`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, statement)
			requireRetentionRefusal(t, err)
		})
	}

	// The spellings that are stopped by something else. They are asserted
	// rather than left out: a test that only exercised these would look like
	// proof of a guard that had been deleted.
	for name, statement := range map[string]string{
		"the parent alone":        `TRUNCATE invoices`,
		"the lines alone":         `TRUNCATE invoice_lines`,
		"the parent and its rows": `TRUNCATE invoices, invoice_lines`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, statement)
			require.Error(t, err)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, "0A000", pgErr.Code,
				"this spelling is stopped by a foreign key, before any trigger runs")
		})
	}
}

// TestEveryTableOfTheDocumentIsGuarded derives the population from the SCHEMA
// so the next table cannot arrive unguarded.
//
// This gate is written because the guard on invoice_line_taxes was nearly
// missed: the table was added, and what noticed was a foreign key changing
// which spelling of TRUNCATE the database refuses first — a side effect, not a
// rule. Nothing asked "does every table of the document refuse a delete". Now
// something does, and it asks the DATABASE rather than a list somebody
// maintains, so a table added tomorrow is in the population the moment it
// exists.
//
// # The population is the document and everything hanging FROM it
//
// It is walked with a recursive join over the foreign keys, starting at
// invoices and following them in the referencing direction. That is the same
// sentence migration 000002 uses to say what has to be guarded — "the lines are
// part of the retained document" — expressed as something the database can
// answer.
//
// invoice_series is therefore OUT, and deliberately: the document points AT a
// series rather than the series being part of a document. What protects it is
// different and already there — invoices.series_id keeps a series with
// documents from being deleted at all, and invoices_number_uniq keeps a reset
// counter from minting a number twice.
func TestEveryTableOfTheDocumentIsGuarded(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`WITH RECURSIVE document AS (
             SELECT c.oid, c.relname
             FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relkind = 'r' AND n.nspname = current_schema()
               AND c.relname = 'invoices'
           UNION
             SELECT child.oid, child.relname
             FROM pg_constraint fk
             JOIN pg_class child ON child.oid = fk.conrelid
             JOIN document d ON d.oid = fk.confrelid
             WHERE fk.contype = 'f'
         )
         SELECT d.relname,
                count(*) FILTER (WHERE t.tgtype & 8 <> 0)  AS delete_guards,
                count(*) FILTER (WHERE t.tgtype & 32 <> 0) AS truncate_guards,
                count(*) FILTER (WHERE t.tgenabled <> 'A') AS not_always
         FROM document d
         LEFT JOIN pg_trigger t ON t.tgrelid = d.oid AND NOT t.tgisinternal
         GROUP BY d.relname
         ORDER BY d.relname`)
	require.NoError(t, err)
	defer rows.Close()

	var visited []string
	for rows.Next() {
		var table string
		var deletes, truncates, notAlways int64
		require.NoError(t, rows.Scan(&table, &deletes, &truncates, &notAlways))

		assert.Positive(t, deletes,
			"%s holds part of a retained document and has no BEFORE DELETE guard", table)
		assert.Positive(t, truncates,
			"%s has no BEFORE TRUNCATE guard; a row trigger never sees a TRUNCATE", table)
		assert.Zero(t, notAlways,
			"%s has a guard that is not ENABLE ALWAYS, so one session-level "+
				"session_replication_role turns it off", table)
		visited = append(visited, table)
	}
	require.NoError(t, rows.Err())

	// A walk that returns nothing, or only its own starting point, would pass
	// every assertion above and prove nothing. The tables are named so that a
	// walk which quietly stops following the keys fails here rather than going
	// green on an empty population.
	assert.Equal(t,
		[]string{"invoice_line_taxes", "invoice_lines", "invoices"}, visited,
		"the walk has to reach the document and everything hanging from it")
}

// TestARawDeleteOfTheBreakdownIsRefused closes the hole that guarding two of
// the three tables leaves open.
//
// Migration 000002 measured the same shape one level up and wrote that "a guard
// on the parent alone protects the number and loses the document". A breakdown
// deleted out from under a row leaves that row printing one rate for a tax
// charged under several — with its stored tax_total unchanged, so nothing about
// the document looks wrong.
func TestARawDeleteOfTheBreakdownIsRefused(t *testing.T) {
	ctx := context.Background()
	issued := issueStackedForBuyer(t, "RDB", "breakdown@example.com")
	require.Len(t, issued.Lines, 1)
	require.Len(t, issued.Lines[0].TaxComponents, 2)

	_, err := testPool.Pool().Exec(ctx,
		`DELETE FROM invoice_line_taxes WHERE invoice_line_id = $1`, issued.Lines[0].ID)
	requireRetentionRefusal(t, err)

	still, err := newService(t).GetInvoice(ctx, issued.ID)
	require.NoError(t, err)
	require.Len(t, still.Lines, 1)
	assert.Len(t, still.Lines[0].TaxComponents, 2,
		"the breakdown has to still be there")
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
// It used to assert an index on lower(buyer_email), because the predicate was
// written that way too, and the two were one decision in two files. Migration
// 000003 ended that arrangement: lower() is the CLUSTER's fold and a --locale=C
// database folds ASCII only, so the erasure now matches a column Go folded
// (ADR 0038). The pairing this test holds is therefore a plainer one — a b-tree
// on buyer_email_folded, and an equality predicate against it.
//
// The 000002 index must be GONE, and that half is not tidiness: left behind it
// would cost a write on every invoice ever issued and serve no read, and its
// presence would be the visible sign that a database had been migrated with a
// copy of 000003 that forgot to drop it.
//
// The PLAN is deliberately not asserted: the test database holds a few dozen
// rows and Postgres is right to scan them sequentially, so a plan assertion here
// would measure the row count rather than the schema and would have to be
// weakened until it measured nothing. (It was measured separately, on a table of
// 20,000 rows, where the planner reports an Index Only Scan on this index — see
// ADR 0038.)
func TestTheBuyerEmailIndexExistsAndMatchesTheQuery(t *testing.T) {
	ctx := context.Background()

	var definition string

	err := testPool.Pool().QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
		  WHERE schemaname = 'public' AND indexname = 'invoices_buyer_email_folded_idx'`).
		Scan(&definition)
	require.NoError(t, err, "migration 000003 has to create the folded buyer address index")
	assert.Contains(t, definition, "buyer_email_folded")
	assert.NotContains(t, definition, "lower(",
		"the fold belongs to Go now; an expression index here would put the cluster back in the loop")

	var leftovers int

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes
		  WHERE schemaname = 'public' AND indexname = 'invoices_buyer_email_idx'`).
		Scan(&leftovers))
	assert.Zero(t, leftovers,
		"000002's expression index served the old predicate and nothing else; 000003 drops it")
}

// TestTheFoldedHandleIsWrittenAndTheDocumentIsNot is the pair migration 000003
// splits apart, checked against a real row.
//
// The whole design rests on two columns meaning different things: buyer_email is
// what the document PRINTS and is immutable (ADR 0024), buyer_email_folded is the
// handle an erasure resolves by. A single implementation writing the folded value
// into both would pass every erasure test in this file and quietly edit a legal
// document, so the two are asserted separately and on purpose.
func TestTheFoldedHandleIsWrittenAndTheDocumentIsNot(t *testing.T) {
	ctx := context.Background()

	// A buyer nobody else in this file issues to: the tests share one database,
	// and an address that folded onto another test's person would change that
	// test's count instead of checking this one.
	issued := issueForBuyer(t, "FLD", "  Grace.Hopper@Example.COM  ")

	var printed, folded string

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT buyer_email, buyer_email_folded FROM invoices WHERE id = $1`, issued.ID).
		Scan(&printed, &folded))

	assert.Equal(t, "  Grace.Hopper@Example.COM  ", printed,
		"the document prints what it was given; an invoice is a snapshot")
	assert.Equal(t, "grace.hopper@example.com", folded,
		"the handle is what models.NormalizeEmail produces, which is what the other five holders store")
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

	result, err := svc.Erase(ctx, personaldata.Subject{Email: "ADA.LOVELACE@example.com"})
	require.NoError(t, err)

	assert.Equal(t, invoice.ModuleName, result.Holder)
	assert.Equal(t, personaldata.Retained, result.Outcome)
	assert.Equal(t, 2, result.Rows, "both spellings of the address are the same person")
	assert.NotEmpty(t, result.Why)
	assert.Contains(t, result.Kept, "invoices.metadata",
		"the free-form columns gobit never rewrites have to be named")

	// Idempotency, against the real database rather than against a fake: the
	// contract requires a second sweep to give the same answer, and here it is
	// a property of the method reading rather than writing.
	again, err := svc.Erase(ctx, personaldata.Subject{Email: "ADA.LOVELACE@example.com"})
	require.NoError(t, err)
	assert.Equal(t, result, again)
}
