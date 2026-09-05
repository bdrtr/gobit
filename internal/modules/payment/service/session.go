package service

import (
	"context"
	"encoding/json"
	"strings"

	coreprovider "github.com/bdrtr/gobit/core/provider"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// fieldSessionID oturum kimliğinin alan adıdır; doğrulama mesajlarında ve hata
// ayrıntılarında AYNI ad kullanılır ki istemci iki farklı isim öğrenmesin.
const fieldSessionID = "payment_session_id"

// CreateSessionInput bir ödeme oturumu açma isteğidir.
type CreateSessionInput struct {
	// Amount bloke edilecek tutardır (minor unit). SIFIR verilirse
	// koleksiyonun KALAN tutarı kullanılır; sağlayıcı sözleşmesindeki
	// "sıfır = tamamı" kuralıyla aynı anlamdadır.
	//
	// Kalan tutar, koleksiyonun tutarından AÇIK oturumların rezerve ettiği
	// toplamın düşülmesiyle bulunur; bu alan yalnızca ödemeyi birden çok
	// oturuma BÖLMEK içindir ve toplamı hiçbir zaman koleksiyonu aşamaz.
	Amount int64
	// IdempotencyKey aynı oturumun iki kez açılmasını engeller; zorunludur.
	IdempotencyKey string
	// Data sağlayıcıya özgü serbest veridir (kart tokenı, dönüş adresi vb.).
	Data map[string]any
}

// CreateSession koleksiyon için bir sağlayıcıda ödeme oturumu açar.
//
// Aynı (sağlayıcı, IdempotencyKey) çifti ile ikinci çağrı YENİ oturum AÇMAZ;
// mevcut oturum döner ve sağlayıcıya hiç gidilmez (plan Bölüm 2.6). Anahtar
// aynı ama koleksiyon FARKLIYSA errors.Conflict döner: idempotency "aynı
// isteği tekrarlamak" demektir, "başka bir isteği eski anahtarla göndermek"
// değil.
//
// # Sonlanmış oturumun anahtarı yeniden KULLANILAMAZ
//
// Anahtarın oturumu iptal edilmiş ya da reddedilmişse errors.Conflict
// ([CodeSessionTerminal]) döner. Mevcut oturumu olduğu gibi dönmek, çağıranın
// bir sonraki adımda ("yetkilendir") anlaşılmaz bir geçiş çakışması almasına
// yol açardı: telafi bir kez çalıştıktan sonra AYNI anahtarla ilerlemenin yolu
// yoktur. Saga bir adımı yeniden denerken anahtarını yürütmeye göre üretir;
// telafiden SONRA yeniden denenen bir akış YENİ bir anahtar üretmek zorundadır
// ve bu hata kodu ona bunu söyler.
//
// Tahsilatı başlamış bir koleksiyona yeni oturum açılamaz (errors.Conflict);
// para çekilmişken ikinci bir ödeme yolu açmak, çift tahsilatın kapısıdır.
//
// # Kalan tutar AÇIK OTURUMLARI da sayar
//
// Açılacak tutar, koleksiyonun tutarından canlı oturumların rezerve ettiği
// toplamın düşülmesiyle bulunur (bkz. [Service.remainingToOpen]); tutar
// verilmezse kalanın tamamı için oturum açılır. Yalnızca yetkilendirilmiş
// tutara bakan bir hesap, aynı koleksiyonda her biri TAM tutarlı birden çok
// oturum açılmasına ve hepsi yetkilendirilince ÇİFT TAHSİLATA izin verirdi.
//
// Kilit sırası: koleksiyon. Sağlayıcı çağrısı bu kilit ALTINDA yapılır
// (gerekçe için paket belgesine bakın).
func (s *Service) CreateSession(
	ctx context.Context,
	collectionID, providerID string,
	in CreateSessionInput,
) (models.PaymentSession, error) {
	if err := requireText("payment_collection_id", collectionID); err != nil {
		return models.PaymentSession{}, err
	}
	if err := requireText("provider_id", providerID); err != nil {
		return models.PaymentSession{}, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if err := requireText("idempotency_key", key); err != nil {
		return models.PaymentSession{}, err
	}
	if err := requireOptionalAmount("amount", in.Amount); err != nil {
		return models.PaymentSession{}, err
	}

	prov, err := s.providers.Get(providerID)
	if err != nil {
		return models.PaymentSession{}, err
	}

	var out models.PaymentSession
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, err := s.store.LockPaymentCollection(ctx, collectionID)
		if err != nil {
			return err
		}

		existing, err := s.store.PaymentSessionByIdempotencyKey(ctx, prov.ID(), key)
		switch {
		case err == nil:
			if existing.PaymentCollectionID != collectionID {
				return errors.Conflict(CodeIdempotencyMismatch,
					"bu idempotency anahtarı %s koleksiyonu için kullanılmış: %s",
					existing.PaymentCollectionID, key)
			}
			if existing.Status.Terminal() {
				return errors.Conflict(CodeSessionTerminal,
					"bu idempotency anahtarının oturumu %q durumunda; yeni bir anahtar gerekir: %s",
					existing.Status, existing.ID).
					WithDetails(map[string]any{
						fieldSessionID: existing.ID,
						"status":       existing.Status.String(),
					})
			}
			s.log.DebugContext(ctx, "mevcut ödeme oturumu döndürüldü",
				"oturum", existing.ID, "anahtar", key)
			out = existing
			return nil
		case errors.HasKind(err, errors.KindNotFound):
			// Beklenen dal: anahtar ilk kez kullanılıyor.
		default:
			return err
		}

		if col.CapturedAmount > 0 {
			return errors.Conflict(CodeCollectionClosed,
				"tahsilatı başlamış koleksiyona yeni oturum açılamaz: %s", col.ID)
		}

		remaining, err := s.remainingToOpen(ctx, col)
		if err != nil {
			return err
		}
		if remaining <= 0 {
			return errors.Conflict(CodeCollectionClosed,
				"koleksiyonun tamamı açık oturumlarca kapatılmış, açılacak tutar kalmadı: %s", col.ID)
		}
		amount := in.Amount
		if amount == 0 {
			amount = remaining
		}
		if amount > remaining {
			return errors.Conflict(CodeInvalidTransition,
				"oturum tutarı kalan tutarı aşamaz: istenen %d, kalan %d (%s)",
				amount, remaining, col.ID)
		}

		session, err := prov.CreateSession(ctx, coreprovider.CreateSessionInput{
			Amount:       amount,
			CurrencyCode: col.CurrencyCode,
			// Reference sağlayıcı tarafında KOLEKSİYON kimliğidir; mutabakatta
			// iki sistemi eşleştiren alan budur.
			Reference:      col.ID,
			IdempotencyKey: key,
			Data:           in.Data,
		})
		if err != nil {
			return err
		}
		status, err := providerStatus(session.Status, prov.ID())
		if err != nil {
			return err
		}
		if strings.TrimSpace(session.ID) == "" {
			return errors.Internal(CodeProviderContract,
				"%q sağlayıcısı kimliksiz bir oturum döndü", prov.ID())
		}

		created, err := s.store.CreatePaymentSession(ctx, models.PaymentSession{
			ID:                  models.NewPaymentSessionID(),
			PaymentCollectionID: col.ID,
			ProviderID:          prov.ID(),
			ExternalID:          session.ID,
			Status:              status,
			Amount:              amount,
			CurrencyCode:        col.CurrencyCode,
			Data:                session.Data,
			IdempotencyKey:      key,
		})
		if err != nil {
			return err
		}

		// Oturum yazıldıktan SONRA türetilir: sayım yeni oturumu görmeli ve
		// koleksiyon "awaiting" olmalıdır.
		if _, err := s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount); err != nil {
			return err
		}

		out = created
		return nil
	})
	if err != nil {
		return models.PaymentSession{}, err
	}
	return out, nil
}

