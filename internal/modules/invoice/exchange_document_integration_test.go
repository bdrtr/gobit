//go:build integration

package invoice_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

// This file holds ADR 0432's documents on Postgres: an exchange's sale carries
// the reason `exchanged`, which fits a sale only, and the schema refuses to be
// rolled back past 000008 while a document of an exchange stands.

// rawAmendment writes an amending document past the service, with the given
// kind, reason and key, and returns what the server answered.
func rawAmendment(ctx context.Context, pool *db.Pool, id, seriesID, kind, saleID, reason, key string) error {
	_, err := pool.Pool().Exec(ctx,
		`INSERT INTO invoices (id, number, series_id, kind, status, currency_code,
		   seller_name, buyer_name, buyer_email_folded, subtotal, total, issued_at,
		   amends_invoice_id, amendment_reason, amendment_key)
		 VALUES ($1, $1, $2, $3, 'issued', 'TRY', 'Seller', 'Buyer', '', 0, 0, now(), $4, $5, NULLIF($6, ''))`,
		id, seriesID, kind, saleID, reason, key)

	return err
}

// TestTheSchemaHoldsAnExchangesReasonToASale writes past the service and
// asserts the server's own report of each constraint 000008 replaced: goods an
// exchange sends are a sale, never a refund, and a return is never a sale.
func TestTheSchemaHoldsAnExchangesReasonToASale(t *testing.T) {
	ctx := context.Background()
	sale := amendedSale(t, newService(t), "AXR")

	for name, refused := range map[string]struct{ id, kind, reason string }{
		"invoices_amendment_reason_fits":  {"inv_xr_refund", "refund", "exchanged"},
		"invoices_amendment_reason_known": {"inv_xr_unknown", "refund", "exchange"},
	} {
		err := rawAmendment(ctx, testPool, refused.id, sale.SeriesID, refused.kind, sale.ID, refused.reason, "")
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `check constraint "`+name+`"`)
	}
	err := rawAmendment(ctx, testPool, "inv_xr_return", sale.SeriesID, "sale", sale.ID, "returned", "")
	require.Error(t, err, "a return is a refund's")
	assert.Contains(t, err.Error(), `check constraint "invoices_amendment_reason_fits"`)

	require.NoError(t, rawAmendment(ctx, testPool, "inv_xr_sale", sale.SeriesID, "sale", sale.ID, "exchanged",
		"exchange_sent:exch_raw"), "an exchange's sale is the schema's")
	require.NoError(t, rawAmendment(ctx, testPool, "inv_xr_price", sale.SeriesID, "sale", sale.ID, "price_raised", ""),
		"a price raised is still a sale's")
}

// TestAnExchangesDocumentsAreIssuedOnPostgres issues the two documents the
// invoicing flow writes for an exchange: a refund of a sale row's unit under
// exchange_returned and a sale adding a row of its own under exchange_sent,
// and a second sale under the same key is refused.
func TestAnExchangesDocumentsAreIssuedOnPostgres(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "AXD")

	back := amendRow("AXD", sale, 1, 1200, 200, models.ReasonReturned)
	back.AmendmentKey = "exchange_returned:exch_pg"
	refund, err := svc.Issue(ctx, back)
	require.NoError(t, err, "the unit that came back is given back from its row")
	assert.Equal(t, models.KindRefund, refund.Kind)

	sent := issueFor("AXD")
	sent.Buyer = models.Party{}
	sent.Amends, sent.AmendmentReason, sent.AmendmentKey = sale.ID, models.ReasonExchanged, "exchange_sent:exch_pg"
	sent.Lines = []service.LineInput{{
		Description: "Jacket", Quantity: 1, UnitPrice: 3000, Subtotal: 3000,
		TaxRateBps: 100, TaxTotal: 30, Total: 3030,
	}}
	sent.Subtotal, sent.TaxTotal, sent.Total = 3000, 30, 3030
	sold, err := svc.Issue(ctx, sent)
	require.NoError(t, err, "a row the sale did not have, at its own rate")
	assert.Equal(t, models.ReasonExchanged, sold.AmendmentReason)
	assert.Empty(t, sold.Lines[0].AmendsLineID)

	_, err = svc.Issue(ctx, sent)
	require.Error(t, err, "one live document per key")
	assert.Equal(t, service.CodeAmendmentExists, coreerrors.CodeOf(err))
}

// TestARollbackRefusesADocumentOfAnExchange keeps 000008's down migration from
// taking away the kinds an exchange's documents name while one stands: the
// order journal could not place its tax and would refuse every read of its
// window. Each half is written alone on a database of its own, since the
// refund carries a reason the rollback keeps and only its key stops it.
func TestARollbackRefusesADocumentOfAnExchange(t *testing.T) {
	ctx := context.Background()

	for name, document := range map[string]struct{ kind, reason, key string }{
		"the refund of what came back": {"refund", "returned", "exchange_returned:exch_rb"},
		"the sale of what was sent":    {"sale", "exchanged", "exchange_sent:exch_rb"},
	} {
		dsn := testdb.New(t, testDSN, "invoice_exchange_rollback")
		source := invoice.New(invoice.Options{}).Migrations()
		require.NoError(t, db.Migrate(ctx, dsn, source, invoice.ModuleName))
		pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
		require.NoError(t, err)
		t.Cleanup(pool.Close)
		sale := amendedSale(t, service.New(repository.New(pool.Pool()), service.Options{}), "AXB")
		require.NoError(t, rawAmendment(ctx, pool, "inv_rb", sale.SeriesID, document.kind, sale.ID,
			document.reason, document.key), name)

		head, dirty, err := db.Version(ctx, dsn, invoice.ModuleName)
		require.NoError(t, err)
		require.False(t, dirty)
		require.GreaterOrEqual(t, head, uint(8))
		err = db.MigrateDown(ctx, dsn, source, invoice.ModuleName, int(head-8)+1)

		require.Error(t, err, "%s: the rollback took away the kind its key names", name)
		// The server's report quotes the constraint; the bare name is also in
		// the migration's own text, which the error carries (D148).
		assert.Contains(t, err.Error(), `check constraint "invoices_carries_no_exchange"`, name)
	}
}
