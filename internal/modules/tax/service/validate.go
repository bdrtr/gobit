package service

import (
	"math"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Uzunluk sınırları. Sınırsız bir metin alanı, tek istekle tabloya
// megabaytlarca veri yazmanın en ucuz yoludur.
const (
	// maxNameLen oran adının azami bayt uzunluğudur.
	maxNameLen = 255
	// maxCodeLen mutabakat kodunun azami bayt uzunluğudur.
	maxCodeLen = 64
	// maxIDLen dışarıdan gelen kimlikler için üst sınırdır; core/link ve diğer
	// modüller de aynı sınırı uygular.
	maxIDLen = 255
)

// NormalizeCountryCode ISO 3166-1 alpha-2 ülke kodunu doğrular ve BÜYÜK harfe
// çevirir.
//
// Kabul edilen biçim tam iki HARFTİR. Baştaki/sondaki boşluklar kırpılır (kod
// zaten büyük harfe dönüştürülerek normalleştiriliyor; boşluk için ayrı bir
// katılık tutarsız olurdu), ama harf dışı hiçbir karakter kabul edilmez.
//
// Yalnızca BİÇİM denetlenir. Kodun ISO'da tanımlı olup olmadığı bu modülde
// bilinmez: ülke listesi region modülünün verisidir ve tax onu import edemez
// (ADR 0001). Ayrım önemlidir — "XX" biçimsel olarak geçerli ama tanımsız bir
// koddur ve bu modülde yalnızca "vergi bölgesi yok" sonucunu doğurur.
//
// Dışa açıktır çünkü aynı normalleştirme hem servis girdilerinde hem de
// modüller arası yüzeyde (bkz. interop.go) kullanılır; iki yerin ayrışması, bir
// yoldan geçen kodun diğerinden geçmemesi demek olurdu.
func NormalizeCountryCode(code string) (string, error) {
	trimmed := strings.TrimSpace(code)
	// Uzunluk BAYT değil RUNE sayısıyla ölçülür: iki baytlık tek bir karakter
	// aksi hâlde uzunluk denetimini geçer, harf denetiminde takılırdı ve mesaj
	// yanlış sebebi gösterirdi.
	if len([]rune(trimmed)) != models.CountryCodeLength {
		return "", errors.Invalid(CodeInvalidInput,
			"ülke kodu tam %d harf olmalı (ISO 3166-1 alpha-2), %q verildi",
			models.CountryCodeLength, code)
	}
	// ASCII denetimi büyük harfe çevirmeden ÖNCE yapılır. Sıra kritiktir:
	// Unicode'un basit büyük harf eşlemesi bazı ASCII DIŞI harfleri ASCII
	// harflere taşır (noktasız "ı" -> "I"); denetim sonra yapılsaydı "ıs"
	// sessizce "IS" (İzlanda) olurdu.
	for _, r := range trimmed {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "", errors.Invalid(CodeInvalidInput,
				"ülke kodu yalnızca ASCII harf içerebilir (ISO 3166-1 alpha-2), %q verildi", code)
		}
	}
	return strings.ToUpper(trimmed), nil
}

// NormalizeProvinceCode eyalet/il kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Boş girdi boş çıktı döner ve HATA DEĞİLDİR: eyalet kodu isteğe bağlıdır ve
// boşluğu "ülke düzeyi" demektir. Dolu girdide kabul edilen alfabe ASCII harf,
// rakam ve tiredir — ISO 3166-2'nin ülke içi bölümü (örn. "US-CA" içindeki
// "CA"), Kanada eyaletleri ve Türkiye'nin plaka kodları ("34") bu kümeye
// girer. Kod RAKAMLA da başlayabilir; kısıt yalnızca ilk karakterin tire
// OLMAMASIDIR ("-CA" gibi bir değer, ayırıcının yanlışlıkla kopyalandığının
// göstergesidir).
func NormalizeProvinceCode(code string) (string, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return "", nil
	}

	runes := []rune(trimmed)
	if len(runes) > models.MaxProvinceCodeLength {
		return "", errors.Invalid(CodeInvalidInput,
			"eyalet kodu en fazla %d karakter olabilir, %q verildi",
			models.MaxProvinceCodeLength, code)
	}
	for i, r := range runes {
		alnum := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if alnum {
			continue
		}
		if r == '-' && i > 0 {
			continue
		}
		return "", errors.Invalid(CodeInvalidInput,
			"eyalet kodu ASCII harf, rakam ve tire içerebilir ve tireyle başlayamaz, %q verildi", code)
	}
	return strings.ToUpper(trimmed), nil
}

