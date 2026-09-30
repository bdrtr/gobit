package order

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// AdminName is the order module's admin panel surface in the container
// (ADR 0271).
const AdminName = ModuleName + ".admin"

// AfterSalesSurface is what the admin panel acts on an order's after-sales
// records through (ADR 0271).
//
// Every method is the order API's own: the three withdrawals of a request are
// the service's, and everything that moves stock, money or a parcel is the
// flow the API calls, so the panel acts under the API's conditions and fails
// closed where the API does. It speaks in primitives, as a cross-module
// surface does (ADR 0006).
type AfterSalesSurface struct {
	svc  *service.Service
	flow api.ReturnReceiving
}

// ReceiveReturn records that a return's goods arrived at the location and puts
// their stock back; warnings need a human.
func (s *AfterSalesSurface) ReceiveReturn(
	ctx context.Context, returnID, locationID string,
) (restockedLines int, restockedUnits int64, warnings []string, err error) {
	return s.flow.ReceiveReturn(ctx, returnID, locationID)
}

// RefundReturn sends money back for a received return; zero is everything the
// collection has left.
func (s *AfterSalesSurface) RefundReturn(
	ctx context.Context, returnID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	return s.flow.RefundReturn(ctx, returnID, amount, reason)
}

// CancelReturn withdraws a return that was asked for and not received.
func (s *AfterSalesSurface) CancelReturn(ctx context.Context, returnID string) error {
	_, err := s.svc.CancelReturn(ctx, returnID)

	return err
}

// SettleClaim settles a claim of the refund kind by refunding it.
func (s *AfterSalesSurface) SettleClaim(
	ctx context.Context, claimID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	return s.flow.SettleClaim(ctx, claimID, amount, reason)
}

// CancelClaim withdraws a claim that was not settled.
func (s *AfterSalesSurface) CancelClaim(ctx context.Context, claimID string) error {
	_, err := s.svc.CancelClaim(ctx, claimID)

	return err
}

// FundExchange names the payment collection that answers an exchange's
// difference.
func (s *AfterSalesSurface) FundExchange(ctx context.Context, exchangeID, collectionID string) error {
	return s.flow.FundExchangeDifference(ctx, exchangeID, collectionID)
}

// RefundExchange sends a funded exchange's money back and takes the request
// back with it.
func (s *AfterSalesSurface) RefundExchange(ctx context.Context, exchangeID, reason string) error {
	return s.flow.RefundExchangeDifference(ctx, exchangeID, reason)
}

// CancelExchange withdraws an exchange that was not funded.
func (s *AfterSalesSurface) CancelExchange(ctx context.Context, exchangeID string) error {
	_, err := s.svc.CancelExchange(ctx, exchangeID)

	return err
}

// DispatchReplacement sends what a replacement promised; alreadySent says the
// goods had gone and nothing moved this time.
func (s *AfterSalesSurface) DispatchReplacement(
	ctx context.Context, replacementID string,
) (fulfillmentID string, sentUnits int64, alreadySent bool, err error) {
	return s.flow.DispatchReplacement(ctx, replacementID)
}

// WithdrawReplacement takes back a replacement that has not left and gives
// back the units it set aside.
func (s *AfterSalesSurface) WithdrawReplacement(ctx context.Context, replacementID string) error {
	return s.flow.WithdrawReplacement(ctx, replacementID)
}
