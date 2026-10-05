package service

// # A backordered line waits for its units (ADR 0392)
//
// The checkout lets a backorder-permitting line through when no warehouse can
// cover it whole (ADR 0048). Its last step records the line here as a CLAIM,
// naming the warehouses fulfillment ranked for the order. Every write that
// raises a level's sellable quantity then fills the oldest waiting claims at
// that level it can complete, whole, as a reservation of the order confirmed
// in the same transaction — so the units an order waits for are never on sale.
//
// A write-off of the line withdraws from its waiting claim the units the
// cancellation flow counts as never leaving, and the module answers how many of
// the line's units it never deducted and where a filled claim deducted the rest,
// so the flow puts back only what the line's stock lost.

import (
	"context"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// fillWaiting gives the units a write just made sellable to the claims waiting
// at this level, oldest first, each whole as a reservation of its order
// confirmed at once; a claim the units cannot complete is passed over, and what
// completes none stays on sale.
//
// The caller holds the level's lock. The claims are locked after it, in queue
// order, and only those that fit what is sellable now; it returns the level
// after the fill and the claims it filled.
func (s *Service) fillWaiting(
	ctx context.Context, level models.InventoryLevel,
) (models.InventoryLevel, []string, error) {
	claims, err := s.store.LockWaitingBackordersAt(ctx, level.InventoryItemID, level.LocationID, level.Available())
	if err != nil {
		return models.InventoryLevel{}, nil, err
	}

	var filled []string
	for i := range claims {
		claim := claims[i]
		need := claim.Owed()
		if need <= 0 || need > level.Available() {
			continue
		}

		reservation, err := s.store.CreateReservation(ctx, models.Reservation{
			ID:              models.NewReservationID(),
			InventoryItemID: level.InventoryItemID,
			LocationID:      level.LocationID,
			Quantity:        need,
			LineItemID:      claim.OrderLineItemID,
			Purpose:         models.PurposeSale,
			Status:          models.ReservationActive,
		})
		if err != nil {
			return models.InventoryLevel{}, nil, err
		}

		reserved, err := addQuantity(level.ReservedQuantity, need)
		if err != nil {
			return models.InventoryLevel{}, nil, err
		}
		// The hold lowers what is sellable, so this write does not come back
		// here; the deduction below leaves it equal, so neither does that one.
		if level, err = s.writeQuantities(ctx, level, level.StockedQuantity, reserved, "", "", "", ""); err != nil {
			return models.InventoryLevel{}, nil, err
		}
		if level, err = s.deductHeld(ctx, reservation, level, claim.OrderID); err != nil {
			return models.InventoryLevel{}, nil, err
		}

		changed, err := s.store.FillBackorder(ctx, claim.ID, reservation.ID, level.LocationID)
		if err != nil {
			return models.InventoryLevel{}, nil, err
		}
		if changed == 0 {
			return models.InventoryLevel{}, nil, errors.Internal(CodeInconsistentState,
				"backorder %s was locked waiting and could not be marked filled", claim.ID)
		}

		s.log.InfoContext(ctx, "units that arrived went to an order waiting for them",
			"backorder_id", claim.ID, "order_id", claim.OrderID,
			"order_line_item_id", claim.OrderLineItemID, "inventory_item_id", level.InventoryItemID,
			"location_id", level.LocationID, "quantity", need, "reservation_id", reservation.ID)
		filled = append(filled, claim.ID)
	}

	return level, filled, nil
}

// ClaimBackorderInput is a line the checkout let through without stock.
type ClaimBackorderInput struct {
	// InventoryItemID is the item the line is owed.
	InventoryItemID string
	// OrderID and OrderLineItemID are the order module's line.
	OrderID         string
	OrderLineItemID string
	// Quantity is the units owed; for a bundle's part, the line's units times
	// the part's.
	Quantity int64
	// LocationIDs are the warehouses the claim may be filled at, in rank order;
	// empty when none could be ranked.
	LocationIDs []string
}

// ClaimBackorder records that an order line is owed quantity units of the item,
// fillable at locationIDs in that order. Every later write that makes units
// sellable at one of those warehouses fills the oldest waiting claims it can
// complete whole, in its own transaction (ADR 0392). It does not fill from stock
// already there; [Service.SettleBackorder] does. A second call for the same line
// and item returns the same claim, and one naming another quantity is a
// conflict.
func (s *Service) ClaimBackorder(ctx context.Context, in ClaimBackorderInput) (models.Backorder, error) {
	if err := requireText("inventory_item_id", in.InventoryItemID); err != nil {
		return models.Backorder{}, err
	}
	if err := requireText("order_id", strings.TrimSpace(in.OrderID)); err != nil {
		return models.Backorder{}, err
	}
	if err := requireText("order_line_item_id", strings.TrimSpace(in.OrderLineItemID)); err != nil {
		return models.Backorder{}, err
	}
	if in.Quantity <= 0 {
		return models.Backorder{}, errors.Invalid(CodeInvalidInput,
			"a backorder owes a positive quantity: %d", in.Quantity)
	}
	locations := make([]string, 0, len(in.LocationIDs))
	for _, id := range in.LocationIDs {
		if err := requireText("location_id", id); err != nil {
			return models.Backorder{}, err
		}
		if !slices.Contains(locations, id) {
			locations = append(locations, id)
		}
	}

	var out models.Backorder
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		// Shared, so claims do not wait on each other; it meets the deletion's
		// exclusive lock, which counts the waiting claims.
		if err := s.store.LockInventoryItemShared(ctx, in.InventoryItemID); err != nil {
			return err
		}

		created, fresh, err := s.store.CreateBackorder(ctx, models.Backorder{
			ID:              models.NewBackorderID(),
			InventoryItemID: in.InventoryItemID,
			OrderID:         in.OrderID,
			OrderLineItemID: in.OrderLineItemID,
			Quantity:        in.Quantity,
			LocationIDs:     locations,
			Status:          models.BackorderWaiting,
		})
		if err != nil {
			return err
		}
		if fresh {
			out = created
			return nil
		}

		existing, err := s.store.GetBackorderOfLine(ctx, in.OrderLineItemID, in.InventoryItemID)
		if err != nil {
			return err
		}
		if existing.Quantity != in.Quantity {
			return errors.Conflict(CodeBackorderMismatch,
				"line %s already owes %d of item %s, not %d", in.OrderLineItemID,
				existing.Quantity, in.InventoryItemID, in.Quantity)
		}
		out = existing

		return nil
	})
	if err != nil {
		return models.Backorder{}, err
	}

	return out, nil
}

