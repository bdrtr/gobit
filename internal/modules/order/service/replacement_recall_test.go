package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// sentReplacement records a replacement of the request and sends it.
func sentReplacement(t *testing.T, e env, in service.CreateReplacementInput) service.ReplacementRecord {
	t.Helper()

	record, err := e.svc.CreateReplacement(context.Background(), in)
	require.NoError(t, err)
	send(t, e, record, in)

	return record
}

// send dispatches a recorded replacement in parcel ful_1, with every promise
// written, and settles its source as the flow does.
func send(t *testing.T, e env, record service.ReplacementRecord, in service.CreateReplacementInput) {
	t.Helper()
	ctx := context.Background()

	for i := range record.Items {
		item := &record.Items[i]
		if len(item.Parts) == 0 {
			variant := item.VariantID
			if variant == "" {
				variant = testVariantID
			}
			require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, item.ID, variant, "invres_"+item.ID))
			continue
		}
		for _, part := range item.Parts {
			require.NoError(t, e.svc.RecordReplacementReservation(
				ctx, record.ID, item.ID, part.VariantID, "invres_"+part.VariantID))
		}
	}
	_, err := e.svc.MarkReplacementDispatched(ctx, record.ID, "ful_1")
	require.NoError(t, err)
	if in.ExchangeID != "" {
		_, err = e.svc.CompleteExchange(ctx, in.ExchangeID)
	} else {
		_, err = e.svc.CompleteClaim(ctx, in.ClaimID)
	}
	require.NoError(t, err)
}

// TestACanceledParcelSendsItsReplacementBackToWaiting is ADR 0239's record: the
// recall clears the parcel and the moment, forgets every promise, counts
// itself, and the claim the goods had settled is open again.
func TestACanceledParcelSendsItsReplacementBackToWaiting(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, boxLine, plainLine := boxAndPlainClaim(t, e)
	record := sentReplacement(t, e, replacementOfBoth(claim.ID, boxLine, plainLine))

	carried, err := e.svc.ReplacementOfParcel(ctx, "ful_1")
	require.NoError(t, err)
	assert.Equal(t, record.ID, carried)

	recalled, err := e.svc.RecallReplacement(ctx, record.ID, "ful_1")
	require.NoError(t, err)
	assert.Equal(t, models.ReplacementRequested, recalled.Status)
	assert.Empty(t, recalled.FulfillmentID)
	assert.Nil(t, recalled.DispatchedAt)
	assert.Equal(t, 1, recalled.Recalls)

	read, err := e.svc.GetReplacement(ctx, record.ID)
	require.NoError(t, err)
	for _, item := range read.Items {
		assert.Empty(t, item.ReservationID, "the line's promise is forgotten: its units are back")
		for _, part := range item.Parts {
			assert.Empty(t, part.ReservationID, "and each part's")
		}
	}
	reopened, err := e.svc.GetClaim(ctx, claim.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ClaimRequested, reopened.Status, "the goods that settled the claim did not leave")
	assert.Nil(t, reopened.CompletedAt)

	carried, err = e.svc.ReplacementOfParcel(ctx, "ful_1")
	require.NoError(t, err)
	assert.Empty(t, carried, "the canceled parcel carries nothing any more")
}

// TestARecallForAParcelTheReplacementLeftChangesNothing answers a late or
// repeated event: only a replacement dispatched in THIS parcel is recalled.
func TestARecallForAParcelTheReplacementLeftChangesNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)
	record := sentReplacement(t, e, replacementOf(claim.ID, lineID, 1))

	out, err := e.svc.RecallReplacement(ctx, record.ID, "ful_OTHER")
	require.NoError(t, err)
	assert.Equal(t, models.ReplacementDispatched, out.Status)
	assert.Equal(t, "ful_1", out.FulfillmentID)
	assert.Zero(t, out.Recalls)

	_, err = e.svc.RecallReplacement(ctx, record.ID, "ful_1")
	require.NoError(t, err)
	again, err := e.svc.RecallReplacement(ctx, record.ID, "ful_1")
	require.NoError(t, err, "a second delivery of the event is answered")
	assert.Equal(t, 1, again.Recalls, "and counts nothing twice")
}

// TestAClaimAnotherReplacementSettledStaysSettled keeps a source closed while
// goods that settle it are still on their way.
func TestAClaimAnotherReplacementSettledStaysSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)
	first, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, err)
	second := sentReplacement(t, e, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, e.svc.RecordReplacementReservation(ctx, first.ID, first.Items[0].ID, testVariantID, "invres_first"))
	_, err = e.svc.MarkReplacementDispatched(ctx, first.ID, "ful_2")
	require.NoError(t, err)

	_, err = e.svc.RecallReplacement(ctx, second.ID, "ful_1")
	require.NoError(t, err)

	settled, err := e.svc.GetClaim(ctx, claim.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ClaimCompleted, settled.Status, "the other replacement's goods still settle it")
}

// TestAnExchangeGoesBackToWhereItStood reopens a funded exchange as funded, so
// its collected difference stays named, and one that owed nothing as
// requested.
func TestAnExchangeGoesBackToWhereItStood(t *testing.T) {
	ctx := context.Background()

	for name, tc := range map[string]struct {
		due  int64
		want models.ExchangeStatus
	}{
		"funded":       {due: 500, want: models.ExchangeFunded},
		"owed nothing": {due: 0, want: models.ExchangeRequested},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			exchange, lineID := exchangeToSend(t, e, tc.due)
			in := replacementOfExchange(exchange.ID, lineID, 1)
			record, err := e.svc.CreateReplacement(ctx, in)
			require.NoError(t, err)
			if tc.due > 0 {
				_, err = e.svc.FundExchange(ctx, exchange.ID, "paycol_1", exchange.DifferenceDue)
				require.NoError(t, err)
			}
			send(t, e, record, in)

			_, err = e.svc.RecallReplacement(ctx, record.ID, "ful_1")
			require.NoError(t, err)

			reopened, err := e.svc.GetExchange(ctx, exchange.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.want, reopened.Status)
			assert.Nil(t, reopened.CompletedAt)
		})
	}
}

// TestAParcelThatCarriedNoReplacementNamesNone is the answer every sale's
// canceled parcel gets.
func TestAParcelThatCarriedNoReplacementNamesNone(t *testing.T) {
	carried, err := newEnv(t).svc.ReplacementOfParcel(context.Background(), "ful_SALE")
	require.NoError(t, err)
	assert.Empty(t, carried)
}
