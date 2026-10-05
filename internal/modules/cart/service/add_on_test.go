package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// The add-on variants of these tests.
const (
	engravingVariant = "variant_ENGRAVING"
	wrapVariant      = "variant_WRAP"
)

// addRing adds a ring with the given add-ons and quantity.
func addRing(
	ctx context.Context, t *testing.T, svc *service.Service, cartID string, quantity int64, addOns ...service.AddOnInput,
) (models.LineItem, error) {
	t.Helper()
	return svc.AddLineItem(ctx, cartID, service.AddLineItemInput{
		VariantID: variantA, Title: "Ring", Quantity: quantity, UnitPrice: 20_000, AddOns: addOns,
	})
}

// engraved is an engraving add-on with the given words.
func engraved(words string) service.AddOnInput {
	return service.AddOnInput{
		VariantID: engravingVariant, Title: "Engraving", UnitPrice: 5_000,
		Properties: map[string]string{"Text": words},
	}
}

// wrapped is a gift wrap add-on.
func wrapped() service.AddOnInput {
	return service.AddOnInput{VariantID: wrapVariant, Title: "Wrap", UnitPrice: 1_000}
}

// linesOf reads the cart's lines keyed by variant and words.
func linesOf(ctx context.Context, t *testing.T, svc *service.Service, cartID string) []models.LineItem {
	t.Helper()
	detail, err := svc.GetCart(ctx, cartID)
	require.NoError(t, err)
	return detail.Items
}

// addOnsOf returns the lines bound to parent.
func addOnsOf(lines []models.LineItem, parent string) []models.LineItem {
	var out []models.LineItem
	for i := range lines {
		if lines[i].ParentLineID != nil && *lines[i].ParentLineID == parent {
			out = append(out, lines[i])
		}
	}
	return out
}

// TestAnAddedLineOpensItsAddOns is ADR 0229: a line added with add-ons opens
// each as a line of its own, bound to it, at its quantity and its own price.
func TestAnAddedLineOpensItsAddOns(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	ring, err := addRing(ctx, t, svc, cart.ID, 2, engraved("Ada"), wrapped())
	require.NoError(t, err)

	lines := linesOf(ctx, t, svc, cart.ID)
	require.Len(t, lines, 3)
	children := addOnsOf(lines, ring.ID)
	require.Len(t, children, 2)
	for _, child := range children {
		assert.Equal(t, int64(2), child.Quantity, "an add-on's quantity is its line's")
		assert.Empty(t, child.AddOnKey, "an add-on carries no add-ons")
		switch child.VariantID {
		case engravingVariant:
			assert.Equal(t, int64(5_000), child.UnitPrice)
			assert.Equal(t, map[string]string{"Text": "Ada"}, child.Properties)
		case wrapVariant:
			assert.Equal(t, int64(1_000), child.UnitPrice)
		default:
			t.Fatalf("an unexpected add-on %s", child.VariantID)
		}
	}
	assert.NotEmpty(t, ring.AddOnKey)
	assert.Nil(t, ring.ParentLineID)
}

// TestTheAddOnsArePartOfTheLine holds the identity: the same line with the
// same add-ons, spelled with other spaces and in another order, raises the
// line and its add-ons; the same variant without them, or with other words on
// them, is another line.
func TestTheAddOnsArePartOfTheLine(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	first, err := addRing(ctx, t, svc, cart.ID, 1, engraved("Ada"), wrapped())
	require.NoError(t, err)
	again, err := addRing(ctx, t, svc, cart.ID, 2, wrapped(), service.AddOnInput{
		VariantID: engravingVariant, Title: "Engraving", UnitPrice: 5_000,
		Properties: map[string]string{" Text ": "Ada "},
	})
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID, "the same add-ons raise the line already there")
	assert.Equal(t, int64(3), again.Quantity)
	lines := linesOf(ctx, t, svc, cart.ID)
	require.Len(t, lines, 3)
	for _, child := range addOnsOf(lines, first.ID) {
		assert.Equal(t, int64(3), child.Quantity, "the add-ons follow the raised line")
	}

	plain, err := addRing(ctx, t, svc, cart.ID, 1)
	require.NoError(t, err)
	other, err := addRing(ctx, t, svc, cart.ID, 1, engraved("Bo"))
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, plain.ID, "a ring without add-ons is another line")
	assert.NotEqual(t, first.ID, other.ID, "a ring engraved otherwise is another line")
	assert.NotEqual(t, plain.ID, other.ID)
	lines = linesOf(ctx, t, svc, cart.ID)
	assert.Len(t, lines, 6)
	assert.Len(t, addOnsOf(lines, other.ID), 1)

	// The same add-on set told apart by the words on it alone.
	ada, err := addRing(ctx, t, svc, cart.ID, 1, engraved("Ada"))
	require.NoError(t, err)
	assert.NotEqual(t, other.ID, ada.ID, "an engraving's words are part of what the line is")
}

