package service

import (
	"context"
	"math"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// Bu dosya modülün Query katmanına açtığı OKUMA YÜZEYİDİR (ADR 0004).
//
// Sağlayıcılar container'a "product.query" ve "variant.query" adlarıyla
// kaydedilir. Query bunları isimle çözer; çekirdek bu modülü tanımaz, bu modül
// de çekirdeğe yalnızca imzayı karşılayarak görünür.
//
// İki ayrı entity sunulmasının sebebi kimliktir: fiyat ve stok bağları VARYANT
// kimliğiyle kurulur, ürün kimliğiyle değil. Tek bir "product" entity'si
// olsaydı link'ler ürün kayıtlarının "id" alanına düşer ve hiçbir şey
// eşleşmezdi.

// providerUnlimited Limit 0 (sınırsız) verildiğinde sorguya giden sınırdır.
//
// Gerçek anlamda sınırsız bir sorgu, tek bir istekle tüm katalogu belleğe
// çekebilirdi; bu sabit hem sınırsızı temsil eder hem de int32 sorgu
// parametresine güvenle sığar.
const providerUnlimited = math.MaxInt32

// productProvider ürün kayıtlarını Query katmanına sunar.
type productProvider struct {
	repo repository.Store
}

// variantProvider varyant kayıtlarını Query katmanına sunar.
type variantProvider struct {
	repo repository.Store
}

// Sağlayıcıların çekirdek sözleşmeyi karşıladığı derleme zamanında sabitlenir.
var (
	_ query.Provider = (*productProvider)(nil)
	_ query.Provider = (*variantProvider)(nil)
)

// NewProductProvider "product" entity'sinin Query sağlayıcısını üretir.
func NewProductProvider(repo repository.Store) query.Provider {
	return &productProvider{repo: repo}
}

// NewVariantProvider "variant" entity'sinin Query sağlayıcısını üretir.
func NewVariantProvider(repo repository.Store) query.Provider {
	return &variantProvider{repo: repo}
}

// Entity sağlayıcının sunduğu entity adını döner.
func (p *productProvider) Entity() string { return EntityProduct }

// List ürün kayıtlarını döner.
//
// Desteklenen filtreler: status, handle, collection_id, id/ids. Tanınmayan bir
// filtre errors.Invalid döner (ADR 0004): sessizce yok saymak, istemcinin
// filtrelediğini sandığı ama filtrelenmemiş bir listeyi doğru sanmasına yol
// açardı.
func (p *productProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	filter := repository.ProductFilter{Limit: providerLimit(opts.Limit), Offset: opts.Offset}
	var ids []string

	for key, raw := range opts.Filters {
		switch key {
		case filterStatus:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.Status = &value
		case filterHandle:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.Handle = &value
		case filterCollectionID:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.CollectionID = &value
		case filterID, filterIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, values...)
		default:
			return nil, unsupportedFilter(EntityProduct, key)
		}
	}

	products, err := p.fetch(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	return records(products, productRecord, opts.Fields, EntityProduct)
}

// fetch kimlik filtresi verilmişse kimliğe göre, verilmemişse ölçütlere göre okur.
func (p *productProvider) fetch(ctx context.Context, ids []string, filter repository.ProductFilter) ([]models.Product, error) {
	if len(ids) == 0 {
		return p.repo.ListProducts(ctx, filter)
	}

	products, err := p.repo.ListProductsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	// Kimlik listesi zaten dar bir kümedir; kalan ölçütler bellek içinde
	// uygulanır ki iki ayrı sorgu yolu tutarlı sonuç versin.
	out := make([]models.Product, 0, len(products))
	for i := range products {
		product := &products[i]
		if filter.Status != nil && product.Status.String() != *filter.Status {
			continue
		}
		if filter.Handle != nil && product.Handle != *filter.Handle {
			continue
		}
		if filter.CollectionID != nil && (product.CollectionID == nil || *product.CollectionID != *filter.CollectionID) {
			continue
		}
		out = append(out, *product)
	}
	return page(out, filter.Limit, filter.Offset), nil
}

// FetchByIDs verilen kimliklerin ürün kayıtlarını TEK sorguda döner.
func (p *productProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	products, err := p.repo.ListProductsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(products, productRecord, fields, EntityProduct)
}

// Entity sağlayıcının sunduğu entity adını döner.
func (v *variantProvider) Entity() string { return EntityVariant }

// List varyant kayıtlarını döner.
//
// Desteklenen filtreler: product_id, product_ids, id/ids.
func (v *variantProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	var (
		ids        []string
		productIDs []string
	)

	for key, raw := range opts.Filters {
		switch key {
		case filterProductID, filterProductIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			productIDs = append(productIDs, values...)
		case filterID, filterIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, values...)
		default:
			return nil, unsupportedFilter(EntityVariant, key)
		}
	}

	variants, err := v.fetch(ctx, ids, productIDs, opts)
	if err != nil {
		return nil, err
	}
	return records(variants, variantRecord, opts.Fields, EntityVariant)
}

// fetch varyantları en dar ölçüte göre okur.
func (v *variantProvider) fetch(
	ctx context.Context,
	ids, productIDs []string,
	opts query.ListOptions,
) ([]models.Variant, error) {
	limit := providerLimit(opts.Limit)

	switch {
	case len(ids) > 0:
		variants, err := v.repo.ListVariantsByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		if len(productIDs) > 0 {
			variants = slices.DeleteFunc(variants, func(variant models.Variant) bool {
				return !slices.Contains(productIDs, variant.ProductID)
			})
		}
		return page(variants, limit, opts.Offset), nil

	case len(productIDs) > 1:
		variants, err := v.repo.ListVariantsByProductIDs(ctx, productIDs)
		if err != nil {
			return nil, err
		}
		return page(variants, limit, opts.Offset), nil

	case len(productIDs) == 1:
		return v.repo.ListVariants(ctx, repository.VariantFilter{
			ProductID: &productIDs[0],
			Limit:     limit,
			Offset:    opts.Offset,
		})

	default:
		return v.repo.ListVariants(ctx, repository.VariantFilter{Limit: limit, Offset: opts.Offset})
	}
}

