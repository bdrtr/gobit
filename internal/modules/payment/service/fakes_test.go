package service_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// txMarkerKey sahte deponun "işlem içindeyiz" işaretidir.
type txMarkerKey struct{}

// fakeStore service.Store'un bellek içi karşılığıdır.
//
// Üç davranışı gerçek depodan BİLİNÇLİ olarak taklit eder, çünkü servisin
// doğruluğu bunlara dayanır:
//
//  1. Kilit alan metotlar işlem DIŞINDA çağrılırsa hata döner. Servis bir
//     akışta WithTx'i unutursa birim testi bunu yakalar; gerçek veritabanında
//     bu hata, kilitsiz okuma yüzünden ancak yarış altında görünürdü.
//  2. İşlem hatayla biterse yazılanlar GERİ ALINIR. "Hata döndü ve hiçbir şey
//     yazılmadı" iddiası ancak böyle sınanabilir.
//  3. Bir oturumdan en fazla BİR tahsilat çıkar; benzersizlik kısıtının
//     karşılığıdır ve Capture'ın idempotentliği ona dayanır.
type fakeStore struct {
	mu          sync.Mutex
	collections map[string]models.PaymentCollection
	sessions    map[string]models.PaymentSession
	payments    map[string]models.Payment
	refunds     map[string]models.Refund

	// kilitler alınan kilitleri SIRASIYLA kaydeder ("collection", "session",
	// "payment"). Kilit sırası bir eşzamanlılık sözleşmesidir ve gerçek
	// veritabanında ihlali ancak yarış altında (kilitlenme olarak) görünür;
	// burada sıra doğrudan okunabilir.
	kilitler []string
	// collectionWrites koleksiyon satırına kaç kez yazıldığını sayar;
	// idempotent dalların tutarlara İKİNCİ KEZ dokunmadığı bununla kanıtlanır.
	collectionWrites int
	// sessionWrites oturum satırına kaç kez yazıldığını sayar.
	sessionWrites int

	// failCreatePayment ayarlanırsa CreatePayment bu hatayı döner; işlem geri
	// alma yolunu sınamak için kullanılır.
	failCreatePayment error
}

// newFakeStore boş bir sahte depo üretir.
func newFakeStore() *fakeStore {
	return &fakeStore{
		collections: map[string]models.PaymentCollection{},
		sessions:    map[string]models.PaymentSession{},
		payments:    map[string]models.Payment{},
		refunds:     map[string]models.Refund{},
	}
}

// Sahte deponun servisin beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ service.Store = (*fakeStore)(nil)

// WithTx fn'i "işlem" içinde çalıştırır; hata dönerse durumu geri alır.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	f.mu.Lock()
	snapshot := struct {
		collections map[string]models.PaymentCollection
		sessions    map[string]models.PaymentSession
		payments    map[string]models.Payment
		refunds     map[string]models.Refund
	}{
		collections: maps.Clone(f.collections),
		sessions:    maps.Clone(f.sessions),
		payments:    maps.Clone(f.payments),
		refunds:     maps.Clone(f.refunds),
	}
	f.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		f.mu.Lock()
		f.collections, f.sessions = snapshot.collections, snapshot.sessions
		f.payments, f.refunds = snapshot.payments, snapshot.refunds
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

// kilitKaydet alınan kilidi sırasıyla kaydeder.
func (f *fakeStore) kilitKaydet(ad string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kilitler = append(f.kilitler, ad)
}

// kilitSirasi kaydedilen kilit sırasını döner.
func (f *fakeStore) kilitSirasi() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.kilitler)
}

// yazimlar koleksiyon ve oturum yazma sayaçlarını döner.
func (f *fakeStore) yazimlar() (collections, sessions int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.collectionWrites, f.sessionWrites
}

// --- koleksiyonlar -----------------------------------------------------------

// CreatePaymentCollection koleksiyonu kaydeder.
func (f *fakeStore) CreatePaymentCollection(
	_ context.Context,
	col models.PaymentCollection,
) (models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now().UTC()
	col.CreatedAt, col.UpdatedAt = now, now
	f.collections[col.ID] = col
	return col, nil
}

