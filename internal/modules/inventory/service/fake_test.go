package service_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// txMarkerKey sahte deponun "işlem içindeyiz" işaretidir.
type txMarkerKey struct{}

// fakeStore service.Store'un bellek içi karşılığıdır.
//
// İki davranışı gerçek depodan BİLİNÇLİ olarak taklit eder, çünkü servisin
// doğruluğu bunlara dayanır:
//
//  1. Kilit alan metotlar işlem DIŞINDA çağrılırsa hata döner. Servis bir
//     akışta WithTx'i unutursa birim testi bunu yakalar; gerçek veritabanında
//     bu hata, kilitsiz okuma yüzünden ancak yarış altında görünürdü.
//  2. İşlem hatayla biterse yazılanlar GERİ ALINIR. "Hata döndü ve hiçbir şey
//     yazılmadı" iddiası ancak böyle sınanabilir.
type fakeStore struct {
	mu           sync.Mutex
	items        map[string]models.InventoryItem
	locations    map[string]models.StockLocation
	levels       map[string]models.InventoryLevel
	reservations map[string]models.Reservation

	// updateLevelCalls stok seviyesine kaç kez yazıldığını sayar; idempotent
	// akışların stoğa İKİNCİ KEZ dokunmadığı bununla kanıtlanır.
	updateLevelCalls int
	// availableCalls toplu satılabilirlik sorgusunun çağrı sayısıdır.
	availableCalls int

	// failCreateReservation ayarlanırsa CreateReservation bu hatayı döner;
	// işlem geri alma yolunu sınamak için kullanılır.
	failCreateReservation error
}

// newFakeStore boş bir sahte depo üretir.
func newFakeStore() *fakeStore {
	return &fakeStore{
		items:        map[string]models.InventoryItem{},
		locations:    map[string]models.StockLocation{},
		levels:       map[string]models.InventoryLevel{},
		reservations: map[string]models.Reservation{},
	}
}

// Sahte deponun servisin beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ service.Store = (*fakeStore)(nil)

// levelKey (kalem, lokasyon) çiftinin harita anahtarıdır.
func levelKey(itemID, locationID string) string {
	return itemID + "\x00" + locationID
}

// WithTx fn'i "işlem" içinde çalıştırır; hata dönerse durumu geri alır.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	f.mu.Lock()
	snapshot := struct {
		items        map[string]models.InventoryItem
		locations    map[string]models.StockLocation
		levels       map[string]models.InventoryLevel
		reservations map[string]models.Reservation
	}{
		items:        maps.Clone(f.items),
		locations:    maps.Clone(f.locations),
		levels:       maps.Clone(f.levels),
		reservations: maps.Clone(f.reservations),
	}
	f.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		f.mu.Lock()
		f.items, f.locations = snapshot.items, snapshot.locations
		f.levels, f.reservations = snapshot.levels, snapshot.reservations
		f.mu.Unlock()
		return err
	}
	return nil
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_tx_required", "%s işlem dışında çağrıldı", op)
	}
	return nil
}

// CreateStockLocation lokasyonu kaydeder.
func (f *fakeStore) CreateStockLocation(_ context.Context, loc models.StockLocation) (models.StockLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	loc.CreatedAt, loc.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.locations[loc.ID] = loc
	return loc, nil
}

// GetStockLocation lokasyonu döner.
func (f *fakeStore) GetStockLocation(_ context.Context, id string) (models.StockLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	loc, ok := f.locations[id]
	if !ok {
		return models.StockLocation{}, errors.NotFound("inventory_location_not_found", "lokasyon yok: %s", id)
	}
	return loc, nil
}

// ListStockLocations lokasyonları sayfalar.
func (f *fakeStore) ListStockLocations(_ context.Context, limit, offset int64) ([]models.StockLocation, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := sortedValues(f.locations, func(loc models.StockLocation) string { return loc.ID })
	return paginate(all, limit, offset), int64(len(all)), nil
}

// CreateInventoryItem kalemi kaydeder.
func (f *fakeStore) CreateInventoryItem(_ context.Context, item models.InventoryItem) (models.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, existing := range f.items {
		if existing.SKU == item.SKU {
			return models.InventoryItem{}, errors.Conflict("inventory_sku_exists", "sku kullanımda: %s", item.SKU)
		}
	}
	item.CreatedAt, item.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.items[item.ID] = item
	return item, nil
}

