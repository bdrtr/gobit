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

// tutarNotu gövdesinde tutar taşıyan uçların açıklamasına eklenen uyarıdır.
//
// Tip şemadan zaten "integer" çıkar, ama TİP tek başına NEDENİ söylemez:
// istemci geliştiricisi 100,50 TL'yi tam sayı olarak gönderemeyeceğini görüp
// kuruşu yuvarlamayı deneyebilir. Birimin yazılı olması, 10050 göndermesi
// gerektiğini söyleyen tek şeydir.
//
// Not ALAN düzeyinde değil İŞLEM düzeyinde durur: şema Go tipinden türetilir
// ve Go alanları açıklama taşımaz. Alan başına açıklama, çekirdeğe yeni bir
// mekanizma (etiketten okunan description) eklemek demekti ve bu modülün işi
// değildir.
const tutarNotu = "Tutarlar MINOR UNIT tam sayıdır (kuruş/cent): " +
	"100,50 TL için 10050 gönderilir; 100.50 gibi ondalıklı bir değer geçersizdir."

// Describe order'ın YÖNETİM uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (createReturnRequest,
// returnDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek
// için dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini genişletmek
// olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir hâle gelirdi.
// Sorgu parametreleri de handler'ın GERÇEKTEN okuduklarıdır ve o okuma bu
// paketteki admin.go ile [parsePage] içindedir; anlatım başka bir pakette
// dursaydı ikisi sessizce ayrışırdı. Modülün [openapi.Describer] uygulaması bu
// yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Register hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # ANLATILMAYAN BEŞ UÇ: "LineItem" bileşen adı ÇAKIŞIYOR
//
// Şema bileşeninin adı Go tip adından türetilir (baş harf büyür, "DTO" son eki
// atılır), yani [lineItemDTO] "LineItem" adını ister. Cart modülünün api
// paketinde de aynı adlı bir tip vardır ve o da anlatılmıştır. İki FARKLI tip
// aynı adı istediğinde [openapi.Doc.Build] hata döner ve belge TÜMDEN
// üretilemez: /openapi.json 500 olur ve yalnızca bu modülün değil cart'ın
// uçları da belgeden düşer. Yani çakışmayı görmezden gelmek, tek bir ucu değil
// belgenin tamamını kaybetmek demektir.
//
// [orderDetailDTO] satırlarını [lineItemDTO] ile taşır; bu yüzden onu döndüren
// beş uç anlatılmadan BIRAKILDI:
//
//   - GET  /store/v1/orders/{id}
//   - GET  /admin/v1/orders/{id}
//   - POST /admin/v1/orders/{id}/cancel
//   - POST /admin/v1/orders/{id}/complete
//   - POST /admin/v1/orders/{id}/archive
//
// Beşi de belgede yolu, metodu ve güvenliğiyle görünür; yalnızca gövdeleri
// olmaz. Çözüm tiplerden birini YENİDEN ADLANDIRMAKTIR ve o karar bu modülün
// içinden verilemez: bileşen adı istemci üreteçlerinin ÜRETTİĞİ SINIF ADIDIR,
// yani yayımlanmış sözleşmedir ve bir kez istemci üretildikten sonra
// değiştirmek kırıcıdır. İki modülü birden ilgilendiren bir kararı tek modülün
// içinden vermek, öteki modülün istemcisini habersiz kırardı.
//
// # Anlatılmayan bir uç daha YOKTUR
//
// Sipariş OLUŞTURMA ucu belgede de yoktur çünkü route'u hiç yoktur; gerekçe
// paket belgesindedir (tutarları istemciden alan bir uç, sıfır tutarlı sipariş
// yazılabilmesi demekti).
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin iade kaydı açarken opsiyonel olan "note" da
// istenir. Alan ADLARI ve TİPLERİ doğrudur, yani şema yanlış bir alan
// uydurmaz; yalnızca fazla şey ister. Sınırın yazılması bilinçlidir: eksik
// olduğunu bilmek, eksik olduğunu sanmamaktan iyidir.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders", openapi.Operation{
		Summary: "Siparişleri süzgeç ve sayfalamayla listeler.",
		// Parametreler handler'ın OKUDUKLARIDIR, isteyebileceklerimiz değil:
		// [Handler.adminListOrders] tam olarak bu beşini okur. Satırlar
		// listede YÜKLENMEZ, bu yüzden burada "expand" gibi bir parametre de
		// yoktur — olsaydı sunucunun yok saydığı bir özellik vaat ederdi.
		Parameters: []openapi.Parameter{
			sorguParametresi("customer_id", tipDize,
				"Siparişleri tek bir müşteriyle sınırlar."),
			sorguParametresi("region_id", tipDize,
				"Siparişleri tek bir bölgeyle sınırlar."),
			sorguParametresi("status", tipDize,
				"Durum süzgeci: pending, completed, archived ya da canceled."),
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Sipariş sayfası", d.List(orderDTO{})),
		},
	})

	describeIadeler(d)
	describeDegisimler(d)
	describeHasarlar(d)
}

