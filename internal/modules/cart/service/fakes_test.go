package service_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// txMarkerKey sahte deponun "işlem içindeyiz" işaretidir.
type txMarkerKey struct{}

// readSnapshotKey salt-okunur işlemin anlık görüntüsünün context anahtarıdır.
type readSnapshotKey struct{}

// fakeSnapshot sahte deponun bir andaki tam hâlidir.
//
// Hem salt-okunur işlemin görüntüsü hem de yazan işlemin geri alma noktası bu
// tiple taşınır; ikisi de "deponun o andaki kopyası"dır.
type fakeSnapshot struct {
	carts     map[string]models.Cart
	items     map[string]models.LineItem
	addresses map[string]models.CartAddress
	methods   map[string]models.ShippingMethod
}

// fakeStore service.Store'un bellek içi karşılığıdır.
//
// # Neyi taklit eder, neyi ETMEZ
//
// Sahte yalnızca VERİTABANININ yaptığı şeyleri taklit eder:
//
//  1. Kilit alan metot işlem DIŞINDA çağrılırsa hata döner. Servis bir akışta
//     WithTx'i unutursa birim testi bunu yakalar; gerçek veritabanında bu hata
//     ancak yarış altında görünürdü.
//  2. İşlem hatayla biterse yazılanlar GERİ ALINIR. "Hata döndü ve hiçbir şey
//     yazılmadı" iddiası ancak böyle sınanabilir.
//  3. (cart_id, variant_id) benzersizliği: migration'daki kısmi benzersiz
//     indeksin karşılığı.
//  4. Salt-okunur işlemin ANLIK GÖRÜNTÜSÜ: [fakeStore.WithReadTx] içindeki
//     okumalar işlemin başındaki hâli görür, araya giren yazmaları görmez —
//     PostgreSQL'in REPEATABLE READ düzeyinin karşılığı.
//
// SERVİSİN sorumluluğundaki hiçbir kural burada TEKRARLANMAZ: sahte,
// tamamlanmış sepete yazmayı engellemez ve toplam kimliğini doğrulamaz. Aksi
// hâlde "servis tamamlanmış sepeti reddediyor" testi, servisten o kontrol
// silinse bile geçerdi — testin kanıtladığı şey sahtenin davranışı olurdu.
type fakeStore struct {
	mu        sync.Mutex
	carts     map[string]models.Cart
	items     map[string]models.LineItem
	addresses map[string]models.CartAddress
	methods   map[string]models.ShippingMethod

	// seq eklenen çocuk kayıtlara artan bir zaman damgası verir; listeleme
	// sırasının deterministik olması buna dayanır.
	seq int

	// lockedCarts kilitlenen sepetleri SIRASIYLA kaydeder. Kilit alınıp
	// alınmadığı bir eşzamanlılık sözleşmesidir ve gerçek veritabanında ihlali
	// ancak yarış altında görünür; burada doğrudan okunabilir.
	lockedCarts []string
	// bumpCalls şekil sayacının kaç kez artırıldığını sayar.
	bumpCalls int

	// failCreateLineItem ayarlanırsa CreateLineItem bu hatayı döner; işlem geri
	// alma yolunu sınamak için kullanılır.
	failCreateLineItem error
	// failSetLineItemTotals ayarlanırsa SetLineItemTotals bu hatayı döner.
	failSetLineItemTotals error

	// hookListLineItems ayarlanırsa ListLineItems'ın BAŞINDA BİR KEZ çağrılır
	// ve ardından temizlenir.
	//
	// Çok sorgulu bir okumanın ORTASINA yazma sokmak için vardır: gerçek
	// veritabanında araya giren yazma zamanlamaya bağlıdır ve testte
	// deterministik olarak üretilemez.
	hookListLineItems func()
}

// newFakeStore boş bir sahte depo üretir.
func newFakeStore() *fakeStore {
	return &fakeStore{
		carts:     map[string]models.Cart{},
		items:     map[string]models.LineItem{},
		addresses: map[string]models.CartAddress{},
		methods:   map[string]models.ShippingMethod{},
	}
}

// Sahte deponun servisin beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ service.Store = (*fakeStore)(nil)

