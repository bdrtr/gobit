// Package manual gerçek bir ağ çağrısı yapmayan test/manuel ödeme
// sağlayıcısıdır (plan Faz 6).
//
// [Provider], internal/core/provider'daki PaymentProvider sözleşmesini
// karşılar ve o sözleşmenin godoc'unda yazılı İDEMPOTENCY şartlarını yerine
// getirir:
//
//   - Aynı IdempotencyKey ile ikinci [Provider.CreateSession] YENİ oturum
//     açmaz, mevcut oturumu döner.
//   - [Provider.Authorize], [Provider.Capture] ve [Provider.Refund] aynı oturum
//     üzerinde tekrar çağrılabilir; ikinci çağrı hata DEĞİL, mevcut durumu döner.
//   - [Provider.Cancel] saga telafisidir ve İDEMPOTENTTİR: iki kez iptal edilen
//     bir oturum ikinci çağrıda hata vermez.
//
// # Durum neden VERİTABANINDA tutulur
//
// Bellekte tutulan bir defter, sürecin her yeniden başlatılışında sıfırlanırdı.
// Bunun bedeli üç yerde ödenirdi:
//
//   - e2e akışları (internal/e2e) ve Faz 9 yük testi, süreç yeniden
//     başladığında AÇILMIŞ bir oturumu bulabilmelidir; aksi hâlde ödeme adımı
//     "oturum bulunamadı" ile düşer.
//   - Saga telafisi tam da sürecin düştüğü senaryoda çalışmalıdır. Belleğe
//     dayanan bir sağlayıcıda Cancel, yeniden başlatma sonrası hiçbir zaman
//     çalışamaz ve bloke edilmiş tutar sonsuza kadar asılı kalırdı.
//   - Birden çok süreç (ya da yatay ölçek) aynı oturumu görmezdi; sağlayıcı
//     yalnızca tek örnekli çalışan bir sunucuda doğru davranırdı.
//
// Gerçek bir ödeme kuruluşunun durumu da kendi sistemindedir ve süreç
// yeniden başlatmalarından etkilenmez; taklit bu yüzden kalıcı olmalıdır.
//
// # Defterin ayrılığı
//
// Sağlayıcının durumu payment_manual_sessions tablosundadır ve payment
// servisinin tablolarından AYRIDIR. Servis bu tabloya hiç dokunmaz; sağlayıcıya
// yalnızca PaymentProvider arayüzünden ulaşır. Ayrım, modülün kazara
// sağlayıcının iç durumunu okumasını yapısal olarak engeller — gerçek bir
// sağlayıcıda da böyle bir okuma mümkün değildir.
//
// # Test için başarısızlık enjeksiyonu
//
// Saga testleri ödeme adımını PATLATABİLMELİDİR. Davranış, oturum açılırken
// verilen Data alanından okunur ve oturumla birlikte kalıcı olarak saklanır;
// böylece süreç yeniden başlasa da aynı oturum aynı biçimde davranır. Bkz.
// [DataKeyOutcome], [DataKeyDeclineReason] ve [DataKeyAuthorizedAmount].
package manual

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	coreprovider "github.com/bdrtr/gobit/internal/core/provider"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// ID sağlayıcının kimliğidir; oturumlar bu adla açılır.
const ID = "manual"

// Sağlayıcının davranışını yönlendiren Data anahtarları.
//
// Anahtarlar oturumun Data alanında gelir ve oturumla birlikte SAKLANIR.
// Saklanmaları şart: yetkilendirme, oturumun açıldığı çağrıdan farklı bir
// istekte (hatta farklı bir süreçte) yapılır ve o çağrının elinde yalnızca
// oturum kimliği vardır.
const (
	// DataKeyOutcome yetkilendirmenin sonucunu belirler; değerleri
	// [OutcomeAuthorize], [OutcomeDecline] ve [OutcomeError]'dır. Verilmezse
	// [OutcomeAuthorize] varsayılır.
	DataKeyOutcome = "manual_outcome"
	// DataKeyDeclineReason reddin sebebini belirler; yalnızca
	// [OutcomeDecline] ile anlamlıdır.
	DataKeyDeclineReason = "manual_decline_reason"
	// DataKeyAuthorizedAmount KISMİ yetkilendirmeyi sınar: verilirse oturum
	// tutarı yerine bu tutar bloke edilir. Oturum tutarından büyük olamaz.
	DataKeyAuthorizedAmount = "manual_authorized_amount"
)

