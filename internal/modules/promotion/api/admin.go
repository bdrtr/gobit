package api

import (
	"net/http"
	"time"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// campaignRequest kampanya oluşturma/güncelleme gövdesidir.
type campaignRequest struct {
	// Name kampanyanın görünen adıdır.
	Name string `json:"name"`
	// CampaignIdentifier benzersiz iş kimliğidir.
	CampaignIdentifier string `json:"campaign_identifier"`
	// Description açıklamadır.
	Description string `json:"description"`
	// StartsAt geçerlilik penceresinin başıdır.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt geçerlilik penceresinin sonudur.
	EndsAt *time.Time `json:"ends_at"`
	// BudgetType bütçenin ölçü birimidir (none | spend | usage).
	BudgetType string `json:"budget_type"`
	// BudgetLimit bütçenin üst sınırıdır.
	BudgetLimit *int64 `json:"budget_limit"`
	// BudgetCurrencyCode "spend" bütçesinin para birimidir.
	BudgetCurrencyCode string `json:"budget_currency_code"`
}

// toCampaignInput gövdeyi servis girdisine çevirir.
func (r campaignRequest) toCampaignInput() service.CampaignInput {
	return service.CampaignInput{
		Name:               r.Name,
		CampaignIdentifier: r.CampaignIdentifier,
		Description:        r.Description,
		StartsAt:           r.StartsAt,
		EndsAt:             r.EndsAt,
		BudgetType:         models.CampaignBudgetType(r.BudgetType),
		BudgetLimit:        r.BudgetLimit,
		BudgetCurrencyCode: r.BudgetCurrencyCode,
	}
}

// createCampaign yeni bir kampanya oluşturur (POST /admin/v1/campaigns).
func (a *API) createCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req campaignRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	campaign, err := a.svc.CreateCampaign(ctx, req.toCampaignInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCampaignDTO(campaign))
}

// listCampaigns kampanyaları sayfalayarak listeler (GET /admin/v1/campaigns).
func (a *API) listCampaigns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListCampaigns(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCampaignDTO)
}

// getCampaign tek bir kampanyayı döner (GET /admin/v1/campaigns/{id}).
func (a *API) getCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	campaign, err := a.svc.GetCampaign(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCampaignDTO(campaign))
}

// updateCampaign kampanyanın tanımını yerine koyar
// (PUT /admin/v1/campaigns/{id}).
//
// Bütçe SAYACI bu yoldan değişmez; yalnızca kullanım akışı onu yazar.
func (a *API) updateCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req campaignRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	campaign, err := a.svc.UpdateCampaign(ctx, pathID(r, "id"), req.toCampaignInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCampaignDTO(campaign))
}

// deleteCampaign kampanyayı soft delete ile siler
// (DELETE /admin/v1/campaigns/{id}).
func (a *API) deleteCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteCampaign(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// promotionRequest promosyon oluşturma/güncelleme gövdesidir.
type promotionRequest struct {
	// Code kupon kodudur.
	Code string `json:"code"`
	// IsAutomatic promosyonun kodsuz uygulanıp uygulanmayacağıdır.
	IsAutomatic bool `json:"is_automatic"`
	// Type promosyonun mekaniğidir (standard | buyget).
	Type string `json:"type"`
	// CampaignID promosyonu bir kampanyaya bağlar.
	CampaignID *string `json:"campaign_id"`
	// Status yayın durumudur (draft | active | inactive).
	Status string `json:"status"`
	// UsageLimit kullanım sınırıdır.
	UsageLimit *int64 `json:"usage_limit"`
	// Metadata operatörün serbest notudur.
	Metadata map[string]string `json:"metadata"`
}

// toPromotionInput gövdeyi servis girdisine çevirir.
func (r promotionRequest) toPromotionInput() service.PromotionInput {
	return service.PromotionInput{
		Code:        r.Code,
		IsAutomatic: r.IsAutomatic,
		Type:        models.PromotionType(r.Type),
		CampaignID:  r.CampaignID,
		Status:      models.PromotionStatus(r.Status),
		UsageLimit:  r.UsageLimit,
		Metadata:    r.Metadata,
	}
}

// createPromotion yeni bir promosyon oluşturur (POST /admin/v1/promotions).
func (a *API) createPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req promotionRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	promo, err := a.svc.CreatePromotion(ctx, req.toPromotionInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPromotionDTO(promo))
}

// listPromotions promosyonları sayfalayarak listeler
// (GET /admin/v1/promotions).
//
// "status" ve "campaign_id" sorgu parametreleriyle süzülebilir.
func (a *API) listPromotions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListPromotionsInput{
		CampaignID: stringParam(r, "campaign_id"),
		Limit:      limit,
		Offset:     offset,
	}
	if raw := stringParam(r, "status"); raw != nil {
		status := models.PromotionStatus(*raw)
		in.Status = &status
	}

	page, err := a.svc.ListPromotions(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toPromotionDTO)
}

