package adminui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// afterSalesCatalog is an order with one line and the given after-sales
// records by entity (ADR 0270).
func afterSalesCatalog(records map[string][]query.Record) *fakeCatalog {
	catalog := linedOrderCatalog(func(query.GraphSpec) ([]query.Record, error) {
		return []query.Record{{
			"id": "oli_ring", "title": "Silver ring", "variant_id": "variant_ring", "quantity": int64(2),
			"unit_price": int64(60_000), "subtotal": int64(120_000), "discount_total": int64(0),
			"tax_total": int64(0), "total": int64(120_000),
		}}, nil
	})
	for entity, list := range records {
		catalog.byEntity[entity] = list
	}

	return catalog
}

// at is a moment on the fixture's day.
func at(hour int) time.Time { return time.Date(2026, 9, 30, hour, 0, 0, 0, time.UTC) }

// TestTheOrderPageListsItsAfterSales prints every kind with what it adds,
// newest first across the kinds, and reads each per order.
func TestTheOrderPageListsItsAfterSales(t *testing.T) {
	t.Parallel()

	catalog := afterSalesCatalog(map[string][]query.Record{
		EntityOrderReturn: {{
			"id": "ret_1", "status": "received", "created_at": at(10), "reason": "too small",
			"refund_amount": int64(60_000), "received_at": at(12), "received_location_id": "sloc_returns",
			"items": []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(1), "refund_amount": int64(60_000)}},
		}},
		EntityOrderClaim: {{
			"id": "claim_1", "status": "completed", "created_at": at(8), "type": "refund",
			"refund_amount": int64(5_000), "completed_at": at(9),
		}},
		EntityOrderExchange: {{
			"id": "exch_1", "status": "requested", "created_at": at(14), "difference_due": int64(-2_500),
			"payment_collection_id": "",
		}},
		EntityOrderReplacement: {{
			"id": "orepl_1", "status": "dispatched", "created_at": at(11), "claim_id": "claim_2",
			"exchange_id": "", "location_id": "sloc_main", "dispatched_at": at(13), "fulfillment_id": "ful_9",
			"items": []map[string]any{{"line_item_id": "", "variant_id": "variant_x", "quantity": int64(2)}},
		}},
	})
	rec := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"Silver ring × 1", "refund 600.00 TRY", "too small", "received 2026-09-30 12:00 · sloc_returns",
		"settled by refund", "refund 50.00 TRY", "completed 2026-09-30 09:00",
		"25.00 TRY due to the customer",
		"for claim claim_2", "from sloc_main", "variant variant_x × 2", "dispatched 2026-09-30 13:00 · ful_9",
	} {
		assert.Contains(t, body, want)
	}
	exchange, replacement := strings.Index(body, "exch_1"), strings.Index(body, "orepl_1")
	ret, claim := strings.Index(body, "ret_1"), strings.Index(body, "claim_1")
	assert.True(t, exchange < replacement && replacement < ret && ret < claim,
		"newest first across the kinds: %d %d %d %d", exchange, replacement, ret, claim)
	assert.NotContains(t, body, "Only the latest")

	for _, entity := range []string{EntityOrderReturn, EntityOrderClaim, EntityOrderExchange, EntityOrderReplacement} {
		spec, ok := catalog.specFor(entity)
		require.True(t, ok, "the page did not read %s", entity)
		assert.Equal(t, "order_1", spec.Filters[fieldOrderID], "%s is read by the order's own id", entity)
		assert.Equal(t, afterSalesPerKind, spec.Limit, entity)
	}
}

// TestAnExchangeSaysWhoPaysItsDifference: the sign is the direction.
func TestAnExchangeSaysWhoPaysItsDifference(t *testing.T) {
	t.Parallel()

	for due, want := range map[int64]string{
		4_000: "40.00 TRY due from the customer",
		0:     "no difference",
	} {
		catalog := afterSalesCatalog(map[string][]query.Record{EntityOrderExchange: {{
			"id": "exch_1", "status": "requested", "created_at": at(9), "difference_due": due,
			"payment_collection_id": "paycol_7",
		}}})
		body := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_1").Body.String()

		assert.Contains(t, body, want)
		assert.Contains(t, body, "paid through paycol_7")
	}
}

// TestAnOrderPageSurvivesItsAfterSalesFailing keeps the order on screen when a
// kind cannot be read, and says the section is missing rather than empty.
func TestAnOrderPageSurvivesItsAfterSalesFailing(t *testing.T) {
	t.Parallel()

	catalog := afterSalesCatalog(nil)
	catalog.errByEntity = map[string]error{EntityOrderClaim: errors.New("the claims could not be read")}
	rec := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_1")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "The after-sales records of this order could not be read.")
	assert.NotContains(t, rec.Body.String(), "No return, claim, exchange or replacement")
}

// TestAnOrderWithNoAfterSalesSaysSo prints the absence.
func TestAnOrderWithNoAfterSalesSaysSo(t *testing.T) {
	t.Parallel()

	body := getOrderPage(newCatalogPanel(t, afterSalesCatalog(nil)), OrdersPath+"/order_1").Body.String()

	assert.Contains(t, body, "No return, claim, exchange or replacement has been opened for this order.")
}

// TestAnOrderPageSaysWhenAKindHasMoreThanItReads names the bound.
func TestAnOrderPageSaysWhenAKindHasMoreThanItReads(t *testing.T) {
	t.Parallel()

	returns := make([]query.Record, 0, afterSalesPerKind)
	for i := range afterSalesPerKind {
		returns = append(returns, query.Record{
			"id": fmt.Sprintf("ret_%02d", i), "status": "requested", "created_at": at(1),
		})
	}
	body := getOrderPage(newCatalogPanel(t, afterSalesCatalog(map[string][]query.Record{
		EntityOrderReturn: returns,
	})), OrdersPath+"/order_1").Body.String()

	assert.Contains(t, body, fmt.Sprintf("Only the latest %d records of each kind are shown.", afterSalesPerKind))
}

// TestAReplacementNamesWhatItSettles: a replacement sent for an exchange says
// so, and one sent for a claim names the claim.
func TestAReplacementNamesWhatItSettles(t *testing.T) {
	t.Parallel()

	catalog := afterSalesCatalog(map[string][]query.Record{EntityOrderReplacement: {
		{"id": "orepl_1", "status": "requested", "created_at": at(9), "claim_id": "", "exchange_id": "exch_2"},
		{"id": "orepl_2", "status": "requested", "created_at": at(8), "claim_id": "claim_3", "exchange_id": ""},
	}})
	body := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_1").Body.String()

	assert.Contains(t, body, "for exchange exch_2")
	assert.Contains(t, body, "for claim claim_3")
	assert.NotContains(t, body, "for claim exch_2")
}