// Yetkilendirme sonuçları ([DataKeyOutcome] değerleri).
const (
	// OutcomeAuthorize tutarı bloke eder; varsayılan davranıştır.
	OutcomeAuthorize = "authorize"
	// OutcomeDecline yetkilendirmeyi REDDEDER: oturum "failed" olur ve
	// AuthResult ret sebebini taşır. Hata DÖNMEZ; ret, sağlayıcı açısından
	// başarılı bir yanıttır.
	OutcomeDecline = "decline"
	// OutcomeError sağlayıcının ERİŞİLEMEDİĞİNİ taklit eder: metot hata döner
	// ve oturumun durumu DEĞİŞMEZ. Saga'nın "adım patladı" dalını sınamak
	// içindir; [OutcomeDecline]'dan farkı, tekrar denenebilir olmasıdır.
	OutcomeError = "error"
)

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "payment_manual_invalid_input"
	// CodeInvalidState oturumun durumunda geçersiz bir geçiş denendiğini
	// bildirir.
	CodeInvalidState = "payment_manual_invalid_state"
	// CodeIdempotencyMismatch aynı anahtarın FARKLI bir gövdeyle yeniden
	// kullanıldığını bildirir.
	CodeIdempotencyMismatch = "payment_manual_idempotency_mismatch"
	// CodeSimulatedFailure test için enjekte edilmiş başarısızlığı bildirir.
	CodeSimulatedFailure = "payment_manual_simulated_failure"
	// CodeDataInvalid oturum verisinin çözümlenemediğini bildirir.
	CodeDataInvalid = "payment_manual_data_invalid"
)

// declineReasonDefault sebep verilmediğinde kullanılan ret gerekçesidir.
const declineReasonDefault = "manuel sağlayıcı reddetti (test)"

// Store sağlayıcının ihtiyaç duyduğu kalıcılık yüzeyidir.
//
// Arayüz TÜKETEN tarafta, yani burada tanımlıdır (ADR 0001'in örüntüsü).
// Sağlayıcı repository paketini import ETMEZ; somut depo bu imzaları yapısal
// olarak karşılar ve bağlantı module.go'da kurulur. Böylece sağlayıcının
// idempotency davranışı gerçek bir veritabanı olmadan, birkaç satırlık bir
// sahte depo ile sınanabilir.
//
// Kilit alan metot ([Store.LockManualSession]) yalnızca [Store.WithTx] içinde
// çağrılabilir: işlemsiz bir FOR UPDATE kilidi hiçbir şeyi korumaz.
type Store interface {
	// WithTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem geri alınır.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// InsertManualSessionIfAbsent oturumu yalnızca idempotency anahtarı henüz
	// kullanılmamışsa yazar. İkinci dönüş değeri satırın yazılıp
	// yazılmadığıdır; çakışma HATA DEĞİLDİR.
	InsertManualSessionIfAbsent(ctx context.Context, ses models.ManualSession) (models.ManualSession, bool, error)
	// ManualSessionByIdempotencyKey oturumu anahtarıyla döner; yoksa NotFound.
	ManualSessionByIdempotencyKey(ctx context.Context, key string) (models.ManualSession, error)
	// ManualSession oturumu kimliğiyle döner; yoksa NotFound.
	ManualSession(ctx context.Context, id string) (models.ManualSession, error)
	// LockManualSession oturumu işlem boyunca kilitler ve güncel hâlini döner.
	LockManualSession(ctx context.Context, id string) (models.ManualSession, error)
	// UpdateManualSessionState durumu ve tutarları MUTLAK değerlerle yazar.
	UpdateManualSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorized, captured, refunded int64,
		declineReason string,
	) (models.ManualSession, error)
}

