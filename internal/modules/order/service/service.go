// Package service order modülünün iş mantığıdır.
//
// Modülün sorumluluğu tek cümleyle: bir siparişin NE olduğunu kalıcı olarak
// bilmek — hangi numarayla, hangi bölgede, kimin adına, hangi satırlarla ve
// hangi tutarla verildiği. Sipariş yazıldıktan sonra tutarları ve satırları
// DEĞİŞMEZ; değişen tek şey durumu ve ona bağlı damgalardır.
//
// # Neyi bilmez
//
// Sipariş sepeti BİLMEZ ve cart modülünü import ETMEZ (Prensip 2.1/2.4,
// ADR 0001). [Service.CreateOrder]'a verilen girdi sepetin ANLIK
// GÖRÜNTÜSÜDÜR: satırlar ve toplamlar çağıran tarafından, hesaplanmış hâlde
// getirilir. Sepeti okuyup burada bırakan taraf complete_cart WORKFLOW'udur
// (plan Bölüm 2.5, ADR 0006).
//
// Ödemeyi de bilmez: tahsil edilen ve iade edilen tutar [models.OrderSummary]
// üzerinden, ödeme sonucunu bilen akış tarafından yazılır.
//
// # Toplamlar neden burada da doğrulanır
//
// Toplamı hesaplayan taraf başkası olsa da, YANLIŞ bir hesabın sessizce
// siparişe yazılması bu modülün sorunudur: sipariş, tutarın kalıcı kaydıdır ve
// yanlış yazılmış bir tutar sonradan düzeltilemez (kayıt değişmez). Bu yüzden
// doğrulama üç kattır: servis (okunabilir hata), veritabanı CHECK kısıtı (son
// savunma) ve satır/sipariş ara toplamının birbirini tutması.
//
// # Eşzamanlılık
//
// Siparişin DURUMUNU değiştiren her akış tek bir veritabanı işleminde koşar ve
// işine siparişin satır kilidini alarak (SELECT ... FOR UPDATE) başlar. Bu,
// "önce oku sonra yaz" yarışını yapısal olarak imkânsız kılar: aynı siparişi
// aynı anda iptal etmeye ve tamamlamaya çalışan iki çağrıdan ikincisi,
// birincinin işlemi bitene kadar bekler ve siparişin GÜNCEL durumunu okur.
//
// Sipariş NUMARASI (display_id) ise kilitle değil, veritabanının IDENTITY
// sütunuyla üretilir: yeni açılan iki sipariş için kilitlenecek ORTAK bir satır
// yoktur ve uygulama katmanındaki her "en büyüğü oku, bir ekle" çözümü
// yarışırdı (bkz. migration yorumu).
//
// # Modül izolasyonu
//
// Bu modül başka hiçbir modülü tanımaz. RegionID, CustomerID, CartID ve
// VariantID başka modüllerin kimlikleridir; serbest metin olarak saklanır,
// foreign key verilmez (Prensip 2.2) ve varlıkları burada doğrulanmaz —
// doğrulama, o modülleri tanıyan workflow'un işidir.
package service

import (
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EntityName modülün Query katmanına sunduğu entity adıdır. Sağlayıcı
// container'a "<EntityName>.query" adıyla kaydedilir (ADR 0004).
const EntityName = "order"

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "order_invalid_input"
	// CodeTotalsInconsistent yazılmak istenen toplamların kimliği sağlamadığını
	// bildirir (total = subtotal - discount + tax + shipping).
	CodeTotalsInconsistent = "order_totals_inconsistent"
	// CodeOrderEmpty satırsız bir siparişin açılmak istendiğini bildirir.
	CodeOrderEmpty = "order_empty"
	// CodeNotPending bekleyen durumda OLMAYAN bir siparişte durum geçişi
	// istendiğini bildirir.
	CodeNotPending = "order_not_pending"
	// CodeNotCompleted tamamlanmamış bir siparişin arşivlenmek istendiğini
	// bildirir.
	CodeNotCompleted = "order_not_completed"
	// CodeDisplayIDInvalid siparişin kullanılabilir bir numara almadığını
	// bildirir.
	CodeDisplayIDInvalid = "order_display_id_invalid"
	// CodeInconsistentState kaydın tanımsız bir durumda olduğunu bildirir.
	CodeInconsistentState = "order_inconsistent_state"
	// CodeSummaryInvalid özet tutarlarının kabul edilemez olduğunu bildirir.
	CodeSummaryInvalid = "order_summary_invalid"
	// CodeRefundExceedsOrder iade/hasar kaydının tutarının siparişin toplamını
	// aştığını bildirir.
	CodeRefundExceedsOrder = "order_refund_exceeds_total"
	// CodeSpendingLimitExceeded siparişin, müşterinin dönem içindeki harcama
	// limitini aştığını bildirir.
	CodeSpendingLimitExceeded = "order_spending_limit_exceeded"
	// CodeSpendingCurrencyMismatch siparişin para biriminin harcama limitinin
	// para biriminden farklı olduğunu bildirir; iki tutar çevrilmeden
	// karşılaştırılamaz.
	CodeSpendingCurrencyMismatch = "order_spending_currency_mismatch"
	// CodeSpendingPolicyUnavailable harcama kuralının OKUNAMADIĞINI bildirir.
	// "Kural yok" ile "kuralı öğrenemedik" farklı durumlardır; ikincisinde
	// sipariş açılmaz.
	CodeSpendingPolicyUnavailable = "order_spending_policy_unavailable"
	// CodeSpendingPolicyInvalid harcama kuralı gövdesinin sözleşmeye
	// uymadığını bildirir.
	CodeSpendingPolicyInvalid = "order_spending_policy_invalid"
	// CodeNotReady servisin eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "order_service_not_ready"
)

