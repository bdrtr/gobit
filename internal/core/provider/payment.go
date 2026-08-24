package provider

import (
	"context"
	"encoding/json"
)

// SessionStatus bir ödeme oturumunun durumudur.
type SessionStatus string

// Ödeme oturumu durumları.
const (
	// SessionPending oturum açıldı ama henüz yetkilendirilmedi.
	SessionPending SessionStatus = "pending"
	// SessionAuthorized tutar müşterinin üzerinde BLOKE edildi; henüz çekilmedi.
	SessionAuthorized SessionStatus = "authorized"
	// SessionCaptured tutar tahsil edildi.
	SessionCaptured SessionStatus = "captured"
	// SessionCanceled oturum iptal edildi; blokaj varsa serbest bırakıldı.
	SessionCanceled SessionStatus = "canceled"
	// SessionFailed sağlayıcı işlemi reddetti.
	SessionFailed SessionStatus = "failed"
)

// CreateSessionInput yeni bir ödeme oturumunun girdisidir.
type CreateSessionInput struct {
	// Amount tahsil edilecek tutardır, minor unit TAM SAYI (plan Bölüm 8).
	Amount int64
	// CurrencyCode ISO 4217 para birimi kodudur.
	CurrencyCode string
	// Reference çağıranın kendi kaydına verdiği kimliktir (örn. payment
	// collection kimliği). Sağlayıcı bunu kendi tarafında saklar; mutabakatta
	// iki sistemi eşleştiren alan budur.
	Reference string
	// IdempotencyKey aynı oturumun iki kez açılmasını engeller.
	//
	// Saga bir adımı yeniden deneyebilir (plan Bölüm 2.6); anahtar olmadan
	// tekrar, müşteriden İKİNCİ KEZ tahsilat denemesi anlamına gelirdi.
	IdempotencyKey string
	// Data sağlayıcıya özgü serbest veridir (kart tokenı, dönüş adresi vb.).
	Data map[string]any
}

// Session sağlayıcıda açılmış bir ödeme oturumudur.
type Session struct {
	// ID sağlayıcı tarafındaki oturum kimliğidir.
	ID string
	// Status oturumun güncel durumudur.
	Status SessionStatus
	// Amount ve CurrencyCode oturumun tutarıdır.
	Amount       int64
	CurrencyCode string
	// Data sağlayıcının döndürdüğü ham veridir (örn. istemcinin kullanacağı
	// client_secret). Olduğu gibi saklanır; çekirdek yorumlamaz.
	Data json.RawMessage
}

// AuthResult yetkilendirme denemesinin sonucudur.
type AuthResult struct {
	// Status yetkilendirme sonrası oturum durumudur.
	Status SessionStatus
	// AuthorizedAmount bloke edilen tutardır; kısmi yetkilendirmede
	// istenenden küçük olabilir.
	AuthorizedAmount int64
	// Data sağlayıcının döndürdüğü ham veridir.
	Data json.RawMessage
	// DeclineReason Status SessionFailed ise reddin sağlayıcı tarafındaki
	// sebebidir. Müşteriye GÖSTERİLMEK üzere değil, teşhis içindir.
	DeclineReason string
}

// PaymentProvider bir ödeme sağlayıcısının çekirdeğe sunduğu sözleşmedir
// (plan Bölüm 5.6).
//
// # İdempotency ve saga
//
// Bu arayüzün metotları saga adımlarından çağrılır ve saga bir adımı YENİDEN
// DENEYEBİLİR. Bu yüzden:
//   - CreateSession, aynı IdempotencyKey ile ikinci kez çağrıldığında YENİ
//     oturum açmaz, mevcut oturumu döner.
//   - Authorize, Capture ve Refund aynı oturum üzerinde tekrar çağrılabilir
//     olmalıdır; ikinci çağrı hata DEĞİL, mevcut durumu dönmelidir.
//
// Telafi (Compensate) yolu Cancel'dır ve saga şartı gereği İDEMPOTENT olmak
// zorundadır: iki kez iptal edilen bir oturum ikinci çağrıda hata vermemelidir.
type PaymentProvider interface {
	Provider

	// CreateSession sağlayıcıda bir ödeme oturumu açar.
	CreateSession(ctx context.Context, in CreateSessionInput) (Session, error)

	// Authorize tutarı müşterinin üzerinde BLOKE eder; tahsilat yapmaz.
	//
	// Ayrım bilinçlidir: saga siparişi oluşturduktan sonra tahsilata geçer ve
	// arada bir adım patlarsa blokajı serbest bırakmak, çekilmiş bir tutarı
	// iade etmekten hem hızlı hem geri dönüşsüz-olmayan bir işlemdir.
	Authorize(ctx context.Context, sessionID string) (AuthResult, error)

	// Capture bloke edilmiş tutarı tahsil eder. amount, yetkilendirilen
	// tutardan büyük OLAMAZ; sıfır verilirse tamamı çekilir.
	Capture(ctx context.Context, sessionID string, amount int64) error

	// Refund tahsil edilmiş tutarı iade eder. amount sıfırsa tamamı iade edilir.
	Refund(ctx context.Context, sessionID string, amount int64) error

	// Cancel yetkilendirilmiş ama tahsil EDİLMEMİŞ bir oturumu iptal eder ve
	// blokajı serbest bırakır. Saga telafisi budur; İDEMPOTENT olmalıdır.
	Cancel(ctx context.Context, sessionID string) error
}
