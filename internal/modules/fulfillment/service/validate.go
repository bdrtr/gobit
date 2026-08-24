package service

import (
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// currencyCodeLength ISO 4217 kodunun harf sayısıdır.
const currencyCodeLength = 3

// maxRuleValues tek bir kuralın alabileceği azami değer sayısıdır.
const maxRuleValues = 100

// requireText zorunlu bir metin alanını doğrular.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
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

// requireID bir kimliğin dolu olduğunu ve beklenen öneki taşıdığını doğrular.
//
// Önek denetimi ucuzdur ve yanlış varlığın kimliğiyle yapılan bir çağrıyı
// veritabanına hiç gitmeden yakalar: "sprof_..." ile bir gönderi aranması
// NotFound değil, doğrudan Invalid'dir ve teşhis edilebilir.
func requireID(value, prefix, label string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.Invalid(CodeInvalidInput, "%s boş olamaz", label)
	}
	if !strings.HasPrefix(trimmed, prefix) {
		return errors.Invalid(CodeInvalidInput,
			"%s %q önekiyle başlamalı: %q", label, prefix, value)
	}
	return checkTextLen(label, trimmed)
}

// normalizeCurrency para birimi kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Kod her yerde büyük harf saklanır; aksi hâlde "try" ve "TRY" iki farklı para
// birimi gibi davranır ve kargo ücreti sepetin para biriminden sessizce
// ayrışırdı.
func normalizeCurrency(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if len(normalized) != currencyCodeLength {
		return "", errors.Invalid(CodeInvalidInput,
			"para birimi üç harfli ISO 4217 kodu olmalı: %q", code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"para birimi yalnızca harf içerebilir: %q", code)
		}
	}
	return normalized, nil
}

// requireAmount bir kargo tutarının izin verilen aralıkta olduğunu doğrular.
//
// Üst sınır keyfi değildir: kargo ücreti sipariş toplamına eklenir ve toplamın
// int64'e sığması gerekir (bkz. [models.MaxAmount]). Sınırsız bir tutar,
// toplama sırasında sessizce negatife sarabilirdi.
func requireAmount(label string, amount int64) error {
	if amount < models.MinAmount || amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s %d ile %d arasında olmalı: %d", label, models.MinAmount, models.MaxAmount, amount)
	}
	return nil
}

// requireRange sayaç türünden bir alanın sıfır ile ustSinir arasında olduğunu
// doğrular.
//
// Üst sınır keyfi değildir: adet ve ağırlık kargo ücretiyle ÇARPILIR ve sınırsız
// bir değer, çarpımı int64'ten taşırıp negatif bir ücrete çevirebilirdi. Sınır
// burada, yani girdinin modüle girdiği yerde durur; sağlayıcının aritmetiği bu
// denetimden bağımsız olarak da savunmalıdır (bkz. manual paketi).
func requireRange(label string, value, ustSinir int64) error {
	if value < 0 || value > ustSinir {
		return errors.Invalid(CodeInvalidInput,
			"%s 0 ile %d arasında olmalı: %d", label, ustSinir, value)
	}
	return nil
}

// normalizeProfileType profil türünü doğrular; boş verilirse varsayılanı
// uygular.
func normalizeProfileType(value string) (models.ProfileType, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return models.ProfileDefault, nil
	}
	profileType := models.ProfileType(trimmed)
	if !profileType.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"%q tanınmayan bir kargo profili türü; geçerli olanlar: %s, %s, %s",
			value, models.ProfileDefault, models.ProfileGiftCard, models.ProfileCustom)
	}
	return profileType, nil
}

// normalizePriceType fiyat türünü doğrular; boş verilirse varsayılanı uygular.
func normalizePriceType(value string) (models.PriceType, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return models.PriceFlat, nil
	}
	priceType := models.PriceType(trimmed)
	if !priceType.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"%q tanınmayan bir fiyat türü; geçerli olanlar: %s, %s",
			value, models.PriceFlat, models.PriceCalculated)
	}
	return priceType, nil
}

// normalizeStatus gönderi durumunu doğrular.
func normalizeStatus(value string) (models.FulfillmentStatus, error) {
	status := models.FulfillmentStatus(strings.TrimSpace(value))
	if !status.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"%q tanınmayan bir gönderi durumu; geçerli olanlar: %s, %s, %s, %s",
			value, models.StatusPending, models.StatusShipped,
			models.StatusDelivered, models.StatusCanceled)
	}
	return status, nil
}

// validateRuleInput bir kural girdisini doğrular ve normalleştirilmiş
// değerlerini döner.
//
// Değerlerin sayısı işlece göre denetlenir: yalnızca "in" ve "nin" birden çok
// değer alır. Tek değer bekleyen bir işlece iki değer verilmesi sessizce
// yutulsaydı, ikinci değer hiçbir zaman değerlendirilmez ve yönetici koyduğunu
// sandığı koşulun çalışmadığını ancak siparişler yanlış aktığında görürdü.
func validateRuleInput(attribute, operator string, values []string) (models.RuleOperator, []string, error) {
	if err := requireText("kural alanı", attribute); err != nil {
		return "", nil, err
	}

	op := models.RuleOperator(strings.TrimSpace(operator))
	if !op.Valid() {
		return "", nil, errors.Invalid(CodeInvalidInput,
			"%q tanınmayan bir kural işleci; geçerli olanlar: eq, ne, in, nin, gt, gte, lt, lte",
			operator)
	}

	if len(values) == 0 {
		return "", nil, errors.Invalid(CodeInvalidInput, "kural en az bir değer içermeli")
	}
	if len(values) > maxRuleValues {
		return "", nil, errors.Invalid(CodeInvalidInput,
			"kural en fazla %d değer içerebilir: %d", maxRuleValues, len(values))
	}
	if !op.MultiValue() && len(values) != 1 {
		return "", nil, errors.Invalid(CodeInvalidInput,
			"%q işleci tek değer alır, %d değer verildi", op, len(values))
	}

	out := make([]string, 0, len(values))
	for i, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "", nil, errors.Invalid(CodeInvalidInput,
				"kural değeri boş olamaz (%d. değer)", i+1)
		}
		if err := checkTextLen("kural değeri", trimmed); err != nil {
			return "", nil, err
		}
		out = append(out, trimmed)
	}
	return op, out, nil
}
