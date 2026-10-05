package service

// # Stock on its way (ADR 0399)
//
// Units a supplier owes a warehouse are an EXPECTED SUPPLIER RECEIPT: an item,
// an open warehouse, a positive quantity and the moment the units are expected
// to be sellable there. Receiving one writes the counted units through the
// ledger as a `supplier_receipt` naming it, in the transaction that closes it,
// so ADR 0392's waiting claims are filled first, as by any write that makes
// units sellable. A status leaves `expected` once: a repeat that matches what
// happened answers the receipt unchanged, and any other answers
// [CodeSupplierReceiptNotExpected].

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// RecordSupplierReceiptInput is units a supplier owes a warehouse.
type RecordSupplierReceiptInput struct {
	// InventoryItemID and LocationID are the item owed and the warehouse; the
	// warehouse has to be open.
	InventoryItemID string
	LocationID      string
	// Quantity is the units expected; it has to be positive.
	Quantity int64
	// ExpectedAt is when the units are expected to be SELLABLE there, after
	// put-away; it is required.
	ExpectedAt time.Time
	// Reference is the embedder's own document number; optional, and not blank
	// when given.
	Reference string
}

// RecordSupplierReceipt records an expected supplier receipt.
//
// The location is locked shared and the item shared, so a close or an item
// deletion that is counting the receipts either counts this one or finds this
// waiting on it and then refuses or finds the item gone.
func (s *Service) RecordSupplierReceipt(
	ctx context.Context, in RecordSupplierReceiptInput,
) (models.SupplierReceipt, error) {
	if err := requireIDs(in.InventoryItemID, in.LocationID); err != nil {
		return models.SupplierReceipt{}, err
	}
	if in.Quantity <= 0 {
		return models.SupplierReceipt{}, errors.Invalid(CodeInvalidInput,
			"a supplier receipt expects a positive quantity: %d", in.Quantity)
	}
	if in.ExpectedAt.IsZero() {
		return models.SupplierReceipt{}, errors.Invalid(CodeInvalidInput,
			"a supplier receipt names when its units are expected to be sellable")
	}
	reference := strings.TrimSpace(in.Reference)
	if in.Reference != "" && reference == "" {
		return models.SupplierReceipt{}, errors.Invalid(CodeInvalidInput,
			"a reference, when given, cannot be blank")
	}
	if err := checkTextLen("reference", reference); err != nil {
		return models.SupplierReceipt{}, err
	}

	var out models.SupplierReceipt
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireOpenLocation(ctx, in.LocationID); err != nil {
			return err
		}
		if err := s.store.LockInventoryItemShared(ctx, in.InventoryItemID); err != nil {
			return err
		}

		created, err := s.store.CreateSupplierReceipt(ctx, models.SupplierReceipt{
			ID:              models.NewSupplierReceiptID(),
			InventoryItemID: in.InventoryItemID,
			LocationID:      in.LocationID,
			Quantity:        in.Quantity,
			ExpectedAt:      in.ExpectedAt.UTC(),
			Reference:       reference,
			Status:          models.SupplierReceiptExpected,
		})
		out = created

		return err
	})
	if err != nil {
		return models.SupplierReceipt{}, err
	}

	return out, nil
}

