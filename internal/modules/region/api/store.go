package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// storeListRegions is the handler for GET /store/v1/regions.
//
// The storefront's currency/region picker is fed from here: every region comes
// back together with its currency's symbol and number of DECIMAL DIGITS,
// because amounts are minor-unit integers and the client has to learn the
// divisor from the same response (see currencyDTO.DecimalDigits).
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

// storeGetRegion is the handler for GET /store/v1/regions/{id}.
func (a *API) storeGetRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	region, err := a.svc.GetStoreRegion(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStoreRegionDTO(region))
}

// toStoreRegionDTO turns the storefront view into the response body.
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