// getPromotion tek bir promosyonu döner (GET /admin/v1/promotions/{id}).
func (a *API) getPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	promo, err := a.svc.GetPromotion(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPromotionDTO(promo))
}

// updatePromotion promosyonun tanımını yerine koyar
// (PUT /admin/v1/promotions/{id}).
func (a *API) updatePromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req promotionRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	promo, err := a.svc.UpdatePromotion(ctx, pathID(r, "id"), req.toPromotionInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPromotionDTO(promo))
}

// deletePromotion promosyonu soft delete ile siler
// (DELETE /admin/v1/promotions/{id}).
func (a *API) deletePromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePromotion(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// applicationMethodRequest uygulama yöntemi yazma gövdesidir.
type applicationMethodRequest struct {
	// Type indirimin ölçüsüdür (fixed | percentage).
	Type string `json:"type"`
	// TargetType indirimin hedefidir (items | shipping_methods | order).
	TargetType string `json:"target_type"`
	// Allocation dağıtım biçimidir (each | across).
	Allocation string `json:"allocation"`
	// Value sabit tutar (minor unit) ya da baz puandır.
	Value int64 `json:"value"`
	// MaxQuantity sabit tutarın uygulanacağı azami adettir.
	MaxQuantity *int64 `json:"max_quantity"`
	// CurrencyCode sabit tutarlı indirimin para birimidir.
	CurrencyCode string `json:"currency_code"`
}

// setApplicationMethod promosyonun uygulama yöntemini yazar
// (PUT /admin/v1/promotions/{id}/application-method).
//
// Yerine koymadır: promosyonun zaten bir yöntemi varsa üzerine yazılır.
func (a *API) setApplicationMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req applicationMethodRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	method, err := a.svc.SetApplicationMethod(ctx, pathID(r, "id"), service.ApplicationMethodInput{
		Type:         models.ApplicationMethodType(req.Type),
		TargetType:   models.ApplicationTargetType(req.TargetType),
		Allocation:   models.Allocation(req.Allocation),
		Value:        req.Value,
		MaxQuantity:  req.MaxQuantity,
		CurrencyCode: req.CurrencyCode,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toApplicationMethodDTO(method))
}

// deleteApplicationMethod yöntemi soft delete ile siler
// (DELETE /admin/v1/promotions/{id}/application-method).
func (a *API) deleteApplicationMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteApplicationMethod(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// promotionRuleRequest kural ekleme gövdesidir.
type promotionRuleRequest struct {
	// RuleType kuralın neye baktığıdır (context | target).
	RuleType string `json:"rule_type"`
	// Attribute bakılacak alan adıdır.
	Attribute string `json:"attribute"`
	// Operator karşılaştırma işlecidir.
	Operator string `json:"operator"`
	// Values karşılaştırmanın sağ tarafıdır.
	Values []string `json:"values"`
}

// listPromotionRules bir promosyonun kurallarını döner
// (GET /admin/v1/promotions/{id}/rules).
func (a *API) listPromotionRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := a.svc.ListPromotionRules(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPromotionRuleDTOs(rules))
}

// createPromotionRule bir promosyona kural ekler
// (POST /admin/v1/promotions/{id}/rules).
func (a *API) createPromotionRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req promotionRuleRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rule, err := a.svc.AddPromotionRule(ctx, pathID(r, "id"), service.RuleInput{
		RuleType:  models.RuleType(req.RuleType),
		Attribute: req.Attribute,
		Operator:  models.RuleOperator(req.Operator),
		Values:    req.Values,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPromotionRuleDTO(rule))
}

// deletePromotionRule kuralı soft delete ile siler
// (DELETE /admin/v1/promotion-rules/{id}).
func (a *API) deletePromotionRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePromotionRule(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listRedemptions bir promosyonun kullanım defterini döner
// (GET /admin/v1/promotions/{id}/redemptions).
func (a *API) listRedemptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListRedemptions(ctx, pathID(r, "id"), limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toRedemptionDTO)
}

// redeemRequest kullanım yazma gövdesidir.
type redeemRequest struct {
	// Reference kullanımın iş kaydı referansıdır; idempotency anahtarıdır.
	Reference string `json:"reference"`
	// Amount uygulanan indirim tutarıdır (minor unit).
	Amount int64 `json:"amount"`
	// CurrencyCode indirimin para birimidir.
	CurrencyCode string `json:"currency_code"`
}

// redeemPromotion promosyonu bir referans için kullanır
// (POST /admin/v1/promotions/{id}/redeem).
//
// İDEMPOTENTTİR: aynı referansla ikinci istek sayacı artırmaz ve var olan
// kaydı döner. Bu yüzden yanıt 201 değil 200'dür — istek her zaman yeni bir
// kayıt YARATMAZ.
//
// Promosyon taslak/pasifse, kampanyasının penceresi kapalıysa ya da bir sayaç
// sınırı aşılacaksa 409 döner; sebeplerin tamamı [service.Service.RedeemPromotion]
// godoc'undadır. Yönetim yüzeyi olması bu denetimleri GEVŞETMEZ: sayaç ve bütçe
// aynı defteri besler.
func (a *API) redeemPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req redeemRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	redemption, err := a.svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID:  pathID(r, "id"),
		Reference:    req.Reference,
		Amount:       req.Amount,
		CurrencyCode: req.CurrencyCode,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toRedemptionDTO(redemption))
}

