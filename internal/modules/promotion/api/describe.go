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

// Describe promotion'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (campaignRequest,
// promotionDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek
// için dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini genişletmek
// olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir hâle
// gelirdi. Sorgu parametreleri de aynı sebeple burada durur — hangi
// parametrenin GERÇEKTEN okunduğunu bilen kod api.go ve admin.go içindedir;
// anlatım başka bir pakete taşınsaydı ikisi sessizce ayrışırdı. Modülün
// [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [API]'ye bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa [API.Routes] hiç çağrılmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # Yönetim gövdesi ile müşteri gövdesi AYRI anlatılır
//
// GET /store/v1/promotions/{code} yönetim gövdesini ([promotionDTO]) DEĞİL,
// dar bir kuponu ([storeCouponDTO]) döner. İkisini tek bileşenle anlatmak,
// şemanın müşteriye durum, sayaç ve kampanya bütçesi vaat etmesi demek olurdu;
// bunların hiçbiri o uçtan gitmez.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin POST /admin/v1/campaigns, bütçesiz bir
// kampanyada boş bırakılan budget_currency_code'u da ister. Alan ADLARI ve
// TİPLERİ doğrudur, yani şema yanlış bir alan uydurmaz; yalnızca fazla şey
// ister. Doğru çözüm ÇEKİRDEKTEDİR (istek gövdeleri için ayrı bir "required"
// politikası); tag'lere omitempty serpiştirmek zorunluluğu servisin
// doğrulamasından json etiketine taşır ve ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describeKampanyalar(d)
	describePromosyonlar(d)
	describeYontemVeKurallar(d)
	describeKullanimlar(d)

	d.Describe(http.MethodPost, "/admin/v1/promotions/compute", openapi.Operation{
		Summary: "Verilen sepet bağlamı için indirimleri hesaplar.",
		Description: "YAN ETKİSİZDİR: hiçbir sayaç ve bütçe değişmez, bu yüzden " +
			"yanıt 200'dür. Gövdenin alan adları modüller arası interop şemasıyla " +
			"birebir aynıdır; yönetim ekranında denenen istek sepet akışında da " +
			"aynı sonucu verir.",
		RequestBody: d.RequestBody(computeRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("İndirim hesabı", d.Item(computeResultDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/promotions/{code}", openapi.Operation{
		Summary: "Kupon kodunu doğrular ve indirimin türünü, hedefini, değerini döner.",
		Description: "Müşteri gövdesi DARDIR: promosyonun durumu, kullanım sayacı, " +
			"kampanyası ve kural koşulları BURADA YOKTUR. Kod geçersizse sebep de " +
			"söylenmez — taslak, pasif, süresi geçmiş, bütçesi bitmiş ve hiç var " +
			"olmayan kod aynı 404'ü döner.",
		Responses: map[string]any{
			"200": openapi.Response("Kupon", d.Item(storeCouponDTO{})),
		},
	})
}

// describeKampanyalar kampanya uçlarını anlatır.
func describeKampanyalar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/campaigns", openapi.Operation{
		Summary:     "Yeni bir kampanya oluşturur.",
		RequestBody: d.RequestBody(campaignRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan kampanya", d.Item(campaignDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/campaigns", openapi.Operation{
		Summary:    "Kampanyaları sayfalayarak listeler.",
		Parameters: sayfaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Kampanyalar", d.List(campaignDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/campaigns/{id}", openapi.Operation{
		Summary: "Kampanyayı kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Kampanya", d.Item(campaignDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/campaigns/{id}", openapi.Operation{
		Summary: "Kampanyanın tanımını yerine koyar.",
		// Gövde oluşturmadakiyle AYNI tiptir: "yerine koyma" tam gövde ister
		// ve iki ayrı tip, aynı kaydın iki farklı şeklini anlatırdı.
		RequestBody: d.RequestBody(campaignRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen kampanya", d.Item(campaignDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/campaigns/{id}", openapi.Operation{
		Summary: "Kampanyayı yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Kampanya silindi"),
		},
	})
}

// describePromosyonlar promosyon uçlarını anlatır.
func describePromosyonlar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/promotions", openapi.Operation{
		Summary:     "Yeni bir promosyon oluşturur.",
		RequestBody: d.RequestBody(promotionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan promosyon", d.Item(promotionDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/promotions", openapi.Operation{
		Summary: "Promosyonları sayfalayarak listeler.",
		Parameters: append(sayfaParametreleri(),
			sorguParametresi("status", tipDize,
				"Listeyi tek bir yayın durumuyla sınırlar (draft | active | inactive)."),
			sorguParametresi("campaign_id", tipDize,
				"Listeyi tek bir kampanyanın promosyonlarıyla sınırlar.")),
		Responses: map[string]any{
			"200": openapi.Response("Promosyonlar", d.List(promotionDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/promotions/{id}", openapi.Operation{
		Summary: "Promosyonu kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Promosyon", d.Item(promotionDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/promotions/{id}", openapi.Operation{
		Summary:     "Promosyonun tanımını yerine koyar.",
		RequestBody: d.RequestBody(promotionRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen promosyon", d.Item(promotionDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/promotions/{id}", openapi.Operation{
		Summary: "Promosyonu yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Promosyon silindi"),
		},
	})
}

// describeYontemVeKurallar uygulama yöntemi ve kural uçlarını anlatır.
func describeYontemVeKurallar(d *openapi.Doc) {
	d.Describe(http.MethodPut, "/admin/v1/promotions/{id}/application-method",
		openapi.Operation{
			Summary: "Promosyonun uygulama yöntemini yazar.",
			Description: "Yerine koymadır: promosyonun zaten bir yöntemi varsa " +
				"üzerine yazılır, bu yüzden yanıt 201 değil 200'dür.",
			RequestBody: d.RequestBody(applicationMethodRequest{}),
			Responses: map[string]any{
				"200": openapi.Response("Yazılan uygulama yöntemi",
					d.Item(applicationMethodDTO{})),
			},
		})

	d.Describe(http.MethodDelete, "/admin/v1/promotions/{id}/application-method",
		openapi.Operation{
			Summary: "Promosyonun uygulama yöntemini yumuşak siler.",
			Responses: map[string]any{
				"204": bosYanit("Uygulama yöntemi silindi"),
			},
		})

	d.Describe(http.MethodGet, "/admin/v1/promotions/{id}/rules", openapi.Operation{
		Summary: "Promosyonun kurallarını listeler.",
		// Sayfalama parametresi YOKTUR: liste [writeItems] ile yazılır ve
		// handler sorgu dizesini hiç okumaz. Yazmak, istemciye çalışmayan bir
		// sayfalama vaat etmek olurdu.
		Responses: map[string]any{
			"200": openapi.Response("Promosyonun kuralları", d.List(promotionRuleDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/promotions/{id}/rules", openapi.Operation{
		Summary:     "Promosyona kural ekler.",
		RequestBody: d.RequestBody(promotionRuleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Eklenen kural", d.Item(promotionRuleDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/promotion-rules/{id}", openapi.Operation{
		Summary: "Promosyon kuralını yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Kural silindi"),
		},
	})
}

// describeKullanimlar kullanım defteri uçlarını anlatır.
func describeKullanimlar(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/promotions/{id}/redemptions", openapi.Operation{
		Summary:    "Promosyonun kullanım defterini sayfalayarak listeler.",
		Parameters: sayfaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Kullanım kayıtları", d.List(redemptionDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/promotions/{id}/redeem", openapi.Operation{
		Summary: "Promosyonu bir iş kaydı referansı için kullanır.",
		Description: "İDEMPOTENTTİR: aynı referansla ikinci istek sayacı artırmaz " +
			"ve var olan kaydı döner. Yanıt bu yüzden 201 değil 200'dür — istek " +
			"her zaman YENİ bir kayıt yaratmaz.",
		RequestBody: d.RequestBody(redeemRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Kullanım kaydı", d.Item(redemptionDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/promotions/{id}/release", openapi.Operation{
		Summary: "Bir kullanımı serbest bırakır ve sayaçları geri alır.",
		Description: "İDEMPOTENTTİR: ikinci çağrı hata vermez ve sayaçlar ikinci " +
			"kez düşmez. Yanıttaki \"released\" alanı BU İSTEKTE bir şeyin geri " +
			"alınıp alınmadığını bildirir; false başarısızlık DEĞİLDİR.",
		RequestBody: d.RequestBody(releaseRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Geri alma sonucu", d.Item(releaseResultDTO{})),
		},
	})
}

// sayfaParametreleri limit/offset sorgu parametrelerini üretir.
//
// Yalnızca [pageParams] çağıran uçlarda kullanılır; sayfalanmayan listeler
// ([writeItems] ile yazılanlar) sorgu dizesini hiç okumaz.
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
// (bkz. [intParam], [stringParam]).
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
// YOKTUR (bkz. admin.go, corehttp.WriteJSON'a nil verilen çağrılar). Boş bir
// şema yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve istemci
// üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
