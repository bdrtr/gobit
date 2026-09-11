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

// exchangeToSend opens an order and an exchange on it, and returns the exchange
// with the order's single line.
func exchangeToSend(
	t *testing.T, e env, differenceDue int64,
) (exchange models.Exchange, lineID string) {
	t.Helper()

	ctx := context.Background()
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)

	exchange, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: order.ID, DifferenceDue: differenceDue,
	})
	require.NoError(t, err)

	return exchange, detail.Items[0].ID
}

// replacementOfExchange is the request that sends goods against an exchange.
func replacementOfExchange(exchangeID, lineID string, quantity int64) service.CreateReplacementInput {
	return service.CreateReplacementInput{
		ExchangeID:       exchangeID,
		ShippingOptionID: testShippingOptionID,
		LocationID:       testLocationID,
		Lines: []service.ReplacementLineInput{
			{OrderLineItemID: lineID, Quantity: quantity},
		},
	}
}

// TestAnExchangeCanSayWhatItWillSend is the half the record was missing.
func TestAnExchangeCanSayWhatItWillSend(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 2))
	require.NoError(t, err)

	assert.Equal(t, exchange.ID, record.ExchangeID)
	assert.Empty(t, record.ClaimID, "a replacement settles ONE record")
	assert.Equal(t, models.SourceExchange, record.Source())
	assert.Equal(t, exchange.ID, record.SourceID())
	require.Len(t, record.Items, 1)
	assert.Equal(t, int64(2), record.Items[0].Quantity)
}

// TestAReplacementSettlesExactlyOneRecord refuses both of the malformed shapes.
func TestAReplacementSettlesExactlyOneRecord(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	both := replacementOfExchange(exchange.ID, lineID, 1)
	both.ClaimID = "claim_1"

	neither := replacementOfExchange(exchange.ID, lineID, 1)
	neither.ExchangeID = ""

	for name, in := range map[string]service.CreateReplacementInput{
		"both a claim and an exchange": both,
		"neither":                      neither,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.CreateReplacement(ctx, in)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
		})
	}
}

// TestOnlyAnOpenExchangeCanPromiseGoods keeps a settled record from gaining a
// new promise.
func TestOnlyAnOpenExchangeCanPromiseGoods(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	_, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestTheLineCeilingHoldsForAnExchangeToo keeps the rule that spans rows on both
// sources.
func TestTheLineCeilingHoldsForAnExchangeToo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	_, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 4))

	require.Error(t, err, "the order's line holds three units")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestAnExchangeThatOwesNothingCanBeSettled is the transition that came back.
func TestAnExchangeThatOwesNothingCanBeSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	settled, err := e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err)

	assert.Equal(t, models.ExchangeCompleted, settled.Status)
	require.NotNil(t, settled.CompletedAt, "the status and its moment imply each other")

	again, err := e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err, "a second settlement is a no-op")
	assert.Equal(t, settled.CompletedAt.UTC(), again.CompletedAt.UTC(),
		"and it keeps the FIRST moment")
}

// TestAnExchangeThatOwesMoneyIsNotSettled is the bound, and it is the reason the
// completion came back narrow rather than general.
func TestAnExchangeThatOwesMoneyIsNotSettled(t *testing.T) {
	ctx := context.Background()

	for name, difference := range map[string]int64{
		"the customer owes": 500,
		"the shop owes":     -500,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			exchange, _ := exchangeToSend(t, e, difference)

			_, err := e.svc.CompleteExchange(ctx, exchange.ID)

			require.Error(t, err,
				"money cannot be moved against an existing order, so saying the "+
					"exchange is settled would say a balance was handled")
		})
	}
}

// TestAWithdrawnExchangeCannotBeSettled is the third row of the table.
func TestAWithdrawnExchangeCannotBeSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	_, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	_, err = e.svc.CompleteExchange(ctx, exchange.ID)

	require.Error(t, err, "a withdrawn request has no goods to answer")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestASettledExchangeCannotBeWithdrawn is the mirror refusal.
func TestASettledExchangeCannotBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	_, err := e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err)

	_, err = e.svc.CancelExchange(ctx, exchange.ID)

	require.Error(t, err, "an exchange that was met is not un-met by withdrawing the request")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestTheDetailNamesTheExchangeAsItsSource is the wire the dispatch flow reads.
