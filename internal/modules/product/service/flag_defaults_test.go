package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The four boolean flags this module publishes — manage_inventory,
// allow_backorder, discountable and is_giftcard — have NO READER anywhere in
// the repository. That was measured on 2026-09-06 across every published
// boolean: no Go branch and no SQL predicate depends on any of the four. Two of
// them cannot even acquire a reader here, because the storefront deliberately
// carries the inventory record through UNINTERPRETED (ADR 0004, see
// [service.StoreVariant]); their only possible reader is the checkout saga.
//
// # Why a flag with no reader still needs its DEFAULT pinned
//
// The obvious conclusion — "nothing reads it, so nothing about it can break" —
// is exactly backwards, and it is the reason these tests exist.
//
// A flag that is read has a second line of defense: change its default and some
// downstream test that depends on the behavior goes red. A flag that is NOT
// read has none. Meanwhile the column keeps accumulating a value per row, and
// the day a reader arrives — allow_backorder's reader is docs/gaps.md A6, still
// an open DECISION — that reader acts on every row written in the meantime. A
// default silently flipped today is not a dormant bug; it is a catalog that
// will mean the wrong thing the moment somebody finally implements the feature,
// with no migration able to tell the intended values from the accidental ones.
//
// So for a carried-only flag the default IS the whole contract. It is the only
// part of the flag that anything in the repository actually produces.
//
// # This hole was measured, not assumed
//
// Before these tests were written, all three defaults were flipped one at a
// time (manage_inventory true->false, allow_backorder false->true,
// discountable true->false) and the WHOLE product suite — unit and integration,
// against a real PostgreSQL — stayed green on every one. The inventory module's
// equivalent default is pinned (its requires_shipping mutation fails
// TestCreateInventoryItemVarsayilanSevkiyatGerektirir at once), which is what
// makes the absence here a gap rather than a house convention.
//
// # Why the values are asserted separately and not as a whole struct
//
// A single require.Equal against a models.Variant literal would fail on the
// generated id and the stamps, so it would have to be relaxed field by field
// anyway — and a relaxed struct comparison is the shape in which a NEW flag
// gets added without anybody choosing its default. Each flag is named here on
// purpose: adding a fifth one leaves this file untouched and visibly silent
// about it, which is the state the reviewer should notice.

// TestCreateVariantDefaultsToManagedStockWithoutBackorder pins the two variant
// flags a caller who sends neither gets.
//
// The pair is the safe one and both halves matter. manage_inventory=true means
// the variant's stock is tracked; had it defaulted to false, every variant
// created without the field would be recorded as untracked and a future reader
// of the flag would let all of them oversell. allow_backorder=false means an
// order is refused when the stock runs out; a true default would record the
// whole catalog as willing to sell what it does not have.
//
// The two are also the DATABASE defaults (manage_inventory DEFAULT true,
// allow_backorder DEFAULT false in migrations/000001_product_init.up.sql).
// They are asserted at the service instead of being left to the column because
// the service does not rely on the column default: it always sends an explicit
// value, so the column default is dead weight the moment these two lines drift.
func TestCreateVariantDefaultsToManagedStockWithoutBackorder(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:  "Shirt",
		Status: models.StatusPublished,
		// Neither flag is sent: this is the caller these defaults are for.
		Variants: []service.CreateVariantInput{{Title: "One size"}},
	})
	require.NoError(t, err)
	require.Len(t, product.Variants, 1)

	assert.True(t, product.Variants[0].ManageInventory,
		"a variant created without the field must have its stock TRACKED; "+
			"an untracked default would silently opt the whole catalog out of stock control")
	assert.False(t, product.Variants[0].AllowBackorder,
		"a variant created without the field must NOT allow backorders; "+
			"a permissive default would record the catalog as willing to sell absent stock")
}

// TestCreateVariantHonorsExplicitFlagValues pins that an explicitly sent value
// survives, in BOTH directions.
//
// This is not a restatement of the previous test. The flags are *bool on the
// input precisely so that "false" can be told apart from "not sent"
// ([service.CreateVariantInput]), and the defaulting is written as two
// if-blocks over those pointers. The failure this catches is a defaulting block
// that overwrites what the caller sent rather than filling in what they left
// out — the assignment and the condition are one line apart, and the previous
// test passes with both of them broken.
func TestCreateVariantHonorsExplicitFlagValues(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	off, on := false, true

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:  "Shirt",
		Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{
			Title: "One size",
			// Both flags are sent AGAINST their default, so neither assertion
			// below can pass by accident.
			ManageInventory: &off,
			AllowBackorder:  &on,
		}},
	})
	require.NoError(t, err)
	require.Len(t, product.Variants, 1)

	assert.False(t, product.Variants[0].ManageInventory,
		"an explicit manage_inventory=false must survive the defaulting block")
	assert.True(t, product.Variants[0].AllowBackorder,
		"an explicit allow_backorder=true must survive the defaulting block")
}

// TestCreateProductDefaultsToDiscountableAndNotAGiftcard pins the two product
// flags.
//
// discountable defaults to TRUE and that direction is the load-bearing one: a
// false default would mark every product created without the field as
// ineligible for promotions, and on the day a reader is written the shop's
// entire catalog would fall out of every campaign at once — silently, because
// a discount that does not apply produces no error.
//
// is_giftcard has no defaulting block at all: it is a plain bool on the input,
// so the Go zero value carries it, and false matches the column default. It is
// asserted next to discountable rather than left implicit because the two are
// ADJACENT booleans on the same row (product.is_giftcard, product.discountable
// in migrations/000001_product_init.up.sql) and adjacent columns of the same
// type are the pair that swaps places without an error — the same hazard
// TestProductColumnMappingHasNotDrifted is written around, one layer up.
func TestCreateProductDefaultsToDiscountableAndNotAGiftcard(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:  "Shirt",
		Status: models.StatusPublished,
	})
	require.NoError(t, err)

	assert.True(t, product.Discountable,
		"a product created without the field must be discountable; a false default "+
			"would quietly remove the whole catalog from every promotion")
	assert.False(t, product.IsGiftcard,
		"a product created without the field is not a gift card")
}

// TestCreateProductHonorsExplicitFlagValues is the product-side counterpart of
// [TestCreateVariantHonorsExplicitFlagValues], and it covers the asymmetry
// between the two product flags.
//
// discountable is a *bool and goes through a defaulting block; is_giftcard is a
// plain bool and does not. Sending both against their defaults in one call is
// what separates "the pointer was dereferenced correctly" from "the field was
// copied at all", and it is the only test in the module that sends
// is_giftcard=true through the service.
//
// Worth recording next to it: is_giftcard can be set at CREATE and never
// changed afterwards — [service.UpdateProductInput] has no such field, while
// discountable does. That asymmetry is not asserted here, because a test that
// pinned it would be pinning a limitation rather than a decision; it is
// reported in the flag measurement instead.
func TestCreateProductHonorsExplicitFlagValues(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	off := false

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:        "Gift card",
		Status:       models.StatusPublished,
		IsGiftcard:   true,
		Discountable: &off,
	})
	require.NoError(t, err)

	assert.False(t, product.Discountable,
		"an explicit discountable=false must survive the defaulting block")
	assert.True(t, product.IsGiftcard,
		"an explicit is_giftcard=true must be carried onto the record")
}
