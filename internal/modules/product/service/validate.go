package service

import (
	"math"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// Sayfalama sınırları. Store ve admin API'leri aynı sınırları paylaşır.
const (
	// DefaultLimit istemci limit vermediğinde kullanılan sayfa boyutudur.
	DefaultLimit = 20
	// MaxLimit tek istekte dönebilecek en fazla kayıt sayısıdır. Sınır
	// kırpılarak uygulanır (hata değil): istemcinin daha büyük bir sayfa
	// istemesi geçerli bir istektir, yalnızca karşılanmaz.
	MaxLimit = 100
)

// Alan uzunluğu sınırları. Veritabanı text sütunlarında sınır yoktur; sınırın
// tek yeri burasıdır ve amacı dev gövdelerin belleğe ve indekslere yayılmasını
// engellemektir.
const (
	maxHandleLen      = 128
	maxTitleLen       = 255
	maxValueLen       = 255
	maxURLLen         = 2048
	maxDescriptionLen = 16 * 1024
)

// int32From bir sıra/indeks değerini int32'ye GÜVENLE daraltır.
//
// Daraltma sessiz olsaydı 2^31'inci görselin sırası negatife dönerdi; böyle bir
// listede sıralama gözle görülür biçimde bozulur ama sebebi görünmez.
func int32From(n int) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
}

// Hata kodları; API tüketicisi errors.CodeOf ile bunlara bakabilir.
const (
	codeInvalidInput = "product_invalid_input"
	codeHandleTaken  = "product_handle_taken"
	codeNotFound     = "product_not_found"
	codeLinkFailed   = "product_link_failed"
	codeQueryFailed  = "product_query_failed"
	codeNotReady     = "product_service_not_ready"
)

// invalid doğrulama hatası üretir.
func invalid(format string, a ...any) error {
	return errors.Invalid(codeInvalidInput, format, a...)
}

// requireText zorunlu bir metin alanını doğrular ve kırpılmış hâlini döner.
//
// Kırpma bilinçlidir: dışarıdan gelen "  Tişört " ile "Tişört" aynı üründür ve
// baştaki boşluk yalnızca sıralamayı ve aramayı bozar. Kimlikler (link uçları)
// için kırpma YAPILMAZ; orada sessiz düzeltme veri kaymasıdır (bkz. core/link).
func requireText(field, value string, maxLen int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", invalid("%s zorunludur", field)
	}
	if len(trimmed) > maxLen {
		return "", invalid("%s en fazla %d karakter olabilir (verilen: %d)", field, maxLen, len(trimmed))
	}
	return trimmed, nil
}

// requireID zorunlu bir kimlik alanını doğrular.
//
// Kimlik KIRPILMAZ: baştaki/sondaki boşluk taşıyan bir kimlik, link tablosunda
// ve kendi tablosunda farklı satırlara denk gelirdi. Bozukluk sessizce
// düzeltilmek yerine reddedilir.
func requireID(field, value string) (string, error) {
	if value == "" {
		return "", invalid("%s zorunludur", field)
	}
	if strings.TrimSpace(value) != value {
		return "", invalid("%s baş/son boşluk içeremez", field)
	}
	if len(value) > maxHandleLen {
		return "", invalid("%s en fazla %d karakter olabilir", field, maxHandleLen)
	}
	return value, nil
}

// resolveHandle handle'ı doğrular; boş bırakılmışsa başlıktan üretir.
//
// Üretilen handle da UZUNLUK SINIRINA tabidir. Başlık maxTitleLen'e (255) kadar
// olabildiği için ondan üretilen slug maxHandleLen'i (128) rahatça aşar; aşan
// bir handle ürünü vitrinde ERİŞİLEMEZ kılardı, çünkü /store/v1/products/{handle}
// kimliği aynı 128 sınırıyla doğrular ve 422 döner — kayıt oluşur ama adresi hiç
// açılmaz.
//
// Üretilen slug bu yüzden KIRPILIR; istemcinin AÇIKÇA verdiği handle ise
// kırpılmaz, reddedilir. Ayrım bilinçlidir: üretim zaten bir kolaylıktır ve
// kısaltılması istemcinin gönderdiği bir değeri değiştirmez, ama verilmiş bir
// handle'ı sessizce kısaltmak istenen adresle kaydedilen adresi ayırırdı.
// Kırpma sonucu başka bir ürünle çakışırsa ensureHandleFree açık bir Conflict
// döner; sessiz bir üzerine yazma olmaz.
func resolveHandle(handle, title string) (string, error) {
	h := strings.TrimSpace(handle)
	if h != "" {
		return validateHandle(h)
	}

	generated := truncateHandle(slugify(title))
	if generated == "" {
		return "", invalid("handle boş bırakıldı ve başlıktan üretilemedi (%q)", title)
	}
	return validateHandle(generated)
}

