package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// JoinParcelInput is what an addition puts into a pending parcel of the order
// it adds to (ADR 0428).
type JoinParcelInput struct {
	// FulfillmentID is the parcel joined.
	FulfillmentID string
	// ParentReference is the order the parcel was opened for.
	ParentReference string
	// AdditionReference is the order that joins it: the items written are its.
	AdditionReference string
	// Items are the units the addition puts in, each held to what its line
	// still owes. Empty asks for every unit it still owes, when ItemsOwed
	// allows that.
	Items []FulfillmentItemInput
	// ItemsOwed lets an empty Items ask for every unit the addition still
	// owes: the caller's answer to whether the addition was sold exactly one
	// delivery on a shipping option, the rule an open's default follows (ADR
	// 0409). Without it an empty Items is refused with [CodeItemsRequired].
	ItemsOwed bool
}

// JoinParcel puts units an addition still owes into a pending parcel of the
// order it adds to, as items the addition owns, and answers the addition's
// items in the parcel (ADR 0428).
//
// # What was wrong
//
// ADR 0197 let an addition travel in its parent's parcel through the order
// link alone, and the parcel's items stayed the parent's lines. Every count of
// what an order's parcels hold summed a parcel's items for the order it was
// opened for (ADR 0409), so the addition's units were held by nothing: the
// order route and the panel offered them to a second parcel, which took them,
// and a write-off of the addition's line put units that were in the box back
// on the shelf (gap D264). The counts now sum the items an order owns, which
// is what makes the items written here count for the addition.
//
// # What it holds, and under which lock
//
// It takes the units an open would: the items named, each within what its
// line still owes, or, when none are named and ItemsOwed allows it, every unit
// still owed; a unit not named stays owed, a backordered one among them (ADR
// 0392). The addition's ceiling is read from the bound before the
// transaction, as an open's is (ADR 0135), so the window ADR 0430 keeps is a
// join's too. Inside it the addition's dispatch lock is taken, the parcel is
// read under its row lock, what the addition's parcels hold is counted under
// the lock, and the units are held to the ceiling less that count as an
// open's are ([Service.holdToCeiling]), with its refusals.
//
// Only the addition's lock is taken. The join writes the addition's items and
// no other order's, and the parent's count sums the items the parent owns, so
// nothing the parent's lock guards moves; a cancel or a dispatch of the parcel
// waits on its row lock instead.
//
// # A repeat is answered from the parcel, in any state
//
// A parcel that already holds an item the addition owns has been joined: the
// call answers with those items and writes nothing, whatever the parcel's
// state, when it names no item or exactly those, so a caller whose binding
// failed can write it again after the parcel left, as an open's binding is
// repaired (ADR 0140). One naming other units is refused with
// [CodeJoinItemsDiffer], as an open's retry with another list is. The repeat
// is read before anything else is asked, and again under the locks for a join
// running at the same moment. A second join therefore cannot add units to a
// parcel that already carries the addition's: canceling the parcel is how its
// contents change. An addition joined before ADR 0428 has none, and joining it
// again while the parcel is pending puts its units in.
func (s *Service) JoinParcel(ctx context.Context, in JoinParcelInput) ([]models.FulfillmentItem, error) {
	if err := requireID(in.FulfillmentID, models.FulfillmentIDPrefix, "the fulfillment identifier"); err != nil {
		return nil, err
	}
	parent := strings.TrimSpace(in.ParentReference)
	if err := requireText("the parent's reference", parent); err != nil {
		return nil, err
	}
	addition := strings.TrimSpace(in.AdditionReference)
	if err := requireText("the addition's reference", addition); err != nil {
		return nil, err
	}
	if addition == parent {
		return nil, errors.Invalid(CodeInvalidInput,
			"order %s cannot join a parcel of its own; a parcel holds what its order owes "+
				"when it is opened", addition)
	}
	items, err := normalizeItems(in.Items)
	if err != nil {
		return nil, err
	}

	parcel, err := s.store.GetFulfillment(ctx, in.FulfillmentID)
	if err != nil {
		return nil, err
	}
	joined, err := s.joinedItems(ctx, parcel, parent, addition, items)
	if err != nil || len(joined) > 0 {
		return joined, err
	}
	if err := refuseLeftParcel(parcel, addition); err != nil {
		return nil, err
	}
	owedDefault := len(items) == 0
	if owedDefault && !in.ItemsOwed {
		return nil, errors.Invalid(CodeItemsRequired,
			"order %s names the units it puts in parcel %s; it may default to every unit it "+
				"owes only when it was sold exactly one delivery on a shipping option", addition, parcel.ID)
	}

	// Read BEFORE the transaction, for refuseOverDispatch's reason: asking
	// another module while holding this one's locks takes a second connection
	// from the same pool (ADR 0130, ADR 0135).
	ceilings, spoken, err := s.refuseOverDispatch(ctx, addition, "", items)
	if err != nil {
		return nil, err
	}

	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		joined = nil

		if err := s.store.LockReferenceDispatch(ctx, addition); err != nil {
			return err
		}

		parcel, err := s.store.LockFulfillment(ctx, in.FulfillmentID)
		if err != nil {
			return err
		}
		if joined, err = s.joinedItems(ctx, parcel, parent, addition, items); err != nil || len(joined) > 0 {
			return err
		}
		if err := refuseLeftParcel(parcel, addition); err != nil {
			return err
		}

		held, err := s.holdToCeiling(ctx, addition, items, ceilings, spoken, owedDefault)
		if err != nil {
			return err
		}
		for _, item := range held {
			saved, err := s.store.CreateFulfillmentItem(ctx, models.FulfillmentItem{
				ID:            models.NewFulfillmentItemID(),
				FulfillmentID: parcel.ID,
				LineItemID:    item.LineItemID,
				Quantity:      item.Quantity,
				Reference:     addition,
			})
			if err != nil {
				return err
			}
			joined = append(joined, saved)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return joined, nil
}

// joinedItems answers the items the addition owns in the parcel, empty when it
// has not joined it, and refuses a parcel that was not opened for the parent's
// goods: one of another order, or one bringing a return back.
//
// A repeat that names items has to name exactly those the parcel holds for
// the addition, compared as [Service.CreateFulfillment] compares a retry's
// list, as a set: one naming other units is refused with
// [CodeJoinItemsDiffer], because answering it as joined would report units
// the parcel does not hold. A repeat naming none is the binding's repair.
func (s *Service) joinedItems(
	ctx context.Context, parcel models.Fulfillment, parent, addition string, requested []FulfillmentItemInput,
) ([]models.FulfillmentItem, error) {
	if parcel.Reference != parent || parcel.ReturnID != "" {
		return nil, errors.Invalid(CodeInvalidInput,
			"parcel %s was not opened for the goods of order %s, so order %s cannot join it",
			parcel.ID, parent, addition)
	}

	items, err := s.store.ListFulfillmentItems(ctx, parcel.ID)
	if err != nil {
		return nil, err
	}
	var joined []models.FulfillmentItem
	for i := range items {
		if items[i].Reference == addition {
			joined = append(joined, items[i])
		}
	}
	if len(joined) == 0 {
		return nil, nil
	}
	saved, wanted := savedItemsKey(joined), requestedItemsKey(requested)
	if len(requested) > 0 && saved != wanted {
		return nil, errors.Conflict(CodeJoinItemsDiffer,
			"order %s already travels in parcel %s with a different item list: existing [%s], "+
				"requested [%s]; a joined parcel takes no more units, and canceling the parcel is "+
				"how to change what it carries", addition, parcel.ID, saved, wanted)
	}
	s.log.DebugContext(ctx, "the addition already travels in the parcel",
		"fulfillment", parcel.ID, "reference", addition)

	return joined, nil
}

// refuseLeftParcel refuses a parcel that is no longer pending: one on its
// way, delivered, come back or canceled takes no more goods.
func refuseLeftParcel(parcel models.Fulfillment, addition string) error {
	if parcel.Status == models.StatusPending {
		return nil
	}

	return errors.Conflict(CodeInvalidTransition,
		"a fulfillment in the %q state takes no more goods; order %s did not join %s",
		parcel.Status, addition, parcel.ID)
}
