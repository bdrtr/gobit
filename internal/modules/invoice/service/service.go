// Package service holds the invoice module's rules.
//
// # What this module does NOT know
//
// It does not know what an order is, what a customer is, or where the numbers
// it prints came from. It is handed a finished document — two parties, a list
// of lines, a set of totals — and it gives that document a number, stores it
// and never lets it change again. Assembling the document from an order is the
// job of a workflow (ADR 0001/0006), which is also the only place allowed to
// read two modules at once.
//
// # What it guarantees
//
// One thing, and it is the reason the module exists: the numbers in a series
// run without a gap and without a repeat. See [Service.Issue].
package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// Error codes.
const (
	// CodeInvalidInput reports that the request could not be accepted.
	CodeInvalidInput = "invoice_invalid_input"
	// CodeNotFound reports that the document does not exist.
	CodeNotFound = "invoice_not_found"
	// CodeTransition reports a status move the document may not make.
	CodeTransition = "invoice_invalid_transition"
	// CodeNumbering reports that a number could not be allocated.
	CodeNumbering = "invoice_numbering_failed"
)

// InvoiceListing names this listing inside a cursor.
//
// A cursor carries the name of the listing it belongs to so that one handed to
// a different listing is REFUSED rather than silently selecting the wrong rows.
const InvoiceListing = "invoices"

// prefixLen is the number of letters an invoice series prefix carries.
//
// Three is the Turkish e-fatura shape (3 letters + 4-digit year + 9-digit
// sequence = 16 characters). It is enforced because the length is part of the
// document's legal format, not a preference: a two-letter prefix produces a
// number the regime will not accept, and finding that out at transmission time
// means the number is already spent.
const prefixLen = 3

// sequenceDigits is the width of the sequence part of a number.
const sequenceDigits = 9

// maxSequence is the largest sequence a series can reach in a year.
const maxSequence = 999_999_999

// DefaultLimit and MaxLimit bound the listing page size.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Repo is the storage surface the service needs.
//
// It is declared HERE, on the consumer's side, so the service depends on the
// methods it calls rather than on a package.
type Repo interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
	TakeNextNumber(ctx context.Context, prefix string, year int32) (models.Series, error)
	ListSeries(ctx context.Context) ([]models.Series, error)
	CreateInvoice(ctx context.Context, in models.Invoice) (models.Invoice, error)
	GetInvoice(ctx context.Context, id string) (models.Invoice, error)
	ListInvoices(ctx context.Context, filter models.Filter) ([]models.Invoice, int64, error)
	SetStatus(
		ctx context.Context, id string, from, to models.Status, reason, providerID, externalID string,
	) (models.Invoice, error)
}

// Options are the service's settings.
type Options struct {
	// Now is where the clock comes from; nil means time.Now.
	//
	// It is injectable because the YEAR of a series is decided by it, and a test
	// that could not choose the year could not exercise the rollover at all.
	Now func() time.Time
	// Logger falls back to slog.Default when nil.
	Logger *slog.Logger
}

// Service is the invoice module's rules.
type Service struct {
	repo Repo
	now  func() time.Time
	log  *slog.Logger
}

// New builds the service.
func New(repo Repo, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Service{repo: repo, now: opts.Now, log: opts.Logger}
}

// FormatNumber builds the printed serial from a series and a sequence value.
//
// The shape is PREFIX + YEAR + a zero-padded sequence, e.g. "GBT2026000000001".
// It is produced in one place because it is a legal format rather than a
// display choice: a number formatted differently in two callers would be two
// different documents as far as the regime is concerned.
func FormatNumber(prefix string, year int32, sequence int64) string {
	return fmt.Sprintf("%s%04d%0*d", prefix, year, sequenceDigits, sequence)
}

// ListSeries returns every series the shop has.
func (s *Service) ListSeries(ctx context.Context) ([]models.Series, error) {
	return s.repo.ListSeries(ctx)
}

// GetInvoice returns the document with its lines.
func (s *Service) GetInvoice(ctx context.Context, id string) (models.Invoice, error) {
	if strings.TrimSpace(id) == "" {
		return models.Invoice{}, errors.Invalid(CodeInvalidInput, "the invoice id is required")
	}

	return s.repo.GetInvoice(ctx, id)
}

// Page is one page of the invoice listing.
type Page struct {
	// Items are the documents on this page, WITHOUT their lines.
	Items []models.Invoice
	// Count is the total number of documents matching the filter.
	Count int64
	// Limit and Offset are the bounds that were applied.
	Limit  int64
	Offset int64
	// NextCursor is the opaque position the NEXT page starts below; empty means
	// this page is the last one.
	NextCursor string
}

// ListInvoices pages the documents.
func (s *Service) ListInvoices(ctx context.Context, filter models.Filter) (Page, error) {
	limit := filter.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0:
		return Page{}, errors.Invalid(CodeInvalidInput, "the limit cannot be negative: %d", limit)
	case limit > MaxLimit:
		return Page{}, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d: %d", MaxLimit, limit)
	}
	if filter.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"the offset cannot be negative: %d", filter.Offset)
	}

	// One row MORE than asked for is fetched and the extra one is dropped
	// below: that is how "is there a next page" is answered without a second
	// query, and it is what lets the cursor be absent on the last page.
	fetch := filter
	fetch.Limit = limit + 1

	items, count, err := s.repo.ListInvoices(ctx, fetch)
	if err != nil {
		return Page{}, err
	}

	out := Page{Items: items, Count: count, Limit: limit, Offset: filter.Offset}
	if int64(len(items)) > limit {
		out.Items = items[:limit]
		last := out.Items[len(out.Items)-1]
		out.NextCursor = corepage.Encode(InvoiceListing,
			corepage.Cursor{Time: last.CreatedAt, ID: last.ID})
	}

	return out, nil
}
