package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// ReplacementDetailJSON returns a replacement with everything a flow needs to
// send it.
//
// # Why the surface is JSON
//
// The consumer is a flow that cannot import this module (ADR 0006), so the
// answer crosses as a document. The schema is [replacementDetailJSON]'s field
// tags.
//
// # Why the variant is joined HERE
//
// A replacement line points at an ORDER LINE and setting stock aside needs the
// VARIANT that line was sold for. Both halves are this module's data, and
// [Service.ReturnDetailJSON] gives the reason for joining them here: a caller
// pairing two of this module's surfaces itself would, when it got the pairing
// wrong, send the wrong product.
func (s *Service) ReplacementDetailJSON(
	ctx context.Context, replacementID string,
) (json.RawMessage, error) {
	if err := requireID("replacement_id", replacementID); err != nil {
		return nil, err
	}

	record, err := s.store.GetReplacement(ctx, replacementID)
	if err != nil {
		return nil, err
	}

	claim, err := s.store.GetClaim(ctx, record.ClaimID)
	if err != nil {
		return nil, err
	}

	items, err := s.store.ListReplacementItems(ctx, replacementID)
	if err != nil {
		return nil, err
	}

	lines, err := s.store.ListLineItems(ctx, claim.OrderID)
	if err != nil {
		return nil, err
	}

	variantOf := make(map[string]string, len(lines))
	for i := range lines {
		variantOf[lines[i].ID] = lines[i].VariantID
	}

	detail := replacementDetailJSON{
		ReplacementID:    record.ID,
		ClaimID:          record.ClaimID,
		ClaimStatus:      claim.Status.String(),
		OrderID:          claim.OrderID,
		Status:           record.Status.String(),
		ShippingOptionID: record.ShippingOptionID,
		LocationID:       record.LocationID,
		FulfillmentID:    record.FulfillmentID,
		Lines:            make([]replacementLineJSON, 0, len(items)),
	}
	for i := range items {
		variantID, onOrder := variantOf[items[i].OrderLineItemID]
		if !onOrder {
			// The same refusal [Service.ReturnDetailJSON] makes, for the mirror
			// reason: a line reported without its variant would let a caller
			// send nothing and believe it sent something.
			return nil, errors.Internal(CodeInconsistentState,
				"replacement %s names line %s, which is not on order %s",
				replacementID, items[i].OrderLineItemID, claim.OrderID)
		}
		detail.Lines = append(detail.Lines, replacementLineJSON{
			ReplacementItemID: items[i].ID,
			OrderLineItemID:   items[i].OrderLineItemID,
			VariantID:         variantID,
			Quantity:          items[i].Quantity,
			ReservationID:     items[i].ReservationID,
		})
	}

	return json.Marshal(detail)
}

// replacementDetailJSON is the wire form of a replacement for a flow.
type replacementDetailJSON struct {
	ReplacementID string `json:"replacement_id"`
	ClaimID       string `json:"claim_id"`
	// ClaimStatus is the claim's own status. A flow reads it to know whether
	// the claim still has to be settled — after a dispatch that died between
	// the parcel and the claim, the answer is yes and nothing else could say
	// so.
	ClaimStatus      string                `json:"claim_status"`
	OrderID          string                `json:"order_id"`
	Status           string                `json:"status"`
	ShippingOptionID string                `json:"shipping_option_id"`
	LocationID       string                `json:"location_id"`
	FulfillmentID    string                `json:"fulfillment_id"`
	Lines            []replacementLineJSON `json:"lines"`
}

// replacementLineJSON is one line of a replacement on the wire.
type replacementLineJSON struct {
	ReplacementItemID string `json:"replacement_item_id"`
	OrderLineItemID   string `json:"order_line_item_id"`
	VariantID         string `json:"variant_id"`
	Quantity          int64  `json:"quantity"`
	// ReservationID is the promise the units are already held under; it is
	// EMPTY until something sets them aside. A flow reads it to know whether it
	// has to make the promise or already made it.
	ReservationID string `json:"reservation_id"`
}

