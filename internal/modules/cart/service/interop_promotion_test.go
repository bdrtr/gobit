package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestTheSnapshotCarriesTheCouponCodes is the PRODUCER's own assertion about the
// wire.
//
// The consumer of this body is the cart workflow, which declares its own copy of
// the schema and cannot be imported here. ADR 0102 found what happens when only
// the consumer is tested: the consumer's fake supplied a field the producer never
// emitted, and every test passed while the boundary carried nothing. So the field
// is read out of the encoded JSON rather than off a struct.
func TestTheSnapshotCarriesTheCouponCodes(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	interop := service.NewInterop(svc)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "summer20")
	require.NoError(t, err)
	_, err = svc.AddPromotionCode(ctx, cart.ID, "FREESHIP")
	require.NoError(t, err)

	raw, err := interop.CartSnapshotJSON(ctx, cart.ID)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	assert.Equal(t, []any{"SUMMER20", "FREESHIP"}, decoded["promotion_codes"],
		"the codes are on the wire, upper-cased and in the order they were typed")
}

// TestASnapshotWithNoCouponCarriesAnEmptyArray keeps the wire from having two
// spellings for "none".
//
// A field that is sometimes null and sometimes [] makes the consumer handle a
// case that does not exist — and a consumer written against the null spelling
// would decode an absent field into nil and never notice the difference until
// somebody else's language did.
func TestASnapshotWithNoCouponCarriesAnEmptyArray(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	interop := service.NewInterop(svc)
	cart := newCart(ctx, t, svc)

	raw, err := interop.CartSnapshotJSON(ctx, cart.ID)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	codes, present := decoded["promotion_codes"]
	require.True(t, present, "the key is always there")
	assert.Equal(t, []any{}, codes)
}

// TestTheInteropWritesAndRemovesACouponCode is the pair the cart flow calls.
func TestTheInteropWritesAndRemovesACouponCode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	interop := service.NewInterop(svc)
	cart := newCart(ctx, t, svc)

	require.NoError(t, interop.AddCartPromotionCode(ctx, cart.ID, "summer20"))
	assert.Equal(t, []string{"SUMMER20"}, codesOf(ctx, t, svc, cart.ID))

	require.NoError(t, interop.RemoveCartPromotionCode(ctx, cart.ID, "SuMmEr20"))
	assert.Empty(t, codesOf(ctx, t, svc, cart.ID),
		"the removal normalizes the same way the write does")
}
