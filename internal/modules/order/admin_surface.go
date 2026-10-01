package order

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// AdminName is the order module's admin panel surface in the container
// (ADR 0271).
const AdminName = ModuleName + ".admin"

// AfterSalesSurface is what the admin panel acts on an order's after-sales
// records through (ADR 0271).
//
// Every method is the order API's own: opening a record and the three
// withdrawals of a request are the service's (ADR 0272), and everything that
// moves stock, money or a parcel is the flow the API calls, so the panel acts
// under the API's conditions and fails closed where the API does. It speaks in
// primitives, as a cross-module surface does (ADR 0006).
//
// It opens an order's parcel as well (ADR 0324), through the fulfilling flow
// the API's open endpoint calls.
type AfterSalesSurface struct {
	svc        *service.Service
	flow       api.ReturnReceiving
	fulfilling api.Fulfilling
}

// OpenParcel opens a parcel for the order on the delivery it was sold, and
// reports whether the idempotency key had already opened it (ADR 0324): the
// panel's form carries one key, so a second press, or a reload of the page it
// landed on, opens nothing new.
func (s *AfterSalesSurface) OpenParcel(
	ctx context.Context, orderID, idempotencyKey string,
) (fulfillmentID string, alreadyOpen bool, err error) {
	if s == nil || s.fulfilling == nil {
		return "", false, errors.Unavailable(codeSetupFailed, "the fulfilling flow is not set up")
	}
	request, err := json.Marshal(struct {
		IdempotencyKey string `json:"idempotency_key"`
	}{idempotencyKey})
	if err != nil {
		return "", false, errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the parcel request could not be encoded")
	}

	return s.fulfilling.OpenForOrder(ctx, orderID, request)
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

// OpenReturn opens a return on the order naming the lines that come back, a
// quantity for each, and the refund it plans; it returns the record's id.
func (s *AfterSalesSurface) OpenReturn(
	ctx context.Context, orderID string, lineIDs []string, quantities, lineRefunds []int64,
	refundAmount int64, reason string,
) (string, error) {
	if len(lineIDs) != len(quantities) || len(lineIDs) != len(lineRefunds) {
		return "", paired(len(lineIDs), len(quantities), len(lineRefunds))
	}
	lines := make([]service.ReturnLineInput, 0, len(lineIDs))
	for i := range lineIDs {
		lines = append(lines, service.ReturnLineInput{
			OrderLineItemID: lineIDs[i], Quantity: quantities[i], RefundAmount: lineRefunds[i],
		})
	}
	ret, err := s.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID, RefundAmount: refundAmount, Reason: reason, Lines: lines,
	})

	return ret.ID, err
}

// OpenClaim opens a claim on the order, settled by "refund" or "replace".
func (s *AfterSalesSurface) OpenClaim(
	ctx context.Context, orderID, claimType string, refundAmount int64, reason string,
) (string, error) {
	claim, err := s.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: orderID, Type: models.ClaimType(claimType), RefundAmount: refundAmount, Reason: reason,
	})

	return claim.ID, err
}

// OpenExchange opens an exchange on the order; a negative difference is paid
// to the customer.
func (s *AfterSalesSurface) OpenExchange(
	ctx context.Context, orderID string, differenceDue int64, note string,
) (string, error) {
	exchange, err := s.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: orderID, DifferenceDue: differenceDue, Note: note,
	})

	return exchange.ID, err
}

// OpenReplacement records what a claim or an exchange will send: the order's
// lines and a quantity for each, variants the order never sold and a quantity
// for each (ADR 0279), how and from where.
func (s *AfterSalesSurface) OpenReplacement(
	ctx context.Context, claimID, exchangeID string, lineIDs []string, quantities []int64,
	variantIDs []string, variantQuantities []int64, shippingOptionID, locationID string,
) (string, error) {
	if len(lineIDs) != len(quantities) {
		return "", paired(len(lineIDs), len(quantities), len(lineIDs))
	}
	if len(variantIDs) != len(variantQuantities) {
		return "", paired(len(variantIDs), len(variantQuantities), len(variantIDs))
	}
	lines := make([]service.ReplacementLineInput, 0, len(lineIDs)+len(variantIDs))
	for i := range lineIDs {
		lines = append(lines, service.ReplacementLineInput{OrderLineItemID: lineIDs[i], Quantity: quantities[i]})
	}
	// A variant the order never sold is an item of its own, naming no line
	// (ADR 0145).
	for i := range variantIDs {
		lines = append(lines, service.ReplacementLineInput{VariantID: variantIDs[i], Quantity: variantQuantities[i]})
	}
	record, err := s.svc.CreateReplacement(ctx, service.CreateReplacementInput{
		ClaimID: claimID, ExchangeID: exchangeID, ShippingOptionID: shippingOptionID,
		LocationID: locationID, Lines: lines,
	})

	return record.ID, err
}

// paired refuses lines whose fields do not come in step: every line needs a
// quantity and the field typed beside it.
func paired(lines, quantities, beside int) error {
	return errors.Invalid(service.CodeInvalidInput,
		"every line needs a quantity and the field beside it: %d lines, %d quantities, %d beside",
		lines, quantities, beside)
}