func TestTheDetailNamesTheExchangeAsItsSource(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	raw, err := e.svc.ReplacementDetailJSON(ctx, record.ID)
	require.NoError(t, err)

	var detail struct {
		SourceKind       string `json:"source_kind"`
		SourceID         string `json:"source_id"`
		SourceStatus     string `json:"source_status"`
		SourceSettleable bool   `json:"source_settleable"`
		OrderID          string `json:"order_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))

	assert.Equal(t, "exchange", detail.SourceKind)
	assert.Equal(t, exchange.ID, detail.SourceID)
	assert.Equal(t, "requested", detail.SourceStatus)
	assert.True(t, detail.SourceSettleable, "this one owes nothing")
	assert.Equal(t, exchange.OrderID, detail.OrderID,
		"the order comes from the SOURCE; the replacement does not carry a copy")
}

// TestTheDetailSaysAnOwingExchangeCannotBeSettled is what keeps a dispatch that
// really sent the goods from failing over a correct state.
func TestTheDetailSaysAnOwingExchangeCannotBeSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 750)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	raw, err := e.svc.ReplacementDetailJSON(ctx, record.ID)
	require.NoError(t, err)

	var detail struct {
		SourceSettleable bool `json:"source_settleable"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))

	assert.False(t, detail.SourceSettleable,
		"the PRODUCER answers it; a flow that re-derived the rule would hold a "+
			"second copy of it")
}

// TestAnExchangesReplacementsAreListedApart keeps the two sources' listings from
// answering each other's question.
func TestAnExchangesReplacementsAreListedApart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	listed, err := e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, record.ID, listed[0].ID)

	claim, claimLineID := claimToReplace(t, e)
	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, claimLineID, 1))
	require.NoError(t, err)

	listed, err = e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Len(t, listed, 1, "the claim's promise is not the exchange's")
}

// TestAnExchangeWithAnOpenPromiseCannotBeWithdrawn is the claim's guard, on the
// record ADR 0114 gave the same capability to.
//
// The claim has refused this since it could promise goods, and its reason is
// the one that applies here word for word: the promise outlives the record that
// made it. ADR 0114 let a replacement name an EXCHANGE as its source and be
// dispatched against it, and the guard stayed on the claim — so a withdrawn
// exchange still had goods on the way and nothing between the two said so.
//
// Gap D56. The asymmetry is the same shape D52 had: a capability moved to the
// sibling record and the rule written for the first one did not follow.
func TestAnExchangeWithAnOpenPromiseCannotBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	_, err = e.svc.CancelExchange(ctx, exchange.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementNotOpen, errors.CodeOf(err))
	assert.Contains(t, err.Error(), record.ID,
		"the refusal has to name the replacement to withdraw first")
}

// TestAWithdrawnPromiseFreesTheExchange keeps the guard from becoming a lock.
//
// It is the other half of the claim's shape: withdrawing the replacement first
// leaves the exchange withdrawable, so the record never reaches a state with no
// way out. Without this the guard would trade one stuck record for another.
func TestAWithdrawnPromiseFreesTheExchange(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)
	_, err = e.svc.CancelReplacement(ctx, record.ID)
	require.NoError(t, err)

	withdrawn, err := e.svc.CancelExchange(ctx, exchange.ID)

	require.NoError(t, err, "a withdrawn promise no longer holds the exchange open")
	assert.Equal(t, models.ExchangeCanceled, withdrawn.Status)
}

