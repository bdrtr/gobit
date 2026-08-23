package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// Query sağlayıcısının sunduğu alan adları.
const (
	// FieldID kaydın kimliğidir; Query birleştirmeyi bu alan üzerinden yapar.
	FieldID = query.IDField
	// FieldSKU stok takip kodudur.
	FieldSKU = "sku"
	// FieldTitle kalemin başlığıdır.
	FieldTitle = "title"
	// FieldDescription kalemin açıklamasıdır.
	FieldDescription = "description"
	// FieldRequiresShipping kalemin sevkiyat gerektirip gerektirmediğidir.
	FieldRequiresShipping = "requires_shipping"
	// FieldAvailableQuantity TÜM lokasyonlardaki satılabilir toplamdır.
	// product'ın mağaza listelemesi stoğu bu alandan okur.
	FieldAvailableQuantity = "available_quantity"
	// FieldCreatedAt oluşturulma zamanıdır.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt son güncellenme zamanıdır.
	FieldUpdatedAt = "updated_at"
)

// itemFieldGetters sunulan alanların çıkarıcılarıdır.
//
// Alan kümesinin tek bir yerde tanımlı olması, doğrulama ile üretimin
// ayrışmasını imkânsız kılar: burada olmayan bir alan istenirse errors.Invalid
// döner (ADR 0004), burada olan her alan da üretilebilir.
var itemFieldGetters = map[string]func(item models.InventoryItem, available int64) any{
	FieldID:                func(item models.InventoryItem, _ int64) any { return item.ID },
	FieldSKU:               func(item models.InventoryItem, _ int64) any { return item.SKU },
	FieldTitle:             func(item models.InventoryItem, _ int64) any { return item.Title },
	FieldDescription:       func(item models.InventoryItem, _ int64) any { return item.Description },
	FieldRequiresShipping:  func(item models.InventoryItem, _ int64) any { return item.RequiresShipping },
	FieldCreatedAt:         func(item models.InventoryItem, _ int64) any { return item.CreatedAt },
	FieldUpdatedAt:         func(item models.InventoryItem, _ int64) any { return item.UpdatedAt },
	FieldAvailableQuantity: func(_ models.InventoryItem, available int64) any { return available },
}

// QueryProvider inventory modülünün Query katmanına açtığı okuma yüzeyidir.
//
// Container'a "inventory_item.query" adıyla kaydedilir; Query onu ADLA çözer
// (ADR 0004). Kayıtlar "available_quantity" alanıyla birlikte döndüğü için
// product'ın mağaza listelemesi ürünü ve stoğunu TEK query çağrısında görür.
type QueryProvider struct {
	svc *Service
}

// QueryProvider'ın çekirdek sözleşmesini karşıladığı derleme zamanında
// doğrulanır; imza kayması çalışma zamanına kalmaz.
var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider verilen servis üzerinde çalışan bir sağlayıcı üretir.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity sağlayıcının sunduğu entity adını döner.
func (p *QueryProvider) Entity() string {
	return EntityName
}

// List kök kayıtları döner.
//
// Desteklenen filtreler: "sku" (string) ve "requires_shipping" (bool). Başka
// bir filtre ya da tanınmayan bir alan errors.Invalid ile reddedilir (ADR 0004).
//
// Limit 0 verilirse [DefaultLimit] uygulanır: bu sağlayıcı SINIRSIZ listeleme
// sunmaz, çünkü sınırsız bir kök sorgu tüm kalem tablosunu belleğe alırdı.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListInventoryItemsInput{
		Page: Page{Limit: int64(opts.Limit), Offset: int64(opts.Offset)},
	}
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		switch name {
		case FieldSKU:
			sku, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"%q filtresi metin olmalı, %T verildi", name, value)
			}
			in.SKU = &sku
		case FieldRequiresShipping:
			flag, ok := value.(bool)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"%q filtresi mantıksal (bool) olmalı, %T verildi", name, value)
			}
			in.RequiresShipping = &flag
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"%q entity'si %q filtresini desteklemiyor", EntityName, name)
		}
	}

	items, _, err := p.svc.ListInventoryItems(ctx, in)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, items, opts.Fields)
}

// FetchByIDs verilen kimliklerin kayıtlarını BATCH olarak döner.
// Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (p *QueryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	items, err := p.svc.ListInventoryItemsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, items, fields)
}

// records kalemleri istenen alanlarla kayda çevirir.
//
// Satılabilir adet YALNIZCA istendiğinde hesaplanır ve hesaplanırken TÜM
// kalemler için tek sorgu yapılır; kayıt başına sorgu (N+1) yoktur.
func (p *QueryProvider) records(ctx context.Context, items []models.InventoryItem, fields []string) ([]query.Record, error) {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(itemFieldGetters))
	}

	available := map[string]int64{}
	if slices.Contains(selected, FieldAvailableQuantity) && len(items) > 0 {
		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		var err error
		if available, err = p.svc.AvailableQuantities(ctx, ids); err != nil {
			return nil, err
		}
	}

	out := make([]query.Record, 0, len(items))
	for _, item := range items {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = itemFieldGetters[name](item, available[item.ID])
		}
		out = append(out, record)
	}
	return out, nil
}

// validateFields istenen alanların hepsinin sunulduğunu doğrular.
func validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := itemFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"%q entity'si %q alanını sunmuyor", EntityName, name)
		}
	}
	return nil
}