// remainingToOpen koleksiyonda yeni bir oturumun kapabileceği tutarı döner;
// hiç kalmadıysa 0.
//
// Hesap koleksiyonun tutarından, CANLI oturumların rezerve ettiği toplamı
// düşer. Yalnızca yetkilendirilmiş tutara bakmak yetmez: hiçbiri
// yetkilendirilmemişken aynı koleksiyona her biri TAM tutarlı iki oturum
// açılabilir, ikisi de yetkilendirilince koleksiyonun iki katı bloke edilir ve
// ikisi de tahsil edilince müşteriden iki kez para çekilirdi.
//
// İşlem İÇİNDE ve koleksiyonun kilidi ALTINDA çağrılmalıdır; kilitsiz okunan
// bir toplam, araya giren bir oturum açılışıyla bayatlar.
func (s *Service) remainingToOpen(ctx context.Context, col models.PaymentCollection) (int64, error) {
	reserved, err := s.store.LiveSessionAmount(ctx, col.ID)
	if err != nil {
		return 0, err
	}
	if reserved >= col.Amount {
		return 0, nil
	}
	return col.Amount - reserved, nil
}

// AuthorizePayment oturumun tutarını müşterinin üzerinde BLOKE eder.
//
// Geçiş tablosu için bkz. [models.SessionStatus.AuthorizeAction]. Zaten
// yetkilendirilmiş bir oturum için sağlayıcıya GİDİLMEZ ve hata dönmez; geçersiz
// bir geçiş (tahsil edilmiş, iptal edilmiş ya da reddedilmiş oturum)
// errors.Conflict döner.
//
// # Ret bir HATADIR
//
// Sağlayıcı reddederse oturum "failed" olarak KALICI yazılır ve metot
// errors.Conflict ([CodeAuthorizationDeclined]) döner. Ret bir sunucu hatası
// değildir ama çağıran açısından istenen geçiş GERÇEKLEŞMEMİŞTİR; sessizce
// başarı dönmek, durumu kontrol etmeyi unutan bir akışın ödenmemiş bir siparişi
// onaylaması demek olurdu. Ret sebebi hatanın Details alanında taşınır.
//
// Ret yazısı, hata dönülmeden ÖNCE işlenmiş olur: işlem başarıyla kapanır,
// hata işlemin dışında üretilir. Aksi hâlde geri alma reddi de silerdi ve
// oturum sonsuza kadar "pending" görünürdü.
//
// # Eşzamanlılık
//
// Kilit sırası: koleksiyon -> oturum. Aynı oturumu aynı anda yetkilendiren iki
// çağrıdan TAM OLARAK BİRİ sağlayıcıya gider; ikincisi birincinin yazdığı
// durumu görüp no-op'a düşer.
func (s *Service) AuthorizePayment(ctx context.Context, sessionID string) (models.PaymentSession, error) {
	if err := requireText(fieldSessionID, sessionID); err != nil {
		return models.PaymentSession{}, err
	}

	prov, err := s.providerForSession(ctx, sessionID)
	if err != nil {
		return models.PaymentSession{}, err
	}

	var (
		out      models.PaymentSession
		declined *models.PaymentSession
	)
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.AuthorizeAction() {
		case models.ActionNoop:
			s.log.DebugContext(ctx, "oturum zaten yetkilendirilmiş, işlem yapılmadı",
				"oturum", ses.ID)
			out = ses
			return nil
		case models.ActionConflict:
			return conflictTransition("yetkilendirilemez", ses)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		result, err := prov.Authorize(ctx, ses.ExternalID)
		if err != nil {
			return err
		}
		status, err := providerStatus(result.Status, ses.ProviderID)
		if err != nil {
			return err
		}

		switch status {
		case models.SessionAuthorized:
			authorized, err := authorizedAmount(result.AuthorizedAmount, ses)
			if err != nil {
				return err
			}
			updated, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
				models.SessionAuthorized, authorized, mergeData(ses.Data, result.Data), "")
			if err != nil {
				return err
			}
			if _, err := s.writeCollectionTotals(ctx, col,
				col.AuthorizedAmount+authorized, col.CapturedAmount, col.RefundedAmount); err != nil {
				return err
			}
			out = updated
			return nil

		case models.SessionFailed:
			updated, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
				models.SessionFailed, 0, mergeData(ses.Data, result.Data), result.DeclineReason)
			if err != nil {
				return err
			}
			if _, err := s.writeCollectionTotals(ctx, col,
				col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount); err != nil {
				return err
			}
			declined = &updated
			return nil

		default:
			return errors.Internal(CodeProviderContract,
				"%q sağlayıcısı yetkilendirmeden %q durumu döndü; beklenen %q ya da %q",
				ses.ProviderID, status, models.SessionAuthorized, models.SessionFailed)
		}
	})
	if err != nil {
		return models.PaymentSession{}, err
	}
	if declined != nil {
		return models.PaymentSession{}, declineError(*declined)
	}
	return out, nil
}

