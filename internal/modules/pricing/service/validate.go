package service

import (
	"math"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// currencyCodeLen ISO 4217 alfabetik kodunun uzunluğudur.
const currencyCodeLen = 3

// maxIDLen kabul edilen kimlik uzunluğu üst sınırıdır. Kimlikler link
// tablosundaki benzersiz indekse de girdiği için sınır orayla uyumlu tutulur.
const maxIDLen = 255

// normalizeCurrency para birimi kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Kabul edilen biçim ISO 4217 alfabetik kodudur: tam üç harf. Baştaki/sondaki
// boşluklar kırpılır (kod zaten büyük harfe dönüştürülerek normalleştiriliyor;
// boşluk için ayrı bir katılık tutarsız olurdu), ama harf dışı hiçbir karakter
// kabul edilmez.
func normalizeCurrency(code string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	if len(trimmed) != currencyCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"para birimi kodu tam %d harf olmalı (ISO 4217), %q verildi", currencyCodeLen, code)
	}
	for _, r := range trimmed {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"para birimi kodu yalnızca harf içerebilir (ISO 4217), %q verildi", code)
		}
	}
	return trimmed, nil
}

// validateAmount tutarın izin verilen aralıkta olduğunu doğrular.
//
// Negatif tutar reddedilir: negatif fiyat bir indirim değildir, indirim
// promotion modülünün işidir. Üst sınır ise taşma korumasıdır — tutar × adet
// çarpımı int64'e sığmalıdır (bkz. [models.MaxAmount]).
func validateAmount(amount int64) error {
	if amount < models.MinAmount {
		return errors.Invalid(CodeInvalidInput,
			"tutar negatif olamaz, %d verildi (minor unit)", amount)
	}
	if amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"tutar en fazla %d olabilir (minor unit), %d verildi", models.MaxAmount, amount)
	}
	return nil
}

// normalizeQuantityRange adet aralığını doğrular ve varsayılanı uygular.
//
// min 0 verilirse 1 kabul edilir: "adet belirtilmedi" ile "her adette geçerli"
// aynı şeydir. Dönen üst sınır KOPYALANIR; çağıranın işaretçisi paylaşılmaz.
func normalizeQuantityRange(minQty int32, maxQty *int32) (outMin int32, outMax *int32, err error) {
	if minQty == 0 {
		minQty = models.MinQuantity
	}
	if minQty < models.MinQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"asgari adet en az %d olmalı, %d verildi", models.MinQuantity, minQty)
	}
	if minQty > models.MaxQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"asgari adet en fazla %d olabilir, %d verildi", models.MaxQuantity, minQty)
	}
	if maxQty == nil {
		return minQty, nil, nil
	}

	limit := *maxQty
	if limit < models.MinQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"azami adet en az %d olmalı, %d verildi", models.MinQuantity, limit)
	}
	if limit > models.MaxQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"azami adet en fazla %d olabilir, %d verildi", models.MaxQuantity, limit)
	}
	if limit < minQty {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"azami adet (%d) asgari adetten (%d) küçük olamaz", limit, minQty)
	}
	return minQty, &limit, nil
}

// validatePriceListRef fiyata verilen liste kimliğini doğrular.
func validatePriceListRef(id *string) error {
	if id == nil {
		return nil
	}
	return requireID(*id, models.PriceListIDPrefix, "fiyat listesi kimliği")
}

// validateRule bir kural girdisinin tutarlı olduğunu doğrular.
//
// Değer sayısı işlece bağlıdır: in/nin çok değer alır, diğerleri TEK değer
// ister. Sayısal işleçlerin (gt/gte/lt/lte) değeri tam sayıya çevrilebilmelidir;
// aksi hâlde kural hiçbir zaman eşleşmez ve sessizce ölü bir kayıt olurdu.
func validateRule(in RuleInput) error {
	if strings.TrimSpace(in.Attribute) == "" {
		return errors.Invalid(CodeInvalidInput, "kural alan adı (attribute) boş olamaz")
	}
	if !in.Operator.Valid() {
		return errors.Invalid(CodeInvalidInput,
			"kural işleci tanımsız: %q", string(in.Operator))
	}
	if len(in.Values) == 0 {
		return errors.Invalid(CodeInvalidInput,
			"%q kuralı en az bir değer içermeli", in.Attribute)
	}
	if !in.Operator.MultiValue() && len(in.Values) != 1 {
		return errors.Invalid(CodeInvalidInput,
			"%q işleci tam bir değer alır, %d değer verildi", string(in.Operator), len(in.Values))
	}
	for _, value := range in.Values {
		if value == "" {
			return errors.Invalid(CodeInvalidInput,
				"%q kuralının değerleri boş olamaz", in.Attribute)
		}
		if in.Operator.Numeric() {
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				return errors.Invalid(CodeInvalidInput,
					"%q işleci tam sayı bekler, %q verildi", string(in.Operator), value)
			}
		}
	}
	return nil
}

