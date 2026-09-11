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
	// the order never sold, and what bounds THAT is the exchange's money guard —
	// a dispatch is refused until the difference is funded (ADR 0120).
	VariantID string
	// Quantity is how many units of it are being sent.
	Quantity int64
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

	var out ReplacementRecord
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		orderID, err := s.openReplacementSource(ctx, in)
		if err != nil {
			return err
		}

		if _, err := s.requireLiveOrder(ctx, orderID, "a replacement record"); err != nil {
			return err
		}
		lines, err := s.store.ListLineItems(ctx, orderID)
		if err != nil {
			return err
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
			item, itemErr := s.store.CreateReplacementItem(ctx, models.ReplacementItem{
				ID:              models.NewReplacementItemID(),
				ReplacementID:   created.ID,
				OrderLineItemID: in.Lines[i].OrderLineItemID,
				VariantID:       in.Lines[i].VariantID,
				Quantity:        in.Lines[i].Quantity,
			})
			if itemErr != nil {
				return itemErr
			}
			items = append(items, item)
		}

		out = ReplacementRecord{Replacement: created, Items: items}

		return nil
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

		out, err = s.store.CancelReplacement(ctx, id)

		return err
	})
	if err != nil {
		return models.Replacement{}, err
	}

	return out, nil
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
// sold, so there is no bought quantity to compare it with (ADR 0145). What bounds
// it instead is the exchange's money guard: a dispatch is refused until the
// difference the operator asked for has been funded through its own collection
// (ADR 0120). That is a weaker bound than the line's and it is written down
// rather than implied, because the number the money is checked against is typed
// by the operator.
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
