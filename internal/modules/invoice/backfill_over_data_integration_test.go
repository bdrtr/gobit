//go:build integration

package invoice_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	coredb "github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// This file runs a migration FORWARD OVER EXISTING DATA, which is the one
// migration shape the suite never exercised.
//
// Every other migration test in this tree starts from an empty database. A
// migration that only has to create a table is correct for a shape nobody has
// yet; the interesting ones REWRITE what is already there, and this repository
// shipped exactly one of those with a known limitation written into its own
// ADR.
//
// # What it reproduces
//
// ADR 0038 moved the e-mail fold out of SQL and into Go, and said plainly what
// its own backfill could not do: `lower(btrim(buyer_email))` is "correct for
// ASCII and only for ASCII", so the rows the defect is about are the rows the
// backfill gets wrong. The remedy is a Go pass, `gobit refold-invoices`.
//
// Nothing checked that. The sentence stood in a record, the backfill ran on an
// empty table in every test, and the one thing that would show the gap — a row
// written BEFORE the migration — never existed.
//
// # The pair of rows is the whole design
//
// `btrim` with one argument strips SPACES. It does not strip a tab or a
// newline, and Go's strings.TrimSpace does. So:
//
//	"  Bob@X.com  "   SQL -> "bob@x.com"     Go -> "bob@x.com"     agree
//	"\tAda@X.com\n"   SQL -> "\tada@x.com\n" Go -> "ada@x.com"     DIFFER
//
// The second row is the defect; the first is what keeps the test from passing
// because the backfill does nothing at all.
//
// # It asserts the CONSEQUENCE, not the column
//
// A wrong handle is not the failure anybody cares about. The failure is that a
// person exercising a legal right is told "we looked and you are not here"
// while their invoice sits in the table — and that is what is asserted, through
// the module's own erasure, before and after the corrective pass.
//
// # Its own container
//
// The package's shared one is migrated to head by TestMain, and 000002 installs
// invoices_no_delete and invoices_no_truncate as ENABLE ALWAYS triggers — so a
// row written here could not be cleaned up afterwards. This test needs a
// database at version 2, which is a different database.

// TestTheBackfillOfMigration000003IsWhatTheGoPassMustCorrect writes two legacy
// rows, migrates over them and asks the module whether a data subject can be
// found. The file header above is the argument; this is the walk.
func TestTheBackfillOfMigration000003IsWhatTheGoPassMustCorrect(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_backfill"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	t.Cleanup(func() {
		if termErr := testcontainers.TerminateContainer(container); termErr != nil {
			t.Logf("the postgres container could not be stopped: %v", termErr)
		}
	})
	require.NoError(t, err, "the postgres container could not be started")

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	source := invoice.New(invoice.Options{}).Migrations()

	// Head first, then back to 2. Migrating straight to 2 would never run
	// 000003's down leg, and this test is about the pair.
	require.NoError(t, coredb.Migrate(ctx, dsn, source, invoice.ModuleName))
	require.NoError(t, coredb.MigrateDown(ctx, dsn, source, invoice.ModuleName, 1))

	version, dirty, err := coredb.Version(ctx, dsn, invoice.ModuleName)
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(2), version,
		"the database is not at the version the legacy rows belong to")

	pool, err := coredb.New(ctx, coredb.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	const (
		spacedRaw = "  Bob@X.com  "
		tabbedRaw = "\tAda@X.com\n"
	)

	// Raw SQL, and it is not a shortcut: the repository's INSERT names
	// buyer_email_folded, and that column does not exist at version 2. A legacy
	// row can only be written the way a legacy row was written.
	_, err = pool.Pool().Exec(ctx,
		`INSERT INTO invoice_series (id, prefix, year, last_number)
		 VALUES ('iser_backfill', 'BF', 2026, 2)`)
	require.NoError(t, err, "the series a legacy invoice hangs from could not be written")

	for id, raw := range map[string]string{"inv_spaced": spacedRaw, "inv_tabbed": tabbedRaw} {
		_, err = pool.Pool().Exec(ctx,
			`INSERT INTO invoices (
				id, number, series_id, kind, status, currency_code,
				seller_name, buyer_name, buyer_email,
				subtotal, discount_total, tax_total, total, issued_at)
			 VALUES ($1, $2, 'iser_backfill', 'invoice', 'issued', 'TRY',
				'A seller', 'A buyer', $3,
				1000, 0, 0, 1000, now())`,
			id, "BF-2026-"+id, raw)
		require.NoError(t, err, "the legacy invoice %s could not be written", id)
	}

	// The migration now runs over rows that already exist.
	require.NoError(t, coredb.Migrate(ctx, dsn, source, invoice.ModuleName))

	stored := func(id string) string {
		var handle string
		require.NoError(t, pool.Pool().QueryRow(ctx,
			`SELECT buyer_email_folded FROM invoices WHERE id = $1`, id).Scan(&handle))

		return handle
	}

	// The handles are written out rather than computed. A computed expectation
	// would be whatever the backfill produced, which is the thing under test.
	assert.Equal(t, "bob@x.com", stored("inv_spaced"),
		"the SQL backfill did not fold the ordinary row; every assertion below rests on it "+
			"having worked where it can")
	assert.Equal(t, "\tada@x.com\n", stored("inv_tabbed"),
		"btrim strips SPACES and this row carries a tab and a newline, so the SQL fold "+
			"cannot produce what Go produces — that is the limitation ADR 0038 wrote down")

	require.NotEqual(t, models.NormalizeEmail(tabbedRaw), stored("inv_tabbed"),
		"the SQL backfill and the Go fold agree on this row, so it demonstrates nothing "+
			"and the corrective pass below would prove nothing either")

	svc := service.New(repository.New(pool.Pool()), service.Options{})

	// THE CONSEQUENCE. Not the column: a controller answering a data subject.
	spacedResult, err := svc.Erase(ctx, personaldata.Subject{Email: spacedRaw})
	require.NoError(t, err)
	assert.Equal(t, 1, spacedResult.Rows,
		"the row the backfill folded correctly cannot be found by its own address")

	tabbedResult, err := svc.Erase(ctx, personaldata.Subject{Email: tabbedRaw})
	require.NoError(t, err)
	assert.Zero(t, tabbedResult.Rows,
		"this row IS findable before the corrective pass, so the pass has nothing to fix "+
			"and this test is measuring a defect that is not there")
	assert.Contains(t, strings.ToLower(tabbedResult.Why), "looked for",
		"the report does not say the address was looked for; that sentence is what a "+
			"controller repeats to a person, and it is false while the document is held")

	// The Go pass is the remedy the record names.
	report, err := svc.RefoldBuyerEmails(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, report.Examined)
	assert.Equal(t, 1, report.Rewritten,
		"the pass rewrote %d handles; exactly one row was folded differently by SQL and Go",
		report.Rewritten)
	assert.Equal(t, models.NormalizeEmail(tabbedRaw), stored("inv_tabbed"))

	afterRefold, err := svc.Erase(ctx, personaldata.Subject{Email: tabbedRaw})
	require.NoError(t, err)
	assert.Equal(t, 1, afterRefold.Rows,
		"the document is still not found by its own address after the corrective pass")

	// Idempotence: an operator running it twice must not be told there was
	// something to fix the second time.
	again, err := svc.RefoldBuyerEmails(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, again.Examined)
	assert.Zero(t, again.Rewritten,
		"a second pass rewrote %d handles; the report an operator reads would say the "+
			"defect keeps coming back", again.Rewritten)
}
