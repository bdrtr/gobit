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
	semaTip        = "type"
	semaBicim      = "format"
	tipDize        = "string"
	tipTamSayi     = "integer"
	bicimTarihSaat = "date-time"
)

// Describe pricing'in TÜM uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (priceSetDTO,
// priceListRequest …) ve şema onlardan yansımayla türetilir. Tipleri
// anlatabilmek için dışa açmak, yalnızca belge üretmek uğruna modülün
// yüzeyini genişletmek olurdu: dışa açık bir tip sözleşmedir ve dışarıdan
// kurulabilir hâle gelirdi. Sorgu parametreleri de handler'ın GERÇEKTEN
// okuduklarıdır ve o okuma bu paketin api.go dosyasındadır ([pageParams],
// [calculateQuery]); anlatım başka bir pakette dursaydı ikisi sessizce
// ayrışırdı. Modülün [openapi.Describer] uygulaması bu yüzden buraya delege
// eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir.
// Metodu [API]'ye bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Routes hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # Neden yönetim yüzeyi de anlatılıyor
//
// pricing'in on altı ucundan on beşi /admin/v1'dedir ve fiyat YAZMANIN tek
// yolu orasıdır. Yalnızca vitrini anlatmak, üretilen istemcide fiyat yazma
// metotlarını gövdesiz ve dönüşsüz bırakırdı: o istemciyle fiyat
// KURULAMAZDI. Anlatılmamış bir uç geçerli bir modeldir ama işe yaramaz bir
// kütüktür de; burada bırakılmıyor.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin PUT /admin/v1/price-lists/{id} boş
// bırakılabilen description ve status alanlarını da ister. Alan ADLARI ve
// TİPLERİ doğrudur, yani şema yanlış bir alan uydurmaz; yalnızca fazla şey
// ister. Doğru çözüm ÇEKİRDEKTEDİR (istek gövdeleri için ayrı bir "required"
// politikası); tag'lere omitempty serpiştirmek zorunluluğu servisin
// doğrulamasından json etiketine taşır ve ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describePriceSets(d)
	describePrices(d)
	describePriceLists(d)
	describePriceRules(d)
	describeStore(d)
}