// Provider manuel/test ödeme sağlayıcısıdır. Eşzamanlı kullanıma güvenlidir.
type Provider struct {
	store Store
	log   *slog.Logger
}

// Provider'ın çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır;
// imza kayması çalışma zamanına kalmaz.
var _ coreprovider.PaymentProvider = (*Provider)(nil)

// New verilen depo üzerinde çalışan bir manuel sağlayıcı üretir.
// log nil verilirse loglar atılır.
func New(store Store, log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Provider{store: store, log: log}
}

// ID sağlayıcının kimliğini döner.
func (p *Provider) ID() string { return ID }

// CreateSession sağlayıcının defterinde bir ödeme oturumu açar.
//
// Aynı IdempotencyKey ile ikinci çağrı YENİ oturum açmaz, mevcut oturumu döner
// (çekirdek sözleşmesinin şartı). Anahtar aynı ama tutar ya da para birimi
// FARKLIYSA errors.Conflict döner: idempotency "aynı isteği tekrarlamak"
// demektir, "farklı bir isteği eski anahtarla göndermek" değil — ikincisini
// sessizce kabul etmek, çağıranın gönderdiğini sandığı tutarın hiç
// uygulanmaması demek olurdu.
func (p *Provider) CreateSession(
	ctx context.Context,
	in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"the idempotency key is required")
	}
	reference := strings.TrimSpace(in.Reference)
	if reference == "" {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput, "reference zorunludur")
	}
	if in.Amount < models.MinAmount || in.Amount > models.MaxAmount {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"tutar %d ile %d arasında olmalı: %d", models.MinAmount, models.MaxAmount, in.Amount)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	if len(currency) != 3 {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"the currency has to be a three-letter ISO 4217 code: %q", in.CurrencyCode)
	}

	raw, err := json.Marshal(in.Data)
	if err != nil {
		return coreprovider.Session{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"oturum verisi kodlanamadı")
	}
	// Veri erken doğrulanır: bozuk bir davranış anahtarı, oturum açılırken
	// söylenmeli; yetkilendirme anında patlaması teşhisi zorlaştırırdı.
	if _, err := parseSessionData(raw); err != nil {
		return coreprovider.Session{}, err
	}

	created, inserted, err := p.store.InsertManualSessionIfAbsent(ctx, models.ManualSession{
		ID:             models.NewManualSessionID(),
		IdempotencyKey: key,
		Reference:      reference,
		Amount:         in.Amount,
		CurrencyCode:   currency,
		Status:         models.SessionPending,
		Data:           raw,
	})
	if err != nil {
		return coreprovider.Session{}, err
	}
	if inserted {
		return toProviderSession(created), nil
	}

	existing, err := p.store.ManualSessionByIdempotencyKey(ctx, key)
	if err != nil {
		return coreprovider.Session{}, err
	}
	if existing.Amount != in.Amount || existing.CurrencyCode != currency {
		return coreprovider.Session{}, errors.Conflict(CodeIdempotencyMismatch,
			"aynı idempotency anahtarı farklı bir tutarla kullanıldı: mevcut %d %s, istenen %d %s",
			existing.Amount, existing.CurrencyCode, in.Amount, currency)
	}
	p.log.DebugContext(ctx, "manuel sağlayıcı mevcut oturumu döndürdü",
		"oturum", existing.ID, "anahtar", key)
	return toProviderSession(existing), nil
}

