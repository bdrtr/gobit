package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// listProviders kayıtlı kargo sağlayıcılarının kimliklerini döner.
//
// YALNIZCA yönetim yüzeyine bağlıdır: hangi kargo firmalarıyla çalışıldığı
// mağazanın operasyonel bilgisidir ve müşteriye gösterilmez (payment'taki
// ödeme sağlayıcılarından farkı budur — orada müşteri hangi ödeme yolunu
// seçeceğini bilmek zorundadır).
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeList(ctx, w, h.svc.ProviderIDs(ctx))
}

// --- kargo profilleri --------------------------------------------------------

// createProfileRequest POST /admin/v1/shipping-profiles gövdesidir.
type createProfileRequest struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Metadata map[string]any `json:"metadata"`
}

// createProfile yeni bir kargo profili oluşturur.
func (h *Handler) createProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createProfileRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	profile, err := h.svc.CreateShippingProfile(ctx, service.CreateProfileInput{
		Name:     body.Name,
		Type:     body.Type,
		Metadata: body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toProfileDTO(profile)})
}

// listProfiles kargo profillerini sayfalayarak döner.
func (h *Handler) listProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListProfilesInput{Page: page}
	if raw := r.URL.Query().Get("type"); raw != "" {
		in.Type = &raw
	}

	profiles, count, err := h.svc.ListShippingProfiles(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]profileDTO, 0, len(profiles))
	for i := range profiles {
		data = append(data, toProfileDTO(profiles[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getProfile profili kimliğiyle döner.
func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	profile, err := h.svc.GetShippingProfile(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toProfileDTO(profile)})
}

// updateProfileRequest PATCH /admin/v1/shipping-profiles/{id} gövdesidir.
//
// Alanlar İŞARETÇİDİR: "gönderilmedi" ile "boş gönderildi" ayrımı korunur;
// gönderilmeyen alan değişmez.
type updateProfileRequest struct {
	Name     *string        `json:"name"`
	Type     *string        `json:"type"`
	Metadata map[string]any `json:"metadata"`
}

// updateProfile profilin verilen alanlarını günceller.
func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateProfileRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	profile, err := h.svc.UpdateShippingProfile(ctx, chi.URLParam(r, "id"), service.UpdateProfileInput{
		Name:     body.Name,
		Type:     body.Type,
		Metadata: body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toProfileDTO(profile)})
}

// deleteProfile profili yumuşak siler.
func (h *Handler) deleteProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingProfile(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- kargo seçenekleri -------------------------------------------------------

// createOptionRequest POST /admin/v1/shipping-options gövdesidir.
type createOptionRequest struct {
	Name              string `json:"name"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	PriceType         string `json:"price_type"`
	// Amount minor unit TAM SAYIDIR ve yalnızca "flat" seçeneklerde anlamlıdır.
	Amount       int64          `json:"amount"`
	CurrencyCode string         `json:"currency_code"`
	RegionID     string         `json:"region_id"`
	IsReturn     bool           `json:"is_return"`
	AdminOnly    bool           `json:"admin_only"`
	Data         map[string]any `json:"data"`
	Metadata     map[string]any `json:"metadata"`
}

// createOption yeni bir kargo seçeneği oluşturur.
func (h *Handler) createOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createOptionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	option, err := h.svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              body.Name,
		ProviderID:        body.ProviderID,
		ShippingProfileID: body.ShippingProfileID,
		PriceType:         body.PriceType,
		Amount:            body.Amount,
		CurrencyCode:      body.CurrencyCode,
		RegionID:          body.RegionID,
		IsReturn:          body.IsReturn,
		AdminOnly:         body.AdminOnly,
		Data:              body.Data,
		Metadata:          body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toOptionDTO(option)})
}

// listOptions kargo seçeneklerini sayfalayarak döner.
func (h *Handler) listOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListOptionsAdminInput{Page: page}
	if raw := r.URL.Query().Get("region_id"); raw != "" {
		in.RegionID = &raw
	}
	if raw := r.URL.Query().Get("shipping_profile_id"); raw != "" {
		in.ProfileID = &raw
	}
	if raw := r.URL.Query().Get("provider_id"); raw != "" {
		in.ProviderID = &raw
	}
	if raw := r.URL.Query().Get("price_type"); raw != "" {
		in.PriceType = &raw
	}

	options, count, err := h.svc.ListShippingOptions(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]optionDTO, 0, len(options))
	for i := range options {
		data = append(data, toOptionDTO(options[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getOption seçeneği kurallarıyla birlikte döner.
func (h *Handler) getOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	option, err := h.svc.GetShippingOption(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOptionDTO(option)})
}

// updateOptionRequest PATCH /admin/v1/shipping-options/{id} gövdesidir.
//
// provider_id ve shipping_profile_id BURADA YOKTUR; gerekçe
// [service.UpdateOptionInput] belgesindedir.
type updateOptionRequest struct {
	Name      *string        `json:"name"`
	PriceType *string        `json:"price_type"`
	Amount    *int64         `json:"amount"`
	RegionID  *string        `json:"region_id"`
	IsReturn  *bool          `json:"is_return"`
	AdminOnly *bool          `json:"admin_only"`
	Data      map[string]any `json:"data"`
	Metadata  map[string]any `json:"metadata"`
}

// updateOption seçeneğin verilen alanlarını günceller.
func (h *Handler) updateOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateOptionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	option, err := h.svc.UpdateShippingOption(ctx, chi.URLParam(r, "id"), service.UpdateOptionInput{
		Name:      body.Name,
		PriceType: body.PriceType,
		Amount:    body.Amount,
		RegionID:  body.RegionID,
		IsReturn:  body.IsReturn,
		AdminOnly: body.AdminOnly,
		Data:      body.Data,
		Metadata:  body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOptionDTO(option)})
}

// deleteOption seçeneği yumuşak siler.
func (h *Handler) deleteOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingOption(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- kargo seçeneği kuralları ------------------------------------------------

// createRuleRequest POST /admin/v1/shipping-options/{id}/rules gövdesidir.
type createRuleRequest struct {
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"`
	Values    []string `json:"values"`
}

// createRule bir seçeneğe kural ekler.
func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createRuleRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rule, err := h.svc.CreateShippingOptionRule(ctx, chi.URLParam(r, "id"), service.CreateRuleInput{
		Attribute: body.Attribute,
		Operator:  body.Operator,
		Values:    body.Values,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toRuleDTO(rule)})
}

// listRules bir seçeneğin kurallarını döner.
func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := h.svc.ListShippingOptionRules(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]ruleDTO, 0, len(rules))
	for i := range rules {
		data = append(data, toRuleDTO(rules[i]))
	}
	writeList(ctx, w, data)
}

