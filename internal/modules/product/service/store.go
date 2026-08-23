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
	Limit        int
	Offset       int
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
func (s *Service) ListStoreProducts(ctx context.Context, opts StoreListOptions) (ListResult[StoreProduct], error) {
	published := models.StatusPublished
	result, err := s.ListProducts(ctx, ListProductsOptions{
		Status:        &published,
		CollectionID:  opts.CollectionID,
		Search:        opts.Search,
		Limit:         opts.Limit,
		Offset:        opts.Offset,
		WithRelations: true,
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
func (s *Service) GetStoreProduct(ctx context.Context, idOrHandle string) (StoreProduct, error) {
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

	items, err := s.toStoreProducts(ctx, []models.Product{product})
	if err != nil {
		return StoreProduct{}, err
	}
	return items[0], nil
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
