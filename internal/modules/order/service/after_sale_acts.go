package service

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// An order's acts after the sale (ADR 0406): the order journal's entries for
// one order, whenever they were written, as the invoicing flow documents them.

// MaxAfterSaleActs is the most acts one order's read returns; an order holding
// more is refused rather than cut, as a journal window is.
const MaxAfterSaleActs = 500

// AfterSaleAct is one entry the order journal books on an order after its
// sale, with what a document of it needs.
type AfterSaleAct struct {
	// Kind is the journal's kind, and ID the journal entry's id: the credit
	// line's, the delivery change's, the payment module's refund's or the
	// exchange's.
	Kind       models.JournalKind
	ID         string
	OccurredAt time.Time
	// Amount is what the act moved, positive.
	Amount int64
	// Documentable is false for an exchange's funding and refund: the figure
	// is typed by the operator and names neither goods nor tax, so no document
	// can carry it.
	Documentable bool
	// Returned are the units a return's refund paid for, by order line; empty
	// for every other kind.
	Returned []ReturnedUnits
}

// ReturnedUnits is how many units of one order line a return took back.
type ReturnedUnits struct {
	LineID   string
	Quantity int64
}

// AfterSaleActs lists the order's acts after its sale, oldest first: its
// credit lines, its cheaper and its paid dearer delivery changes, its funded
// exchanges and the refunds its returns, claims and exchanges caused. The ids
// and kinds are the order journal's (ADR 0188).
func (s *Service) AfterSaleActs(ctx context.Context, orderID string) ([]AfterSaleAct, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the order id is required")
	}
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	credits, err := s.store.ListCreditLines(ctx, orderID)
	if err != nil {
		return nil, err
	}
	changes, err := s.store.DeliveryChangesByOrderIDs(ctx, []string{orderID})
	if err != nil {
		return nil, err
	}
	causes, err := s.store.OrderAfterSaleCauses(ctx, orderID, MaxAfterSaleActs)
	if err != nil {
		return nil, err
	}
	if len(causes) > MaxAfterSaleActs {
		return nil, errors.Invalid(CodeInvalidInput,
			"order %s has more than %d after-sale records", orderID, MaxAfterSaleActs)
	}

	acts := deliveryAndCreditActs(credits, changes[orderID])
	for i := range causes {
		cause := &causes[i]
		if cause.Kind == causeExchange && cause.FundedAt != nil {
			acts = append(acts, AfterSaleAct{
				Kind: models.JournalExchangeFunded, ID: cause.ID, OccurredAt: cause.FundedAt.UTC(),
				Amount: cause.DifferenceDue,
			})
		}
	}
	refunded, err := s.refundActs(ctx, order, causes)
	if err != nil {
		return nil, err
	}
	acts = append(acts, refunded...)
	if len(acts) > MaxAfterSaleActs {
		return nil, errors.Invalid(CodeInvalidInput,
			"order %s has more than %d acts after its sale", orderID, MaxAfterSaleActs)
	}

	slices.SortFunc(acts, func(a, b AfterSaleAct) int {
		return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), cmp.Compare(a.ID, b.ID))
	})

	return acts, nil
}

// AfterSaleAct reads one of the order's acts after its sale by its journal
// kind and id; NotFound when the order has no such act.
func (s *Service) AfterSaleAct(ctx context.Context, orderID string, kind models.JournalKind, id string) (
	AfterSaleAct, error,
) {
	acts, err := s.AfterSaleActs(ctx, orderID)
	if err != nil {
		return AfterSaleAct{}, err
	}
	for i := range acts {
		if acts[i].Kind == kind && acts[i].ID == id {
			return acts[i], nil
		}
	}

	return AfterSaleAct{}, errors.NotFound(CodeAfterSaleActUnknown, "order %s has no %s act %s", orderID, kind, id)
}