// TestAnAddOnFollowsItsLine holds the life of an add-on: the line's quantity is
// written onto it, it cannot be written or removed alone, and it goes with its
// line.
func TestAnAddOnFollowsItsLine(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)
	ring, err := addRing(ctx, t, svc, cart.ID, 1, engraved("Ada"))
	require.NoError(t, err)
	engraving := addOnsOf(linesOf(ctx, t, svc, cart.ID), ring.ID)[0]

	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, ring.ID, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(4), addOnsOf(linesOf(ctx, t, svc, cart.ID), ring.ID)[0].Quantity)

	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, engraving.ID, 1)
	assert.Equal(t, service.CodeAddOnFollows, errors.CodeOf(err))
	err = svc.RemoveLineItem(ctx, cart.ID, engraving.ID)
	assert.Equal(t, service.CodeAddOnFollows, errors.CodeOf(err))
	assert.Len(t, linesOf(ctx, t, svc, cart.ID), 2, "a refused write changes nothing")

	require.NoError(t, svc.RemoveLineItem(ctx, cart.ID, ring.ID))
	assert.Empty(t, linesOf(ctx, t, svc, cart.ID), "the add-on goes with its line")
}

// TestAddOnsAreRefusedBeforeAnythingIsWritten holds the refusals of the add-ons
// given with a line; the line is not written either.
func TestAddOnsAreRefusedBeforeAnythingIsWritten(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	many := make([]service.AddOnInput, 0, service.MaxLineAddOns+1)
	for i := range service.MaxLineAddOns + 1 {
		many = append(many, service.AddOnInput{
			VariantID: fmt.Sprintf("variant_ADDON_%d", i), Title: "Add-on", UnitPrice: 100,
		})
	}
	for name, case_ := range map[string]struct {
		addOns []service.AddOnInput
		code   string
	}{
		"past the ceiling": {many, service.CodeAddOnInvalid},
		"named twice":      {[]service.AddOnInput{engraved("Ada"), engraved("Bo")}, service.CodeAddOnInvalid},
		"no variant":       {[]service.AddOnInput{{Title: "Nothing", UnitPrice: 1}}, service.CodeAddOnInvalid},
		"words out of bounds": {[]service.AddOnInput{{
			VariantID: engravingVariant, Title: "Engraving", UnitPrice: 1,
			Properties: map[string]string{"Text": ""},
		}}, models.CodePropertiesInvalid},
	} {
		_, err := addRing(ctx, t, svc, cart.ID, 1, case_.addOns...)
		require.Error(t, err, name)
		assert.Equal(t, case_.code, errors.CodeOf(err), name)
	}
	assert.Empty(t, linesOf(ctx, t, svc, cart.ID))
}

// TestAnAddWithAddOnsCountsEveryLine holds the line ceiling (ADR 0227) over a
// line and its add-ons: they are opened together or not at all.
func TestAnAddWithAddOnsCountsEveryLine(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)
	engraveLines(ctx, t, svc, cart.ID, variantB, service.MaxLineItems-2)

	_, err := addRing(ctx, t, svc, cart.ID, 1, engraved("Ada"), wrapped())
	assert.Equal(t, service.CodeLineLimit, errors.CodeOf(err))
	assert.Len(t, linesOf(ctx, t, svc, cart.ID), service.MaxLineItems-2, "the line is not opened without its add-ons")

	_, err = addRing(ctx, t, svc, cart.ID, 1, engraved("Ada"))
	require.NoError(t, err)
	assert.Len(t, linesOf(ctx, t, svc, cart.ID), service.MaxLineItems)
}

