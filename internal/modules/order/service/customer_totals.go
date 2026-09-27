package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// MaxCustomerTotals is the most customers one [Service.CustomerOrderTotals]
// call reads.
const MaxCustomerTotals = 500

// CustomerOrderTotals counts and sums the given customers' orders placed at or
// after since, per customer and currency (ADR 0217); since nil reads their
// whole history.
//
// The rules are the spending limit's: a canceled order does not count, a
// pending one does, and what was refunded is deducted. No currency is
// converted, so a customer who bought in two currencies has two rows, and a
// customer with no order has none.
func (s *Service) CustomerOrderTotals(
	ctx context.Context, customerIDs []string, since *time.Time,
) ([]models.CustomerOrderTotal, error) {
	if len(customerIDs) == 0 || len(customerIDs) > MaxCustomerTotals {
		return nil, errors.Invalid(CodeInvalidInput,
			"order totals are read for 1 to %d customers, %d asked", MaxCustomerTotals, len(customerIDs))
	}
	for _, id := range customerIDs {
		if strings.TrimSpace(id) == "" {
			return nil, errors.Invalid(CodeInvalidInput, "a customer id cannot be blank")
		}
	}

	return s.store.CustomerOrderTotals(ctx, customerIDs, since)
}

// interopCustomerOrderTotal is one row of [Interop.CustomerOrderTotalsJSON]:
//
//	{"customer_id": "cus_...", "currency_code": "TRY", "orders": 3, "net_spend": 45000}
type interopCustomerOrderTotal struct {
	CustomerID   string `json:"customer_id"`
	CurrencyCode string `json:"currency_code"`
	Orders       int64  `json:"orders"`
	NetSpend     int64  `json:"net_spend"`
}

// CustomerOrderTotalsJSON is [Service.CustomerOrderTotals] as a JSON array of
// [interopCustomerOrderTotal]. The consumer is the customer segment flow.
func (i *Interop) CustomerOrderTotalsJSON(
	ctx context.Context, customerIDs []string, since *time.Time,
) (json.RawMessage, error) {
	totals, err := i.svc.CustomerOrderTotals(ctx, customerIDs, since)
	if err != nil {
		return nil, err
	}
	out := make([]interopCustomerOrderTotal, 0, len(totals))
	for _, total := range totals {
		out = append(out, interopCustomerOrderTotal{
			CustomerID: total.CustomerID, CurrencyCode: total.CurrencyCode,
			Orders: total.Orders, NetSpend: total.NetSpend,
		})
	}

	return json.Marshal(out)
}
