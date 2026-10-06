package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// JournalFacts reads the records the order journal is derived from, inside the
// half-open window [from, to) and in one currency when currencyCode is set
// (ADR 0188).
//
// Each kind is read with a limit of limit+1, so a caller that gets more than
// limit facts back knows the window was cut. The facts come
// back grouped by kind; ordering the whole is the caller's.
func (r *Repository) JournalFacts(
	ctx context.Context, from, to time.Time, currencyCode string, limit int32,
) ([]models.JournalFact, error) {
	rowLimit := limit + 1
	currency := nullString(currencyCode)
	fromAt, toAt := fromTimePtr(&from), fromTimePtr(&to)
	q := r.queries(ctx)

	placed, err := q.JournalOrdersPlaced(ctx, orderdb.JournalOrdersPlacedParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the placed orders of the journal could not be read")
	}
	canceled, err := q.JournalOrdersCanceled(ctx, orderdb.JournalOrdersCanceledParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the canceled orders of the journal could not be read")
	}
	credits, err := q.JournalCreditLines(ctx, orderdb.JournalCreditLinesParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the credit lines of the journal could not be read")
	}

	upgrades, err := q.JournalDeliveryUpgrades(ctx, orderdb.JournalDeliveryUpgradesParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the dearer deliveries of the journal could not be read")
	}

	exchanges, err := q.JournalExchangesFunded(ctx, orderdb.JournalExchangesFundedParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the funded exchanges of the journal could not be read")
	}

	out := make([]models.JournalFact, 0,
		len(placed)+len(canceled)+len(credits)+len(upgrades)+len(exchanges))
	for i := range placed {
		row := &placed[i]
		out = append(out, models.JournalFact{
			ID: row.ID, Kind: models.JournalOrderPlaced, OrderID: row.ID,
			OccurredAt: toTime(row.PlacedAt), CurrencyCode: row.CurrencyCode,
			Subtotal: row.Subtotal, DiscountTotal: row.DiscountTotal, TaxTotal: row.TaxTotal,
			ShippingTotal: row.ShippingTotal, Total: row.Total, GiftCardSubtotal: row.GiftCardSubtotal,
		})
	}
	for i := range canceled {
		row := &canceled[i]
		out = append(out, models.JournalFact{
			ID: row.ID, Kind: models.JournalOrderCanceled, OrderID: row.ID,
			OccurredAt: toTime(row.CanceledAt), CurrencyCode: row.CurrencyCode,
			Subtotal: row.Subtotal, DiscountTotal: row.DiscountTotal, TaxTotal: row.TaxTotal,
			ShippingTotal: row.ShippingTotal, Total: row.Total, GiftCardSubtotal: row.GiftCardSubtotal,
		})
	}
	for i := range credits {
		row := &credits[i]
		fact := models.JournalFact{
			ID: row.ID, Kind: models.JournalCreditLine, OrderID: row.OrderID,
			OccurredAt: toTime(row.CreatedAt), CurrencyCode: row.CurrencyCode, Amount: row.Amount,
		}
		// A credit a delivery change wrote is the change's entry (ADR 0199).
		if row.DeliveryChangeID != nil {
			fact.ID, fact.Kind = *row.DeliveryChangeID, models.JournalDeliveryChanged
		}
		out = append(out, fact)
	}
	for i := range upgrades {
		row := &upgrades[i]
		out = append(out, models.JournalFact{
			ID: row.ID, Kind: models.JournalDeliveryUpgraded, OrderID: row.OrderID,
			OccurredAt: toTime(row.CreatedAt), CurrencyCode: row.CurrencyCode, Amount: row.Difference,
		})
	}
	for i := range exchanges {
		row := &exchanges[i]
		out = append(out, models.JournalFact{
			ID: row.ID, Kind: models.JournalExchangeFunded, OrderID: row.OrderID,
			OccurredAt: toTime(row.FundedAt), CurrencyCode: row.CurrencyCode, Amount: row.DifferenceDue,
		})
	}

	return out, nil
}

// JournalCauses reads which order each of the given returns, claims and
// exchanges belongs to (ADR 0189, 0203). An id that is none of them has no
// row.
func (r *Repository) JournalCauses(ctx context.Context, ids []string) ([]models.JournalCause, error) {
	if len(ids) == 0 {
		return []models.JournalCause{}, nil
	}
	rows, err := r.queries(ctx).JournalCauses(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the causes of the journal's refunds could not be read")
	}

	out := make([]models.JournalCause, 0, len(rows))
	for i := range rows {
		out = append(out, models.JournalCause{
			ID: rows[i].ID, Kind: rows[i].Kind, OrderID: rows[i].OrderID, CurrencyCode: rows[i].CurrencyCode,
		})
	}

	return out, nil
}

// JournalActOrders reads which order each of the given credit lines and
// delivery changes belongs to, and its currency (ADR 0419). An id that is
// neither has no row.
func (r *Repository) JournalActOrders(
	ctx context.Context, creditLineIDs, changeIDs []string,
) ([]models.JournalActOrder, error) {
	if len(creditLineIDs) == 0 && len(changeIDs) == 0 {
		return []models.JournalActOrder{}, nil
	}
	rows, err := r.queries(ctx).JournalActOrders(ctx, orderdb.JournalActOrdersParams{
		CreditLineIds: nonNilStrings(creditLineIDs), ChangeIds: nonNilStrings(changeIDs),
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the orders of the documented acts could not be read")
	}

	out := make([]models.JournalActOrder, 0, len(rows))
	for i := range rows {
		out = append(out, models.JournalActOrder{
			ID: rows[i].ID, Kind: rows[i].Kind, OrderID: rows[i].OrderID, CurrencyCode: rows[i].CurrencyCode,
		})
	}

	return out, nil
}

// nonNilStrings turns a nil list into an empty one: pgx sends a nil slice as
// NULL, and `= ANY (NULL)` matches nothing only by accident of three-valued
// logic.
func nonNilStrings(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// OrderAfterSaleCauses reads the order's returns, claims and exchanges, with
// when each exchange's difference was funded, at most limit+1 of them
// (ADR 0406).
func (r *Repository) OrderAfterSaleCauses(
	ctx context.Context, orderID string, limit int32,
) ([]models.AfterSaleCause, error) {
	rows, err := r.queries(ctx).OrderAfterSaleCauses(ctx, orderdb.OrderAfterSaleCausesParams{
		OrderID: orderID, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the after-sale records of order %s could not be read", orderID)
	}

	out := make([]models.AfterSaleCause, 0, len(rows))
	for i := range rows {
		out = append(out, models.AfterSaleCause{
			ID: rows[i].ID, Kind: rows[i].Kind, FundedAt: toTimePtr(rows[i].FundedAt),
			DifferenceDue: rows[i].DifferenceDue,
		})
	}

	return out, nil
}