// CancelPayment oturumu kapatır ve blokaj varsa serbest bırakır.
//
// SAGA TELAFİSİ BUDUR ve İDEMPOTENTTİR: zaten iptal edilmiş bir oturum için
// hata dönmez, sağlayıcıya ikinci kez gidilmez ve koleksiyonun tutarına İKİNCİ
// KEZ dokunulmaz. Telafi adımı yeniden çalıştırılabilir olmak zorundadır — bir
// workflow yeniden denendiğinde ya da çift tetiklendiğinde ikinci çağrı akışı
// patlatmamalıdır.
//
// Bilinmeyen bir kimlik için errors.NotFound döner: idempotentlik "her şeyi
// sessizce yut" demek değildir. İki kez iptal edilen GERÇEK bir oturum ile hiç
// var olmamış bir kimlik farklı durumlardır ve ikincisi çağıran tarafta bir
// hatadır. Oturum kaydı silinmediği (yalnızca durumu değiştiği) için ilk durum
// her zaman ayırt edilebilir.
//
// Tahsil edilmiş bir oturum iptal EDİLEMEZ (errors.Conflict): para çekilmiştir
// ve geri almanın yolu [Service.RefundPayment]'tır. Reddedilmiş bir oturum ise
// iptal EDİLEBİLİR; kapatılır ve ret sebebi decline_reason'da korunur.
//
// Kilit sırası: koleksiyon -> oturum.
func (s *Service) CancelPayment(ctx context.Context, sessionID string) error {
	if err := requireText(fieldSessionID, sessionID); err != nil {
		return err
	}

	prov, err := s.providerForSession(ctx, sessionID)
	if err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CancelAction() {
		case models.ActionNoop:
			s.log.DebugContext(ctx, "oturum zaten iptal edilmiş, işlem yapılmadı",
				"oturum", ses.ID)
			return nil
		case models.ActionConflict:
			return conflictTransition("iptal edilemez; iade kullanın", ses)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		if err := prov.Cancel(ctx, ses.ExternalID); err != nil {
			return err
		}

		released := ses.AuthorizedAmount
		if released > col.AuthorizedAmount {
			return errors.Internal(CodeInconsistentState,
				"koleksiyonun bloke tutarı (%d) oturumunkinden (%d) küçük (%s)",
				col.AuthorizedAmount, released, ses.ID)
		}

		if _, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
			models.SessionCanceled, 0, ses.Data, ses.DeclineReason); err != nil {
			return err
		}
		_, err = s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount-released, col.CapturedAmount, col.RefundedAmount)
		return err
	})
}

