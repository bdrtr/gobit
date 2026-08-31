package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Parametre şemalarında geçen JSON Schema adları.
//
// Çekirdeğin karşılıkları dışa kapalıdır ve burada tekrarlanmalarının sebebi
// maliyet değil SESSİZLİK: "strig" yazılmış bir tip adı derlenir, belge
// üretilir ve yalnızca şemayı okuyan istemci parametreyi yanlış tiple
// ürettiğinde ortaya çıkar.
const (
	semaTip    = "type"
	tipDize    = "string"
	tipTamSayi = "integer"
)

// Describe tax'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (createTaxRateRequest,
// taxRateDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek
// için dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini genişletmek
// olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir hâle
// gelirdi. Sorgu parametreleri de aynı sebeple burada durur — hangi
// parametrenin GERÇEKTEN okunduğunu bilen kod admin.go içindedir; anlatım
// başka bir pakete taşınsaydı ikisi sessizce ayrışırdı. Modülün
// [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [API]'ye bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa [API.Routes] hiç çağrılmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # Neden yalnızca /admin/v1
//
// Modülün vitrin yüzeyi YOKTUR (bkz. paket belgesi): vergi müşteriye doğrudan
// açılmaz, sepetin hesaplanmış vergi satırı üzerinden gider. Belgede
// olmayan bir uç uydurmak, istemci üretecine hiç çağrılamayacak bir metot
// yazdırırdı.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin oluşturma DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin POST /admin/v1/tax-regions, ülke kökü
// oluştururken boş bırakılan province_code ve parent_id'yi de ister. Alan
// ADLARI ve TİPLERİ doğrudur, yani şema yanlış bir alan uydurmaz; yalnızca
// fazla şey ister. Güncelleme gövdesi ([updateTaxRateRequest]) bu sınırın
// dışındadır: alanları işaretçidir ve şemada null kabul ederek "verilmeyen
// alan değişmez" davranışını doğru anlatır. Doğru çözüm ÇEKİRDEKTEDİR (istek
// gövdeleri için ayrı bir "required" politikası); tag'lere omitempty
// serpiştirmek zorunluluğu servisin doğrulamasından json etiketine taşır ve
// ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describeBolgeler(d)
	describeOranlar(d)
	describeKurallar(d)
}