// ReceiveSupplierReceipt writes the counted units of an expected receipt through
// the ledger as a [models.MovementSupplierReceipt] naming it and closes the
// receipt with that movement's count and moment, in one transaction. The count
// may differ from the quantity expected; units the supplier still owes are a new
// receipt. A level the warehouse did not have is opened, and the claims waiting
// there are filled first (ADR 0392): the level returned is the one after the
// fill.
//
// A receipt no longer expected is answered without a lock, because a status
// leaves expected once: received with the same count is the receipt unchanged
// and the level as it reads now (nil when there is none), so a retry finishes
// even after the warehouse closed; anything else is
// [CodeSupplierReceiptNotExpected]. Another item's receipt is NotFound.
//
// The locks are the location (shared), the item (EXCLUSIVELY, since the receipt
// may open the level), the level and then the receipt, whose status is checked
// again under its lock.
func (s *Service) ReceiveSupplierReceipt(
	ctx context.Context, itemID, receiptID string, quantity int64,
) (models.SupplierReceipt, *models.InventoryLevel, error) {
	if err := requireText("inventory_item_id", itemID); err != nil {
		return models.SupplierReceipt{}, nil, err
	}
	if err := requireText("receipt_id", receiptID); err != nil {
		return models.SupplierReceipt{}, nil, err
	}
	if quantity <= 0 {
		return models.SupplierReceipt{}, nil, errors.Invalid(CodeInvalidInput,
			"a receipt receives the positive quantity counted: %d", quantity)
	}

	receipt, err := s.supplierReceiptOf(ctx, itemID, receiptID)
	if err != nil {
		return models.SupplierReceipt{}, nil, err
	}
	if receipt.Status != models.SupplierReceiptExpected {
		if err := receivedAlready(receipt, quantity); err != nil {
			return models.SupplierReceipt{}, nil, err
		}
		level, err := s.levelAt(ctx, receipt.InventoryItemID, receipt.LocationID)

		return receipt, level, err
	}

	var (
		out   models.SupplierReceipt
		level *models.InventoryLevel
	)
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireOpenLocation(ctx, receipt.LocationID); err != nil {
			return err
		}
		if err := s.store.LockInventoryItem(ctx, itemID); err != nil {
			return err
		}
		current, err := s.store.LockInventoryLevel(ctx, itemID, receipt.LocationID)
		found := false
		switch {
		case err == nil:
			found = true
		case errors.HasKind(err, errors.KindNotFound):
			// The item is locked and exists; the level is opened below.
		default:
			return err
		}

		locked, err := s.store.LockSupplierReceipt(ctx, receiptID)
		if err != nil {
			return err
		}
		if locked.Status != models.SupplierReceiptExpected {
			if err := receivedAlready(locked, quantity); err != nil {
				return err
			}
			out = locked
			if found {
				level = &current
			}

			return nil
		}

		var after models.InventoryLevel
		if found {
			stocked, err := addQuantity(current.StockedQuantity, quantity)
			if err != nil {
				return err
			}
			after, err = s.writeQuantities(ctx, current, stocked, current.ReservedQuantity,
				models.MovementSupplierReceipt, "", locked.ID, "")
			if err != nil {
				return err
			}
		} else {
			after, err = s.openLevel(ctx, itemID, locked.LocationID, quantity,
				models.MovementSupplierReceipt, locked.ID)
			if err != nil {
				return err
			}
		}
		level = &after

		changed, err := s.store.ReceiveSupplierReceipt(ctx, locked.ID)
		if err != nil {
			return err
		}
		if changed == 0 {
			return errors.Internal(CodeInconsistentState,
				"supplier receipt %s was locked expected and could not be closed as received", locked.ID)
		}
		out, err = s.store.GetSupplierReceipt(ctx, locked.ID)

		return err
	})
	if err != nil {
		return models.SupplierReceipt{}, nil, err
	}

	return out, level, nil
}

// CancelSupplierReceipt closes an expected receipt as canceled; nothing is
// written to the stock. Canceling a canceled receipt answers it unchanged, and
// canceling a received one is [CodeSupplierReceiptNotExpected]. It locks the
// receipt alone.
func (s *Service) CancelSupplierReceipt(ctx context.Context, itemID, receiptID string) (models.SupplierReceipt, error) {
	if err := requireText("inventory_item_id", itemID); err != nil {
		return models.SupplierReceipt{}, err
	}
	if err := requireText("receipt_id", receiptID); err != nil {
		return models.SupplierReceipt{}, err
	}

	receipt, err := s.supplierReceiptOf(ctx, itemID, receiptID)
	if err != nil {
		return models.SupplierReceipt{}, err
	}
	if receipt.Status != models.SupplierReceiptExpected {
		return receipt, canceledAlready(receipt)
	}

	var out models.SupplierReceipt
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		locked, err := s.store.LockSupplierReceipt(ctx, receiptID)
		if err != nil {
			return err
		}
		if locked.Status != models.SupplierReceiptExpected {
			out = locked
			return canceledAlready(locked)
		}

		changed, err := s.store.CancelSupplierReceipt(ctx, locked.ID)
		if err != nil {
			return err
		}
		if changed == 0 {
			return errors.Internal(CodeInconsistentState,
				"supplier receipt %s was locked expected and could not be canceled", locked.ID)
		}
		out, err = s.store.GetSupplierReceipt(ctx, locked.ID)

		return err
	})
	if err != nil {
		return models.SupplierReceipt{}, err
	}

	return out, nil
}

