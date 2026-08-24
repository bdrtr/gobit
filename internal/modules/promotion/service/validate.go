package service

import (
	"math"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// currencyCodeLen ISO 4217 alfabetik kodunun uzunluğudur.
const currencyCodeLen = 3

// maxIDLen kabul edilen kimlik uzunluğu üst sınırıdır. Kimlikler link
// tablosundaki benzersiz indekse de girdiği için sınır orayla uyumlu tutulur.
const maxIDLen = 255

// Kupon kodu sınırları.
//
// Kod hem müşterinin yazdığı hem operatörün andığı addır; bu yüzden hem insan
// yazabilecek kadar kısa hem de anlamlı olacak kadar uzun olmalıdır. İzin
// verilen karakterler harf, rakam, tire ve alt çizgidir: boşluk ve noktalama,
// kodu telefonda okurken ya da e-postaya yapıştırırken sessizce bozan tek
// şeydir.
const (
	// MinCodeLen kupon kodunun en kısa uzunluğudur.
	MinCodeLen = 3
	// MaxCodeLen kupon kodunun en uzun uzunluğudur.
	MaxCodeLen = 64
	// MaxCodesPerCompute tek bir hesapta verilebilecek azami kupon sayısıdır.
	//
	// Sınırın var olması şarttır: her kod veritabanı sorgusuna ve kural
	// değerlendirmesine girer, sınırsız bir liste tek istekle hesabı
	// meşgul ederdi.
	MaxCodesPerCompute = 20
)

// Metin alanı sınırları.
const (
	// MaxNameLen kampanya adının azami uzunluğudur.
	MaxNameLen = 255
	// MaxDescriptionLen açıklamanın azami uzunluğudur.
	MaxDescriptionLen = 2000
	// MaxIdentifierLen kampanya iş kimliğinin azami uzunluğudur.
	MaxIdentifierLen = 128
	// MaxReferenceLen kullanım referansının azami uzunluğudur.
	MaxReferenceLen = 255
	// MaxAttributeLen kural alan adının azami uzunluğudur.
	MaxAttributeLen = 128
	// MaxRuleValues bir kuralın azami değer sayısıdır.
	MaxRuleValues = 100
	// MaxMetadataKeys üstverinin azami anahtar sayısıdır.
	MaxMetadataKeys = 64
	// MaxMetadataValueLen üstveri değerinin azami uzunluğudur.
	MaxMetadataValueLen = 512
)

// normalizeCode kupon kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Büyük harfe çevirme bir SAKLAMA kararıdır: kupon kodları büyük/küçük harf
// ayrımı yapmamalıdır — "yaz20" yazan müşteriyle "YAZ20" yazan aynı kuponu
// kullanır. Ayrım korunsaydı, iki kod yalnızca harf durumuyla ayrışabilir ve
// müşteri yanlış olanı aldığını hiç anlamazdı.
func normalizeCode(code string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	if len(trimmed) < MinCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"kupon kodu en az %d karakter olmalı, %q verildi", MinCodeLen, code)
	}
	if len(trimmed) > MaxCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"kupon kodu en fazla %d karakter olabilir, %d karakter verildi", MaxCodeLen, len(trimmed))
	}
	for _, r := range trimmed {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", errors.Invalid(CodeInvalidInput,
				"kupon kodu yalnızca harf, rakam, tire ve alt çizgi içerebilir, %q verildi", code)
		}
	}
	return trimmed, nil
}

// normalizeCurrency para birimi kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Kabul edilen biçim ISO 4217 alfabetik kodudur: tam üç harf.
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
// Üst sınır taşma korumasıdır: hesabın en büyük ara çarpımı
// tutar × [models.BasisPointDenominator]'dır ve int64'e sığmalıdır.
func validateAmount(label string, amount int64) error {
	if amount < models.MinAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s negatif olamaz, %d verildi (minor unit)", label, amount)
	}
	if amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d olabilir (minor unit), %d verildi", label, models.MaxAmount, amount)
	}
	return nil
}

// validateQuantity adedin izin verilen aralıkta olduğunu doğrular.
func validateQuantity(label string, quantity int64) error {
	if quantity < models.MinQuantity {
		return errors.Invalid(CodeInvalidInput,
			"%s en az %d olmalı, %d verildi", label, models.MinQuantity, quantity)
	}
	if quantity > models.MaxQuantity {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d olabilir, %d verildi", label, models.MaxQuantity, quantity)
	}
	return nil
}

