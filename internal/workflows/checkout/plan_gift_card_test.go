package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// TestAGiftCardLineIsPlacedAsOne is ADR 0211: the product's flag, read when the
// plan is made, reaches the order line under the order's own key.
func TestAGiftCardLineIsPlacedAsOne(t *testing.T) {
	h := newHarness(t)
	variants := defaultVariants()
	card := variants[testVariantB]
	card.giftcard = true
	variants[testVariantB] = card
	scriptCatalog(h, variants)

	h.totals.calculateFn = untaxedCardTotals

	var sent json.RawMessage
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		sent = snapshot
		return testOrderID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	var body struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(sent, &body))
	flags := map[any]any{}
	for _, item := range body.Items {
		flags[item["variant_id"]] = item["is_giftcard"]
	}
	assert.Equal(t, map[any]any{testVariantA: false, testVariantB: true}, flags)
}

// untaxedCardTotals is [defaultTotals] with line B a gift card, which carries no
// tax (ADR 0247); line A carries the whole 500, so the cart still comes to 3000.
func untaxedCardTotals(ctx context.Context, cartID string) (cartwf.Totals, error) {
	totals, err := defaultTotals(ctx, cartID)
	totals.Lines[0].TaxTotal, totals.Lines[0].Total = 500, 2500
	totals.Lines[1].TaxTotal, totals.Lines[1].Total = 0, 500

	return totals, err
}

// TestATaxedGiftCardOpensNoOrder is ADR 0247's second layer: the cart prices a
// line whose product it could not read as any line, so a gift card can arrive
// taxed, and the plan, which read the flag strictly, refuses it before any
// stock is reserved or any money asked for.
func TestATaxedGiftCardOpensNoOrder(t *testing.T) {
	h := newHarness(t)
	variants := defaultVariants()
	card := variants[testVariantB]
	card.giftcard = true
	variants[testVariantB] = card
	scriptCatalog(h, variants)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeGiftCardTaxed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "the next attempt computes the totals again")
	assert.Equal(t, 0, h.rec.count("order:place"))
	assert.Equal(t, 0, h.rec.count("payment:collection"))
}

// TestAProductTheCatalogDoesNotKnowOpensNoOrder: a line whose product cannot
// be read would be booked as a sale and its card never issued, so the order is
// not opened.
func TestAProductTheCatalogDoesNotKnowOpensNoOrder(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
		records := catalogAnswer(defaultVariants(), spec)
		if spec.Entity == EntityProduct {
			return records[:1], nil
		}
		return records, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindNotFound))
	assert.Equal(t, 0, h.rec.count("order:place"))
}

// TestAProductReadOutageStaysAnOutage: the totals price without the flag when
// the read fails, the checkout does not place without it.
func TestAProductReadOutageStaysAnOutage(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
		if spec.Entity == EntityProduct {
			return nil, errors.Unavailable("query_unavailable", "the read layer is unreachable")
		}
		return catalogAnswer(defaultVariants(), spec), nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeCatalogReadFailed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
	assert.Equal(t, 0, h.rec.count("order:place"))
}

// TestAGiftCardFlagThatIsNotABoolIsRefused for [TestAFlagThatIsNotABoolIsRefused]'s
// reason: a type error is not a decision.
func TestAGiftCardFlagThatIsNotABoolIsRefused(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
		records := catalogAnswer(defaultVariants(), spec)
		if spec.Entity == EntityProduct {
			for _, record := range records {
				record[FieldIsGiftcard] = "false"
			}
		}
		return records, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal))
	assert.Equal(t, 0, h.rec.count("order:place"))
}