// Authorize tutarı müşterinin üzerinde BLOKE eder; tahsilat yapmaz.
//
// Zaten sonlanmış bir oturum için hata DÖNMEZ, mevcut durumu döner (çekirdek
// sözleşmesinin şartı). Ret ([OutcomeDecline]) de hata değildir: sonuç
// SessionFailed durumuyla ve ret sebebiyle döner. Yalnızca [OutcomeError]
// gerçek bir hata üretir ve o hâlde oturumun durumu DEĞİŞMEZ.
func (p *Provider) Authorize(ctx context.Context, sessionID string) (coreprovider.AuthResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return coreprovider.AuthResult{}, errors.Invalid(CodeInvalidInput, "oturum kimliği zorunludur")
	}

	var out coreprovider.AuthResult
	err := p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}

		if ses.Status != models.SessionPending {
			// Sonlanmış oturum: mevcut durum olduğu gibi döner.
			out = coreprovider.AuthResult{
				Status:           coreprovider.SessionStatus(ses.Status),
				AuthorizedAmount: ses.AuthorizedAmount,
				Data:             ses.Data,
				DeclineReason:    ses.DeclineReason,
			}
			return nil
		}

		decision, err := decideAuthorize(ses)
		if err != nil {
			return err
		}

		updated, err := p.store.UpdateManualSessionState(ctx, ses.ID,
			decision.Status, decision.AuthorizedAmount, ses.CapturedAmount, ses.RefundedAmount,
			decision.DeclineReason)
		if err != nil {
			return err
		}

		out = coreprovider.AuthResult{
			Status:           coreprovider.SessionStatus(updated.Status),
			AuthorizedAmount: updated.AuthorizedAmount,
			Data:             updated.Data,
			DeclineReason:    updated.DeclineReason,
		}
		return nil
	})
	if err != nil {
		return coreprovider.AuthResult{}, err
	}
	return out, nil
}

// Capture bloke edilmiş tutarı tahsil eder. amount sıfırsa tamamı çekilir ve
// yetkilendirilen tutardan büyük OLAMAZ.
//
// Kısmi tahsilatta ÇEKİLMEYEN blokaj serbest bırakılır: defterdeki bloke tutar
// çekilen tutara iner. Oturum artık iptal edilemeyeceği için fark bırakılmazsa
// sonsuza kadar asılı kalırdı.
//
// Zaten tahsil edilmiş bir oturumda aynı tutarla (ya da sıfırla) yapılan ikinci
// çağrı hata DÖNMEZ; farklı bir tutar istenirse errors.Conflict döner, çünkü bu
// artık bir tekrar değil, yeni bir istektir.
func (p *Provider) Capture(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "oturum kimliği zorunludur")
	}
	if amount < 0 {
		return errors.Invalid(CodeInvalidInput, "tahsilat tutarı negatif olamaz: %d", amount)
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CaptureAction() {
		case models.ActionNoop:
			if amount != 0 && amount != ses.CapturedAmount {
				return errors.Conflict(CodeInvalidState,
					"oturum %d tutarıyla tahsil edilmiş; %d ile yeniden tahsil edilemez (%s)",
					ses.CapturedAmount, amount, sessionID)
			}
			p.log.DebugContext(ctx, "manuel sağlayıcı oturumu zaten tahsil edilmiş", "oturum", sessionID)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidState,
				"%q durumundaki oturum tahsil edilemez: %s", ses.Status, sessionID)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		captured := amount
		if captured == 0 {
			captured = ses.AuthorizedAmount
		}
		if captured > ses.AuthorizedAmount {
			return errors.Conflict(CodeInvalidState,
				"tahsilat tutarı yetkilendirilen tutarı aşamaz: istenen %d, bloke %d (%s)",
				captured, ses.AuthorizedAmount, sessionID)
		}

		// Çekilmeyen blokaj serbest bırakılır: defterdeki bloke tutar fiilen
		// çekilen tutara iner. Gerçek sağlayıcılar da tahsilatta kalan blokajı
		// bırakır; taklidin defteri modülün kaydıyla ayrışmamalıdır.
		_, err = p.store.UpdateManualSessionState(ctx, ses.ID,
			models.SessionCaptured, captured, captured, ses.RefundedAmount, ses.DeclineReason)
		return err
	})
}