// requireID bir kimliğin kullanılabilir ve DOĞRU TÜRDE olduğunu doğrular.
//
// Önek kontrolü bilinçlidir: önekli kimliklerin varlık sebebi, yanlış türde bir
// kimliğin (örn. varyant kimliğinin price set yerine geçmesi) "bulunamadı"
// olarak değil, ne olduğu belli bir doğrulama hatası olarak dönmesidir.
func requireID(id, prefix, label string) error {
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "%s boş olamaz", label)
	}
	if strings.TrimSpace(id) != id {
		return errors.Invalid(CodeInvalidInput, "%s baş/son boşluk içeremez: %q", label, id)
	}
	if len(id) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d bayt olabilir, %d bayt verildi", label, maxIDLen, len(id))
	}
	if !strings.HasPrefix(id, prefix) {
		return errors.Invalid(CodeInvalidInput,
			"%s %q önekiyle başlamalı, %q verildi", label, prefix, id)
	}
	return nil
}

// normalizePaging sayfalama parametrelerini uygulanabilir değerlere çevirir.
//
// Limit 0 veya negatifse varsayılan, [MaxLimit]'i aşıyorsa azami değer
// uygulanır; kırpma hata DEĞİLDİR ama uygulanan değer sonuçta geri bildirilir
// (bkz. [Page]). Negatif offset ise düzeltilemez bir istektir ve reddedilir.
func normalizePaging(limit, offset int32) (outLimit, outOffset int32, err error) {
	if offset < 0 {
		return 0, 0, errors.Invalid(CodeInvalidInput, "offset negatif olamaz, %d verildi", offset)
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return limit, offset, nil
}

// clampToInt32 bir int değeri int32 aralığına sıkıştırır.
//
// Query katmanının [query.ListOptions] alanları int'tir; 64 bit bir platformda
// oradan gelen devasa bir değer int32'ye dönüşürken SARARDI ve negatif bir
// limit üretebilirdi. Sıkıştırma bu sarmayı imkânsız kılar; sınırın kendisi
// zaten normalizePaging'de [MaxLimit]'e indirilir.
func clampToInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}

// Hata ayrıntısındaki indeks anahtarları.
const (
	// detailIndex kaçıncı FİYATIN reddedildiğini bildirir.
	detailIndex = "index"
	// detailRuleIndex o fiyatın kaçıncı KURALININ reddedildiğini bildirir.
	detailRuleIndex = "rule_index"
)

// withIndex bir doğrulama hatasına kaçıncı girdide oluştuğunu ekler.
//
// Toplu yazmada (SetPrices) hangi fiyatın reddedildiğini bilmek, hatayı
// kullanılabilir kılan tek bilgidir.
//
// Anahtar çağırandan gelir çünkü indeksler İÇ İÇEDİR: bir kural hatası hem
// fiyatın hem kuralın sırasını taşır. İki seviye aynı anahtarı kullansaydı
// [errors.Error.WithDetails] onu EZER ve dıştaki fiyat indeksi içteki kural
// indeksini yok ederdi; istemci "prices[0].rules[3] geçersiz" durumunda yalnızca
// index=0 görüp hatayı fiyatın kendisinde arardı.
func withIndex(err error, key string, index int) error {
	var typed *errors.Error
	if errors.As(err, &typed) && typed != nil {
		return typed.WithDetails(map[string]any{key: index})
	}
	return err
}
