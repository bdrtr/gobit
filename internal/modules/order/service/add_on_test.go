package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// withEngraving turns validInput's order into a ring and its engraving, the
// engraving keyed to the ring (ADR 0229).
func withEngraving(parentKey string) service.CreateOrderInput {
	in := validInput()
	in.Items[0].LineKey = "li_ring"
	in.Items = append(in.Items, service.CreateOrderItemInput{
		VariantID: "variant_ENGRAVING", Title: "Engraving", Quantity: 3,
		UnitPrice: 500, Subtotal: 1500, Total: 1500,
		LineKey: "li_engraving", ParentLineKey: parentKey,
	})
	in.Subtotal += 1500
	in.Total += 1500
	return in
}

// TestAnOrderLineKeepsItsParent is ADR 0229 on the order: an add-on keyed to an
// earlier line is written naming that line's id, which the order made.
func TestAnOrderLineKeepsItsParent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, withEngraving("li_ring"))
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)
	byVariant := map[string]int{}
	for i := range detail.Items {
		byVariant[detail.Items[i].VariantID] = i
	}
	ring := detail.Items[byVariant[testVariantID]]
	engraving := detail.Items[byVariant["variant_ENGRAVING"]]
	assert.Nil(t, ring.ParentLineItemID)
	require.NotNil(t, engraving.ParentLineItemID)
	assert.Equal(t, ring.ID, *engraving.ParentLineItemID, "the key is mapped to the id the order made")
}

// TestAnAddOnNamesAParentItCanHave holds the refusals of the keys, each before
// anything is written: a parent no earlier line is, a parent that is itself an
// add-on, and a key named twice.
func TestAnAddOnNamesAParentItCanHave(t *testing.T) {
	ctx := context.Background()

	for name, in := range map[string]service.CreateOrderInput{
		"a parent no line is": withEngraving("li_gone"),
		"a parent listed after it": func() service.CreateOrderInput {
			in := withEngraving("li_ring")
			in.Items[0], in.Items[1] = in.Items[1], in.Items[0]
			return in
		}(),
		"a parent that is an add-on": func() service.CreateOrderInput {
			in := withEngraving("li_ring")
			in.Items = append(in.Items, service.CreateOrderItemInput{
				VariantID: "variant_WRAP", Title: "Wrap", Quantity: 3, UnitPrice: 100,
				Subtotal: 300, Total: 300, LineKey: "li_wrap", ParentLineKey: "li_engraving",
			})
			in.Subtotal += 300
			in.Total += 300
			return in
		}(),
		"a key named twice": func() service.CreateOrderInput {
			in := withEngraving("")
			in.Items[1].LineKey = "li_ring"
			return in
		}(),
	} {
		e := newEnv(t)
		_, err := e.svc.CreateOrder(ctx, in)
		require.Error(t, err, name)
		assert.Equal(t, service.CodeAddOnInvalid, errors.CodeOf(err), name)
		assert.Empty(t, e.store.orders, "%s: nothing was written", name)
	}
}
