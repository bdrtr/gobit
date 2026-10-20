package service_test

import (
	"cmp"
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The fake store's half of an order's transfers (ADR 0450), kept as the
// queries in queries/order_transfers.sql keep them.

// InsertTransfer writes a transfer, closed at the store's clock unless it is
// a request.
func (f *fakeStore) InsertTransfer(ctx context.Context, t models.OrderTransfer) (models.OrderTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failTransferInsert != nil {
		return models.OrderTransfer{}, f.failTransferInsert
	}
	if _, ok := f.orders[t.OrderID]; !ok {
		return models.OrderTransfer{}, notFound(t.OrderID)
	}
	t.CreatedAt = f.nextStamp()
	if t.Status != models.TransferRequested {
		closed := f.nextStamp()
		t.ClosedAt = &closed
	}
	f.recordUndo(ctx, undoEntry(f.transfers, t.ID))
	f.transfers[t.ID] = t

	return t, nil
}

// GetTransfer returns the transfer.
func (f *fakeStore) GetTransfer(_ context.Context, id string) (models.OrderTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	t, ok := f.transfers[id]
	if !ok {
		return models.OrderTransfer{}, errors.NotFound("order_transfer_not_found", "no transfer %s", id)
	}

	return t, nil
}

// LockTransfer reads the transfer inside a transaction.
func (f *fakeStore) LockTransfer(ctx context.Context, id string) (models.OrderTransfer, error) {
	if err := requireTx(ctx, "LockTransfer"); err != nil {
		return models.OrderTransfer{}, err
	}

	return f.GetTransfer(ctx, id)
}

// RequestedTransfers returns the order's waiting transfers, newest first.
func (f *fakeStore) RequestedTransfers(_ context.Context, orderID string) ([]models.OrderTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []models.OrderTransfer
	for id := range f.transfers {
		if f.transfers[id].OrderID == orderID && f.transfers[id].Status == models.TransferRequested {
			out = append(out, f.transfers[id])
		}
	}
	sortTransfersNewestFirst(out)

	return out, nil
}

// CloseTransfer moves a waiting transfer to status.
func (f *fakeStore) CloseTransfer(
	ctx context.Context, id string, status models.TransferStatus,
) (models.OrderTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	t, ok := f.transfers[id]
	if !ok || t.Status != models.TransferRequested {
		return models.OrderTransfer{}, errors.Conflict("order_state_changed", "the transfer %s no longer waits", id)
	}
	closed := f.nextStamp()
	t.Status, t.ClosedAt = status, &closed
	f.recordUndo(ctx, undoEntry(f.transfers, id))
	f.transfers[id] = t

	return t, nil
}

// ListTransfers pages the order's transfers, newest first.
func (f *fakeStore) ListTransfers(
	_ context.Context, orderID string, limit, offset int32,
) ([]models.OrderTransfer, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var all []models.OrderTransfer
	for id := range f.transfers {
		if f.transfers[id].OrderID == orderID {
			all = append(all, f.transfers[id])
		}
	}
	sortTransfersNewestFirst(all)
	count := int64(len(all))
	start := min(int(offset), len(all))
	end := min(start+int(limit), len(all))

	return all[start:end], count, nil
}

// MoveOrderCustomer moves the order and its additions while each still
// belongs to fromCustomerID.
func (f *fakeStore) MoveOrderCustomer(
	ctx context.Context, orderID, fromCustomerID, toCustomerID, toEmail string,
) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var moved []string
	for id := range f.orders {
		order := f.orders[id]
		if id != orderID && order.AddsToOrderID != orderID {
			continue
		}
		if order.CustomerID != fromCustomerID {
			continue
		}
		if _, erased := f.erased[id]; erased {
			continue
		}
		order.CustomerID, order.Email = toCustomerID, toEmail
		order.UpdatedAt = f.nextStamp()
		f.recordUndo(ctx, undoEntry(f.orders, id))
		f.orders[id] = order
		moved = append(moved, id)
	}
	slices.Sort(moved)

	return moved, nil
}

// EraseTransferEmails empties the address a transfer asked.
func (f *fakeStore) EraseTransferEmails(
	ctx context.Context, orderIDs []string, customerID, email string,
) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var written int64
	for id := range f.transfers {
		t := f.transfers[id]
		if !slices.Contains(orderIDs, t.OrderID) &&
			(customerID == "" || t.FromCustomerID != customerID) &&
			(email == "" || t.FromEmail != email) {
			continue
		}
		t.FromEmail = ""
		f.recordUndo(ctx, undoEntry(f.transfers, id))
		f.transfers[id] = t
		written++
	}

	return written, nil
}

// CancelTransfersTo withdraws the requests still waiting for the customer and
// counts every transfer naming them as receiver.
func (f *fakeStore) CancelTransfersTo(ctx context.Context, customerID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var named int64
	for id := range f.transfers {
		t := f.transfers[id]
		if t.ToCustomerID != customerID {
			continue
		}
		named++
		if t.Status != models.TransferRequested {
			continue
		}
		closed := f.nextStamp()
		t.Status, t.ClosedAt = models.TransferCanceled, &closed
		f.recordUndo(ctx, undoEntry(f.transfers, id))
		f.transfers[id] = t
	}

	return named, nil
}

// TransfersForDisclosure returns the transfers of the orders and those naming
// the person.
func (f *fakeStore) TransfersForDisclosure(
	_ context.Context, orderIDs []string, customerID, email string,
) ([]models.OrderTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []models.OrderTransfer
	for id := range f.transfers {
		t := f.transfers[id]
		if slices.Contains(orderIDs, t.OrderID) ||
			(customerID != "" && (t.FromCustomerID == customerID || t.ToCustomerID == customerID)) ||
			(email != "" && t.FromEmail == email) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b models.OrderTransfer) int {
		return cmp.Or(cmp.Compare(a.OrderID, b.OrderID), a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})

	return out, nil
}

// sortTransfersNewestFirst orders transfers as the queries do.
func sortTransfersNewestFirst(ts []models.OrderTransfer) {
	slices.SortFunc(ts, func(a, b models.OrderTransfer) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
	})
}
