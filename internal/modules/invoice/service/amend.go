package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// amendedOf holds an amending document to the sale it names and prints the
// sale's buyer on it (ADR 0406). It runs inside the issue's transaction,
// before the number is taken, and locks the sale for the rest of it.
//
// # What it refuses
//
// An amendment amends a LIVE sale that amends nothing, in the sale's currency
// and price convention: a rejected or canceled sale took no effect. Each row it
// names is a row of that sale, or a row a live charge added to it, at the same
// rates. A refund row gives back at most what its row carried and what later
// documents charged on it, in amount, in tax and under each rate: one row
// cannot give back more tax than it charged, which a ceiling per document
// would let through.
//
// # Why the sale is locked
//
// Two amendments of one sale read what its rows have left, and each fits on
// its own; read together without a lock, both would be written and the row
// would give back twice. The lock makes the second read the first one's
// document. A cancellation of the sale takes the same lock, so neither can
// pass the other's check unseen.
func (s *Service) amendedOf(ctx context.Context, in *IssueInput) error {
	sale, err := s.repo.LockInvoice(ctx, in.Amends)
	if err != nil {
		return err
	}

	switch {
	case sale.Kind != models.KindSale || sale.AmendsInvoiceID != "":
		return errors.Invalid(CodeInvalidInput,
			"invoice %s is not a sale document that amends nothing, so it cannot be amended", sale.ID)
	case !sale.Status.Live():
		return errors.Conflict(CodeAmendsVoid,
			"invoice %s is %s: it took no effect, so there is nothing to amend", sale.ID, sale.Status)
	case sale.CurrencyCode != in.CurrencyCode:
		return errors.Invalid(CodeInvalidInput,
			"invoice %s is in %s and the amendment in %s", sale.ID, sale.CurrencyCode, in.CurrencyCode)
	case sale.PricesIncludeTax != in.PricesIncludeTax:
		return errors.Invalid(CodeInvalidInput,
			"invoice %s and its amendment read their prices under different conventions", sale.ID)
	}

	if in.AmendmentKey != "" {
		live, err := s.repo.CountLiveAmendmentsWithKey(ctx, sale.ID, in.AmendmentKey)
		if err != nil {
			return err
		}
		if live > 0 {
			return errors.Conflict(CodeAmendmentExists,
				"invoice %s already has a live document for %s", sale.ID, in.AmendmentKey)
		}
	}

	rows := make(map[string]models.Line, len(sale.Lines))
	for i := range sale.Lines {
		rows[sale.Lines[i].ID] = sale.Lines[i]
	}
	if namesAnotherRow(in.Lines, rows) {
		// A charge that added a row the sale did not have, a dearer
		// delivery on a sale that shipped free, is given back from that row.
		charged, err := s.chargeRows(ctx, sale.ID, "")
		if err != nil {
			return err
		}
		for id := range charged {
			rows[id] = charged[id]
		}
	}
	for i := range in.Lines {
		if in.Lines[i].AmendsLineID == "" {
			continue
		}
		row, ok := rows[in.Lines[i].AmendsLineID]
		if !ok {
			return errors.Invalid(CodeInvalidInput,
				"line %d names %s, which is not a row of invoice %s nor of a live charge on it",
				i+1, in.Lines[i].AmendsLineID, sale.ID)
		}
		if err := sameRates(i, in.Lines[i], row); err != nil {
			return err
		}
	}

	if in.Kind == models.KindRefund {
		amended, err := s.repo.AmendedRows(ctx, sale.ID)
		if err != nil {
			return err
		}
		if err := withinSale(in.Lines, rows, amended); err != nil {
			return err
		}
	}

	in.Buyer = sale.Buyer

	return nil
}

// sameRates refuses a row printed at other rates than the sale row it moves.
func sameRates(i int, line LineInput, row models.Line) error {
	if line.TaxRateBps != row.TaxRateBps || len(line.TaxComponents) != len(row.TaxComponents) {
		return errors.Invalid(CodeInvalidInput,
			"line %d is taxed otherwise than sale row %d it moves", i+1, row.Position)
	}
	for at := range line.TaxComponents {
		if line.TaxComponents[at].RateBps != row.TaxComponents[at].RateBps ||
			line.TaxComponents[at].Compound != row.TaxComponents[at].Compound {
			return errors.Invalid(CodeInvalidInput,
				"line %d's rate %d is not sale row %d's", i+1, at+1, row.Position)
		}
	}

	return nil
}

// withinSale holds a refund's rows to what their sale rows carried: for each
// sale row, what live documents gave back and what this one gives back is at
// most the row and what live documents charged on it, in amount, in tax and
// under each rate. Equality fits.
func withinSale(lines []LineInput, rows map[string]models.Line, amended map[string]models.AmendedRow) error {
	type moved struct {
		total, tax int64
		components map[int]int64
	}
	asked := map[string]*moved{}
	order := make([]string, 0, len(lines))
	for i := range lines {
		id := lines[i].AmendsLineID
		m, ok := asked[id]
		if !ok {
			m = &moved{components: map[int]int64{}}
			asked[id] = m
			order = append(order, id)
		}
		m.total += lines[i].Total
		m.tax += lines[i].TaxTotal
		for at := range lines[i].TaxComponents {
			m.components[at] += lines[i].TaxComponents[at].TaxAmount
		}
	}

	for _, id := range order {
		now := asked[id]
		if err := fits(rows[id], amended[id], now.total, now.tax, now.components); err != nil {
			return err
		}
	}

	return nil
}

