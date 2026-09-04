package service

import (
	"slices"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// maxIDLen kabul edilen kimlik uzunluğu üst sınırıdır. Kimlikler link
// tablosundaki benzersiz indekse de girdiği için sınır orayla uyumlu tutulur.
const maxIDLen = 255

// normalizeEmail e-postayı doğrular ve saklama biçimine çevirir.
//
// Doğrulama BİLİNÇLİ OLARAK dardır: tam bir RFC 5322 ayrıştırıcısı yazmak
// yerine yalnızca "tek @ var, iki yanı da dolu, alan adında nokta var, boşluk
// yok" denetlenir. Daha katı bir desen geçerli ama alışılmadık adresleri
// (artı işaretli, tireli, uzun TLD'li) reddeder ve kullanıcıyı hesap açamaz
// hâle getirirdi; daha gevşek bir desen ise migration'daki CHECK kısıtına
// takılıp istemciye anlamsız bir veritabanı hatası döndürürdü. Desen o kısıtla
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

// requireID kimliğin boş olmadığını, önekini ve uzunluğunu doğrular.
//
// Önek denetimi ucuz bir tip güvenliğidir: bir kanal kimliğinin kullanıcı
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

// normalizeScopes yetki listesini doğrular, kırpar ve tekrarları eler.
//
// nil dilim "yetki verilmedi" demektir ve olduğu gibi döner; çağıran bunu
// varsayılana çevirir. Boş olmayan bir dilimdeki boş ad ise REDDEDİLİR:
// veritabanındaki CHECK kısıtı da aynı şeyi söyler ve istemcinin oraya
// takılıp anlamsız bir kısıt hatası görmesi gereksizdir.
func normalizeScopes(scopes []string) ([]string, error) {
	if scopes == nil {
		return nil, nil
	}
	if len(scopes) > models.MaxScopeCount {
		return nil, errors.Invalid(CodeInvalidInput,
			"en fazla %d yetki verilebilir, %d verildi", models.MaxScopeCount, len(scopes))
	}

	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		trimmed := strings.TrimSpace(scope)
		if trimmed == "" {
			return nil, errors.Invalid(CodeInvalidInput, "yetki adı boş olamaz")
		}
		if len(trimmed) > models.MaxScopeLen {
			return nil, errors.Invalid(CodeInvalidInput,
				"yetki adı en fazla %d bayt olabilir, %q verildi", models.MaxScopeLen, trimmed)
		}
		if !slices.Contains(out, trimmed) {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

// Parola politikası sınırları.
//
// Politika bilinçli olarak UZUNLUĞA dayanır, bileşime değil: NIST SP 800-63B,
// "en az bir büyük harf, bir rakam, bir simge" türü kuralların kullanıcıyı
// tahmin edilebilir kalıplara ("Parola1!") ittiğini ve gerçek entropiyi
// artırmadığını söyler. Uzunluk ise doğrudan arama uzayını büyütür.
const (
	// MinPasswordLen kabul edilen en kısa parola uzunluğudur (bayt).
	//
	// Yönetim kullanıcısı için 12 seçilmiştir: bu hesap tüm mağazayı yönetir
	// ve tek bir sızıntının bedeli en yüksek olan hesaptır.
	MinPasswordLen = 12
	// MaxPasswordLen kabul edilen en uzun parola uzunluğudur (bayt).
	//
	// 72, bcrypt algoritmasının işlediği azami anahtar uzunluğudur. Sınır
	// AÇIKÇA uygulanır: bcrypt.GenerateFromPassword daha uzununu reddeder ve
	// o hata istemciye ham hâliyle gitseydi, kullanıcı parolasının neden
	// kabul edilmediğini anlamazdı. Sessizce KIRPMAK ise daha kötüsü olurdu —
	// 80 karakterlik bir parolanın ilk 72 karakteri yeterli sanılırdı.
	MaxPasswordLen = 72
)

// validatePassword parola politikasını uygular.
//
// PAROLA HATA MESAJINDA GEÇMEZ: yalnızca uzunluğu bildirilir. Hata mesajları
// log'a düşer ve bir gün destek kaydına kopyalanır; parolanın kendisi o
// yolculuğa hiç çıkmamalıdır.
func validatePassword(password string) error {
	if len(password) < MinPasswordLen {
		return errors.Invalid(CodeWeakPassword,
			"parola en az %d karakter olmalı, %d karakter verildi", MinPasswordLen, len(password))
	}
	if len(password) > MaxPasswordLen {
		return errors.Invalid(CodeWeakPassword,
			"parola en fazla %d bayt olabilir (bcrypt sınırı), %d bayt verildi",
			MaxPasswordLen, len(password))
	}
	if strings.TrimSpace(password) == "" {
		return errors.Invalid(CodeWeakPassword, "parola yalnızca boşluktan oluşamaz")
	}
	return nil
}
