package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// ReplacementLineInput is one line of a replacement request.
type ReplacementLineInput struct {
	// OrderLineItemID is the order line being replaced; leave it empty to send
	// something the order did not sell.
	OrderLineItemID string
	// VariantID is the product being sent when it is not one of the order's.
	//
	// Exactly ONE of the two is given (ADR 0145). A line names goods the customer
	// already has, and what bounds it is what was bought; a variant names goods
	// the order never sold, and nothing bounds THAT but the exchange's
	// difference: a dispatch is refused only when a collection the exchange was
	// funded with no longer holds it (ADR 0124), and an unfunded difference
	// leaves the exchange open after its goods leave.
	VariantID string
	// Quantity is how many units of it are being sent.
	Quantity int64
	// UnitPrice is the operator's unit price for an item naming a variant, sent
	// by an exchange that names its return; nil prices it by the quote
	// (ADR 0432). Its tax is the quote's either way. It is refused on an item
	// naming a line, which is priced at what the line charged, and on any
	// replacement that prices nothing.
	UnitPrice *int64
}

// CreateReplacementInput is what a claim or an exchange promises to send.
type CreateReplacementInput struct {
	// ClaimID is the claim the replacement settles; leave it empty to send
	// against an exchange.
	ClaimID string
	// ExchangeID is the exchange the replacement settles; leave it empty to send
	// against a claim.
	//
	// Exactly ONE of the two is given. Both would answer "what settled this" twice
	// and neither would leave the record hanging off nothing.
	ExchangeID string
	// ShippingOptionID is HOW it will be sent.
	ShippingOptionID string
	// LocationID is the stock location it will be sent FROM.
	LocationID string
	// Note is a free-form note.
	Note string
	// Lines are the order lines being replaced and how many of each.
	Lines []ReplacementLineInput
}

// ReplacementRecord is a replacement together with its lines.
type ReplacementRecord struct {
	models.Replacement
	// Items are the lines being replaced.
	Items []models.ReplacementItem
}

