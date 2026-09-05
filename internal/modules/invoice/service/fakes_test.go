package service_test

import (
	"context"
	"fmt"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// fakeRepo is an in-memory stand-in for the repository.
//
// It imitates the two behaviors the rules depend on: the series advance returns
// the NEW value, and a status write refuses a row whose current status is not
// the one the caller believed. It does NOT imitate the transaction, because
// there is nothing in memory for a rollback to undo — the gap-free guarantee
// rests on real transactional behavior and is proven against a real database in
// the module's integration test, not here.
type fakeRepo struct {
	mu sync.Mutex

	series   map[string]models.Series
	invoices map[string]models.Invoice

	// takeErr and createErr script a failure.
	takeErr   error
	createErr error

	// listResult and listCount are what the listing returns.
	listResult []models.Invoice
	listCount  int64
	// listFilter records what the listing was asked for.
	listFilter models.Filter
}

// newFakeRepo builds an empty fake.
func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		series:   map[string]models.Series{},
		invoices: map[string]models.Invoice{},
	}
}

// WithTx runs fn directly: there is no transaction to open in memory.
func (f *fakeRepo) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// TakeNextNumber opens the series if it is new and takes the next number, as
// the real upsert does.
func (f *fakeRepo) TakeNextNumber(
	_ context.Context, prefix string, year int32,
) (models.Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.takeErr != nil {
		return models.Series{}, f.takeErr
	}

	for id, s := range f.series {
		if s.Prefix == prefix && s.Year == year {
			s.LastNumber++
			f.series[id] = s

			return s, nil
		}
	}

	opened := models.Series{
		ID:         models.NewSeriesID(),
		Prefix:     prefix,
		Year:       year,
		LastNumber: 1,
	}
	f.series[opened.ID] = opened

	return opened, nil
}

// ListSeries returns every series.
func (f *fakeRepo) ListSeries(_ context.Context) ([]models.Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.Series, 0, len(f.series))
	for _, s := range f.series {
		out = append(out, s)
	}

	return out, nil
}

// CreateInvoice stores the document.
func (f *fakeRepo) CreateInvoice(_ context.Context, in models.Invoice) (models.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return models.Invoice{}, f.createErr
	}

	for id := range f.invoices {
		if f.invoices[id].Number == in.Number {
			return models.Invoice{}, errors.Conflict("fake_number_taken",
				"number %s is already used", in.Number)
		}
	}

	f.invoices[in.ID] = in

	return in, nil
}

// GetInvoice returns the stored document.
func (f *fakeRepo) GetInvoice(_ context.Context, id string) (models.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, ok := f.invoices[id]
	if !ok {
		return models.Invoice{}, errors.NotFound("fake_invoice_missing", "no such invoice")
	}

	return in, nil
}

// ListInvoices returns the scripted page and records the filter.
func (f *fakeRepo) ListInvoices(
	_ context.Context, filter models.Filter,
) ([]models.Invoice, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listFilter = filter

	return f.listResult, f.listCount, nil
}

// SetStatus writes the status only when the current one matches.
func (f *fakeRepo) SetStatus(
	_ context.Context, id string, from, to models.Status, reason, providerID, externalID string,
) (models.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, ok := f.invoices[id]
	if !ok {
		return models.Invoice{}, errors.NotFound("fake_invoice_missing", "no such invoice")
	}
	if in.Status != from {
		return models.Invoice{}, errors.Conflict("fake_status_moved",
			"invoice %s is no longer in status %q", id, from)
	}

	in.Status = to
	in.StatusReason = reason
	if providerID != "" {
		in.ProviderID = providerID
	}
	if externalID != "" {
		in.ExternalID = externalID
	}
	f.invoices[id] = in

	return in, nil
}

// seed writes a document straight into the fake.
func (f *fakeRepo) seed(in models.Invoice) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.invoices[in.ID] = in
}

// validIssue is a request that passes every rule; a test changes the one field
// it is about.
func validIssue() service.IssueInput {
	return service.IssueInput{
		SeriesPrefix: "GBT",
		Kind:         models.KindSale,
		CurrencyCode: "TRY",
		Seller:       models.Party{Name: "Gobit Shop", TaxNumber: "1234567890", CountryCode: "TR"},
		Buyer:        models.Party{Name: "A Customer", CountryCode: "TR"},
		Lines: []service.LineInput{{
			Description: "Red T-Shirt",
			Quantity:    2,
			UnitPrice:   1000,
			Subtotal:    2000,
			TaxRateBps:  2000,
			TaxTotal:    400,
			Total:       2400,
		}},
		Subtotal: 2000,
		TaxTotal: 400,
		Total:    2400,
	}
}

// issuedInvoice is a stored document in the given status.
func issuedInvoice(id string, status models.Status) models.Invoice {
	return models.Invoice{
		ID:           id,
		Number:       fmt.Sprintf("GBT2026%09d", 1),
		Kind:         models.KindSale,
		Status:       status,
		CurrencyCode: "TRY",
		Total:        2400,
	}
}
