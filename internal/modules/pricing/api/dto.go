package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.

// priceSetDTO bir price set'in yanıt gövdesidir.
type priceSetDTO struct {
	// ID kabın kimliğidir.
	ID string `json:"id"`
	// Prices kabın fiyatlarıdır; istenmediyse nil (JSON'da yok).
	Prices []priceDTO `json:"prices,omitempty"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// priceDTO bir fiyatın yanıt gövdesidir.
type priceDTO struct {
	// ID fiyatın kimliğidir.
	ID string `json:"id"`
	// PriceSetID fiyatın ait olduğu kaptır.
	PriceSetID string `json:"price_set_id"`
	// PriceListID fiyatın bağlı olduğu listedir; taban fiyatta null.
	PriceListID *string `json:"price_list_id"`
	// CurrencyCode ISO 4217 kodudur (BÜYÜK harf).
	CurrencyCode string `json:"currency_code"`
	// Amount minor unit cinsinden tutardır.
	Amount int64 `json:"amount"`
	// MinQuantity alt adet sınırıdır.
	MinQuantity int32 `json:"min_quantity"`
	// MaxQuantity üst adet sınırıdır; sınırsızsa null.
	MaxQuantity *int32 `json:"max_quantity"`
	// Rules fiyatın geçerlilik koşullarıdır.
	Rules []priceRuleDTO `json:"rules"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// priceRuleDTO bir fiyat kuralının yanıt gövdesidir.
type priceRuleDTO struct {
	// ID kuralın kimliğidir.
	ID string `json:"id"`
	// PriceID kuralın bağlı olduğu fiyattır.
	PriceID string `json:"price_id"`
	// Attribute hesaplama bağlamında bakılan alan adıdır.
	Attribute string `json:"attribute"`
	// Operator karşılaştırma işlecidir.
	Operator string `json:"operator"`
	// Values karşılaştırmanın sağ tarafıdır.
	Values []string `json:"values"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// priceListDTO bir fiyat listesinin yanıt gövdesidir.
type priceListDTO struct {
	// ID listenin kimliğidir.
	ID string `json:"id"`
	// Title listenin görünen adıdır.
	Title string `json:"title"`
	// Description açıklamadır.
	Description string `json:"description"`
	// Type listenin türüdür (sale | override).
	Type string `json:"type"`
	// Status listenin durumudur (draft | active | expired).
	Status string `json:"status"`
	// StartsAt geçerlilik penceresinin başıdır; yoksa null.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt geçerlilik penceresinin sonudur; yoksa null.
	EndsAt *time.Time `json:"ends_at"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// calculatedPriceDTO bir hesaplama sonucunun yanıt gövdesidir.
type calculatedPriceDTO struct {
	// PriceID seçilen fiyatın kimliğidir.
	PriceID string `json:"price_id"`
	// PriceSetID kabın kimliğidir.
	PriceSetID string `json:"price_set_id"`
	// CurrencyCode seçilen fiyatın para birimidir.
	CurrencyCode string `json:"currency_code"`
	// Amount birim başına minor unit tutardır.
	Amount int64 `json:"amount"`
	// Quantity hesaplamanın yapıldığı adettir.
	Quantity int32 `json:"quantity"`
	// Total tutar × adet çarpımıdır.
	Total int64 `json:"total"`
	// MinQuantity seçilen fiyatın alt adet sınırıdır.
	MinQuantity int32 `json:"min_quantity"`
	// MaxQuantity seçilen fiyatın üst adet sınırıdır; sınırsızsa null.
	MaxQuantity *int32 `json:"max_quantity"`
	// PriceListID fiyat bir listeden geliyorsa listenin kimliğidir.
	PriceListID *string `json:"price_list_id"`
	// PriceListType fiyat bir listeden geliyorsa listenin türüdür.
	PriceListType *string `json:"price_list_type"`
	// MatchedRules seçilen fiyatın eşleşen kural sayısıdır; seçimin NEDEN o
	// fiyata düştüğünü açıklar.
	MatchedRules int `json:"matched_rules"`
}

// priceRequest tek bir fiyatın istek gövdesidir.
type priceRequest struct {
	// CurrencyCode ISO 4217 kodudur; büyük/küçük harf serbesttir.
	CurrencyCode string `json:"currency_code"`
	// Amount minor unit cinsinden tutardır.
	Amount int64 `json:"amount"`
	// MinQuantity alt adet sınırıdır; 0 verilirse 1 kabul edilir.
	MinQuantity int32 `json:"min_quantity"`
	// MaxQuantity üst adet sınırıdır; null ise sınırsız.
	MaxQuantity *int32 `json:"max_quantity"`
	// PriceListID fiyatı bir listeye bağlar; null ise taban fiyat.
	PriceListID *string `json:"price_list_id"`
	// Rules fiyatın geçerlilik koşullarıdır.
	Rules []ruleRequest `json:"rules"`
}

// ruleRequest tek bir fiyat kuralının istek gövdesidir.
type ruleRequest struct {
	// Attribute hesaplama bağlamında bakılacak alan adıdır.
	Attribute string `json:"attribute"`
	// Operator karşılaştırma işlecidir (eq|ne|in|nin|gt|gte|lt|lte).
	Operator string `json:"operator"`
	// Values karşılaştırmanın sağ tarafıdır.
	Values []string `json:"values"`
}

// createPriceSetRequest price set oluşturma isteğidir.
type createPriceSetRequest struct {
	// Prices kapla birlikte yazılacak fiyatlardır; boş bırakılabilir.
	Prices []priceRequest `json:"prices"`
}

// setPricesRequest bir kabın fiyatlarını topluca yazma isteğidir.
type setPricesRequest struct {
	// Prices kabın YENİ fiyat kümesidir; verilmeyenler silinir.
	Prices []priceRequest `json:"prices"`
}

// priceListRequest fiyat listesi oluşturma/güncelleme isteğidir.
type priceListRequest struct {
	// Title listenin görünen adıdır; zorunludur.
	Title string `json:"title"`
	// Description açıklamadır.
	Description string `json:"description"`
	// Type listenin türüdür (sale | override); zorunludur.
	Type string `json:"type"`
	// Status listenin durumudur; boşsa draft.
	Status string `json:"status"`
	// StartsAt geçerlilik penceresinin başıdır.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt geçerlilik penceresinin sonudur.
	EndsAt *time.Time `json:"ends_at"`
}

// calculateRequest fiyat hesaplama isteğidir.
type calculateRequest struct {
	// CurrencyCode istenen para birimidir; zorunludur.
	CurrencyCode string `json:"currency_code"`
	// Quantity adettir; 0 verilirse 1 kabul edilir.
	Quantity int32 `json:"quantity"`
	// Attributes kural bağlamıdır.
	Attributes map[string]string `json:"attributes"`
	// At hesaplama anıdır; verilmezse "şimdi".
	At *time.Time `json:"at"`
}

// toPriceSetDTO price set modelini yanıt gövdesine çevirir.
// prices nil verilirse fiyat alanı yanıta yazılmaz.
func toPriceSetDTO(set models.PriceSet, prices []models.Price) priceSetDTO {
	dto := priceSetDTO{
		ID:        set.ID,
		CreatedAt: set.CreatedAt,
		UpdatedAt: set.UpdatedAt,
	}
	if prices != nil {
		dto.Prices = toPriceDTOs(prices)
	}
	return dto
}

// toPriceSetSummaryDTO fiyatsız bir price set gövdesi üretir; liste yanıtları
// için kullanılır.
func toPriceSetSummaryDTO(set models.PriceSet) priceSetDTO {
	return toPriceSetDTO(set, nil)
}

// toPriceDTOs fiyat dilimini yanıt gövdelerine çevirir.
func toPriceDTOs(prices []models.Price) []priceDTO {
	out := make([]priceDTO, 0, len(prices))
	for i := range prices {
		out = append(out, toPriceDTO(prices[i]))
	}
	return out
}

// toPriceDTO fiyat modelini yanıt gövdesine çevirir.
func toPriceDTO(price models.Price) priceDTO {
	return priceDTO{
		ID:           price.ID,
		PriceSetID:   price.PriceSetID,
		PriceListID:  price.PriceListID,
		CurrencyCode: price.CurrencyCode,
		Amount:       price.Amount,
		MinQuantity:  price.MinQuantity,
		MaxQuantity:  price.MaxQuantity,
		Rules:        toPriceRuleDTOs(price.Rules),
		CreatedAt:    price.CreatedAt,
		UpdatedAt:    price.UpdatedAt,
	}
}

// toPriceRuleDTOs kural dilimini yanıt gövdelerine çevirir.
func toPriceRuleDTOs(rules []models.PriceRule) []priceRuleDTO {
	out := make([]priceRuleDTO, 0, len(rules))
	for i := range rules {
		out = append(out, toPriceRuleDTO(rules[i]))
	}
	return out
}

// toPriceRuleDTO kural modelini yanıt gövdesine çevirir.
func toPriceRuleDTO(rule models.PriceRule) priceRuleDTO {
	values := rule.Values
	if values == nil {
		values = []string{}
	}
	return priceRuleDTO{
		ID:        rule.ID,
		PriceID:   rule.PriceID,
		Attribute: rule.Attribute,
		Operator:  string(rule.Operator),
		Values:    values,
		CreatedAt: rule.CreatedAt,
		UpdatedAt: rule.UpdatedAt,
	}
}

// toPriceListDTO fiyat listesi modelini yanıt gövdesine çevirir.
func toPriceListDTO(list models.PriceList) priceListDTO {
	return priceListDTO{
		ID:          list.ID,
		Title:       list.Title,
		Description: list.Description,
		Type:        string(list.Type),
		Status:      string(list.Status),
		StartsAt:    list.StartsAt,
		EndsAt:      list.EndsAt,
		CreatedAt:   list.CreatedAt,
		UpdatedAt:   list.UpdatedAt,
	}
}

// toCalculatedPriceDTO hesaplama sonucunu yanıt gövdesine çevirir.
func toCalculatedPriceDTO(calculated models.CalculatedPrice) calculatedPriceDTO {
	return calculatedPriceDTO{
		PriceID:       calculated.PriceID,
		PriceSetID:    calculated.PriceSetID,
		CurrencyCode:  calculated.CurrencyCode,
		Amount:        calculated.Amount,
		Quantity:      calculated.Quantity,
		Total:         calculated.Total,
		MinQuantity:   calculated.MinQuantity,
		MaxQuantity:   calculated.MaxQuantity,
		PriceListID:   calculated.PriceListID,
		PriceListType: listTypeOrNil(calculated.PriceListType),
		MatchedRules:  calculated.MatchedRules,
	}
}

// toPriceInputs istek gövdelerini servis girdilerine çevirir.
//
// Doğrulama YAPILMAZ: geçerliliğe servis karar verir ve tek bir doğrulama
// yerinin olması, HTTP ile modüller arası çağrının aynı kuralları görmesini
// sağlar.
func toPriceInputs(requests []priceRequest) []service.PriceInput {
	out := make([]service.PriceInput, 0, len(requests))
	for _, req := range requests {
		out = append(out, service.PriceInput{
			CurrencyCode: req.CurrencyCode,
			Amount:       req.Amount,
			MinQuantity:  req.MinQuantity,
			MaxQuantity:  req.MaxQuantity,
			PriceListID:  req.PriceListID,
			Rules:        toRuleInputs(req.Rules),
		})
	}
	return out
}

// toRuleInputs kural isteklerini servis girdilerine çevirir.
func toRuleInputs(requests []ruleRequest) []service.RuleInput {
	out := make([]service.RuleInput, 0, len(requests))
	for _, req := range requests {
		out = append(out, service.RuleInput{
			Attribute: req.Attribute,
			Operator:  models.RuleOperator(req.Operator),
			Values:    req.Values,
		})
	}
	return out
}

// toPriceListInput istek gövdesini servis girdisine çevirir.
func toPriceListInput(req priceListRequest) service.PriceListInput {
	return service.PriceListInput{
		Title:       req.Title,
		Description: req.Description,
		Type:        models.PriceListType(req.Type),
		Status:      models.PriceListStatus(req.Status),
		StartsAt:    req.StartsAt,
		EndsAt:      req.EndsAt,
	}
}
