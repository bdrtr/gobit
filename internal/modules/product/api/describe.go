package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
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

// Describe product'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Sorgu parametreleri handler'ın GERÇEKTEN okuduğu parametrelerdir ve o okuma
// bu paketteki store.go ile [paging] içindedir. Anlatım başka bir pakette
// dursaydı, parametre listesi okumadan uzaklaşır ve ikisi sessizce ayrışırdı.
// Modülün [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Register hiç çalışmamışken de belge üretilebilir.
//
// # İki yüzey de anlatılır
//
// Vitrin uçları bir mağaza istemcisinin, /admin/v1 uçları yönetim panelinin ve
// katalog entegrasyonlarının ihtiyacıdır.
//
// # Silme uçları 204 DEĞİL 200 döner
//
// Yönetim tarafındaki her DELETE bir GÖVDE yazar ([deleted] kaydını, tekil
// zarf içinde; bkz. admin.go). Alışkanlıkla "204, gövdesiz" yazmak burada
// somut bir yalan olurdu: istemci üreteci gövdeyi hiç okumayan bir metot
// üretir ve silmenin gerçekten olduğunu bildiren "deleted" alanı istemciye
// hiç ulaşmazdı.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/store/v1/products", openapi.Operation{
		Summary: "Yayındaki ürünleri fiyat ve stok bilgisiyle listeler.",
		// Parametreler handler'ın OKUDUKLARIDIR, isteyebileceklerimiz değil:
		// [Handler.storeListProducts] yalnızca bu dördünü okur.
		//
		// "sales_channel_id" BİLİNÇLİ OLARAK YOKTUR ve eklenmemelidir: kanal
		// isteğin publishable anahtarından gelir, sorgu dizesinden değil
		// (bkz. [salesChannelIDs]). Şemaya yazmak, hem okunmayan bir
		// parametre vaat etmek hem de istemciye kanal süzgecinin
		// atlatılabileceğini ima etmek olurdu.
		Parameters: []openapi.Parameter{
			sorguParametresi("collection_id", tipDize,
				"Ürünleri tek bir koleksiyonla sınırlar."),
			sorguParametresi("q", tipDize,
				"Başlık ve handle üzerinde serbest metin araması."),
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Vitrin ürünleri", d.List(service.StoreProduct{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/products/{id}", openapi.Operation{
		Summary: "Tek bir vitrin ürününü kimliğe ya da handle'a göre döner.",
		// Yol parametresi çekirdek tarafından desenden de türetilir; burada
		// ELLE yazılmasının tek sebebi açıklamasıdır. Ad "id"dir ama değer
		// handle da olabilir ("/store/v1/products/tisort") ve bunu yalnızca
		// handler bilir; türetici desene bakıp söyleyemez.
		Parameters: []openapi.Parameter{{
			Name:        "id",
			In:          "path",
			Required:    true,
			Schema:      map[string]any{semaTip: tipDize},
			Description: "Ürün kimliği (prod_…) ya da vitrin adresindeki handle.",
		}},
		Responses: map[string]any{
			"200": openapi.Response("Vitrin ürünü", d.Item(service.StoreProduct{})),
		},
	})

	describeYonetimUrunler(d)
	describeYonetimVaryantlar(d)
	describeYonetimSecenekler(d)
	describeYonetimBaglar(d)
	describeYonetimSatisKanallari(d)
	describeYonetimTaksonomi(d)
}