// providerForSession oturumun sağlayıcısını çözer.
//
// Oturum işlem DIŞINDA, kilitsiz okunur: buradaki amaç yalnızca hangi
// sağlayıcının ve hangi koleksiyonun söz konusu olduğunu öğrenmektir. Karar
// verdiren okuma her zaman işlem içinde, kilit altında YENİDEN yapılır
// (bkz. [Service.lockCollectionAndSession]); bu yüzden araya giren bir
// değişiklik kararı bozamaz.
func (s *Service) providerForSession(ctx context.Context, sessionID string) (coreprovider.PaymentProvider, error) {
	ses, err := s.store.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.providers.Get(ses.ProviderID)
}

// lockCollectionAndSession kilitleri KANONİK sırada alır: önce koleksiyon,
// sonra oturum.
//
// Koleksiyon kimliği için oturumun kilitsiz bir okuması gerekir; sıranın ters
// dönmemesi için bu okuma kilit ALMADAN yapılır ve oturum, koleksiyon
// kilitlendikten sonra yeniden ve kilitli okunur.
func (s *Service) lockCollectionAndSession(
	ctx context.Context,
	sessionID string,
) (models.PaymentCollection, models.PaymentSession, error) {
	preview, err := s.store.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return models.PaymentCollection{}, models.PaymentSession{}, err
	}

	col, err := s.store.LockPaymentCollection(ctx, preview.PaymentCollectionID)
	if err != nil {
		return models.PaymentCollection{}, models.PaymentSession{}, err
	}

	ses, err := s.store.LockPaymentSession(ctx, sessionID)
	if err != nil {
		return models.PaymentCollection{}, models.PaymentSession{}, err
	}
	if ses.PaymentCollectionID != col.ID {
		// Oturum kilitlenene kadar başka bir koleksiyona taşınmış olamaz;
		// böyle bir sapma veri bozulmasıdır ve sessiz kalmamalıdır.
		return models.PaymentCollection{}, models.PaymentSession{}, errors.Internal(CodeInconsistentState,
			"oturum %s koleksiyonu kilitlendikten sonra %s koleksiyonunda bulundu",
			col.ID, ses.PaymentCollectionID)
	}
	return col, ses, nil
}

