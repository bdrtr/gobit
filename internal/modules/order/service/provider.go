package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// Query sağlayıcısının sunduğu alan adları.
const (
	// FieldID kaydın kimliğidir; Query birleştirmeyi bu alan üzerinden yapar.
	FieldID = query.IDField
	// FieldDisplayID siparişin insan okunur numarasıdır.
	FieldDisplayID = "display_id"
	// FieldStatus siparişin durumudur.
	FieldStatus = "status"
	// FieldRegionID siparişin bölgesidir.
	FieldRegionID = "region_id"
	// FieldCustomerID siparişin müşterisidir; misafir siparişinde boştur.
	FieldCustomerID = "customer_id"
	// FieldEmail siparişin iletişim adresidir.
	FieldEmail = "email"
	// FieldCurrencyCode siparişin para birimidir.
	FieldCurrencyCode = "currency_code"
	// FieldCartID siparişin doğduğu sepettir; boş olabilir.
	FieldCartID = "cart_id"
	// FieldSubtotal satır ara toplamlarının toplamıdır (minor unit).
	FieldSubtotal = "subtotal"
	// FieldDiscountTotal toplam indirimdir (minor unit).
	FieldDiscountTotal = "discount_total"
	// FieldTaxTotal toplam vergidir (minor unit).
	FieldTaxTotal = "tax_total"
	// FieldShippingTotal toplam kargo tutarıdır (minor unit).
	FieldShippingTotal = "shipping_total"
	// FieldTotal ödenecek tutardır (minor unit).
	FieldTotal = "total"
	// FieldPlacedAt siparişin verildiği andır.
	FieldPlacedAt = "placed_at"
	// FieldCompletedAt siparişin tamamlandığı andır; tamamlanmamışsa nil.
	FieldCompletedAt = "completed_at"
	// FieldCanceledAt siparişin iptal edildiği andır; iptal edilmemişse nil.
	FieldCanceledAt = "canceled_at"
	// FieldCreatedAt oluşturulma zamanıdır.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt son güncellenme zamanıdır.
	FieldUpdatedAt = "updated_at"
)

// orderFieldGetters sunulan alanların çıkarıcılarıdır.
//
// Alan kümesinin tek bir yerde tanımlı olması, doğrulama ile üretimin
// ayrışmasını imkânsız kılar: burada olmayan bir alan istenirse errors.Invalid
// döner (ADR 0004), burada olan her alan da üretilebilir.
var orderFieldGetters = map[string]func(order models.Order) any{
	FieldID:            func(o models.Order) any { return o.ID },
	FieldDisplayID:     func(o models.Order) any { return o.DisplayID },
	FieldStatus:        func(o models.Order) any { return o.Status.String() },
	FieldRegionID:      func(o models.Order) any { return o.RegionID },
	FieldCustomerID:    func(o models.Order) any { return o.CustomerID },
	FieldEmail:         func(o models.Order) any { return o.Email },
	FieldCurrencyCode:  func(o models.Order) any { return o.CurrencyCode },
	FieldCartID:        func(o models.Order) any { return o.CartID },
	FieldSubtotal:      func(o models.Order) any { return o.Subtotal },
	FieldDiscountTotal: func(o models.Order) any { return o.DiscountTotal },
	FieldTaxTotal:      func(o models.Order) any { return o.TaxTotal },
	FieldShippingTotal: func(o models.Order) any { return o.ShippingTotal },
	FieldTotal:         func(o models.Order) any { return o.Total },
	FieldPlacedAt:      func(o models.Order) any { return o.PlacedAt },
	FieldCompletedAt: func(o models.Order) any {
		if o.CompletedAt == nil {
			return nil
		}
		return *o.CompletedAt
	},
	FieldCanceledAt: func(o models.Order) any {
		if o.CanceledAt == nil {
			return nil
		}
		return *o.CanceledAt
	},
	FieldCreatedAt: func(o models.Order) any { return o.CreatedAt },
	FieldUpdatedAt: func(o models.Order) any { return o.UpdatedAt },
}

// QueryProvider order modülünün Query katmanına açtığı okuma yüzeyidir.
//
// Container'a "order.query" adıyla kaydedilir; Query onu ADLA çözer (ADR 0004).
// Sağlayıcı SATIRLARI SUNMAZ: bir siparişin satırları sayfalanmayan, sipariş
// başına değişken uzunlukta bir kümedir ve Record içine gömülmeleri Query'nin
// birleştirme anahtarı sözleşmesine (tek "id" alanı) uymazdı. Satırlar
// [Service.GetOrder] ile okunur.
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
// Desteklenen filtreler: "customer_id" (string), "region_id" (string) ve
// "status" (string). Başka bir filtre ya da tanınmayan bir alan errors.Invalid
// ile reddedilir (ADR 0004).
//
// Limit [MaxLimit]'e KIRPILIR; bkz. [providerLimit].
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListOrdersInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// Filtreler ADA GÖRE SIRALI gezilir: harita sırası rastgeledir ve birden
	// çok filtre birden geçersizse hangi hatanın döneceği rastgele olurdu.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		switch name {
		case FieldCustomerID:
			id, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"%q filtresi metin olmalı, %T verildi", name, value)
			}
			in.CustomerID = &id
		case FieldRegionID:
			id, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"%q filtresi metin olmalı, %T verildi", name, value)
			}
			in.RegionID = &id
		case FieldStatus:
			raw, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"%q filtresi metin olmalı, %T verildi", name, value)
			}
			status := models.OrderStatus(raw)
			in.Status = &status
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"%q entity'si %q filtresini desteklemiyor", EntityName, name)
		}
	}

	orders, _, err := p.svc.ListOrders(ctx, in)
	if err != nil {
		return nil, err
	}
	return records(orders, opts.Fields), nil
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

	orders, err := p.svc.ListOrdersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(orders, fields), nil
}

// records siparişleri istenen alanlarla kayda çevirir.
func records(orders []models.Order, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(orderFieldGetters))
	}

	out := make([]query.Record, 0, len(orders))
	// Döngü indeksle gezilir: sipariş yapısı büyüktür ve değerle kopyalamak her
	// tur birkaç yüz baytı boşuna taşır.
	for i := range orders {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = orderFieldGetters[name](orders[i])
		}
		out = append(out, record)
	}
	return out
}

// providerLimit çekirdeğin limit değerini sağlayıcının sayfa tavanına kırpar.
//
// Çekirdek sözleşmesinde ([query.ListOptions]) 0 "SINIRSIZ" demektir; bu
// sağlayıcı sınırsız listeleme sunmaz, çünkü sınırsız bir kök sorgu tüm sipariş
// tablosunu belleğe alırdı. Sınırsız istek bu yüzden [MaxLimit]'e çevrilir —
// [DefaultLimit]'e DEĞİL: çağıran açıkça "hepsini istiyorum" demiştir,
// alabileceğinin en fazlasını almalıdır. Anlamsız bir negatif değer de aynı
// kefeye konur: bu yolda limit bir istemci girdisi değil, başka bir modülün
// sorgu tanımından gelen bir sayıdır ve reddedilmesi tüm okumayı düşürürdü.
func providerLimit(limit int) int64 {
	if limit <= 0 || int64(limit) > MaxLimit {
		return MaxLimit
	}
	return int64(limit)
}

// validateFields istenen alanların hepsinin sunulduğunu doğrular.
func validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := orderFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"%q entity'si %q alanını sunmuyor", EntityName, name)
		}
	}
	return nil
}
