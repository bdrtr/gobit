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
	tipDize      = "string"
	tipTamSayi   = "integer"
	tipMantiksal = "boolean"
)

// Describe cart'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (createCartRequest,
// cartDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek için
// dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini genişletmek
// olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir hâle gelirdi.
// Modülün [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Register hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # İki yüzey de anlatılır
//
// Vitrin uçları bir mağaza istemcisinin (storefront, SDK), /admin/v1 uçları
// yönetim panelinin ihtiyacıdır. Yönetim yüzeyi YALNIZCA OKUMADIR (bkz.
// admin.go); anlatımı da bu yüzden hiçbir istek gövdesi taşımaz — gövdesi
// olmayan bir yazma ucu değil, gövdesi OLMAYAN bir okuma ucu anlatılır.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin POST /store/v1/carts, misafir sepetinde boş
// bırakılan customer_id ve email'i de ister. Alan ADLARI ve TİPLERİ doğrudur,
// yani şema yanlış bir alan uydurmaz; yalnızca fazla şey ister. Sınırın
// yazılması bilinçlidir: eksik olduğunu bilmek, eksik olduğunu sanmamaktan
// iyidir. Doğru çözüm ÇEKİRDEKTEDİR (istek gövdeleri için ayrı bir "required"
// politikası); tag'lere omitempty serpiştirmek zorunluluğu servisin
// doğrulamasından json etiketine taşır ve ikisi sessizce ayrışırdı.
//
// # Sorgu parametresini yalnızca yönetim listesi okur
//
// Vitrin sepeti uçlarının hiçbiri sorgu dizesine bakmaz (bkz. store.go) ve
// şemaları da parametre duyurmaz; sorgu dizesini okuyan TEK uç
// GET /admin/v1/carts'tır (bkz. admin.go ve [parsePage]). Okunmayan bir
// parametreyi şemaya yazmak, istemciye ÇALIŞMAYAN bir özellik vaat etmek
// olurdu: istemci üreteci metoda bir argüman koyar, çağıran onu doldurur ve
// sunucu sessizce yok sayar.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/carts", openapi.Operation{
		Summary:     "Yeni sepet oluşturur.",
		RequestBody: d.RequestBody(createCartRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan sepet", d.Item(cartDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/carts/{id}", openapi.Operation{
		Summary: "Sepeti satırları, adresleri ve kargo yöntemleriyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Sepet ve çocukları", d.Item(cartDetailDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/carts/{id}", openapi.Operation{
		Summary:     "Sepetin e-postasını ve müşterisini günceller.",
		RequestBody: d.RequestBody(updateCartRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen sepet", d.Item(cartDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/carts/{id}", openapi.Operation{
		Summary: "Sepeti siler.",
		Responses: map[string]any{
			"204": bosYanit("Sepet silindi"),
		},
	})

	describeSatirlar(d)
	describeAdresler(d)
	describeKargo(d)
	describeYonetim(d)
}

// describeSatirlar sepet satırı uçlarını anlatır.
func describeSatirlar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/carts/{id}/line-items", openapi.Operation{
		Summary:     "Sepete satır ekler.",
		RequestBody: d.RequestBody(addLineItemRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Eklenen satır", d.Item(lineItemDTO{})),
		},
	})

	d.Describe(http.MethodPatch, "/store/v1/carts/{id}/line-items/{line_item_id}", openapi.Operation{
		Summary:     "Sepet satırının adedini günceller.",
		RequestBody: d.RequestBody(updateLineItemRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen satır", d.Item(lineItemDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/carts/{id}/line-items/{line_item_id}", openapi.Operation{
		Summary: "Sepet satırını kaldırır.",
		Responses: map[string]any{
			"204": bosYanit("Satır kaldırıldı"),
		},
	})
}

// describeAdresler sepet adresi uçlarını anlatır.
//
// İki uç AYNI gövdeyi ([addressRequest]) ve AYNI kaydı ([addressDTO]) taşır;
// ayıran tek şey adresin türüdür ve o, yanıttaki "type" alanında görünür.
func describeAdresler(d *openapi.Doc) {
	d.Describe(http.MethodPut, "/store/v1/carts/{id}/shipping-address", openapi.Operation{
		Summary:     "Sepetin kargo adresini yazar.",
		RequestBody: d.RequestBody(addressRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Yazılan kargo adresi", d.Item(addressDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/store/v1/carts/{id}/billing-address", openapi.Operation{
		Summary:     "Sepetin fatura adresini yazar.",
		RequestBody: d.RequestBody(addressRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Yazılan fatura adresi", d.Item(addressDTO{})),
		},
	})
}

// describeKargo kargo yöntemi uçlarını anlatır.
func describeKargo(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/carts/{id}/shipping-methods", openapi.Operation{
		Summary:     "Sepete kargo yöntemi ekler.",
		RequestBody: d.RequestBody(addShippingMethodRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Eklenen kargo yöntemi", d.Item(shippingMethodDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/carts/{id}/shipping-methods/{shipping_method_id}",
		openapi.Operation{
			Summary: "Kargo yöntemini sepetten kaldırır.",
			Responses: map[string]any{
				"204": bosYanit("Kargo yöntemi kaldırıldı"),
			},
		})
}

// describeYonetim /admin/v1 sepet uçlarını anlatır.
//
// Yüzey YALNIZCA OKUMADIR (bkz. admin.go), dolayısıyla hiçbir uçta requestBody
// yoktur. Bunu "eksik anlatım" sanmamak için burada yazılıyor: sepeti
// değiştiren tek taraf müşteridir ve yönetimde bir yazma ucu YOKTUR.
func describeYonetim(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/carts", openapi.Operation{
		Summary: "Sepetleri müşteri, bölge ve tamamlanma durumuna göre sayfalar.",
		// Parametreler [Handler.adminListCarts]'ın OKUDUKLARIDIR; başkası
		// eklenirse istemciye çalışmayan bir süzgeç vaat edilmiş olurdu.
		Parameters: []openapi.Parameter{
			sorguParametresi("customer_id", tipDize,
				"Sepetleri tek bir müşteriyle sınırlar."),
			sorguParametresi("region_id", tipDize,
				"Sepetleri tek bir bölgeyle sınırlar."),
			sorguParametresi("completed", tipMantiksal,
				"true yalnızca tamamlanmış, false yalnızca açık sepetleri döner."),
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			// Kayıt [cartDetailDTO] DEĞİL [cartDTO]'dur: liste ucu satırları,
			// adresleri ve kargo yöntemlerini YÜKLEMEZ (N+1'e açardı). Detay
			// şemasını yazmak, istemciye hiç dolmayacak alanlar vaat etmek olurdu.
			"200": openapi.Response("Sepet sayfası", d.List(cartDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/carts/{id}", openapi.Operation{
		Summary: "Tek bir sepeti satırları, adresleri ve kargo yöntemleriyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Sepet ve çocukları", d.Item(cartDetailDTO{})),
		},
	})
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// Hiçbiri zorunlu DEĞİLDİR: verilmediğinde handler varsayılanla devam eder
// (bkz. [parsePage] ve [Handler.adminListCarts]).
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
// YOKTUR (bkz. store.go, corehttp.WriteJSON'a nil verilen çağrılar). Boş bir
// şema yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve istemci
// üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