// Sayfalama sınırları (plan Bölüm 8: limit/offset).
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int64 = 50
	// MaxLimit tek istekte istenebilecek en büyük sayfa boyutudur.
	MaxLimit int64 = 100
)

// maxTextLen serbest metin alanları için üst sınırdır. Sınır, tek bir isteğin
// veritabanına sınırsız büyüklükte metin yazmasını engeller.
const maxTextLen = 512

// maxIDLen dışarıdan gelen kimlikler için üst sınırdır.
//
// Bu kimlikler (region_id, customer_id, cart_id, variant_id) orders_region_idx
// ve orders_customer_idx indekslerine girer. Sınırsız bir dize indeksi sipariş
// başına keyfi büyüklükte şişirir ve süzme maliyetini tek bir isteğin
// belirlemesine izin verirdi.
const maxIDLen = 255

// maxOrderItems tek bir siparişin taşıyabileceği azami satır sayısıdır.
//
// Sınır, tek bir isteğin tek işlemde binlerce INSERT açmasını engeller: satırlar
// aynı işlemde yazıldığı için sınırsız bir liste, işlemi ve onun tuttuğu
// bağlantıyı keyfi süre boyunca meşgul ederdi.
const maxOrderItems = 500

// Service order modülünün dışa açık servisidir. Eşzamanlı kullanıma güvenlidir.
type Service struct {
	store    Store
	events   EventPublisher
	spending SpendingPolicy
	log      *slog.Logger
}

// Options servisin bağımlılıklarıdır.
type Options struct {
	// Repo kalıcılık yüzeyidir; zorunludur.
	Repo Store
	// Events olay veri yoludur; zorunludur. "order.placed" olayı buradan
	// yayımlanır (plan Faz 6 DoD).
	Events EventPublisher
	// Spending harcama limiti kuralının kaynağıdır; OPSİYONELDİR.
	//
	// nil ise hiçbir limit uygulanmaz ve sipariş açma yolu bu alan hiç
	// eklenmemiş gibi davranır: ne ek bir okuma ne de bir kilit vardır. Saf
	// B2C bir kurulumda "harcama limiti" diye bir kavram olmadığı için doğru
	// varsayılan budur; alanı dolduran taraf modülün kablolamasıdır
	// (bkz. module.go).
	Spending SpendingPolicy
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// New verilen bağımlılıklarla bir servis üretir.
//
// Eksik bir bağımlılık KURULUM anında hata döner; çalışma zamanında nil
// kontrolü yapılmaz. Olay veri yolunun da zorunlu olması bilinçlidir: opsiyonel
// olsaydı, veri yolu kaydı unutulmuş bir kurulumda sipariş sessizce yazılır ama
// "order.placed" hiç yayımlanmazdı ve eksiklik ancak abonelerin çalışmadığı
// fark edildiğinde — yani üretimde — görünürdü.
//
// [Options.Spending] bu kuralın TEK istisnasıdır ve olmak zorundadır: harcama
// limiti B2B'ye özgü bir kavramdır, saf B2C bir kurulumda kuralın kaynağı diye
// bir şey yoktur ve onu zorunlu kılmak, b2b modülü olmayan her kurulumu
// açılışta düşürürdü.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Internal(CodeNotReady, "order servisi depo olmadan kurulamaz")
	}
	if opts.Events == nil {
		return nil, errors.Internal(CodeNotReady, "order servisi olay veri yolu olmadan kurulamaz")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		store:    opts.Repo,
		events:   opts.Events,
		spending: opts.Spending,
		log:      log,
	}, nil
}

// Page liste isteklerinin sayfalama parametreleridir.
type Page struct {
	// Limit döndürülecek azami satır sayısıdır; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// normalize sayfalama parametrelerini doğrular ve varsayılanları uygular.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "limit negatif olamaz: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "offset negatif olamaz: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"limit en fazla %d olabilir: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// requireID dışarıdan gelen bir kimliğin kullanılabilir olduğunu doğrular.
//
// Kimlik KIRPILMAZ, reddedilir: kırpma çağıranın gönderdiği kimlikle saklanan
// kimliği ayırır ve fark ancak veri bozulduktan sonra görünür. Aynı gerekçe
// core/link'in kimlik sözleşmesinde de geçerlidir.
func requireID(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s boş olamaz", label)
	}
	if strings.TrimSpace(value) != value {
		return errors.Invalid(CodeInvalidInput, "%s baş/son boşluk içeremez: %q", label, value)
	}
	if len(value) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d bayt olabilir: %d", label, maxIDLen, len(value))
	}
	return nil
}

