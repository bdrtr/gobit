package order

import (
	"context"
	"encoding/json"
	"time"

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
// the API's open endpoint calls. And it credits an order, puts a delivery on
// another option and corrects where an order ships, each from what the page
// was drawn with, so a form sent twice acts once (ADR 0388).
type AfterSalesSurface struct {
	svc        *service.Service
	flow       api.ReturnReceiving
	fulfilling api.Fulfilling
	invoicing  api.Invoicing
}

// OpenParcel opens a parcel for the order and reports whether the idempotency
// key had already opened it (ADR 0324): the panel's form carries one key, so a
// second press, or a reload of the page it landed on, opens nothing new. The
// parcel goes on the delivery deliveryID names, on the option it stands on now
// (ADR 0332), or, when deliveryID is empty, on the one the order was sold.
func (s *AfterSalesSurface) OpenParcel(
	ctx context.Context, orderID, deliveryID, idempotencyKey string,
) (fulfillmentID string, alreadyOpen bool, err error) {
	if s == nil || s.fulfilling == nil {
		return "", false, errors.Unavailable(codeSetupFailed, "the fulfilling flow is not set up")
	}
	optionID := ""
	if deliveryID != "" {
		if optionID, err = s.optionOf(ctx, orderID, deliveryID); err != nil {
			return "", false, err
		}
	}
	request, err := json.Marshal(struct {
		IdempotencyKey   string `json:"idempotency_key"`
		ShippingOptionID string `json:"shipping_option_id,omitempty"`
	}{idempotencyKey, optionID})
	if err != nil {
		return "", false, errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the parcel request could not be encoded")
	}

	return s.fulfilling.OpenForOrder(ctx, orderID, request)
}

// adminDelivery is one of an order's deliveries as the panel offers it (ADR
// 0332); the json tags are the contract with the panel.
type adminDelivery struct {
	ID               string `json:"id"`
	ShippingOptionID string `json:"shipping_option_id"`
	Name             string `json:"name"`
	// Amount is what the delivery costs as it stands, its latest change's
	// price when it was changed (ADR 0388).
	Amount int64 `json:"amount"`
}

// DeliveriesJSON lists the order's deliveries as they stand after their
// changes (ADR 0199), in the order they were sold (ADR 0332).
func (s *AfterSalesSurface) DeliveriesJSON(ctx context.Context, orderID string) (json.RawMessage, error) {
	deliveries, err := s.deliveriesOf(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]adminDelivery, 0, len(deliveries))
	for _, delivery := range deliveries {
		out = append(out, adminDelivery{
			ID: delivery.ID, ShippingOptionID: delivery.ShippingOptionID, Name: delivery.Name, Amount: delivery.Amount,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the deliveries could not be encoded")
	}

	return body, nil
}

// deliveriesOf reads the order's deliveries as they stand.
func (s *AfterSalesSurface) deliveriesOf(ctx context.Context, orderID string) ([]models.OrderShippingMethod, error) {
	if s == nil || s.svc == nil {
		return nil, errors.Unavailable(codeSetupFailed, "the order service is not set up")
	}
	detail, err := s.svc.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	return models.CurrentDeliveries(detail.ShippingMethods, detail.DeliveryChanges), nil
}

// optionOf is the option the order's delivery stands on now; a delivery the
// order does not have is not found.
func (s *AfterSalesSurface) optionOf(ctx context.Context, orderID, deliveryID string) (string, error) {
	deliveries, err := s.deliveriesOf(ctx, orderID)
	if err != nil {
		return "", err
	}
	for _, delivery := range deliveries {
		if delivery.ID == deliveryID {
			return delivery.ShippingOptionID, nil
		}
	}

	return "", errors.NotFound(service.CodeDeliveryMissing,
		"order %s has no delivery %s; draw the page again", orderID, deliveryID)
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

// adminEvidence is one piece of a claim's evidence as the panel lists it; the
// json tags are the contract with the panel, which cannot import this package.
type adminEvidence struct {
	ID        string    `json:"id"`
	UploadID  string    `json:"upload_id"`
	Caption   string    `json:"caption"`
	CreatedAt time.Time `json:"created_at"`
}

// ClaimEvidenceJSON lists the claim's evidence, oldest first (ADR 0325): the
// upload each names, by id, and what the operator said it shows.
func (s *AfterSalesSurface) ClaimEvidenceJSON(ctx context.Context, claimID string) (json.RawMessage, error) {
	evidence, err := s.svc.ListClaimEvidence(ctx, claimID)
	if err != nil {
		return nil, err
	}
	out := make([]adminEvidence, 0, len(evidence))
	for i := range evidence {
		out = append(out, adminEvidence{
			ID: evidence[i].ID, UploadID: evidence[i].UploadID,
			Caption: evidence[i].Caption, CreatedAt: evidence[i].CreatedAt,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeSetupFailed, "the evidence could not be encoded")
	}

	return body, nil
}

// AttachClaimEvidence binds an upload to the claim with the operator's caption
// and returns the evidence's id (ADR 0325).
func (s *AfterSalesSurface) AttachClaimEvidence(ctx context.Context, claimID, uploadID, caption string) (string, error) {
	evidence, err := s.svc.AttachClaimEvidence(ctx, claimID, service.AttachClaimEvidenceInput{
		UploadID: uploadID, Caption: caption,
	})
	if err != nil {
		return "", err
	}

	return evidence.ID, nil
}

// DetachClaimEvidence removes a piece of evidence from its claim; the file is
// left to the file module (ADR 0325).
func (s *AfterSalesSurface) DetachClaimEvidence(ctx context.Context, evidenceID string) error {
	return s.svc.DetachClaimEvidence(ctx, evidenceID)
}

// InvoiceOfOrder names the document issued for the order, and reports when
// there is none (ADR 0335).
func (s *AfterSalesSurface) InvoiceOfOrder(
	ctx context.Context, orderID string,
) (invoiceID, number, status string, found bool, err error) {
	if s == nil || s.invoicing == nil {
		return "", "", "", false, errors.Unavailable(codeSetupFailed, "the invoicing flow is not set up")
	}
	invoiceID, number, status, err = s.invoicing.InvoiceOfOrder(ctx, orderID)
	switch {
	case errors.IsNotFound(err):
		return "", "", "", false, nil
	case err != nil:
		return "", "", "", false, err
	}

	return invoiceID, number, status, true, nil
}

// IssueInvoice issues the order's document on the series the prefix names,
// or returns the one the order has, reporting which (ADR 0335). The buyer is
// the flow's JSON party, its printed fields named rather than placed: a name,
// address, country or e-mail left out is taken from the order (ADR 0193), and
// the tax number and office are as given.
func (s *AfterSalesSurface) IssueInvoice(
	ctx context.Context, orderID, seriesPrefix string, buyer json.RawMessage,
) (invoiceID, number string, alreadyIssued bool, err error) {
	if s == nil || s.invoicing == nil {
		return "", "", false, errors.Unavailable(codeSetupFailed, "the invoicing flow is not set up")
	}
	if len(buyer) == 0 {
		buyer = json.RawMessage(`{}`)
	}
	request, err := json.Marshal(struct {
		SeriesPrefix string          `json:"series_prefix"`
		Buyer        json.RawMessage `json:"buyer"`
	}{seriesPrefix, buyer})
	if err != nil {
		return "", "", false, errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the invoice request could not be encoded")
	}

	return s.invoicing.IssueForOrder(ctx, orderID, request)
}

// AmendmentsOfOrder lists the order's acts after its sale with the live
// document of each (ADR 0406).
func (s *AfterSalesSurface) AmendmentsOfOrder(ctx context.Context, orderID string) (json.RawMessage, error) {
	if s == nil || s.invoicing == nil {
		return nil, errors.Unavailable(codeSetupFailed, "the invoicing flow is not set up")
	}

	return s.invoicing.AmendmentsOfOrder(ctx, orderID)
}

// IssueAmendment documents one act after the order's sale on the series the
// prefix names, spreading its amount as the act's kind does, or returns the
// document it has, reporting which (ADR 0406).
func (s *AfterSalesSurface) IssueAmendment(
	ctx context.Context, orderID, seriesPrefix, kind, id string,
) (invoiceID, number string, alreadyIssued bool, err error) {
	if s == nil || s.invoicing == nil {
		return "", "", false, errors.Unavailable(codeSetupFailed, "the invoicing flow is not set up")
	}
	type act struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	request, err := json.Marshal(struct {
		SeriesPrefix string `json:"series_prefix"`
		Act          act    `json:"act"`
	}{seriesPrefix, act{kind, id}})
	if err != nil {
		return "", "", false, errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the amendment request could not be encoded")
	}

	return s.invoicing.IssueAmendment(ctx, orderID, request)
}

// CancelOrder cancels an order the checkout placed and writes off every unit
// not yet returned or written off, so their stock comes back (ADR 0285), as
// the API's cancel does (ADR 0339). A completed order and one with money
// collected are refused, and a second call writes nothing.
func (s *AfterSalesSurface) CancelOrder(ctx context.Context, orderID, reason string) error {
	if s == nil || s.svc == nil {
		return errors.Unavailable(codeSetupFailed, "the order service is not set up")
	}

	return s.svc.CancelPlacedOrder(ctx, orderID, reason)
}

// CompleteOrder marks a pending order completed, as the API's complete does
// (ADR 0340); a canceled or already completed order is refused.
func (s *AfterSalesSurface) CompleteOrder(ctx context.Context, orderID string) error {
	if s == nil || s.svc == nil {
		return errors.Unavailable(codeSetupFailed, "the order service is not set up")
	}
	_, err := s.svc.CompleteOrder(ctx, orderID)

	return err
}

// ArchiveOrder takes a completed order out of the daily lists, as the API's
// archive does (ADR 0340); an order that is not completed is refused.
func (s *AfterSalesSurface) ArchiveOrder(ctx context.Context, orderID string) error {
	if s == nil || s.svc == nil {
		return errors.Unavailable(codeSetupFailed, "the order service is not set up")
	}
	_, err := s.svc.ArchiveOrder(ctx, orderID)

	return err
}

// CancelOrderLine writes off units of one line of a live order, with the
// reason and the note kept with the cancellation, as the API's line
// cancellation does; readSpokenFor is how many of the line's units were
// asked back or written off when the operator read the line, and the module
// refuses when that has changed, so a form sent twice writes off once (ADR
// 0341).
func (s *AfterSalesSurface) CancelOrderLine(
	ctx context.Context, orderID, lineID string, readSpokenFor, quantity int64, reason, note string,
) error {
	if s == nil || s.svc == nil {
		return errors.Unavailable(codeSetupFailed, "the order service is not set up")
	}
	_, err := s.svc.CancelOrderLine(ctx, orderID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: quantity, Reason: reason, Note: note, ReadSpokenFor: &readSpokenFor,
	})

	return err
}

// adminCredit is one of an order's credits as the panel lists it; the json
// tags are the contract with the panel (ADR 0388).
type adminCredit struct {
	ID        string    `json:"id"`
	Amount    int64     `json:"amount"`
	Reason    string    `json:"reason"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// adminCredits is an order's credits with their sum, the credited total the
// module's ceiling reads under the order's lock.
type adminCredits struct {
	CreditedTotal int64         `json:"credited_total"`
	Lines         []adminCredit `json:"lines"`
}

// CreditLinesJSON lists the order's credits, oldest first, with their sum:
// the credited total a credit from the panel names (ADR 0388).
func (s *AfterSalesSurface) CreditLinesJSON(ctx context.Context, orderID string) (json.RawMessage, error) {
	if s == nil || s.svc == nil {
		return nil, errors.Unavailable(codeSetupFailed, "the order service is not set up")
	}
	credits, err := s.svc.ListCreditLines(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := adminCredits{Lines: make([]adminCredit, 0, len(credits))}
	for i := range credits {
		out.CreditedTotal += credits[i].Amount
		out.Lines = append(out.Lines, adminCredit{
			ID: credits[i].ID, Amount: credits[i].Amount, Reason: credits[i].Reason,
			Note: credits[i].Note, CreatedAt: credits[i].CreatedAt,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeSetupFailed, "the credits could not be encoded")
	}

	return body, nil
}

// CreditOrder writes off part of what the order owes, with the reason and
// the note, as the API's credit does; readCredited is the credited total the
// operator read, and the module refuses when that has changed, so a form
// sent twice credits once (ADR 0388).
func (s *AfterSalesSurface) CreditOrder(
	ctx context.Context, orderID string, readCredited, amount int64, reason, note string,
) error {
	if s == nil || s.svc == nil {
		return errors.Unavailable(codeSetupFailed, "the order service is not set up")
	}
	_, err := s.svc.CreateCreditLine(ctx, orderID, service.CreateCreditLineInput{
		Amount: amount, Reason: reason, Note: note, ReadCredited: &readCredited,
	})

	return err
}

// DeliveryQuoteJSON lists the options the order's deliveries can be put on,
// each with the price a change to it would write, through the fulfilling flow
// the API's change calls (ADR 0388).
func (s *AfterSalesSurface) DeliveryQuoteJSON(ctx context.Context, orderID string) (json.RawMessage, error) {
	if s == nil || s.fulfilling == nil {
		return nil, errors.Unavailable(codeSetupFailed, "the fulfilling flow is not set up")
	}

	return s.fulfilling.DeliveryQuoteJSON(ctx, orderID)
}

// ChangeDelivery puts the order's delivery on another option through the
// flow the API's change calls, at the price quotedAmount the operator was
// shown: a quote that moved since is refused (ADR 0388). A dearer option
// names the collection that took the difference (ADR 0200). The answer is the
// change written, or JSON null when the delivery was on that option already.
func (s *AfterSalesSurface) ChangeDelivery(
	ctx context.Context, orderID, deliveryID, optionID, collectionID string, quotedAmount int64,
) (json.RawMessage, error) {
	if s == nil || s.fulfilling == nil {
		return nil, errors.Unavailable(codeSetupFailed, "the fulfilling flow is not set up")
	}

	return s.fulfilling.ChangeDelivery(ctx, orderID, deliveryID, optionID, collectionID, &quotedAmount)
}

// CorrectShippingAddress corrects where the order ships through the flow the
// API's correction calls, from the shipping address row readAddressID the
// operator's form was drawn from; the module refuses when the order holds
// another row, and keeps the row's metadata (ADR 0388). The address is the
// order API's JSON.
func (s *AfterSalesSurface) CorrectShippingAddress(
	ctx context.Context, orderID string, address json.RawMessage, readAddressID string,
) error {
	if s == nil || s.fulfilling == nil {
		return errors.Unavailable(codeSetupFailed, "the fulfilling flow is not set up")
	}
	_, err := s.fulfilling.CorrectShippingAddress(ctx, orderID, address, readAddressID)

	return err
}
