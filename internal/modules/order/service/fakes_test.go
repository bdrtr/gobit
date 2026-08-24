package service_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
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
	orders    map[string]models.Order
	items     map[string]models.OrderLineItem
	summaries map[string]models.OrderSummary
	returns   map[string]models.Return
	exchanges map[string]models.Exchange
	claims    map[string]models.Claim
}

// fakeStore service.Store'un bellek içi karşılığıdır.
//
// # Neyi taklit eder, neyi ETMEZ
//
// Sahte yalnızca VERİTABANININ yaptığı şeyleri taklit eder:
//
//  1. display_id'yi DEPO üretir (IDENTITY sütununun karşılığı). Servis
//     numarayı kendi üretmeye kalksaydı testler bunu görürdü.
//  2. Kilit alan metot işlem DIŞINDA çağrılırsa hata döner.
//  3. İşlem hatayla biterse yazılanlar GERİ ALINIR.
//  4. idempotency_key benzersizliği: migration'daki kısmi benzersiz indeksin
//     karşılığı; yumuşak silinmiş kayıtlar kısıtın dışındadır.
//  5. Durum geçişi sorgularının WHERE koşulu: beklenen durumda olmayan bir
//     siparişte hiçbir satır etkilenmez ve Conflict döner.
//  6. Özet tutarlarının GREATEST ile birleştirilmesi: sorgu kayıtlı değerle
//     bildirilen değerin BÜYÜĞÜNÜ saklar.
//  7. Foreign key: olmayan bir siparişe çocuk kayıt bağlanamaz.
//  8. Salt-okunur işlemin ANLIK GÖRÜNTÜSÜ (REPEATABLE READ karşılığı).
//
// SERVİSİN sorumluluğundaki hiçbir kural burada TEKRARLANMAZ: sahte toplam
// kimliğini doğrulamaz, iptal edilmiş siparişe iade kaydı açmayı engellemez ve
// "zaten iptal edilmiş" durumunu sessizce başarı saymaz. Aksi hâlde ilgili
// testler, servisten o kontrol silinse bile geçerdi — testin kanıtladığı şey
// sahtenin davranışı olurdu.
type fakeStore struct {
	mu        sync.Mutex
	orders    map[string]models.Order
	items     map[string]models.OrderLineItem
	summaries map[string]models.OrderSummary
	returns   map[string]models.Return
	exchanges map[string]models.Exchange
	claims    map[string]models.Claim

	// seq eklenen kayıtlara artan bir zaman damgası verir; listeleme sırasının
	// deterministik olması buna dayanır.
	seq int
	// displaySeq sipariş numarasının sequence'ıdır ve 1'den başlar.
	displaySeq int64
	// forceDisplayID ayarlanırsa üretilen numara bu olur. Bozuk bir sequence'ı
	// (ya da doğrudan SQL müdahalesini) taklit eder.
	forceDisplayID *int64

	// lockedOrders kilitlenen siparişleri SIRASIYLA kaydeder. Kilit alınıp
	// alınmadığı bir eşzamanlılık sözleşmesidir ve gerçek veritabanında ihlali
	// ancak yarış altında görünür; burada doğrudan okunabilir.
	lockedOrders []string

	// failCreateLineItem ayarlanırsa CreateLineItem bu hatayı döner; işlem geri
	// alma yolunu sınamak için kullanılır.
	failCreateLineItem error
	// failCreateSummary ayarlanırsa CreateSummary bu hatayı döner.
	failCreateSummary error

	// hookCreateOrder ayarlanırsa CreateOrder'ın BAŞINDA BİR KEZ çağrılır ve
	// ardından temizlenir.
	//
	// İdempotent çağrıların YARIŞINI kurmak için vardır: gerçek veritabanında
	// "iki çağrı da anahtarı bulamadı, ikisi de yazmaya kalktı" durumu
	// zamanlamaya bağlıdır ve testte deterministik üretilemez.
	hookCreateOrder func()
}

// newFakeStore boş bir sahte depo üretir.
func newFakeStore() *fakeStore {
	return &fakeStore{
		orders:    map[string]models.Order{},
		items:     map[string]models.OrderLineItem{},
		summaries: map[string]models.OrderSummary{},
		returns:   map[string]models.Return{},
		exchanges: map[string]models.Exchange{},
		claims:    map[string]models.Claim{},
	}
}

// Sahte deponun servisin beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ service.Store = (*fakeStore)(nil)