// describeIadeler iade kaydı uçlarını anlatır.
func describeIadeler(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/returns", openapi.Operation{
		Summary:    "Siparişin iade kayıtlarını sayfalayarak listeler.",
		Parameters: sayfaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("İade kaydı sayfası", d.List(returnDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/returns", openapi.Operation{
		Summary:     "Siparişe iade kaydı açar.",
		Description: tutarNotu,
		RequestBody: d.RequestBody(createReturnRequest{}),
		Responses: map[string]any{
			// Handler 201 yazar (bkz. admin.go); yeni bir kayıt doğar.
			"201": openapi.Response("Açılan iade kaydı", d.Item(returnDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/returns/{returnId}", openapi.Operation{
		Summary: "İade kaydını kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("İade kaydı", d.Item(returnDTO{})),
		},
	})
}

// describeDegisimler değişim kaydı uçlarını anlatır.
func describeDegisimler(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/exchanges", openapi.Operation{
		Summary:    "Siparişin değişim kayıtlarını sayfalayarak listeler.",
		Parameters: sayfaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Değişim kaydı sayfası", d.List(exchangeDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/exchanges", openapi.Operation{
		Summary: "Siparişe değişim kaydı açar.",
		// Farkın İŞARETİ anlamlıdır ve tipten okunamaz; yazılmazsa istemci
		// müşteriye ödenecek farkı da tahsil edilecek gibi gösterebilir.
		Description: tutarNotu + " difference_due pozitifse fark müşteriden " +
			"tahsil edilir, negatifse müşteriye ödenir.",
		RequestBody: d.RequestBody(createExchangeRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Açılan değişim kaydı", d.Item(exchangeDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/exchanges/{exchangeId}", openapi.Operation{
		Summary: "Değişim kaydını kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Değişim kaydı", d.Item(exchangeDTO{})),
		},
	})
}

// describeHasarlar hasar/eksik kaydı uçlarını anlatır.
func describeHasarlar(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/claims", openapi.Operation{
		Summary:    "Siparişin hasar kayıtlarını sayfalayarak listeler.",
		Parameters: sayfaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Hasar kaydı sayfası", d.List(claimDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/claims", openapi.Operation{
		Summary: "Siparişe hasar/eksik kaydı açar.",
		// Tür şemada yalnızca "string"dir; kabul edilen iki değer handler'da
		// zorlanır (bkz. [Handler.adminCreateClaim]) ve burada yazılmasaydı
		// istemci geliştiricisi geçerli değeri deneme yanılmayla bulurdu.
		Description: tutarNotu + " type zorunludur ve \"refund\" ya da " +
			"\"replace\" olmalıdır; refund_amount yalnızca \"refund\" için anlamlıdır.",
		RequestBody: d.RequestBody(createClaimRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Açılan hasar kaydı", d.Item(claimDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/claims/{claimId}", openapi.Operation{
		Summary: "Hasar kaydını kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Hasar kaydı", d.Item(claimDTO{})),
		},
	})
}

// sayfaParametreleri [parsePage]'in okuduğu iki parametreyi döner.
//
// Satış sonrası listelerinin BAŞKA süzgeci yoktur: handler'lar yalnızca
// sipariş kimliğini yoldan alır (bkz. admin.go). Buraya bir "status" süzgeci
// yazmak, sunucunun yok sayacağı bir alan için istemci üretecine argüman
// koydurmak olurdu.
func sayfaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi,
			"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
		sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// Hiçbiri zorunlu DEĞİLDİR: verilmediğinde handler süzgeci hiç uygulamaz ya da
// varsayılanla devam eder (bkz. [parsePage], [parseInt64Param]).
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}
