package openapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// CakisanKayit ad çakışması testinin İKİ tipinden biridir; ötekini dış test
// paketi (openapi_test) AYNI adla tanımlar.
//
// İki tipin iki AYRI pakette olması zorunludur: aynı pakette aynı adı taşıyan
// iki tip zaten derlenmez, yani çakışma yalnızca paketler arasında doğar ve
// tek dosyalık bir testle üretilemez. Dahili test dosyası bu yüzden var —
// derleyici için openapi paketinin parçasıdır ve adı openapi_test'tekiyle
// çakışır, ama üretim ikilisine hiç girmez.
type CakisanKayit struct {
	Alan string `json:"alan"`
}

// TestBilesenAdiGoAyrintisiniSizdirmaz yayımlanan bileşen adlarının Go'nun
// dışa açma kuralına ve paket içi adlandırma alışkanlığına bağlı kalmadığını
// doğrular.
//
// Bileşen adı bir iç ayrıntı DEĞİL, yayımlanan sözleşmedir: istemci üreteçleri
// ondan sınıf adı üretir ve istemci bir kez üretildikten sonra adı değiştirmek
// kırıcıdır. Normalleştirilmeseydi aynı belgede "StoreProduct" (dışa açık) ile
// "cartDTO" (dışa kapalı) yan yana durur, üretilen istemcide iki farklı
// adlandırma düzeni olurdu.
func TestBilesenAdiGoAyrintisiniSizdirmaz(t *testing.T) {
	t.Parallel()

	testler := map[string]struct {
		goAdi   string
		bekleme string
	}{
		"dışa kapalı ad büyütülür":     {goAdi: "cartDTO", bekleme: "Cart"},
		"DTO son eki atılır":           {goAdi: "addressDTO", bekleme: "Address"},
		"dışa açık ad korunur":         {goAdi: "StoreProduct", bekleme: "StoreProduct"},
		"istek tipleri anlamlı kalır":  {goAdi: "createCartRequest", bekleme: "CreateCartRequest"},
		"yalnızca DTO adı yok edilmez": {goAdi: "DTO", bekleme: "DTO"},
		"boş ad boş kalır":             {goAdi: "", bekleme: ""},
	}

	for ad, tt := range testler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.bekleme, bilesenAdi(tt.goAdi))
		})
	}
}
