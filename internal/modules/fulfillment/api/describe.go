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
	semaTip      = "type"
	semaOgeler   = "items"
	tipDize      = "string"
	tipTamSayi   = "integer"
	tipMantiksal = "boolean"
	tipDizi      = "array"
)

// Describe fulfillment'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (createProfileRequest,
// fulfillmentDTO …) ve şema onlardan yansımayla türetilir. Tipleri
// anlatabilmek için dışa açmak, yalnızca belge üretmek uğruna modülün
// yüzeyini genişletmek olurdu: dışa açık bir tip sözleşmedir ve dışarıdan
// kurulabilir hâle gelirdi. Sorgu parametreleri de aynı sebeple burada durur —
// hangi parametrenin GERÇEKTEN okunduğunu bilen kod api.go ve handlers.go
// içindedir; anlatım başka bir pakete taşınsaydı ikisi sessizce ayrışırdı.
// Modülün [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa [Handler.Routes] hiç çağrılmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # ANLATILMAYAN uçlar: kargo seçeneği CRUD'u
//
// POST /admin/v1/shipping-options, GET /admin/v1/shipping-options, GET ve
// PATCH /admin/v1/shipping-options/{id} gövdeleriyle anlatılMAZ. Sebep bu
// modülde değil BELGENİN AD ALANINDADIR: bileşen adı Go tip adından türetilir
// (baş harf büyütülür, "DTO" son eki atılır) ve [optionDTO] "Option" adını
// ister — aynı adı product modülünün models.Option'ı da ister. İki FARKLI tip
// aynı bileşen adını istediğinde belge üretimi hata döner, yani bu dört ucu
// anlatmak /openapi.json'un TAMAMINI çökertirdi.
//
// Tipi yeniden adlandırmak bu paketin tek başına alabileceği bir karar
// DEĞİLDİR: bileşen adı yayımlanan sözleşmedir ve düzeltme hangi tarafta
// yapılırsa yapılsın öteki modülün üretilmiş istemcilerini kırar. Uçlar bu
// yüzden anlatılmadan bırakıldı — belgede yolu, metodu ve güvenliğiyle
// görünürler, yalnızca gövdeleri olmaz.
//
// DELETE /admin/v1/shipping-options/{id} ise anlatılır: 204 döner ve gövdesi
// olmadığı için [optionDTO]'ya hiç dokunmaz.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin POST /admin/v1/shipping-profiles, boş
// bırakılabilen metadata'yı da ister. Alan ADLARI ve TİPLERİ doğrudur, yani
// şema yanlış bir alan uydurmaz; yalnızca fazla şey ister. Doğru çözüm
// ÇEKİRDEKTEDİR (istek gövdeleri için ayrı bir "required" politikası);
// tag'lere omitempty serpiştirmek zorunluluğu servisin doğrulamasından json
// etiketine taşır ve ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminProviders, openapi.Operation{
		Summary: "Kayıtlı kargo sağlayıcılarının kimliklerini listeler.",
		Responses: map[string]any{
			// Kayıt bir DTO değil, düz bir dizedir (bkz.
			// [Handler.listProviders]); zarf yine de liste zarfıdır.
			"200": openapi.Response("Sağlayıcı kimlikleri", d.List("")),
		},
	})

	describeProfiller(d)
	describeDepoPolitikalari(d)
	describeSecenekler(d)
	describeUygunluk(d)
	describeGonderiler(d)
}

