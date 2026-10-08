package service_test

import (
	"context"
	"slices"
	"sort"

	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// The fake's half of ADR 0406: it reads what the real queries read, from the
// documents it holds. It takes no lock, since nothing in memory runs at once;
// the lock is proven against a real database in the module's integration test.

// LockInvoice returns the stored document, as the real read after its lock.
func (f *fakeRepo) LockInvoice(ctx context.Context, id string) (models.Invoice, error) {
	return f.GetInvoice(ctx, id)
}

// AmendedRows sums the rows of the live documents amending the sale.
func (f *fakeRepo) AmendedRows(_ context.Context, saleID string) (map[string]models.AmendedRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := map[string]models.AmendedRow{}
	for id := range f.invoices {
		doc := f.invoices[id]
		if doc.AmendsInvoiceID != saleID || !doc.Status.Live() {
			continue
		}
		for k := range doc.Lines {
			line := &doc.Lines[k]
			if line.AmendsLineID == "" {
				continue
			}
			row := out[line.AmendsLineID]
			if row.Components == nil {
				row.Components = map[int32]models.AmendedComponent{}
			}
			given := doc.Kind == models.KindRefund
			if given {
				row.RefundedTotal += line.Total
				row.RefundedTax += line.TaxTotal
			} else {
				row.ChargedTotal += line.Total
				row.ChargedTax += line.TaxTotal
			}
			for _, component := range line.TaxComponents {
				moved := row.Components[component.Position]
				if given {
					moved.RefundedTax += component.TaxAmount
				} else {
					moved.ChargedTax += component.TaxAmount
				}
				row.Components[component.Position] = moved
			}
			out[line.AmendsLineID] = row
		}
	}

	return out, nil
}

// RefundsByRow names, per row of the sale, the live refunds that gave back on
// it, once each, in the order they were written, as the grouped query does.
func (f *fakeRepo) RefundsByRow(ctx context.Context, saleID string) (map[string][]models.DocumentRef, error) {
	documents, err := f.ListAmendmentsOf(ctx, saleID, 1<<30)
	if err != nil {
		return nil, err
	}
	out := map[string][]models.DocumentRef{}
	for k := range documents {
		doc := &documents[k]
		if doc.Kind != models.KindRefund || !doc.Status.Live() {
			continue
		}
		for l := range doc.Lines {
			row := doc.Lines[l].AmendsLineID
			if row == "" || slices.ContainsFunc(out[row], func(r models.DocumentRef) bool { return r.ID == doc.ID }) {
				continue
			}
			out[row] = append(out[row], models.DocumentRef{ID: doc.ID, Number: doc.Number})
		}
	}

	return out, nil
}

// CountLiveAmendments counts the live documents amending the sale.
func (f *fakeRepo) CountLiveAmendments(ctx context.Context, saleID string) (int64, error) {
	return f.CountLiveAmendmentsWithKey(ctx, saleID, "")
}

// CountLiveAmendmentsWithKey counts the live documents amending the sale, for
// one act when the key is not empty.
func (f *fakeRepo) CountLiveAmendmentsWithKey(_ context.Context, saleID, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	for id := range f.invoices {
		doc := f.invoices[id]
		if doc.AmendsInvoiceID == saleID && doc.Status.Live() && (key == "" || doc.AmendmentKey == key) {
			count++
		}
	}

	return count, nil
}

// ListAmendmentsOf lists the documents amending the sale by number.
func (f *fakeRepo) ListAmendmentsOf(_ context.Context, saleID string, limit int64) ([]models.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.Invoice{}
	for id := range f.invoices {
		doc := f.invoices[id]
		if doc.AmendsInvoiceID == saleID {
			out = append(out, doc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	if int64(len(out)) > limit {
		out = out[:limit]
	}

	return out, nil
}