// describePriceSets fiyat kabı uçlarını anlatır.
func describePriceSets(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/price-sets", openapi.Operation{
		Summary:     "Yeni bir fiyat kabı oluşturur ve gövdedeki fiyatları yazar.",
		RequestBody: d.RequestBody(createPriceSetRequest{}),
		Responses: map[string]any{
			// 201, handler'ın GERÇEKTEN yazdığı koddur (bkz.
			// [API.createPriceSet]); yanıt kap ile birlikte az önce yazılmış
			// fiyatları da taşır.
			"201": openapi.Response("Oluşturulan fiyat kabı", d.Item(priceSetDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-sets", openapi.Operation{
		Summary: "Fiyat kaplarını sayfalayarak listeler.",
		// [API.listPriceSets] sorgu dizesinden YALNIZCA bu ikisini okur
		// ([pageParams]); başka bir parametre yazmak istemciye çalışmayan bir
		// süzgeç vaat etmek olurdu.
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			// Liste yanıtında fiyat YOKTUR (bkz. [toPriceSetSummaryDTO]) ama
			// şema aynı tipten türer: "prices" omitempty taşır, dolayısıyla
			// zorunlu değildir ve istemci onu isteğe bağlı görür.
			"200": openapi.Response("Fiyat kabı sayfası", d.List(priceSetDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-sets/{id}", openapi.Operation{
		Summary: "Tek bir fiyat kabını fiyatlarıyla döner.",
		Responses: map[string]any{
			"200": openapi.Response("Fiyat kabı ve fiyatları", d.Item(priceSetDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/price-sets/{id}", openapi.Operation{
		Summary: "Fiyat kabını ve fiyatlarını siler.",
		Responses: map[string]any{
			"204": bosYanit("Fiyat kabı silindi"),
		},
	})
}

// describePrices bir kabın fiyatlarını okuyan ve yazan uçları anlatır.
func describePrices(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/price-sets/{id}/prices", openapi.Operation{
		Summary: "Bir kabın bütün fiyatlarını kurallarıyla döner.",
		// Uç SAYFALANMAZ ([API.listPrices] sorgu dizesini hiç okumaz) ama
		// zarfı yine liste zarfıdır: istemcinin gördüğü şekil uç noktaya göre
		// değişmez (bkz. [writeItems]).
		Responses: map[string]any{
			"200": openapi.Response("Kabın fiyatları", d.List(priceDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/price-sets/{id}/prices", openapi.Operation{
		Summary:     "Bir kabın fiyat kümesini topluca değiştirir.",
		RequestBody: d.RequestBody(setPricesRequest{}),
		Responses: map[string]any{
			// 200'dür, 201 DEĞİL: uç yeni bir kaynak yaratmaz, var olan kabın
			// fiyat kümesini yerine koyar ve yazdığı kümeyi LİSTE zarfıyla
			// döner ([API.setPrices] → [writeItems]). 201 yazmak, istemci
			// üretecinde "oluşturuldu" dalına düşen bir metot üretirdi.
			"200": openapi.Response("Kabın yeni fiyat kümesi", d.List(priceDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-sets/{id}/calculate", openapi.Operation{
		Summary: "Verilen bağlamda kabın geçerli fiyatını seçer ve tutarı hesaplar.",
		// Kural bağlamı "attr_" ÖNEKLİ parametrelerle taşınır ve bu, şemada
		// bir parametre olarak YAZILAMAZ: OpenAPI parametreyi ADIYLA tanımlar,
		// önekle değil. Uydurma bir "attr_*" girdisi yazmak, istemci
		// üretecinde tam olarak o adı taşıyan çalışmayan bir argüman üretirdi.
		// Doğrusu, adı olan üç parametreyi anlatmak ve öneki açıklamada
		// söylemektir: istemci geliştiricisi okur, üreteç yalan üretmez.
		Description: "Kural bağlamı `" + paramAttrPrefix + "` önekli sorgu " +
			"parametreleriyle verilir (örn. `" + paramAttrPrefix + "region_id=reg_1`); " +
			"önek soyulur ve kalan ad kuralın baktığı alan adı olur. " +
			"Tanınmayan (öneksiz ve ayrılmış olmayan) bir parametre hatadır.",
		Parameters: []openapi.Parameter{
			sorguParametresi(paramCurrencyCode, tipDize,
				"İstenen para birimi (ISO 4217); verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi(paramQuantity, tipTamSayi,
				"Hesaplamanın yapılacağı adet; verilmezse servisin varsayılanı uygulanır."),
			zamanParametresi(paramAt,
				"Hesaplama anı (RFC 3339); verilmezse şimdi. Saat dilimi "+
					"ofsetindeki \"+\" karakteri yüzde kodlanmalıdır."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Seçilen fiyat ve hesaplanan tutar",
				d.Item(calculatedPriceDTO{})),
		},
	})
}

// describePriceLists fiyat listesi uçlarını anlatır.
//
// Oluşturma ve güncelleme AYNI gövdeyi ([priceListRequest]) taşır; ayıran tek
// şey metottur. PUT kısmi güncelleme DEĞİLDİR: gövdede olmayan alanlar
// sıfırlanır (bkz. [API.updatePriceList]).
func describePriceLists(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/price-lists", openapi.Operation{
		Summary:     "Yeni bir fiyat listesi oluşturur.",
		RequestBody: d.RequestBody(priceListRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan fiyat listesi", d.Item(priceListDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-lists", openapi.Operation{
		Summary:    "Fiyat listelerini sayfalayarak listeler.",
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Fiyat listesi sayfası", d.List(priceListDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-lists/{id}", openapi.Operation{
		Summary: "Tek bir fiyat listesini döner.",
		Responses: map[string]any{
			"200": openapi.Response("Fiyat listesi", d.Item(priceListDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/price-lists/{id}", openapi.Operation{
		Summary:     "Fiyat listesinin TÜM alanlarını yazar; gövdede olmayanlar sıfırlanır.",
		RequestBody: d.RequestBody(priceListRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen fiyat listesi", d.Item(priceListDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/price-lists/{id}", openapi.Operation{
		Summary: "Fiyat listesini siler.",
		Responses: map[string]any{
			"204": bosYanit("Fiyat listesi silindi"),
		},
	})
}

// describePriceRules fiyat kuralı uçlarını anlatır.
func describePriceRules(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/prices/{price_id}/rules", openapi.Operation{
		Summary: "Bir fiyatın geçerlilik kurallarını döner.",
		Responses: map[string]any{
			"200": openapi.Response("Fiyatın kuralları", d.List(priceRuleDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/prices/{price_id}/rules", openapi.Operation{
		Summary: "Bir fiyata geçerlilik kuralı ekler.",
		// Gövde TEK bir kuraldır, kural listesi değil: handler gelen gövdeyi
		// tek elemanlı bir dilime sarar (bkz. [API.createPriceRule]). Liste
		// anlatmak, istemcinin gönderdiği ikinci kuralın sessizce
		// kaybolmasına yol açardı.
		RequestBody: d.RequestBody(ruleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Eklenen kural", d.Item(priceRuleDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/price-rules/{id}", openapi.Operation{
		Summary: "Fiyat kuralını siler.",
		Responses: map[string]any{
			"204": bosYanit("Kural silindi"),
		},
	})
}

// describeStore modülün TEK vitrin ucunu anlatır.
//
// Yanıt tipi yönetim ucuyla aynıdır ([priceSetDTO]) ama İÇERİĞİ dardır:
// vitrin yalnızca gösterilebilir fiyatları görür (bkz. [API.storeGetPriceSet]).
// Ayrı bir DTO açmak şemaya yeni bir bileşen eklerdi ve alan kümesi
// birebir aynı olurdu; fark verinin kendisindedir, şeklinde değil.
func describeStore(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/store/v1/price-sets/{id}", openapi.Operation{
		Summary: "Bir fiyat kabını mağazada gösterilebilir fiyatlarıyla döner.",
		Responses: map[string]any{
			"200": openapi.Response("Fiyat kabı ve vitrin fiyatları", d.Item(priceSetDTO{})),
		},
	})
}

// sayfalamaParametreleri [pageParams]'ın okuduğu sorgu parametrelerini döner.
//
// İkisi de zorunlu DEĞİLDİR: verilmediklerinde servis kendi varsayılanını
// uygular.
func sayfalamaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi,
			"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
		sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}

// zamanParametresi RFC 3339 damgası taşıyan bir sorgu parametresi tanımlar.
//
// Biçim şemada AÇIKÇA yazılır: düz "string" demek, istemci üretecinin alanı
// serbest metin yapması ve çağıranın kendi biçimini uydurması demekti — oysa
// [timeParam] RFC 3339 dışındaki her değeri reddeder.
func zamanParametresi(ad, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tipDize, semaBicim: bicimTarihSaat},
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