// Settlement is what a settled line's claims answer, per inventory item.
type Settlement struct {
	// Undeducted is the units of the line this module never took off a shelf.
	Undeducted map[string]int64
	// FilledAt is the warehouse a filled claim deducted at.
	FilledAt map[string]string
}

// SettleBackorder brings each waiting claim of the line up to window of the
// line's units withdrawn, the units the cancellation flow counts as never
// leaving, then fills what still waits from stock on hand. It answers, per item,
// the units this module never deducted for the line and the warehouse a filled
// claim deducted at; a line with no claim answers neither.
//
// The withdrawal commits before the drain, so a drain that fails leaves the
// answers true: a claim withdrawn up to the window owes the shelf nothing,
// filled or not. The drain's errors are returned with the answers.
func (s *Service) SettleBackorder(
	ctx context.Context, orderLineItemID string, bought, window int64,
) (Settlement, error) {
	out := Settlement{Undeducted: map[string]int64{}, FilledAt: map[string]string{}}
	if err := requireText("order_line_item_id", orderLineItemID); err != nil {
		return out, err
	}
	if bought <= 0 {
		return out, errors.Invalid(CodeInvalidInput, "a line bought a positive quantity: %d", bought)
	}
	if window < 0 || window > bought {
		return out, errors.Invalid(CodeInvalidInput,
			"the units that will not leave are between 0 and the %d bought: %d", bought, window)
	}

	var waiting []models.Backorder
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		claims, err := s.store.LockBackordersOfLine(ctx, orderLineItemID)
		if err != nil {
			return err
		}

		for i := range claims {
			claim := claims[i]
			perUnit := claim.Quantity / bought
			if perUnit*bought != claim.Quantity {
				return errors.Internal(CodeInconsistentState,
					"backorder %s owes %d units, which is not a whole number per each of the %d bought",
					claim.ID, claim.Quantity, bought)
			}

			if claim.Status == models.BackorderWaiting {
				withdrawn := max(claim.WithdrawnQuantity, min(claim.Quantity, window*perUnit))
				if withdrawn != claim.WithdrawnQuantity {
					status := models.BackorderWaiting
					if withdrawn == claim.Quantity {
						status = models.BackorderWithdrawn
					}
					if claim, err = s.store.WithdrawBackorder(ctx, claim.ID, withdrawn, status); err != nil {
						return err
					}
				}
			}

			out.Undeducted[claim.InventoryItemID] = claim.Undeducted()
			if claim.Status == models.BackorderFilled {
				out.FilledAt[claim.InventoryItemID] = claim.FilledLocationID
			}
			if claim.Status == models.BackorderWaiting {
				waiting = append(waiting, claim)
			}
		}

		return nil
	})
	if err != nil {
		return Settlement{Undeducted: map[string]int64{}, FilledAt: map[string]string{}}, err
	}

	var drainErrs []error
	for i := range waiting {
		if err := s.drain(ctx, waiting[i]); err != nil {
			drainErrs = append(drainErrs, err)
		}
	}

	return out, errors.Join(drainErrs...)
}

