package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestCreateReturnOpensARecordAndListsIt validates the basic flow of the return
// skeleton.
func TestCreateReturnOpensARecordAndListsIt(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	returned, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      order.ID,
		RefundAmount: 3600,
		Reason:       "the size did not fit",
		Metadata:     map[string]any{"channel": "support"},
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(returned.ID, models.ReturnIDPrefix))
	assert.Equal(t, models.ReturnRequested, returned.Status,
		"a return has to be born as a REQUEST")
	assert.Equal(t, int64(3600), returned.RefundAmount)

	readBack, err := e.svc.GetReturn(ctx, returned.ID)
	require.NoError(t, err)
	assert.Equal(t, returned.ID, readBack.ID)

	records, count, err := e.svc.ListReturns(ctx, order.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, records, 1)
	assert.Equal(t, returned.ID, records[0].ID)
}

// TestAfterSalesAmountCannotExceedTheOrderTotal validates that the amount of the
// return/claim record IS TIED to the order.
//
// The range check (0..MaxTotal) is not enough on its own: a return record worth
// millions could be opened on an order whose total is 6100. The record does not
// produce a money movement today, but the return flow will read this amount in
// the next phase and the mistake would only show up when the money was about to
// be paid back — that is, long after the record was opened.
func TestAfterSalesAmountCannotExceedTheOrderTotal(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.Equal(t, int64(6100), order.Total)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      order.ID,
		RefundAmount: order.Total + 1,
	})
	require.Error(t, err, "a return record exceeding the order total must not be openable")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeRefundExceedsOrder, errors.CodeOf(err))

	_, err = e.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      order.ID,
		Type:         models.ClaimRefund,
		RefundAmount: order.Total + 1,
	})
	require.Error(t, err, "a claim record exceeding the order total must not be openable")
	assert.Equal(t, service.CodeRefundExceedsOrder, errors.CodeOf(err))

	// A return of exactly the total IS VALID: the bound is not exclusive.
	full, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      order.ID,
		RefundAmount: order.Total,
	})
	require.NoError(t, err)
	assert.Equal(t, order.Total, full.RefundAmount)

	// No record must have been written: out of the two rejected calls only the
	// valid one is left behind.
	_, count, err := e.svc.ListReturns(ctx, order.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestCreateExchangeAcceptsANegativeDifference validates that the difference of
// an exchange can arise in both directions.
//
// A negative difference means "to be paid to the customer"; rejecting it would
// make an exchange done with a cheaper product impossible to record.
func TestCreateExchangeAcceptsANegativeDifference(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID:       order.ID,
		DifferenceDue: -500,
		Note:          "an exchange with a cheaper model",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(-500), exchange.DifferenceDue)
	assert.Equal(t, models.ExchangeRequested, exchange.Status)

	records, count, err := e.svc.ListExchanges(ctx, order.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, records, 1)
	assert.Equal(t, exchange.ID, records[0].ID)
}

// TestCreateClaimValidatesTheType validates the rules of the claim record type.
func TestCreateClaimValidatesTheType(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	refundClaim, err := e.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      order.ID,
		Type:         models.ClaimRefund,
		RefundAmount: 1200,
		Reason:       "it arrived broken",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ClaimRequested, refundClaim.Status)
	assert.True(t, strings.HasPrefix(refundClaim.ID, models.ClaimIDPrefix))

	// On a claim whose goods are sent again there is NO money to refund; a
	// filled-in amount would be a silent double payment where the customer
	// receives both the goods and the money.
	_, err = e.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      order.ID,
		Type:         models.ClaimReplace,
		RefundAmount: 1200,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "refund_amount")

	_, err = e.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: order.ID,
		Type:    models.ClaimType("credit"),
	})
	require.Error(t, err, "an undefined type has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestAfterSalesRecordsCannotBeOpenedOnACanceledOrder validates that a
// return/exchange/claim record cannot be opened on a canceled order.
//
// On a canceled order there are no delivered goods: there is nothing to return,
// to exchange or to be damaged either.
func TestAfterSalesRecordsCannotBeOpenedOnACanceledOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "the payment was declined"))

	cases := map[string]func() error{
		"return": func() error {
			_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID})
			return err
		},
		"exchange": func() error {
			_, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID})
			return err
		},
		"claim": func() error {
			_, err := e.svc.CreateClaim(ctx, service.CreateClaimInput{
				OrderID: order.ID, Type: models.ClaimRefund,
			})
			return err
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call()

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
		})
	}

	records, count, err := e.svc.ListReturns(ctx, order.ID, service.Page{})
	require.NoError(t, err)
	assert.Zero(t, count)
	assert.Empty(t, records)
}

// TestAfterSalesRecordsTakeTheOrderLock validates that the check is done
// RACE-FREE.
//
// A lockless check would only be true "at that moment": between the check and
// the write the order could be canceled and the record could end up attached to
// a canceled order.
func TestAfterSalesRecordsTakeTheOrderLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID})
	require.NoError(t, err)

	assert.Contains(t, e.store.lockedOrders, order.ID,
		"the return record has to take the lock of the order")
}

// TestAfterSalesRecordsNotFoundOnAMissingOrder validates that a missing order
// returns NotFound.
func TestAfterSalesRecordsNotFoundOnAMissingOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: "order_MISSING"})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestAfterSalesListingValidatesThePagination shows that the pagination
// parameters are validated.
func TestAfterSalesListingValidatesThePagination(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, _, err := e.svc.ListClaims(ctx, "", service.Page{})
	require.Error(t, err, "an empty order identifier has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, _, err = e.svc.ListExchanges(ctx, "order_X", service.Page{Limit: -1})
	require.Error(t, err, "a negative limit has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}