// nextStamp sıradaki artan zaman damgasını üretir. Çağıran kilidi tutmalıdır.
func (f *fakeStore) nextStamp() time.Time {
	f.seq++
	return time.Unix(0, 0).UTC().Add(time.Duration(f.seq) * time.Millisecond)
}

// snapshot deponun o andaki kopyasını alır.
func (f *fakeStore) snapshot() fakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeSnapshot{
		orders:    maps.Clone(f.orders),
		items:     maps.Clone(f.items),
		summaries: maps.Clone(f.summaries),
		returns:   maps.Clone(f.returns),
		exchanges: maps.Clone(f.exchanges),
		claims:    maps.Clone(f.claims),
	}
}

// WithTx fn'i "işlem" içinde çalıştırır; hata dönerse İŞLEMİN KENDİ yazdıkları
// geri alınır.
//
// Geri alma, deponun tamamını bir kopyaya döndürerek DEĞİL, işlem sırasında
// yapılan her yazma için tutulan geri alma kaydını ters sırada çalıştırarak
// yapılır. Fark önemlidir: toptan kopya, işlem sürerken BAŞKA bir işlemin
// yazdıklarını da silerdi ve eşzamanlı senaryolar (örn. idempotent çağrı
// yarışı) gerçek veritabanında olmayan bir biçimde bozulurdu.
//
// [fakeStore.displaySeq] BİLİNÇLİ OLARAK geri alınmaz: PostgreSQL'de de
// sequence işlemle birlikte geri sarılmaz, geri alınan bir INSERT numarayı
// tüketir.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txMarkerKey{}).(*txState); ok {
		return fn(ctx)
	}

	state := &txState{}
	if err := fn(context.WithValue(ctx, txMarkerKey{}, state)); err != nil {
		f.mu.Lock()
		for i := len(state.undos) - 1; i >= 0; i-- {
			state.undos[i]()
		}
		f.mu.Unlock()
		return err
	}
	return nil
}

// txState bir işlemin geri alma kaydıdır.
type txState struct {
	// undos yapılan yazmaların geri alıcılarıdır; ters sırada çalıştırılır.
	undos []func()
}

// recordUndo bir yazmanın geri alıcısını işleme kaydeder.
//
// İşlem dışındaki yazmalar geri alınamaz; gerçek veritabanında da işlemsiz bir
// INSERT anında kalıcıdır.
func (f *fakeStore) recordUndo(ctx context.Context, undo func()) {
	if state, ok := ctx.Value(txMarkerKey{}).(*txState); ok {
		state.undos = append(state.undos, undo)
	}
}

// undoEntry bir harita girdisinin YAZMADAN ÖNCEKİ hâlini geri yükleyen
// kapanışı üretir.
func undoEntry[V any](m map[string]V, key string) func() {
	prev, existed := m[key]
	return func() {
		if existed {
			m[key] = prev
			return
		}
		delete(m, key)
	}
}

// WithReadTx fn'i tek anlık görüntülü bir okuma içinde çalıştırır.
func (f *fakeStore) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, inTx := ctx.Value(txMarkerKey{}).(*txState); inTx || ctx.Value(readSnapshotKey{}) != nil {
		return fn(ctx)
	}
	snapshot := f.snapshot()
	return fn(context.WithValue(ctx, readSnapshotKey{}, &snapshot))
}

// view okumanın göreceği durumu döner: salt-okunur işlemdeyse görüntüyü,
// değilse canlı hâli.
func (f *fakeStore) view(ctx context.Context) fakeSnapshot {
	if snapshot, ok := ctx.Value(readSnapshotKey{}).(*fakeSnapshot); ok {
		return *snapshot
	}
	return f.snapshot()
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if _, ok := ctx.Value(txMarkerKey{}).(*txState); !ok {
		return errors.Internal("fake_tx_required", "%s işlem içinde çağrılmalı", op)
	}
	return nil
}

// notFound eksik sipariş hatasıdır.
func notFound(id string) error {
	return errors.NotFound("order_not_found", "sipariş bulunamadı: %s", id)
}

// --- siparişler --------------------------------------------------------------