// GetPaymentCollection koleksiyonu döner.
func (f *fakeStore) GetPaymentCollection(_ context.Context, id string) (models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	col, ok := f.collections[id]
	if !ok {
		return models.PaymentCollection{}, errors.NotFound("fake_collection_not_found",
			"koleksiyon yok: %s", id)
	}
	return col, nil
}

// LockPaymentCollection koleksiyonu kilitler.
func (f *fakeStore) LockPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	if err := requireTx(ctx, "LockPaymentCollection"); err != nil {
		return models.PaymentCollection{}, err
	}
	f.kilitKaydet("collection")
	return f.GetPaymentCollection(ctx, id)
}

// ListPaymentCollections koleksiyonları süzer ve sayfalar.
func (f *fakeStore) ListPaymentCollections(
	_ context.Context,
	filter models.CollectionFilter,
) ([]models.PaymentCollection, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var eslesen []models.PaymentCollection
	for _, id := range slices.Sorted(maps.Keys(f.collections)) {
		col := f.collections[id]
		if filter.Reference != nil && col.Reference != *filter.Reference {
			continue
		}
		if filter.Status != nil && col.Status.String() != *filter.Status {
			continue
		}
		eslesen = append(eslesen, col)
	}

	total := int64(len(eslesen))
	if filter.Offset >= total {
		return []models.PaymentCollection{}, total, nil
	}
	son := min(filter.Offset+filter.Limit, total)
	return slices.Clone(eslesen[filter.Offset:son]), total, nil
}

// PaymentCollectionsByIDs kimlik kümesini döner.
func (f *fakeStore) PaymentCollectionsByIDs(_ context.Context, ids []string) ([]models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.PaymentCollection, 0, len(ids))
	for _, id := range slices.Sorted(slices.Values(ids)) {
		if col, ok := f.collections[id]; ok {
			out = append(out, col)
		}
	}
	return out, nil
}

// UpdatePaymentCollectionTotals tutarları ve durumu yazar.
func (f *fakeStore) UpdatePaymentCollectionTotals(
	_ context.Context,
	id string,
	status models.CollectionStatus,
	authorized, captured, refunded int64,
) (models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	col, ok := f.collections[id]
	if !ok {
		return models.PaymentCollection{}, errors.NotFound("fake_collection_not_found",
			"koleksiyon yok: %s", id)
	}
	col.Status = status
	col.AuthorizedAmount = authorized
	col.CapturedAmount = captured
	col.RefundedAmount = refunded
	col.UpdatedAt = time.Now().UTC()
	f.collections[id] = col
	f.collectionWrites++
	return col, nil
}

// --- oturumlar ---------------------------------------------------------------

// CreatePaymentSession oturumu kaydeder.
func (f *fakeStore) CreatePaymentSession(
	_ context.Context,
	ses models.PaymentSession,
) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.sessions {
		if f.sessions[id].ProviderID == ses.ProviderID &&
			f.sessions[id].IdempotencyKey == ses.IdempotencyKey {
			return models.PaymentSession{}, errors.Conflict("fake_session_exists",
				"bu anahtarla oturum var: %s", ses.IdempotencyKey)
		}
	}

	now := time.Now().UTC()
	ses.CreatedAt, ses.UpdatedAt = now, now
	f.sessions[ses.ID] = ses
	f.sessionWrites++
	return ses, nil
}

// GetPaymentSession oturumu döner.
func (f *fakeStore) GetPaymentSession(_ context.Context, id string) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ses, ok := f.sessions[id]
	if !ok {
		return models.PaymentSession{}, errors.NotFound("fake_session_not_found", "oturum yok: %s", id)
	}
	return ses, nil
}

// LockPaymentSession oturumu kilitler.
func (f *fakeStore) LockPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	if err := requireTx(ctx, "LockPaymentSession"); err != nil {
		return models.PaymentSession{}, err
	}
	f.kilitKaydet("session")
	return f.GetPaymentSession(ctx, id)
}

// PaymentSessionByIdempotencyKey oturumu anahtarıyla döner.
func (f *fakeStore) PaymentSessionByIdempotencyKey(
	_ context.Context,
	providerID, key string,
) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, id := range slices.Sorted(maps.Keys(f.sessions)) {
		ses := f.sessions[id]
		if ses.ProviderID == providerID && ses.IdempotencyKey == key {
			return ses, nil
		}
	}
	return models.PaymentSession{}, errors.NotFound("fake_session_not_found",
		"anahtarla oturum yok: %s", key)
}