// addressKey (sepet, tür) çiftinin harita anahtarıdır.
func addressKey(cartID string, kind models.AddressType) string {
	return cartID + "\x00" + kind.String()
}

// nextStamp sıradaki artan zaman damgasını üretir. Çağıran kilidi tutmalıdır.
func (f *fakeStore) nextStamp() time.Time {
	f.seq++
	return time.Unix(0, 0).UTC().Add(time.Duration(f.seq) * time.Millisecond)
}

// WithTx fn'i "işlem" içinde çalıştırır; hata dönerse durumu geri alır.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	snapshot := f.snapshot()
	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		f.mu.Lock()
		f.carts, f.items = snapshot.carts, snapshot.items
		f.addresses, f.methods = snapshot.addresses, snapshot.methods
		f.mu.Unlock()
		return err
	}
	return nil
}

// WithReadTx fn'i TEK ANLIK GÖRÜNTÜLÜ bir "işlem" içinde çalıştırır.
//
// Görüntü işlemin başında donar ve okumalar ona bakar; araya giren bir yazma
// bu işlemin içinde GÖRÜNMEZ. Gerçek karşılığı REPEATABLE READ'dir ve taklit
// edilmesi şarttır: yırtık okuma tam olarak bu düzeyin yokluğunda doğar.
func (f *fakeStore) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(readSnapshotKey{}) != nil || ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}
	return fn(context.WithValue(ctx, readSnapshotKey{}, f.snapshot()))
}

// snapshot deponun o andaki tam kopyasını üretir.
func (f *fakeStore) snapshot() fakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()

	return fakeSnapshot{
		carts:     cloneMap(f.carts),
		items:     cloneMap(f.items),
		addresses: cloneMap(f.addresses),
		methods:   cloneMap(f.methods),
	}
}

// readSnapshot salt-okunur işlemin görüntüsünü döner; işlem yoksa nil.
func readSnapshot(ctx context.Context) *fakeSnapshot {
	snap, ok := ctx.Value(readSnapshotKey{}).(fakeSnapshot)
	if !ok {
		return nil
	}
	return &snap
}

// storeView bir okumanın bakacağı haritalardır.
type storeView struct {
	fakeSnapshot
	// release okuma bitince çağrılır; canlı haritalarda kilidi bırakır.
	release func()
}

// view okumanın bakacağı haritaları döner.
//
// Salt-okunur bir işlem varsa onun DONMUŞ görüntüsü verilir ve kilide gerek
// kalmaz — kopya başka kimseyle paylaşılmaz. Yoksa canlı haritalar kilit
// ALTINDA verilir; kilit [storeView.release] çağrılana kadar tutulur.
func (f *fakeStore) view(ctx context.Context) storeView {
	if snap := readSnapshot(ctx); snap != nil {
		return storeView{fakeSnapshot: *snap, release: func() {}}
	}

	f.mu.Lock()
	return storeView{
		fakeSnapshot: fakeSnapshot{
			carts:     f.carts,
			items:     f.items,
			addresses: f.addresses,
			methods:   f.methods,
		},
		release: f.mu.Unlock,
	}
}

// fireListLineItems ListLineItems kancasını BİR KEZ çalıştırır.
func (f *fakeStore) fireListLineItems() {
	f.mu.Lock()
	hook := f.hookListLineItems
	f.hookListLineItems = nil
	f.mu.Unlock()

	if hook != nil {
		hook()
	}
}

// cloneMap haritanın yüzeysel kopyasını üretir.
func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_tx_required", "%s işlem dışında çağrıldı", op)
	}
	return nil
}

// CreateCart sepeti kaydeder.
func (f *fakeStore) CreateCart(_ context.Context, cart models.Cart) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stamp := f.nextStamp()
	cart.CreatedAt, cart.UpdatedAt = stamp, stamp
	f.carts[cart.ID] = cart
	return cart, nil
}

// GetCart sepeti döner.
func (f *fakeStore) GetCart(ctx context.Context, id string) (models.Cart, error) {
	view := f.view(ctx)
	defer view.release()

	cart, ok := view.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "sepet bulunamadı: %s", id)
	}
	return cart, nil
}