// ListSupplierReceiptsInput is a request for one item's receipts.
type ListSupplierReceiptsInput struct {
	// InventoryItemID is the item; it is required.
	InventoryItemID string
	// Status narrows the listing to one status; empty means every status.
	Status string
	Page
}

// ListSupplierReceipts returns a page of the item's receipts in the order they
// are expected, and the total. An item that does not exist is errors.NotFound,
// and an unknown status is refused.
func (s *Service) ListSupplierReceipts(
	ctx context.Context, in ListSupplierReceiptsInput,
) ([]models.SupplierReceipt, int64, error) {
	if err := requireText("inventory_item_id", in.InventoryItemID); err != nil {
		return nil, 0, err
	}
	status := models.SupplierReceiptStatus(in.Status)
	if status != "" && !status.Valid() {
		return nil, 0, errors.Invalid(CodeInvalidInput,
			"unknown supplier receipt status %q; one of expected, received, canceled", in.Status)
	}
	paging, err := in.normalize()
	if err != nil {
		return nil, 0, err
	}
	if _, err := s.store.GetInventoryItem(ctx, in.InventoryItemID); err != nil {
		return nil, 0, err
	}

	return s.store.ListSupplierReceipts(ctx, models.SupplierReceiptFilter{
		InventoryItemID: in.InventoryItemID,
		Status:          status,
		Limit:           paging.Limit,
		Offset:          paging.Offset,
	})
}

// supplierReceiptOf reads a receipt of the item without a lock; another item's
// receipt is NotFound, as a missing one is.
func (s *Service) supplierReceiptOf(ctx context.Context, itemID, receiptID string) (models.SupplierReceipt, error) {
	receipt, err := s.store.GetSupplierReceipt(ctx, receiptID)
	if err != nil {
		return models.SupplierReceipt{}, err
	}
	if receipt.InventoryItemID != itemID {
		return models.SupplierReceipt{}, errors.NotFound(codeSupplierReceiptNotFound,
			"item %s has no supplier receipt %s", itemID, receiptID)
	}

	return receipt, nil
}

// codeSupplierReceiptNotFound is the repository's code for a missing receipt,
// repeated for one that belongs to another item.
const codeSupplierReceiptNotFound = "inventory_supplier_receipt_not_found"

// receivedAlready answers a receive of a receipt that is no longer expected:
// nothing when it was received with the same count, and a conflict otherwise.
func receivedAlready(receipt models.SupplierReceipt, quantity int64) error {
	if receipt.Status == models.SupplierReceiptReceived && receipt.ReceivedQuantity == quantity {
		return nil
	}

	if receipt.Status == models.SupplierReceiptCanceled {
		return errors.Conflict(CodeSupplierReceiptNotExpected,
			"supplier receipt %s was canceled; units that arrived anyway are a new receipt", receipt.ID)
	}

	return errors.Conflict(CodeSupplierReceiptNotExpected,
		"supplier receipt %s was received with %d units, not %d; a miscount is corrected by an "+
			"adjustment and units still owed are a new receipt", receipt.ID, receipt.ReceivedQuantity, quantity)
}

// canceledAlready answers a cancel of a receipt that is no longer expected:
// nothing when it was canceled, and a conflict when it was received.
func canceledAlready(receipt models.SupplierReceipt) error {
	if receipt.Status == models.SupplierReceiptCanceled {
		return nil
	}

	return errors.Conflict(CodeSupplierReceiptNotExpected,
		"supplier receipt %s was received; its units are in the ledger and are corrected "+
			"by an adjustment, not a cancel", receipt.ID)
}

// levelAt reads the item's level at the location without a lock; nil when
// there is none.
func (s *Service) levelAt(ctx context.Context, itemID, locationID string) (*models.InventoryLevel, error) {
	levels, err := s.store.ListInventoryLevels(ctx, itemID)
	if err != nil {
		return nil, err
	}
	for i := range levels {
		if levels[i].LocationID == locationID {
			return &levels[i], nil
		}
	}

	return nil, nil
}