// ListPaymentSessionsByCollection koleksiyonun oturumlarını döner.
func (f *fakeStore) ListPaymentSessionsByCollection(
	_ context.Context,
	collectionID string,
) ([]models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.PaymentSession{}
	for _, id := range slices.Sorted(maps.Keys(f.sessions)) {
		if f.sessions[id].PaymentCollectionID == collectionID {
			out = append(out, f.sessions[id])
		}
	}
	return out, nil
}

// ListSessionsForReconciliation mutabakat için şüpheli kümeyi döner.
//
// Gerçek sorgunun ÜÇ koşulunu da uygular — yetkilendirilmiş, verilen andan
// önce güncellenmiş, silinmemiş — ve sonucu updated_at'e göre sıralar. Sahte
// depo bunları taklit etmeseydi, servisin kümeyi daraltma iddiası hiçbir
// testte yanlışlanamazdı.
func (f *fakeStore) ListSessionsForReconciliation(
	_ context.Context,
	unchangedSince time.Time,
	limit int32,
) ([]models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.PaymentSession{}
	for _, id := range slices.Sorted(maps.Keys(f.sessions)) {
		ses := f.sessions[id]
		if ses.Status != models.SessionAuthorized || ses.DeletedAt != nil {
			continue
		}
		if !ses.UpdatedAt.Before(unchangedSince) {
			continue
		}
		out = append(out, ses)
	}

	slices.SortStableFunc(out, func(a, b models.PaymentSession) int {
		return a.UpdatedAt.Compare(b.UpdatedAt)
	})

	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SessionCounts koleksiyonun oturumlarını duruma göre sayar.
func (f *fakeStore) SessionCounts(_ context.Context, collectionID string) (models.SessionCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var counts models.SessionCounts
	for id := range f.sessions {
		if f.sessions[id].PaymentCollectionID != collectionID {
			continue
		}
		counts.Total++
		switch f.sessions[id].Status {
		case models.SessionPending, models.SessionAuthorized:
			counts.Live++
		case models.SessionCanceled:
			counts.Canceled++
		case models.SessionFailed:
			counts.Failed++
		case models.SessionCaptured:
			// Tahsil edilmiş oturum hiçbir sayıma girmez; koleksiyonun durumu
			// zaten tahsilat tutarından türetilir.
		}
	}
	return counts, nil
}

// LiveSessionAmount canlı oturumların rezerve ettiği tutarı toplar.
//
// Gerçek sorgunun kuralı BİREBİR taklit edilir: bekleyen oturum kendi
// tutarını, yetkilendirilmiş oturum bloke edilen tutarı rezerve eder. Kural
// burada gevşetilseydi, "iki tam tutarlı oturum açılamaz" iddiası birim
// testinde tutar ama gerçek veritabanında tutmazdı.
func (f *fakeStore) LiveSessionAmount(_ context.Context, collectionID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var reserved int64
	for id := range f.sessions {
		ses := f.sessions[id]
		if ses.PaymentCollectionID != collectionID {
			continue
		}
		switch ses.Status {
		case models.SessionPending:
			reserved += ses.Amount
		case models.SessionAuthorized:
			reserved += ses.AuthorizedAmount
		case models.SessionCaptured, models.SessionCanceled, models.SessionFailed:
			// Sonlanmış oturum tutar rezerve etmez.
		}
	}
	return reserved, nil
}

// UpdatePaymentSessionState oturumun durumunu yazar.
func (f *fakeStore) UpdatePaymentSessionState(
	_ context.Context,
	id string,
	status models.SessionStatus,
	authorizedAmount int64,
	data []byte,
	declineReason string,
) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ses, ok := f.sessions[id]
	if !ok {
		return models.PaymentSession{}, errors.NotFound("fake_session_not_found", "oturum yok: %s", id)
	}
	ses.Status = status
	ses.AuthorizedAmount = authorizedAmount
	ses.Data = json.RawMessage(data)
	ses.DeclineReason = declineReason
	ses.UpdatedAt = time.Now().UTC()
	f.sessions[id] = ses
	f.sessionWrites++
	return ses, nil
}

// --- tahsilatlar ve iadeler --------------------------------------------------

