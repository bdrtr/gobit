package manual_test

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// txMarkerKey sahte deponun "işlem içindeyiz" işaretidir.
type txMarkerKey struct{}

// memStore manual.Store'un bellek içi karşılığıdır.
//
// Üç davranışı gerçek depodan BİLİNÇLİ olarak taklit eder, çünkü sağlayıcının
// doğruluğu bunlara dayanır:
//
//  1. LockManualSession işlem DIŞINDA çağrılırsa hata döner. Sağlayıcı bir
//     akışta WithTx'i unutursa birim testi bunu yakalar.
//  2. İşlem hatayla biterse yazılanlar GERİ ALINIR; "hata döndü ve defter
//     değişmedi" iddiası ancak böyle sınanabilir.
//  3. InsertManualSessionIfAbsent aynı anahtarla ikinci kez yazmaz ve HATA
//     DÖNMEZ; idempotency sözleşmesinin zemini budur.
type memStore struct {
	mu       sync.Mutex
	sessions map[string]models.ManualSession

	// insertCalls kaç kez GERÇEK yazma yapıldığını sayar; aynı anahtarla
	// ikinci CreateSession'ın yeni satır açmadığı bununla kanıtlanır.
	insertCalls int
	// updateCalls kaç kez durum yazıldığını sayar; idempotent dalların
	// deftere İKİNCİ KEZ dokunmadığı bununla kanıtlanır.
	updateCalls int
}

// newMemStore boş bir bellek içi defter üretir.
func newMemStore() *memStore {
	return &memStore{sessions: map[string]models.ManualSession{}}
}

// Sahte deponun sağlayıcının beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ manual.Store = (*memStore)(nil)

// WithTx fn'i "işlem" içinde çalıştırır; hata dönerse defteri geri alır.
func (m *memStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	m.mu.Lock()
	snapshot := maps.Clone(m.sessions)
	m.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		m.mu.Lock()
		m.sessions = snapshot
		m.mu.Unlock()
		return err
	}
	return nil
}

// InsertManualSessionIfAbsent oturumu yalnızca anahtar boştaysa yazar.
func (m *memStore) InsertManualSessionIfAbsent(
	_ context.Context,
	ses models.ManualSession,
) (models.ManualSession, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == ses.IdempotencyKey {
			return models.ManualSession{}, false, nil
		}
	}

	now := time.Now().UTC()
	ses.CreatedAt, ses.UpdatedAt = now, now
	m.sessions[ses.ID] = ses
	m.insertCalls++
	return ses, true, nil
}

// ManualSessionByIdempotencyKey oturumu anahtarıyla döner.
func (m *memStore) ManualSessionByIdempotencyKey(_ context.Context, key string) (models.ManualSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == key {
			return m.sessions[id], nil
		}
	}
	return models.ManualSession{}, errors.NotFound("fake_not_found", "oturum yok: %s", key)
}

// ManualSession oturumu kimliğiyle döner.
func (m *memStore) ManualSession(_ context.Context, id string) (models.ManualSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ses, ok := m.sessions[id]
	if !ok {
		return models.ManualSession{}, errors.NotFound("fake_not_found", "oturum yok: %s", id)
	}
	return ses, nil
}

// LockManualSession oturumu döner; işlem dışında çağrılırsa hata verir.
func (m *memStore) LockManualSession(ctx context.Context, id string) (models.ManualSession, error) {
	if ctx.Value(txMarkerKey{}) == nil {
		return models.ManualSession{}, errors.Internal("fake_tx_required",
			"LockManualSession işlem dışında çağrıldı")
	}
	return m.ManualSession(ctx, id)
}

// UpdateManualSessionState durumu ve tutarları mutlak değerlerle yazar.
func (m *memStore) UpdateManualSessionState(
	_ context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.ManualSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ses, ok := m.sessions[id]
	if !ok {
		return models.ManualSession{}, errors.NotFound("fake_not_found", "oturum yok: %s", id)
	}
	ses.Status = status
	ses.AuthorizedAmount = authorized
	ses.CapturedAmount = captured
	ses.RefundedAmount = refunded
	ses.DeclineReason = declineReason
	ses.UpdatedAt = time.Now().UTC()
	m.sessions[id] = ses
	m.updateCalls++
	return ses, nil
}

// sayimlar yazma sayaçlarını birlikte döner.
func (m *memStore) sayimlar() (inserts, updates int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.insertCalls, m.updateCalls
}