// GetInventoryItem kalemi döner.
func (f *fakeStore) GetInventoryItem(_ context.Context, id string) (models.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.items[id]
	if !ok {
		return models.InventoryItem{}, errors.NotFound("inventory_item_not_found", "kalem yok: %s", id)
	}
	return item, nil
}

// LockInventoryItem kalemi "kilitler".
func (f *fakeStore) LockInventoryItem(ctx context.Context, id string) error {
	if err := requireTx(ctx, "LockInventoryItem"); err != nil {
		return err
	}
	_, err := f.GetInventoryItem(ctx, id)
	return err
}

// ListInventoryItems kalemleri filtreleyip sayfalar.
func (f *fakeStore) ListInventoryItems(_ context.Context, filter models.InventoryItemFilter) ([]models.InventoryItem, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := sortedValues(f.items, func(item models.InventoryItem) string { return item.ID })
	matched := make([]models.InventoryItem, 0, len(all))
	for _, item := range all {
		if filter.SKU != nil && item.SKU != *filter.SKU {
			continue
		}
		if filter.RequiresShipping != nil && item.RequiresShipping != *filter.RequiresShipping {
			continue
		}
		matched = append(matched, item)
	}
	return paginate(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
}

// InventoryItemsByIDs kimlik kümesini döner.
func (f *fakeStore) InventoryItemsByIDs(_ context.Context, ids []string) ([]models.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.InventoryItem, 0, len(ids))
	for _, id := range ids {
		if item, ok := f.items[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

// SoftDeleteInventoryItem kalemi siler.
func (f *fakeStore) SoftDeleteInventoryItem(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.items[id]; !ok {
		return errors.NotFound("inventory_item_not_found", "kalem yok: %s", id)
	}
	delete(f.items, id)
	return nil
}

// SoftDeleteInventoryLevelsByItem kalemin seviyelerini siler.
func (f *fakeStore) SoftDeleteInventoryLevelsByItem(_ context.Context, itemID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for key, level := range f.levels {
		if level.InventoryItemID == itemID {
			delete(f.levels, key)
		}
	}
	return nil
}

// LockInventoryLevel seviyeyi "kilitler" ve döner.
func (f *fakeStore) LockInventoryLevel(ctx context.Context, itemID, locationID string) (models.InventoryLevel, error) {
	if err := requireTx(ctx, "LockInventoryLevel"); err != nil {
		return models.InventoryLevel{}, err
	}
	return f.GetInventoryLevel(ctx, itemID, locationID)
}

// GetInventoryLevel seviyeyi döner.
func (f *fakeStore) GetInventoryLevel(_ context.Context, itemID, locationID string) (models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	level, ok := f.levels[levelKey(itemID, locationID)]
	if !ok {
		return models.InventoryLevel{}, errors.NotFound("inventory_level_not_found",
			"seviye yok (%s, %s)", itemID, locationID)
	}
	return level, nil
}

// CreateInventoryLevel seviyeyi kaydeder.
func (f *fakeStore) CreateInventoryLevel(_ context.Context, level models.InventoryLevel) (models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := levelKey(level.InventoryItemID, level.LocationID)
	if _, ok := f.levels[key]; ok {
		return models.InventoryLevel{}, errors.Conflict("inventory_level_exists", "seviye zaten var")
	}
	level.CreatedAt, level.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.levels[key] = level
	return level, nil
}

// UpdateInventoryLevelQuantities adetleri yazar.
func (f *fakeStore) UpdateInventoryLevelQuantities(_ context.Context, levelID string, stocked, reserved int64) (models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.updateLevelCalls++
	for key, level := range f.levels {
		if level.ID != levelID {
			continue
		}
		level.StockedQuantity, level.ReservedQuantity = stocked, reserved
		level.UpdatedAt = time.Now().UTC()
		f.levels[key] = level
		return level, nil
	}
	return models.InventoryLevel{}, errors.NotFound("inventory_level_not_found", "seviye yok: %s", levelID)
}

// ListInventoryLevels kalemin seviyelerini döner.
func (f *fakeStore) ListInventoryLevels(_ context.Context, itemID string) ([]models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := sortedValues(f.levels, func(level models.InventoryLevel) string { return level.ID })
	out := make([]models.InventoryLevel, 0, len(all))
	for _, level := range all {
		if level.InventoryItemID == itemID {
			out = append(out, level)
		}
	}
	return out, nil
}

// AvailableByItemIDs kalem başına satılabilir toplamı döner.
func (f *fakeStore) AvailableByItemIDs(_ context.Context, ids []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.availableCalls++
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}

	out := map[string]int64{}
	for _, level := range f.levels {
		if _, ok := wanted[level.InventoryItemID]; ok {
			out[level.InventoryItemID] += level.Available()
		}
	}
	return out, nil
}

// CreateReservation rezervasyonu kaydeder.
func (f *fakeStore) CreateReservation(_ context.Context, res models.Reservation) (models.Reservation, error) {
	if f.failCreateReservation != nil {
		return models.Reservation{}, f.failCreateReservation
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	res.CreatedAt, res.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.reservations[res.ID] = res
	return res, nil
}

// LockReservation rezervasyonu "kilitler" ve döner.
func (f *fakeStore) LockReservation(ctx context.Context, id string) (models.Reservation, error) {
	if err := requireTx(ctx, "LockReservation"); err != nil {
		return models.Reservation{}, err
	}
	return f.GetReservation(ctx, id)
}

// GetReservation rezervasyonu döner.
func (f *fakeStore) GetReservation(_ context.Context, id string) (models.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	res, ok := f.reservations[id]
	if !ok {
		return models.Reservation{}, errors.NotFound("inventory_reservation_not_found",
			"rezervasyon yok: %s", id)
	}
	return res, nil
}

// SetReservationStatus durumu yazar.
func (f *fakeStore) SetReservationStatus(_ context.Context, id string, status models.ReservationStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	res, ok := f.reservations[id]
	if !ok {
		return errors.NotFound("inventory_reservation_not_found", "rezervasyon yok: %s", id)
	}
	res.Status = status
	res.UpdatedAt = time.Now().UTC()
	f.reservations[id] = res
	return nil
}

// CountActiveReservations aktif rezervasyonları sayar.
func (f *fakeStore) CountActiveReservations(_ context.Context, itemID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	for id := range f.reservations {
		res := f.reservations[id]
		if res.InventoryItemID == itemID && res.Status == models.ReservationActive {
			count++
		}
	}
	return count, nil
}

// --- test kurulum yardımcıları ----------------------------------------------

// seedItem sahte depoya bir kalem koyar.
func (f *fakeStore) seedItem(id, sku string) models.InventoryItem {
	f.mu.Lock()
	defer f.mu.Unlock()

	item := models.InventoryItem{
		ID: id, SKU: sku, RequiresShipping: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	f.items[id] = item
	return item
}

// seedLevel sahte depoya bir stok seviyesi koyar.
func (f *fakeStore) seedLevel(itemID, locationID string, stocked, reserved int64) models.InventoryLevel {
	f.mu.Lock()
	defer f.mu.Unlock()

	level := models.InventoryLevel{
		ID:               "invlevel_" + itemID + "_" + locationID,
		InventoryItemID:  itemID,
		LocationID:       locationID,
		StockedQuantity:  stocked,
		ReservedQuantity: reserved,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	f.levels[levelKey(itemID, locationID)] = level
	return level
}

// seedReservation sahte depoya bir rezervasyon koyar.
func (f *fakeStore) seedReservation(id, itemID, locationID string, qty int64, status models.ReservationStatus) models.Reservation {
	f.mu.Lock()
	defer f.mu.Unlock()

	res := models.Reservation{
		ID: id, InventoryItemID: itemID, LocationID: locationID,
		Quantity: qty, Status: status,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	f.reservations[id] = res
	return res
}

// level testte doğrulama için seviyeyi döner.
func (f *fakeStore) level(itemID, locationID string) models.InventoryLevel {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.levels[levelKey(itemID, locationID)]
}

// reservation testte doğrulama için rezervasyonu döner.
func (f *fakeStore) reservation(id string) models.Reservation {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.reservations[id]
}

// sortedValues haritanın değerlerini anahtar işlevine göre sıralı döner.
// Sıra sabit olmadan liste testleri rastgele başarısız olurdu.
func sortedValues[T any](m map[string]T, key func(T) string) []T {
	out := make([]T, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	slices.SortFunc(out, func(a, b T) int { return strings.Compare(key(a), key(b)) })
	return out
}

// paginate dilime limit/offset uygular.
func paginate[T any](all []T, limit, offset int64) []T {
	if offset >= int64(len(all)) {
		return []T{}
	}
	rest := all[offset:]
	if limit > 0 && int64(len(rest)) > limit {
		rest = rest[:limit]
	}
	return rest
}
