// Package service cart modülünün iş mantığıdır.
//
// Modülün sorumluluğu tek cümleyle: bir sepetin NEYE sahip olduğunu bilmek —
// hangi bölgede, kimin adına, hangi satırlarla, hangi adresi ve kargo
// yöntemiyle. Sepetin NE KADAR TUTTUĞU bu modülün işi DEĞİLDİR.
//
// # Toplamlar neden burada hesaplanmaz
//
// Ara toplam fiyatı pricing'den, vergi ise region/tax'tan gelir; ikisini bir
// araya getiren akış birden çok modüle dokunur ve plan Bölüm 2.5 gereği
// WORKFLOW'a aittir (calculate_totals). ADR 0006 bu erişimin biçimini de
// belirler: workflow modülleri import etmez, dar arayüzü kendi paketinde
// tanımlar ve servisi container'dan adla çözer. Bu yüzden cart servisi hiçbir
// fiyat ya da vergi kaynağını ÇAĞIRMAZ; yalnızca [Service.SetTotals] ile
// gelen sonucu SAKLAR ve DOĞRULAR.
//
// Doğrulama, workflow'daki bir hesap hatasının sessizce veritabanına yazılmasını
// engellemek içindir ve üç kattır: servis (okunabilir hata), veritabanı CHECK
// kısıtı (son savunma) ve [models.Cart.TotalsStale] (bayatlığın görünürlüğü).
//
// # Eşzamanlılık
//
// Sepeti değiştiren HER akış tek bir veritabanı işleminde koşar ve işine
// sepetin satır kilidini alarak (SELECT ... FOR UPDATE) başlar. Bu, "önce oku
// sonra yaz" yarışını yapısal olarak imkânsız kılar: aynı sepete aynı anda iki
// satır eklemeye çalışan iki çağrıdan ikincisi, birincinin işlemi bitene kadar
// bekler ve READ COMMITTED altında sepetin GÜNCEL hâlini okur. Aynı varyant
// için yarışan iki ekleme bu yüzden iki satır üretmez; ikincisi birincinin
// açtığı satırı görüp adedini artırır.
//
// Kilit sırası tektir ve her akışta aynıdır: önce SEPET, sonra çocuk satırlar.
// Sıra akışa göre değişseydi iki akış aynı iki satırı ters sırada isteyip
// birbirini kilitler ve veritabanı işlemlerden birini öldürürdü.
//
// # Değişmezlik
//
// [models.Cart.CompletedAt] dolu bir sepet DEĞİŞTİRİLEMEZ: sipariş geçmişinin
// dayandığı kayıt odur. Yazan her metot kilit altında bunu kontrol eder ve
// errors.Conflict döner.
//
// # Modül izolasyonu
//
// Bu modül başka hiçbir modülü tanımaz (Prensip 2.1/2.4, ADR 0001). RegionID,
// CustomerID ve VariantID başka modüllerin kimlikleridir; serbest metin olarak
// saklanır, foreign key verilmez (Prensip 2.2) ve varlıkları bu modülde
// doğrulanmaz — doğrulama, o modülleri tanıyan workflow'un işidir.
package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// EntityName modülün Query katmanına sunduğu entity adıdır. Sağlayıcı
// container'a "<EntityName>.query" adıyla kaydedilir (ADR 0004).
const EntityName = "cart"

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "cart_invalid_input"
	// CodeCompleted tamamlanmış bir sepetin değiştirilmek istendiğini bildirir.
	CodeCompleted = "cart_completed"
	// CodeTotalsInconsistent yazılmak istenen toplamların kimliği sağlamadığını
	// bildirir (total = subtotal - discount + tax + shipping).
	CodeTotalsInconsistent = "cart_totals_inconsistent"
	// CodeTotalsStale toplamların sepetin güncel şekline ait olmadığını bildirir.
	CodeTotalsStale = "cart_totals_stale"
	// CodeCartEmpty satırsız bir sepetin tamamlanmak istendiğini bildirir.
	CodeCartEmpty = "cart_empty"
	// CodeCustomerMismatch sepetin başka bir müşteriye devredilmek istendiğini
	// bildirir.
	CodeCustomerMismatch = "cart_customer_mismatch"
	// CodeLineItemNotFound sepette olmayan bir satıra atıf yapıldığını bildirir.
	CodeLineItemNotFound = "cart_line_item_not_found"
	// CodeNotReady servisin eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "cart_service_not_ready"
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
// Bu kimlikler (region_id, customer_id, variant_id) carts_region_idx ve
// carts_customer_idx indekslerine girer. Sınırsız bir dize indeksi sepet
// başına keyfi büyüklükte şişirir ve süzme maliyetini tek bir isteğin
// belirlemesine izin verirdi.
const maxIDLen = 255