// providerStatus çekirdek sözleşmesinin durum değerini modülün durumuna
// çevirir ve tanınmayan bir değeri sözleşme ihlali olarak bildirir.
func providerStatus(status coreprovider.SessionStatus, providerID string) (models.SessionStatus, error) {
	converted := models.SessionStatus(status)
	if !converted.Valid() {
		return "", errors.Internal(CodeProviderContract,
			"%q sağlayıcısı tanınmayan bir oturum durumu döndü: %q", providerID, status)
	}
	return converted, nil
}

// authorizedAmount sağlayıcının bildirdiği bloke tutarı doğrular.
//
// Sıfır "tamamı" demektir; sözleşmenin Capture ve Refund için koyduğu kuralın
// aynısı burada da uygulanır ki alanı doldurmayan bir sağlayıcı, sıfır tutar
// bloke etmiş sayılmasın. Oturum tutarını AŞAN bir bloke ise sözleşme
// ihlalidir: müşteriden istenenden fazlası bloke edilmiş olurdu.
func authorizedAmount(reported int64, ses models.PaymentSession) (int64, error) {
	if reported < 0 {
		return 0, errors.Internal(CodeProviderContract,
			"%q sağlayıcısı negatif bloke tutarı döndü: %d (%s)",
			ses.ProviderID, reported, ses.ID)
	}
	if reported == 0 {
		return ses.Amount, nil
	}
	if reported > ses.Amount {
		return 0, errors.Internal(CodeProviderContract,
			"%q sağlayıcısı oturum tutarından fazlasını bloke etti: %d > %d (%s)",
			ses.ProviderID, reported, ses.Amount, ses.ID)
	}
	return reported, nil
}

// mergeData sağlayıcının döndürdüğü ham veriyi seçer; boşsa mevcut veri korunur.
//
// Sağlayıcı her çağrıda gövde döndürmek zorunda değildir; boş bir yanıtla
// mevcut veriyi silmek, oturumun açılışında saklanan bilgiyi (örn. istemcinin
// kullanacağı client_secret) kaybetmek olurdu.
func mergeData(current, incoming json.RawMessage) []byte {
	if len(incoming) == 0 {
		return current
	}
	return incoming
}

// conflictTransition geçersiz bir durum geçişi için ortak hatayı üretir.
func conflictTransition(action string, ses models.PaymentSession) error {
	return errors.Conflict(CodeInvalidTransition,
		"%q durumundaki ödeme oturumu %s: %s", ses.Status, action, ses.ID).
		WithDetails(map[string]any{
			fieldSessionID: ses.ID,
			"status":       ses.Status.String(),
		})
}

// declineError reddedilmiş bir yetkilendirme için ortak hatayı üretir.
func declineError(ses models.PaymentSession) error {
	return errors.Conflict(CodeAuthorizationDeclined,
		"ödeme reddedildi: %s (%s)", ses.DeclineReason, ses.ID).
		WithDetails(map[string]any{
			fieldSessionID:   ses.ID,
			"provider_id":    ses.ProviderID,
			"decline_reason": ses.DeclineReason,
		})
}
