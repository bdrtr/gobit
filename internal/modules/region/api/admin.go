package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// createRegion is the handler for POST /admin/v1/regions.
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

// listRegions is the handler for GET /admin/v1/regions.
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

// getRegion is the handler for GET /admin/v1/regions/{id}.
func (a *API) getRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	region, err := a.svc.GetRegion(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toRegionDTO(region))
}

// updateRegion is the handler for PUT /admin/v1/regions/{id}.
//
// The method is PUT, but the semantics are PARTIAL: a field that is not given
// does not change. This is a deliberate simplification — offering PATCH as a
// separate method would mean two body shapes and two validation paths, while a
// PUT that is not partial would silently zero a field the client forgot to
// send.
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

// deleteRegion is the handler for DELETE /admin/v1/regions/{id}.
func (a *API) deleteRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteRegion(ctx, pathParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// addCountry is the handler for POST /admin/v1/regions/{id}/countries.
//
// If the country belongs to another region the service returns
// errors.Conflict and corehttp turns that into 409; the handler does not
// choose a status.
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

// removeCountry is the handler for DELETE /admin/v1/regions/{id}/countries/{code}.
func (a *API) removeCountry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.RemoveCountryFromRegion(ctx, pathParam(r, "id"), pathParam(r, "code")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listRegionCountries is the handler for GET /admin/v1/regions/{id}/countries.
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

// listCountries is the handler for GET /admin/v1/countries.
//
// If the "region_id" query parameter is given, only that region's countries
// are returned. If the parameter IS given but empty, that is not read as "no
// filter"; the service rejects the empty ID, because an empty value is the
// client's mistake and silently returning the whole list would hide it.
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

// listCurrencies is the handler for GET /admin/v1/currencies.
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

// getCurrency is the handler for GET /admin/v1/currencies/{code}.
func (a *API) getCurrency(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	currency, err := a.svc.GetCurrency(ctx, pathParam(r, "code"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCurrencyDTO(currency))
}
