package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// withComponents is validInput's line sold as a bundle of the given parts.
func withComponents(parts ...service.CreateOrderLineComponentInput) service.CreateOrderInput {
	in := validInput()
	in.Items[0].Components = parts
	return in
}

// TestABundleLineIsWrittenWithItsComponents is ADR 0235 on the order: the
// parts the checkout names are kept on the line, in the order given.
func TestABundleLineIsWrittenWithItsComponents(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, withComponents(
		service.CreateOrderLineComponentInput{VariantID: "variant_TOWEL", Quantity: 1},
		service.CreateOrderLineComponentInput{VariantID: "variant_SOAP", Quantity: 2},
	))
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, []models.OrderLineComponent{
		{VariantID: "variant_TOWEL", Quantity: 1}, {VariantID: "variant_SOAP", Quantity: 2},
	}, detail.Items[0].Components)
}

// TestABundleLinesComponentsAreHeldToAShape refuses, before anything is
// written, a composition the put-back acts could not multiply by: a part
// named twice or as the line's own variant, a part held no times or past the
// product module's bound, and more parts than it keeps.
func TestABundleLinesComponentsAreHeldToAShape(t *testing.T) {
	part := func(id string, quantity int64) service.CreateOrderLineComponentInput {
		return service.CreateOrderLineComponentInput{VariantID: id, Quantity: quantity}
	}
	tooMany := make([]service.CreateOrderLineComponentInput, 0, service.MaxLineComponents+1)
	for i := range service.MaxLineComponents + 1 {
		tooMany = append(tooMany, part(fmt.Sprintf("variant_%02d", i), 1))
	}

	for name, parts := range map[string][]service.CreateOrderLineComponentInput{
		"a part named twice":           {part("variant_SOAP", 1), part("variant_SOAP", 1)},
		"the line's own variant":       {part(testVariantID, 1)},
		"a part held no times":         {part("variant_SOAP", 0)},
		"a part held past the bound":   {part("variant_SOAP", service.MaxLineComponentQuantity+1)},
		"a part with no variant":       {part("", 1)},
		"more parts than a bundle has": tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			_, err := e.svc.CreateOrder(context.Background(), withComponents(parts...))
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "got: %v", err)
		})
	}

	e := newEnv(t)
	_, err := e.svc.CreateOrder(context.Background(),
		withComponents(part("variant_SOAP", service.MaxLineComponentQuantity)))
	require.NoError(t, err, "the bound itself is a part the catalog could write")
}
