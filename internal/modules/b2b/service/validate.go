package service

import (
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// countryCodeLen ISO 3166-1 alpha-2 kodunun uzunluğudur.
const countryCodeLen = 2

// currencyCodeLen ISO 4217 kodunun uzunluğudur.
const currencyCodeLen = 3

// maxIDLen kabul edilen kimlik uzunluğu üst sınırıdır. Kimlikler link
// tablosundaki benzersiz indekse de girdiği için sınır orayla uyumlu tutulur.
const maxIDLen = 255

// normalizeEmail e-postayı doğrular ve saklama biçimine çevirir.
//
// Doğrulama BİLİNÇLİ OLARAK dardır: tam bir RFC 5322 ayrıştırıcısı yerine
// yalnızca "tek @ var, iki yanı da dolu, alan adında nokta var, boşluk yok"
// denetlenir. Daha katı bir desen geçerli ama alışılmadık adresleri reddeder,
// daha gevşek bir desen ise migration'daki CHECK kısıtına takılıp istemciye
// anlamsız bir veritabanı hatası döndürürdü.
//
// Aynı doğrulayıcı customer modülünde de vardır; modül izolasyonu gereği
// (Prensip 2.4) o paket import edilemez ve mantık burada tekrarlanır.
func normalizeEmail(email string) (string, error) {
	normalized := models.NormalizeEmail(email)
	if normalized == "" {
		return "", errors.Invalid(CodeInvalidInput, "e-posta boş olamaz")
	}
	if len(normalized) > models.MaxEmailLen {
		return "", errors.Invalid(CodeInvalidInput,
			"e-posta en fazla %d bayt olabilir, %d bayt verildi", models.MaxEmailLen, len(normalized))
	}
	if strings.ContainsFunc(normalized, unicode.IsSpace) {
		return "", errors.Invalid(CodeInvalidInput, "e-posta boşluk içeremez: %q", email)
	}

	local, domain, found := strings.Cut(normalized, "@")
	if !found || local == "" || domain == "" {
		return "", errors.Invalid(CodeInvalidInput,
			"e-posta \"ad@alan.uzanti\" biçiminde olmalı, %q verildi", email)
	}
	if strings.Contains(domain, "@") {
		return "", errors.Invalid(CodeInvalidInput,
			"e-posta birden çok @ içeremez, %q verildi", email)
	}
	// Alan adında en az bir nokta aranır ve nokta uçlarda olamaz: "a@b" ile
	// "a@b." arasındaki fark, ikincisinin hiçbir zaman teslim edilememesidir.
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", errors.Invalid(CodeInvalidInput,
			"e-posta alan adı geçersiz, %q verildi", email)
	}
	return normalized, nil
}

// normalizeCountryCode ülke kodunu doğrular ve BÜYÜK harfe çevirir.
//
// BOŞ değer geçerlidir ve boş döner: şirket adresi zorunlu değildir, çoğu kayıt
// fatura adresi kesinleşmeden açılır. Kodun gerçekten var olan bir ülkeye
// karşılık gelip gelmediği BURADA denetlenmez; ülke listesinin sahibi region
// modülüdür ve b2b onu import edemez (ADR 0001).
func normalizeCountryCode(code string) (string, error) {
	normalized := models.NormalizeCountryCode(code)
	if normalized == "" {
		return "", nil
	}
	if len(normalized) != countryCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"ülke kodu tam %d harf olmalı (ISO 3166-1 alpha-2), %q verildi", countryCodeLen, code)
	}
	if !harflerdenOlusuyor(normalized) {
		return "", errors.Invalid(CodeInvalidInput,
			"ülke kodu yalnızca harf içerebilir (ISO 3166-1 alpha-2), %q verildi", code)
	}
	return normalized, nil
}

