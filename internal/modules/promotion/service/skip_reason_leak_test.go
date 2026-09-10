package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// TestTheInteropBodyDoesNotCarryTheReason is the LEAK GUARD, and it is the whole
// reason this file exists.
//
// The reason a promotion was skipped is published on the admin compute endpoint,
// which asks for a scope. The interop body is consumed by the cart flow, whose
// totals the STOREFRONT reads — so a reason traveling that way would end up
// readable by the customer whose code was refused. "That code exists but its
// campaign has not started" hands a code guesser a campaign calendar, which is
// the leak LookupStoreCoupon deliberately refuses to open.
//
// The gate is written against the ENCODED JSON rather than against the struct: a
// field added to [interopResponse] would compile, would be serialized, and would
// be invisible to a test that only read the type it was added to.
func TestTheInteropBodyDoesNotCarryTheReason(t *testing.T) {
	repo := newMemRepo()
	// A promotion that is CONSIDERED and refused: the code is sent, and the
	// promotion is paused. Without a skipped candidate the assertion below would
	// hold on an empty computation and prove nothing.
	seedPromotion(repo, models.Promotion{
		ID: "promo_paused", Code: "PAUSED", Status: models.PromotionInactive,
	}, percentageMethod("promo_paused", 2000, models.TargetItems, models.AllocationEach))

	interop := NewInterop(newTestService(repo))
	request := []byte(`{
	  "currency_code": "TRY",
	  "items": [{"id": "li_1", "amount": 1000, "quantity": 1, "attributes": {}}],
	  "shipping_methods": [],
	  "codes": ["PAUSED"],
	  "at": "2026-08-24T10:00:00Z"
	}`)

	payload, err := interop.ComputeDiscountsJSON(context.Background(), request)
	require.NoError(t, err)

	// The computation really does have something to leave out; otherwise this
	// test would pass for the wrong reason. It is asked of ExplainDiscounts,
	// which is the path that CAN see a paused promotion — and that asymmetry is
	// the point: the admin body carries the reason and the interop body, checked
	// above, does not.
	result, err := newTestService(repo).ExplainDiscounts(context.Background(), ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 1000, 1, nil)},
		Codes:        []string{"PAUSED"},
		At:           testNow,
	})
	require.NoError(t, err)
	require.Len(t, result.Skipped, 1, "precondition: a candidate was skipped")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.NotContains(t, decoded, "skipped",
		"the reason must not cross the surface the storefront's totals are built from")

	// The body still says what it always said: an unusable code comes back as
	// unmatched, which is the ONE thing the customer may know.
	assert.Equal(t, []any{"PAUSED"}, decoded["unmatched_codes"])
}
