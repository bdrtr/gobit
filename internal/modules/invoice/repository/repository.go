// Package repository is the invoice module's data access layer.
//
// # Conversion stays here
//
// pgtype and the generated row types DO NOT LEAVE this package: the service
// speaks in domain models. The boundary is what keeps a database detail — a
// nullable column, a numeric type — from becoming a fact the whole module has
// to know about.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository/invoicedb"
)

// Error codes.
const (
	codeQueryFailed = "invoice_query_failed"
	codeNotFound    = "invoice_not_found"
	codeTxBegin     = "invoice_tx_begin_failed"
	codeTxCommit    = "invoice_tx_commit_failed"
	codeTxRequired  = "invoice_tx_required"
	codeConflict    = "invoice_conflict"
)

// rollbackTimeout is the budget for the rollback of an interrupted transaction.
const rollbackTimeout = 5 * time.Second

// txContextKey is the context key carrying the open transaction.
type txContextKey struct{}

// txKey is the single instance of the key.
var txKey = txContextKey{}

// Repository reads and writes the invoice module's tables.
type Repository struct {
	pool *pgxpool.Pool
}

// New builds a repository over the given pool.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// WithTx runs fn inside a single database transaction.
//
// The context given to fn carries the transaction; every repository method
// called with that context runs in the same transaction. On an error or a panic
// the transaction is rolled back.
//
// A nested call does NOT open a second transaction: in PostgreSQL that means a
// savepoint, and the number allocation this module rests on has to be one
// atomic unit rather than a unit that an inner rollback can partly undo.
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, codeTxBegin, "the transaction could not be begun")
	}

	committed := false

	defer func() {
		if committed {
			return
		}
		// A context independent of the caller's: a rollback made with a context
		// the caller has already canceled would fail instantly.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, codeTxCommit, "the transaction could not be committed")
	}

	committed = true

	return nil
}

// txFromContext returns the transaction handle in the context.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)

	return tx, ok
}

// queries returns the query set matching the context: the one bound to the
// transaction if there is one, otherwise the one bound to the pool.
func (r *Repository) queries(ctx context.Context) *invoicedb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return invoicedb.New(tx)
	}

	return invoicedb.New(r.pool)
}

// TakeNextNumber opens the series if it is new and takes the next number.
//
// It REFUSES to run outside a transaction. The statement's own row lock is what
// serializes two concurrent issues, but a lock is only held until its
// transaction ends — outside one, that is the statement itself, and the number
// would be spent the moment it was taken. Gap-freeness needs the increment and
// the document to commit or roll back TOGETHER.
func (r *Repository) TakeNextNumber(
	ctx context.Context, prefix string, year int32,
) (models.Series, error) {
	if _, ok := txFromContext(ctx); !ok {
		return models.Series{}, coreerrors.Internal(codeTxRequired,
			"the number has to be taken inside the transaction that writes the invoice; "+
				"outside one, a failed issue would leave the number spent and a gap in the series")
	}

	row, err := r.queries(ctx).TakeNextNumber(ctx, invoicedb.TakeNextNumberParams{
		ID:     models.NewSeriesID(),
		Prefix: prefix,
		Year:   year,
	})
	if err != nil {
		return models.Series{}, wrapDB(err, codeQueryFailed, "the next invoice number could not be taken")
	}

	return toSeries(row), nil
}

// CreateSeries opens a new series.
func (r *Repository) CreateSeries(ctx context.Context, s models.Series) (models.Series, error) {
	row, err := r.queries(ctx).CreateSeries(ctx, invoicedb.CreateSeriesParams{
		ID:     s.ID,
		Prefix: s.Prefix,
		Year:   s.Year,
	})
	if err != nil {
		return models.Series{}, wrapDB(err, codeQueryFailed, "the series could not be created")
	}

	return toSeries(row), nil
}

// SeriesByPrefixYear returns the series of the given prefix and year.
func (r *Repository) SeriesByPrefixYear(
	ctx context.Context, prefix string, year int32,
) (models.Series, error) {
	row, err := r.queries(ctx).GetSeriesByPrefixYear(ctx, invoicedb.GetSeriesByPrefixYearParams{
		Prefix: prefix,
		Year:   year,
	})
	if err != nil {
		return models.Series{}, wrapDB(err, codeNotFound, "the series could not be read")
	}

	return toSeries(row), nil
}

// ListSeries returns every series, newest year first.
func (r *Repository) ListSeries(ctx context.Context) ([]models.Series, error) {
	rows, err := r.queries(ctx).ListSeries(ctx)
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "the series could not be listed")
	}

	out := make([]models.Series, 0, len(rows))
	for i := range rows {
		out = append(out, toSeries(rows[i]))
	}

	return out, nil
}

