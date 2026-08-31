package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// Query sağlayıcılarının anladığı filtre anahtarları.
//
// Anahtarlar sözleşmedir: sağlayıcı tanımadığı bir filtre görürse
// errors.Invalid döner (ADR 0004), bu yüzden isimlerin tek bir yerde durması
// gerekir.
const (
	filterIDs          = "ids"
	filterID           = "id"
	filterProductID    = "product_id"
	filterProductIDs   = "product_ids"
	filterStatus       = "status"
	filterHandle       = "handle"
	filterCollectionID = "collection_id"
)

// Store yanıtında fiyat ve stok kayıtlarının yazıldığı anahtarlar.
const (
	keyPriceSet  = "price_set"
	keyInventory = "inventory_item"
)

// codeProviderNotFound Query katmanının "bu entity'nin sağlayıcısı container'da
// kayıtlı değil" hata kodudur.
//
// Kod BURADA TEKRARLANIR çünkü core/query'deki karşılığı unexported'dır ve
// paketler arası tek taşınabilir bağ hata kodudur (bkz. core/errors: "kod
// sözleşmenin parçasıdır"). Değeri değişirse bu modül sessizce daha az
// bağışlayıcı olur — vitrin fiyatsız dönmek yerine hata verir; sessizce daha
// hoşgörülü olmaktan yeğdir.
const codeProviderNotFound = "query_provider_not_found"

// StoreListOptions vitrin (store) ürün listelemesinin ölçütleridir.
//
// Durum filtresi YOKTUR: vitrin yalnızca yayındaki ürünleri gösterir ve bunun
// istemci tarafından değiştirilebilmesi taslak ürünleri sızdırırdı.
type StoreListOptions struct {
	CollectionID *string
	Search       *string
	// SalesChannelIDs isteğin bağlı olduğu satış kanallarıdır.
	//
	// Değer isteğin KİMLİĞİNDEN gelir (publishable anahtarın kanalları), sorgu
	// dizesinden DEĞİL; gerekçe için bkz. api/store.go.
	//
	// nil ile BOŞ AMA nil OLMAYAN dilim FARKLI şeyler söyler:
	//
	//   - nil: istek hiçbir satış kanalı kimliği taşımıyor (mağaza kimlik
	//     doğrulaması bu kurulumda bağlı değil). Süzgeç UYGULANMAZ.
	//   - boş dilim: kimlik var ama hiç kanalı yok. Süzgeç UYGULANIR ve yalnızca
	//     ataması olmayan ürünler görünür.
	//
	// İkinci durum pratikte oluşmaz — auth kanalsız bir publishable anahtarı
	// zaten reddeder — ama savunmacı davranış şudur: kanalsız bir kimliği
	// "süzme yok" saymak, o kimliğe TÜM kanalların katalogunu açardı. Boş kümeyi
	// kuralın kendisine uygulamak (hiçbir atama eşleşmez, atamasızlar kalır)
	// ayrı bir kod yolu açmaz ve sızdırma yönünde asla yanılmaz.
	SalesChannelIDs []string
	Limit           int
	Offset          int
}

// StoreProduct vitrin için hazırlanmış üründür.
type StoreProduct struct {
	models.Product
	// Variants gömülü ürünün Variants alanını GÖLGELER: JSON'da yalnızca bu
	// alan görünür ve varyantlar fiyat/stok bilgisiyle zenginleştirilmiştir.
	Variants []StoreVariant `json:"variants"`
}

// StoreVariant fiyat ve stok bilgisiyle zenginleştirilmiş varyanttır.
//
// PriceSet ve InventoryItem alanları BAŞKA MODÜLLERİN kayıtlarıdır ve bu modül
// onların şemasını bilmez: Query katmanından geldikleri gibi (gevşek tipli
// kayıt olarak) taşınırlar. Tip güvenliğinin burada yeniden kazanılmaması
// bilinçlidir; alanları yorumlamak, pricing/inventory şemasını bu modüle
// kopyalamak demek olurdu (ADR 0004'ün kabul edilmiş bedeli).
type StoreVariant struct {
	models.Variant
	PriceSet      query.Record `json:"price_set,omitempty"`
	InventoryItem query.Record `json:"inventory_item,omitempty"`
}

// enrichment tek bir varyantın başka modüllerden gelen ekleridir.
type enrichment struct {
	priceSet  query.Record
	inventory query.Record
}