// Refund tahsil edilmiş tutarı iade eder. amount sıfırsa KALAN tutarın tamamı
// iade edilir.
//
// Tamamı iade edilmiş bir oturumda amount sıfırla yapılan ikinci çağrı hata
// DÖNMEZ: kalan sıfırdır ve hiçbir şey yapılmaz. Böylece tam iade isteği
// güvenle yeniden denenebilir. Kalanı AŞAN açık bir tutar ise errors.Conflict
// üretir; o bir tekrar değil, olmayan parayı iade etme isteğidir.
func (p *Provider) Refund(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "oturum kimliği zorunludur")
	}
	if amount < 0 {
		return errors.Invalid(CodeInvalidInput, "iade tutarı negatif olamaz: %d", amount)
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if ses.Status != models.SessionCaptured {
			return errors.Conflict(CodeInvalidState,
				"%q durumundaki oturumdan iade yapılamaz: %s", ses.Status, sessionID)
		}

		remaining := ses.RefundableAmount()
		refund := amount
		if refund == 0 {
			if remaining == 0 {
				p.log.DebugContext(ctx, "manuel sağlayıcı oturumu zaten tamamen iade edilmiş",
					"oturum", sessionID)
				return nil
			}
			refund = remaining
		}
		if refund > remaining {
			return errors.Conflict(CodeInvalidState,
				"iade tutarı kalan tutarı aşamaz: istenen %d, kalan %d (%s)",
				refund, remaining, sessionID)
		}

		_, err = p.store.UpdateManualSessionState(ctx, ses.ID,
			ses.Status, ses.AuthorizedAmount, ses.CapturedAmount, ses.RefundedAmount+refund,
			ses.DeclineReason)
		return err
	})
}

// Cancel oturumu kapatır ve blokaj varsa serbest bırakır.
//
// SAGA TELAFİSİ BUDUR ve İDEMPOTENTTİR: zaten iptal edilmiş bir oturum için
// hata dönmez ve defterde ikinci kez değişiklik yapılmaz. Tahsil edilmiş bir
// oturum iptal EDİLEMEZ (errors.Conflict); para çekilmiştir ve geri almanın
// yolu iadedir.
//
// Bilinmeyen bir kimlik için errors.NotFound döner: idempotentlik "her şeyi
// sessizce yut" demek değildir. İki kez iptal edilen GERÇEK bir oturum ile hiç
// var olmamış bir kimlik farklı durumlardır ve ikincisi çağıran tarafta bir
// hatadır. Oturum kaydı silinmediği (yalnızca durumu değiştiği) için ilk durum
// her zaman ayırt edilebilir.
func (p *Provider) Cancel(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "oturum kimliği zorunludur")
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CancelAction() {
		case models.ActionNoop:
			p.log.DebugContext(ctx, "manuel sağlayıcı oturumu zaten iptal edilmiş", "oturum", sessionID)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidState,
				"%q durumundaki oturum iptal edilemez; iade kullanın: %s", ses.Status, sessionID)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		// Blokaj serbest bırakılır: bloke tutar sıfırlanır. Ret sebebi
		// KORUNUR; iptal edilen bir oturumun neden reddedildiği teşhis için
		// hâlâ okunabilir olmalıdır.
		_, err = p.store.UpdateManualSessionState(ctx, ses.ID,
			models.SessionCanceled, 0, ses.CapturedAmount, ses.RefundedAmount, ses.DeclineReason)
		return err
	})
}

// GetSession sağlayıcının defterindeki oturumu döner; yoksa errors.NotFound.
//
// Çekirdek sözleşmesinde YOKTUR ve payment servisi bunu ÇAĞIRMAZ. Yalnızca
// entegrasyon testleri ve teşhis içindir: bir oturumun sağlayıcı tarafındaki
// durumunu, modülün kendi kaydına bakmadan doğrulamak gerekir — iki defterin
// ayrıştığı bir hata ancak böyle görülebilir.
func (p *Provider) GetSession(ctx context.Context, sessionID string) (models.ManualSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return models.ManualSession{}, errors.Invalid(CodeInvalidInput, "oturum kimliği zorunludur")
	}
	return p.store.ManualSession(ctx, sessionID)
}