// deliveryAndCreditActs reads the credit lines and the delivery changes: a
// credit line a cheaper delivery wrote is the change's act, and a dearer
// change was paid before it was written (ADR 0199, 0200).
func deliveryAndCreditActs(credits []models.OrderCreditLine, changes []models.DeliveryChange) []AfterSaleAct {
	changeOf := make(map[string]string, len(changes))
	acts := make([]AfterSaleAct, 0, len(credits)+len(changes))
	for i := range changes {
		change := &changes[i]
		if change.CreditLineID != "" {
			changeOf[change.CreditLineID] = change.ID
		}
		if change.Difference > 0 {
			acts = append(acts, AfterSaleAct{
				Kind: models.JournalDeliveryUpgraded, ID: change.ID, OccurredAt: change.CreatedAt.UTC(),
				Amount: change.Difference, Documentable: true,
			})
		}
	}
	for i := range credits {
		credit := &credits[i]
		act := AfterSaleAct{
			Kind: models.JournalCreditLine, ID: credit.ID, OccurredAt: credit.CreatedAt.UTC(),
			Amount: credit.Amount, Documentable: true,
		}
		if changeID, ok := changeOf[credit.ID]; ok {
			act.Kind, act.ID = models.JournalDeliveryChanged, changeID
		}
		acts = append(acts, act)
	}

	return acts
}

// refundActs reads the refunds the order's returns, claims and exchanges
// caused, and the units each return took back. A refund in another currency
// than the order's is an error, as in the journal: its reference would name
// the wrong order.
func (s *Service) refundActs(ctx context.Context, order models.Order, causes []models.AfterSaleCause) (
	[]AfterSaleAct, error,
) {
	if s.refunds == nil || len(causes) == 0 {
		return nil, nil
	}
	kindOf := make(map[string]string, len(causes))
	ids := make([]string, 0, len(causes))
	for i := range causes {
		kindOf[causes[i].ID] = causes[i].Kind
		ids = append(ids, causes[i].ID)
	}

	raw, err := s.refunds.CausedRefundsOfJSON(ctx, ids)
	if err != nil {
		return nil, err
	}
	var refunds []causedRefund
	if err := json.Unmarshal(raw, &refunds); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the payment module's refunds could not be read")
	}

	var returnIDs []string
	acts := make([]AfterSaleAct, 0, len(refunds))
	causeOf := make([]string, 0, len(refunds))
	for i := range refunds {
		refund := &refunds[i]
		kind, ok := kindOf[refund.Reference]
		if !ok {
			continue
		}
		if refund.CurrencyCode != order.CurrencyCode {
			return nil, errors.Internal(CodeInvalidInput,
				"refund %s names %s %s in %s, and the order is in %s",
				refund.ID, kind, refund.Reference, refund.CurrencyCode, order.CurrencyCode)
		}
		act := AfterSaleAct{
			Kind: models.JournalReturnRefunded, ID: refund.ID, OccurredAt: refund.RefundedAt.UTC(),
			Amount: refund.Amount, Documentable: true,
		}
		switch kind {
		case causeClaim:
			act.Kind = models.JournalClaimRefunded
		case causeExchange:
			act.Kind, act.Documentable = models.JournalExchangeRefunded, false
		default:
			returnIDs = append(returnIDs, refund.Reference)
		}
		acts = append(acts, act)
		causeOf = append(causeOf, refund.Reference)
	}

	if len(returnIDs) == 0 {
		return acts, nil
	}
	items, err := s.store.ReturnItemsOf(ctx, returnIDs)
	if err != nil {
		return nil, err
	}
	for i := range acts {
		if acts[i].Kind != models.JournalReturnRefunded {
			continue
		}
		for _, item := range items[causeOf[i]] {
			acts[i].Returned = append(acts[i].Returned, ReturnedUnits{
				LineID: item.OrderLineItemID, Quantity: item.Quantity,
			})
		}
	}

	return acts, nil
}
