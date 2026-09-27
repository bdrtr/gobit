package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
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