// validateUsageLimit kullanım sınırını doğrular; nil sınırsız demektir.
func validateUsageLimit(limit *int64) error {
	if limit == nil {
		return nil
	}
	if *limit < 0 {
		return errors.Invalid(CodeInvalidInput,
			"kullanım sınırı negatif olamaz, %d verildi", *limit)
	}
	return nil
}

// validateText bir metin alanının boş olmadığını ve sınırı aşmadığını doğrular.
func validateText(label, value string, minLen, maxLen int) error {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < minLen {
		return errors.Invalid(CodeInvalidInput, "%s boş olamaz", label)
	}
	if len(trimmed) > maxLen {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d bayt olabilir, %d bayt verildi", label, maxLen, len(trimmed))
	}
	return nil
}

// validateRuleInput bir kural girdisinin tutarlı olduğunu doğrular.
//
// Değer sayısı işlece bağlıdır: in/nin çok değer alır, diğerleri TEK değer
// ister. Sayısal işleçlerin (gt/gte/lt/lte) değeri tam sayıya çevrilebilmelidir;
// aksi hâlde kural hiçbir zaman eşleşmez ve sessizce ölü bir kayıt olurdu.
func validateRuleInput(in RuleInput) error {
	if !in.RuleType.Valid() {
		return errors.Invalid(CodeInvalidInput, "kural türü tanımsız: %q", string(in.RuleType))
	}
	if err := validateText("kural alan adı (attribute)", in.Attribute, 1, MaxAttributeLen); err != nil {
		return err
	}
	if !in.Operator.Valid() {
		return errors.Invalid(CodeInvalidInput, "kural işleci tanımsız: %q", string(in.Operator))
	}
	if len(in.Values) == 0 {
		return errors.Invalid(CodeInvalidInput, "%q kuralı en az bir değer içermeli", in.Attribute)
	}
	if len(in.Values) > MaxRuleValues {
		return errors.Invalid(CodeInvalidInput,
			"%q kuralı en fazla %d değer içerebilir, %d verildi", in.Attribute, MaxRuleValues, len(in.Values))
	}
	if !in.Operator.MultiValue() && len(in.Values) != 1 {
		return errors.Invalid(CodeInvalidInput,
			"%q işleci tam bir değer alır, %d değer verildi", string(in.Operator), len(in.Values))
	}
	for _, value := range in.Values {
		if value == "" {
			return errors.Invalid(CodeInvalidInput, "%q kuralının değerleri boş olamaz", in.Attribute)
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

// normalizeMetadata üstveriyi doğrular ve KOPYALAYARAK döner.
//
// Kopya şarttır: çağıranın haritası doğrudan modele konsaydı, isteği sonradan
// değiştiren bir çağıran yazılmış kaydı da değiştirmiş olurdu.
func normalizeMetadata(md map[string]string) (map[string]string, error) {
	if len(md) == 0 {
		return map[string]string{}, nil
	}
	if len(md) > MaxMetadataKeys {
		return nil, errors.Invalid(CodeInvalidInput,
			"üstveri en fazla %d anahtar içerebilir, %d verildi", MaxMetadataKeys, len(md))
	}

	out := make(map[string]string, len(md))
	for key, value := range md {
		if strings.TrimSpace(key) == "" {
			return nil, errors.Invalid(CodeInvalidInput, "üstveri anahtarı boş olamaz")
		}
		if len(key) > MaxAttributeLen {
			return nil, errors.Invalid(CodeInvalidInput,
				"üstveri anahtarı en fazla %d bayt olabilir, %q verildi", MaxAttributeLen, key)
		}
		if len(value) > MaxMetadataValueLen {
			return nil, errors.Invalid(CodeInvalidInput,
				"%q üstveri değeri en fazla %d bayt olabilir, %d bayt verildi",
				key, MaxMetadataValueLen, len(value))
		}
		out[key] = value
	}
	return out, nil
}

// requireID bir kimliğin kullanılabilir ve DOĞRU TÜRDE olduğunu doğrular.
//
// Önek kontrolü bilinçlidir: önekli kimliklerin varlık sebebi, yanlış türde bir
// kimliğin (örn. kampanya kimliğinin promosyon yerine geçmesi) "bulunamadı"
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
// Query katmanının ListOptions alanları int'tir; 64 bit bir platformda oradan
// gelen devasa bir değer int32'ye dönüşürken SARARDI ve negatif bir limit
// üretebilirdi. Sıkıştırma bu sarmayı imkânsız kılar; sınırın kendisi zaten
// normalizePaging'de [MaxLimit]'e indirilir.
func clampToInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}
