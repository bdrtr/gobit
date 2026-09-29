package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// RecallReplacementUnits puts back the units a confirmed replacement promise
// took out of the count, when the parcel that was to carry them is canceled
// (ADR 0239).
//
// # Why it is keyed by the promise and not by a target
//
// [Service.ReturnCanceledInventory] takes a target because two different acts
// put a written-off line's units back (D82). A replacement's units have one way
// back, the cancellation of their parcel: a withdrawal refuses a promise that
// was confirmed (ADR 0237). So the promise itself is the key. The movement is a
// cancellation that names the promise as its reference, and the ledger is asked
// for one under the reservation's lock: the index that once held a
// cancellation's reference unique is gone (ADR 0142), so the lock is what makes
// a second call find the first one's row. It writes nothing then and returns
// [models.ErrMovementAlreadyRecorded].
//
// # What it refuses
//
// A promise that was never confirmed took nothing out of the count, and one
// made for a sale is the order's to put back, through the line's write-off.
// Both are refused as conflicts rather than written, because either would add
// units to a shelf that never lost them.
func (s *Service) RecallReplacementUnits(ctx context.Context, reservationID string) (models.InventoryLevel, error) {
	if err := requireText("reservation_id", reservationID); err != nil {
		return models.InventoryLevel{}, err
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		reservation, err := s.store.LockReservation(ctx, reservationID)
		if err != nil {
			return err
		}
		if reservation.Purpose != models.PurposeReplacement {
			return errors.Conflict(CodeReservationNotActive,
				"reservation %s was made for a %s; only a replacement's units are recalled here",
				reservationID, reservation.Purpose)
		}
		if reservation.Status != models.ReservationConfirmed {
			return errors.Conflict(CodeReservationNotActive,
				"reservation %s is %s, so its units never left the count", reservationID, reservation.Status)
		}

		recorded, err := s.store.CancellationRecorded(ctx, reservationID)
		if err != nil {
			return err
		}
		if recorded {
			return models.ErrMovementAlreadyRecorded
		}

		if err := s.requireOpenLocation(ctx, reservation.LocationID); err != nil {
			return err
		}
		if err := s.store.LockInventoryItemShared(ctx, reservation.InventoryItemID); err != nil {
			return err
		}
		level, err := s.store.LockInventoryLevel(ctx, reservation.InventoryItemID, reservation.LocationID)
		if err != nil {
			return err
		}

		stocked, err := addQuantity(level.StockedQuantity, reservation.Quantity)
		if err != nil {
			return err
		}
		out, err = s.writeQuantities(ctx, level, stocked, level.ReservedQuantity,
			models.MovementCancellation, "", reservationID, "")

		return err
	})
	if err != nil {
		return models.InventoryLevel{}, err
	}

	return out, nil
}
