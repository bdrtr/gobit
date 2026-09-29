package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// boxAndPlainClaim opens an order that sold a gift box of a towel and two
// soaps beside a line of its own, and a claim to be settled with goods; it
// returns the claim and the two lines, the box's first.
func boxAndPlainClaim(t *testing.T, e env) (claim models.Claim, boxLine, plainLine string) {
	t.Helper()
	ctx := context.Background()

	in := withComponents(
		service.CreateOrderLineComponentInput{VariantID: "variant_TOWEL", Quantity: 1},
		service.CreateOrderLineComponentInput{VariantID: "variant_SOAP", Quantity: 2},
	)
	in.Items[0].VariantID = "variant_BOX"
	plain := validInput().Items[0]
	in.Items = append(in.Items, plain)
	in.Subtotal += plain.Subtotal
	in.TaxTotal += plain.TaxTotal
	in.Total += plain.Total

	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)
	require.NotEmpty(t, detail.Items[0].Components, "precondition: the first line sold the box")

	claim, err = e.svc.CreateClaim(ctx, service.CreateClaimInput{OrderID: order.ID, Type: models.ClaimReplace})
	require.NoError(t, err)

	return claim, detail.Items[0].ID, detail.Items[1].ID
}

// replacementOfBoth is a request for two of the box and one of the plain line.
func replacementOfBoth(claimID, boxLine, plainLine string) service.CreateReplacementInput {
	in := replacementOf(claimID, boxLine, 2)
	in.Lines = append(in.Lines, service.ReplacementLineInput{OrderLineItemID: plainLine, Quantity: 1})

	return in
}

// TestAReplacedBoxSendsThePartsItWasSoldWith is ADR 0238's record: the item
// that replaces a line that sold a bundle keeps the line's parts, in their
// order, and the flow's document carries them; a line of its own has none.
func TestAReplacedBoxSendsThePartsItWasSoldWith(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, boxLine, plainLine := boxAndPlainClaim(t, e)

	record, err := e.svc.CreateReplacement(ctx, replacementOfBoth(claim.ID, boxLine, plainLine))
	require.NoError(t, err)

	want := []models.ReplacementItemPart{
		{VariantID: "variant_TOWEL", Quantity: 1}, {VariantID: "variant_SOAP", Quantity: 2},
	}
	require.Len(t, record.Items, 2)
	assert.Equal(t, want, record.Items[0].Parts)
	assert.Nil(t, record.Items[1].Parts, "a line of its own is sent as itself")

	readBack, err := e.svc.GetReplacement(ctx, record.ID)
	require.NoError(t, err)
	require.Len(t, readBack.Items, 2)
	assert.Equal(t, want, readBack.Items[0].Parts)
	assert.Nil(t, readBack.Items[1].Parts)

	raw, err := e.svc.ReplacementDetailJSON(ctx, record.ID)
	require.NoError(t, err)
	var detail struct {
		Lines []struct {
			VariantID string          `json:"variant_id"`
			Quantity  int64           `json:"quantity"`
			Parts     json.RawMessage `json:"parts"`
		} `json:"lines"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))
	require.Len(t, detail.Lines, 2)
	assert.JSONEq(t, `[{"variant_id":"variant_TOWEL","quantity":1,"reservation_id":""},`+
		`{"variant_id":"variant_SOAP","quantity":2,"reservation_id":""}]`, string(detail.Lines[0].Parts))
	assert.Equal(t, int64(2), detail.Lines[0].Quantity, "the line's quantity is boxes; the flow multiplies")
	assert.Empty(t, detail.Lines[1].Parts, "the key is absent on a line of its own")
}

// TestEachPartOfABoxIsHeldUnderItsOwnPromise holds the rules of the line's
// promise at the part: a retry is answered, a second promise is refused, and a
// variant the box does not hold is not a part of it. The line itself holds
// nothing.
func TestEachPartOfABoxIsHeldUnderItsOwnPromise(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, boxLine, plainLine := boxAndPlainClaim(t, e)
	record, err := e.svc.CreateReplacement(ctx, replacementOfBoth(claim.ID, boxLine, plainLine))
	require.NoError(t, err)
	box := record.Items[0].ID

	require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, box, "variant_TOWEL", "invres_towel"))
	require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, box, "variant_TOWEL", "invres_towel"),
		"the same promise again is a retry")

	err = e.svc.RecordReplacementReservation(ctx, record.ID, box, "variant_TOWEL", "invres_other")
	require.Error(t, err, "a second promise would leave the first with nothing to release it")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementNotOpen, errors.CodeOf(err))

	err = e.svc.RecordReplacementReservation(ctx, record.ID, box, "variant_BOX", "invres_box")
	require.Error(t, err, "the box tracks no stock; its parts are what is held")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementLineUnknown, errors.CodeOf(err))

	require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, box, "variant_SOAP", "invres_soap"))

	readBack, err := e.svc.GetReplacement(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, []models.ReplacementItemPart{
		{VariantID: "variant_TOWEL", Quantity: 1, ReservationID: "invres_towel"},
		{VariantID: "variant_SOAP", Quantity: 2, ReservationID: "invres_soap"},
	}, readBack.Items[0].Parts)
	assert.Empty(t, readBack.Items[0].ReservationID, "a box is held part by part, never as itself")
	assert.Empty(t, readBack.Items[1].ReservationID, "no part's promise lands on another line")
}

// TestABoxIsNotRecordedSentUntilEveryPartIsHeld is the record half's check
// at the part: one soap with no promise is units nothing says left.
func TestABoxIsNotRecordedSentUntilEveryPartIsHeld(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, boxLine, _ := boxAndPlainClaim(t, e)
	record, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, boxLine, 1))
	require.NoError(t, err)
	box := record.Items[0].ID
	require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, box, "variant_TOWEL", "invres_towel"))

	_, err = e.svc.MarkReplacementDispatched(ctx, record.ID, "ful_1")
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementNotHeld, errors.CodeOf(err))

	require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, box, "variant_SOAP", "invres_soap"))
	sent, err := e.svc.MarkReplacementDispatched(ctx, record.ID, "ful_1")
	require.NoError(t, err, "with every part held the box is recorded as sent")
	assert.Equal(t, models.ReplacementDispatched, sent.Status)
}

// TestALineIsHeldOnlyForWhatItSends refuses a promise for units of another
// variant, on both shapes of item: the line's variant for an item naming a
// line, the item's own for one naming a variant.
func TestALineIsHeldOnlyForWhatItSends(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	claim, lineID := claimToReplace(t, e)
	byLine, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	err = e.svc.RecordReplacementReservation(ctx, byLine.ID, byLine.Items[0].ID, "variant_OTHER", "invres_1")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementLineUnknown, errors.CodeOf(err))
	require.NoError(t, e.svc.RecordReplacementReservation(ctx, byLine.ID, byLine.Items[0].ID, testVariantID, "invres_1"))

	exchange, _ := exchangeToSend(t, e, 0)
	byVariant, err := e.svc.CreateReplacement(ctx, replacementOfVariant(exchange.ID, "variant_larger", 1))
	require.NoError(t, err)

	err = e.svc.RecordReplacementReservation(ctx, byVariant.ID, byVariant.Items[0].ID, testVariantID, "invres_2")
	require.Error(t, err, "the order's variant is not what this item sends")
	assert.Equal(t, service.CodeReplacementLineUnknown, errors.CodeOf(err))
	require.NoError(t, e.svc.RecordReplacementReservation(ctx, byVariant.ID, byVariant.Items[0].ID, "variant_larger", "invres_2"))
}
