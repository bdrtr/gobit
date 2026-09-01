// Package graph product modülünün GraphQL vitrin okuma yüzeyidir.
//
// # Neden ikinci bir okuma yüzeyi
//
// Vitrin istemcisi tek bir ürün sayfası için başlık, varyantlar, fiyat ve
// stoğu birlikte ister; REST'te bu, sabit bir gövdenin tamamını çekmek
// demektir. GraphQL istemcinin ne istediğini söylemesine izin verir ve
// çekirdeğin query katmanı (kök çek → link çöz → toplu getir → birleştir)
// zaten bu biçimde çalışır.
//
// # Kapsam DARDIR
//
// Yalnızca okuma: products ve product. Mutation ve yönetim yüzeyi YOKTUR
// (gerekçe schema.graphqls'in başında). Dar tutmak, kalıbın doğru oturduğunu
// önce küçük bir yüzeyde görmek içindir.
//
// # Servise İNER, depoya İNMEZ
//
// Resolver'lar [service.Service]'in vitrin metotlarını çağırır; repository'ye
// inmek ya da yeni SQL yazmak YASAKTIR. Satış kanalı görünürlük kuralı TEK
// bir yerde yaşar ve ikinci bir uygulama sessizce ayrışır: bu depodaki
// hataların tamamı "kural bir yerde tanımlı, başka yerde uygulanmamış"
// sınıfındaydı — arama eklentisi aynı tuzağı bilinçle atlatmıştı (eklenti
// yalnızca kimlik indeksler, kayıtları product getirir).
//
// # Koruma
//
// Uç /store/v1 altındadır ve bu bir yerleşim tercihi değil, koruma
// tercihidir: publishable anahtar doğrulaması ve hız sınırı o öneke bağlı
// yığından OTOMATİK gelir (bkz. corehttp.APIGuards) ve satış kanalı
// kimlikleri Principal'a oradan dolar. Ayrı bir önek açmak, kimlik ve kota
// kurallarını ikinci kez yazmak olurdu.
//
// # Üretilen kod
//
// generated.go ve models_gen.go gqlgen tarafından üretilir (make gen,
// yapılandırma ../gqlgen.yml) ve DÜZENLENMEZ. Elle yazılan her şey ayrı
// dosyalardadır: schema.graphqls, graph.go, resolver.go, handler.go.
package graph

import (
	"context"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Path GraphQL ucunun tam yoludur.
//
// Sabit dışa açıktır çünkü ucu bağlayan (api/routes.go) ile anlatan
// (api/describe.go) iki ayrı yerdir; yolun dize olarak tekrarlanması, birinin
// değişip diğerinin unutulması demek olurdu — belge o durumda var olmayan bir
// ucu anlatır ve OpenAPI testi bunu ancak yol adı ELLE eşleştiğinde yakalar.
const Path = "/store/v1/graphql"

// Storefront resolver'ların servisten ihtiyaç duyduğu YÜZEYDİR.
//
// Somut servis yerine arayüz kullanılmasının sebebi testtir: kanal süzgecinin
// gerçekten geçirildiğini doğrulamak için veritabanı gerekmemelidir. Yüzey
// bilinçli olarak İKİ metottur; genişlemesi, GraphQL'in vitrin servisinin
// dışına taşması demek olur.
type Storefront interface {
	ListStoreProducts(
		ctx context.Context,
		opts service.StoreListOptions,
	) (service.ListResult[service.StoreProduct], error)

	GetStoreProduct(
		ctx context.Context,
		idOrHandle string,
		salesChannelIDs []string,
	) (service.StoreProduct, error)
}

// ProductList vitrin listesinin GraphQL karşılığıdır.
//
// TAKMA ADDIR (alias), yeni bir tip DEĞİL: şema zarfı ile servisin döndürdüğü
// zarf aynı şeydir ve ikisini iki tip olarak tutmak, aralarında bir
// dönüştürücü — yani zarfın ikinci bir tanımını — gerektirirdi. Servis
// [service.ListResult]'a bir alan eklerse şema onu göstermez (şemaya
// yazılmayan alan görünmez), ama alan adı değişirse üretilen kod DERLENMEZ.
type ProductList = service.ListResult[service.StoreProduct]

// SalesChannelIDsFromContext isteğin bağlı olduğu satış kanallarını
// DOĞRULANMIŞ KİMLİKTEN okur.
//
// Kural burada, iki okuma yüzeyinin de (REST handler'ları ve GraphQL
// resolver'ları) ulaşabildiği tek yerde yaşar. İkinci bir kopya, tam da bu
// modülün kaçındığı hata sınıfını üretirdi: kuralın bir yerde düzeltilip
// diğerinde unutulması, yüzeylerden birinde katalog sızıntısı demektir.
//
// Kanal, istemcinin bildirdiği bir değer OLAMAZ; bu yüzden girdi yalnızca
// context'tir. Sorgu argümanı ya da sorgu dizesi kabul edilseydi süzgeç bir
// yetkilendirme olmaktan çıkıp görüntüleme tercihine dönüşür, elindeki
// herhangi bir publishable anahtarla gelen istemci BAŞKA bir kanalın
// katalogunu okuyabilirdi. Kimliği corehttp.RequireStore koyar; kanal listesi
// anahtarın kaydından gelir.
//
// Dönüş değerinin nil olup olmaması ANLAMLIDIR
// (bkz. [service.StoreListOptions]):
//
//   - Kimlik YOKSA nil dönülür. Bu, mağaza kimlik doğrulamasının bu kurulumda
//     hiç bağlanmamış olduğu durumdur (product tek başına dağıtılabilir) ve
//     süzgeç uygulanmaz; aksi hâlde auth'suz bir kurulumda vitrin sessizce
//     boşalırdı.
//   - Kimlik VARSA nil ASLA dönülmez: kanalsız bir kimlik BOŞ KÜME demektir,
//     "süzme yok" demek değildir. Bu iki durumu bir tutmak, kanalsız bir
//     kimliğe tüm kanalların katalogunu açardı.
func SalesChannelIDsFromContext(ctx context.Context) []string {
	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}

	if principal.SalesChannelIDs == nil {
		return []string{}
	}

	return principal.SalesChannelIDs
}