// ListStoreProducts yayındaki ürünleri FİYAT ve STOK bilgisiyle listeler.
//
// Fiyat pricing, stok inventory modülünün verisidir; ikisi de import EDİLMEZ.
// Veri, varyant kimlikleri üzerinden link'lerle çözülür ve Query katmanının
// toplu (batch) sağlayıcı çağrılarıyla toplanır (ADR 0004).
//
// Sorgu sayısı ürün ya da varyant sayısından BAĞIMSIZDIR: katalog için sabit
// sayıda sorgu, zenginleştirme için genişletme başına bir link çözümü ve bir
// sağlayıcı çağrısı yapılır. N+1 yoktur.
//
// # Satış kanalı süzgeci
//
// Kural şudur: kanal ataması OLMAYAN ürün TÜM kanallarda görünür, ataması OLAN
// ürün YALNIZCA atandığı kanallarda görünür. Geriye uyumludur (bugünkü katalog
// bir gecede boşalmaz) ama süzme gerçekten çalışır: bir ürün A kanalına
// atandığı an B kanalında görünmez olur.
//
// Katı alternatif — "atanmamış ürün gizlidir" — bilinçli olarak UYGULANMADI.
// İleriye dönük bir karardır ve uygulandığı gün var olan her kataloğu tek
// seferde boşaltır; seçilecekse önce bir geçiş (tüm ürünleri varsayılan kanala
// atama) gerekir.
//
// Süzgeç [repository.Store] üzerinden VERİTABANINDA uygulanır; sayfalamayı
// neden Go tarafında yapamayacağımız için bkz. repository/saleschannel.go.
func (s *Service) ListStoreProducts(ctx context.Context, opts StoreListOptions) (ListResult[StoreProduct], error) {
	published := models.StatusPublished
	result, err := s.ListProducts(ctx, ListProductsOptions{
		Status:          &published,
		CollectionID:    opts.CollectionID,
		Search:          opts.Search,
		SalesChannelIDs: opts.SalesChannelIDs,
		Limit:           opts.Limit,
		Offset:          opts.Offset,
		WithRelations:   true,
	})
	if err != nil {
		return ListResult[StoreProduct]{}, err
	}

	items, err := s.toStoreProducts(ctx, result.Items)
	if err != nil {
		return ListResult[StoreProduct]{}, err
	}
	return ListResult[StoreProduct]{
		Items:  items,
		Count:  result.Count,
		Offset: result.Offset,
		Limit:  result.Limit,
	}, nil
}

// GetStoreProduct vitrin için tek bir ürünü fiyat ve stok bilgisiyle döner.
//
// Kimlik ya da handle kabul edilir: vitrin adresleri handle taşır, iç çağrılar
// kimlik. Yayında olmayan ürün BULUNAMADI döner — taslak bir ürünün varlığını
// "yetkisiz" gibi bir hatayla ele vermek de sızıntıdır.
//
// salesChannelIDs, listelemedekiyle AYNI anlamı taşır
// (bkz. [StoreListOptions.SalesChannelIDs]) ve tekil uç da AYNI süzgece
// tabidir: listede gizlenen bir ürünü tekil uçtan göstermek, gizlemeyi tümüyle
// anlamsız kılardı — vitrin adresleri handle taşıdığı için tahmin edilebilir
// olan tam da bu uçtur.
//
// Görünmeyen ürün, yayında olmayan ürünle AYNI hatayı (NotFound) döner: başka
// bir kanalda satılan bir ürünün varlığını farklı bir hata sınıfıyla ele
// vermek, gizlemenin kendisini delerdi.
func (s *Service) GetStoreProduct(
	ctx context.Context,
	idOrHandle string,
	salesChannelIDs []string,
) (StoreProduct, error) {
	if _, err := requireID("id", idOrHandle); err != nil {
		return StoreProduct{}, err
	}

	var (
		product models.Product
		err     error
	)
	if strings.HasPrefix(idOrHandle, prefixProduct) {
		product, err = s.GetProduct(ctx, idOrHandle)
	} else {
		product, err = s.GetProductByHandle(ctx, idOrHandle)
	}
	if err != nil {
		return StoreProduct{}, err
	}
	if product.Status != models.StatusPublished {
		return StoreProduct{}, errors.NotFound(codeNotFound, "ürün bulunamadı: %s", idOrHandle)
	}

	// nil, "istek kanal kimliği taşımıyor" demektir; sorgu o durumda zaten
	// true dönerdi, tur boşuna atılmaz.
	if salesChannelIDs != nil {
		visible, err := s.repo.ProductVisibleInSalesChannels(ctx, product.ID, salesChannelIDs)
		if err != nil {
			return StoreProduct{}, err
		}
		if !visible {
			return StoreProduct{}, errors.NotFound(codeNotFound, "ürün bulunamadı: %s", idOrHandle)
		}
	}

	items, err := s.toStoreProducts(ctx, []models.Product{product})
	if err != nil {
		return StoreProduct{}, err
	}
	return items[0], nil
}

