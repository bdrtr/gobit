package models

import "time"

// DeliveryStatus bir teslim günlüğü kaydının durumudur.
type DeliveryStatus string

// Teslim durumları.
//
// Değerler migration'daki CHECK kısıtıyla BİREBİR aynıdır; buradaki bir
// yazım hatası, kaydı yazma anında kısıt ihlaline çevirir.
const (
	// DeliveryPending kayıt açıldı, sağlayıcıya HENÜZ gidilmedi.
	//
	// Kalıcı olarak bu durumda kalmış bir satır bir ARIZANIN kanıtıdır:
	// gönderim yapıldı ama sonucu yazılamadı (ya da süreç arada öldü). Böyle
	// bir satır "gitti mi" sorusuna cevap veremez ve elle incelenmelidir.
	DeliveryPending DeliveryStatus = "pending"
	// DeliverySent sağlayıcı bildirimi kabul etti.
	//
	// "Müşteriye ULAŞTI" demek DEĞİLDİR: sağlayıcı sözleşmesi (bkz.
	// internal/core/provider) yalnızca isteğin kabul edildiğini bildirir,
	// teslim durumu sorgulanmaz.
	DeliverySent DeliveryStatus = "sent"
	// DeliveryFailed sağlayıcı hata döndü; sebep Error alanındadır.
	//
	// Bildirimin GİTMEDİĞİ anlamına da gelmez: zaman aşımına uğrayan bir
	// istek karşı tarafta işlenmiş olabilir (çekirdek sözleşmesinin uyarısı).
	DeliveryFailed DeliveryStatus = "failed"
	// DeliverySkipped gönderilecek adres olmadığı için sağlayıcıya HİÇ
	// gidilmedi.
	//
	// Hata DEĞİLDİR: adressiz bir sipariş (örn. yönetim tarafından açılan)
	// geçerli bir kayıttır. Durumun ayrı adı olması, "adres yoktu" ile
	// "sağlayıcı reddetti"yi ayırır — ikisi farklı düzeltme gerektirir.
	DeliverySkipped DeliveryStatus = "skipped"
)

// String durumun metin karşılığını döner.
func (s DeliveryStatus) String() string { return string(s) }

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s DeliveryStatus) Valid() bool {
	switch s {
	case DeliveryPending, DeliverySent, DeliveryFailed, DeliverySkipped:
		return true
	default:
		return false
	}
}

// Delivery tek bir bildirim gönderim denemesinin günlük kaydıdır.
//
// # ALICI ADRESİ ALANI YOKTUR
//
// Kayıt kime gönderildiğini TAŞIMAZ; gerekçe migration dosyasındadır (özet:
// adres siparişte zaten durur, ikinci kopya silinmesi gereken yerlerin
// sayısını artırır). Kaydı siparişe bağlayan alan [Delivery.Reference]'tır.
type Delivery struct {
	// ID kaydın kimliğidir.
	ID string
	// Template gönderilen bildirimin şablonudur (örn. "order.placed").
	Template string
	// Channel gönderim kanalıdır ("email" | "sms").
	Channel string
	// Reference bildirimin bağlı olduğu kaydın kimliğidir (sipariş).
	// Serbest metindir, foreign key DEĞİLDİR (Prensip 2.2).
	Reference string
	// ProviderID gönderimi yapan sağlayıcının kimliğidir.
	ProviderID string
	// Status denemenin sonucudur.
	Status DeliveryStatus
	// Error yalnızca Status [DeliveryFailed] iken doludur; teşhis içindir.
	Error string
	// CreatedAt kaydın açıldığı, yani gönderimin DENENDİĞİ andır.
	CreatedAt time.Time
	// UpdatedAt sonucun yazıldığı andır.
	UpdatedAt time.Time
}

// DeliveryFilter teslim günlüğü listelemesinin süzgeç ve sayfalama
// parametreleridir.
//
// İşaretçi alanlar "verilmedi" ile "boş verildi" ayrımını korur: nil bir
// Reference süzgeç uygulanmadığı anlamına gelir. Değer tipi kullanılsaydı iki
// durum ayırt edilemezdi.
type DeliveryFilter struct {
	// Reference verilirse yalnızca o referansın kayıtları döner.
	Reference *string
	// Status verilirse yalnızca o durumdaki kayıtlar döner.
	Status *string
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}
