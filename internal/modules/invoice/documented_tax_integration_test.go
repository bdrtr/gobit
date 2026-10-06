//go:build integration

package invoice_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	coredb "github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// This file holds ADR 0419 on Postgres: a document says when it was voided,
// the schema ties that moment to the final states, the journal's read finds a
// document that names an act by its issue and by its voiding, and migration
// 000007 fills the moment for documents voided before it.

// TestAVoidedDocumentSaysWhen: a rejection and a cancellation stamp voided_at,
// and a move to sent or accepted leaves it empty.
func TestAVoidedDocumentSaysWhen(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "AVW")

	accepted, err := svc.Issue(ctx, amendRow("AVW", sale, 1, 120, 20, models.ReasonPriceLowered))
	require.NoError(t, err)
	assert.Nil(t, accepted.VoidedAt, "an issued document stands")
	for _, to := range []models.Status{models.StatusSent, models.StatusAccepted} {
		accepted, err = svc.MoveStatus(ctx, accepted.ID, service.MoveInput{To: to})
		require.NoError(t, err)
		assert.Nil(t, accepted.VoidedAt, "a document %s stands", to)
	}

	before := time.Now().Add(-time.Second)
	rejected, err := svc.Issue(ctx, amendRow("AVW", sale, 1, 120, 20, models.ReasonPriceLowered))
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, rejected.ID, service.MoveInput{To: models.StatusSent})
	require.NoError(t, err)
	rejected, err = svc.MoveStatus(ctx, rejected.ID, service.MoveInput{To: models.StatusRejected, Reason: "refused"})
	require.NoError(t, err)
	require.NotNil(t, rejected.VoidedAt, "a rejection voids the document")
	assert.True(t, rejected.VoidedAt.After(before), "it is stamped when it is rejected")

	canceled, err := svc.Issue(ctx, amendRow("AVW", sale, 1, 120, 20, models.ReasonPriceLowered))
	require.NoError(t, err)
	canceled, err = svc.MoveStatus(ctx, canceled.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	require.NoError(t, err)
	require.NotNil(t, canceled.VoidedAt, "a cancellation voids the document")

	read, err := svc.GetInvoice(ctx, canceled.ID)
	require.NoError(t, err)
	require.NotNil(t, read.VoidedAt)
	assert.True(t, read.VoidedAt.Equal(*canceled.VoidedAt), "the stamp is stored, not computed")
}

// TestTheSchemaTiesVoidedAtToTheFinalStates writes past the service and
// asserts the server's own report of the constraint, both ways.
func TestTheSchemaTiesVoidedAtToTheFinalStates(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "AVS")
	live, err := svc.Issue(ctx, amendRow("AVS", sale, 1, 120, 20, models.ReasonPriceLowered))
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `UPDATE invoices SET voided_at = now() WHERE id = $1`, live.ID)
	require.Error(t, err, "a standing document carries no voiding")
	assert.Contains(t, err.Error(), `check constraint "invoices_voided_when_final"`)

	_, err = testPool.Pool().Exec(ctx, `UPDATE invoices SET status = 'canceled' WHERE id = $1`, live.ID)
	require.Error(t, err, "a voided document carries its moment")
	assert.Contains(t, err.Error(), `check constraint "invoices_voided_when_final"`)
}

// documentedRead is one element of the journal's read.
type documentedRead struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	AmendmentKey string     `json:"amendment_key"`
	TaxTotal     int64      `json:"tax_total"`
	CurrencyCode string     `json:"currency_code"`
	IssuedAt     time.Time  `json:"issued_at"`
	VoidedAt     *time.Time `json:"voided_at"`
}

// documentedIn reads the journal's answer over [from, to) and keeps the
// documents among ids, by id.
func documentedIn(
	t *testing.T, from, to time.Time, currency string, ids ...string,
) map[string]documentedRead {
	t.Helper()

	raw, err := service.NewInterop(newService(t)).DocumentedTaxJSON(context.Background(), from, to, currency)
	require.NoError(t, err)
	var all []documentedRead
	require.NoError(t, json.Unmarshal(raw, &all))
	out := map[string]documentedRead{}
	for _, document := range all {
		for _, id := range ids {
			if document.ID == id {
				out[id] = document
			}
		}
	}

	return out
}