// TestAMergeCarriesAddOnsWithTheirLine holds the merge over add-ons: a line the
// target does not hold opens under the target with its add-ons bound to it,
// and a line it holds with the same add-ons is raised, its add-ons following.
func TestAMergeCarriesAddOnsWithTheirLine(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	held, err := addRing(ctx, t, svc, target.ID, 1, wrapped())
	require.NoError(t, err)
	_, err = addRing(ctx, t, svc, source.ID, 2, wrapped())
	require.NoError(t, err)
	_, err = addRing(ctx, t, svc, source.ID, 1, engraved("Ada"))
	require.NoError(t, err)

	_, err = svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	lines := linesOf(ctx, t, svc, target.ID)
	require.Len(t, lines, 4)
	var engravedRing string
	for _, line := range lines {
		switch {
		case line.ID == held.ID:
			assert.Equal(t, int64(3), line.Quantity, "the held line is raised")
		case line.ParentLineID == nil:
			engravedRing = line.ID
			assert.Equal(t, int64(1), line.Quantity)
		}
	}
	require.NotEmpty(t, engravedRing)
	heldAddOns := addOnsOf(lines, held.ID)
	require.Len(t, heldAddOns, 1)
	assert.Equal(t, int64(3), heldAddOns[0].Quantity, "its wrap follows it")
	opened := addOnsOf(lines, engravedRing)
	require.Len(t, opened, 1, "the engraving is bound to the ring the merge opened")
	assert.Equal(t, engravingVariant, opened[0].VariantID)
}

// TestAMergeOpensEachAddOnUnderItsLine is ADR 0393 on the merge: a guest's two
// engraved rings, the first wrapped as well, opened on an empty target, are
// listed each followed by its own add-ons, as the cart that held them listed
// them.
func TestAMergeOpensEachAddOnUnderItsLine(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	_, err := addRing(ctx, t, svc, source.ID, 1, engraved("Ada"), wrapped())
	require.NoError(t, err)
	_, err = addRing(ctx, t, svc, source.ID, 1, engraved("Bo"))
	require.NoError(t, err)
	// shape names each line by its variant and words, and an add-on by the
	// position of its line.
	shape := func(lines []models.LineItem) []string {
		at := map[string]int{}
		out := make([]string, len(lines))
		for i := range lines {
			at[lines[i].ID] = i
			out[i] = lines[i].VariantID + " " + lines[i].Properties["Text"]
			if lines[i].ParentLineID != nil {
				parent, ok := at[*lines[i].ParentLineID]
				require.True(t, ok, "line %d follows its own line", i)
				out[i] += fmt.Sprintf(" under %d", parent)
			}
		}
		return out
	}
	held := shape(linesOf(ctx, t, svc, source.ID))
	require.Equal(t, []string{
		variantA + " ", engravingVariant + " Ada under 0", wrapVariant + "  under 0",
		variantA + " ", engravingVariant + " Bo under 3",
	}, held, "the guest's cart lists each ring followed by its add-ons, as they were added")

	_, err = svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	assert.Equal(t, held, shape(linesOf(ctx, t, svc, target.ID)), "in the order the guest's cart held them")
}

// TestAMergeRefusesAnAddOnWithoutItsLine: an add-on whose line the source does
// not hold as a line of its own cannot be opened under anything, so the merge
// is refused and the target is left as it was. The line may be gone, or be an
// add-on itself. The service never writes such a line; the store is seeded
// with one directly.
func TestAMergeRefusesAnAddOnWithoutItsLine(t *testing.T) {
	for name, parentOf := range map[string]func(ring models.LineItem, lines []models.LineItem) string{
		"its line is gone": func(models.LineItem, []models.LineItem) string { return "cali_gone" },
		"its line is an add-on": func(ring models.LineItem, lines []models.LineItem) string {
			return addOnsOf(lines, ring.ID)[0].ID
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			svc, store := newService(t)
			target := newCart(ctx, t, svc)
			source := newCart(ctx, t, svc)
			ring, err := addRing(ctx, t, svc, source.ID, 1, engraved("Ada"))
			require.NoError(t, err)
			parent := parentOf(ring, linesOf(ctx, t, svc, source.ID))
			_, err = store.CreateLineItem(ctx, models.LineItem{
				ID: models.NewLineItemID(), CartID: source.ID, VariantID: wrapVariant, Title: "Wrap",
				Quantity: 1, UnitPrice: 1_000, ParentLineID: &parent,
			})
			require.NoError(t, err)

			_, err = svc.MergeCart(ctx, source.ID, target.ID)

			require.Error(t, err)
			assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err), "%v", err)
			assert.Empty(t, linesOf(ctx, t, svc, target.ID), "nothing is opened on the target")
		})
	}
}
