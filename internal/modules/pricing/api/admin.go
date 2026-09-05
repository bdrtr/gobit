package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// createPriceSet yeni bir price set oluşturur (POST /admin/v1/price-sets).
//
// Gövdedeki fiyatlar aynı istekte yazılır; biri geçersizse hiçbiri yazılmaz ve
// kap da oluşturulmaz.
func (a *API) createPriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createPriceSetRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	set, err := a.svc.CreatePriceSet(ctx, toPriceInputs(req.Prices))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	prices, err := a.svc.ListPrices(ctx, set.ID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPriceSetDTO(set, prices))
}

// listPriceSets price set'leri sayfalayarak listeler (GET /admin/v1/price-sets).
//
// Liste yanıtında fiyatlar YOKTUR: bir sayfa dolusu kabın tüm fiyatlarını
// getirmek, çağıranın neredeyse hiç kullanmadığı büyük bir gövde üretirdi.
// Fiyatlar tekil uç noktadan ya da Query katmanından okunur.
func (a *API) listPriceSets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListPriceSets(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toPriceSetSummaryDTO)
}

// getPriceSet tek bir price set'i fiyatlarıyla döner
// (GET /admin/v1/price-sets/{id}).
func (a *API) getPriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathID(r, "id")

	set, err := a.svc.GetPriceSet(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	prices, err := a.svc.ListPrices(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceSetDTO(set, prices))
}

// deletePriceSet price set'i ve fiyatlarını soft delete ile siler
// (DELETE /admin/v1/price-sets/{id}).
func (a *API) deletePriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePriceSet(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listPrices bir kabın fiyatlarını döner
// (GET /admin/v1/price-sets/{id}/prices).
func (a *API) listPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	prices, err := a.svc.ListPrices(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPriceDTOs(prices))
}

// setPrices bir kabın fiyatlarını topluca DEĞİŞTİRİR
// (POST /admin/v1/price-sets/{id}/prices).
//
// İşlem yerine koymadır: gövdede olmayan fiyatlar silinir. Yazma atomiktir.
func (a *API) setPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req setPricesRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	prices, err := a.svc.SetPrices(ctx, pathID(r, "id"), toPriceInputs(req.Prices))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPriceDTOs(prices))
}

// calculatePrice bir kabın verilen bağlamdaki geçerli fiyatını seçer
// (GET /admin/v1/price-sets/{id}/calculate).
//
// POST değil GET'tir. Uç eskiden POST'tu ve gerekçesi "bağlam yapılandırılmış
// bir gövdedir, sorgu dizesine düzleştirilirse iç içe değerler kaybolur" diye
// yazılmıştı; bu gerekçenin tiplerde karşılığı yok: service.CalculateParams
// düzdür ve kural bağlamı map[string]string'tir — kaybolacak iç içe değer
// yoktur. Bedeli ise somuttu: yetki sözlüğü metoda baktığı için (bkz.
// [API.Routes]) hiçbir şey yazmayan bu uç [ScopeWrite] istiyordu ve fiyatı
// yalnızca okuyan entegrasyonlar — fiyat karşılaştırma, dışa aktarma — fiyat
// YAZABİLEN bir kimlikle çalışmak zorunda kalıyordu.
//
// Sorgu biçimi:
//
//	?currency_code=TRY&quantity=10&at=2026-06-15T12:00:00Z&attr_region_id=reg_1
//
// Kural bağlamı tek bir yapılı değer yerine [paramAttrPrefix] ÖNEKLİ ayrı
// parametrelerle taşınır: alan adları modelin kendi snake_case adlarıdır ve
// önek onları ayrılmış parametrelerden ([paramCurrencyCode], [paramQuantity],
// [paramAt]) ayırmaya yeter. İki alternatif elendi: sorguya gömülü bir JSON
// nesnesi ("attributes={...}") ve "attributes[region_id]" biçimi. İkisi de
// URL'i elle okunamaz kılar ve HTTP katmanına ikinci bir çözümleyici sokar;
// karşılığında çözecekleri bir iç içe yapı yoktur.
//
// Zaman damgası RFC 3339'dur ve saat dilimi ofsetindeki "+" sorgu dizesinde
// yüzde kodlanmalıdır ("%2B"); yoksa net/url onu boşluk olarak çözer. "Z"
// biçimi bu tuzağı hiç yaşamaz.
func (a *API) calculatePrice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params, err := calculateQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	calculated, err := a.svc.CalculatePrice(ctx, pathID(r, "id"), params)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCalculatedPriceDTO(calculated))
}

// createPriceList yeni bir fiyat listesi oluşturur (POST /admin/v1/price-lists).
func (a *API) createPriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req priceListRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	list, err := a.svc.CreatePriceList(ctx, toPriceListInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPriceListDTO(list))
}

// listPriceLists fiyat listelerini sayfalayarak döner
// (GET /admin/v1/price-lists).
func (a *API) listPriceLists(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListPriceLists(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toPriceListDTO)
}

// getPriceList tek bir fiyat listesini döner (GET /admin/v1/price-lists/{id}).
func (a *API) getPriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	list, err := a.svc.GetPriceList(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceListDTO(list))
}

// updatePriceList fiyat listesinin tüm alanlarını yazar
// (PUT /admin/v1/price-lists/{id}).
//
// PUT'tur çünkü kısmi güncelleme değildir: gövdede olmayan alanlar sıfırlanır.
func (a *API) updatePriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req priceListRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	list, err := a.svc.UpdatePriceList(ctx, pathID(r, "id"), toPriceListInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceListDTO(list))
}

// deletePriceList fiyat listesini soft delete ile siler
// (DELETE /admin/v1/price-lists/{id}).
func (a *API) deletePriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePriceList(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listPriceRules bir fiyatın kurallarını döner
// (GET /admin/v1/prices/{price_id}/rules).
func (a *API) listPriceRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := a.svc.ListPriceRules(ctx, pathID(r, "price_id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPriceRuleDTOs(rules))
}

// createPriceRule bir fiyata kural ekler
// (POST /admin/v1/prices/{price_id}/rules).
func (a *API) createPriceRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ruleRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rules := toRuleInputs([]ruleRequest{req})
	rule, err := a.svc.CreatePriceRule(ctx, pathID(r, "price_id"), rules[0])
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPriceRuleDTO(rule))
}

// deletePriceRule kuralı soft delete ile siler
// (DELETE /admin/v1/price-rules/{id}).
func (a *API) deletePriceRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePriceRule(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
