package models

// Bu dosya payment modülünün DURUM MAKİNESİDİR.
//
// Geçişler burada, saf ve veritabanısız fonksiyonlar olarak durur; servis
// yalnızca sonucu tipli hataya çevirir. Ayrım bilinçlidir: bir geçişin geçerli
// olup olmadığı bir iş kuralıdır ve tablo hâlinde okunabilmeli, üç ayrı servis
// metodunun içine dağılmış if'lerden çıkarılmak zorunda kalınmamalıdır.

// SessionStatus bir ödeme oturumunun durumudur.
//
// Değerler internal/core/provider'daki [provider.SessionStatus] ile BİREBİR
// aynıdır ama o paket burada yeniden kullanılmaz: sütun değeri modülün kendi
// şemasına aittir ve çekirdek sözleşmesi değiştiğinde veritabanındaki değerler
// sessizce değişmemelidir. Çeviri repository/servis sınırında yapılır.
type SessionStatus string

// Ödeme oturumu durumları.
const (
	// SessionPending oturum açıldı, henüz yetkilendirilmedi.
	SessionPending SessionStatus = "pending"
	// SessionAuthorized tutar müşterinin üzerinde BLOKE edildi; çekilmedi.
	SessionAuthorized SessionStatus = "authorized"
	// SessionCaptured tutar tahsil edildi; oturumdan bir Payment doğdu.
	SessionCaptured SessionStatus = "captured"
	// SessionCanceled oturum kapatıldı; blokaj varsa serbest bırakıldı.
	SessionCanceled SessionStatus = "canceled"
	// SessionFailed sağlayıcı yetkilendirmeyi reddetti; sebep decline_reason'dadır.
	SessionFailed SessionStatus = "failed"
)

// String durumun metin karşılığını döner.
func (s SessionStatus) String() string { return string(s) }

// Terminal oturumun GERİ DÖNÜŞSÜZ biçimde kapandığını bildirir: iptal edilmiş
// ya da reddedilmiş bir oturum yeniden yetkilendirilemez ve ondan tahsilat
// çıkmaz (bkz. [SessionStatus.AuthorizeAction]).
//
// [SessionCaptured] terminal SAYILMAZ: tahsil edilmiş oturum akışın başarıyla
// tamamlanmış hâlidir ve aynı isteği tekrarlayan bir çağrı ondan mevcut
// tahsilatı okuyabilir. Ayrım, aynı idempotency anahtarıyla yapılan bir
// tekrarın hangi durumda ilerleyebileceğini belirler.
func (s SessionStatus) Terminal() bool {
	return s == SessionCanceled || s == SessionFailed
}

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s SessionStatus) Valid() bool {
	switch s {
	case SessionPending, SessionAuthorized, SessionCaptured, SessionCanceled, SessionFailed:
		return true
	default:
		return false
	}
}

// SessionAction bir oturum işleminin durum makinesindeki sonucudur.
//
// Sıfır değeri [ActionConflict]'tir; tanımsız bir durum kazara "devam et"
// olarak yorumlanmaz.
type SessionAction uint8

// Oturum işlemlerinin olası sonuçları.
const (
	// ActionConflict geçiş GEÇERSİZDİR; servis errors.Conflict döner.
	ActionConflict SessionAction = iota
	// ActionProceed geçiş geçerlidir; sağlayıcıya gidilir ve durum yazılır.
	ActionProceed
	// ActionNoop oturum ZATEN hedef durumdadır; sağlayıcıya GİDİLMEZ ve hata
	// dönmez. İdempotentliği sağlayan daldır.
	ActionNoop
)

// String sonucun metin karşılığını döner.
func (a SessionAction) String() string {
	switch a {
	case ActionProceed:
		return "proceed"
	case ActionNoop:
		return "noop"
	case ActionConflict:
		return "conflict"
	default:
		return "conflict"
	}
}