// deleteRule kuralı yumuşak siler.
func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingOptionRule(ctx, chi.URLParam(r, "rule_id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- uygunluk listelemesi ----------------------------------------------------

// listStoreEligibleOptions GET /store/v1/shipping-options ucudur.
//
// admin_only seçenekler ASLA dönmez ve istemci bunu isteyemez: bayrak sorgu
// parametresinden okunmaz, sabit olarak false verilir. Okunsaydı, vitrinden
// gelen tek bir parametre yönetime özel seçenekleri açardı.
//
// # Sepet olguları BURADA DOĞRULANAMAZ
//
// subtotal, item_count ve total_weight sorgu parametrelerinden gelir; sepet
// cart modülünün verisidir ve bu modül onu ne hesaplayabilir ne doğrulayabilir
// (Prensip 2.1). Yani üçü de İSTEMCİNİN İDDİASIDIR: boş sepetle
// "?subtotal=50000" göndermek serbesttir.
//
// Bu yüzden uç, servise TrustedFacts=false ile gider ve o bayrak, bu üç olguya
// BAĞLI kuralı olan seçenekleri listeden tümüyle çıkarır (gerekçe:
// [service.Service.ListShippingOptionsFor]). Uç böylece bir "kural oracle"ı olmaktan
// çıkar: uydurulmuş bir ara toplam artık kimseye kapalı bir seçeneği açmaz.
//
// Kalan iki sınır AÇIKÇA kabul edilmiştir:
//
//   - Fiyat GÖSTERİMDİR. "calculated" bir seçeneğin ücreti istemcinin bildirdiği
//     ağırlıkla hesaplanır; gerçek ücret, sepetin gerçek olgularıyla ödeme
//     adımında yeniden belirlenmelidir.
//   - Serbest kural bağlamı ([service.ListOptionsInput.Attributes]) bu uçtan
//     HİÇ okunmaz. Sonucu: "customer_group_id" gibi bir alana bağlanmış
//     seçenekler HTTP uygunluk uçlarında (yönetim ucu dâhil) listelenemez.
//     Okunması, müşterinin kendi grubunu ilan etmesi demek olurdu; gerçek
//     değerin sahibi bu modül değildir ve bağlamı taşıyan yol
//     [service.Interop]'tur.
func (h *Handler) listStoreEligibleOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	in, err := parseEligibilityQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	in.IncludeAdminOnly = false
	in.TrustedFacts = false

	quoted, err := h.svc.ListShippingOptionsFor(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]storeOptionDTO, 0, len(quoted))
	for i := range quoted {
		data = append(data, toStoreOptionDTO(quoted[i]))
	}
	writeList(ctx, w, data)
}

