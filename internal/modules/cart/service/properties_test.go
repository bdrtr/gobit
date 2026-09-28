package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// addEngraved adds a line of variant A with the given words and quantity.
func addEngraved(
	ctx context.Context, t *testing.T, svc *service.Service, cartID string, quantity int64, properties map[string]string,
) models.LineItem {
	t.Helper()
	line, err := svc.AddLineItem(ctx, cartID, service.AddLineItemInput{
		VariantID: variantA, Title: "Ring", Quantity: quantity, UnitPrice: 1000, Properties: properties,
	})
	require.NoError(t, err)
	return line
}

// TestAVariantWithOtherPropertiesIsAnotherLine is ADR 0223: the same words
// raise the quantity of the line already there, other words or none open
// another, and words out of their bounds are refused before anything is
// written.
func TestAVariantWithOtherPropertiesIsAnotherLine(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	first := addEngraved(ctx, t, svc, cart.ID, 1, map[string]string{"Engraving": "Ada"})
	again := addEngraved(ctx, t, svc, cart.ID, 2, map[string]string{" Engraving ": "Ada "})
	other := addEngraved(ctx, t, svc, cart.ID, 1, map[string]string{"Engraving": "Bo"})
	plain := addEngraved(ctx, t, svc, cart.ID, 1, nil)

	assert.Equal(t, first.ID, again.ID, "the same words, trimmed, are the same line")
	assert.Equal(t, int64(3), again.Quantity)
	assert.NotEqual(t, first.ID, other.ID)
	assert.NotEqual(t, first.ID, plain.ID)
	assert.NotEqual(t, other.ID, plain.ID)
	assert.Equal(t, map[string]string{"Engraving": "Ada"}, again.Properties)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Ring", Quantity: 1, UnitPrice: 1000,
		Properties: map[string]string{"Engraving": ""},
	})
	assert.Equal(t, models.CodePropertiesInvalid, errors.CodeOf(err))
	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, 3)
}

// TestAMergeKeepsLinesApartByTheirProperties: two carts holding one variant
// with different words merge into two lines, and the same words into one.
func TestAMergeKeepsLinesApartByTheirProperties(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	target, source := newCart(ctx, t, svc), newCart(ctx, t, svc)
	addEngraved(ctx, t, svc, target.ID, 1, map[string]string{"Engraving": "Ada"})
	addEngraved(ctx, t, svc, source.ID, 2, map[string]string{"Engraving": "Ada"})
	addEngraved(ctx, t, svc, source.ID, 1, map[string]string{"Engraving": "Bo"})

	_, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, target.ID)
	require.NoError(t, err)
	byWords := map[string]int64{}
	for _, line := range detail.Items {
		byWords[line.Properties["Engraving"]] = line.Quantity
	}
	assert.Equal(t, map[string]int64{"Ada": 3, "Bo": 1}, byWords)
}