// AuthorizeAction yetkilendirme isteğinin bu durumdaki sonucunu döner.
//
// Geçiş tablosu (bkz. dosya başındaki not):
//
//	pending    -> proceed   (sağlayıcıya gidilir; authorized ya da failed olur)
//	authorized -> noop      (zaten bloke; İKİNCİ KEZ bloke edilmez)
//	captured   -> conflict  (tahsil edilmiş tutar yeniden yetkilendirilemez)
//	canceled   -> conflict  (kapatılmış oturum yeniden açılmaz)
//	failed     -> conflict  (ret sağlayıcı tarafında NİHAİDİR; yeni oturum açılır)
func (s SessionStatus) AuthorizeAction() SessionAction {
	switch s {
	case SessionPending:
		return ActionProceed
	case SessionAuthorized:
		return ActionNoop
	case SessionCaptured, SessionCanceled, SessionFailed:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// CaptureAction tahsilat isteğinin bu durumdaki sonucunu döner.
//
// Geçiş tablosu:
//
//	pending    -> conflict  (önce yetkilendirme gerekir)
//	authorized -> proceed   (blokaj çekilir; Payment kaydı doğar)
//	captured   -> noop      (aynı oturumdan İKİNCİ tahsilat çıkmaz)
//	canceled   -> conflict
//	failed     -> conflict
func (s SessionStatus) CaptureAction() SessionAction {
	switch s {
	case SessionAuthorized:
		return ActionProceed
	case SessionCaptured:
		return ActionNoop
	case SessionPending, SessionCanceled, SessionFailed:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// CancelAction iptal isteğinin bu durumdaki sonucunu döner.
//
// İptal SAGA TELAFİSİDİR ve idempotent olmak ZORUNDADIR; tablodaki tek
// conflict dalı geri alınamaz olandır.
//
// Geçiş tablosu:
//
//	pending    -> proceed   (açık oturum kapatılır)
//	authorized -> proceed   (blokaj serbest bırakılır)
//	captured   -> conflict  (para çekilmiştir; geri almanın yolu İADEDİR)
//	canceled   -> noop      (idempotentlik burada sağlanır)
//	failed     -> proceed   (oturum kapatılır; ret sebebi decline_reason'da KORUNUR)
func (s SessionStatus) CancelAction() SessionAction {
	switch s {
	case SessionPending, SessionAuthorized, SessionFailed:
		return ActionProceed
	case SessionCanceled:
		return ActionNoop
	case SessionCaptured:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// CollectionStatus bir ödeme koleksiyonunun durumudur.
type CollectionStatus string

// Ödeme koleksiyonu durumları.
const (
	// CollectionNotPaid henüz açık bir oturum ve tahsilat yoktur.
	CollectionNotPaid CollectionStatus = "not_paid"
	// CollectionAwaiting en az bir oturum açıktır; ödeme beklenmektedir.
	CollectionAwaiting CollectionStatus = "awaiting"
	// CollectionAuthorized koleksiyonun TAMAMI bloke edilmiştir.
	CollectionAuthorized CollectionStatus = "authorized"
	// CollectionPartiallyCaptured tahsilat yapılmıştır ama koleksiyonun
	// tutarını KARŞILAMAMAKTADIR; ödeme eksiktir.
	CollectionPartiallyCaptured CollectionStatus = "partially_captured"
	// CollectionCaptured koleksiyonun TAMAMI tahsil edilmiştir.
	CollectionCaptured CollectionStatus = "captured"
	// CollectionPartiallyRefunded tahsilatın bir kısmı iade edilmiştir.
	CollectionPartiallyRefunded CollectionStatus = "partially_refunded"
	// CollectionRefunded tahsil edilen tutarın TAMAMI iade edilmiştir.
	CollectionRefunded CollectionStatus = "refunded"
	// CollectionCanceled oturumlar iptal edilmiştir ve tahsilat yoktur.
	CollectionCanceled CollectionStatus = "canceled"
)

// String durumun metin karşılığını döner.
func (c CollectionStatus) String() string { return string(c) }

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (c CollectionStatus) Valid() bool {
	switch c {
	case CollectionNotPaid, CollectionAwaiting, CollectionAuthorized,
		CollectionPartiallyCaptured, CollectionCaptured,
		CollectionPartiallyRefunded, CollectionRefunded, CollectionCanceled:
		return true
	default:
		return false
	}
}

// SessionCounts bir koleksiyonun oturumlarının duruma göre sayımıdır.
type SessionCounts struct {
	// Live açık oturumlardır (pending ya da authorized).
	Live int64
	// Canceled iptal edilmiş oturumlardır.
	Canceled int64
	// Failed sağlayıcının reddettiği oturumlardır.
	Failed int64
	// Total silinmemiş TÜM oturumlardır.
	Total int64
}

// CollectionStatusFor koleksiyonun durumunu tutarlarından ve oturum
// sayımlarından TÜRETİR.
//
// Durum sütunda saklanır ama gerçeğin kaynağı bu fonksiyondur: her mutasyondan
// sonra yeniden hesaplanıp yazılır. Alternatif — her akışta durumu elle atamak —
// aynı kuralın beş yere dağılması ve bir dalın unutulması demekti; koleksiyonun
// durumu ile tutarları ayrışırsa mutabakat imkânsız hâle gelir.
//
// Sıra ANLAMLIDIR ve paradan oturuma doğrudur; para her zaman sayımı yener:
//
//  1. Tahsilat varsa önce iade durumuna bakılır: tamamı iade edildiyse
//     refunded, bir kısmı iade edildiyse partially_refunded.
//  2. İade yoksa tahsilatın koleksiyonu KARŞILAYIP karşılamadığına bakılır:
//     tamamı tahsil edildiyse captured, eksikse partially_captured. Eksik
//     tahsilata "captured" demek, 50.000'lik bir koleksiyondan 1 birim
//     çekildiğinde siparişi ödenmiş saymak demekti; kuralın aynısı aşağıdaki
//     yetkilendirme dalında da geçerlidir.
//  3. Bloke edilen tutar koleksiyonun TAMAMINI karşılıyorsa authorized.
//     Kısmi yetkilendirme yetmez; eksik bloke edilmiş bir koleksiyon hâlâ
//     ödeme beklemektedir.
//  4. Açık oturum varsa awaiting.
//  5. İptal edilmiş oturum varsa canceled. Saga telafisinin izi budur.
//  6. Aksi hâlde not_paid. Yalnızca REDDEDİLMİŞ oturumu olan koleksiyon da
//     buraya düşer: ret nihai değildir, müşteri yeni bir oturumla tekrar
//     deneyebilir ve "canceled" demek o kapıyı yanlış biçimde kapatırdı.
func CollectionStatusFor(c PaymentCollection, counts SessionCounts) CollectionStatus {
	if c.CapturedAmount > 0 {
		switch {
		case c.RefundedAmount >= c.CapturedAmount:
			return CollectionRefunded
		case c.RefundedAmount > 0:
			return CollectionPartiallyRefunded
		case c.CapturedAmount >= c.Amount:
			return CollectionCaptured
		default:
			return CollectionPartiallyCaptured
		}
	}
	if c.AuthorizedAmount > 0 && c.AuthorizedAmount >= c.Amount {
		return CollectionAuthorized
	}
	if counts.Live > 0 {
		return CollectionAwaiting
	}
	if counts.Canceled > 0 {
		return CollectionCanceled
	}
	return CollectionNotPaid
}
