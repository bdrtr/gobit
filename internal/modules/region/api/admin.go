package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// createRegion POST /admin/v1/regions handler'ıdır.
func (a *API) createRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createRegionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	region, err := a.svc.CreateRegion(ctx, toCreateRegionInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toRegionDTO(region))
}

// listRegions GET /admin/v1/regions handler'ıdır.
func (a *API) listRegions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListRegions(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toRegionDTO)
}

// getRegion GET /admin/v1/regions/{id} handler'ıdır.
func (a *API) getRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	region, err := a.svc.GetRegion(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toRegionDTO(region))
}

// updateRegion PUT /admin/v1/regions/{id} handler'ıdır.
//
// Yöntem PUT olsa da semantik KISMİDİR: verilmeyen alan değişmez. Bu bilinçli
// bir sadeleştirmedir — PATCH'i ayrı bir yöntem olarak sunmak, iki gövde
// şekli ve iki doğrulama yolu demek olurdu; kısmi olmayan bir PUT ise
// göndermeyi unutulan bir alanı sessizce sıfırlardı.
func (a *API) updateRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateRegionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	region, err := a.svc.UpdateRegion(ctx, pathParam(r, "id"), toUpdateRegionInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toRegionDTO(region))
}

// deleteRegion DELETE /admin/v1/regions/{id} handler'ıdır.
func (a *API) deleteRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteRegion(ctx, pathParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// addCountry POST /admin/v1/regions/{id}/countries handler'ıdır.
//
// Ülke başka bir bölgeye aitse servis errors.Conflict döner ve corehttp bunu
// 409'a çevirir; handler status seçmez.
func (a *API) addCountry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addCountryRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	country, err := a.svc.AddCountryToRegion(ctx, pathParam(r, "id"), body.CountryCode)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCountryDTO(country))
}

// removeCountry DELETE /admin/v1/regions/{id}/countries/{code} handler'ıdır.
func (a *API) removeCountry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.RemoveCountryFromRegion(ctx, pathParam(r, "id"), pathParam(r, "code")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listRegionCountries GET /admin/v1/regions/{id}/countries handler'ıdır.
func (a *API) listRegionCountries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	regionID := pathParam(r, "id")
	page, err := a.svc.ListCountries(ctx, service.ListCountriesInput{
		RegionID: &regionID,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCountryDTO)
}

// listCountries GET /admin/v1/countries handler'ıdır.
//
// "region_id" sorgu parametresi verilirse yalnızca o bölgenin ülkeleri döner.
// Parametre VERİLMİŞ ama boşsa süzgeç uygulanmaz denmez; servis boş kimliği
// reddeder, çünkü boş bir değer istemcinin hatasıdır ve sessizce tüm listeyi
// döndürmek o hatayı gizlerdi.
func (a *API) listCountries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListCountries(ctx, service.ListCountriesInput{
		RegionID: optionalParam(r, "region_id"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCountryDTO)
}

// listCurrencies GET /admin/v1/currencies handler'ıdır.
func (a *API) listCurrencies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListCurrencies(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCurrencyDTO)
}

// getCurrency GET /admin/v1/currencies/{code} handler'ıdır.
func (a *API) getCurrency(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	currency, err := a.svc.GetCurrency(ctx, pathParam(r, "code"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCurrencyDTO(currency))
}