// StoreProductsByIDs vitrin ürünlerini KİMLİĞE göre, İSTENEN SIRAYLA döner.
//
// Arama gibi dış tüketiciler içindir: alaka sırasını dışarıdan onlar verir
// ("product.interop" yüzeyi bu metoda bakar, bkz. interop.go).
//
// # Görünürlük kuralı listeyle AYNIDIR
//
// Kural burada YENİDEN YAZILMAZ; iki yerde ifade edilen bir görünürlük kuralı,
// biri değiştiğinde vitrin ile aramanın ayrışması ve aramanın kanal süzmesinin
// BYPASS'ı hâline gelmesi demektir. Bu yüzden:
//
//   - Yayın durumu tekil vitrin ucuyla aynı şekilde süzülür (yalnızca
//     "published"; bkz. [Service.GetStoreProduct]).
//   - Kanal görünürlüğü, tekil ucun kullandığı depo çağrısıyla
//     (ProductVisibleInSalesChannels) — yani listelemenin SQL'iyle AYNI
//     şablonla — sorulur (bkz. repository/saleschannel.go).
//
// salesChannelIDs'in nil ile boş dilim arasındaki farkı listelemedekiyle aynı
// anlamı taşır (bkz. [StoreListOptions.SalesChannelIDs]).
//
// Görünürlük kimlik başına bir sorgudur; kimlik sayısı [MaxLimit] ile
// sınırlıdır ve her sorgu link tablosunun birincil anahtar önekini kullanır.
// Bedel bilinçlidir: kuralın TEK tanımı kalsın diye ödenir. Bu yol bir gün
// sıcaklaşırsa doğru adım kuralı Go'ya taşımak değil, saleschannel.go'daki
// salesChannelVisible şablonundan TOPLU bir sorgu üretmektir.
//
// # Sıra ve bulunamayan kimlikler
//
// Yanıt, isteğin kimlik sırasını korur. Bilinmeyen, silinmiş, yayında olmayan
// ya da isteğin kanallarında görünmeyen kimlik SESSİZCE atlanır — hepsi
// çağıranın "bu kimlik sende var mı" sorusunun geçerli yanıtlarıdır ve hata
// dönmek, aramanın bir ürün silindiği için tümüyle düşmesi demek olurdu. Sızma
// yönünde bir bilgi de vermez: başka kanaldaki ürün ile hiç var olmayan ürün
// çağıran için AYIRT EDİLEMEZ (tekil vitrin ucunun ikisine de NotFound
// dönmesiyle aynı gerekçe).
//
// Tekrarlanan kimlik yanıtta BİR KEZ görünür; ilk geçtiği sırayı korur.
func (s *Service) StoreProductsByIDs(ctx context.Context, ids, salesChannelIDs []string) ([]StoreProduct, error) {
	wanted, err := uniqueIDs("ids", ids)
	if err != nil {
		return nil, err
	}
	if len(wanted) == 0 {
		return []StoreProduct{}, nil
	}
	// Sınırı aşan istek KIRPILMAZ, reddedilir: sessiz kırpma arama sonucunu
	// sessizce eksiltir ve çağıran bunu asla göremez. Açık hata onu sayfalamaya
	// zorlar.
	if len(wanted) > MaxLimit {
		return nil, invalid("ids en fazla %d kimlik taşıyabilir (verilen: %d)", MaxLimit, len(wanted))
	}

	found, err := s.repo.ListProductsByIDs(ctx, wanted)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.Product, len(found))
	for i := range found {
		byID[found[i].ID] = found[i]
	}

	// Görünürlük TEK sorguda sorulur. Kimlik başına sormak, arama sonucu
	// sayısı kadar gidiş-dönüş demektir ve N+1'i yapısal olarak dışarıda tutan
	// bir mimaride (bkz. core/query) onu en sıcak uçta geri getirirdi.
	//
	// nil, "istek kanal kimliği taşımıyor" demektir; süzgeç uygulanmaz ve
	// sorgu hiç atılmaz.
	var gorunur map[string]struct{}
	if salesChannelIDs != nil {
		gorunur, err = s.repo.VisibleProductIDs(ctx, wanted, salesChannelIDs)
		if err != nil {
			return nil, err
		}
	}

	visible := make([]models.Product, 0, len(wanted))
	for _, id := range wanted {
		product, ok := byID[id]
		if !ok || product.Status != models.StatusPublished {
			continue
		}
		if gorunur != nil {
			if _, ok := gorunur[id]; !ok {
				continue
			}
		}
		visible = append(visible, product)
	}
	if len(visible) == 0 {
		return []StoreProduct{}, nil
	}

	if err := s.attachRelations(ctx, visible); err != nil {
		return nil, err
	}
	return s.toStoreProducts(ctx, visible)
}