// CreateReplacement records what a claim or an exchange will send, and sends
// nothing.
//
// # What this does and deliberately does NOT do
//
// It writes a record. No stock moves, no parcel is opened and no other module
// is called — the flow that does those things is a separate change, and this is
// the half it was missing: until now nothing in the schema could say WHAT a
// claim of type 'replace' was going to send, which is the first reason
// internal/workflows/returns refuses to settle one.
//
// # Why the source has to be OPEN
//
// A 'refund' claim is settled with money and its refund amount is the record of
// that; promising goods on it would leave two settlements for one claim. A
// claim or an exchange that is already completed or withdrawn has had its
// answer, and adding to it would change what a settled record says.
//
// # Why an exchange is a source at all
//
// An exchange IS goods out against goods back, so a record of what goes out is
// what it was always missing (ADR 0114). It carries no type to check: unlike a
// claim, every exchange settles with goods, and the money beside them — its
// difference — is what this framework still cannot move.
//
// # What it prices
//
// A replacement of an exchange that names its return is priced item by item
// when it is written, and the exchange's difference becomes what its live
// replacements send less what the return takes back (ADR 0432): an item naming
// a line at what the line charged for its units, an item naming a variant at
// the cart flow's quote, read before the transaction as the catalog is. A
// claim's replacement, and one of an exchange that names no return, is priced
// nothing.
//
// # Where the ceiling is checked
//
// Under the ORDER's lock, before the record is written, exactly as
// [Service.CreateReturn] does it: the sum this request would make is compared
// against what was bought on the line, and the sum the NEXT request reads is
// this very table. Writing first and validating after would leave a rejected
// replacement in the table for the length of the transaction.
//
// The ceiling counts replacements only. A line that was also returned is not
// counted against it: goods coming back and goods going out are different
// movements, and a customer who returned a broken unit is exactly the customer
// a replacement is for.
func (s *Service) CreateReplacement(
	ctx context.Context, in CreateReplacementInput,
) (ReplacementRecord, error) {
	if err := checkReplacementSource(in); err != nil {
		return ReplacementRecord{}, err
	}
	if err := requireID("shipping_option_id", in.ShippingOptionID); err != nil {
		return ReplacementRecord{}, err
	}
	if err := requireID("location_id", in.LocationID); err != nil {
		return ReplacementRecord{}, err
	}
	if len(in.Lines) == 0 {
		return ReplacementRecord{}, errors.Invalid(CodeInvalidInput,
			"a replacement has to say what to send: no lines were given")
	}
	if err := checkReplacementLines(in.Lines); err != nil {
		return ReplacementRecord{}, err
	}
	// Read before the transaction: the catalog is another module's, and no
	// lock of this one is held while it answers (ADR 0244).
	bundles, err := s.variantBundleParts(ctx, replacementVariantIDs(in.Lines))
	if err != nil {
		return ReplacementRecord{}, err
	}
	// The quote is read before the transaction too, and for the same reason;
	// the transaction checks again, under the exchange's lock, that the
	// exchange is still the one it was priced for.
	priced, quoted, err := s.replacementQuotes(ctx, in)
	if err != nil {
		return ReplacementRecord{}, err
	}

	var out ReplacementRecord
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		orderID, err := s.openReplacementSource(ctx, in)
		if err != nil {
			return err
		}

		if _, err := s.requireLiveOrder(ctx, orderID, "a replacement record"); err != nil {
			return err
		}
		var exchange models.Exchange
		if in.ExchangeID != "" {
			if exchange, err = s.lockOpenExchange(ctx, in.ExchangeID); err != nil {
				return err
			}
			if exchange.Priced() != priced {
				return errors.Conflict(CodeInconsistentState,
					"exchange %s changed whether it names a return while its replacement was priced",
					in.ExchangeID)
			}
		}
		lines, err := s.store.ListLineItems(ctx, orderID)
		if err != nil {
			return err
		}
		// What the exchange already sends of each line, read under its lock
		// before this replacement's items are written: a line item is priced
		// as the units after those (ADR 0432).
		var sent map[string]int64
		if priced {
			if sent, err = s.store.ExchangeLineUnits(ctx, exchange.ID); err != nil {
				return err
			}
		}
		promised, err := s.store.ReplacedQuantities(ctx, replacementLineIDs(in.Lines))
		if err != nil {
			return err
		}
		if err := checkReplacementQuantities(lines, promised, in.Lines); err != nil {
			return err
		}

		created, err := s.store.CreateReplacement(ctx, models.Replacement{
			ID:               models.NewReplacementID(),
			ClaimID:          in.ClaimID,
			ExchangeID:       in.ExchangeID,
			Status:           models.ReplacementRequested,
			ShippingOptionID: in.ShippingOptionID,
			LocationID:       in.LocationID,
			Note:             in.Note,
		})
		if err != nil {
			return err
		}

		items := make([]models.ReplacementItem, 0, len(in.Lines))
		for i := range in.Lines {
			var price *models.ReplacementPrice
			if priced {
				price = itemPrice(lines, sent, quoted, in.Lines, i)
				if err := fitsItsTotal(price, in.Lines[i].OrderLineItemID); err != nil {
					return err
				}
			}
			item, itemErr := s.store.CreateReplacementItem(ctx, models.ReplacementItem{
				ID:              models.NewReplacementItemID(),
				ReplacementID:   created.ID,
				OrderLineItemID: in.Lines[i].OrderLineItemID,
				VariantID:       in.Lines[i].VariantID,
				Quantity:        in.Lines[i].Quantity,
				Parts:           replacementParts(lines, bundles, in.Lines[i]),
				Price:           price,
			})
			if itemErr != nil {
				return itemErr
			}
			items = append(items, item)
		}

		out = ReplacementRecord{Replacement: created, Items: items}
		if !priced {
			return nil
		}
		_, err = s.deriveExchangeDifference(ctx, exchange)

		return err
	})
	if err != nil {
		return ReplacementRecord{}, err
	}

	return out, nil
}