// releaseRequest kullanım geri alma gövdesidir.
type releaseRequest struct {
	// Reference geri alınacak kullanımın referansıdır.
	Reference string `json:"reference"`
}

// releaseResultDTO geri alma yanıtının gövdesidir.
type releaseResultDTO struct {
	// Released BU İSTEKTE bir şeyin geri alınıp alınmadığını bildirir.
	//
	// false, isteğin başarısız olduğu anlamına GELMEZ: telafi idempotenttir ve
	// zaten geri alınmış bir kullanım için ikinci çağrı hata vermez.
	Released bool `json:"released"`
}

// releasePromotion bir kullanımı serbest bırakır
// (POST /admin/v1/promotions/{id}/release).
//
// İDEMPOTENTTİR: ikinci çağrı hata vermez ve sayaçlar ikinci kez düşmez.
func (a *API) releasePromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req releaseRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	released, err := a.svc.ReleasePromotion(ctx, service.ReleaseInput{
		PromotionID: pathID(r, "id"),
		Reference:   req.Reference,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, releaseResultDTO{Released: released})
}

// computeRequest indirim hesabı gövdesidir.
//
// Alan adları interop şemasıyla BİREBİR aynıdır (bkz. service paketindeki
// interop.go); iki yüzeyin aynı hesabı farklı adlarla istemesi, operatörün
// yönetim ekranında denediği isteğin sepet akışında farklı davranması demek
// olurdu.
type computeRequest struct {
	// CurrencyCode sepetin para birimidir.
	CurrencyCode string `json:"currency_code"`
	// Context bağlam kurallarının bakacağı alanlardır.
	Context map[string]string `json:"context"`
	// Items sepet kalemleridir.
	Items []computeItemRequest `json:"items"`
	// ShippingMethods sepetin kargo yöntemleridir.
	ShippingMethods []computeShippingRequest `json:"shipping_methods"`
	// Codes uygulanacak kupon kodlarıdır.
	Codes []string `json:"codes"`
	// At hesabın yapıldığı andır; boşsa "şimdi".
	At *time.Time `json:"at"`
}

// computeItemRequest hesaptaki tek bir kalemin gövdesidir.
type computeItemRequest struct {
	// ID kalemin kimliğidir.
	ID string `json:"id"`
	// Amount kalemin ara toplamıdır (birim × adet), minor unit.
	Amount int64 `json:"amount"`
	// Quantity kalemin adedidir.
	Quantity int64 `json:"quantity"`
	// Attributes hedef kurallarının bakacağı özniteliklerdir.
	Attributes map[string]string `json:"attributes"`
}

// computeShippingRequest hesaptaki tek bir kargo yönteminin gövdesidir.
type computeShippingRequest struct {
	// ID kargo yönteminin kimliğidir.
	ID string `json:"id"`
	// Amount kargo tutarıdır (minor unit).
	Amount int64 `json:"amount"`
	// Attributes hedef kurallarının bakacağı özniteliklerdir.
	Attributes map[string]string `json:"attributes"`
}

// computeDiscounts verilen sepet bağlamı için indirimleri hesaplar
// (POST /admin/v1/promotions/compute).
//
// YAN ETKİSİZDİR: hiçbir sayaç değişmez. Uç nokta yönetim tarafındadır çünkü
// gövdesi promosyonların KİMLİKLERİNİ ve kodlarını döner; müşteri tarafındaki
// karşılığı, indirimi sepet toplamına yazan sepet akışıdır.
func (a *API) computeDiscounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req computeRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ComputeInput{
		CurrencyCode:    req.CurrencyCode,
		Context:         req.Context,
		Items:           make([]service.ComputeItem, 0, len(req.Items)),
		ShippingMethods: make([]service.ComputeShippingMethod, 0, len(req.ShippingMethods)),
		Codes:           req.Codes,
	}
	if req.At != nil {
		in.At = *req.At
	}
	for i := range req.Items {
		in.Items = append(in.Items, service.ComputeItem{
			ID:         req.Items[i].ID,
			Amount:     req.Items[i].Amount,
			Quantity:   req.Items[i].Quantity,
			Attributes: req.Items[i].Attributes,
		})
	}
	for i := range req.ShippingMethods {
		in.ShippingMethods = append(in.ShippingMethods, service.ComputeShippingMethod{
			ID:         req.ShippingMethods[i].ID,
			Amount:     req.ShippingMethods[i].Amount,
			Attributes: req.ShippingMethods[i].Attributes,
		})
	}

	result, err := a.svc.ComputeDiscounts(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toComputeResultDTO(result))
}