// Service cart modülünün dışa açık servisidir. Eşzamanlı kullanıma güvenlidir.
type Service struct {
	store Store
	log   *slog.Logger
}

// Options servisin bağımlılıklarıdır.
type Options struct {
	// Repo kalıcılık yüzeyidir; zorunludur.
	Repo Store
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// New verilen bağımlılıklarla bir servis üretir.
//
// Eksik bir bağımlılık KURULUM anında hata döner; çalışma zamanında nil
// kontrolü yapılmaz. Deposuz bir servis her çağrıda panik üretirdi ve bunun
// açılışta değil ilk istekte görünmesi için hiçbir sebep yoktur.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Internal(CodeNotReady, "cart servisi depo olmadan kurulamaz")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: opts.Repo, log: log}, nil
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

// mutate sepeti değiştiren akışların ORTAK ÇERÇEVESİDİR.
//
// Sırayla: tek işlem aç -> sepeti KİLİTLE -> tamamlanmışsa reddet -> işi yap ->
// sepetin şekil sayacını artır. Çerçevenin tek yerde olması iki şeyi garanti
// eder: (a) hiçbir yazma yolu kilidi ya da değişmezlik kontrolünü atlayamaz,
// (b) toplamların bayatladığı hiçbir yapısal değişiklikte damgalanmadan kalmaz.
//
// fn'e verilen ctx İŞLEMİ TAŞIR; içerideki her çağrı bu ctx ile yapılmalıdır,
// aksi hâlde o çağrı işlemin dışında kalır ve atomiklik sessizce kaybolur.
//
// Dönen sepet, sayaç artırıldıktan SONRAKİ hâldir: fn'e verilen kopya artık
// bayattır ve onu döndürmek çağırana bir eksik revision göstermek olurdu.
func (s *Service) mutate(ctx context.Context, cartID string, fn func(ctx context.Context, cart models.Cart) error) (models.Cart, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return models.Cart{}, err
	}

	var updated models.Cart
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		if err := fn(ctx, cart); err != nil {
			return err
		}
		updated, err = s.store.BumpCartRevision(ctx, cartID)
		return err
	})
	if err != nil {
		return models.Cart{}, err
	}
	return updated, nil
}

// completedError tamamlanmış sepete yazma denemesinin tipli hatasıdır.
func completedError(cartID string) error {
	return errors.Conflict(CodeCompleted,
		"sepet tamamlanmış ve değiştirilemez: %s", cartID)
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
	if strings.TrimSpace(code) == "" {
		return "", errors.Invalid(CodeInvalidInput, "currency_code boş olamaz")
	}
	return alphaCode("currency_code", "ISO 4217", code, 3)
}

// normalizeCountry ülke kodunu doğrular ve BÜYÜK harfe çevirir; boş kabul edilir.
func normalizeCountry(code string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", nil
	}
	return alphaCode("country_code", "ISO 3166-1 alpha-2", code, 2)
}

// alphaCode sabit uzunluklu bir harf kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Para birimi ile ülke kodu AYNI kuralı paylaşır ve ortak yardımcının sebebi
// tam olarak budur: kural iki yerde ayrı yazıldığında biri eksik kaldı —
// ülke kodunda yalnızca uzunluk sınanıyor, harf olup olmadığı sorulmuyordu ve
// "12" ya da "T1" gibi bir kod sepetin adresine girebiliyordu. Ülke kodu Faz
// 7'de vergi bölgesi ve kargo seçeneği eşlemesinin ANAHTARI olacağı için,
// biçimsiz bir kodun hatası sepetten çok sonra, eşleme aşamasında patlardı.
//
// Baş ve sondaki boşluk KIRPILIR, kod BÜYÜK harfe çevrilir. Harf dışı karakter
// ise düşürülmez, REDDEDİLİR: boşluk ve harf büyüklüğü aynı kodun yazım
// varyantlarıdır, ama "T1" gibi bir girdiyi "T"ye indirgeyip saklamak farkı
// ancak veri bozulduktan sonra görünür kılardı.
//
// [requireID] daha katıdır ve boşluklu kimliği de reddeder; kimlik dışarıdan
// gelen bir referanstır, kod ise kullanıcı girdisidir.
func alphaCode(label, standard, code string, length int) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if len(normalized) != length {
		return "", errors.Invalid(CodeInvalidInput,
			"%s %d harfli %s kodu olmalı: %q", label, length, standard, code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"%s yalnızca harf içerebilir: %q", label, code)
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