// describeProfiller kargo profili uçlarını anlatır.
func describeProfiller(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminProfiles, openapi.Operation{
		Summary:     "Yeni bir kargo profili oluşturur.",
		RequestBody: d.RequestBody(createProfileRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan profil", d.Item(profileDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminProfiles, openapi.Operation{
		Summary: "Kargo profillerini sayfalayarak listeler.",
		Parameters: append(sayfaParametreleri(),
			sorguParametresi("type", tipDize,
				"Listeyi tek bir profil türüyle sınırlar.")),
		Responses: map[string]any{
			"200": openapi.Response("Kargo profilleri", d.List(profileDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminProfile, openapi.Operation{
		Summary: "Kargo profilini kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Kargo profili", d.Item(profileDTO{})),
		},
	})

	d.Describe(http.MethodPatch, pathAdminProfile, openapi.Operation{
		Summary:     "Kargo profilinin verilen alanlarını günceller.",
		RequestBody: d.RequestBody(updateProfileRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen profil", d.Item(profileDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminProfile, openapi.Operation{
		Summary: "Kargo profilini yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Profil silindi"),
		},
	})
}

// describeDepoPolitikalari depo seçim politikası uçlarını anlatır.
func describeDepoPolitikalari(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminLocations, openapi.Operation{
		Summary:    "Depo kargo politikalarını öncelik sırasıyla listeler.",
		Parameters: sayfaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Depo kargo politikaları", d.List(locationDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminLocation, openapi.Operation{
		Summary: "Bir deponun kargo politikasını döner.",
		Responses: map[string]any{
			"200": openapi.Response("Depo kargo politikası", d.Item(locationDTO{})),
		},
	})

	d.Describe(http.MethodPut, pathAdminLocation, openapi.Operation{
		Summary:     "Bir deponun kargo politikasını yazar ya da üzerine yazar.",
		RequestBody: d.RequestBody(setLocationRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Yazılan politika", d.Item(locationDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminLocation, openapi.Operation{
		Summary: "Bir deponun kargo politikasını siler ve depoyu varsayılana döndürür.",
		Responses: map[string]any{
			"204": bosYanit("Politika silindi"),
		},
	})
}

// describeSecenekler kargo seçeneği uçlarından anlatılabilenleri anlatır.
//
// Seçeneğin KENDİ gövdesini taşıyan dört uç bilinçli olarak dışarıdadır;
// gerekçe [Describe] godoc'undadır. Burada kalanlar seçenek kaydına hiç
// dokunmayanlardır: silme (gövdesiz) ve kurallar (kendi DTO'suyla).
func describeSecenekler(d *openapi.Doc) {
	d.Describe(http.MethodDelete, pathAdminOption, openapi.Operation{
		Summary: "Kargo seçeneğini yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Seçenek silindi"),
		},
	})

	d.Describe(http.MethodPost, pathAdminOptionRules, openapi.Operation{
		Summary:     "Kargo seçeneğine uygunluk kuralı ekler.",
		RequestBody: d.RequestBody(createRuleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Eklenen kural", d.Item(ruleDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminOptionRules, openapi.Operation{
		Summary: "Kargo seçeneğinin uygunluk kurallarını listeler.",
		// Sayfalama parametresi YOKTUR: liste [writeList] ile yazılır ve
		// handler sorgu dizesini hiç okumaz. Yazmak, istemciye çalışmayan bir
		// sayfalama vaat etmek olurdu.
		Responses: map[string]any{
			"200": openapi.Response("Seçeneğin kuralları", d.List(ruleDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminOptionRule, openapi.Operation{
		Summary: "Uygunluk kuralını yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Kural silindi"),
		},
	})
}

// describeUygunluk iki uygunluk listelemesini anlatır.
//
// İkisi AYNI kaydı anlatır ama farklı tiplerle ([quotedOptionDTO] ve
// [storeOptionDTO]); ayrımın ne olduğu her ikisinin açıklamasında yazılıdır.
func describeUygunluk(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminEligible, openapi.Operation{
		Summary: "Verilen sepet bağlamı için uygun kargo seçeneklerini yönetim " +
			"gösterimiyle listeler.",
		Description: "Yönetim gösterimi admin_only işaretli seçenekleri DE içerir " +
			"ve her kayıtta provider_id, shipping_profile_id, is_return ve " +
			"admin_only alanlarını taşır. AYNI seçenek vitrin ucunda " +
			"(GET /store/v1/shipping-options) ya hiç görünmez ya da bu alanlar " +
			"olmadan görünür: iki uç aynı kataloğu okur, gösterimleri farklıdır. " +
			"Sepet olguları burada GÜVENİLİR sayılır, dolayısıyla subtotal, " +
			"item_count ve total_weight'e bağlı kuralı olan seçenekler de listelenir.",
		Parameters: uygunlukParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Uygun seçenekler (yönetim gösterimi)",
				d.List(quotedOptionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreOptions, openapi.Operation{
		Summary: "Verilen sepet bağlamı için uygun kargo seçeneklerini vitrin " +
			"gösterimiyle listeler.",
		Description: "Vitrin gösterimi yönetim gösteriminden DARDIR ve aynı kayıt " +
			"iki uçta farklı görünür: admin_only işaretli seçenekler burada HİÇ " +
			"listelenmez, listelenenlerde de provider_id, shipping_profile_id, " +
			"is_return ve admin_only alanları yazılmaz " +
			"(bkz. GET /admin/v1/shipping-options/eligible). Sepet olguları " +
			"(subtotal, item_count, total_weight) istemcinin İDDİASIDIR: bu üç " +
			"olguya bağlı kuralı olan seçenekler listeden tümüyle çıkarılır ve " +
			"\"calculated\" seçeneklerin ücreti yalnızca GÖSTERİMDİR — gerçek ücret " +
			"ödeme adımında sepetin gerçek olgularıyla belirlenir.",
		Parameters: uygunlukParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Uygun seçenekler (vitrin gösterimi)",
				d.List(storeOptionDTO{})),
		},
	})
}

// describeGonderiler gönderi uçlarını anlatır.
func describeGonderiler(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminFulfillments, openapi.Operation{
		Summary:     "Sağlayıcıda yeni bir gönderi açar.",
		RequestBody: d.RequestBody(createFulfillmentRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Açılan gönderi", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminFulfillments, openapi.Operation{
		Summary: "Gönderileri sayfalayarak listeler.",
		Parameters: append(sayfaParametreleri(),
			sorguParametresi("reference", tipDize,
				"Listeyi tek bir iş kaydı referansıyla sınırlar."),
			sorguParametresi("status", tipDize,
				"Listeyi tek bir gönderi durumuyla sınırlar.")),
		Responses: map[string]any{
			"200": openapi.Response("Gönderiler", d.List(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminFulfillment, openapi.Operation{
		Summary: "Gönderiyi kalemleriyle birlikte döner.",
		Responses: map[string]any{
			"200": openapi.Response("Gönderi", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminCancel, openapi.Operation{
		Summary: "Gönderiyi iptal eder ve güncel hâlini döner.",
		Description: "İDEMPOTENTTİR: ikinci çağrı da 200 ve aynı kaydı döner. " +
			"Gövde ALMAZ; iptal edilecek gönderi yoldan seçilir.",
		Responses: map[string]any{
			"200": openapi.Response("İptal edilmiş gönderi", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminShip, openapi.Operation{
		Summary: "Gönderiyi kargoya verilmiş olarak işaretler.",
		// Gövde İSTEĞE BAĞLIDIR: bazı taşıyıcılar takip numarasını sonradan
		// verir ve handler boş gövdeyi hata saymaz (bkz.
		// [decodeOptionalBody]). Zorunlu göstermek, istemci üretecinin
		// gövdesiz çağrıyı hiç mümkün kılmaması demekti.
		RequestBody: istegeBagliGovde(d, shipRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Kargoya verilmiş gönderi", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminDeliver, openapi.Operation{
		Summary: "Gönderiyi teslim edilmiş olarak işaretler.",
		Responses: map[string]any{
			"200": openapi.Response("Teslim edilmiş gönderi", d.Item(fulfillmentDTO{})),
		},
	})
}

// uygunlukParametreleri uygunluk listelemesinin sorgu parametreleridir.
//
// Liste [parseEligibilityQuery]'nin OKUDUKLARIDIR ve iki yüzeyde de aynıdır.
// "include_admin_only" ve "trusted_facts" BİLİNÇLİ OLARAK YOKTUR: ikisi de bir
// GÜVEN kararıdır ve değeri handler'ın hangi yüzeye ait olduğuna göre
// sabitlenir. Şemaya yazmak, vitrinden gelen bir istemciye tek bir parametreyle
// yönetime özel seçenekleri açabileceğini ima etmek olurdu.
//
// Serbest kural bağlamı ([service.ListOptionsInput.Attributes]) da yoktur;
// HTTP uçlarının hiçbiri onu okumaz.
func uygunlukParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("region_id", tipDize,
			"Seçenekleri sepetin bölgesiyle sınırlar."),
		sorguParametresi("currency_code", tipDize,
			"Ücretin hesaplanacağı para birimi."),
		sorguParametresi("country_code", tipDize,
			"Teslimat ülkesi; ülkeye bağlı kurallar bununla değerlendirilir."),
		{
			Name: "shipping_profile_id",
			In:   "query",
			// TEKRARLANABİLİR: bir sepette birden çok profile bağlı ürün
			// bulunabilir ve hepsi aynı anda sorulur (bkz. query["…"] okuması).
			Schema: map[string]any{
				semaTip:    tipDizi,
				semaOgeler: map[string]any{semaTip: tipDize},
			},
			Description: "Sepetteki ürünlerin kargo profilleri; birden çok kez verilebilir.",
		},
		sorguParametresi("subtotal", tipTamSayi,
			"Sepetin ara toplamı (minor unit)."),
		sorguParametresi("item_count", tipTamSayi, "Sepetteki kalem adedi."),
		sorguParametresi("total_weight", tipTamSayi, "Sepetin toplam ağırlığı."),
		sorguParametresi("is_return", tipMantiksal,
			"true ise iade seçenekleri listelenir."),
	}
}

// sayfaParametreleri limit/offset sorgu parametrelerini üretir.
//
// Yalnızca [parsePage] çağıran uçlarda kullanılır; sayfalanmayan listeler
// ([writeList] ile yazılanlar) sorgu dizesini hiç okumaz.
func sayfaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi,
			"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
		sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// Hiçbiri zorunlu DEĞİLDİR: verilmediklerinde handler varsayılanla devam eder
// (bkz. [parseInt64Param], [parseBoolParam]).
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}

// istegeBagliGovde gövdesi ZORUNLU OLMAYAN bir istek tanımı üretir.
//
// [openapi.Doc.RequestBody] gövdeyi her zaman zorunlu işaretler ve bu, yazma
// uçlarının neredeyse tamamı için doğrudur. Sevk bildiriminde ise değildir:
// handler boş gövdeyi kabul eder ve takip bilgisi olmadan da sevk yazılır.
// Zorunlu göstermek, istemci üretecinin gövdesiz çağrıyı hiç üretmemesi
// demekti.
func istegeBagliGovde(d *openapi.Doc, v any) map[string]any {
	govde := d.RequestBody(v)
	govde["required"] = false

	return govde
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