// normalizeCurrencyCode para birimi kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Ülke kodunun tersine ZORUNLUDUR: harcama limiti bir tam sayıdır ve hangi para
// biriminde olduğu bilinmeden karşılaştırılamaz. Kodun gerçekten tanımlı bir
// para birimi olup olmadığı burada denetlenmez; o liste region modülünündür.
func normalizeCurrencyCode(code string) (string, error) {
	normalized := models.NormalizeCurrencyCode(code)
	if normalized == "" {
		return "", errors.Invalid(CodeInvalidInput, "para birimi kodu boş olamaz")
	}
	if len(normalized) != currencyCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"para birimi kodu tam %d harf olmalı (ISO 4217), %q verildi", currencyCodeLen, code)
	}
	if !harflerdenOlusuyor(normalized) {
		return "", errors.Invalid(CodeInvalidInput,
			"para birimi kodu yalnızca harf içerebilir (ISO 4217), %q verildi", code)
	}
	return normalized, nil
}

// harflerdenOlusuyor dizenin yalnızca A-Z harflerinden oluştuğunu bildirir.
//
// unicode.IsLetter KULLANILMAZ: Türkçe "Ş" de bir harftir ama ISO kodları
// yalnızca ASCII'dir ve geçmesine izin vermek, veritabanındaki desen kısıtına
// anlaşılmaz bir hatayla takılırdı.
func harflerdenOlusuyor(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// normalizeResetPeriod sıfırlama periyodunu doğrular.
//
// Boş değer [models.ResetNever]'a düşer: periyot vermemek "sıfırlama yok"
// demektir ve en kısıtlayıcı seçenektir — bilinmeyen bir değeri aylık saymak,
// hiç istenmemiş bir sıfırlamayı sessizce açardı.
func normalizeResetPeriod(period string) (models.SpendingResetPeriod, error) {
	trimmed := models.SpendingResetPeriod(strings.TrimSpace(period))
	if trimmed == "" {
		return models.ResetNever, nil
	}
	if !trimmed.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"harcama limiti sıfırlama periyodu %q, %q ya da %q olmalı, %q verildi",
			models.ResetMonthly, models.ResetYearly, models.ResetNever, period)
	}
	return trimmed, nil
}

// requireID kimliğin boş olmadığını, önekini ve uzunluğunu doğrular.
//
// Önek denetimi ucuz bir tip güvenliğidir: bir şirket kimliğinin çalışan
// kimliği yerine geçirilmesi veritabanına hiç gitmeden yakalanır ve hata,
// "bulunamadı" yerine ne beklendiğini söyler.
func requireID(id, prefix, label string) error {
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if strings.TrimSpace(id) != id {
		return errors.Invalid(CodeInvalidInput, "%s cannot contain leading/trailing whitespace: %q", label, id)
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
// Limit 0 verilirse varsayılan uygulanır. Negatif limit/offset ve [MaxLimit]'i
// aşan limit ise DÜZELTİLMEZ, reddedilir: sessizce kırpılan bir limit istemciye
// sayfa boyunu yanlış bildirir ve sayfalama döngüsü aynı kayıtları tekrar okur.
func normalizePaging(limit, offset int64) (outLimit, outOffset int64, err error) {
	if limit < 0 {
		return 0, 0, errors.Invalid(CodeInvalidInput, "limit negatif olamaz, %d verildi", limit)
	}
	if offset < 0 {
		return 0, 0, errors.Invalid(CodeInvalidInput, "offset negatif olamaz, %d verildi", offset)
	}
	if limit > MaxLimit {
		return 0, 0, errors.Invalid(CodeInvalidInput,
			"limit en fazla %d olabilir, %d verildi", MaxLimit, limit)
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	return limit, offset, nil
}

// checkLen bir metin alanının uzunluk sınırını doğrular.
func checkLen(label, value string, limit int) error {
	if len(value) > limit {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d bayt olabilir, %d bayt verildi", label, limit, len(value))
	}
	return nil
}

// requireText bir metin alanının dolu olduğunu doğrular.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return nil
}

// validateSpendingLimit harcama limitinin anlamlı olduğunu doğrular.
//
// nil "sınırsız" demektir ve geçerlidir. Negatif bir limit ise bir sınır değil,
// anlamsız bir sayıdır: her karşılaştırma onu aşardı ve çalışan sessizce hiç
// alışveriş yapamaz hâle gelirdi.
func validateSpendingLimit(limit *int64) error {
	if limit != nil && *limit < 0 {
		return errors.Invalid(CodeInvalidInput,
			"harcama limiti negatif olamaz, %d verildi (sınırsız için alanı boş bırakın)", *limit)
	}
	return nil
}
