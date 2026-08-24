// Package models payment modülünün alan (domain) modellerini içerir.
//
// Buradaki tipler veritabanı sürücüsünden bağımsızdır: pgtype ve sqlc üretimi
// tipler buraya SIZMAZ. Çeviri repository katmanında yapılır; servis, API ve
// testler yalnızca bu tipleri görür.
//
// Para her yerde TAM SAYI minor unit'tir (kuruş/cent) ve para birimi ayrı
// alanda durur (plan Bölüm 8); kayan nokta hiçbir alanda kullanılmaz. Zamanlar
// UTC'dir.
//
// # Neyi bilmez
//
// Bu modül bir ödemenin HANGİ sepete ya da siparişe ait olduğunu bilmez.
// [PaymentCollection.Reference] serbest bir metindir, foreign key DEĞİLDİR
// (Prensip 2.2) ve varlığı burada doğrulanmaz; bağ Module Links ile kurulur.
package models

import (
	"encoding/json"
	"time"
)

// Tutar sınırları.
//
// Sınırlar keyfi değildir: koleksiyonun tutarı, yetkilendirilen, tahsil edilen
// ve iade edilen tutarların hepsi aynı tavana tabidir ve toplamları int64'e
// SIĞMALIDIR. 4 × 10^12 < 9.22 × 10^18 olduğu için taşma yapısal olarak
// imkânsızdır. Aynı tavan cart ve pricing modüllerindekiyle bilinçli olarak
// aynıdır; modüller birbirini import etmediği için değer burada tekrarlanır
// (ADR 0001'in kabul edilen bedeli).
const (
	// MinAmount izin verilen en küçük tutardır.
	//
	// SIFIR DEĞİLDİR: tutarı sıfır olan bir sipariş için ödeme toplanmaz ve
	// açılan böyle bir koleksiyon hiçbir zaman "captured" olamaz — sonsuza
	// kadar ödeme bekleyen ölü bir kayıt olurdu.
	MinAmount int64 = 1
	// MaxAmount izin verilen en büyük tutardır (minor unit).
	MaxAmount int64 = 1_000_000_000_000
)

// PaymentCollection bir sepet ya da sipariş için toplanan ödemelerin kabıdır.
//
// # Tutarlar
//
// AuthorizedAmount, CapturedAmount ve RefundedAmount alt kayıtların
// toplamlarıdır ve koleksiyonun satır kilidi altında güncellenir. Status bu
// tutarlardan ve oturum sayımlarından TÜRETİLİR (bkz. [CollectionStatusFor]);
// sütun yalnızca sorgulanabilirlik içindir.
//
// Yeni bir oturumun kapabileceği KALAN tutar bu satırdan tek başına
// hesaplanamaz: henüz yetkilendirilmemiş açık oturumlar da tutar rezerve eder
// ve hiçbiri AuthorizedAmount'a girmez. Hesap, oturumları da gören servis
// katmanında ve koleksiyon kilidi altında yapılır.
type PaymentCollection struct {
	// ID "paycol_" önekli, zamana göre sıralanabilir kimliktir.
	ID string
	// Reference çağıranın kendi kaydının kimliğidir (sepet ya da sipariş).
	// FOREIGN KEY DEĞİLDİR (Prensip 2.2) ve bu modülde doğrulanmaz.
	Reference string
	// Amount toplanması gereken toplam tutardır (minor unit).
	Amount int64
	// CurrencyCode ISO 4217 kodudur ve daima BÜYÜK harf saklanır.
	CurrencyCode string
	// Status türetilmiş durumdur; bkz. [CollectionStatusFor].
	Status CollectionStatus
	// AuthorizedAmount HÂLÂ bloke olan toplam tutardır (minor unit).
	//
	// Kümülatif değildir: iptal edilen ya da tahsil edilen bir oturumun blokajı
	// buradan düşülür. Aksi hâlde aynı para hem bloke hem tahsil edilmiş
	// sayılır ve koleksiyon müşterinin üzerinde olmayan bir tutarı gösterirdi.
	AuthorizedAmount int64
	// CapturedAmount tahsil edilmiş toplam tutardır (minor unit).
	CapturedAmount int64
	// RefundedAmount iade edilmiş toplam tutardır (minor unit).
	RefundedAmount int64
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise koleksiyon canlıdır.
	DeletedAt *time.Time
}

// RefundableAmount koleksiyondan geri ödenebilecek tutarı döner.
func (c PaymentCollection) RefundableAmount() int64 {
	if c.RefundedAmount >= c.CapturedAmount {
		return 0
	}
	return c.CapturedAmount - c.RefundedAmount
}

