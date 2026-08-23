package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// Query sağlayıcısının sunduğu alan adları.
const (
	// FieldID kaydın kimliğidir; Query birleştirmeyi bu alan üzerinden yapar.
	FieldID = query.IDField
	// FieldRegionID sepetin bölgesidir.
	FieldRegionID = "region_id"
	// FieldCustomerID sepetin müşterisidir; misafir sepetinde boştur.
	FieldCustomerID = "customer_id"
	// FieldEmail sepetin iletişim adresidir.
	FieldEmail = "email"
	// FieldCurrencyCode sepetin para birimidir.
	FieldCurrencyCode = "currency_code"
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
	// FieldTotalsStale toplamların sepetin güncel şekline ait OLMADIĞINI
	// bildirir. Alan türetilmiştir; sepetin bayat bir tutarı doğru sanılmasın
	// diye toplamlarla BİRLİKTE sunulur.
	FieldTotalsStale = "totals_stale"
	// FieldCompleted sepetin tamamlanmış olup olmadığını bildirir.
	FieldCompleted = "completed"
	// FieldCompletedAt sepetin tamamlandığı andır; tamamlanmamışsa nil.
	FieldCompletedAt = "completed_at"
	// FieldCreatedAt oluşturulma zamanıdır.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt son güncellenme zamanıdır.
	FieldUpdatedAt = "updated_at"
)

// cartFieldGetters sunulan alanların çıkarıcılarıdır.
//
// Alan kümesinin tek bir yerde tanımlı olması, doğrulama ile üretimin
// ayrışmasını imkânsız kılar: burada olmayan bir alan istenirse errors.Invalid
// döner (ADR 0004), burada olan her alan da üretilebilir.
var cartFieldGetters = map[string]func(cart models.Cart) any{
	FieldID:            func(c models.Cart) any { return c.ID },
	FieldRegionID:      func(c models.Cart) any { return c.RegionID },
	FieldCustomerID:    func(c models.Cart) any { return c.CustomerID },
	FieldEmail:         func(c models.Cart) any { return c.Email },
	FieldCurrencyCode:  func(c models.Cart) any { return c.CurrencyCode },
	FieldSubtotal:      func(c models.Cart) any { return c.Subtotal },
	FieldDiscountTotal: func(c models.Cart) any { return c.DiscountTotal },
	FieldTaxTotal:      func(c models.Cart) any { return c.TaxTotal },
	FieldShippingTotal: func(c models.Cart) any { return c.ShippingTotal },
	FieldTotal:         func(c models.Cart) any { return c.Total },
	FieldTotalsStale:   func(c models.Cart) any { return c.TotalsStale() },
	FieldCompleted:     func(c models.Cart) any { return c.Completed() },
	FieldCompletedAt: func(c models.Cart) any {
		if c.CompletedAt == nil {
			return nil
		}
		return *c.CompletedAt
	},
	FieldCreatedAt: func(c models.Cart) any { return c.CreatedAt },
	FieldUpdatedAt: func(c models.Cart) any { return c.UpdatedAt },
}

// QueryProvider cart modülünün Query katmanına açtığı okuma yüzeyidir.
//
// Container'a "cart.query" adıyla kaydedilir; Query onu ADLA çözer (ADR 0004).
// Sağlayıcı SATIRLARI SUNMAZ: bir sepetin satırları sayfalanmayan, sepet başına
// değişken uzunlukta bir kümedir ve Record içine gömülmeleri Query'nin
// birleştirme anahtarı sözleşmesine (tek "id" alanı) uymazdı. Satırlar
// [Service.GetCart] ile okunur.
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
// "completed" (bool). Başka bir filtre ya da tanınmayan bir alan
// errors.Invalid ile reddedilir (ADR 0004).
//
// Limit [MaxLimit]'e KIRPILIR; bkz. [providerLimit].
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListCartsInput{
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
		case FieldCompleted:
			flag, ok := value.(bool)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"%q filtresi mantıksal (bool) olmalı, %T verildi", name, value)
			}
			in.Completed = &flag
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"%q entity'si %q filtresini desteklemiyor", EntityName, name)
		}
	}

	carts, _, err := p.svc.ListCarts(ctx, in)
	if err != nil {
		return nil, err
	}
	return records(carts, opts.Fields), nil
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

	carts, err := p.svc.ListCartsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(carts, fields), nil
}

// records sepetleri istenen alanlarla kayda çevirir.
func records(carts []models.Cart, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(cartFieldGetters))
	}

	out := make([]query.Record, 0, len(carts))
	// Döngü indeksle gezilir: sepet yapısı büyüktür ve değerle kopyalamak her
	// tur birkaç yüz baytı boşuna taşır.
	for i := range carts {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = cartFieldGetters[name](carts[i])
		}
		out = append(out, record)
	}
	return out
}

// providerLimit çekirdeğin limit değerini sağlayıcının sayfa tavanına kırpar.
//
// Çekirdek sözleşmesinde ([query.ListOptions]) 0 "SINIRSIZ" demektir; bu
// sağlayıcı sınırsız listeleme sunmaz, çünkü sınırsız bir kök sorgu tüm sepet
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
		if _, ok := cartFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"%q entity'si %q alanını sunmuyor", EntityName, name)
		}
	}
	return nil
}