// toStoreProducts ürünleri vitrin biçimine çevirir ve varyantları
// zenginleştirir.
func (s *Service) toStoreProducts(ctx context.Context, products []models.Product) ([]StoreProduct, error) {
	variantIDs := make([]string, 0, len(products))
	for i := range products {
		variants := products[i].Variants
		for j := range variants {
			variantIDs = append(variantIDs, variants[j].ID)
		}
	}

	extras, err := s.enrichVariants(ctx, variantIDs)
	if err != nil {
		return nil, err
	}

	out := make([]StoreProduct, 0, len(products))
	for i := range products {
		p := products[i]
		variants := make([]StoreVariant, 0, len(p.Variants))
		for j := range p.Variants {
			variant := p.Variants[j]
			extra := extras[variant.ID]
			variants = append(variants, StoreVariant{
				Variant:       variant,
				PriceSet:      extra.priceSet,
				InventoryItem: extra.inventory,
			})
		}
		// Gömülü ürünün varyant dilimi boşaltılır: aynı veriyi iki yerde
		// taşımak, birinin güncellenip diğerinin unutulmasına açık kapı bırakır.
		p.Variants = nil
		out = append(out, StoreProduct{Product: p, Variants: variants})
	}
	return out, nil
}

// enrichVariants varyantların fiyat ve stok kayıtlarını TEK graph çağrısıyla
// toplar.
//
// Query katmanı bunu şöyle çözer: varyant sağlayıcısından kökler (tek sorgu),
// her genişletme için bir link çözümü ve hedef modülün sağlayıcısına tek bir
// FetchByIDs. Yani varyant sayısı ne olursa olsun tur sayısı sabittir.
//
// # Eksik modüle karşı davranış
//
// pricing ya da inventory bu kurulumda kayıtlı değilse Query "sağlayıcı
// bulunamadı" (codeProviderNotFound) döner. YALNIZCA bu durumda listeleme HATA
// VERMEZ: katalog fiyatsız/stoksuz döner ve durum uyarı olarak loglanır.
// Gerekçe, modülerliğin kendisidir — product modülü tek başına da
// dağıtılabilmelidir; ayrıca fiyatın eksik olması yanlış fiyat göstermekten
// iyidir (alan hiç yazılmaz).
//
// Düşüş HATA SINIFINA göre değil KODA göre daraltılmıştır. Sınıfa bakmak
// (KindNotFound) fazla genişti: kayıtlı bir sağlayıcının kendi içinde ürettiği
// NotFound da (query_provider_failed) o kapıdan geçer ve gerçek bir arıza,
// fiyatsız ama 200 dönen bir vitrin sayfasına dönüşürdü — Faz 4'ün DoD'si
// tek bir log satırı dışında hiçbir iz bırakmadan ihlal edilirdi.
func (s *Service) enrichVariants(ctx context.Context, variantIDs []string) (map[string]enrichment, error) {
	out := make(map[string]enrichment, len(variantIDs))
	if len(variantIDs) == 0 {
		return out, nil
	}
	if s.graph == nil {
		s.log.DebugContext(ctx, "query katmanı kayıtlı değil; vitrin fiyat/stok olmadan dönüyor")
		return out, nil
	}

	records, err := s.graph.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{filterID},
		Filters: map[string]any{filterIDs: variantIDs},
		Limit:   len(variantIDs),
		Expand: []query.Expansion{
			{Link: LinkVariantPriceSet, As: keyPriceSet},
			{Link: LinkVariantInventory, As: keyInventory},
		},
	})
	if err != nil {
		if errors.CodeOf(err) == codeProviderNotFound {
			s.log.WarnContext(ctx, "fiyat/stok sağlayıcısı kayıtlı değil; vitrin bu bilgi olmadan dönüyor",
				"error", err)
			return out, nil
		}
		return nil, errors.Wrap(err, errors.KindOf(err), codeQueryFailed,
			"varyantların fiyat/stok bilgisi okunamadı (%d varyant)", len(variantIDs))
	}

	for _, rec := range records {
		id, ok := rec[filterID].(string)
		if !ok || id == "" {
			continue
		}
		out[id] = enrichment{
			priceSet:  asRecord(rec[keyPriceSet]),
			inventory: asRecord(rec[keyInventory]),
		}
	}
	return out, nil
}

// asRecord genişletme sonucunu kayda çevirir; eşleşme yoksa nil döner.
//
// İki tip de kabul edilir: çekirdek query.Record yazar, ama sağlayıcı ya da
// bir sahte uygulama düz map[string]any döndürebilir ve tip iddiası o durumda
// sessizce başarısız olup fiyatı yutardı.
func asRecord(v any) query.Record {
	switch t := v.(type) {
	case query.Record:
		return t
	case map[string]any:
		return t
	default:
		return nil
	}
}