// LockCart sepeti kilitler; işlem dışında çağrılırsa hata döner.
func (f *fakeStore) LockCart(ctx context.Context, id string) (models.Cart, error) {
	if err := requireTx(ctx, "LockCart"); err != nil {
		return models.Cart{}, err
	}
	cart, err := f.GetCart(ctx, id)
	if err != nil {
		return models.Cart{}, err
	}

	f.mu.Lock()
	f.lockedCarts = append(f.lockedCarts, id)
	f.mu.Unlock()
	return cart, nil
}

// ListCarts sepetleri süzer ve sayfalar.
func (f *fakeStore) ListCarts(ctx context.Context, filter models.CartFilter) ([]models.Cart, int64, error) {
	view := f.view(ctx)
	defer view.release()

	matched := make([]models.Cart, 0, len(view.carts))
	// Döngüler indeksle/anahtarla gezilir: model yapıları büyüktür ve değerle
	// kopyalamak her tur birkaç yüz baytı boşuna taşır.
	for id := range view.carts {
		cart := view.carts[id]
		if cart.DeletedAt != nil {
			continue
		}
		if filter.CustomerID != nil && cart.CustomerID != *filter.CustomerID {
			continue
		}
		if filter.RegionID != nil && cart.RegionID != *filter.RegionID {
			continue
		}
		if filter.Completed != nil && cart.Completed() != *filter.Completed {
			continue
		}
		matched = append(matched, cart)
	}
	slices.SortFunc(matched, func(a, b models.Cart) int {
		return cmpString(a.ID, b.ID)
	})

	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Cart{}, total, nil
	}
	end := min(filter.Offset+filter.Limit, total)
	return matched[filter.Offset:end], total, nil
}

// cmpString iki dizeyi karşılaştırır.
func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// CartsByIDs kimlik kümesinin sepetlerini döner.
func (f *fakeStore) CartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error) {
	view := f.view(ctx)
	defer view.release()

	out := make([]models.Cart, 0, len(ids))
	for _, id := range ids {
		if cart, ok := view.carts[id]; ok && cart.DeletedAt == nil {
			out = append(out, cart)
		}
	}
	slices.SortFunc(out, func(a, b models.Cart) int { return cmpString(a.ID, b.ID) })
	return out, nil
}

// UpdateCartContact sepetin e-posta ve müşteri alanlarını yazar.
//
// Kimin kime devredilebileceği BURADA denetlenmez; o kural servisindir.
func (f *fakeStore) UpdateCartContact(_ context.Context, id string, contact models.CartContact) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "sepet bulunamadı: %s", id)
	}
	cart.Email = contact.Email
	cart.CustomerID = contact.CustomerID
	cart.UpdatedAt = f.nextStamp()
	f.carts[id] = cart
	return cart, nil
}

// UpdateCartTotals toplamları yazar.
//
// Toplam kimliği BURADA doğrulanmaz; o kural servisindir (bkz. tip belgesi).
func (f *fakeStore) UpdateCartTotals(_ context.Context, id string, totals models.CartTotals) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "sepet bulunamadı: %s", id)
	}
	cart.Subtotal = totals.Subtotal
	cart.DiscountTotal = totals.DiscountTotal
	cart.TaxTotal = totals.TaxTotal
	cart.ShippingTotal = totals.ShippingTotal
	cart.Total = totals.Total
	cart.TotalsRevision = totals.Revision
	cart.UpdatedAt = f.nextStamp()
	f.carts[id] = cart
	return cart, nil
}

// BumpCartRevision şekil sayacını artırır.
func (f *fakeStore) BumpCartRevision(_ context.Context, id string) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "sepet bulunamadı: %s", id)
	}
	cart.Revision++
	cart.UpdatedAt = f.nextStamp()
	f.carts[id] = cart
	f.bumpCalls++
	return cart, nil
}

