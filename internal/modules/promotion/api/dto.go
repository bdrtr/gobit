package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.
//
// Ayrım burada ikinci bir işe daha yarar: MÜŞTERİ yüzeyinin gövdesi
// ([storeCouponDTO]) yönetim yüzeyininkinden (bkz. [promotionDTO]) ayrı bir
// tiptir ve bu ayrım, bir alan eklendiğinde onun kazara müşteriye sızmasını
// engeller.

// campaignDTO bir kampanyanın yanıt gövdesidir.
type campaignDTO struct {
	// ID kampanyanın kimliğidir.
	ID string `json:"id"`
	// Name kampanyanın görünen adıdır.
	Name string `json:"name"`
	// CampaignIdentifier operatörün verdiği benzersiz iş kimliğidir.
	CampaignIdentifier string `json:"campaign_identifier"`
	// Description açıklamadır.
	Description string `json:"description"`
	// StartsAt geçerlilik penceresinin başıdır; yoksa null.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt geçerlilik penceresinin sonudur; yoksa null.
	EndsAt *time.Time `json:"ends_at"`
	// BudgetType bütçenin ölçü birimidir (none | spend | usage).
	BudgetType string `json:"budget_type"`
	// BudgetLimit bütçenin üst sınırıdır; sınırsızsa null.
	BudgetLimit *int64 `json:"budget_limit"`
	// BudgetUsed bütçenin tüketilen kısmıdır.
	BudgetUsed int64 `json:"budget_used"`
	// BudgetCurrencyCode "spend" bütçesinin para birimidir; yoksa null.
	BudgetCurrencyCode *string `json:"budget_currency_code"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// promotionDTO bir promosyonun YÖNETİM yanıt gövdesidir.
type promotionDTO struct {
	// ID promosyonun kimliğidir.
	ID string `json:"id"`
	// Code kupon kodudur (BÜYÜK harf).
	Code string `json:"code"`
	// IsAutomatic promosyonun kodsuz uygulanıp uygulanmadığıdır.
	IsAutomatic bool `json:"is_automatic"`
	// Type promosyonun mekaniğidir (standard | buyget).
	Type string `json:"type"`
	// CampaignID promosyonun kampanyasıdır; kampanyasızsa null.
	CampaignID *string `json:"campaign_id"`
	// Status yayın durumudur (draft | active | inactive).
	Status string `json:"status"`
	// UsageLimit kullanım sınırıdır; sınırsızsa null.
	UsageLimit *int64 `json:"usage_limit"`
	// UsageCount kullanılmış sayıdır.
	UsageCount int64 `json:"usage_count"`
	// Metadata operatörün serbest notudur.
	Metadata map[string]string `json:"metadata"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// applicationMethodDTO bir uygulama yönteminin yanıt gövdesidir.
type applicationMethodDTO struct {
	// ID yöntemin kimliğidir.
	ID string `json:"id"`
	// PromotionID yöntemin bağlı olduğu promosyondur.
	PromotionID string `json:"promotion_id"`
	// Type indirimin ölçüsüdür (fixed | percentage).
	Type string `json:"type"`
	// TargetType indirimin hedefidir (items | shipping_methods | order).
	TargetType string `json:"target_type"`
	// Allocation dağıtım biçimidir (each | across).
	Allocation string `json:"allocation"`
	// Value sabit tutar (minor unit) ya da baz puandır.
	Value int64 `json:"value"`
	// MaxQuantity sabit tutarın uygulanacağı azami adettir; sınırsızsa null.
	MaxQuantity *int64 `json:"max_quantity"`
	// CurrencyCode sabit tutarlı indirimin para birimidir; yüzdede null.
	CurrencyCode *string `json:"currency_code"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// promotionRuleDTO bir promosyon kuralının YÖNETİM yanıt gövdesidir.
//
// Bu tipin store yüzeyinde bir karşılığı YOKTUR: bir kuralın sağ tarafı iş
// bilgisidir ve müşteriye hiçbir uç noktadan gitmez.
type promotionRuleDTO struct {
	// ID kuralın kimliğidir.
	ID string `json:"id"`
	// PromotionID kuralın bağlı olduğu promosyondur.
	PromotionID string `json:"promotion_id"`
	// RuleType kuralın neye baktığıdır (context | target).
	RuleType string `json:"rule_type"`
	// Attribute bakılan alan adıdır.
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

// redemptionDTO bir kullanım kaydının yanıt gövdesidir.
type redemptionDTO struct {
	// ID kullanımın kimliğidir.
	ID string `json:"id"`
	// PromotionID kullanılan promosyondur.
	PromotionID string `json:"promotion_id"`
	// CampaignID kullanım anındaki kampanyadır; yoksa null.
	CampaignID *string `json:"campaign_id"`
	// Reference kullanımın iş kaydı referansıdır.
	Reference string `json:"reference"`
	// Amount uygulanan indirim tutarıdır (minor unit).
	Amount int64 `json:"amount"`
	// CurrencyCode indirimin para birimidir.
	CurrencyCode string `json:"currency_code"`
	// BudgetDelta kampanya bütçesine eklenen değerdir.
	BudgetDelta int64 `json:"budget_delta"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// ReleasedAt serbest bırakılma anıdır; hâlâ geçerliyse null.
	ReleasedAt *time.Time `json:"released_at"`
}

// computeResultDTO bir indirim hesabının yanıt gövdesidir.
//
// Alan adları interop şemasıyla (service.Interop) BİREBİR aynıdır: iki yüzeyin
// aynı hesabı farklı adlarla anlatması, istemcinin hangisine bakacağını
// bilememesi demek olurdu.
type computeResultDTO struct {
	// CurrencyCode hesabın para birimidir.
	CurrencyCode string `json:"currency_code"`
	// Items kalem başına indirimlerdir.
	Items []lineDiscountDTO `json:"items"`
	// ShippingMethods kargo yöntemi başına indirimlerdir.
	ShippingMethods []lineDiscountDTO `json:"shipping_methods"`
	// ItemsDiscountTotal kalemlere düşen toplam indirimdir.
	ItemsDiscountTotal int64 `json:"items_discount_total"`
	// ShippingDiscountTotal kargoya düşen toplam indirimdir.
	ShippingDiscountTotal int64 `json:"shipping_discount_total"`
	// DiscountTotal toplam indirimdir.
	DiscountTotal int64 `json:"discount_total"`
	// Applied fiilen indirim üreten promosyonlardır.
	Applied []appliedPromotionDTO `json:"applied"`
	// UnmatchedCodes bağlanamayan kupon kodlarıdır.
	UnmatchedCodes []string `json:"unmatched_codes"`
}

// lineDiscountDTO tek bir satır indiriminin yanıt gövdesidir.
type lineDiscountDTO struct {
	// ID satırın kimliğidir.
	ID string `json:"id"`
	// Amount satıra düşen indirimdir (minor unit).
	Amount int64 `json:"amount"`
}

// appliedPromotionDTO uygulanmış bir promosyonun yanıt gövdesidir.
type appliedPromotionDTO struct {
	// PromotionID promosyonun kimliğidir.
	PromotionID string `json:"promotion_id"`
	// Code promosyonun kupon kodudur.
	Code string `json:"code"`
	// IsAutomatic promosyonun kodsuz uygulanıp uygulanmadığıdır.
	IsAutomatic bool `json:"is_automatic"`
	// Amount promosyonun fiilen uyguladığı toplam indirimdir.
	Amount int64 `json:"amount"`
}

// storeCouponDTO MÜŞTERİYE giden kupon gövdesidir.
//
// Bilinçli olarak DARDIR ve yönetim gövdesinden AYRI bir tiptir: durum,
// kullanım sayacı, kampanya bütçesi, üstveri ve kural koşulları BURADA YOKTUR.
// Ayrı tip olması, yönetim gövdesine eklenen bir alanın buraya kazara
// sızmasını yapısal olarak engeller.
type storeCouponDTO struct {
	// Code kupon kodudur (BÜYÜK harf).
	Code string `json:"code"`
	// Type indirimin ölçüsüdür (fixed | percentage).
	Type string `json:"type"`
	// TargetType indirimin hedefidir (items | shipping_methods | order).
	TargetType string `json:"target_type"`
	// Value sabit tutar (minor unit) ya da baz puandır.
	Value int64 `json:"value"`
	// CurrencyCode sabit tutarlı indirimin para birimidir; yüzdede null.
	CurrencyCode *string `json:"currency_code"`
}

// toCampaignDTO kampanyayı yanıt gövdesine çevirir.
func toCampaignDTO(c models.Campaign) campaignDTO {
	return campaignDTO{
		ID:                 c.ID,
		Name:               c.Name,
		CampaignIdentifier: c.CampaignIdentifier,
		Description:        c.Description,
		StartsAt:           c.StartsAt,
		EndsAt:             c.EndsAt,
		BudgetType:         string(c.BudgetType),
		BudgetLimit:        c.BudgetLimit,
		BudgetUsed:         c.BudgetUsed,
		BudgetCurrencyCode: stringOrNil(c.BudgetCurrencyCode),
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
	}
}

// toPromotionDTO promosyonu yönetim yanıt gövdesine çevirir.
func toPromotionDTO(p models.Promotion) promotionDTO {
	metadata := p.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	return promotionDTO{
		ID:          p.ID,
		Code:        p.Code,
		IsAutomatic: p.IsAutomatic,
		Type:        string(p.Type),
		CampaignID:  p.CampaignID,
		Status:      string(p.Status),
		UsageLimit:  p.UsageLimit,
		UsageCount:  p.UsageCount,
		Metadata:    metadata,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// toApplicationMethodDTO uygulama yöntemini yanıt gövdesine çevirir.
func toApplicationMethodDTO(m models.ApplicationMethod) applicationMethodDTO {
	return applicationMethodDTO{
		ID:           m.ID,
		PromotionID:  m.PromotionID,
		Type:         string(m.Type),
		TargetType:   string(m.TargetType),
		Allocation:   string(m.Allocation),
		Value:        m.Value,
		MaxQuantity:  m.MaxQuantity,
		CurrencyCode: stringOrNil(m.CurrencyCode),
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

// toPromotionRuleDTO kuralı yanıt gövdesine çevirir.
func toPromotionRuleDTO(rule models.PromotionRule) promotionRuleDTO {
	values := rule.Values
	if values == nil {
		values = []string{}
	}
	return promotionRuleDTO{
		ID:          rule.ID,
		PromotionID: rule.PromotionID,
		RuleType:    string(rule.RuleType),
		Attribute:   rule.Attribute,
		Operator:    string(rule.Operator),
		Values:      values,
		CreatedAt:   rule.CreatedAt,
		UpdatedAt:   rule.UpdatedAt,
	}
}

// toPromotionRuleDTOs kural listesini yanıt gövdelerine çevirir.
func toPromotionRuleDTOs(rules []models.PromotionRule) []promotionRuleDTO {
	out := make([]promotionRuleDTO, 0, len(rules))
	for i := range rules {
		out = append(out, toPromotionRuleDTO(rules[i]))
	}
	return out
}

// toRedemptionDTO kullanım kaydını yanıt gövdesine çevirir.
func toRedemptionDTO(r models.Redemption) redemptionDTO {
	return redemptionDTO{
		ID:           r.ID,
		PromotionID:  r.PromotionID,
		CampaignID:   r.CampaignID,
		Reference:    r.Reference,
		Amount:       r.Amount,
		CurrencyCode: r.CurrencyCode,
		BudgetDelta:  r.BudgetDelta,
		CreatedAt:    r.CreatedAt,
		ReleasedAt:   r.ReleasedAt,
	}
}

// toComputeResultDTO hesap sonucunu yanıt gövdesine çevirir.
func toComputeResultDTO(result service.ComputeResult) computeResultDTO {
	out := computeResultDTO{
		CurrencyCode:          result.CurrencyCode,
		Items:                 make([]lineDiscountDTO, 0, len(result.Items)),
		ShippingMethods:       make([]lineDiscountDTO, 0, len(result.ShippingMethods)),
		ItemsDiscountTotal:    result.ItemsDiscountTotal,
		ShippingDiscountTotal: result.ShippingDiscountTotal,
		DiscountTotal:         result.DiscountTotal,
		Applied:               make([]appliedPromotionDTO, 0, len(result.Applied)),
		UnmatchedCodes:        result.UnmatchedCodes,
	}
	if out.UnmatchedCodes == nil {
		out.UnmatchedCodes = []string{}
	}
	for i := range result.Items {
		out.Items = append(out.Items, lineDiscountDTO{
			ID:     result.Items[i].ID,
			Amount: result.Items[i].Amount,
		})
	}
	for i := range result.ShippingMethods {
		out.ShippingMethods = append(out.ShippingMethods, lineDiscountDTO{
			ID:     result.ShippingMethods[i].ID,
			Amount: result.ShippingMethods[i].Amount,
		})
	}
	for i := range result.Applied {
		out.Applied = append(out.Applied, appliedPromotionDTO{
			PromotionID: result.Applied[i].PromotionID,
			Code:        result.Applied[i].Code,
			IsAutomatic: result.Applied[i].IsAutomatic,
			Amount:      result.Applied[i].Amount,
		})
	}
	return out
}

// toStoreCouponDTO kuponu MÜŞTERİ gövdesine çevirir.
func toStoreCouponDTO(c service.StoreCoupon) storeCouponDTO {
	return storeCouponDTO{
		Code:         c.Code,
		Type:         string(c.MethodType),
		TargetType:   string(c.TargetType),
		Value:        c.Value,
		CurrencyCode: stringOrNil(c.CurrencyCode),
	}
}

// stringOrNil boş dizeyi JSON null'a çevirir.
//
// Ayrım anlamlıdır: yüzde indirimde para birimi YOKTUR ve boş dize yerine null
// görünmesi, "para birimi yok" ile "para birimi boş" karışıklığını önler.
func stringOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
