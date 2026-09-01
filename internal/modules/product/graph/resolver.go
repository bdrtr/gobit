package graph

import (
	"context"
	"strings"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Hata kodları; istemci bunlara errors.CodeOf ile (ya da GraphQL yanıtındaki
// extensions.code alanından) bakabilir.
const (
	codeBadArgument = "product_graphql_bad_argument"
	codePanic       = "product_graphql_panic"
	// codeGovdeCokBuyuk, GraphQL zarfında değil çekirdeğin hata zarfında
	// görünür: belge çalıştırıcıya hiç ulaşmamıştır (bkz. govdeSiniri).
	codeGovdeCokBuyuk = "product_graphql_body_too_large"
)

// Resolver GraphQL kökünü vitrin servisine bağlar.
//
// Resolver'lar SERVİSİ çağırır, depoya inmez ve yeni SQL yazmaz; gerekçe için
// bkz. paket belgesi.
type Resolver struct {
	svc Storefront
}

// NewResolver verilen vitrin servisiyle kök resolver üretir.
func NewResolver(svc Storefront) *Resolver { return &Resolver{svc: svc} }

// Üretilen sözleşmenin karşılandığı DERLEME zamanında sabitlenir: şemaya bir
// alan eklendiğinde ya da imzası değiştiğinde hata testte değil derlemede
// görünür — şema-önce (schema-first) üretimin asıl kazancı budur.
var _ ResolverRoot = (*Resolver)(nil)

// Query üretilen kodun beklediği sorgu resolver'ını döner.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

// Variant üretilen kodun beklediği varyant resolver'ını döner.
func (r *Resolver) Variant() VariantResolver { return &variantResolver{r} }

// queryResolver kök sorguları karşılar.
type queryResolver struct{ *Resolver }

// variantResolver varyantın alan çözümlerini karşılar.
type variantResolver struct{ *Resolver }

// Products yayındaki ürünleri listeler.
//
// # Satış kanalı SORGUDAN GELMEZ
//
// Kanallar [SalesChannelIDsFromContext] ile isteğin DOĞRULANMIŞ kimliğinden
// okunur; şemada böyle bir argüman yoktur ve olmayacaktır (bkz. schema_test.go).
// Argüman olsaydı süzgeç bir yetkilendirme olmaktan çıkıp görüntüleme
// tercihine dönüşürdü.
//
// # Sayfalama varsayılanı BURADA DEĞİL
//
// Verilmeyen limit/offset 0 olarak geçirilir; 0'ı varsayılana çevirmek ve üst
// sınıra kırpmak servisin işidir (bkz. service normalizePaging). Burada bir
// varsayılan seçmek, aynı kuralın ikinci bir tanımı olurdu ve iki okuma yüzeyi
// farklı sayfa boyutları döndürmeye başlardı.
//
// # Boş metin argümanı VERİLMEMİŞ sayılır
//
// q ve collectionId kırpıldıktan sonra boş kalıyorsa servise nil geçer; kural
// REST'teki stringParam ile AYNIDIR ve tekil uçtaki [tekilSecici] de aynı
// ayrımı yapar. Olduğu gibi geçirmek iki yüzeyi ayrıştırırdı: `collectionId:
// ""` boş bir kimlikle süzer ve hiçbir şey döndürmez, `q: ""` ise her satırı
// eşleştiren bir ILIKE taramasını sonuca hiç dokunmadan ekler — ikisi de
// istemcinin niyeti değildir ve ikisi de sessizdir.
func (r *queryResolver) Products(
	ctx context.Context,
	limit, offset *int,
	q, collectionID *string,
) (*ProductList, error) {
	result, err := r.svc.ListStoreProducts(ctx, service.StoreListOptions{
		CollectionID:    kirpilmisIsaretci(collectionID),
		Search:          kirpilmisIsaretci(q),
		SalesChannelIDs: SalesChannelIDsFromContext(ctx),
		Limit:           intDegeri(limit),
		Offset:          intDegeri(offset),
	})
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// Product tek bir vitrin ürününü kimliğe ya da handle'a göre döner.
//
// Kanal süzgeci listedekiyle AYNIDIR: listede gizlenen bir ürünü tekil uçtan
// göstermek gizlemeyi tümüyle anlamsız kılardı ve vitrin adresleri handle
// taşıdığı için tahmin edilebilir olan tam da bu sorgudur.
func (r *queryResolver) Product(ctx context.Context, id, handle *string) (*service.StoreProduct, error) {
	secici, err := tekilSecici(id, handle)
	if err != nil {
		return nil, err
	}

	product, err := r.svc.GetStoreProduct(ctx, secici, SalesChannelIDsFromContext(ctx))
	if err != nil {
		return nil, err
	}

	return &product, nil
}

// PriceSet varyantın fiyat setini (pricing modülünün kaydı) döner.
//
// Alan neden ELLE çözülüyor: kayıt bu modüle query.Record olarak gelir, JSON
// skalarının taşıyıcısı ise map[string]any'dir. İkisi Go'da atanabilir ama
// üreteç için AYNI tip değildir, bu yüzden alan otomatik bağlanamaz.
// Alternatif, çekirdeğin query.Record'una GraphQL serileştirmesi öğretmekti;
// o yol bir modülün sunum kaygısını çekirdeğe taşır ve çekirdeği bir GraphQL
// kütüphanesine bağlardı (Prensip 2.4'ün ruhu). Buradaki dönüşüm bedelsizdir:
// atama, kopya değil.
func (r *variantResolver) PriceSet(_ context.Context, obj *service.StoreVariant) (map[string]any, error) {
	return obj.PriceSet, nil
}

// InventoryItem varyantın stok kalemini (inventory modülünün kaydı) döner.
//
// Gerekçe [variantResolver.PriceSet] ile aynıdır.
func (r *variantResolver) InventoryItem(_ context.Context, obj *service.StoreVariant) (map[string]any, error) {
	return obj.InventoryItem, nil
}

// tekilSecici id/handle argümanlarından servise verilecek seçiciyi üretir.
//
// Servis ikisini TEK parametrede alır ve öneke bakarak ayırır (prod_… kimlik,
// gerisi handle); bu yüzden burada yapılan tek şey "tam olarak biri verilmiş
// mi" sorusudur. İkisini birden kabul edip birine öncelik vermek, çelişkili
// bir isteği sessizce yorumlamak olurdu: istemci handle'ı yazdığını sanırken
// kimliğin yanıtını alırdı.
//
// Boş ve yalnızca boşluktan oluşan değer VERİLMEMİŞ sayılır; aksi hâlde
// istemci `id: ""` göndererek "iki argümandan biri verildi" testini geçer ve
// hatayı bir katman ötede, çok daha kapalı bir mesajla alırdı.
func tekilSecici(id, handle *string) (string, error) {
	kimlik := kirp(id)
	ad := kirp(handle)

	switch {
	case kimlik != "" && ad != "":
		return "", coreerrors.Invalid(codeBadArgument,
			"id ve handle birlikte verilemez; yalnızca biri verilmelidir")
	case kimlik != "":
		return kimlik, nil
	case ad != "":
		return ad, nil
	default:
		return "", coreerrors.Invalid(codeBadArgument,
			"id ya da handle argümanlarından biri verilmelidir")
	}
}

// kirp isteğe bağlı metin argümanını kırpar; verilmemişse boş dize döner.
func kirp(v *string) string {
	if v == nil {
		return ""
	}

	return strings.TrimSpace(*v)
}

// kirpilmisIsaretci isteğe bağlı metin argümanını kırpar; boş kalıyorsa nil döner.
//
// [kirp] ile aynı kuralı SÜZGEÇ argümanlarına uygular. Ayrı bir fonksiyon
// olmasının sebebi dönüş tipidir: süzgeçler servise işaretçiyle gider ve
// "verilmedi" ile "boş verildi" ayrımını taşıyan tek şey nil'dir.
//
// Dönen işaretçi ARGÜMANIN kendisi değil, kırpılmış değerin kopyasıdır;
// çağıranın verdiği işaretçiyi geçirmek " tişört " gibi bir değeri kırpılmamış
// hâliyle servise sokardı.
func kirpilmisIsaretci(v *string) *string {
	kirpilmis := kirp(v)
	if kirpilmis == "" {
		return nil
	}

	return &kirpilmis
}

// intDegeri isteğe bağlı tam sayı argümanını okur; verilmemişse 0 döner.
//
// 0, servis için "varsayılanı uygula" demektir; bkz. [queryResolver.Products].
func intDegeri(v *int) int {
	if v == nil {
		return 0
	}

	return *v
}