// truncateHandle üretilen slug'ı azami handle uzunluğuna kırpar.
//
// Slug yalnızca ASCII harf, rakam ve tire içerir (bkz. slugify), bu yüzden bayt
// sınırında kırpmak bir rune'u ikiye bölemez. Kırpmadan artakalan sondaki tire
// atılır: handle tire ile bitemez ve bitseydi validateHandle üretilmiş bir
// değeri reddederdi.
func truncateHandle(slug string) string {
	if len(slug) <= maxHandleLen {
		return slug
	}
	return strings.TrimRight(slug[:maxHandleLen], "-")
}

// validateHandle handle biçimini doğrular.
//
// Kabul edilen biçim: küçük harf, rakam ve TEK tire ile ayrılmış parçalar.
// Handle bir URL parçasıdır; büyük harf ve boşluk kabul edilseydi aynı ürün
// iki farklı adresle görünür, benzersizlik indeksi de bunu yakalayamazdı
// ("Tisort" ile "tisort" farklı iki satır olurdu).
func validateHandle(handle string) (string, error) {
	if handle == "" {
		return "", invalid("handle zorunludur")
	}
	if len(handle) > maxHandleLen {
		return "", invalid("handle en fazla %d karakter olabilir (verilen: %d)", maxHandleLen, len(handle))
	}
	if handle != slugify(handle) {
		return "", invalid(
			"handle yalnızca küçük harf, rakam ve tire içerebilir; tire ile başlayamaz/bitemez (verilen: %q, önerilen: %q)",
			handle, slugify(handle))
	}
	return handle, nil
}

// slugify serbest metinden URL'de kullanılabilir bir handle üretir.
//
// Türkçe harfler ASCII karşılıklarına çevrilir: "Tişört" -> "tisort". Aksi
// hâlde handle ya UTF-8 kaçışlarıyla dolu bir URL üretir ya da harfler
// düşünce anlamsızlaşırdı ("tirt").
func slugify(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	lastDash := true // baştaki tireleri engeller
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			lastDash = false
		case turkishASCII[r] != 0:
			b.WriteRune(turkishASCII[r])
			lastDash = false
		case unicode.Is(unicode.Mn, r):
			// Birleşen işaret atılır ve AYIRICI ÜRETMEZ: "İ"nin küçük hâli
			// "i" + birleşen noktadır, ayırıcı sayılsaydı "İstanbul"
			// "i-stanbul" olurdu.
			continue
		case unicode.IsSpace(r), r == '-', r == '_', r == '.', r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			// Çevrilemeyen harf (örn. Kiril) atılır; kalan parçalar yine tek
			// tireyle ayrılır.
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// turkishASCII Türkçe harflerin ASCII karşılıklarıdır.
//
// strings.ToLower zaten uygulandığı için yalnızca küçük harfler yeterlidir.
// "İ" burada yoktur: küçük harfe çevrildiğinde "i" + birleşen nokta olur ve
// noktayı slugify'ın birleşen işaret dalı düşürür.
var turkishASCII = map[rune]rune{
	'ı': 'i', 'ş': 's', 'ğ': 'g', 'ü': 'u', 'ö': 'o', 'ç': 'c',
	'â': 'a', 'î': 'i', 'û': 'u',
}

// normalizeStatus ürün durumunu doğrular; boşsa taslak varsayar.
func normalizeStatus(status models.Status) (models.Status, error) {
	if status == "" {
		return models.StatusDraft, nil
	}
	if !status.Valid() {
		return "", invalid("geçersiz ürün durumu: %q (geçerli değerler: draft, published, archived)", status)
	}
	return status, nil
}

// normalizePaging sayfalama değerlerini doğrular ve sınırlara çeker.
//
// Negatif değer HATA'dır (istemci hatasıdır ve sessizce düzeltmek yanlış sayfa
// döndürürdü); limit 0 varsayılana, sınırı aşan limit MaxLimit'e çekilir.
func normalizePaging(limit, offset int) (outLimit, outOffset int, err error) {
	if limit < 0 {
		return 0, 0, invalid("limit negatif olamaz (verilen: %d)", limit)
	}
	if offset < 0 {
		return 0, 0, invalid("offset negatif olamaz (verilen: %d)", offset)
	}
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	return limit, offset, nil
}

// trimOptional isteğe bağlı bir metin alanını kırpar; boşaldıysa nil döner.
func trimOptional(v *string, field string, maxLen int) (*string, error) {
	if v == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil, nil
	}
	if len(trimmed) > maxLen {
		return nil, invalid("%s en fazla %d karakter olabilir (verilen: %d)", field, maxLen, len(trimmed))
	}
	return &trimmed, nil
}

// uniqueIDs kimlik dilimini doğrular, sırayı koruyarak tekilleştirir.
func uniqueIDs(field string, ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		clean, err := requireID(field, id)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out, nil
}
