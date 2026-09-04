package manual_test

import (
	"context"
	"maps"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// memStore is the in-memory counterpart of manual.Store.
//
// It imitates THREE behaviors of the real store, because the provider's
// correctness rests on them:
//
//  1. The idempotency key is UNIQUE; it is the counterpart of the unique index
//     and Create's idempotency rests on it.
//  2. The locking method returns an error if it is called OUTSIDE a
//     transaction.
//  3. If the transaction ends with an error, whatever was written is ROLLED
//     BACK.
type memStore struct {
	mu        sync.Mutex
	shipments map[string]models.ManualShipment

	// writes counts how many times the ledger was written to; it is what proves
	// that the idempotent branch does not touch the ledger A SECOND TIME.
	writes int
}

// newMemStore produces an empty in-memory ledger.
func newMemStore() *memStore {
	return &memStore{shipments: map[string]models.ManualShipment{}}
}

// That memStore satisfies the surface the provider expects is verified at
// compile time.
var _ manual.Store = (*memStore)(nil)

// WithTx runs fn inside a "transaction"; if it returns an error the state is
// rolled back.
func (m *memStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	m.mu.Lock()
	snapshot := maps.Clone(m.shipments)
	m.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		m.mu.Lock()
		m.shipments = snapshot
		m.mu.Unlock()
		return err
	}
	return nil
}

// InsertManualShipmentIfAbsent writes the shipment only if the key is free.
func (m *memStore) InsertManualShipmentIfAbsent(
	_ context.Context,
	shipment models.ManualShipment,
) (models.ManualShipment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Only the KEYS are walked: walking with the value would copy the whole
	// shipment struct on every iteration.
	for id := range m.shipments {
		if m.shipments[id].IdempotencyKey == shipment.IdempotencyKey {
			return models.ManualShipment{}, false, nil
		}
	}
	m.shipments[shipment.ID] = shipment
	m.writes++
	return shipment, true, nil
}

// ManualShipmentByIdempotencyKey returns the shipment by its key.
func (m *memStore) ManualShipmentByIdempotencyKey(
	_ context.Context,
	key string,
) (models.ManualShipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.shipments {
		if m.shipments[id].IdempotencyKey == key {
			return m.shipments[id], nil
		}
	}
	return models.ManualShipment{}, errors.NotFound("mem_shipment_not_found",
		"no shipment under this key: %s", key)
}

// ManualShipment returns the shipment by its identifier.
func (m *memStore) ManualShipment(_ context.Context, id string) (models.ManualShipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	shipment, ok := m.shipments[id]
	if !ok {
		return models.ManualShipment{}, errors.NotFound("mem_shipment_not_found",
			"shipment not found: %s", id)
	}
	return shipment, nil
}

// LockManualShipment locks the shipment; if it is called outside a transaction
// it returns an error.
func (m *memStore) LockManualShipment(ctx context.Context, id string) (models.ManualShipment, error) {
	if ctx.Value(txMarkerKey{}) == nil {
		return models.ManualShipment{}, errors.Internal("mem_tx_required",
			"LockManualShipment was called outside a transaction")
	}
	return m.ManualShipment(ctx, id)
}

// UpdateManualShipmentState writes the status and the tracking details.
func (m *memStore) UpdateManualShipmentState(
	_ context.Context,
	id string,
	status models.FulfillmentStatus,
	trackingNumber, trackingURL string,
) (models.ManualShipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	shipment, ok := m.shipments[id]
	if !ok {
		return models.ManualShipment{}, errors.NotFound("mem_shipment_not_found",
			"shipment not found: %s", id)
	}
	shipment.Status = status
	shipment.TrackingNumber = trackingNumber
	shipment.TrackingURL = trackingURL
	m.shipments[id] = shipment
	m.writes++
	return shipment, nil
}

// writeCount returns the number of writes made to the ledger.
func (m *memStore) writeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}