// authorizeDecision yetkilendirme kararının sonucudur.
type authorizeDecision struct {
	// Status oturumun yeni durumudur.
	Status models.SessionStatus
	// AuthorizedAmount bloke edilecek tutardır.
	AuthorizedAmount int64
	// DeclineReason yalnızca Status [models.SessionFailed] iken doludur.
	DeclineReason string
}

// sessionData sağlayıcının davranışını yönlendiren Data alanlarıdır.
//
// Tanınmayan alanlar YOK SAYILIR: Data, çağıranın sağlayıcıya ilettiği serbest
// veridir (kart tokenı, dönüş adresi) ve sağlayıcının anlamadığı bir alan hata
// değildir.
//
// AuthorizedAmount İŞARETÇİDİR: sıfır tutarlı bir yetkilendirme ile "alan hiç
// verilmedi" ayrımı korunmalıdır. Değer tipi kullanılsaydı, alanı hiç
// göndermeyen bir çağrı sıfır tutar bloke etmiş sayılırdı.
type sessionData struct {
	Outcome          string `json:"manual_outcome"`
	DeclineReason    string `json:"manual_decline_reason"`
	AuthorizedAmount *int64 `json:"manual_authorized_amount"`
}

// parseSessionData oturum verisindeki davranış anahtarlarını çözer.
func parseSessionData(raw []byte) (sessionData, error) {
	var out sessionData
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return sessionData{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"oturum verisi çözümlenemedi")
	}

	switch out.Outcome {
	case "", OutcomeAuthorize, OutcomeDecline, OutcomeError:
		return out, nil
	default:
		return sessionData{}, errors.Invalid(CodeInvalidInput,
			"%q tanınmayan bir %s değeri; %q, %q ya da %q olmalı",
			out.Outcome, DataKeyOutcome, OutcomeAuthorize, OutcomeDecline, OutcomeError)
	}
}

// decideAuthorize bekleyen bir oturumun yetkilendirme sonucunu belirler.
//
// Saf bir karardır: veritabanına dokunmaz, yalnızca oturumun kendisine ve
// saklanmış davranış anahtarlarına bakar. Ayrılığı bilinçlidir — enjekte
// edilmiş her başarısızlık dalı, veritabanı olmadan tek tek sınanabilir.
func decideAuthorize(ses models.ManualSession) (authorizeDecision, error) {
	data, err := parseSessionData(ses.Data)
	if err != nil {
		return authorizeDecision{}, err
	}

	switch data.Outcome {
	case OutcomeError:
		return authorizeDecision{}, errors.Unavailable(CodeSimulatedFailure,
			"manuel sağlayıcıya ulaşılamadı (test için enjekte edilmiş hata): %s", ses.ID)
	case OutcomeDecline:
		reason := strings.TrimSpace(data.DeclineReason)
		if reason == "" {
			reason = declineReasonDefault
		}
		return authorizeDecision{Status: models.SessionFailed, DeclineReason: reason}, nil
	}

	authorized := ses.Amount
	if data.AuthorizedAmount != nil {
		authorized = *data.AuthorizedAmount
		if authorized <= 0 || authorized > ses.Amount {
			return authorizeDecision{}, errors.Invalid(CodeInvalidInput,
				"%s 1 ile %d arasında olmalı: %d", DataKeyAuthorizedAmount, ses.Amount, authorized)
		}
	}
	return authorizeDecision{Status: models.SessionAuthorized, AuthorizedAmount: authorized}, nil
}

// toProviderSession defter kaydını çekirdek sözleşmesinin oturum tipine
// çevirir.
func toProviderSession(ses models.ManualSession) coreprovider.Session {
	return coreprovider.Session{
		ID:           ses.ID,
		Status:       coreprovider.SessionStatus(ses.Status),
		Amount:       ses.Amount,
		CurrencyCode: ses.CurrencyCode,
		Data:         ses.Data,
	}
}
