package service

import (
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// countryCodeLen ISO 3166-1 alpha-2 kodunun uzunluğudur.
const countryCodeLen = 2

// maxIDLen kabul edilen kimlik uzunluğu üst sınırıdır. Kimlikler link
// tablosundaki benzersiz indekse de girdiği için sınır orayla uyumlu tutulur.
const maxIDLen = 255

// normalizeEmail e-postayı doğrular ve saklama biçimine çevirir.
//
// Doğrulama BİLİNÇLİ OLARAK dardır: tam bir RFC 5322 ayrıştırıcısı yazmak
// yerine yalnızca "tek @ var, iki yanı da dolu, alan adında nokta var, boşluk
// yok" denetlenir. Daha katı bir desen geçerli ama alışılmadık adresleri
// (artı işaretli, tireli, uzun TLD'li) reddeder ve müşteriyi kaydolamaz hâle
// getirirdi; daha gevşek bir desen ise migration'daki CHECK kısıtına takılıp
// istemciye anlamsız bir veritabanı hatası döndürürdü. Desen o kısıtla
// birebir aynı gereksinimi ifade eder.
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
// Kabul edilen biçim ISO 3166-1 alpha-2'dir: tam iki harf. Kodun gerçekten var
// olan bir ülkeye karşılık gelip gelmediği BURADA denetlenmez; ülke listesinin
// sahibi region modülüdür ve customer onu import edemez (ADR 0001).
func normalizeCountryCode(code string) (string, error) {
	normalized := models.NormalizeCountryCode(code)
	if len(normalized) != countryCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"ülke kodu tam %d harf olmalı (ISO 3166-1 alpha-2), %q verildi", countryCodeLen, code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"ülke kodu yalnızca harf içerebilir (ISO 3166-1 alpha-2), %q verildi", code)
		}
	}
	return normalized, nil
}

// requireID kimliğin boş olmadığını, önekini ve uzunluğunu doğrular.
//
// Önek denetimi ucuz bir tip güvenliğidir: bir group idnin müşteri
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
// aşan limit ise DÜZELTİLMEZ, reddedilir: sessizce kırpılan bir limit
// istemciye sayfa boyunu yanlış bildirir ve sayfalama döngüsü aynı kayıtları
// tekrar okur.
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

// validatePerson kişi alanlarının uzunluk sınırlarını doğrular.
func validatePerson(firstName, lastName, phone string) error {
	if err := checkLen("ad", firstName, models.MaxNameLen); err != nil {
		return err
	}
	if err := checkLen("soyad", lastName, models.MaxNameLen); err != nil {
		return err
	}
	return checkLen("telefon", phone, models.MaxPhoneLen)
}

// validatePatchPerson kısmi güncellemedeki kişi alanlarını doğrular.
//
// nil alanlar atlanır: "dokunma" ile "boş yaz" ayrımı korunur ve verilmeyen
// bir alan için uzunluk hatası üretilmez.
func validatePatchPerson(patch models.CustomerPatch) error {
	if patch.FirstName != nil {
		if err := checkLen("ad", *patch.FirstName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.LastName != nil {
		if err := checkLen("soyad", *patch.LastName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.Phone != nil {
		if err := checkLen("telefon", *patch.Phone, models.MaxPhoneLen); err != nil {
			return err
		}
	}
	return nil
}
