package service

import (
	"context"
)

// Bu dosya region'ın MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// Buradaki imzalar YALNIZCA ilkel ve stdlib tipleri kullanır. Sebebi Go'nun
// yapısal uyum kuralıdır: tüketici modül region'ı import edemediği için
// imzasında models.Region gibi bir tipi adlandıramaz; adlandırdığı an o, kendi
// paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin arayüzünü
// karşılamaz. İlkel tiplerle yazılmış bir imza ise tüketicinin kendi paketinde
// birebir tekrarlanabilir ve container'dan "region.service" adıyla çözülür.
//
// Modül içi zengin yüzey (models tipleriyle) service.go, country.go ve
// currency.go'dadır; onu yalnızca region'ın kendi API katmanı ve query
// sağlayıcısı çağırır.
//
// Yüzey BİLİNÇLİ OLARAK dardır. Sepetin region'dan ihtiyaç duyduğu üç şey
// vardır — ülkeden bölge bulmak, bölgenin para birimini öğrenmek, bölgenin
// vergisini öğrenmek — ve dördüncüsü (bir kodun ondalık basamağı) sunum içindir.
// Buraya eklenen her metot, region'ı ayrı bir servise çıkarmanın maliyetini
// artırır.

// RegionIDForCountry ülke kodundan bölge KİMLİĞİNİ döner.
//
// Sepet oluşturma akışının ilk adımıdır: müşterinin ülkesi bilinir, sepetin
// bağlanacağı bölge bulunur. Bölge bulunamazsa errors.NotFound döner ve kodu
// hangi durumun geçerli olduğunu söyler (bkz. [Service.ResolveRegionForCountry]).
//
// Tüketici tarafındaki karşılığı (Faz 5'te cart bunu tanımlayacaktır):
//
//	type RegionResolver interface {
//	    RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
//	}
func (s *Service) RegionIDForCountry(ctx context.Context, countryCode string) (string, error) {
	region, err := s.ResolveRegionForCountry(ctx, countryCode)
	if err != nil {
		return "", err
	}
	return region.ID, nil
}

// RegionCurrency bölgenin para birimi kodunu ve ONDALIK BASAMAK sayısını döner.
//
// İkisi birlikte döner çünkü çağıranın ikisine de aynı anda ihtiyacı vardır ve
// ayrı iki çağrı iki tur demek olurdu: kod, sepetin hangi para biriminde
// tutulacağını; basamak sayısı ise minor unit tam sayının hangi çarpanla
// gösterileceğini söyler (bkz. models.Currency.MinorUnitFactor). Basamak
// sayısını bilmeyen bir sunum katmanı sabit 100 varsayar ve yen tutarlarını
// yüz kat küçük gösterir.
//
// Bölge yoksa errors.NotFound döner. Bölge var ama para birimi referans
// tablosunda yoksa da errors.NotFound döner; bu durum foreign key nedeniyle
// normalde oluşamaz.
//
// Tüketici tarafındaki karşılığı:
//
//	type RegionCurrencyReader interface {
//	    RegionCurrency(ctx context.Context, regionID string) (string, int32, error)
//	}
func (s *Service) RegionCurrency(ctx context.Context, regionID string) (code string, decimalDigits int32, err error) {
	region, err := s.GetRegion(ctx, regionID)
	if err != nil {
		return "", 0, err
	}

	currency, err := s.repo.GetCurrency(ctx, region.CurrencyCode)
	if err != nil {
		return "", 0, err
	}
	return currency.Code, currency.DecimalDigits, nil
}

// RegionTax bölgenin GEÇİCİ vergi oranını (baz puan) ve verginin otomatik
// uygulanıp uygulanmayacağını döner.
//
// GEÇİCİ: plan Faz 7'de tax modülü vergi hesabını devralacaktır ve bu metot
// kaldırılacaktır. O güne kadar sepet toplamının vergi satırını hesaplayabilmesi
// için tek ve basit bir oran taşınır.
//
// Oran tam sayıdır ve baz puandır (2000 = %20): float bir oran, tutarla
// çarpıldığında kuruş düzeyinde sessiz yuvarlama üretirdi (plan Bölüm 8).
// Çağıran vergiyi "tutar * oran / 10000" biçiminde, tam sayı aritmetiğiyle
// hesaplamalıdır.
//
// Tüketici tarafındaki karşılığı:
//
//	type RegionTaxReader interface {
//	    RegionTax(ctx context.Context, regionID string) (int32, bool, error)
//	}
func (s *Service) RegionTax(ctx context.Context, regionID string) (rateBps int32, automatic bool, err error) {
	region, err := s.GetRegion(ctx, regionID)
	if err != nil {
		return 0, false, err
	}
	return region.TaxRate, region.AutomaticTaxes, nil
}

// CurrencyDecimalDigits bir para birimi kodunun ondalık basamak sayısını döner.
//
// Elinde bölge değil yalnızca para birimi kodu olan çağıranlar içindir
// (örn. sipariş kaydının para birimi). Bölgeden gidiliyorsa
// [Service.RegionCurrency] tek çağrıda ikisini de verir.
//
// Tüketici tarafındaki karşılığı:
//
//	type CurrencyReader interface {
//	    CurrencyDecimalDigits(ctx context.Context, currencyCode string) (int32, error)
//	}
func (s *Service) CurrencyDecimalDigits(ctx context.Context, currencyCode string) (int32, error) {
	currency, err := s.GetCurrency(ctx, currencyCode)
	if err != nil {
		return 0, err
	}
	return currency.DecimalDigits, nil
}
