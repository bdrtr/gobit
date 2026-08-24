package manual_test

import (
	"context"
	"maps"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// txMarkerKey sahte deponun "işlem içindeyiz" işaretidir.
type txMarkerKey struct{}

// memStore manual.Store'un bellek içi karşılığıdır.
//
// Gerçek deponun ÜÇ davranışını taklit eder, çünkü sağlayıcının doğruluğu
// bunlara dayanır:
//
//  1. Idempotency anahtarı TEKTİR; benzersiz indeksin karşılığıdır ve
//     Create'in idempotentliği ona dayanır.
//  2. Kilit alan metot işlem DIŞINDA çağrılırsa hata döner.
//  3. İşlem hatayla biterse yazılanlar GERİ ALINIR.
type memStore struct {
	mu        sync.Mutex
	shipments map[string]models.ManualShipment

	// writes deftere kaç kez yazıldığını sayar; idempotent dalın deftere
	// İKİNCİ KEZ dokunmadığı bununla kanıtlanır.
	writes int
}

// newMemStore boş bir bellek içi defter üretir.
func newMemStore() *memStore {
	return &memStore{shipments: map[string]models.ManualShipment{}}
}

// memStore'un sağlayıcının beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ manual.Store = (*memStore)(nil)

// WithTx fn'i "işlem" içinde çalıştırır; hata dönerse durumu geri alır.
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

// InsertManualShipmentIfAbsent gönderiyi yalnızca anahtar boştaysa yazar.
func (m *memStore) InsertManualShipmentIfAbsent(
	_ context.Context,
	shipment models.ManualShipment,
) (models.ManualShipment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Yalnızca ANAHTARLAR gezilir: değerle gezmek her yinelemede gönderi
	// yapısının tamamını kopyalardı.
	for id := range m.shipments {
		if m.shipments[id].IdempotencyKey == shipment.IdempotencyKey {
			return models.ManualShipment{}, false, nil
		}
	}
	m.shipments[shipment.ID] = shipment
	m.writes++
	return shipment, true, nil
}

// ManualShipmentByIdempotencyKey gönderiyi anahtarıyla döner.
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
		"bu anahtarla gönderi yok: %s", key)
}

// ManualShipment gönderiyi kimliğiyle döner.
func (m *memStore) ManualShipment(_ context.Context, id string) (models.ManualShipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	shipment, ok := m.shipments[id]
	if !ok {
		return models.ManualShipment{}, errors.NotFound("mem_shipment_not_found",
			"gönderi bulunamadı: %s", id)
	}
	return shipment, nil
}

// LockManualShipment gönderiyi kilitler; işlem dışında çağrılırsa hata döner.
func (m *memStore) LockManualShipment(ctx context.Context, id string) (models.ManualShipment, error) {
	if ctx.Value(txMarkerKey{}) == nil {
		return models.ManualShipment{}, errors.Internal("mem_tx_required",
			"LockManualShipment işlem dışında çağrıldı")
	}
	return m.ManualShipment(ctx, id)
}

// UpdateManualShipmentState durumu ve takip bilgisini yazar.
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
			"gönderi bulunamadı: %s", id)
	}
	shipment.Status = status
	shipment.TrackingNumber = trackingNumber
	shipment.TrackingURL = trackingURL
	m.shipments[id] = shipment
	m.writes++
	return shipment, nil
}

// yazmaSayisi deftere yapılan yazma sayısını döner.
func (m *memStore) yazmaSayisi() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}
