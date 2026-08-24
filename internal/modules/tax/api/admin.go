package api

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// createRegion POST /admin/v1/tax-regions handler'ıdır.
func (a *API) createRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createTaxRegionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	region, err := a.svc.CreateTaxRegion(ctx, toCreateTaxRegionInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxRegionDTO(region))
}

// listRegions GET /admin/v1/tax-regions handler'ıdır.
//
// "country_code" sorgu parametresi listeyi tek ülkeye daraltır; verilmezse tüm
// bölgeler döner.
func (a *API) listRegions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListTaxRegions(ctx, r.URL.Query().Get("country_code"), limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toTaxRegionDTO)
}

// getRegion GET /admin/v1/tax-regions/{id} handler'ıdır.
func (a *API) getRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	region, err := a.svc.GetTaxRegion(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toTaxRegionDTO(region))
}

// deleteRegion DELETE /admin/v1/tax-regions/{id} handler'ıdır.
//
// Silme AĞACI kapsar: alt bölgeler, oranları ve o oranların kuralları da
// yumuşak silinir (bkz. service.Service.DeleteTaxRegion). Yanıt gövdesizdir;
// silinen ağacın dökümünü döndürmek, istemcinin ihtiyacı olmayan bir listeyi
// her çağrıda üretmek olurdu.
func (a *API) deleteRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteTaxRegion(ctx, pathParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listRegionRates GET /admin/v1/tax-regions/{id}/tax-rates handler'ıdır.
func (a *API) listRegionRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rates, err := a.svc.ListTaxRates(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, rates, toTaxRateDTO)
}

// createRate POST /admin/v1/tax-rates handler'ıdır.
func (a *API) createRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createTaxRateRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rate, err := a.svc.CreateTaxRate(ctx, toCreateTaxRateInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxRateDTO(rate))
}

// listRates GET /admin/v1/tax-rates handler'ıdır.
//
// "tax_region_id" sorgu parametresi ZORUNLUDUR: oranlar daima bir bölgeye
// aittir ve bölgesiz bir oran listesi, hangi coğrafyaya ait olduğu okunamayan
// bir tablodur. Eksikse errors.Invalid döner.
func (a *API) listRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	regionID := r.URL.Query().Get("tax_region_id")
	if regionID == "" {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidBody,
			"%q sorgu parametresi zorunludur", "tax_region_id"))
		return
	}

	rates, err := a.svc.ListTaxRates(ctx, regionID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, rates, toTaxRateDTO)
}

// getRate GET /admin/v1/tax-rates/{id} handler'ıdır.
func (a *API) getRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rate, err := a.svc.GetTaxRate(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toTaxRateDTO(rate))
}

// updateRate PUT /admin/v1/tax-rates/{id} handler'ıdır.
//
// Yöntem PUT olsa da semantik KISMİDİR: verilmeyen alan değişmez. Bu bilinçli
// bir sadeleştirmedir — PATCH'i ayrı bir yöntem olarak sunmak, iki gövde şekli
// ve iki doğrulama yolu demek olurdu; kısmi olmayan bir PUT ise göndermeyi
// unutulan bir oranı sessizce sıfırlardı.
func (a *API) updateRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateTaxRateRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rate, err := a.svc.UpdateTaxRate(ctx, pathParam(r, "id"), toUpdateTaxRateInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toTaxRateDTO(rate))
}

// deleteRate DELETE /admin/v1/tax-rates/{id} handler'ıdır.
func (a *API) deleteRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteTaxRate(ctx, pathParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// createRule POST /admin/v1/tax-rates/{id}/rules handler'ıdır.
//
// Oran kimliği YOLDAN alınır: kural, oranın alt kaynağıdır ve gövdede ikinci
// kez taşınsaydı yol ile gövde çelişebilirdi.
func (a *API) createRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createTaxRateRuleRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rule, err := a.svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID:   pathParam(r, "id"),
		Reference:   body.Reference,
		ReferenceID: body.ReferenceID,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxRateRuleDTO(rule))
}

// listRules GET /admin/v1/tax-rates/{id}/rules handler'ıdır.
func (a *API) listRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := a.svc.ListRateRules(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, rules, toTaxRateRuleDTO)
}

// deleteRule DELETE /admin/v1/tax-rates/{id}/rules/{ruleID} handler'ıdır.
//
// Yoldaki oran kimliği yalnızca kaynağın yerini belirtir; silme kural
// kimliğiyle yapılır. İkisinin tutarlılığı ayrıca denetlenmez — başka bir
// oranın kuralını bu yol üzerinden silmek yalnızca yolun anlamsız yazılması
// demektir ve sonuç yine doğru kaydı siler.
func (a *API) deleteRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteRateRule(ctx, pathParam(r, "ruleID")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