// TestANegativeDifferenceCannotBeFunded keeps the sign out at the module's own
// door, not only at the flow's.
//
// The database refuses it too (order_exchanges_funded_is_positive), and both
// are wanted: the constraint cannot be skipped by a second writer, and the
// service's refusal is the one an operator can read.
func TestANegativeDifferenceCannotBeFunded(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, -500)

	_, err := e.svc.FundExchange(ctx, exchange.ID, "paycol_1")

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestASecondCollectionCannotBeNamed is the silence that would have cost money.
//
// A flow that died before recording would open a SECOND collection and call
// again. Answering "already funded" quietly would leave that second collection
// holding money nothing on this side names — money the customer can still be
// charged through the payment module's own endpoints. The retry path, which
// passes the SAME collection, is the one that stays quiet.
func TestASecondCollectionCannotBeNamed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 500)

	funded, err := e.svc.FundExchange(ctx, exchange.ID, "paycol_1")
	require.NoError(t, err)
	assert.Equal(t, models.ExchangeFunded, funded.Status)

	again, err := e.svc.FundExchange(ctx, exchange.ID, "paycol_1")
	require.NoError(t, err, "the retry path names the same collection and is quiet")
	assert.Equal(t, funded.FundedAt, again.FundedAt, "the FIRST funding keeps its moment")

	_, err = e.svc.FundExchange(ctx, exchange.ID, "paycol_2")
	require.Error(t, err, "a different collection is not a retry")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Contains(t, err.Error(), "paycol_1", "the refusal names the collection already recorded")
}

// TestAFundedExchangeRefusesTheOrdinaryWithdrawal is the guard the whole record
// is built around.
func TestAFundedExchangeRefusesTheOrdinaryWithdrawal(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 500)
	_, err := e.svc.FundExchange(ctx, exchange.ID, "paycol_1")
	require.NoError(t, err)

	_, err = e.svc.CancelExchange(ctx, exchange.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestTheFundedExchangeHasAnExit is why the refusal above is not a trap.
//
// Without it a record whose goods turn out to be unsendable would sit funded
// for ever, and the order it hangs from could never be forgotten. The exit is
// FORWARD: the record reaches a terminal state and keeps the moment it took
// money, next to the collection that answers for where it went.
func TestTheFundedExchangeHasAnExit(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 500)
	_, err := e.svc.FundExchange(ctx, exchange.ID, "paycol_1")
	require.NoError(t, err)

	withdrawn, err := e.svc.WithdrawFundedExchange(ctx, exchange.ID)

	require.NoError(t, err)
	assert.Equal(t, models.ExchangeCanceled, withdrawn.Status)
	require.NotNil(t, withdrawn.CanceledAt)
	assert.NotNil(t, withdrawn.FundedAt, "the row still says it once held money")
	assert.Equal(t, "paycol_1", withdrawn.PaymentCollectionID,
		"and which collection answers for where it went")
}

// TestAFundedExchangeIsSettleableAndAnUnfundedOneIsNot pins the predicate the
// dispatch reads.
//
// It is a SECOND predicate beside OwesNothing rather than a redefinition of it,
// and this test is why: OwesNothing's three existing cases carry no funding, so
// they would have passed unchanged under a widened definition and the widening
// would have shipped ungated.
func TestAFundedExchangeIsSettleableAndAnUnfundedOneIsNot(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owing, _ := exchangeToSend(t, e, 500)

	assert.False(t, owing.Settleable(), "money is owed and nothing answers it yet")

	funded, err := e.svc.FundExchange(ctx, owing.ID, "paycol_1")
	require.NoError(t, err)
	assert.True(t, funded.Settleable(), "the difference is answered; the goods decide the rest")
	assert.False(t, funded.OwesNothing(), "it still OWES; what changed is that it was paid")
}

// replacementOfVariant promises a product the order did NOT sell.
func replacementOfVariant(exchangeID, variantID string, quantity int64) service.CreateReplacementInput {
	return service.CreateReplacementInput{
		ExchangeID:       exchangeID,
		ShippingOptionID: testShippingOptionID,
		LocationID:       testLocationID,
		Lines: []service.ReplacementLineInput{
			{VariantID: variantID, Quantity: quantity},
		},
	}
}

// TestAnExchangeCanSendADIFFERENTProduct is what the record could not express.
//
// "Send me the same shirt a size larger" is the ordinary exchange. Until ADR 0145
// a replacement item pointed at an order line with a NOT NULL foreign key, so the
// only thing it could carry was units of the exact variant already sold — and the
// money half has been able to take a difference since ADR 0120, with nothing for
// it to answer.
func TestAnExchangeCanSendADIFFERENTProduct(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx,
		replacementOfVariant(exchange.ID, "variant_larger", 1))
	require.NoError(t, err)

	require.Len(t, record.Items, 1)
	assert.Equal(t, "variant_larger", record.Items[0].VariantID)
	assert.Empty(t, record.Items[0].OrderLineItemID,
		"an item names ONE thing; a line here would be a second answer to what is sent")
	assert.True(t, record.Items[0].SendsAVariant())
}