// CreatePayment tahsilatı kaydeder; oturum başına en fazla bir tane.
func (f *fakeStore) CreatePayment(_ context.Context, pay models.Payment) (models.Payment, error) {
	if f.failCreatePayment != nil {
		return models.Payment{}, f.failCreatePayment
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.payments {
		if f.payments[id].PaymentSessionID == pay.PaymentSessionID {
			return models.Payment{}, errors.Conflict("fake_payment_exists",
				"bu oturumdan tahsilat çıkmış: %s", pay.PaymentSessionID)
		}
	}

	now := time.Now().UTC()
	pay.CreatedAt, pay.UpdatedAt = now, now
	f.payments[pay.ID] = pay
	return pay, nil
}

// GetPayment tahsilatı döner.
func (f *fakeStore) GetPayment(_ context.Context, id string) (models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pay, ok := f.payments[id]
	if !ok {
		return models.Payment{}, errors.NotFound("fake_payment_not_found", "tahsilat yok: %s", id)
	}
	return pay, nil
}

// LockPayment tahsilatı kilitler.
func (f *fakeStore) LockPayment(ctx context.Context, id string) (models.Payment, error) {
	if err := requireTx(ctx, "LockPayment"); err != nil {
		return models.Payment{}, err
	}
	f.kilitKaydet("payment")
	return f.GetPayment(ctx, id)
}

// PaymentBySession oturumdan doğan tahsilatı döner.
func (f *fakeStore) PaymentBySession(_ context.Context, sessionID string) (models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, id := range slices.Sorted(maps.Keys(f.payments)) {
		if f.payments[id].PaymentSessionID == sessionID {
			return f.payments[id], nil
		}
	}
	return models.Payment{}, errors.NotFound("fake_payment_not_found",
		"oturumdan tahsilat yok: %s", sessionID)
}

// ListPaymentsByCollection koleksiyonun tahsilatlarını döner.
func (f *fakeStore) ListPaymentsByCollection(_ context.Context, collectionID string) ([]models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.Payment{}
	for _, id := range slices.Sorted(maps.Keys(f.payments)) {
		if f.payments[id].PaymentCollectionID == collectionID {
			out = append(out, f.payments[id])
		}
	}
	return out, nil
}

// UpdatePaymentRefundedAmount iade edilen tutarı yazar.
func (f *fakeStore) UpdatePaymentRefundedAmount(
	_ context.Context,
	id string,
	refunded int64,
) (models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pay, ok := f.payments[id]
	if !ok {
		return models.Payment{}, errors.NotFound("fake_payment_not_found", "tahsilat yok: %s", id)
	}
	pay.RefundedAmount = refunded
	pay.UpdatedAt = time.Now().UTC()
	f.payments[id] = pay
	return pay, nil
}

// CreateRefund iadeyi kaydeder.
func (f *fakeStore) CreateRefund(_ context.Context, ref models.Refund) (models.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now().UTC()
	ref.CreatedAt, ref.UpdatedAt = now, now
	f.refunds[ref.ID] = ref
	return ref, nil
}

// ListRefundsByPayment tahsilatın iadelerini döner.
func (f *fakeStore) ListRefundsByPayment(_ context.Context, paymentID string) ([]models.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.Refund{}
	for _, id := range slices.Sorted(maps.Keys(f.refunds)) {
		if f.refunds[id].PaymentID == paymentID {
			out = append(out, f.refunds[id])
		}
	}
	return out, nil
}

// --- sahte sağlayıcı ---------------------------------------------------------

// fakeProvider senaryolanabilir bir ödeme sağlayıcısıdır.
//
// Gerçek sağlayıcı yerine kullanılır ki servisin KARARLARI, sağlayıcının
// davranışından bağımsız olarak sınanabilsin: reddi, hatayı ve kısmi
// yetkilendirmeyi burada tek satırla kurmak mümkündür.
type fakeProvider struct {
	mu sync.Mutex

	id string
	// nextStatus bir sonraki Authorize'ın döneceği durumdur.
	nextStatus coreprovider.SessionStatus
	// authorizedAmount sıfır değilse Authorize bu tutarı bildirir.
	authorizedAmount int64
	// declineReason ret sebebidir.
	declineReason string
	// authorizeData Authorize'ın döneceği ham gövdedir. Boş bırakılması
	// GERÇEKÇİDİR: sağlayıcıların çoğu yetkilendirme yanıtında gövde döndürmez
	// ve modülün o hâlde oturumun mevcut verisini KORUMASI gerekir.
	authorizeData json.RawMessage
	// authorizeErr ayarlanırsa Authorize bu hatayı döner.
	authorizeErr error
	// captureErr ayarlanırsa Capture bu hatayı döner.
	captureErr error
	// cancelErr ayarlanırsa Cancel bu hatayı döner.
	cancelErr error
	// createErr ayarlanırsa CreateSession bu hatayı döner.
	createErr error

	// createCalls, authorizeCalls, captureCalls, refundCalls, cancelCalls
	// sağlayıcıya KAÇ KEZ gidildiğini sayar. İdempotent dalların sağlayıcıya
	// hiç gitmediği bu sayaçlarla kanıtlanır.
	createCalls    int
	authorizeCalls int
	captureCalls   int
	refundCalls    int
	cancelCalls    int

	// sessions açılmış sağlayıcı oturumlarıdır (anahtar -> kimlik).
	sessions map[string]string
}

// newFakeProvider varsayılan davranışı "yetkilendir" olan bir sağlayıcı üretir.
func newFakeProvider(id string) *fakeProvider {
	return &fakeProvider{
		id:         id,
		nextStatus: coreprovider.SessionAuthorized,
		sessions:   map[string]string{},
	}
}

// Sahte sağlayıcının çekirdek sözleşmesini karşıladığı derleme zamanında
// doğrulanır.
var _ coreprovider.PaymentProvider = (*fakeProvider)(nil)

// ID sağlayıcının kimliğini döner.
func (p *fakeProvider) ID() string { return p.id }

// CreateSession sağlayıcı tarafında bir oturum açar.
func (p *fakeProvider) CreateSession(
	_ context.Context,
	in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.createErr != nil {
		return coreprovider.Session{}, p.createErr
	}
	p.createCalls++

	id, ok := p.sessions[in.IdempotencyKey]
	if !ok {
		id = "ext_" + in.IdempotencyKey
		p.sessions[in.IdempotencyKey] = id
	}
	return coreprovider.Session{
		ID:           id,
		Status:       coreprovider.SessionPending,
		Amount:       in.Amount,
		CurrencyCode: in.CurrencyCode,
		Data:         json.RawMessage(`{"fake":true}`),
	}, nil
}

// Authorize senaryolanmış sonucu döner.
func (p *fakeProvider) Authorize(_ context.Context, _ string) (coreprovider.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.authorizeErr != nil {
		return coreprovider.AuthResult{}, p.authorizeErr
	}
	p.authorizeCalls++
	return coreprovider.AuthResult{
		Status:           p.nextStatus,
		AuthorizedAmount: p.authorizedAmount,
		Data:             p.authorizeData,
		DeclineReason:    p.declineReason,
	}, nil
}

// Capture tahsilatı kaydeder.
func (p *fakeProvider) Capture(_ context.Context, _ string, _ int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.captureErr != nil {
		return p.captureErr
	}
	p.captureCalls++
	return nil
}

// Refund iadeyi kaydeder.
func (p *fakeProvider) Refund(_ context.Context, _ string, _ int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refundCalls++
	return nil
}

// Cancel iptali kaydeder.
func (p *fakeProvider) Cancel(_ context.Context, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cancelErr != nil {
		return p.cancelErr
	}
	p.cancelCalls++
	return nil
}

// cagrilar sağlayıcıya yapılan çağrı sayılarını döner.
func (p *fakeProvider) cagrilar() (create, authorize, capture, refund, cancel int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.createCalls, p.authorizeCalls, p.captureCalls, p.refundCalls, p.cancelCalls
}

// senaryo sağlayıcının bir sonraki yanıtını ayarlar.
func (p *fakeProvider) senaryo(status coreprovider.SessionStatus, amount int64, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextStatus = status
	p.authorizedAmount = amount
	p.declineReason = reason
}

// yetkilendirmeVerisi Authorize'ın döneceği ham gövdeyi ayarlar.
func (p *fakeProvider) yetkilendirmeVerisi(data json.RawMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorizeData = data
}