// CreateOrder yeni bir sipariş yazar ve numarasını DEPO üretir.
func (f *fakeStore) CreateOrder(ctx context.Context, order models.Order) (models.Order, error) {
	if hook := f.takeCreateHook(); hook != nil {
		hook()
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if order.IdempotencyKey != "" {
		// Döngü ANAHTARLA gezilir: sipariş yapısı büyüktür ve değerle
		// kopyalamak her tur birkaç yüz baytı boşuna taşır.
		for id := range f.orders {
			if f.orders[id].DeletedAt == nil && f.orders[id].IdempotencyKey == order.IdempotencyKey {
				return models.Order{}, errors.Conflict("order_idempotency_key_taken",
					"bu idempotency anahtarıyla bir sipariş zaten var")
			}
		}
	}

	f.displaySeq++
	order.DisplayID = f.displaySeq
	if f.forceDisplayID != nil {
		order.DisplayID = *f.forceDisplayID
	}
	stamp := f.nextStamp()
	order.PlacedAt = stamp
	order.CreatedAt = stamp
	order.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.orders, order.ID))
	f.orders[order.ID] = order
	return order, nil
}

// takeCreateHook varsa kancayı alır ve temizler.
func (f *fakeStore) takeCreateHook() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	hook := f.hookCreateOrder
	f.hookCreateOrder = nil
	return hook
}

// GetOrder siparişi kimliğiyle döner.
func (f *fakeStore) GetOrder(ctx context.Context, id string) (models.Order, error) {
	order, ok := f.view(ctx).orders[id]
	if !ok || order.DeletedAt != nil {
		return models.Order{}, notFound(id)
	}
	return order, nil
}

// GetOrderByDisplayID siparişi numarasıyla döner.
func (f *fakeStore) GetOrderByDisplayID(ctx context.Context, displayID int64) (models.Order, error) {
	snapshot := f.view(ctx)
	for id := range snapshot.orders {
		if snapshot.orders[id].DeletedAt == nil && snapshot.orders[id].DisplayID == displayID {
			return snapshot.orders[id], nil
		}
	}
	return models.Order{}, errors.NotFound("order_not_found", "sipariş bulunamadı: #%d", displayID)
}

// GetOrderByIdempotencyKey anahtarla açılmış siparişi döner.
func (f *fakeStore) GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error) {
	snapshot := f.view(ctx)
	for id := range snapshot.orders {
		order := snapshot.orders[id]
		if order.DeletedAt == nil && order.IdempotencyKey != "" && order.IdempotencyKey == key {
			return order, nil
		}
	}
	return models.Order{}, errors.NotFound("order_not_found",
		"bu idempotency anahtarıyla sipariş bulunamadı")
}

// LockOrder siparişi kilitler; yalnızca işlem içinde çağrılabilir.
func (f *fakeStore) LockOrder(ctx context.Context, id string) (models.Order, error) {
	if err := requireTx(ctx, "LockOrder"); err != nil {
		return models.Order{}, err
	}

	f.mu.Lock()
	f.lockedOrders = append(f.lockedOrders, id)
	order, ok := f.orders[id]
	f.mu.Unlock()

	if !ok || order.DeletedAt != nil {
		return models.Order{}, notFound(id)
	}
	return order, nil
}

// ListOrders siparişleri filtreleyip sayfalar.
func (f *fakeStore) ListOrders(ctx context.Context, filter models.OrderFilter) ([]models.Order, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Order, 0, len(snapshot.orders))
	for id := range snapshot.orders {
		if snapshot.orders[id].DeletedAt != nil {
			continue
		}
		if filter.CustomerID != nil && snapshot.orders[id].CustomerID != *filter.CustomerID {
			continue
		}
		if filter.RegionID != nil && snapshot.orders[id].RegionID != *filter.RegionID {
			continue
		}
		if filter.Status != nil && snapshot.orders[id].Status != *filter.Status {
			continue
		}
		matched = append(matched, snapshot.orders[id])
	}
	// created_at DESC, id DESC — sorgudaki sıralamanın aynısı.
	slices.SortFunc(matched, func(a, b models.Order) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return b.CreatedAt.Compare(a.CreatedAt)
		}
		return strings.Compare(b.ID, a.ID)
	})

	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Order{}, total, nil
	}
	end := min(filter.Offset+filter.Limit, total)
	return slices.Clone(matched[filter.Offset:end]), total, nil
}

// OrdersByIDs kimlik kümesini döner.
func (f *fakeStore) OrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error) {
	snapshot := f.view(ctx)
	out := make([]models.Order, 0, len(ids))
	for _, id := range slices.Sorted(slices.Values(ids)) {
		if order, ok := snapshot.orders[id]; ok && order.DeletedAt == nil {
			out = append(out, order)
		}
	}
	return out, nil
}