// listAdminEligibleOptions GET /admin/v1/shipping-options/eligible ucudur.
//
// Yönetim yüzeyi admin_only seçenekleri DE görür; ayrım budur.
//
// Sepet olguları burada GÜVENİLİR sayılır (TrustedFacts=true) ve kurala bağlı
// seçenekler listelenir. Gerekçe: bu uç yöneticinin "şu bağlamda hangi
// seçenekler çıkar" diye denediği bir ÖNİZLEME aracıdır; yönetici zaten tüm
// kataloğu ve kurallarını okuyabildiği için bağlamı uydurması ona yeni bir şey
// açmaz. Mağaza ucunda aynı varsayım geçerli DEĞİLDİR
// (bkz. [Handler.listStoreEligibleOptions]).
func (h *Handler) listAdminEligibleOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	in, err := parseEligibilityQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	in.IncludeAdminOnly = true
	in.TrustedFacts = true

	quoted, err := h.svc.ListShippingOptionsFor(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]quotedOptionDTO, 0, len(quoted))
	for i := range quoted {
		data = append(data, toQuotedOptionDTO(quoted[i]))
	}
	writeList(ctx, w, data)
}

// parseEligibilityQuery uygunluk listelemesinin sorgu parametrelerini çözer.
//
// IncludeAdminOnly ve TrustedFacts BURADA OKUNMAZ: ikisi de GÜVEN kararıdır ve
// değerleri handler'ın hangi yüzeye ait olduğuna göre sabitlenir
// (bkz. [Handler.listStoreEligibleOptions]). Sorgudan okunsalardı, vitrinden
// gelen tek bir parametre iki kapıyı da açardı.
func parseEligibilityQuery(r *http.Request) (service.ListOptionsInput, error) {
	query := r.URL.Query()

	subtotal, err := parseInt64Param(r, "subtotal")
	if err != nil {
		return service.ListOptionsInput{}, err
	}
	itemCount, err := parseInt64Param(r, "item_count")
	if err != nil {
		return service.ListOptionsInput{}, err
	}
	totalWeight, err := parseInt64Param(r, "total_weight")
	if err != nil {
		return service.ListOptionsInput{}, err
	}
	isReturn, err := parseBoolParam(r, "is_return")
	if err != nil {
		return service.ListOptionsInput{}, err
	}

	return service.ListOptionsInput{
		RegionID:     query.Get("region_id"),
		CurrencyCode: query.Get("currency_code"),
		CountryCode:  query.Get("country_code"),
		// Profil kimliği TEKRARLANABİLİR bir parametredir: bir sepette birden
		// çok profile bağlı ürün bulunabilir ve hepsi aynı anda sorulmalıdır.
		ShippingProfileIDs: query["shipping_profile_id"],
		Subtotal:           subtotal,
		ItemCount:          itemCount,
		TotalWeight:        totalWeight,
		IsReturn:           isReturn,
	}, nil
}

