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

// referansVeriNotu para birimi ve ülke uçlarının yazma yüzeyi OLMADIĞINI
// söyleyen açıklamadır.
//
// Ayrım şemaya yazılır çünkü tek başına yol listesi onu göstermez: istemci
// geliştiricisi GET /admin/v1/currencies'i görüp "demek ki POST da vardır"
// diye düşünür ve olmayan bir ucu bekler. Notun tek yerde durması da
// bilinçlidir — üç uçta üç kez yazılsaydı biri güncellenip ötekiler
// eskirdi.
const referansVeriNotu = "REFERANS VERİDİR ve yalnızca OKUNUR: kayıtlar " +
	"migration ile tohumlanır (ISO 4217 / ISO 3166-1 kopyası) ve HTTP " +
	"üzerinden oluşturulamaz, değiştirilemez, silinemez. Bu modülün yazma " +
	"yüzeyi yalnızca BÖLGEDEDİR; ülkede değişebilen tek şey hangi bölgeye " +
	"ait olduğudur ve o da bölgenin alt kaynağından yönetilir " +
	"(POST/DELETE /admin/v1/regions/{id}/countries)."

// Describe region'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (createRegionRequest,
// regionDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek
// için dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini
// genişletmek olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir
// hâle gelirdi. Sorgu parametreleri de burada durmalıdır, çünkü onları
// GERÇEKTEN okuyan kod ([pageParams], [optionalParam]) bu pakettedir;
// anlatım başka bir pakette dursaydı ikisi sessizce ayrışırdı. Modülün
// [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [API]'ye bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Register hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # Yazma yüzeyi yalnızca bölgededir
//
// Para birimi ve ülke uçları OKUMADIR; gerekçesi paket belgesindedir ve
// istemcinin görebilmesi için [referansVeriNotu] ile şemaya da yazılır.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: [createRegionRequest] omitempty taşımadığı için
// varsayılanı kabul edilebilen automatic_taxes ve tax_rate_bps de zorunlu
// görünür. Alan ADLARI ve TİPLERİ doğrudur, yani şema yanlış bir alan
// uydurmaz; yalnızca fazla şey ister. Doğru çözüm ÇEKİRDEKTEDİR (istek
// gövdeleri için ayrı bir "required" politikası); tag'lere omitempty
// serpiştirmek zorunluluğu servisin doğrulamasından json etiketine taşır ve
// ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describeBolgeler(d)
	describeBolgeUlkeleri(d)
	describeReferansVeri(d)
	describeVitrin(d)
}

// describeBolgeler bölgenin yönetim uçlarını anlatır.
func describeBolgeler(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRegions, openapi.Operation{
		Summary:     "Yeni bölge oluşturur.",
		RequestBody: d.RequestBody(createRegionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan bölge", d.Item(regionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegions, openapi.Operation{
		Summary:    "Bölgeleri sayfalayarak listeler.",
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Bölge sayfası", d.List(regionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegion, openapi.Operation{
		Summary: "Tek bir bölgeyi kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Bölge", d.Item(regionDTO{})),
		},
	})

	d.Describe(http.MethodPut, pathAdminRegion, openapi.Operation{
		Summary: "Bölgenin verilen alanlarını günceller.",
		Description: "Yöntem PUT olsa da semantik KISMİDİR: gövdede olmayan " +
			"alan değişmez. Tam gövde istenseydi tax_rate_bps göndermeyi " +
			"unutan bir istemci oranı sessizce sıfırlardı.",
		RequestBody: d.RequestBody(updateRegionRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen bölge", d.Item(regionDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRegion, openapi.Operation{
		Summary: "Bölgeyi siler.",
		Responses: map[string]any{
			"204": bosYanit("Bölge silindi"),
		},
	})
}

