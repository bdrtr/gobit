package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// storeListRegions GET /store/v1/regions handler'ıdır.
//
// Vitrinin para birimi/bölge seçimi buradan beslenir: her bölge para
// biriminin sembolü ve ONDALIK BASAMAK sayısıyla birlikte döner, çünkü
// tutarlar minor unit tam sayıdır ve istemci bölme çarpanını aynı yanıttan
// öğrenmelidir (bkz. currencyDTO.DecimalDigits).
func (a *API) storeListRegions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListStoreRegions(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toStoreRegionDTO)
}

// storeGetRegion GET /store/v1/regions/{id} handler'ıdır.
func (a *API) storeGetRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	region, err := a.svc.GetStoreRegion(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStoreRegionDTO(region))
}

// toStoreRegionDTO vitrin görünümünü yanıt gövdesine çevirir.
func toStoreRegionDTO(item service.StoreRegion) storeRegionDTO {
	dto := storeRegionDTO{
		ID:           item.Region.ID,
		Name:         item.Region.Name,
		CurrencyCode: item.Region.CurrencyCode,
		Countries:    make([]countryDTO, 0, len(item.Countries)),
	}
	if item.Currency != nil {
		currency := toCurrencyDTO(*item.Currency)
		dto.Currency = &currency
	}
	for i := range item.Countries {
		dto.Countries = append(dto.Countries, toCountryDTO(item.Countries[i]))
	}
	return dto
}