// describeYonetimUrunler /admin/v1 ürün uçlarını anlatır.
//
// Yanıt kaydı vitrindeki [service.StoreProduct] DEĞİL [models.Product]'tır:
// yönetim servisi fiyat ve stok zenginleştirmesi yapmaz (bkz. writeItem
// çağrıları). Vitrin tipini yazmak, istemciye hiç dolmayacak "price_set" ve
// "inventory_item" alanları vaat etmek olurdu.
func describeYonetimUrunler(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products", openapi.Operation{
		Summary:     "Yeni ürünü seçenekleri, varyantları ve görselleriyle oluşturur.",
		RequestBody: d.RequestBody(createProductRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan ürün", d.Item(models.Product{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products", openapi.Operation{
		Summary: "Kataloğun tamamını taslaklar dâhil süzerek listeler.",
		// Parametreler [Handler.adminListProducts]'ın OKUDUKLARIDIR. Vitrin
		// listesinden farkı iki tanedir ve ikisi de yönetime özgüdür: "status"
		// taslak/arşiv kayıtlarını görmeyi sağlar (vitrin yalnızca yayındakini
		// döner), "expand" ise varyant ve seçenekleri yanıta ekler.
		Parameters: []openapi.Parameter{
			sorguParametresi("collection_id", tipDize,
				"Ürünleri tek bir koleksiyonla sınırlar."),
			sorguParametresi("handle", tipDize,
				"Ürünleri tek bir handle ile sınırlar."),
			sorguParametresi("q", tipDize,
				"Başlık ve handle üzerinde serbest metin araması."),
			sorguParametresi("status", tipDize,
				"Yayın durumu süzgeci: draft | published | archived."),
			sorguParametresi("expand", tipMantiksal,
				"true ise varyant, seçenek, görsel ve taksonomi kayıtları da döner."),
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Ürün sayfası", d.List(models.Product{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}", openapi.Operation{
		Summary: "Tek bir ürünü kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Ürün", d.Item(models.Product{})),
		},
	})

	d.Describe(http.MethodPatch, "/admin/v1/products/{id}", openapi.Operation{
		Summary:     "Ürünün yalnızca verilen alanlarını günceller.",
		RequestBody: d.RequestBody(updateProductRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen ürün", d.Item(models.Product{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/products/{id}", openapi.Operation{
		Summary: "Ürünü siler.",
		Responses: map[string]any{
			"200": openapi.Response("Silme kaydı", d.Item(deleted{})),
		},
	})
}

// describeYonetimVaryantlar /admin/v1 varyant uçlarını anlatır.
func describeYonetimVaryantlar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products/{id}/variants", openapi.Operation{
		Summary:     "Ürüne yeni varyant ekler.",
		RequestBody: d.RequestBody(createVariantRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan varyant", d.Item(models.Variant{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}/variants", openapi.Operation{
		Summary: "Ürünün varyantlarını seçenek değerleriyle listeler.",
		// Handler seçenek değerlerini HER ZAMAN yükler (WithOptionValues:true),
		// yani "expand" gibi bir anahtar okumaz; yalnızca sayfalama okunur.
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Varyant sayfası", d.List(models.Variant{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/variants/{id}", openapi.Operation{
		Summary: "Tek bir varyantı kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Varyant", d.Item(models.Variant{})),
		},
	})

	d.Describe(http.MethodPatch, "/admin/v1/variants/{id}", openapi.Operation{
		Summary:     "Varyantın yalnızca verilen alanlarını günceller.",
		RequestBody: d.RequestBody(updateVariantRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen varyant", d.Item(models.Variant{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/variants/{id}", openapi.Operation{
		Summary: "Varyantı siler.",
		Responses: map[string]any{
			"200": openapi.Response("Silme kaydı", d.Item(deleted{})),
		},
	})
}

// describeYonetimSecenekler /admin/v1 seçenek ve seçenek değeri uçlarını
// anlatır.
func describeYonetimSecenekler(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products/{id}/options", openapi.Operation{
		Summary:     "Ürüne yeni bir seçenek ekseni ekler.",
		RequestBody: d.RequestBody(createOptionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan seçenek", d.Item(models.Option{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}/options", openapi.Operation{
		Summary: "Ürünün seçenek eksenlerini değerleriyle listeler.",
		// Sayfalama parametresi BİLİNÇLİ olarak yoktur: [Handler.adminListOptions]
		// sorgu dizesine hiç bakmaz ve sonucun tamamını tek sayfa gibi yazar.
		// "limit" duyurmak, istemcinin gönderdiği ama sunucunun yok saydığı bir
		// argüman üretirdi — bir ürünün seçenek sayısı zaten avuç içi kadardır.
		Responses: map[string]any{
			"200": openapi.Response("Seçenek listesi", d.List(models.Option{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-options/{id}/values", openapi.Operation{
		Summary:     "Seçeneğe yeni bir değer ekler.",
		RequestBody: d.RequestBody(optionValueRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Eklenen seçenek değeri", d.Item(models.OptionValue{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/product-options/{id}", openapi.Operation{
		Summary: "Seçeneği değerleriyle birlikte siler.",
		Responses: map[string]any{
			"200": openapi.Response("Silme kaydı", d.Item(deleted{})),
		},
	})
}

// describeYonetimBaglar varyantın fiyat/stok bağ uçlarını anlatır.
//
// # Bilinen sınır: [linkRequest] iki alan taşır, her uç BİRİNİ okur
//
// Gövde şeması GERÇEK DTO'dan türetilir ve o DTO ikisini birden taşır: fiyat
// ucu yalnızca "price_set_id"yi, stok ucu yalnızca "inventory_item_id"yi
// okur (bkz. [Handler.adminSetPriceSet], [Handler.adminSetInventoryItem]).
// Yani şema, gönderilebilecek ama YOK SAYILACAK bir alan gösteriyor. Sınır
// bilinçli olarak yazıldı: düzeltmenin doğru yeri şema değil, DTO'yu uç başına
// ayırmaktır ve bu, anlatım işinin değil api katmanının kararıdır. Alan ADLARI
// ve TİPLERİ doğrudur; şema uydurma bir alan göstermez.
func describeYonetimBaglar(d *openapi.Doc) {
	d.Describe(http.MethodPut, "/admin/v1/variants/{id}/price-set", openapi.Operation{
		Summary:     "Varyantı bir fiyat kümesine bağlar.",
		RequestBody: d.RequestBody(linkRequest{}),
		Responses: map[string]any{
			// Yanıt bağın kendisi değil varyantın GÜNCEL bağ kümesidir; istemci
			// ikinci bir GET atmadan sonucu görür (bkz. writeVariantLinks).
			"200": openapi.Response("Varyantın güncel bağları", d.Item(service.VariantLinks{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/variants/{id}/price-set", openapi.Operation{
		Summary: "Varyantın fiyat kümesi bağını kaldırır.",
		Responses: map[string]any{
			"200": openapi.Response("Silme kaydı", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/variants/{id}/inventory-item", openapi.Operation{
		Summary:     "Varyantı bir stok kalemine bağlar.",
		RequestBody: d.RequestBody(linkRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Varyantın güncel bağları", d.Item(service.VariantLinks{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/variants/{id}/inventory-item", openapi.Operation{
		Summary: "Varyantın stok kalemi bağını kaldırır.",
		Responses: map[string]any{
			"200": openapi.Response("Silme kaydı", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/variants/{id}/links", openapi.Operation{
		Summary: "Varyantın fiyat kümesi ve stok kalemi bağlarını döner.",
		Responses: map[string]any{
			"200": openapi.Response("Varyantın bağları", d.Item(service.VariantLinks{})),
		},
	})
}

// describeYonetimSatisKanallari ürün–satış kanalı bağ uçlarını anlatır.
//
// Üçü de AYNI kaydı döner ([productSalesChannels]): bağ çoktan çoğa olduğu için
// istemcinin sorusu "hangi kanallardayım"dır ve cevap her istekten sonra
// güncel hâliyle verilir.
func describeYonetimSatisKanallari(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products/{id}/sales-channels", openapi.Operation{
		Summary:     "Ürünü bir satış kanalına bağlar.",
		RequestBody: d.RequestBody(linkSalesChannelRequest{}),
		Responses: map[string]any{
			// 201 DEĞİL 200: bağlama idempotenttir ve aynı çifti ikinci kez
			// göndermek yeni bir kayıt yaratmaz (bkz. [Handler.adminAddSalesChannel]).
			"200": openapi.Response("Ürünün güncel kanal bağları", d.Item(productSalesChannels{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/products/{id}/sales-channels/{sales_channel_id}",
		openapi.Operation{
			Summary: "Ürünün bir satış kanalı bağını kaldırır.",
			Responses: map[string]any{
				// Gövdesiz 204 DEĞİL: uç kalan bağların listesini döner.
				"200": openapi.Response("Ürünün güncel kanal bağları", d.Item(productSalesChannels{})),
			},
		})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}/sales-channels", openapi.Operation{
		Summary: "Ürünün bağlı olduğu satış kanallarını döner.",
		Responses: map[string]any{
			"200": openapi.Response("Ürünün kanal bağları", d.Item(productSalesChannels{})),
		},
	})
}

// describeYonetimTaksonomi koleksiyon, kategori ve etiket uçlarını anlatır.
func describeYonetimTaksonomi(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/product-collections", openapi.Operation{
		Summary:     "Yeni koleksiyon oluşturur.",
		RequestBody: d.RequestBody(createCollectionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan koleksiyon", d.Item(models.Collection{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-collections", openapi.Operation{
		Summary:    "Koleksiyonları sayfalayarak listeler.",
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Koleksiyon sayfası", d.List(models.Collection{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-categories", openapi.Operation{
		Summary:     "Yeni kategori oluşturur.",
		RequestBody: d.RequestBody(createCategoryRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan kategori", d.Item(models.Category{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-categories", openapi.Operation{
		Summary: "Kategorileri üst kategoriye göre süzerek listeler.",
		Parameters: append(sayfalamaParametreleri(),
			sorguParametresi("parent_id", tipDize,
				"Yalnızca bu kategorinin ALT kategorilerini döner.")),
		Responses: map[string]any{
			"200": openapi.Response("Kategori sayfası", d.List(models.Category{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-tags", openapi.Operation{
		Summary:     "Yeni etiket oluşturur.",
		RequestBody: d.RequestBody(createTagRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan etiket", d.Item(models.Tag{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-tags", openapi.Operation{
		Summary:    "Etiketleri sayfalayarak listeler.",
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Etiket sayfası", d.List(models.Tag{})),
		},
	})
}

// sayfalamaParametreleri [paging]'in okuduğu iki parametreyi döner.
//
// Yeni bir dilim her çağrıda üretilir: paylaşılan bir dilim, çağıranın append
// ile eklediği parametrenin başka bir ucun listesine sızmasına izin verirdi.
func sayfalamaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi,
			"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
		sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// Hiçbiri zorunlu DEĞİLDİR: verilmediğinde handler varsayılanla devam eder
// (bkz. [stringParam], [intParam], [boolParam]).
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}
