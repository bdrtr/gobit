package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// Query sağlayıcısının sunduğu alan adları.
const (
	// FieldID kaydın kimliğidir; Query birleştirmeyi bu alan üzerinden yapar.
	FieldID = query.IDField
	// FieldReference koleksiyonun bağlı olduğu sepet/sipariş kimliğidir.
	FieldReference = "reference"
	// FieldAmount toplanması gereken toplam tutardır (minor unit).
	FieldAmount = "amount"
	// FieldCurrencyCode ISO 4217 para birimi kodudur.
	FieldCurrencyCode = "currency_code"
	// FieldStatus koleksiyonun türetilmiş durumudur.
	FieldStatus = "status"
	// FieldAuthorizedAmount bloke edilmiş toplam tutardır.
	FieldAuthorizedAmount = "authorized_amount"
	// FieldCapturedAmount tahsil edilmiş toplam tutardır.
	FieldCapturedAmount = "captured_amount"
	// FieldRefundedAmount iade edilmiş toplam tutardır.
	FieldRefundedAmount = "refunded_amount"
	// FieldCreatedAt oluşturulma zamanıdır.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt son güncellenme zamanıdır.
	FieldUpdatedAt = "updated_at"
)

// collectionFieldGetters sunulan alanların çıkarıcılarıdır.
//
// Alan kümesinin tek bir yerde tanımlı olması, doğrulama ile üretimin
// ayrışmasını imkânsız kılar: burada olmayan bir alan istenirse errors.Invalid
// döner (ADR 0004), burada olan her alan da üretilebilir.
//
// Metadata BİLİNÇLİ OLARAK sunulmaz: çağıranın koyduğu serbest veridir ve
// modüller arası okuma yüzeyinde şeması olmayan bir alanı taşımak, Query'nin
// birleştirdiği kayıtları öngörülemez hâle getirirdi.
var collectionFieldGetters = map[string]func(col models.PaymentCollection) any{
	FieldID:               func(col models.PaymentCollection) any { return col.ID },
	FieldReference:        func(col models.PaymentCollection) any { return col.Reference },
	FieldAmount:           func(col models.PaymentCollection) any { return col.Amount },
	FieldCurrencyCode:     func(col models.PaymentCollection) any { return col.CurrencyCode },
	FieldStatus:           func(col models.PaymentCollection) any { return col.Status.String() },
	FieldAuthorizedAmount: func(col models.PaymentCollection) any { return col.AuthorizedAmount },
	FieldCapturedAmount:   func(col models.PaymentCollection) any { return col.CapturedAmount },
	FieldRefundedAmount:   func(col models.PaymentCollection) any { return col.RefundedAmount },
	FieldCreatedAt:        func(col models.PaymentCollection) any { return col.CreatedAt },
	FieldUpdatedAt:        func(col models.PaymentCollection) any { return col.UpdatedAt },
}

// QueryProvider payment modülünün Query katmanına açtığı okuma yüzeyidir.
//
// Container'a "payment_collection.query" adıyla kaydedilir; Query onu ADLA
// çözer (ADR 0004). Sipariş listelemesi, siparişin ödeme durumunu bu sağlayıcı
// üzerinden ve "order_payment" link'iyle görür.
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
// Desteklenen süzgeçler: "reference" ve "status" (ikisi de metin). Başka bir
// süzgeç ya da tanınmayan bir alan errors.Invalid ile reddedilir (ADR 0004).
//
// Limit [MaxLimit]'e KIRPILIR; bkz. [providerLimit]. Kırpma sessizdir ve hata
// dönmez, ama sonuç sayfa boyutunun aşılamayacağı anlamına gelir: çağıran tüm
// kayıtları aldığını varsaymamalı, [MaxLimit] kadar kayıt dönen bir yanıtı
// "devamı olabilir" diye okumalıdır.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListCollectionsInput{
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
		case FieldReference:
			in.Reference = &text
		case FieldStatus:
			in.Status = &text
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"%q entity'si %q süzgecini desteklemiyor", EntityName, name)
		}
	}

	collections, _, err := p.svc.ListPaymentCollections(ctx, in)
	if err != nil {
		return nil, err
	}
	return records(collections, opts.Fields), nil
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

	collections, err := p.svc.ListPaymentCollectionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(collections, fields), nil
}

// records koleksiyonları istenen alanlarla kayda çevirir.
// fields boşsa sunulan TÜM alanlar döner.
func records(collections []models.PaymentCollection, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(collectionFieldGetters))
	}

	out := make([]query.Record, 0, len(collections))
	// Dilim İNDEKSLE gezilir: değerle gezmek her yinelemede koleksiyon
	// yapısının tamamını kopyalardı ve kayıt sayısı arttıkça bedeli büyürdü.
	for i := range collections {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = collectionFieldGetters[name](collections[i])
		}
		out = append(out, record)
	}
	return out
}

// providerLimit çekirdeğin limit değerini sağlayıcının sayfa tavanına kırpar.
//
// Çekirdek sözleşmesinde ([query.ListOptions]) 0 "SINIRSIZ" demektir; bu
// sağlayıcı sınırsız listeleme sunmaz, çünkü sınırsız bir kök sorgu tüm
// koleksiyon tablosunu belleğe alırdı. Sınırsız istek bu yüzden [MaxLimit]'e
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
		if _, ok := collectionFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"%q entity'si %q alanını sunmuyor", EntityName, name)
		}
	}
	return nil
}