// GetReplacement returns a replacement with its lines.
func (s *Service) GetReplacement(
	ctx context.Context, id string,
) (ReplacementRecord, error) {
	if err := requireID("id", id); err != nil {
		return ReplacementRecord{}, err
	}

	record, err := s.store.GetReplacement(ctx, id)
	if err != nil {
		return ReplacementRecord{}, err
	}
	items, err := s.store.ListReplacementItems(ctx, id)
	if err != nil {
		return ReplacementRecord{}, err
	}

	return ReplacementRecord{Replacement: record, Items: items}, nil
}

// ListReplacementsOfClaim returns a claim's replacements, newest first.
func (s *Service) ListReplacementsOfClaim(
	ctx context.Context, claimID string,
) ([]models.Replacement, error) {
	if err := requireID("claim_id", claimID); err != nil {
		return nil, err
	}

	return s.store.ListReplacementsByClaim(ctx, claimID)
}

// CancelReplacement withdraws a request that has not been acted on.
//
// Withdrawing is idempotent: a replacement that is already canceled is returned
// as it stands rather than refused. The repetition is the same request, and the
// answer to it has not changed — the shape [Service.CancelReturn] uses.
//
// A DISPATCHED replacement is refused. Withdrawing it would say the goods were
// never promised while they are with the carrier, and the schema would refuse
// the write anyway: the two moments imply their statuses in both directions.
//
// A replacement of an exchange that names its return is refused only while
// the exchange is funded (ADR 0432): its collection holds the buyer's money for
// what it sends, and taking a replacement out from under it would leave the
// buyer paying for goods that no longer go. The exchange's refund sends that
// money back and withdraws the exchange, and the replacement is withdrawn
// after it. While the exchange is requested its line items are priced again
// from each line's first unit and its difference is derived again, under its
// lock; once it is withdrawn or settled the figures stay as they stood, so a
// replacement withdrawn from a settled exchange leaves the buyer owed its
// share, which no route of this module pays.
func (s *Service) CancelReplacement(
	ctx context.Context, id string,
) (models.Replacement, error) {
	if err := requireID("id", id); err != nil {
		return models.Replacement{}, err
	}

	var out models.Replacement
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockReplacement(ctx, id)
		if err != nil {
			return err
		}
		if current.Status == models.ReplacementCanceled {
			out = current

			return nil
		}
		if current.Status == models.ReplacementDispatched {
			return errors.Conflict(CodeReplacementNotOpen,
				"replacement %s left in parcel %s; goods that have gone out are taken back "+
					"by a return rather than by withdrawing the request that sent them",
				id, current.FulfillmentID)
		}
		var exchange models.Exchange
		if current.ExchangeID != "" {
			if exchange, err = s.store.LockExchange(ctx, current.ExchangeID); err != nil {
				return err
			}
			if err := heldByItsExchange(exchange, id); err != nil {
				return err
			}
		}

		out, err = s.store.CancelReplacement(ctx, id)
		if err != nil || !exchange.Priced() || exchange.Status != models.ExchangeRequested {
			return err
		}
		if err := s.repriceExchangeLines(ctx, exchange); err != nil {
			return err
		}
		_, err = s.deriveExchangeDifference(ctx, exchange)

		return err
	})
	if err != nil {
		return models.Replacement{}, err
	}

	return out, nil
}

// heldByItsExchange refuses the withdrawal of a replacement whose exchange
// names its return and is funded: the collection it names holds the buyer's
// money for what the replacement sends (ADR 0432). It is one rule for the
// withdrawal and for the detail a flow reads before it releases any stock.
func heldByItsExchange(exchange models.Exchange, replacementID string) error {
	if !exchange.Priced() || exchange.Status != models.ExchangeFunded {
		return nil
	}

	return errors.Conflict(CodeExchangeDifferenceHeld,
		"exchange %s is funded: collection %s holds the buyer's money for what replacement %s sends; "+
			"refund the exchange's difference, which withdraws the exchange, and then withdraw the replacement",
		exchange.ID, exchange.PaymentCollectionID, replacementID)
}