// describeBolgeler vergi bölgesi uçlarını anlatır.
func describeBolgeler(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRegions, openapi.Operation{
		Summary: "Yeni bir vergi bölgesi oluşturur.",
		Description: "province_code boş bırakılırsa ÜLKE KÖKÜ oluşturulur; dolu " +
			"verilirse parent_id de zorunludur.",
		RequestBody: d.RequestBody(createTaxRegionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan vergi bölgesi", d.Item(taxRegionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegions, openapi.Operation{
		Summary: "Vergi bölgelerini sayfalayarak listeler.",
		Parameters: append(sayfaParametreleri(),
			sorguParametresi("country_code", tipDize, false,
				"Listeyi tek bir ülkeyle sınırlar; verilmezse tüm bölgeler döner.")),
		Responses: map[string]any{
			"200": openapi.Response("Vergi bölgeleri", d.List(taxRegionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegion, openapi.Operation{
		Summary: "Vergi bölgesini kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Vergi bölgesi", d.Item(taxRegionDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRegion, openapi.Operation{
		Summary: "Vergi bölgesini AĞACIYLA birlikte yumuşak siler.",
		Description: "Silme alt bölgeleri, onların oranlarını ve o oranların " +
			"kurallarını da kapsar. Yanıtın gövdesi YOKTUR: silinen ağacın " +
			"dökümünü döndürmek, istemcinin ihtiyacı olmayan bir listeyi her " +
			"çağrıda üretmek olurdu.",
		Responses: map[string]any{
			"204": bosYanit("Vergi bölgesi ve ağacı silindi"),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegionRates, openapi.Operation{
		Summary: "Vergi bölgesinin oranlarını listeler.",
		// Sayfalama parametresi YOKTUR: liste [writeAll] ile yazılır ve handler
		// sorgu dizesini hiç okumaz. Yazmak, istemciye çalışmayan bir sayfalama
		// vaat etmek olurdu.
		Responses: map[string]any{
			"200": openapi.Response("Bölgenin vergi oranları", d.List(taxRateDTO{})),
		},
	})
}

// describeOranlar vergi oranı uçlarını anlatır.
func describeOranlar(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRates, openapi.Operation{
		Summary: "Yeni bir vergi oranı oluşturur.",
		Description: "Oranın bölgesi GÖVDEDE taşınır (tax_region_id); oran " +
			"yalnızca bu uçtan YAZILIR, bölgenin altındaki uç yalnızca okumadır.",
		RequestBody: d.RequestBody(createTaxRateRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan vergi oranı", d.Item(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRates, openapi.Operation{
		Summary: "Bir bölgenin vergi oranlarını listeler.",
		// "tax_region_id" ZORUNLUDUR ve eksikse handler 422 döner; limit/offset
		// ise HİÇ OKUNMAZ (liste [writeAll] ile yazılır). İkisini de yazmak,
		// istemciye çalışmayan bir sayfalama vaat etmek olurdu.
		Parameters: []openapi.Parameter{
			sorguParametresi("tax_region_id", tipDize, true,
				"Oranların okunacağı vergi bölgesi; zorunludur."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Vergi oranları", d.List(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRate, openapi.Operation{
		Summary: "Vergi oranını kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Vergi oranı", d.Item(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodPut, pathAdminRate, openapi.Operation{
		Summary: "Vergi oranının verilen alanlarını günceller.",
		Description: "Yöntem PUT olsa da semantik KISMİDİR: gövdede verilmeyen " +
			"alan değişmez. Kodu KALDIRMAK için code alanına boş dize gönderilir.",
		RequestBody: d.RequestBody(updateTaxRateRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen vergi oranı", d.Item(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRate, openapi.Operation{
		Summary: "Vergi oranını yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Vergi oranı silindi"),
		},
	})
}

// describeKurallar vergi oranı kuralı uçlarını anlatır.
func describeKurallar(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRateRules, openapi.Operation{
		Summary: "Vergi oranına bir kural ekler.",
		Description: "Oran kimliği YOLDAN alınır; gövdede ikinci kez taşınsaydı " +
			"yol ile gövde çelişebilirdi. reference_id BAŞKA bir modülün " +
			"kimliğidir ve bu modül varlığını doğrulamaz.",
		RequestBody: d.RequestBody(createTaxRateRuleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Eklenen kural", d.Item(taxRateRuleDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRateRules, openapi.Operation{
		Summary: "Vergi oranının kurallarını listeler.",
		Responses: map[string]any{
			"200": openapi.Response("Oranın kuralları", d.List(taxRateRuleDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRateRule, openapi.Operation{
		Summary: "Vergi oranı kuralını yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Kural silindi"),
		},
	})
}

// sayfaParametreleri limit/offset sorgu parametrelerini üretir.
//
// Yalnızca [pageParams] çağıran uçlarda kullanılır; bu modülde tek böyle uç
// bölge listelemesidir. Oran ve kural listeleri sayfalanmaz.
func sayfaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi, false,
			"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
		sorguParametresi("offset", tipTamSayi, false, "Atlanacak kayıt sayısı."),
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// zorunlu bayrağı BİLİNÇLİ olarak parametredir: bu modülde tek bir zorunlu
// sorgu parametresi vardır (GET /admin/v1/tax-rates'in tax_region_id'si) ve
// onu isteğe bağlı göstermek, istemci üretecinin çağrılabilir sandığı ama her
// zaman 422 dönen bir metot üretmesi demekti.
func sorguParametresi(ad, tip string, zorunlu bool, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Required:    zorunlu,
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}

// bosYanit GÖVDESİZ bir yanıt tanımı üretir.
//
// [openapi.Response] her zaman bir gövde şeması yazar; 204'ün gövdesi ise
// YOKTUR (bkz. admin.go, corehttp.WriteJSON'a nil verilen çağrılar). Boş bir
// şema yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve istemci
// üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