// optionalID boş bırakılabilen bir kimliği doğrular.
func optionalID(label, value string) error {
	if value == "" {
		return nil
	}
	return requireID(label, value)
}

// requireText zorunlu bir metin alanını doğrular.
func requireText(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s boş olamaz", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen metin alanının uzunluk sınırını doğrular.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d bayt olabilir: %d", label, maxTextLen, len(value))
	}
	return nil
}

// checkAmount bir tutarın izin verilen aralıkta olduğunu doğrular.
//
// Üst sınır keyfi değildir: taşmayı yapısal olarak imkânsız kılar
// (bkz. [models.MaxAmount] ve [models.MaxTotal]).
func checkAmount(label string, value, upper int64) error {
	if value < models.MinAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s negatif olamaz: %d", label, value)
	}
	if value > upper {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d olabilir: %d", label, upper, value)
	}
	return nil
}

// checkQuantity bir adedin izin verilen aralıkta olduğunu doğrular.
func checkQuantity(quantity int64) error {
	if quantity < models.MinQuantity {
		return errors.Invalid(CodeInvalidInput,
			"adet en az %d olmalı: %d", models.MinQuantity, quantity)
	}
	if quantity > models.MaxQuantity {
		return errors.Invalid(CodeInvalidInput,
			"adet en fazla %d olabilir: %d", models.MaxQuantity, quantity)
	}
	return nil
}

// normalizeCurrency para birimi kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Kod saklanmadan önce tekleştirilir: "try" ile "TRY" aynı para birimidir ve
// iki ayrı dize olarak saklanırsa toplamların karşılaştırılması sessizce
// yanlış sonuç verirdi.
func normalizeCurrency(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if len(normalized) != 3 {
		return "", errors.Invalid(CodeInvalidInput,
			"currency_code 3 harfli ISO 4217 kodu olmalı: %q", code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"currency_code yalnızca harf içerebilir: %q", code)
		}
	}
	return normalized, nil
}

// normalizeEmail e-postayı doğrular ve küçük harfe çevirir; boş kabul edilir.
//
// Doğrulama BİLİNÇLİ OLARAK yüzeyseldir: tam RFC 5322 doğrulaması geçerli
// adresleri reddetmesiyle ünlüdür ve adresin gerçekten teslim edilebilir olup
// olmadığını yalnızca gönderim söyleyebilir. Burada aranan tek şey, alanın
// e-posta olarak KULLANILABİLİR biçimde olmasıdır.
func normalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return "", nil
	}
	if err := checkTextLen("email", normalized); err != nil {
		return "", err
	}
	at := strings.IndexByte(normalized, '@')
	if at <= 0 || at == len(normalized)-1 || strings.ContainsAny(normalized, " \t\n") {
		return "", errors.Invalid(CodeInvalidInput, "email geçerli görünmüyor: %q", email)
	}
	if strings.Count(normalized, "@") != 1 {
		return "", errors.Invalid(CodeInvalidInput, "email geçerli görünmüyor: %q", email)
	}
	return normalized, nil
}

// multiplyAmount birim fiyat ile adedi TAŞMADAN çarpar.
//
// Çarpanlar servis doğrulamasından geçtiğinde sonuç zaten [models.MaxTotal]
// altındadır; kontrol, anormal bir adet ya da fiyatla gelen bir çağrıya karşı
// son savunmadır. Taşan bir çarpım sessizce negatif bir ara toplam üretir ve
// tutarlılık kontrolünü YANLIŞLIKLA geçebilirdi.
func multiplyAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity < 0 || unitPrice < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"birim fiyat ve adet negatif olamaz: %d × %d", unitPrice, quantity)
	}
	if quantity > models.MaxTotal/unitPrice {
		return 0, errors.Invalid(CodeInvalidInput,
			"satır ara toplamı sınırı aşıyor: %d × %d > %d", unitPrice, quantity, models.MaxTotal)
	}
	return unitPrice * quantity, nil
}

// addAmount iki tutarı TAŞMADAN toplar.
func addAmount(sum, value int64) (int64, error) {
	if value < 0 {
		return 0, errors.Invalid(CodeTotalsInconsistent, "tutar negatif olamaz: %d", value)
	}
	if sum > models.MaxTotal-value {
		return 0, errors.Invalid(CodeTotalsInconsistent,
			"tutarların toplamı sınırı aşıyor (%d)", models.MaxTotal)
	}
	return sum + value, nil
}