// TestTheJournalFindsADocumentByItsIssueAndItsVoiding: a document that names an
// act is read in the window it was issued in, still after it is voided, and in
// the window it was voided in; a document naming no act is never read, and the
// currency filter holds.
func TestTheJournalFindsADocumentByItsIssueAndItsVoiding(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "AJF")

	start := time.Now().Add(-time.Second)
	keyed := amendRow("AJF", sale, 1, 120, 20, models.ReasonReturned)
	keyed.AmendmentKey = "return_refunded:re_journal"
	document, err := svc.Issue(ctx, keyed)
	require.NoError(t, err)
	keyless, err := svc.Issue(ctx, amendRow("AJF", sale, 1, 120, 20, models.ReasonReturned))
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	issuedUntil := time.Now()
	time.Sleep(50 * time.Millisecond)

	read := documentedIn(t, start, issuedUntil, "TRY", document.ID, keyless.ID)
	require.Contains(t, read, document.ID, "the document is read in the window it was issued in")
	assert.NotContains(t, read, keyless.ID, "a document naming no act is not read")
	assert.Equal(t, documentedRead{
		ID: document.ID, Kind: "refund", AmendmentKey: "return_refunded:re_journal", TaxTotal: 20,
		CurrencyCode: "TRY", IssuedAt: document.IssuedAt.UTC(),
	}, read[document.ID])
	assert.Empty(t, documentedIn(t, start, issuedUntil, "EUR", document.ID), "the currency filter holds")

	_, err = svc.MoveStatus(ctx, document.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	require.NoError(t, err)
	end := time.Now().Add(time.Second)

	again := documentedIn(t, start, issuedUntil, "", document.ID)
	require.Contains(t, again, document.ID, "a later voiding leaves the document in the window of its issue")
	require.NotNil(t, again[document.ID].VoidedAt)
	voided := documentedIn(t, issuedUntil, end, "", document.ID)
	require.Contains(t, voided, document.ID, "the document is read in the window it was voided in")
	assert.False(t, voided[document.ID].VoidedAt.Before(issuedUntil))
}

// TestMigration000007FillsTheMomentOfDocumentsVoidedBefore writes a rejected
// and a standing document at version 6, migrates over them, and reads what
// voided_at became: the rejected one's updated_at, and nothing for the other.
// It runs on its own container, as the 000003 backfill test does, because the
// shared one is migrated to head and its rows cannot be removed.
func TestMigration000007FillsTheMomentOfDocumentsVoidedBefore(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_voided"),
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

	// Head first, then back to the version before 000007, derived from head.
	const beforeVersion uint = 6
	require.NoError(t, coredb.Migrate(ctx, dsn, source, invoice.ModuleName))
	head, dirty, err := coredb.Version(ctx, dsn, invoice.ModuleName)
	require.NoError(t, err)
	require.False(t, dirty)
	require.Greater(t, head, beforeVersion)
	require.NoError(t, coredb.MigrateDown(ctx, dsn, source, invoice.ModuleName, int(head-beforeVersion)))

	pool, err := coredb.New(ctx, coredb.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Pool().Exec(ctx,
		`INSERT INTO invoice_series (id, prefix, year, last_number) VALUES ('iser_voided', 'VD', 2026, 3)`)
	require.NoError(t, err)
	// The issue, the last status write and the database's own clock are three
	// different moments, so a backfill from the wrong column cannot pass.
	issuedAt := time.Date(2026, time.March, 1, 9, 0, 0, 0, time.UTC)
	movedAt := map[string]time.Time{
		"inv_rejected": time.Date(2026, time.March, 3, 10, 0, 0, 0, time.UTC),
		"inv_canceled": time.Date(2026, time.March, 4, 11, 0, 0, 0, time.UTC),
		"inv_standing": time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
	}
	status := map[string]string{"inv_rejected": "rejected", "inv_canceled": "canceled", "inv_standing": "accepted"}
	for id, at := range movedAt {
		_, err = pool.Pool().Exec(ctx,
			`INSERT INTO invoices (id, number, series_id, kind, status, currency_code,
			   seller_name, buyer_name, buyer_email_folded, subtotal, total, issued_at, updated_at)
			 VALUES ($1, $1, 'iser_voided', 'sale', $2, 'TRY', 'Seller', 'Buyer', '', 0, 0, $3, $4)`,
			id, status[id], issuedAt, at)
		require.NoError(t, err, "the document %s could not be written at version 6", id)
	}

	require.NoError(t, coredb.Migrate(ctx, dsn, source, invoice.ModuleName))

	voided := func(id string) *time.Time {
		var at *time.Time
		require.NoError(t, pool.Pool().QueryRow(ctx, `SELECT voided_at FROM invoices WHERE id = $1`, id).Scan(&at))
		return at
	}
	for _, id := range []string{"inv_rejected", "inv_canceled"} {
		at := voided(id)
		require.NotNil(t, at, "%s, voided before the migration, says when", id)
		assert.True(t, at.Equal(movedAt[id]),
			"%s's moment is its last status write, which updated_at kept: %s, not %s", id, at, movedAt[id])
	}
	assert.Nil(t, voided("inv_standing"), "a standing document says nothing")
}
