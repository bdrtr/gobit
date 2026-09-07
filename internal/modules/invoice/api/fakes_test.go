package api_test

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// memRepo is an in-memory stand-in for the invoice repository.
//
// The HTTP tests are run against the REAL service with only the storage faked,
// which is what makes them worth writing: the claim being checked is that a
// handler does not CHOOSE a status code — it hands the service's error to
// core/http, and the error's kind decides. Behind a stubbed service that
// returns whatever a test told it to, that claim cannot fail, because the test
// would be picking the kind it then asserts on.
//
// What this fake deliberately does NOT imitate:
//
//   - The transaction. There is nothing in memory for a rollback to undo, and
//     the gap-free guarantee rests on real transactional behavior; it is proven
//     against Postgres in the module's integration test.
//   - The keyset cursor. [memRepo.ListInvoices] RECORDS the position it was
//     given and does not apply it. A cursor honored by a fake would prove that
//     the fake compares two structs, not that the SQL row comparison seeks the
//     right rows, so following a cursor across pages is asserted in the
//     integration test instead. What the HTTP tests hold is the half that
//     really is theirs: whether the cursor was decoded at all, whether it was
//     refused when it names another listing, and whether the envelope offers a
//     next position only when there is one.
type memRepo struct {
	mu sync.Mutex

	// series holds the number series by id.
	series map[string]models.Series
	// stored holds the documents in the order they were written, oldest first.
	stored []models.Invoice

	// writes counts the documents written, so a stored row can be given a
	// created_at that increases the way the real column's default does.
	writes int

	// listFilter records what the last listing was asked for, so a test can
	// show that a query parameter reached the service instead of being dropped
	// on the way through the handler.
	listFilter models.Filter
}

// That the fake satisfies the surface the service needs is pinned down at
// compile time: a method added to [service.Repo] would otherwise leave these
// tests compiling against a repository that no longer exists.
var _ service.Repo = (*memRepo)(nil)

// firstStoredAt is the created_at of the first document written to the fake.
//
// The stored timestamps have to be distinct and increasing, because the listing
// orders by created_at and a cursor carries it; rows sharing one timestamp
// would make the position ambiguous and hide an ordering mistake.
var firstStoredAt = time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

// newMemRepo builds an empty fake.
func newMemRepo() *memRepo {
	return &memRepo{series: map[string]models.Series{}}
}

// WithTx runs fn directly: there is no transaction to open in memory.
func (m *memRepo) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// TakeNextNumber opens the series if it is new and takes the next number, the
// way the real upsert does in one statement.
func (m *memRepo) TakeNextNumber(
	_ context.Context, prefix string, year int32,
) (models.Series, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.series {
		s := m.series[id]
		if s.Prefix == prefix && s.Year == year {
			s.LastNumber++
			m.series[id] = s

			return s, nil
		}
	}

	opened := models.Series{
		ID:         models.NewSeriesID(),
		Prefix:     prefix,
		Year:       year,
		LastNumber: 1,
		CreatedAt:  firstStoredAt,
		UpdatedAt:  firstStoredAt,
	}
	m.series[opened.ID] = opened

	return opened, nil
}

// ListSeries returns every series, newest year first as the query does.
func (m *memRepo) ListSeries(_ context.Context) ([]models.Series, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]models.Series, 0, len(m.series))
	for id := range m.series {
		out = append(out, m.series[id])
	}

	// The real listing is ordered by year descending and then by prefix; the
	// map iteration that produced this slice has no order at all, so a test
	// asserting on a position would otherwise pass or fail by chance.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && less(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	return out, nil
}

// less orders two series the way the listing query does.
func less(a, b models.Series) bool {
	if a.Year != b.Year {
		return a.Year > b.Year
	}

	return a.Prefix < b.Prefix
}

// CreateInvoice stores the document and refuses a repeated number.
//
// The refusal is here because the table's UNIQUE constraint is there: a fake
// that accepted the same number twice would let a numbering mistake pass
// through every HTTP test in this package.
func (m *memRepo) CreateInvoice(_ context.Context, in models.Invoice) (models.Invoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.stored {
		if m.stored[i].Number == in.Number {
			return models.Invoice{}, errors.Conflict("fake_number_taken",
				"number %s is already used", in.Number)
		}
	}

	m.writes++
	in.CreatedAt = firstStoredAt.Add(time.Duration(m.writes) * time.Second)
	in.UpdatedAt = in.CreatedAt

	m.stored = append(m.stored, in)

	return in, nil
}

// GetInvoice returns the stored document with its lines.
func (m *memRepo) GetInvoice(_ context.Context, id string) (models.Invoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.stored {
		if m.stored[i].ID == id {
			return m.stored[i], nil
		}
	}

	return models.Invoice{}, errors.NotFound("fake_invoice_missing", "no such invoice: %s", id)
}

// ListInvoices returns the matching page, newest first, WITHOUT the lines.
//
// The lines are dropped because the real query does not select them, and a fake
// that returned them would make the listing's shape look like a decision this
// package makes rather than one the SQL makes.
func (m *memRepo) ListInvoices(
	_ context.Context, filter models.Filter,
) ([]models.Invoice, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.listFilter = filter

	matching := make([]models.Invoice, 0, len(m.stored))

	for i := len(m.stored) - 1; i >= 0; i-- {
		row := m.stored[i]
		if filter.Status != nil && row.Status.String() != *filter.Status {
			continue
		}
		if filter.Kind != nil && row.Kind.String() != *filter.Kind {
			continue
		}

		row.Lines = nil
		matching = append(matching, row)
	}

	count := int64(len(matching))
	if filter.Offset >= count {
		return nil, count, nil
	}

	page := matching[filter.Offset:]
	if int64(len(page)) > filter.Limit {
		page = page[:filter.Limit]
	}

	return page, count, nil
}

// CountInvoicesByBuyerEmail counts the documents issued to an address.
//
// Both sides are lowered exactly as the real query lowers them; see the service
// package's fake for why a case-sensitive imitation would be a fake that agrees
// with the service about everything except the one thing the column does not
// constrain.
func (m *memRepo) CountInvoicesByBuyerEmail(_ context.Context, email string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var count int64

	for i := range m.stored {
		if strings.EqualFold(m.stored[i].Buyer.Email, email) {
			count++
		}
	}

	return count, nil
}

// SetStatus writes the status only when the current one still matches, which is
// how the real UPDATE decides a race in its WHERE clause.
func (m *memRepo) SetStatus(
	_ context.Context, id string, from, to models.Status, reason, providerID, externalID string,
) (models.Invoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.stored {
		if m.stored[i].ID != id {
			continue
		}
		if m.stored[i].Status != from {
			return models.Invoice{}, errors.Conflict("fake_status_moved",
				"invoice %s is no longer in status %q", id, from)
		}

		m.stored[i].Status = to
		m.stored[i].StatusReason = reason

		if providerID != "" {
			m.stored[i].ProviderID = providerID
		}
		if externalID != "" {
			m.stored[i].ExternalID = externalID
		}

		return m.stored[i], nil
	}

	return models.Invoice{}, errors.NotFound("fake_invoice_missing", "no such invoice: %s", id)
}