// replacementQuotes says whether the replacement is priced — its source is an
// exchange that names its return — and quotes the items naming a variant when
// it is. A unit price is refused where nothing is priced and on an item naming
// a line, which is priced at what its line charged.
func (s *Service) replacementQuotes(
	ctx context.Context, in CreateReplacementInput,
) (priced bool, quoted map[int]*models.ReplacementPrice, err error) {
	var order models.Order
	if in.ExchangeID != "" {
		exchange, readErr := s.store.GetExchange(ctx, in.ExchangeID)
		if readErr != nil {
			return false, nil, readErr
		}
		if priced = exchange.Priced(); priced {
			if order, err = s.store.GetOrder(ctx, exchange.OrderID); err != nil {
				return false, nil, err
			}
		}
	}
	for i := range in.Lines {
		switch {
		case in.Lines[i].UnitPrice == nil:
		case !priced:
			return false, nil, errors.Invalid(CodeReplacementPriceRefused,
				"%s carries a unit price, and only an exchange that names its return prices what "+
					"it sends", replacementLineName(in.Lines[i]))
		case in.Lines[i].OrderLineItemID != "":
			return false, nil, errors.Invalid(CodeReplacementPriceRefused,
				"line %s is sent at what it was sold for; a unit price is named for a variant the "+
					"order did not sell", in.Lines[i].OrderLineItemID)
		default:
			if err := checkAmount("unit_price", *in.Lines[i].UnitPrice, models.MaxAmount); err != nil {
				return false, nil, err
			}
		}
	}
	if !priced {
		return false, nil, nil
	}
	if quoted, err = s.quoteVariantItems(ctx, order, in.Lines); err != nil {
		return false, nil, err
	}

	return true, quoted, nil
}

// replacementLineName names a requested line for a message.
func replacementLineName(line ReplacementLineInput) string {
	if line.OrderLineItemID != "" {
		return "line " + line.OrderLineItemID
	}

	return "variant " + line.VariantID
}

// itemPrice is the price of the i-th requested item of a priced replacement:
// its line's figures for the units after those the exchange already sends of
// it, or its quote.
func itemPrice(
	lines []models.OrderLineItem, sent map[string]int64, quoted map[int]*models.ReplacementPrice,
	requested []ReplacementLineInput, i int,
) *models.ReplacementPrice {
	if requested[i].VariantID != "" {
		return quoted[i]
	}
	for k := range lines {
		if lines[k].ID == requested[i].OrderLineItemID {
			return linePrice(lines[k], sent[lines[k].ID], requested[i].Quantity)
		}
	}

	return nil
}

// lockOpenExchange locks the exchange a replacement is written for and checks,
// under the lock, that it is still requested: the unlocked read that found its
// order could have been followed by a funding.
func (s *Service) lockOpenExchange(ctx context.Context, exchangeID string) (models.Exchange, error) {
	exchange, err := s.store.LockExchange(ctx, exchangeID)
	if err != nil {
		return models.Exchange{}, err
	}
	if exchange.Status != models.ExchangeRequested {
		return models.Exchange{}, errors.Conflict(CodeNotPending,
			"exchange %s is %s; a replacement can only be added to an open exchange",
			exchangeID, exchange.Status)
	}

	return exchange, nil
}

// checkReplacementLines validates the shape of the requested lines before any
// read.
//
// The duplicate check is here rather than left to the unique index, for the
// reason [checkReturnLines] gives: the index rejects the SECOND insert, so the
// first would already be written and the error would name a constraint instead
// of the mistake.
func checkReplacementLines(lines []ReplacementLineInput) error {
	seen := make(map[string]bool, len(lines))
	for i := range lines {
		named, err := replacementItemName(lines[i])
		if err != nil {
			return err
		}
		if lines[i].Quantity <= 0 {
			return errors.Invalid(CodeInvalidInput,
				"the replaced quantity has to be positive: %s, quantity %d",
				named, lines[i].Quantity)
		}
		if seen[named] {
			return errors.Invalid(CodeInvalidInput,
				"%s appears twice in one replacement; the quantity carries the count",
				named)
		}
		seen[named] = true
	}

	return nil
}

