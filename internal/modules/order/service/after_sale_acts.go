package service

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// An order's acts after the sale (ADR 0406): the order journal's entries for
// one order, whenever they were written, as the invoicing flow documents them.

// MaxAfterSaleActs is the most acts one order's read returns; an order holding
// more is refused rather than cut, as a journal window is.
const MaxAfterSaleActs = 500

// AfterSaleAct is one entry the order journal books on an order after its
// sale, with what a document of it needs, or an exchange that names its return
// (ADR 0432).
type AfterSaleAct struct {
	// Kind is the journal's kind, and ID the journal entry's id: the credit
	// line's, the delivery change's, the payment module's refund's or the
	// exchange's. An exchange that names its return is listed under
	// [models.JournalExchange] beside its funding and its refund.
	Kind       models.JournalKind
	ID         string
	OccurredAt time.Time
	// Amount is what the act moved, positive; for an exchange, what its live
	// replacements send, which its sale document carries.
	Amount int64
	// Documentable is false for an exchange's funding and refund, which no
	// document carries: an exchange written without a return carries a figure
	// the operator typed that names neither goods nor tax, and one that names
	// its return is documented as its exchange act. That act is documentable
	// once its return has been received and every live replacement of it has
	// left, at least one: the moment goods have moved both ways. A withdrawn
	// exchange is not.
	Documentable bool
	// Returned are the units a return's refund paid for, or the units an
	// exchange's return takes back, by order line; empty for every other kind.
	Returned []ReturnedUnits
	// Sent are the items an exchange's live replacements send, in the order
	// they were written, each at the price recorded when it was written;
	// empty for every other kind.
	Sent []SentItem
	// Withdrawn says the act is an exchange that was withdrawn (ADR 0432): it
	// is listed so documents issued before can still be seen, and canceled,
	// and it is documented no more.
	Withdrawn bool
}

// SentItem is one item an exchange that names its return sends, as its sale
// document prints it (ADR 0432).
type SentItem struct {
	// LineID is the order line the item sends again, and VariantID the
	// variant it sends instead; exactly one is set.
	LineID    string
	VariantID string
	// Title and ProductTitle name the goods: the order line's for a line, and
	// for a variant its catalog's when the act is read on its own, empty when
	// the catalog has no answer.
	Title        string
	ProductTitle string
	Quantity     int64
	// Price is what the item's units are sold at, recorded when it was
	// written.
	Price models.ReplacementPrice
}

// ReturnedUnits is how many units of one order line a return took back.
type ReturnedUnits struct {
	LineID   string
	Quantity int64
}

// AfterSaleActs lists the order's acts after its sale, oldest first: its
// credit lines, its cheaper and its paid dearer delivery changes, its funded
// exchanges and the refunds its returns, claims and exchanges caused, whose
// ids and kinds are the order journal's (ADR 0188), and each exchange that
// names its return, at the moment it was opened (ADR 0432).
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
	exchanged, err := s.exchangeActs(ctx, orderID, causes)
	if err != nil {
		return nil, err
	}
	acts = append(acts, exchanged...)
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
		if acts[i].Kind != kind || acts[i].ID != id {
			continue
		}
		if kind == models.JournalExchange {
			if err := s.describeSentVariants(ctx, acts[i].Sent); err != nil {
				return AfterSaleAct{}, err
			}
		}

		return acts[i], nil
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