// CancelOrder siparişi iptal eder; yalnızca 'pending' durumda etki eder.
func (f *fakeStore) CancelOrder(ctx context.Context, id, reason string) (models.Order, error) {
	return f.applyStatus(ctx, id, models.OrderPending, models.OrderCanceled, reason)
}

// CompleteOrder siparişi tamamlar; yalnızca 'pending' durumda etki eder.
func (f *fakeStore) CompleteOrder(ctx context.Context, id string) (models.Order, error) {
	return f.applyStatus(ctx, id, models.OrderPending, models.OrderCompleted, "")
}

// ArchiveOrder siparişi arşivler; yalnızca 'completed' durumda etki eder.
func (f *fakeStore) ArchiveOrder(ctx context.Context, id string) (models.Order, error) {
	return f.applyStatus(ctx, id, models.OrderCompleted, models.OrderArchived, "")
}

// applyStatus durum geçiş sorgularının WHERE koşulunu taklit eder.
func (f *fakeStore) applyStatus(ctx context.Context, id string, required, next models.OrderStatus, reason string) (models.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[id]
	if !ok || order.DeletedAt != nil || order.Status != required {
		return models.Order{}, errors.Conflict("order_state_changed",
			"geçiş uygulanamadı: siparişin durumu beklenenden farklı (%s)", id)
	}

	stamp := f.nextStamp()
	order.Status = next
	order.UpdatedAt = stamp
	switch next {
	case models.OrderCanceled:
		order.CanceledAt = &stamp
		order.CancelReason = reason
	case models.OrderCompleted:
		order.CompletedAt = &stamp
	case models.OrderArchived, models.OrderPending:
		// Damga değişmez.
	}
	f.recordUndo(ctx, undoEntry(f.orders, id))
	f.orders[id] = order
	return order, nil
}

// --- satırlar ----------------------------------------------------------------

// CreateLineItem yeni bir sipariş satırı yazar.
func (f *fakeStore) CreateLineItem(ctx context.Context, item models.OrderLineItem) (models.OrderLineItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failCreateLineItem != nil {
		return models.OrderLineItem{}, f.failCreateLineItem
	}
	if order, ok := f.orders[item.OrderID]; !ok || order.DeletedAt != nil {
		return models.OrderLineItem{}, notFound(item.OrderID)
	}
	stamp := f.nextStamp()
	item.CreatedAt = stamp
	item.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.items, item.ID))
	f.items[item.ID] = item
	return item, nil
}

// ListLineItems siparişin satırlarını oluşturulma sırasıyla döner.
func (f *fakeStore) ListLineItems(ctx context.Context, orderID string) ([]models.OrderLineItem, error) {
	snapshot := f.view(ctx)
	out := make([]models.OrderLineItem, 0)
	for id := range snapshot.items {
		if snapshot.items[id].OrderID == orderID {
			out = append(out, snapshot.items[id])
		}
	}
	slices.SortFunc(out, func(a, b models.OrderLineItem) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// --- özet --------------------------------------------------------------------

// CreateSummary siparişin özetini açar.
func (f *fakeStore) CreateSummary(ctx context.Context, summary models.OrderSummary) (models.OrderSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failCreateSummary != nil {
		return models.OrderSummary{}, f.failCreateSummary
	}
	if order, ok := f.orders[summary.OrderID]; !ok || order.DeletedAt != nil {
		return models.OrderSummary{}, notFound(summary.OrderID)
	}
	if _, exists := f.summaries[summary.OrderID]; exists {
		return models.OrderSummary{}, errors.Conflict("order_summary_exists",
			"siparişin özeti zaten var")
	}
	stamp := f.nextStamp()
	summary.CreatedAt = stamp
	summary.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.summaries, summary.OrderID))
	f.summaries[summary.OrderID] = summary
	return summary, nil
}

// GetSummary siparişin özetini döner.
func (f *fakeStore) GetSummary(ctx context.Context, orderID string) (models.OrderSummary, error) {
	summary, ok := f.view(ctx).summaries[orderID]
	if !ok {
		return models.OrderSummary{}, errors.NotFound("order_summary_not_found",
			"sipariş özeti bulunamadı: %s", orderID)
	}
	return summary, nil
}