// drain fills a waiting claim from stock already on hand, trying its warehouses
// in rank order, one transaction each, until it is filled. A closed warehouse
// and one with no level of the item are passed over.
//
// The fill is [Service.fillWaiting]'s, so the oldest claims that fit are filled
// first — the stock on hand goes to whoever waited longest, this claim or not.
func (s *Service) drain(ctx context.Context, claim models.Backorder) error {
	for _, locationID := range claim.LocationIDs {
		filled := false
		err := s.store.WithTx(ctx, func(ctx context.Context) error {
			if err := s.requireOpenLocation(ctx, locationID); err != nil {
				return err
			}
			if err := s.store.LockInventoryItemShared(ctx, claim.InventoryItemID); err != nil {
				return err
			}
			level, err := s.store.LockInventoryLevel(ctx, claim.InventoryItemID, locationID)
			if err != nil {
				return err
			}
			if level.Available() <= 0 {
				return nil
			}

			_, ids, err := s.fillWaiting(ctx, level)
			filled = slices.Contains(ids, claim.ID)

			return err
		})
		switch {
		case err == nil && filled:
			return nil
		case err == nil, errors.IsNotFound(err), errors.CodeOf(err) == CodeLocationClosed:
			continue
		default:
			return err
		}
	}

	return nil
}

// ListBackordersInput is a request for one item's claims.
type ListBackordersInput struct {
	// InventoryItemID is the item; it is required.
	InventoryItemID string
	// Status narrows the listing to one status; empty means every status.
	Status string
	Page
}

// ListBackorders returns a page of the item's claims in queue order and the
// total. An item that does not exist is errors.NotFound, and an unknown status
// is refused.
func (s *Service) ListBackorders(ctx context.Context, in ListBackordersInput) ([]models.Backorder, int64, error) {
	if err := requireText("inventory_item_id", in.InventoryItemID); err != nil {
		return nil, 0, err
	}
	status := models.BackorderStatus(in.Status)
	if status != "" && !status.Valid() {
		return nil, 0, errors.Invalid(CodeInvalidInput,
			"unknown backorder status %q; one of waiting, filled, withdrawn", in.Status)
	}
	paging, err := in.normalize()
	if err != nil {
		return nil, 0, err
	}
	if _, err := s.store.GetInventoryItem(ctx, in.InventoryItemID); err != nil {
		return nil, 0, err
	}

	return s.store.ListBackorders(ctx, models.BackorderFilter{
		InventoryItemID: in.InventoryItemID,
		Status:          status,
		Limit:           paging.Limit,
		Offset:          paging.Offset,
	})
}

// OpenLocationIDs lists every open warehouse, the set a backordered line's claim
// is ranked over.
func (s *Service) OpenLocationIDs(ctx context.Context) ([]string, error) {
	return s.store.OpenStockLocationIDs(ctx)
}