// RecordReplacementReservation writes the promise a line's units are held
// under.
//
// # Why it takes the replacement as well as the line
//
// The write is made under the replacement's lock, and the lock is what makes
// the pair of rules below decidable at all: the record has to still be open,
// and the line has to belong to it. A signature taking only the line id could
// check neither without reading its way back to the record anyway.
//
// # Why a second, different promise is refused
//
// A line already holding units under one promise and told to hold them under
// another would leave the first one standing with nothing to release it. The
// repetition of the SAME id is accepted, because that is a retry saying what it
// already said.
func (s *Service) RecordReplacementReservation(
	ctx context.Context, replacementID, itemID, reservationID string,
) error {
	if err := requireID("replacement_id", replacementID); err != nil {
		return err
	}
	if err := requireID("item_id", itemID); err != nil {
		return err
	}
	if err := requireID("reservation_id", reservationID); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		record, err := s.store.LockReplacement(ctx, replacementID)
		if err != nil {
			return err
		}
		if record.Status != models.ReplacementRequested {
			return errors.Conflict(CodeReplacementNotOpen,
				"replacement %s is %s; units cannot be set aside for it any more",
				replacementID, record.Status)
		}

		items, err := s.store.ListReplacementItems(ctx, replacementID)
		if err != nil {
			return err
		}
		for i := range items {
			if items[i].ID != itemID {
				continue
			}
			if items[i].ReservationID == reservationID {
				return nil
			}
			if items[i].ReservationID != "" {
				return errors.Conflict(CodeReplacementNotOpen,
					"line %s already holds its units under promise %s; a second promise "+
						"would leave the first standing with nothing to release it",
					itemID, items[i].ReservationID)
			}

			_, err = s.store.SetReplacementItemReservation(ctx, itemID, reservationID)

			return err
		}

		return errors.Invalid(CodeReplacementLineUnknown,
			"line %s is not a line of replacement %s", itemID, replacementID)
	})
}

// MarkReplacementDispatched records that the goods left, in the parcel named.
//
// # It is the RECORD half of sending a replacement
//
// The stock and the parcel are the flow's half, because both reach modules this
// one does not know. What this says is that they happened — and it refuses to
// say so unless every line names the promise its units left against, which is
// the one part of the claim this module can check for itself.
//
// # Repeating it is answered rather than refused
//
// A second call naming the SAME parcel returns the record as it stands, keeping
// the first moment: a retried flow must not make the record claim the goods
// left twice. A call naming a DIFFERENT parcel is refused, because one of the
// two answers is wrong and this module cannot tell which.
func (s *Service) MarkReplacementDispatched(
	ctx context.Context, replacementID, fulfillmentID string,
) (models.Replacement, error) {
	if err := requireID("replacement_id", replacementID); err != nil {
		return models.Replacement{}, err
	}
	if err := requireID("fulfillment_id", fulfillmentID); err != nil {
		return models.Replacement{}, err
	}

	var out models.Replacement
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockReplacement(ctx, replacementID)
		if err != nil {
			return err
		}

		switch current.Status {
		case models.ReplacementDispatched:
			if current.FulfillmentID != fulfillmentID {
				return errors.Conflict(CodeReplacementNotOpen,
					"replacement %s already left in parcel %s, not in %s",
					replacementID, current.FulfillmentID, fulfillmentID)
			}
			out = current

			return nil
		case models.ReplacementCanceled:
			return errors.Conflict(CodeReplacementNotOpen,
				"replacement %s was withdrawn; goods cannot leave against it", replacementID)
		case models.ReplacementRequested:
			// Below.
		default:
			return errors.Internal(CodeInconsistentState,
				"unknown replacement status %q (%s)", current.Status, replacementID)
		}

		items, err := s.store.ListReplacementItems(ctx, replacementID)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.Internal(CodeInconsistentState,
				"replacement %s has no lines; a dispatch of nothing is not a dispatch",
				replacementID)
		}
		for i := range items {
			if items[i].ReservationID == "" {
				return errors.Conflict(CodeReplacementNotHeld,
					"line %s of replacement %s names no promise, so nothing says its units "+
						"left the warehouse", items[i].ID, replacementID)
			}
		}

		out, err = s.store.DispatchReplacement(ctx, replacementID, fulfillmentID)

		return err
	})
	if err != nil {
		return models.Replacement{}, err
	}

	return out, nil
}