// SetSummaryTotals özet tutarlarını GREATEST ile birleştirir.
//
// Birleştirme sorgunun kendisindedir (queries/order_summaries.sql), yani
// veritabanının davranışıdır ve sahte onu taklit eder.
func (f *fakeStore) SetSummaryTotals(ctx context.Context, orderID string, paid, refunded int64) (models.OrderSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	summary, ok := f.summaries[orderID]
	if !ok {
		return models.OrderSummary{}, errors.NotFound("order_summary_not_found",
			"sipariş özeti bulunamadı: %s", orderID)
	}
	summary.PaidTotal = max(summary.PaidTotal, paid)
	summary.RefundedTotal = max(summary.RefundedTotal, refunded)
	summary.UpdatedAt = f.nextStamp()
	f.recordUndo(ctx, undoEntry(f.summaries, orderID))
	f.summaries[orderID] = summary
	return summary, nil
}

// --- iade / değişim / hasar --------------------------------------------------

// CreateReturn iade kaydı yazar.
func (f *fakeStore) CreateReturn(ctx context.Context, ret models.Return) (models.Return, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if order, ok := f.orders[ret.OrderID]; !ok || order.DeletedAt != nil {
		return models.Return{}, notFound(ret.OrderID)
	}
	stamp := f.nextStamp()
	ret.CreatedAt = stamp
	ret.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.returns, ret.ID))
	f.returns[ret.ID] = ret
	return ret, nil
}

// GetReturn iade kaydını döner.
func (f *fakeStore) GetReturn(ctx context.Context, id string) (models.Return, error) {
	ret, ok := f.view(ctx).returns[id]
	if !ok {
		return models.Return{}, errors.NotFound("order_return_not_found",
			"iade kaydı bulunamadı: %s", id)
	}
	return ret, nil
}

// ListReturns siparişin iade kayıtlarını sayfalar.
func (f *fakeStore) ListReturns(ctx context.Context, filter models.ChildFilter) ([]models.Return, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Return, 0)
	for id := range snapshot.returns {
		if snapshot.returns[id].OrderID == filter.OrderID {
			matched = append(matched, snapshot.returns[id])
		}
	}
	slices.SortFunc(matched, func(a, b models.Return) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Return{}, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, total)], total, nil
}

// CreateExchange değişim kaydı yazar.
func (f *fakeStore) CreateExchange(ctx context.Context, exchange models.Exchange) (models.Exchange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if order, ok := f.orders[exchange.OrderID]; !ok || order.DeletedAt != nil {
		return models.Exchange{}, notFound(exchange.OrderID)
	}
	stamp := f.nextStamp()
	exchange.CreatedAt = stamp
	exchange.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.exchanges, exchange.ID))
	f.exchanges[exchange.ID] = exchange
	return exchange, nil
}

// GetExchange değişim kaydını döner.
func (f *fakeStore) GetExchange(ctx context.Context, id string) (models.Exchange, error) {
	exchange, ok := f.view(ctx).exchanges[id]
	if !ok {
		return models.Exchange{}, errors.NotFound("order_exchange_not_found",
			"değişim kaydı bulunamadı: %s", id)
	}
	return exchange, nil
}

// ListExchanges siparişin değişim kayıtlarını sayfalar.
func (f *fakeStore) ListExchanges(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Exchange, 0)
	for id := range snapshot.exchanges {
		if snapshot.exchanges[id].OrderID == filter.OrderID {
			matched = append(matched, snapshot.exchanges[id])
		}
	}
	slices.SortFunc(matched, func(a, b models.Exchange) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Exchange{}, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, total)], total, nil
}

// CreateClaim hasar kaydı yazar.
func (f *fakeStore) CreateClaim(ctx context.Context, claim models.Claim) (models.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if order, ok := f.orders[claim.OrderID]; !ok || order.DeletedAt != nil {
		return models.Claim{}, notFound(claim.OrderID)
	}
	stamp := f.nextStamp()
	claim.CreatedAt = stamp
	claim.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.claims, claim.ID))
	f.claims[claim.ID] = claim
	return claim, nil
}

// GetClaim hasar kaydını döner.
func (f *fakeStore) GetClaim(ctx context.Context, id string) (models.Claim, error) {
	claim, ok := f.view(ctx).claims[id]
	if !ok {
		return models.Claim{}, errors.NotFound("order_claim_not_found",
			"hasar kaydı bulunamadı: %s", id)
	}
	return claim, nil
}