// replacementItemName answers what one item is sending, and refuses a request
// that names neither thing or both.
//
// EXACTLY ONE, and it is checked here rather than left to the schema's CHECK for
// the reason the duplicate check gives above: a constraint violation names a
// constraint, and the person filling in a form wants to be told which field to
// fill. The database keeps the same rule underneath, because a service is not the
// only thing that writes rows.
func replacementItemName(line ReplacementLineInput) (string, error) {
	switch {
	case line.OrderLineItemID != "" && line.VariantID != "":
		return "", errors.Invalid(CodeInvalidInput,
			"a replacement item names either an order line or a variant, and this one "+
				"names both (%s and %s); a row with two answers to \"what is being sent\" "+
				"leaves the dispatch to pick",
			line.OrderLineItemID, line.VariantID)
	case line.OrderLineItemID != "":
		return line.OrderLineItemID, requireID("order_line_item_id", line.OrderLineItemID)
	case line.VariantID != "":
		return line.VariantID, requireID("variant_id", line.VariantID)
	default:
		return "", errors.Invalid(CodeInvalidInput,
			"a replacement item has to name what is being sent: an order line to send "+
				"more of, or a variant the order did not sell")
	}
}

// checkReplacementQuantities verifies that the requested lines belong to the
// order and that no more of one is promised than was bought on it.
//
// # A variant-shaped item is bounded by something else
//
// The ceiling here is "no more of a line than was bought on it", and an item that
// names a VARIANT is not against any line's ceiling — it is goods the order never
// sold, so there is no bought quantity to compare it with (ADR 0145). What
// stands in its place is the exchange's difference, and it is a weaker bound
// than the line's, written down rather than implied: a dispatch is refused
// only when a collection the exchange was funded with no longer holds the
// difference (ADR 0124), and an unfunded difference leaves the exchange open
// after its goods leave. An exchange that names its return fixes the figure,
// the variant's quoted price in it (ADR 0432), and not the dispatch; on one
// that names none the figure is the operator's.
func checkReplacementQuantities(
	lines []models.OrderLineItem,
	alreadyPromised map[string]int64,
	requested []ReplacementLineInput,
) error {
	ordered := make(map[string]int64, len(lines))
	for i := range lines {
		ordered[lines[i].ID] = lines[i].Quantity
	}

	for i := range requested {
		if requested[i].VariantID != "" {
			continue
		}

		bought, onOrder := ordered[requested[i].OrderLineItemID]
		if !onOrder {
			return errors.Invalid(CodeReplacementLineUnknown,
				"line %s is not on this order", requested[i].OrderLineItemID)
		}

		total := alreadyPromised[requested[i].OrderLineItemID] + requested[i].Quantity
		if total > bought {
			return errors.Conflict(CodeReplacementQuantityExceeded,
				"more of line %s was promised than was bought: %d requested plus %d already, %d bought",
				requested[i].OrderLineItemID, requested[i].Quantity,
				alreadyPromised[requested[i].OrderLineItemID], bought)
		}
	}

	return nil
}

// replacementParts is what one unit of a replacement item holds: the parts of
// the line it replaces, or of the bundle variant it names as the catalog makes
// it now (ADR 0244); nothing for anything else.
func replacementParts(
	lines []models.OrderLineItem, bundles map[string][]models.ReplacementItemPart, line ReplacementLineInput,
) []models.ReplacementItemPart {
	if line.VariantID != "" {
		return bundles[line.VariantID]
	}

	return replacementPartsOf(lines, line.OrderLineItemID)
}

// replacementVariantIDs is the variants the request names instead of lines.
func replacementVariantIDs(lines []ReplacementLineInput) []string {
	out := make([]string, 0, len(lines))
	for i := range lines {
		if lines[i].VariantID != "" {
			out = append(out, lines[i].VariantID)
		}
	}

	return out
}