// TestTheBOUGHTCeilingDoesNotApplyToAVariantTheOrderNeverSold separates the two
// bounds.
//
// The line ceiling is "no more of a line than was bought on it", and goods the
// order never sold are against no line's ceiling. Applying it anyway would refuse
// every exchange for a different product, because the bought quantity of a variant
// that is not on the order is zero.
func TestTheBOUGHTCeilingDoesNotApplyToAVariantTheOrderNeverSold(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	_, err := e.svc.CreateReplacement(ctx,
		replacementOfVariant(exchange.ID, "variant_larger", 99))

	require.NoError(t, err,
		"the bound on a variant item is the exchange's money guard, not a quantity "+
			"this order never sold")
}

// TestALineCeilingSTILLAppliesToALineShapedItem is the other half, and it is a
// SEPARATE test on purpose.
//
// One fixture asserting both would let an implementation that dropped the ceiling
// entirely pass the variant case and never reach this one.
func TestALineCeilingSTILLAppliesToALineShapedItem(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	_, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 99))

	require.Error(t, err, "more of a line than was bought on it is still refused")
}

// TestAnItemNamingBOTHOrNEITHERIsRefused pins the shape.
//
// Both is two answers to "what is being sent" and leaves the dispatch to pick;
// neither is a row promising nothing. The service says which field to fill rather
// than letting the schema's CHECK name a constraint.
func TestAnItemNamingBOTHOrNEITHERIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	for name, line := range map[string]service.ReplacementLineInput{
		"both":    {OrderLineItemID: lineID, VariantID: "variant_larger", Quantity: 1},
		"neither": {Quantity: 1},
	} {
		t.Run(name, func(t *testing.T) {
			in := replacementOfExchange(exchange.ID, lineID, 1)
			in.Lines = []service.ReplacementLineInput{line}

			_, err := e.svc.CreateReplacement(ctx, in)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestTheSameVariantTwiceInOneReplacementIsRefused is the duplicate rule, on the
// other side of the row.
func TestTheSameVariantTwiceInOneReplacementIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	in := replacementOfVariant(exchange.ID, "variant_larger", 1)
	in.Lines = append(in.Lines,
		service.ReplacementLineInput{VariantID: "variant_larger", Quantity: 2})

	_, err := e.svc.CreateReplacement(ctx, in)

	require.Error(t, err, "the quantity carries the count; two rows would be two promises")
}

// TestTheDETAILCarriesAVariantItemWithoutJoiningTheOrder is what the dispatch
// flow reads, and the reason a variant item works at all.
//
// The document derives a line item's variant by joining the order's lines — a
// variant item has no line to join, and a version that tried would refuse it with
// "names line , which is not on order". Nothing downstream would have to change
// for a variant replacement to be dispatched, and nothing downstream would ever
// SEE one either: the flow holds stock from this document's VariantID.
//
// A mutation restoring the join survived every other test in this file until this
// one existed.
func TestTheDETAILCarriesAVariantItemWithoutJoiningTheOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx,
		replacementOfVariant(exchange.ID, "variant_larger", 1))
	require.NoError(t, err)

	raw, err := e.svc.ReplacementDetailJSON(ctx, record.ID)
	require.NoError(t, err,
		"a replacement that sends a product the order did not sell has to produce a "+
			"document; refusing here is refusing the whole feature at dispatch time")

	var detail struct {
		Lines []struct {
			OrderLineItemID string `json:"order_line_item_id"`
			VariantID       string `json:"variant_id"`
			Quantity        int64  `json:"quantity"`
		} `json:"lines"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))

	require.Len(t, detail.Lines, 1)
	assert.Equal(t, "variant_larger", detail.Lines[0].VariantID,
		"the flow sets stock aside from THIS field and nothing else")
	assert.Empty(t, detail.Lines[0].OrderLineItemID)
	assert.Equal(t, int64(1), detail.Lines[0].Quantity)
}