// ListClaims siparişin hasar kayıtlarını sayfalar.
func (f *fakeStore) ListClaims(ctx context.Context, filter models.ChildFilter) ([]models.Claim, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Claim, 0)
	for id := range snapshot.claims {
		if snapshot.claims[id].OrderID == filter.OrderID {
			matched = append(matched, snapshot.claims[id])
		}
	}
	slices.SortFunc(matched, func(a, b models.Claim) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Claim{}, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, total)], total, nil
}

// --- bağ servisi -------------------------------------------------------------

// fakeLink service.Linker'ın bellek içi karşılığıdır.
type fakeLink struct {
	mu sync.Mutex
	// created kurulan bağları "ad|from|to" biçiminde SIRASIYLA tutar.
	created []string
	// deleted kaldırılan bağları aynı biçimde tutar.
	deleted []string
	// failOn ayarlanırsa o adlı bağın kurulması bu hatayla düşer.
	failOn string
	// failErr failOn tetiklendiğinde dönen hatadır.
	failErr error

	// hookCreate ayarlanırsa İLK Create çağrısının gövdesinden önce çalışır ve
	// ardından temizlenir.
	//
	// Araya giren bir çağrıyı deterministik kurmak için vardır: kanca içinden
	// yapılan ikinci bir CreateOrder, birincisi tam bağını kurarken çalışır ve
	// "yarı doğmuş sipariş görünür mü" sorusu zamanlamaya bırakılmadan
	// sınanabilir. TEK ATIMLIK olması şart: kanca içindeki çağrı da bağ kurar
	// ve kanca temizlenmezse sonsuz özyineleme olurdu. Kanca kilit TUTULMADAN
	// çağrılır; aksi hâlde içerideki çağrı aynı sahteye girip kilitlenirdi.
	hookCreate func()
}

// Sahte bağ servisinin servisin beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ service.Linker = (*fakeLink)(nil)

// newFakeLink boş bir sahte bağ servisi üretir.
func newFakeLink() *fakeLink { return &fakeLink{} }

// linkKey bağın kayıt anahtarıdır.
func linkKey(name, from, to string) string { return name + "|" + from + "|" + to }

// Create bağı kaydeder.
func (l *fakeLink) Create(_ context.Context, name, fromID, toID string) error {
	l.mu.Lock()
	hook := l.hookCreate
	l.hookCreate = nil
	// Hata KARARI kancadan ÖNCE alınır: kanca içindeki çağrı failOn'u
	// temizleyip kendi bağını kurabilsin, bu çağrı yine de düşsün.
	failing := l.failOn != "" && l.failOn == name
	failErr := l.failErr
	l.mu.Unlock()

	if hook != nil {
		hook()
	}
	if failing {
		return failErr
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.created = append(l.created, linkKey(name, fromID, toID))
	return nil
}

// Delete bağı kaldırır.
func (l *fakeLink) Delete(_ context.Context, name, fromID, toID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := linkKey(name, fromID, toID)
	l.deleted = append(l.deleted, key)
	l.created = slices.DeleteFunc(l.created, func(existing string) bool {
		return existing == key
	})
	return nil
}

// List fromID'ye bağlı hedefleri döner.
func (l *fakeLink) List(_ context.Context, name, fromID string) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	prefix := name + "|" + fromID + "|"
	out := make([]string, 0)
	for _, key := range l.created {
		if after, ok := strings.CutPrefix(key, prefix); ok {
			out = append(out, after)
		}
	}
	return out, nil
}

// links kurulmuş bağların anlık kopyasını döner.
func (l *fakeLink) links() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.created)
}

// --- olay veri yolu ----------------------------------------------------------

// fakeBus service.EventPublisher'ın bellek içi karşılığıdır.
type fakeBus struct {
	mu sync.Mutex
	// published yayımlanan olayları SIRASIYLA tutar.
	published []eventbus.Event
	// failErr ayarlanırsa Publish bu hatayı döner.
	failErr error
}

// Sahte veri yolunun servisin beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ service.EventPublisher = (*fakeBus)(nil)

// newFakeBus boş bir sahte veri yolu üretir.
func newFakeBus() *fakeBus { return &fakeBus{} }

// Publish olayı kaydeder.
func (b *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.failErr != nil {
		return b.failErr
	}
	b.published = append(b.published, e)
	return nil
}

// events yayımlanan olayların anlık kopyasını döner.
func (b *fakeBus) events() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.published)
}