// exchangeActs lists each exchange of the order that names its return as one
// act (ADR 0432): the units its return takes back and the items its live
// replacements send, at the prices recorded when they were written. A
// withdrawn one is listed as withdrawn and not documentable, since documents
// issued before its withdrawal still stand until they are canceled; the
// invoicing flow shows it only while one does. An exchange written without a
// return prices nothing and is not listed; its funding and its refund are.
func (s *Service) exchangeActs(ctx context.Context, orderID string, causes []models.AfterSaleCause) (
	[]AfterSaleAct, error,
) {
	var ids []string
	for i := range causes {
		if causes[i].Kind == causeExchange {
			ids = append(ids, causes[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	exchanges, err := s.store.ExchangesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	var lines map[string]models.OrderLineItem
	acts := make([]AfterSaleAct, 0, len(exchanges))
	for i := range exchanges {
		exchange := &exchanges[i]
		if !exchange.Priced() {
			continue
		}
		if lines == nil {
			read, err := s.store.ListLineItems(ctx, orderID)
			if err != nil {
				return nil, err
			}
			lines = make(map[string]models.OrderLineItem, len(read))
			for k := range read {
				lines[read[k].ID] = read[k]
			}
		}
		act, err := s.exchangeAct(ctx, exchange, lines)
		if err != nil {
			return nil, err
		}
		acts = append(acts, act)
	}

	return acts, nil
}

// exchangeAct reads one exchange that names its return as an act: the units
// its return takes back, the items its live replacements send oldest first,
// and whether goods have moved both ways: the return received and every one
// of those sent.
func (s *Service) exchangeAct(
	ctx context.Context, exchange *models.Exchange, lines map[string]models.OrderLineItem,
) (AfterSaleAct, error) {
	act := AfterSaleAct{
		Kind: models.JournalExchange, ID: exchange.ID, OccurredAt: exchange.CreatedAt.UTC(),
		Withdrawn: exchange.Status == models.ExchangeCanceled,
	}
	ret, err := s.store.GetReturn(ctx, exchange.ReturnID)
	if err != nil {
		return AfterSaleAct{}, err
	}
	returned, err := s.store.ListReturnItems(ctx, exchange.ReturnID)
	if err != nil {
		return AfterSaleAct{}, err
	}
	for _, item := range returned {
		act.Returned = append(act.Returned, ReturnedUnits{LineID: item.OrderLineItemID, Quantity: item.Quantity})
	}

	replacements, err := s.store.ListReplacementsByExchange(ctx, exchange.ID)
	if err != nil {
		return AfterSaleAct{}, err
	}
	live := make([]models.Replacement, 0, len(replacements))
	for i := range replacements {
		if replacements[i].Status != models.ReplacementCanceled {
			live = append(live, replacements[i])
		}
	}
	if len(live) == 0 {
		return act, nil
	}
	slices.SortFunc(live, func(a, b models.Replacement) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	ids := make([]string, 0, len(live))
	for i := range live {
		ids = append(ids, live[i].ID)
	}
	items, err := s.store.ReplacementItemsOf(ctx, ids)
	if err != nil {
		return AfterSaleAct{}, err
	}

	act.Documentable = ret.Status == models.ReturnReceived && !act.Withdrawn
	for i := range live {
		if live[i].Status != models.ReplacementDispatched {
			act.Documentable = false
		}
		for k := range items[live[i].ID] {
			item := &items[live[i].ID][k]
			if item.Price == nil {
				return AfterSaleAct{}, errors.Internal(CodeInconsistentState,
					"exchange %s names its return and sends item %s, which carries no price", exchange.ID, item.ID)
			}
			sent := SentItem{
				LineID: item.OrderLineItemID, VariantID: item.VariantID, Quantity: item.Quantity, Price: *item.Price,
			}
			if line, ok := lines[item.OrderLineItemID]; ok {
				sent.Title, sent.ProductTitle = line.Title, line.ProductTitle
			}
			act.Amount += item.Price.Total
			act.Sent = append(act.Sent, sent)
		}
	}

	return act, nil
}

// The query layer's names an exchange's sent variants are described under
// (ADR 0432), repeated as literals as the bundle's names are; internal/arch's
// TestTheBundleNamesAgree holds them to the checkout's, which the product
// module's provider answers.
const (
	CatalogEntityProduct  = "product"
	CatalogFieldTitle     = "title"
	CatalogFieldProductID = "product_id"
)

// describeSentVariants names the variants an exchange sends from the catalog,
// the variant's title and its product's as an order line keeps them
// (ADR 0365), when the exchange is documented: a replacement item keeps a
// price of its own but no title (ADR 0432). A variant the catalog no longer
// answers for, or an installation without one, leaves both empty, and the
// document names the variant by its id.
func (s *Service) describeSentVariants(ctx context.Context, sent []SentItem) error {
	if s.catalog == nil {
		return nil
	}
	var variantIDs []string
	for i := range sent {
		if sent[i].VariantID != "" && !slices.Contains(variantIDs, sent[i].VariantID) {
			variantIDs = append(variantIDs, sent[i].VariantID)
		}
	}
	if len(variantIDs) == 0 {
		return nil
	}

	variants, err := s.catalog.Graph(ctx, query.GraphSpec{
		Entity:  CatalogEntityVariant,
		Fields:  []string{query.IDField, CatalogFieldTitle, CatalogFieldProductID},
		Filters: map[string]any{CatalogFilterIDs: variantIDs},
		Limit:   len(variantIDs),
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"the variants the exchange sends could not be read")
	}
	titles := make(map[string]string, len(variants))
	productOf := make(map[string]string, len(variants))
	var productIDs []string
	for _, record := range variants {
		id, _ := record[query.IDField].(string)
		titles[id], _ = record[CatalogFieldTitle].(string)
		productOf[id], _ = record[CatalogFieldProductID].(string)
		if product := productOf[id]; product != "" && !slices.Contains(productIDs, product) {
			productIDs = append(productIDs, product)
		}
	}

	productTitles := make(map[string]string, len(productIDs))
	if len(productIDs) > 0 {
		products, err := s.catalog.Graph(ctx, query.GraphSpec{
			Entity:  CatalogEntityProduct,
			Fields:  []string{query.IDField, CatalogFieldTitle},
			Filters: map[string]any{CatalogFilterIDs: productIDs},
			Limit:   len(productIDs),
		})
		if err != nil {
			return errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
				"the products of the variants the exchange sends could not be read")
		}
		for _, record := range products {
			id, _ := record[query.IDField].(string)
			productTitles[id], _ = record[CatalogFieldTitle].(string)
		}
	}

	for i := range sent {
		if sent[i].VariantID == "" {
			continue
		}
		sent[i].Title = titles[sent[i].VariantID]
		sent[i].ProductTitle = productTitles[productOf[sent[i].VariantID]]
	}

	return nil
}