// fits holds one row to its ceiling: what live documents gave back on it and
// total, tax and components more is at most the row and what live documents
// charged on it, in amount, in tax and under each rate. Equality fits.
func fits(row models.Line, before models.AmendedRow, total, tax int64, components map[int]int64) error {
	if before.RefundedTotal+total > row.Total+before.ChargedTotal ||
		before.RefundedTax+tax > row.TaxTotal+before.ChargedTax {
		return errors.Conflict(CodeAmendmentExceedsSale,
			"row %d carried %d with %d tax and gave back %d with %d tax; %d with %d tax more does not fit",
			row.Position, row.Total+before.ChargedTotal, row.TaxTotal+before.ChargedTax,
			before.RefundedTotal, before.RefundedTax, total, tax)
	}
	for at := range row.TaxComponents {
		component := row.TaxComponents[at]
		given := before.Components[component.Position]
		if given.RefundedTax+components[at] > component.TaxAmount+given.ChargedTax {
			return errors.Conflict(CodeAmendmentExceedsSale,
				"row %d's rate %d carried %d tax and gave back %d; %d more does not fit",
				row.Position, component.Position, component.TaxAmount+given.ChargedTax,
				given.RefundedTax, components[at])
		}
	}

	return nil
}

// namesAnotherRow reports whether a line names a row that is not the sale's.
func namesAnotherRow(lines []LineInput, rows map[string]models.Line) bool {
	for i := range lines {
		if _, ok := rows[lines[i].AmendsLineID]; lines[i].AmendsLineID != "" && !ok {
			return true
		}
	}

	return false
}

// chargeRows reads the rows of the live sales amending the sale, a charge
// named by except left out (ADR 0406).
func (s *Service) chargeRows(ctx context.Context, saleID, except string) (map[string]models.Line, error) {
	documents, err := s.repo.ListAmendmentsOf(ctx, saleID, MaxAmendments+1)
	if err != nil {
		return nil, err
	}
	if len(documents) > MaxAmendments {
		return nil, errors.Invalid(CodeInvalidInput,
			"invoice %s is amended by more than %d documents", saleID, MaxAmendments)
	}

	rows := map[string]models.Line{}
	for k := range documents {
		if documents[k].Kind != models.KindSale || !documents[k].Status.Live() || documents[k].ID == except {
			continue
		}
		charge, err := s.repo.GetInvoice(ctx, documents[k].ID)
		if err != nil {
			return nil, err
		}
		for i := range charge.Lines {
			rows[charge.Lines[i].ID] = charge.Lines[i]
		}
	}

	return rows, nil
}

// chargeLeavesRowsCovered refuses to withdraw a live charge that a refund
// relies on (ADR 0406): with the charge gone, every row of the sale and of its
// other live charges still holds what live refunds gave back on it, and no
// live refund gave back on a row the charge added. It runs with the amended
// sale locked, as an amendment does.
func (s *Service) chargeLeavesRowsCovered(ctx context.Context, charge models.Invoice) error {
	sale, err := s.repo.GetInvoice(ctx, charge.AmendsInvoiceID)
	if err != nil {
		return err
	}
	amended, err := s.repo.AmendedRows(ctx, sale.ID)
	if err != nil {
		return err
	}
	rows, err := s.chargeRows(ctx, sale.ID, charge.ID)
	if err != nil {
		return err
	}
	for i := range sale.Lines {
		rows[sale.Lines[i].ID] = sale.Lines[i]
	}

	for i := range charge.Lines {
		line := &charge.Lines[i]
		if moved := amended[line.ID]; moved.RefundedTotal > 0 || moved.RefundedTax > 0 {
			return errors.Conflict(CodeAmendmentExceedsSale,
				"a live refund gives back on row %d this charge added; cancel the refund first", line.Position)
		}
		if line.AmendsLineID == "" {
			continue
		}
		moved := amended[line.AmendsLineID]
		moved.ChargedTotal -= line.Total
		moved.ChargedTax -= line.TaxTotal
		if len(line.TaxComponents) > 0 {
			components := make(map[int32]models.AmendedComponent, len(moved.Components))
			for position, component := range moved.Components {
				components[position] = component
			}
			for _, component := range line.TaxComponents {
				given := components[component.Position]
				given.ChargedTax -= component.TaxAmount
				components[component.Position] = given
			}
			moved.Components = components
		}
		amended[line.AmendsLineID] = moved
	}

	for id := range rows {
		if err := fits(rows[id], amended[id], 0, 0, nil); err != nil {
			return errors.Conflict(CodeAmendmentExceedsSale,
				"invoice %s's live refunds rely on this charge (%v); cancel them first", sale.ID, err)
		}
	}

	return nil
}