// normalizeName bir oranın adını doğrular ve baş/son boşluklarını kırpar.
func normalizeName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.Invalid(CodeInvalidInput, "vergi oranı adı boş olamaz")
	}
	if len(trimmed) > maxNameLen {
		return "", errors.Invalid(CodeInvalidInput,
			"vergi oranı adı en fazla %d bayt olabilir, %d bayt verildi", maxNameLen, len(trimmed))
	}
	for _, r := range trimmed {
		// Kontrol karakterleri (satır sonu dâhil) bir ad değildir ve log ile
		// yönetim arayüzünü bozar.
		if unicode.IsControl(r) {
			return "", errors.Invalid(CodeInvalidInput, "vergi oranı adı kontrol karakteri içeremez")
		}
	}
	return trimmed, nil
}

// normalizeCode mutabakat kodunu doğrular; boş girdi boş çıktı döner.
//
// Boşluk "kod yok" demektir ve depoda SQL NULL'a çevrilir. Boş dizeyi kod
// saymak, bölge içindeki benzersizlik indeksinde iki kodsuz oranın çakışması
// demek olurdu.
func normalizeCode(code string) (string, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"vergi oranı kodu en fazla %d bayt olabilir, %d bayt verildi", maxCodeLen, len(trimmed))
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.Invalid(CodeInvalidInput,
				"vergi oranı kodu boşluk ya da kontrol karakteri içeremez: %q", code)
		}
	}
	return trimmed, nil
}

// validateRateBps oranın izin verilen aralıkta olduğunu doğrular.
//
// Oran BAZ PUANDIR (2000 = %20). Üst sınır %100'dür: daha büyük bir oran veri
// giriş hatasıdır ve sepet toplamını sessizce ikiye katlardı.
func validateRateBps(rateBps int32) error {
	if rateBps < models.MinRateBps {
		return errors.Invalid(CodeInvalidInput,
			"vergi oranı negatif olamaz, %d verildi (baz puan)", rateBps)
	}
	if rateBps > models.MaxRateBps {
		return errors.Invalid(CodeInvalidInput,
			"vergi oranı en fazla %d baz puan (%%100) olabilir, %d verildi",
			models.MaxRateBps, rateBps)
	}
	return nil
}

// requireID dışarıdan gelen bir kimliğin kullanılabilir ve DOĞRU TÜRDE
// olduğunu doğrular.
//
// Önek kontrolü bilinçlidir: önekli kimliklerin varlık sebebi, yanlış türde bir
// kimliğin (örn. bir oran kimliğinin bölge yerine geçmesi) "bulunamadı" olarak
// değil, ne olduğu belli bir doğrulama hatası olarak dönmesidir.
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

// requireReferenceID kuralın baktığı YABANCI kimliğin kullanılabilir olduğunu
// doğrular.
//
// Önek denetimi YAPILMAZ: kimlik başka bir modüle aittir (ürün, ürün tipi,
// kargo seçeneği) ve o modüllerin önek sözleşmesini burada tekrarlamak, bir
// modül önekini değiştirdiğinde tax'ın sessizce kural kabul etmemesi demek
// olurdu (ADR 0001 — tax o modülleri tanımaz).
func requireReferenceID(id string) error {
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "kural referans kimliği boş olamaz")
	}
	if strings.TrimSpace(id) != id {
		return errors.Invalid(CodeInvalidInput,
			"kural referans kimliği baş/son boşluk içeremez: %q", id)
	}
	if len(id) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"kural referans kimliği en fazla %d bayt olabilir, %d bayt verildi", maxIDLen, len(id))
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
