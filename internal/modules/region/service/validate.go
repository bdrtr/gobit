package service

import (
	"math"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// maxNameLen bölge adının azami bayt uzunluğudur. Sınırsız bir ad, tek istekle
// tabloya megabaytlarca metin yazmanın en ucuz yoludur.
const maxNameLen = 255

// maxIDLen kabul edilen kimlik uzunluğu üst sınırıdır. Kimlikler link
// tablosundaki benzersiz indekse de girdiği için sınır orayla uyumlu tutulur.
const maxIDLen = 255

// NormalizeCurrencyCode ISO 4217 para birimi kodunu doğrular ve BÜYÜK harfe
// çevirir.
//
// Kabul edilen biçim tam üç HARFTİR. Baştaki/sondaki boşluklar kırpılır (kod
// zaten büyük harfe dönüştürülerek normalleştiriliyor; boşluk için ayrı bir
// katılık tutarsız olurdu), ama harf dışı hiçbir karakter kabul edilmez.
//
// Yalnızca BİÇİM denetlenir; kodun tanımlı olup olmadığı ancak veritabanındaki
// referans tablosundan bilinir ve oradaki foreign key ile denetlenir. Ayrım
// önemlidir: "abc" biçimsel olarak geçerli ama tanımsız bir koddur ve ikisi de
// errors.Invalid döner, farkları yalnızca mesajlarındadır.
//
// Dışa açıktır çünkü aynı normalleştirme hem servis girdilerinde hem de
// modüller arası yüzeyde (bkz. interop.go) kullanılır ve iki yerin ayrışması,
// bir yoldan geçen kodun diğerinden geçmemesi demek olurdu.
func NormalizeCurrencyCode(code string) (string, error) {
	return normalizeAlphaCode(code, models.CurrencyCodeLength, "para birimi kodu", "ISO 4217")
}

// NormalizeCountryCode ISO 3166-1 alpha-2 ülke kodunu doğrular ve BÜYÜK harfe
// çevirir.
//
// Kabul edilen biçim tam iki HARFTİR; kuralın gerekçesi
// [NormalizeCurrencyCode] ile aynıdır.
func NormalizeCountryCode(code string) (string, error) {
	return normalizeAlphaCode(code, models.CountryCodeLength, "ülke kodu", "ISO 3166-1 alpha-2")
}

// normalizeAlphaCode sabit uzunluklu bir alfabetik kodu doğrular ve büyük
// harfe çevirir.
//
// ASCII denetimi büyük harfe çevirmeden ÖNCE, yalnızca kırpılmış ORİJİNAL
// rune'lar üzerinde yapılır. Sıra kritiktir: Unicode'un basit büyük harf
// eşlemesi bazı ASCII DIŞI harfleri ASCII harflere taşır (noktasız "ı" -> "I",
// uzun "ſ" -> "S"). Denetim çevirmeden SONRA yapılsaydı "ıs" sessizce "IS"
// (İzlanda), "ıls" da "ILS" olur ve fonksiyonun "yalnızca ASCII harf" sözü
// tutmazdı.
//
// Uzunluk BAYT değil RUNE sayısıyla ölçülür: "TRY" ile aynı bayt uzunluğunda
// ama üç harf olmayan bir girdi (örn. iki çok baytlı karakter) aksi hâlde
// uzunluk denetimini geçer, harf denetiminde takılırdı — mesaj o zaman yanlış
// sebebi gösterirdi.
func normalizeAlphaCode(code string, length int, label, standard string) (string, error) {
	trimmed := strings.TrimSpace(code)
	if len([]rune(trimmed)) != length {
		return "", errors.Invalid(CodeInvalidInput,
			"%s tam %d harf olmalı (%s), %q verildi", label, length, standard, code)
	}
	for _, r := range trimmed {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "", errors.Invalid(CodeInvalidInput,
				"%s yalnızca ASCII harf içerebilir (%s), %q verildi", label, standard, code)
		}
	}
	return strings.ToUpper(trimmed), nil
}

// normalizeName bölge adını doğrular ve baş/son boşluklarını kırpar.
func normalizeName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.Invalid(CodeInvalidInput, "bölge adı boş olamaz")
	}
	if len(trimmed) > maxNameLen {
		return "", errors.Invalid(CodeInvalidInput,
			"bölge adı en fazla %d bayt olabilir, %d bayt verildi", maxNameLen, len(trimmed))
	}
	for _, r := range trimmed {
		// Kontrol karakterleri (satır sonu dâhil) bir ad değildir ve log ile
		// yönetim arayüzünü bozar.
		if unicode.IsControl(r) {
			return "", errors.Invalid(CodeInvalidInput, "bölge adı kontrol karakteri içeremez")
		}
	}
	return trimmed, nil
}

// validateTaxRate vergi oranının izin verilen aralıkta olduğunu doğrular.
//
// Oran BAZ PUANDIR (2000 = %20). Üst sınır %100'dür: daha büyük bir oran
// veri giriş hatasıdır ve sepet toplamını sessizce ikiye katlardı.
func validateTaxRate(rate int32) error {
	if rate < models.MinTaxRate {
		return errors.Invalid(CodeInvalidInput,
			"vergi oranı negatif olamaz, %d verildi (baz puan)", rate)
	}
	if rate > models.MaxTaxRate {
		return errors.Invalid(CodeInvalidInput,
			"vergi oranı en fazla %d baz puan (%%100) olabilir, %d verildi", models.MaxTaxRate, rate)
	}
	return nil
}

// requireRegionID bir bölge kimliğinin kullanılabilir ve DOĞRU TÜRDE olduğunu
// doğrular.
//
// Önek kontrolü bilinçlidir: önekli kimliklerin varlık sebebi, yanlış türde bir
// kimliğin (örn. bir müşteri kimliğinin bölge yerine geçmesi) "bulunamadı"
// olarak değil, ne olduğu belli bir doğrulama hatası olarak dönmesidir.
func requireRegionID(id string) error {
	const label = "bölge kimliği"
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
	if !strings.HasPrefix(id, models.RegionIDPrefix) {
		return errors.Invalid(CodeInvalidInput,
			"%s %q önekiyle başlamalı, %q verildi", label, models.RegionIDPrefix, id)
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