// describeBolgeUlkeleri bölge-ülke bağının uçlarını anlatır.
//
// Bunlar ülkenin KENDİSİNİ yazmaz, yalnızca hangi bölgeye ait olduğunu
// değiştirir; referans verinin yazılamazlığıyla çelişmezler (bkz.
// [referansVeriNotu]).
func describeBolgeUlkeleri(d *openapi.Doc) {
	// 201 döner çünkü oluşan şey BAĞDIR ve yanıt gövdesi bağın yeni hâlini
	// taşıyan ülke kaydıdır (bkz. [API.addCountry]). 200 yazmak, istemci
	// üretecinde yanlış dallanma üretirdi.
	d.Describe(http.MethodPost, pathAdminRegionCountries, openapi.Operation{
		Summary: "Bölgeye ülke ekler.",
		Description: "Ülke başka bir bölgeye aitse 409 döner; bir ülke aynı " +
			"anda yalnızca tek bir bölgeye bağlı olabilir.",
		RequestBody: d.RequestBody(addCountryRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Bölgeye bağlanan ülke", d.Item(countryDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegionCountries, openapi.Operation{
		Summary:     "Bölgeye bağlı ülkeleri sayfalayarak listeler.",
		Description: "Ülke kayıtları " + referansVeriNotu,
		Parameters:  sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Bölgenin ülkeleri", d.List(countryDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRegionCountry, openapi.Operation{
		Summary: "Ülkeyi bölgeden çıkarır.",
		Description: "Ülke kaydı SİLİNMEZ; yalnızca bölge bağı kaldırılır ve " +
			"region_id null olur.",
		Responses: map[string]any{
			"204": bosYanit("Ülke bölgeden çıkarıldı"),
		},
	})
}

// describeReferansVeri para birimi ve ülke OKUMA uçlarını anlatır.
func describeReferansVeri(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminCountries, openapi.Operation{
		Summary:     "Ülkeleri sayfalayarak listeler.",
		Description: "Ülke kayıtları " + referansVeriNotu,
		// "region_id" GERÇEKTEN okunur ([API.listCountries], [optionalParam])
		// ve boş dize ile "hiç verilmedi" AYRIDIR: boş bir kimlik istemcinin
		// hatasıdır ve servis onu reddeder, sessizce "süzme yok"a dönüşmez.
		Parameters: append(sayfalamaParametreleri(),
			sorguParametresi("region_id", tipDize,
				"Ülkeleri tek bir bölgeyle sınırlar. Verilip BOŞ bırakılırsa "+
					"süzgeç kalkmaz; istek 422 ile reddedilir.")),
		Responses: map[string]any{
			"200": openapi.Response("Ülke sayfası", d.List(countryDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminCurrencies, openapi.Operation{
		Summary:     "Para birimlerini sayfalayarak listeler.",
		Description: "Para birimi kayıtları " + referansVeriNotu,
		Parameters:  sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Para birimi sayfası", d.List(currencyDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminCurrency, openapi.Operation{
		Summary:     "Tek bir para birimini ISO kodundan döner.",
		Description: "Para birimi kayıtları " + referansVeriNotu,
		// Yol parametresi desenden de türetilir; ELLE yazılmasının tek sebebi
		// açıklamasıdır. Kodun ISO 4217 olduğunu ve büyük harfe
		// normalleştirildiğini yalnızca handler bilir.
		Parameters: []openapi.Parameter{{
			Name:        "code",
			In:          "path",
			Required:    true,
			Schema:      map[string]any{semaTip: tipDize},
			Description: "ISO 4217 para birimi kodu (örn. TRY).",
		}},
		Responses: map[string]any{
			"200": openapi.Response("Para birimi", d.Item(currencyDTO{})),
		},
	})
}

// describeVitrin bölgenin vitrin uçlarını anlatır.
//
// Vitrin gövdesi yönetim gövdesinden FARKLIDIR ve iki ayrı bileşen olarak
// görünmesi bilinçlidir: müşteriye giden kayıt para biriminin sembolünü ve
// ondalık basamağını taşır ama vergi oranını taşımaz. Tek bir bileşen
// kullanılsaydı istemci, vitrinde hiç dönmeyen tax_rate_bps alanını okuyabilir
// sanırdı.
func describeVitrin(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathStoreRegions, openapi.Operation{
		Summary: "Vitrinin seçebileceği bölgeleri para birimi ve ülkeleriyle listeler.",
		Description: "Tutarlar minor unit TAM SAYIDIR; istemci bölme " +
			"çarpanını (10^decimal_digits) aynı yanıttaki para biriminden " +
			"öğrenir. Sabit 100 varsayan bir istemci yen tutarlarını yüz kat " +
			"küçük gösterir.",
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Vitrin bölgeleri", d.List(storeRegionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreRegion, openapi.Operation{
		Summary: "Tek bir vitrin bölgesini kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Vitrin bölgesi", d.Item(storeRegionDTO{})),
		},
	})
}

// sayfalamaParametreleri limit ve offset sorgu parametrelerini döner.
//
// İkisi de her sayfalanan uçta AYNI anlamı taşır ([pageParams]); tek yerde
// durmaları, açıklamalardan birinin güncellenip ötekilerin eskimesini
// engeller. Dilim her çağrıda YENİDEN kurulur: çağıran ona ek parametre
// ekleyebiliyor (bkz. [describeReferansVeri]) ve paylaşılan bir dilime append
// etmek öteki uçların parametre listesini de değiştirebilirdi.
func sayfalamaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi,
			"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
		sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// Hiçbiri zorunlu DEĞİLDİR: verilmediklerinde handler süzgeci uygulamaz ya da
// servisin varsayılanıyla devam eder (bkz. [pageParams], [optionalParam]).
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}

// bosYanit GÖVDESİZ bir yanıt tanımı üretir.
//
// [openapi.Response] her zaman bir gövde şeması yazar; 204'ün gövdesi ise
// YOKTUR (bkz. corehttp.WriteJSON'a nil verilen çağrılar). Boş bir şema
// yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve istemci
// üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
