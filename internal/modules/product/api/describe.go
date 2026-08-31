package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/service"
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

// Describe product'ın VİTRİN uçlarını OpenAPI belgesine işler.
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
// # Neden yalnızca /store/v1
//
// Bir mağaza istemcisinin ihtiyacı budur ve kapsamı dar tutmak kalıbın
// oturduğunu küçük bir yüzeyde görmeyi sağlar. /admin/v1 yüzeyi (otuza yakın
// uç) AYRI bir iştir; anlatılmamış uç belgede yolu ve güvenliğiyle görünür.
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
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// Hiçbiri zorunlu DEĞİLDİR: dördü de verilmediğinde handler varsayılanla
// devam eder (bkz. [stringParam], [intParam]).
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}