// MarkCartCompleted sepeti tamamlanmış damgalar.
//
// İkinci damgayı BURADA engellemez; o kural servisindir.
func (f *fakeStore) MarkCartCompleted(_ context.Context, id string) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "sepet bulunamadı: %s", id)
	}
	now := f.nextStamp()
	cart.CompletedAt = &now
	cart.UpdatedAt = now
	f.carts[id] = cart
	return cart, nil
}

// SoftDeleteCart sepeti yumuşak siler.
func (f *fakeStore) SoftDeleteCart(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return errors.NotFound("cart_not_found", "sepet bulunamadı: %s", id)
	}
	now := f.nextStamp()
	cart.DeletedAt = &now
	f.carts[id] = cart
	return nil
}

// CreateLineItem satırı kaydeder.
func (f *fakeStore) CreateLineItem(_ context.Context, item models.LineItem) (models.LineItem, error) {
	if f.failCreateLineItem != nil {
		return models.LineItem{}, f.failCreateLineItem
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// (cart_id, variant_id) benzersizliği: migration'daki kısmi indeksin
	// karşılığı.
	for id := range f.items {
		if f.items[id].CartID == item.CartID && f.items[id].VariantID == item.VariantID {
			return models.LineItem{}, errors.Conflict("cart_line_item_exists",
				"bu varyant sepette zaten var")
		}
	}

	stamp := f.nextStamp()
	item.CreatedAt, item.UpdatedAt = stamp, stamp
	f.items[item.ID] = item
	return item, nil
}

// GetLineItem satırı döner.
func (f *fakeStore) GetLineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error) {
	view := f.view(ctx)
	defer view.release()

	item, ok := view.items[lineID]
	if !ok || item.CartID != cartID {
		return models.LineItem{}, lineNotFound(cartID, lineID)
	}
	return item, nil
}

// GetLineItemByVariant sepetteki varyantın satırını döner.
func (f *fakeStore) GetLineItemByVariant(ctx context.Context, cartID, variantID string) (models.LineItem, error) {
	view := f.view(ctx)
	defer view.release()

	for id := range view.items {
		if view.items[id].CartID == cartID && view.items[id].VariantID == variantID {
			return view.items[id], nil
		}
	}
	return models.LineItem{}, errors.NotFound("cart_line_item_not_found",
		"sepette bu varyanttan satır yok (%s / %s)", cartID, variantID)
}