// CreateInvoice writes the document and its lines.
//
// It has to be called inside a transaction: the document and the series advance
// that produced its number are one unit, and a document written without that
// advance would hand the same number out twice.
func (r *Repository) CreateInvoice(ctx context.Context, in models.Invoice) (models.Invoice, error) {
	if _, ok := txFromContext(ctx); !ok {
		return models.Invoice{}, coreerrors.Internal(codeTxRequired,
			"the invoice has to be written inside the transaction that advanced its series; "+
				"outside one, a rollback would take the document and leave the number spent")
	}

	metadata, err := json.Marshal(orEmptyMap(in.Metadata))
	if err != nil {
		return models.Invoice{}, coreerrors.Internal(codeQueryFailed,
			"the invoice metadata could not be encoded: %v", err)
	}

	row, err := r.queries(ctx).CreateInvoice(ctx, invoicedb.CreateInvoiceParams{
		ID:                in.ID,
		Number:            in.Number,
		SeriesID:          in.SeriesID,
		Kind:              in.Kind.String(),
		Status:            in.Status.String(),
		CurrencyCode:      in.CurrencyCode,
		SellerName:        in.Seller.Name,
		SellerTaxNumber:   in.Seller.TaxNumber,
		SellerTaxOffice:   in.Seller.TaxOffice,
		SellerEmail:       in.Seller.Email,
		SellerAddress:     in.Seller.Address,
		SellerCountryCode: in.Seller.CountryCode,
		BuyerName:         in.Buyer.Name,
		BuyerTaxNumber:    in.Buyer.TaxNumber,
		BuyerTaxOffice:    in.Buyer.TaxOffice,
		BuyerEmail:        in.Buyer.Email,
		BuyerAddress:      in.Buyer.Address,
		BuyerCountryCode:  in.Buyer.CountryCode,
		Subtotal:          in.Subtotal,
		DiscountTotal:     in.DiscountTotal,
		TaxTotal:          in.TaxTotal,
		Total:             in.Total,
		IssuedAt:          fromTime(in.IssuedAt),
		Metadata:          metadata,
	})
	if err != nil {
		return models.Invoice{}, wrapDB(err, codeQueryFailed, "the invoice could not be written")
	}

	out, err := toInvoice(row)
	if err != nil {
		return models.Invoice{}, err
	}

	for i := range in.Lines {
		line := in.Lines[i]

		lineRow, lineErr := r.queries(ctx).CreateInvoiceLine(ctx, invoicedb.CreateInvoiceLineParams{
			ID:            line.ID,
			InvoiceID:     out.ID,
			Position:      line.Position,
			Description:   line.Description,
			Quantity:      line.Quantity,
			UnitPrice:     line.UnitPrice,
			Subtotal:      line.Subtotal,
			DiscountTotal: line.DiscountTotal,
			TaxRateBps:    line.TaxRateBps,
			TaxTotal:      line.TaxTotal,
			Total:         line.Total,
		})
		if lineErr != nil {
			return models.Invoice{}, wrapDB(lineErr, codeQueryFailed,
				"the invoice line could not be written")
		}

		out.Lines = append(out.Lines, toLine(lineRow))
	}

	return out, nil
}

// GetInvoice returns the document with its lines.
func (r *Repository) GetInvoice(ctx context.Context, id string) (models.Invoice, error) {
	row, err := r.queries(ctx).GetInvoice(ctx, id)
	if err != nil {
		return models.Invoice{}, wrapDB(err, codeNotFound, "the invoice could not be read")
	}

	out, err := toInvoice(row)
	if err != nil {
		return models.Invoice{}, err
	}

	lines, err := r.queries(ctx).ListInvoiceLines(ctx, id)
	if err != nil {
		return models.Invoice{}, wrapDB(err, codeQueryFailed, "the invoice lines could not be read")
	}

	for i := range lines {
		out.Lines = append(out.Lines, toLine(lines[i]))
	}

	return out, nil
}

// ListInvoices pages the documents and returns the total matching count.
//
// The lines are NOT loaded: a page of documents with all their lines would be
// an N+1 waiting to happen and a listing nobody needs in that shape. The lines
// come with [Repository.GetInvoice].
func (r *Repository) ListInvoices(
	ctx context.Context, filter models.Filter,
) ([]models.Invoice, int64, error) {
	// The cursor arrives as SQL NULL when it names no position; the COALESCE
	// sentinels in the query turn that into "start at the top". A zero TIME sent
	// instead would make the first page come back empty with no error anywhere.
	afterAt, afterID := cursorBounds(filter.After)

	rows, err := r.queries(ctx).ListInvoices(ctx, invoicedb.ListInvoicesParams{
		Status:    filter.Status,
		Kind:      filter.Kind,
		AfterAt:   afterAt,
		AfterID:   afterID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the invoices could not be listed")
	}

	total, err := r.queries(ctx).CountInvoices(ctx, invoicedb.CountInvoicesParams{
		Status: filter.Status,
		Kind:   filter.Kind,
	})
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the invoices could not be counted")
	}

	out := make([]models.Invoice, 0, len(rows))

	for i := range rows {
		invoice, convErr := toInvoice(rows[i])
		if convErr != nil {
			return nil, 0, convErr
		}

		out = append(out, invoice)
	}

	return out, total, nil
}

// SetStatus moves the document and returns the row it wrote.
//
// The move is decided by the DATABASE: the update carries the status the caller
// believed the document was in, so two operators acting at the same time cannot
// both win. A move that matched no row comes back as a conflict rather than as
// "not found", because the document does exist — it simply is not where the
// caller thought.
func (r *Repository) SetStatus(
	ctx context.Context, id string, from, to models.Status, reason, providerID, externalID string,
) (models.Invoice, error) {
	row, err := r.queries(ctx).SetInvoiceStatus(ctx, invoicedb.SetInvoiceStatusParams{
		ID:            id,
		CurrentStatus: from.String(),
		NextStatus:    to.String(),
		StatusReason:  reason,
		ProviderID:    optional(providerID),
		ExternalID:    optional(externalID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Invoice{}, coreerrors.Conflict(codeConflict,
				"invoice %s is no longer in status %q, so it cannot be moved to %q", id, from, to)
		}

		return models.Invoice{}, wrapDB(err, codeQueryFailed, "the invoice status could not be written")
	}

	return toInvoice(row)
}
