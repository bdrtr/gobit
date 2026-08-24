package models

// Bu dosya fulfillment modülünün DURUM MAKİNESİDİR.
//
// Geçişler burada, saf ve veritabanısız fonksiyonlar olarak durur; servis
// yalnızca sonucu tipli hataya çevirir. Ayrım bilinçlidir: bir geçişin geçerli
// olup olmadığı bir iş kuralıdır ve tablo hâlinde okunabilmeli, üç ayrı servis
// metodunun içine dağılmış if'lerden çıkarılmak zorunda kalınmamalıdır
// (payment modülündeki ayrımın aynısı).

// FulfillmentStatus bir gönderinin durumudur.
//
// Değerler internal/core/provider'daki provider.FulfillmentStatus ile BİREBİR
// aynıdır ama o paket burada yeniden kullanılmaz: sütun değeri modülün kendi
// şemasına aittir ve çekirdek sözleşmesi değiştiğinde veritabanındaki değerler
// sessizce değişmemelidir. Çeviri repository/servis sınırında yapılır.
type FulfillmentStatus string

// Gönderi durumları.
const (
	// StatusPending gönderi oluşturuldu, kargo firması henüz teslim almadı.
	StatusPending FulfillmentStatus = "pending"
	// StatusShipped kargo firması gönderiyi teslim aldı; yoldadır.
	StatusShipped FulfillmentStatus = "shipped"
	// StatusDelivered gönderi alıcıya ulaştı. GERİ DÖNÜŞSÜZDÜR.
	StatusDelivered FulfillmentStatus = "delivered"
	// StatusCanceled gönderi iptal edildi.
	StatusCanceled FulfillmentStatus = "canceled"
)

// String durumun metin karşılığını döner.
func (s FulfillmentStatus) String() string { return string(s) }

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s FulfillmentStatus) Valid() bool {
	switch s {
	case StatusPending, StatusShipped, StatusDelivered, StatusCanceled:
		return true
	default:
		return false
	}
}

// Action bir gönderi işleminin durum makinesindeki sonucudur.
//
// Sıfır değeri [ActionConflict]'tir; tanımsız bir durum kazara "devam et"
// olarak yorumlanmaz.
type Action uint8

// Gönderi işlemlerinin olası sonuçları.
const (
	// ActionConflict geçiş GEÇERSİZDİR; servis errors.Conflict döner.
	ActionConflict Action = iota
	// ActionProceed geçiş geçerlidir; işlem yapılır ve durum yazılır.
	ActionProceed
	// ActionNoop gönderi ZATEN hedef durumdadır; sağlayıcıya GİDİLMEZ ve hata
	// dönmez. İdempotentliği sağlayan daldır.
	ActionNoop
)

// String sonucun metin karşılığını döner.
func (a Action) String() string {
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

// CancelAction iptal isteğinin bu durumdaki sonucunu döner.
//
// İptal SAGA TELAFİSİDİR ve çekirdek sözleşmesi (internal/core/provider) gereği
// İDEMPOTENT olmak ZORUNDADIR; tablodaki tek conflict dalı geri alınamaz olandır.
//
// Geçiş tablosu:
//
//	pending   -> proceed   (etiket iptal edilir)
//	shipped   -> proceed   (kargo firması yoldaki gönderiyi GERİ ÇAĞIRABİLİR;
//	                        yapabilip yapamayacağının mercii sağlayıcıdır ve
//	                        yapamıyorsa Cancel hata döner. Burada kapatmak,
//	                        operatörü sistemin dışında çalışmaya zorlardı.)
//	delivered -> conflict  (teslim GERÇEKLEŞMİŞTİR; paket müşterinin elindedir
//	                        ve "iptal" fiziksel dünya hakkında yalan olurdu.
//	                        Çaresi İADEDİR: is_return işaretli bir kargo
//	                        seçeneğiyle yeni bir gönderi açılır. Kural,
//	                        payment'ta tahsil edilmiş bir oturumun iptal
//	                        edilemeyip iade edilmesiyle aynıdır.)
//	canceled  -> noop      (idempotentlik burada sağlanır)
func (s FulfillmentStatus) CancelAction() Action {
	switch s {
	case StatusPending, StatusShipped:
		return ActionProceed
	case StatusCanceled:
		return ActionNoop
	case StatusDelivered:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// ShipAction kargoya verme isteğinin bu durumdaki sonucunu döner.
//
// Geçiş tablosu:
//
//	pending   -> proceed   (kargo firması teslim aldı; shipped_at yazılır)
//	shipped   -> noop      (aynı gönderi İKİ KEZ yola çıkmaz)
//	delivered -> conflict  (teslim edilmiş gönderi geriye, "yolda"ya dönmez)
//	canceled  -> conflict  (iptal edilmiş gönderi yola çıkmaz)
func (s FulfillmentStatus) ShipAction() Action {
	switch s {
	case StatusPending:
		return ActionProceed
	case StatusShipped:
		return ActionNoop
	case StatusDelivered, StatusCanceled:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// DeliverAction teslim bildiriminin bu durumdaki sonucunu döner.
//
// Geçiş tablosu:
//
//	pending   -> conflict  (teslim alınmamış bir gönderi teslim EDİLEMEZ;
//	                        sırayı atlamak, shipped_at'i boş bırakır ve
//	                        mutabakatta gönderinin ne zaman yola çıktığı
//	                        cevapsız kalırdı)
//	shipped   -> proceed   (delivered_at yazılır)
//	delivered -> noop      (idempotentlik burada sağlanır)
//	canceled  -> conflict  (iptal edilmiş gönderi teslim edilmez)
func (s FulfillmentStatus) DeliverAction() Action {
	switch s {
	case StatusShipped:
		return ActionProceed
	case StatusDelivered:
		return ActionNoop
	case StatusPending, StatusCanceled:
		return ActionConflict
	default:
		return ActionConflict
	}
}