// ListLineItems sepetin satırlarını oluşturulma sırasıyla döner.
func (f *fakeStore) ListLineItems(ctx context.Context, cartID string) ([]models.LineItem, error) {
	f.fireListLineItems()

	view := f.view(ctx)
	defer view.release()

	out := make([]models.LineItem, 0, len(view.items))
	for id := range view.items {
		if view.items[id].CartID == cartID {
			out = append(out, view.items[id])
		}
	}
	slices.SortFunc(out, func(a, b models.LineItem) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return cmpString(a.ID, b.ID)
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// SetLineItemQuantity satırın adedini yazar.
func (f *fakeStore) SetLineItemQuantity(_ context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.items[lineID]
	if !ok || item.CartID != cartID {
		return models.LineItem{}, lineNotFound(cartID, lineID)
	}
	item.Quantity = quantity
	item.UpdatedAt = f.nextStamp()
	f.items[lineID] = item
	return item, nil
}

// SetLineItemTotals satırın para alanlarını yazar.
func (f *fakeStore) SetLineItemTotals(_ context.Context, cartID, lineID string, totals models.LineTotals) (models.LineItem, error) {
	if f.failSetLineItemTotals != nil {
		return models.LineItem{}, f.failSetLineItemTotals
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.items[lineID]
	if !ok || item.CartID != cartID {
		return models.LineItem{}, lineNotFound(cartID, lineID)
	}
	item.UnitPrice = totals.UnitPrice
	item.Subtotal = totals.Subtotal
	item.DiscountTotal = totals.DiscountTotal
	item.TaxTotal = totals.TaxTotal
	item.Total = totals.Total
	item.UpdatedAt = f.nextStamp()
	f.items[lineID] = item
	return item, nil
}

// SoftDeleteLineItem satırı siler.
func (f *fakeStore) SoftDeleteLineItem(_ context.Context, cartID, lineID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.items[lineID]
	if !ok || item.CartID != cartID {
		return lineNotFound(cartID, lineID)
	}
	delete(f.items, lineID)
	return nil
}

// SoftDeleteLineItemsByCart sepetin tüm satırlarını siler.
func (f *fakeStore) SoftDeleteLineItemsByCart(_ context.Context, cartID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.items {
		if f.items[id].CartID == cartID {
			delete(f.items, id)
		}
	}
	return nil
}

// lineNotFound eksik satır hatasını üretir.
func lineNotFound(cartID, lineID string) error {
	return errors.NotFound("cart_line_item_not_found",
		"sepet satırı bulunamadı (%s / %s)", cartID, lineID)
}

// UpsertCartAddress adresi yazar; var olanın kimliğini KORUR.
func (f *fakeStore) UpsertCartAddress(_ context.Context, addr models.CartAddress) (models.CartAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := addressKey(addr.CartID, addr.Type)
	stamp := f.nextStamp()
	if existing, ok := f.addresses[key]; ok {
		addr.ID = existing.ID
		addr.CreatedAt = existing.CreatedAt
	} else {
		addr.CreatedAt = stamp
	}
	addr.UpdatedAt = stamp
	f.addresses[key] = addr
	return addr, nil
}

// ListCartAddresses sepetin adreslerini döner.
func (f *fakeStore) ListCartAddresses(ctx context.Context, cartID string) ([]models.CartAddress, error) {
	view := f.view(ctx)
	defer view.release()

	out := make([]models.CartAddress, 0, 2)
	for _, kind := range []models.AddressType{models.AddressBilling, models.AddressShipping} {
		if addr, ok := view.addresses[addressKey(cartID, kind)]; ok {
			out = append(out, addr)
		}
	}
	return out, nil
}

// SoftDeleteCartAddressesByCart sepetin adreslerini siler.
func (f *fakeStore) SoftDeleteCartAddressesByCart(_ context.Context, cartID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, kind := range []models.AddressType{models.AddressBilling, models.AddressShipping} {
		delete(f.addresses, addressKey(cartID, kind))
	}
	return nil
}

// CreateShippingMethod kargo yöntemi ekler.
func (f *fakeStore) CreateShippingMethod(_ context.Context, method models.ShippingMethod) (models.ShippingMethod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if method.ShippingOptionID != "" {
		for id := range f.methods {
			if f.methods[id].CartID == method.CartID && f.methods[id].ShippingOptionID == method.ShippingOptionID {
				return models.ShippingMethod{}, errors.Conflict("cart_shipping_option_already_added",
					"bu kargo seçeneği sepete zaten eklenmiş")
			}
		}
	}

	stamp := f.nextStamp()
	method.CreatedAt, method.UpdatedAt = stamp, stamp
	f.methods[method.ID] = method
	return method, nil
}

// ListShippingMethods sepetin kargo yöntemlerini döner.
func (f *fakeStore) ListShippingMethods(ctx context.Context, cartID string) ([]models.ShippingMethod, error) {
	view := f.view(ctx)
	defer view.release()

	out := make([]models.ShippingMethod, 0, len(view.methods))
	for id := range view.methods {
		if view.methods[id].CartID == cartID {
			out = append(out, view.methods[id])
		}
	}
	slices.SortFunc(out, func(a, b models.ShippingMethod) int { return cmpString(a.ID, b.ID) })
	return out, nil
}

// SoftDeleteShippingMethod kargo yöntemini siler.
func (f *fakeStore) SoftDeleteShippingMethod(_ context.Context, cartID, methodID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	method, ok := f.methods[methodID]
	if !ok || method.CartID != cartID {
		return errors.NotFound("cart_shipping_method_not_found",
			"kargo yöntemi bulunamadı (%s / %s)", cartID, methodID)
	}
	delete(f.methods, methodID)
	return nil
}

// SoftDeleteShippingMethodsByCart sepetin tüm kargo yöntemlerini siler.
func (f *fakeStore) SoftDeleteShippingMethodsByCart(_ context.Context, cartID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.methods {
		if f.methods[id].CartID == cartID {
			delete(f.methods, id)
		}
	}
	return nil
}
