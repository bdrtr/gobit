package service

import (
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Tutar ve oran sınırları.
//
// Sınırlar cart akışındakilerle bilinçli olarak UYUMLUDUR; iki taraf birbirini
// import etmediği için değerler burada tekrarlanır (ADR 0001'in kabul edilen
// bedeli). Aynı olmaları şart değil, YETERLİ olmaları şarttır: buradaki tavan
// çağıranınkinden küçük olsaydı, sepette geçerli sayılan bir satır vergi
// hesabında reddedilir ve müşteri sepetini hiç kapatamazdı.
const (
	// MaxTaxableAmount tek bir kalemin vergilendirilebilir taban üst sınırıdır
	// (minor unit).
	//
	// Değer, sepet akışındaki bir satır ara toplamının teorik tavanıyla aynıdır
	// (birim fiyat tavanı × adet tavanı = 10^12 × 10^6). Yani sepette
	// oluşabilecek HER satır bu hesaba girebilir.
	MaxTaxableAmount int64 = 1_000_000_000_000_000_000
	// MaxItems tek bir hesapta kabul edilen kalem sayısıdır.
	//
	// Sınırın varlığı şarttır: sınırsız bir kalem listesi, tek istekle
	// belleği ve CPU'yu tüketmenin en ucuz yoludur. Bin kalem, gerçek bir
	// sepetin çok üstündedir.
	MaxItems = 1000
	// BpsScale baz puan ölçeğidir: 10000 baz puan = %100.
	BpsScale int64 = 10_000
)

// addAmount iki tutarı TAŞMADAN toplar.
//
// Toplam [MaxTaxableAmount]'ı aşarsa hata döner. Taşan bir toplama sessizce
// NEGATİF bir tutar üretir; negatif bir vergi toplamı, sepet toplamını
// müşteri lehine sınırsız küçültebilirdi.
func addAmount(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Internal(CodeAmountOverflow,
			"tutarlar negatif olamaz: %d + %d", a, b)
	}
	if a > MaxTaxableAmount-b {
		return 0, errors.Invalid(CodeAmountOverflow,
			"tutar toplamı sınırı aşıyor: %d + %d > %d", a, b, MaxTaxableAmount)
	}
	return a + b, nil
}

// TaxOf verilen taban üzerinden baz puan oranıyla vergiyi hesaplar.
//
// # Yuvarlama yönü
//
// Sonuç AŞAĞI yuvarlanır (tam sayı bölmesi). Hata kalem başına bir minor
// unit'ten küçüktür ve daima MÜŞTERİ LEHİNEDİR. Yakına yuvarlama
// (round-half-up) seçilmedi: müşteriden fazla tahsil eder ve "fazlası nereden
// geldi" sorusunu mutabakata bırakır. Kayan noktalı oran ise plan Bölüm 8
// gereği hiç düşünülmez.
//
// # Neden önce bölünüyor
//
// Doğrudan base × rate hesabı taşardı: taban en fazla [MaxTaxableAmount]
// (10^18) ve oran en fazla 10^4 olduğu için çarpım 10^22'ye kadar çıkar,
// int64 ise 9,22 × 10^18'de biter. Bölmeyi öne almak sonucu DEĞİŞTİRMEZ —
// base = q × 10000 + r yazılırsa base × rate / 10000 = q × rate +
// (r × rate) / 10000'dir ve q × rate zaten tam sayı olduğu için aşağı
// yuvarlama yalnızca ikinci terime düşer. Her iki terim de int64'e rahatça
// sığar (q × rate ≤ 10^18, r × rate < 10^8).
//
// Dışa açıktır çünkü hem yerel sağlayıcı hem de dış sağlayıcı adaptörleri aynı
// aritmetiği kullanmalıdır; iki ayrı uygulama, iki farklı yuvarlama demek
// olurdu.
func TaxOf(base int64, rateBps int32) (int64, error) {
	if base < 0 {
		return 0, errors.Internal(CodeAmountOverflow, "vergi tabanı negatif olamaz: %d", base)
	}
	if base > MaxTaxableAmount {
		return 0, errors.Invalid(CodeAmountOverflow,
			"vergi tabanı sınırı aşıyor: %d > %d", base, MaxTaxableAmount)
	}
	if rateBps < models.MinRateBps || rateBps > models.MaxRateBps {
		return 0, errors.Internal(CodeRateOutOfRange,
			"vergi oranı [%d, %d] baz puan aralığında olmalı, %d bildirildi",
			models.MinRateBps, models.MaxRateBps, rateBps)
	}
	if base == 0 || rateBps == 0 {
		return 0, nil
	}

	rate := int64(rateBps)
	whole := (base / BpsScale) * rate
	remainder := ((base % BpsScale) * rate) / BpsScale
	return whole + remainder, nil
}

// checkTaxableAmount vergilendirilebilir bir tabanın kabul edilebilir olduğunu
// doğrular.
//
// Negatif taban REDDEDİLİR, sıfıra çekilmez: negatif bir taban çağıranın
// indirim hesabında bir hata yaptığının en açık göstergesidir ve sessizce
// düzeltmek o hatayı gizlerdi.
func checkTaxableAmount(label string, value int64) error {
	if value < 0 {
		return errors.Invalid(CodeInvalidInput, "%s negatif olamaz: %d", label, value)
	}
	if value > MaxTaxableAmount {
		return errors.Invalid(CodeAmountOverflow,
			"%s can be at most %d: %d", label, MaxTaxableAmount, value)
	}
	return nil
}