// --- gönderiler --------------------------------------------------------------

// createFulfillmentRequest POST /admin/v1/fulfillments gövdesidir.
type createFulfillmentRequest struct {
	Reference        string `json:"reference"`
	ShippingOptionID string `json:"shipping_option_id"`
	// IdempotencyKey zorunludur: aynı anahtarla ikinci istek YENİ gönderi
	// açmaz, mevcut gönderiyi döner.
	IdempotencyKey string                 `json:"idempotency_key"`
	Items          []fulfillmentItemInput `json:"items"`
	Data           map[string]any         `json:"data"`
	Metadata       map[string]any         `json:"metadata"`
}

// fulfillmentItemInput gönderi kalemi gövdesidir.
type fulfillmentItemInput struct {
	LineItemID string `json:"line_item_id"`
	// Quantity işaretçidir: "gönderilmedi" ile "sıfır gönderildi" ayrımı
	// korunur ve ikisi de reddedilir ama farklı mesajla.
	Quantity *int64 `json:"quantity"`
}

// createFulfillment sağlayıcıda bir gönderi açar.
func (h *Handler) createFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createFulfillmentRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items := make([]service.FulfillmentItemInput, 0, len(body.Items))
	for i, item := range body.Items {
		if item.Quantity == nil {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"%d. kalemin quantity alanı zorunludur", i+1))
			return
		}
		items = append(items, service.FulfillmentItemInput{
			LineItemID: item.LineItemID,
			Quantity:   *item.Quantity,
		})
	}

	ful, err := h.svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        body.Reference,
		ShippingOptionID: body.ShippingOptionID,
		IdempotencyKey:   body.IdempotencyKey,
		Items:            items,
		Data:             body.Data,
		Metadata:         body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// listFulfillments gönderileri sayfalayarak döner.
func (h *Handler) listFulfillments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListFulfillmentsInput{Page: page}
	if raw := r.URL.Query().Get("reference"); raw != "" {
		in.Reference = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		in.Status = &raw
	}

	list, count, err := h.svc.ListFulfillments(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]fulfillmentDTO, 0, len(list))
	for i := range list {
		data = append(data, toFulfillmentDTO(list[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getFulfillment gönderiyi kalemleriyle birlikte döner.
func (h *Handler) getFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ful, err := h.svc.GetFulfillment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// cancelFulfillment gönderiyi iptal eder ve GÜNCEL hâlini döner.
//
// İptal İDEMPOTENTTİR: ikinci çağrı da 200 döner. Yanıtın gövdeli olması
// bilinçlidir — çağıran, iptalin gerçekten yazıldığını durum alanından
// görebilmelidir.
func (h *Handler) cancelFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	if err := h.svc.CancelFulfillment(ctx, id); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeFulfillment(w, r, id)
}

// shipRequest POST /admin/v1/fulfillments/{id}/ship gövdesidir.
type shipRequest struct {
	TrackingNumber string `json:"tracking_number"`
	TrackingURL    string `json:"tracking_url"`
}

// shipFulfillment gönderiyi kargoya verilmiş olarak işaretler.
//
// Gövde İSTEĞE BAĞLIDIR: takip bilgisi olmadan da sevk bildirilebilir (bazı
// taşıyıcılar numarayı sonradan verir).
func (h *Handler) shipFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body shipRequest
	if err := decodeOptionalBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ful, err := h.svc.MarkShipped(ctx, chi.URLParam(r, "id"), body.TrackingNumber, body.TrackingURL)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// deliverFulfillment gönderiyi teslim edilmiş olarak işaretler.
func (h *Handler) deliverFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ful, err := h.svc.MarkDelivered(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// writeFulfillment gönderiyi okuyup tekil zarfla yazar.
//
// Gövdesi olmayan bir işlemden (iptal) sonra güncel kaydı döndürmek için
// vardır; okuma hata verirse o hata yazılır.
func (h *Handler) writeFulfillment(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	ful, err := h.svc.GetFulfillment(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}