// replacementPartsOf is what one unit of a replacement item that names a line
// holds: the components the line sold (ADR 0238), or nothing for a line that
// sold no bundle.
//
// They are copied from the LINE rather than read from the catalog, for the
// reason ADR 0235 gives the put-back flows: a bundle edited after the sale
// would otherwise send parts the customer never bought.
func replacementPartsOf(lines []models.OrderLineItem, lineID string) []models.ReplacementItemPart {
	for i := range lines {
		if lines[i].ID != lineID || len(lines[i].Components) == 0 {
			continue
		}
		parts := make([]models.ReplacementItemPart, 0, len(lines[i].Components))
		for _, c := range lines[i].Components {
			parts = append(parts, models.ReplacementItemPart{VariantID: c.VariantID, Quantity: c.Quantity})
		}

		return parts
	}

	return nil
}

// replacementLineIDs is the line ids of the requested lines.
//
// Variant-shaped items are LEFT OUT: they name no line, and an empty string in
// the list would make the sum query look for a line called "".
func replacementLineIDs(lines []ReplacementLineInput) []string {
	out := make([]string, 0, len(lines))
	for i := range lines {
		if lines[i].OrderLineItemID == "" {
			continue
		}
		out = append(out, lines[i].OrderLineItemID)
	}

	return out
}

// checkReplacementSource requires EXACTLY ONE source and validates its shape.
//
// The rule is checked before any read because it is about the request rather
// than about the world: a body naming both records, or neither, is malformed
// whatever the database holds.
func checkReplacementSource(in CreateReplacementInput) error {
	switch {
	case in.ClaimID != "" && in.ExchangeID != "":
		return errors.Invalid(CodeInvalidInput,
			"a replacement settles ONE record: claim %s and exchange %s were both given",
			in.ClaimID, in.ExchangeID)
	case in.ClaimID != "":
		return requireID("claim_id", in.ClaimID)
	case in.ExchangeID != "":
		return requireID("exchange_id", in.ExchangeID)
	default:
		return errors.Invalid(CodeInvalidInput,
			"a replacement has to say what it settles: neither a claim nor an exchange was given")
	}
}

// openReplacementSource reads the source, refuses one that is not open, and
// returns the ORDER it belongs to.
//
// The order comes from here rather than from the caller: the record hangs off a
// claim or an exchange, and both name their order themselves. A caller passing
// one would be passing a fact this module already holds, and the two could
// disagree.
func (s *Service) openReplacementSource(
	ctx context.Context, in CreateReplacementInput,
) (string, error) {
	if in.ExchangeID != "" {
		exchange, err := s.store.GetExchange(ctx, in.ExchangeID)
		if err != nil {
			return "", err
		}
		if exchange.Status != models.ExchangeRequested {
			return "", errors.Conflict(CodeNotPending,
				"exchange %s is %s; a replacement can only be added to an open exchange",
				in.ExchangeID, exchange.Status)
		}

		return exchange.OrderID, nil
	}

	claim, err := s.store.GetClaim(ctx, in.ClaimID)
	if err != nil {
		return "", err
	}
	if claim.Type != models.ClaimReplace {
		return "", errors.Conflict(CodeClaimNotReplaceable,
			"claim %s is settled with %s, so it sends no goods", in.ClaimID, claim.Type)
	}
	if claim.Status != models.ClaimRequested {
		return "", errors.Conflict(CodeNotPending,
			"claim %s is %s; a replacement can only be added to an open claim",
			in.ClaimID, claim.Status)
	}

	return claim.OrderID, nil
}

// ListReplacementsOfExchange returns an exchange's replacements, newest first.
func (s *Service) ListReplacementsOfExchange(
	ctx context.Context, exchangeID string,
) ([]models.Replacement, error) {
	if err := requireID("exchange_id", exchangeID); err != nil {
		return nil, err
	}
	// The exchange's existence is verified for the reason
	// [Service.ListReplacementsOfClaim] verifies the claim's: an empty slice for
	// a record that is not there reads as "it promised nothing".
	if _, err := s.store.GetExchange(ctx, exchangeID); err != nil {
		return nil, err
	}

	return s.store.ListReplacementsByExchange(ctx, exchangeID)
}