// FetchByIDs verilen kimliklerin varyant kayıtlarını TEK sorguda döner.
func (v *variantProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	variants, err := v.repo.ListVariantsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(variants, variantRecord, fields, EntityVariant)
}

// productRecord ürünü Query kaydına çevirir.
//
// Anahtarlar JSON alan adlarıyla aynıdır: aynı veri iki yüzeyde iki farklı
// adla görünseydi, sorguyu yazan ile yanıtı okuyan farklı sözlük kullanmak
// zorunda kalırdı.
func productRecord(p models.Product) query.Record {
	return query.Record{
		"id":             p.ID,
		"handle":         p.Handle,
		"title":          p.Title,
		"subtitle":       deref(p.Subtitle),
		"description":    deref(p.Description),
		"thumbnail":      deref(p.Thumbnail),
		"status":         p.Status.String(),
		"is_giftcard":    p.IsGiftcard,
		"discountable":   p.Discountable,
		"weight":         derefInt32(p.Weight),
		"collection_id":  deref(p.CollectionID),
		"material":       deref(p.Material),
		"origin_country": deref(p.OriginCountry),
		"metadata":       p.Metadata,
		"created_at":     p.CreatedAt,
		"updated_at":     p.UpdatedAt,
	}
}

// variantRecord varyantı Query kaydına çevirir.
func variantRecord(v models.Variant) query.Record {
	return query.Record{
		"id":               v.ID,
		"product_id":       v.ProductID,
		"title":            v.Title,
		"sku":              deref(v.SKU),
		"barcode":          deref(v.Barcode),
		"ean":              deref(v.EAN),
		"upc":              deref(v.UPC),
		"manage_inventory": v.ManageInventory,
		"allow_backorder":  v.AllowBackorder,
		"weight":           derefInt32(v.Weight),
		"rank":             v.Rank,
		"metadata":         v.Metadata,
		"created_at":       v.CreatedAt,
		"updated_at":       v.UpdatedAt,
	}
}

// records modelleri kayda çevirir ve istenen alanları seçer.
func records[T any](items []T, toRecord func(T) query.Record, fields []string, entity string) ([]query.Record, error) {
	out := make([]query.Record, 0, len(items))
	for i := range items {
		rec, err := project(toRecord(items[i]), fields, entity)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// project kayıttan istenen alanları seçer.
//
// Alan listesi boşsa kayıt olduğu gibi döner. Sağlayıcının tanımadığı bir alan
// errors.Invalid üretir (ADR 0004): eksik alanı sessizce atlamak, çağıranın
// beklediği veriyi taşımayan bir kaydı geçerli göstermek olurdu.
func project(rec query.Record, fields []string, entity string) (query.Record, error) {
	if len(fields) == 0 {
		return rec, nil
	}
	out := make(query.Record, len(fields))
	for _, field := range fields {
		value, ok := rec[field]
		if !ok {
			return nil, errors.Invalid(codeInvalidInput,
				"%q entity'sinde %q alanı yok", entity, field).
				WithDetails(map[string]any{"entity": entity, "alan": field})
		}
		out[field] = value
	}
	return out, nil
}

// page bellek içi bir dilime limit/offset uygular.
func page[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	items = items[offset:]
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}

// providerLimit Query'nin "0 = sınırsız" sözleşmesini sorgu sınırına çevirir.
func providerLimit(limit int) int {
	if limit <= 0 {
		return providerUnlimited
	}
	return limit
}

// stringFilter filtre değerini tek bir dizgeye çevirir.
func stringFilter(key string, raw any) (string, error) {
	value, ok := raw.(string)
	if !ok {
		return "", errors.Invalid(codeInvalidInput,
			"%q filtresi dizge bekliyor, %T geldi", key, raw)
	}
	return value, nil
}

// stringsFilter filtre değerini dizge dilimine çevirir.
//
// Tek dizge de kabul edilir: "id" ile "ids" aynı yolu kullanır ve çağıranın
// tek kimlik için dilim sarmalaması gerekmez. []any biçimi JSON'dan gelen
// filtreler içindir.
func stringsFilter(key string, raw any) ([]string, error) {
	switch v := raw.(type) {
	case string:
		return []string{v}, nil
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			value, ok := item.(string)
			if !ok {
				return nil, errors.Invalid(codeInvalidInput,
					"%q filtresindeki değerler dizge olmalı, %T geldi", key, item)
			}
			out = append(out, value)
		}
		return out, nil
	default:
		return nil, errors.Invalid(codeInvalidInput,
			"%q filtresi dizge ya da dizge dilimi bekliyor, %T geldi", key, raw)
	}
}

// unsupportedFilter tanınmayan filtre için tipli hata üretir.
func unsupportedFilter(entity, key string) error {
	return errors.Invalid(codeInvalidInput,
		"%q sağlayıcısı %q filtresini desteklemiyor", entity, key).
		WithDetails(map[string]any{"entity": entity, "filtre": key})
}

// deref işaretçiyi değere çevirir; nil ise boş dizge döner.
//
// Kayıtta nil yerine boş dizge durması bilinçlidir: JSON'a yazıldığında
// "subtitle": null ile "subtitle": "" arasındaki fark tüketiciyi ilgilendirmez,
// ama nil bir işaretçi tip iddialarında sürpriz üretir.
func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// derefInt32 işaretçiyi değere çevirir; nil ise sıfır döner.
func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}