// PaymentSession bir SAĞLAYICIDA açılmış ödeme oturumudur.
type PaymentSession struct {
	// ID "payses_" önekli modül kimliğidir.
	ID string
	// PaymentCollectionID oturumun bağlı olduğu koleksiyondur (modül içi FK).
	PaymentCollectionID string
	// ProviderID oturumu açan sağlayıcının kimliğidir (örn. "manual").
	ProviderID string
	// ExternalID sağlayıcı tarafındaki oturum kimliğidir; mutabakatta iki
	// sistemi eşleştiren alan budur.
	ExternalID string
	// Status oturumun güncel durumudur.
	Status SessionStatus
	// Amount oturumun tutarıdır (minor unit).
	Amount int64
	// AuthorizedAmount bloke edilen tutardır; kısmi yetkilendirmede Amount'tan
	// küçük olabilir.
	AuthorizedAmount int64
	// CurrencyCode ISO 4217 kodudur ve daima BÜYÜK harf saklanır.
	CurrencyCode string
	// Data sağlayıcının ham verisidir; olduğu gibi saklanır, yorumlanmaz.
	Data json.RawMessage
	// IdempotencyKey aynı oturumun iki kez açılmasını engeller.
	IdempotencyKey string
	// DeclineReason yalnızca Status [SessionFailed] iken doludur. Teşhis
	// içindir, müşteriye gösterilmek üzere DEĞİLDİR.
	DeclineReason string
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise oturum canlıdır.
	DeletedAt *time.Time
}

// Payment gerçekleşmiş bir tahsilattır.
//
// Bir oturumdan EN FAZLA BİR tahsilat doğar; kısmi tahsilat oturumu kapatır.
// Capture'ın idempotentliği buna dayanır.
type Payment struct {
	// ID "pay_" önekli kimliktir.
	ID string
	// PaymentSessionID tahsilatın çıktığı oturumdur.
	PaymentSessionID string
	// PaymentCollectionID tahsilatın ait olduğu koleksiyondur.
	PaymentCollectionID string
	// Amount tahsil edilen tutardır (minor unit).
	Amount int64
	// CurrencyCode ISO 4217 kodudur.
	CurrencyCode string
	// RefundedAmount bu tahsilattan iade edilmiş toplam tutardır.
	RefundedAmount int64
	// CapturedAt tahsilatın gerçekleştiği andır (UTC).
	CapturedAt time.Time
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise tahsilat canlıdır.
	DeletedAt *time.Time
}

// RefundableAmount tahsilattan geri ödenebilecek kalan tutarı döner.
func (p Payment) RefundableAmount() int64 {
	if p.RefundedAmount >= p.Amount {
		return 0
	}
	return p.Amount - p.RefundedAmount
}

// Refund bir tahsilatın geri ödenmesidir. Kısmi iade birden çok kayıt üretir.
type Refund struct {
	// ID "refund_" önekli kimliktir.
	ID string
	// PaymentID iadenin yapıldığı tahsilattır.
	PaymentID string
	// Amount iade edilen tutardır (minor unit); daima pozitiftir.
	Amount int64
	// Reason iadenin serbest metin sebebidir; isteğe bağlıdır.
	Reason string
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise iade canlıdır.
	DeletedAt *time.Time
}

// ManualSession manuel sağlayıcının KENDİ defterindeki oturumdur.
//
// Bu kayıt modülün alan verisi değildir; taklit edilen dış sistemin durumudur.
// payment servisi ona hiç dokunmaz, yalnızca manual sağlayıcı okur ve yazar
// (bkz. internal/modules/payment/manual).
type ManualSession struct {
	// ID "manses_" önekli SAĞLAYICI kimliğidir; modülün oturum kaydında
	// ExternalID olarak durur.
	ID string
	// IdempotencyKey aynı oturumun iki kez açılmasını engeller; sağlayıcının
	// defterinde TEKTİR.
	IdempotencyKey string
	// Reference çağıranın kendi kaydının kimliğidir (koleksiyon kimliği).
	Reference string
	// Amount oturumun tutarıdır (minor unit).
	Amount int64
	// CurrencyCode ISO 4217 kodudur.
	CurrencyCode string
	// Status oturumun sağlayıcı tarafındaki durumudur.
	Status SessionStatus
	// AuthorizedAmount, CapturedAmount ve RefundedAmount sağlayıcının
	// defterindeki tutarlardır (minor unit).
	AuthorizedAmount int64
	CapturedAmount   int64
	RefundedAmount   int64
	// Data oturum açılırken verilen serbest veridir. Manuel sağlayıcının
	// davranışını yönlendiren anahtarlar buradadır (bkz. manual paketi).
	Data json.RawMessage
	// DeclineReason yalnızca Status [SessionFailed] iken doludur.
	DeclineReason string
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RefundableAmount sağlayıcı defterinde geri ödenebilecek kalan tutarı döner.
func (m ManualSession) RefundableAmount() int64 {
	if m.RefundedAmount >= m.CapturedAmount {
		return 0
	}
	return m.CapturedAmount - m.RefundedAmount
}
