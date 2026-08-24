package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// Query sağlayıcısının sunduğu alan adları.
const (
	// FieldID kaydın kimliğidir; Query birleştirmeyi bu alan üzerinden yapar.
	FieldID = query.IDField
	// FieldName seçeneğin görünen adıdır.
	FieldName = "name"
	// FieldProviderID seçeneği yürütecek sağlayıcının kimliğidir.
	FieldProviderID = "provider_id"
	// FieldProfileID seçeneğin bağlı olduğu kargo profilidir.
	FieldProfileID = "shipping_profile_id"
	// FieldPriceType ücretin nereden geldiğini söyler ("flat"/"calculated").
	FieldPriceType = "price_type"
	// FieldAmount "flat" seçeneklerin sabit ücretidir (minor unit).
	FieldAmount = "amount"
	// FieldCurrencyCode ISO 4217 para birimi kodudur.
	FieldCurrencyCode = "currency_code"
	// FieldRegionID seçeneğin geçerli olduğu bölgedir; boş ise her bölge.
	FieldRegionID = "region_id"
	// FieldIsReturn seçeneğin iade gönderisi için olduğunu bildirir.
	FieldIsReturn = "is_return"
	// FieldAdminOnly seçeneğin mağaza yüzeyine çıkmadığını bildirir.
	FieldAdminOnly = "admin_only"
	// FieldCreatedAt oluşturulma zamanıdır.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt son güncellenme zamanıdır.
	FieldUpdatedAt = "updated_at"
)

// optionFieldGetters sunulan alanların çıkarıcılarıdır.
//
// Alan kümesinin tek bir yerde tanımlı olması, doğrulama ile üretimin
// ayrışmasını imkânsız kılar: burada olmayan bir alan istenirse errors.Invalid
// döner (ADR 0004), burada olan her alan da üretilebilir.
//
// Data ve Metadata BİLİNÇLİ OLARAK sunulmaz. Data sağlayıcının iç
// yapılandırmasıdır ve modüller arası okuma yüzeyinde hiç görünmemelidir;
// Metadata ise çağıranın koyduğu serbest veridir ve şeması olmayan bir alanı
// taşımak, Query'nin birleştirdiği kayıtları öngörülemez hâle getirirdi.
//
// [FieldAdminOnly] ise SUNULUR: bu yüzey modüller arası bir OKUMA yüzeyidir,
// mağaza yanıtı değildir. Tüketicinin bir seçeneğin vitrine çıkıp çıkmadığını
// bilmesi gerekir; mağaza yüzeyindeki gizleme api paketinde yapılır.
var optionFieldGetters = map[string]func(option models.ShippingOption) any{
	FieldID:           func(option models.ShippingOption) any { return option.ID },
	FieldName:         func(option models.ShippingOption) any { return option.Name },
	FieldProviderID:   func(option models.ShippingOption) any { return option.ProviderID },
	FieldProfileID:    func(option models.ShippingOption) any { return option.ShippingProfileID },
	FieldPriceType:    func(option models.ShippingOption) any { return option.PriceType.String() },
	FieldAmount:       func(option models.ShippingOption) any { return option.Amount },
	FieldCurrencyCode: func(option models.ShippingOption) any { return option.CurrencyCode },
	FieldRegionID:     func(option models.ShippingOption) any { return option.RegionID },
	FieldIsReturn:     func(option models.ShippingOption) any { return option.IsReturn },
	FieldAdminOnly:    func(option models.ShippingOption) any { return option.AdminOnly },
	FieldCreatedAt:    func(option models.ShippingOption) any { return option.CreatedAt },
	FieldUpdatedAt:    func(option models.ShippingOption) any { return option.UpdatedAt },
}

// QueryProvider fulfillment modülünün Query katmanına açtığı okuma yüzeyidir.
//
// Container'a "shipping_option.query" adıyla kaydedilir; Query onu ADLA çözer
// (ADR 0004). Sipariş listelemesi, siparişin hangi kargo seçeneğiyle
// gönderildiğini bu sağlayıcı ve bir link üzerinden görür.
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
func (p *QueryProvider) Entity() string { return EntityName }

// List kök kayıtları döner.
//
// Desteklenen süzgeçler: "region_id", "shipping_profile_id", "provider_id" ve
// "price_type" (hepsi metin). Başka bir süzgeç ya da tanınmayan bir alan
// errors.Invalid ile reddedilir (ADR 0004).
//
// Limit [MaxLimit]'e KIRPILIR; bkz. [providerLimit]. Kırpma sessizdir ve hata
// dönmez, ama sonuç sayfa boyutunun aşılamayacağı anlamına gelir: çağıran tüm
// kayıtları aldığını varsaymamalı, [MaxLimit] kadar kayıt dönen bir yanıtı
// "devamı olabilir" diye okumalıdır.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListOptionsAdminInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// Süzgeçler sıralı gezilir: harita üzerinde dönmek, birden çok süzgeç
	// birden geçersizken hangi hatanın döneceğini rastgele bırakırdı.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		text, ok := value.(string)
		if !ok {
			return nil, errors.Invalid(CodeInvalidInput,
				"%q süzgeci metin olmalı, %T verildi", name, value)
		}
		switch name {
		case FieldRegionID:
			in.RegionID = &text
		case FieldProfileID:
			in.ProfileID = &text
		case FieldProviderID:
			in.ProviderID = &text
		case FieldPriceType:
			in.PriceType = &text
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"%q entity'si %q süzgecini desteklemiyor", EntityName, name)
		}
	}

	options, _, err := p.svc.ListShippingOptions(ctx, in)
	if err != nil {
		return nil, err
	}
	return records(options, opts.Fields), nil
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

	options, err := p.svc.ListShippingOptionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(options, fields), nil
}

// records seçenekleri istenen alanlarla kayda çevirir.
// fields boşsa sunulan TÜM alanlar döner.
func records(options []models.ShippingOption, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(optionFieldGetters))
	}

	out := make([]query.Record, 0, len(options))
	// Dilim İNDEKSLE gezilir: değerle gezmek her yinelemede seçenek yapısının
	// tamamını kopyalardı ve kayıt sayısı arttıkça bedeli büyürdü.
	for i := range options {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = optionFieldGetters[name](options[i])
		}
		out = append(out, record)
	}
	return out
}

// providerLimit çekirdeğin limit değerini sağlayıcının sayfa tavanına kırpar.
//
// Çekirdek sözleşmesinde ([query.ListOptions]) 0 "SINIRSIZ" demektir; bu
// sağlayıcı sınırsız listeleme sunmaz, çünkü sınırsız bir kök sorgu tüm
// seçenek tablosunu belleğe alırdı. Sınırsız istek bu yüzden [MaxLimit]'e
// çevrilir — [DefaultLimit]'e DEĞİL: çağıran açıkça "hepsini istiyorum"
// demiştir, alabileceğinin en fazlasını almalıdır. Anlamsız bir negatif değer
// de aynı kefeye konur: bu yolda limit bir istemci girdisi değil, başka bir
// modülün sorgu tanımından gelen bir sayıdır ve reddedilmesi tüm okumayı
// düşürürdü.
func providerLimit(limit int) int64 {
	if limit <= 0 || int64(limit) > MaxLimit {
		return MaxLimit
	}
	return int64(limit)
}

// validateFields istenen alanların hepsinin sunulduğunu doğrular.
func validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := optionFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"%q entity'si %q alanını sunmuyor", EntityName, name)
		}
	}
	return nil
}
