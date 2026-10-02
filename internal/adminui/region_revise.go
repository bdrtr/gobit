package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Correcting a region (ADR 0362): each row of the Regions screen corrects
// its region's name, whether taxes are computed for it and its tax rate from
// the ones the row was drawn with, through the region module's panel
// surface, for an operator who may write the regions. The currency is not
// corrected here.

// ServiceRegionAdmin is the region module's panel surface, spelled by hand
// and pinned against the module's constant in internal/arch.
const ServiceRegionAdmin = "region.admin"

// RegionPath corrects one region.
const RegionPath = RegionsPath + "/{id}"

// scopeRegionWrite is the region module's write privilege.
const scopeRegionWrite = "region:write"

// The row form's fields: the terms the row was drawn with and the typed
// ones, the rate as a percent.
const (
	formRegionReadName      = "read_name"
	formRegionReadAutomatic = "read_automatic_taxes"
	formRegionReadRate      = "read_tax_rate"
	formRegionName          = "name"
	formRegionAutomatic     = "automatic_taxes"
	formRegionTaxRate       = "tax_rate"
	formRegionID            = "region_id"
)

// RegionReviser is the narrow surface a region is corrected through.
type RegionReviser interface {
	// ReviseRegion corrects the region from the terms read, both as JSON, and
	// refuses when they are no longer the ones read.
	ReviseRegion(ctx context.Context, id string, read, next json.RawMessage) error
}

// regionTerms are a region's terms as the surface takes them; the json tags
// spell the fields the region provider publishes.
type regionTerms struct {
	Name           string `json:"name"`
	AutomaticTaxes bool   `json:"automatic_taxes"`
	TaxRate        int64  `json:"tax_rate"`
}

// reviseRegion corrects the region in the path from the terms its row was
// drawn with and returns to the page, which says so; a refusal comes back on
// the page with what was typed in that row (ADR 0362).
func (u *UI) reviseRegion(w http.ResponseWriter, r *http.Request) {
	if u.regions == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Regions unavailable",
			"The region module's panel surface cannot correct a region in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	name := strings.TrimSpace(r.PostFormValue(formRegionName))
	err := u.sendRegionRevision(r, u.regions, id, name)
	switch {
	case err == nil:
		landing := url.Values{paramWritten: {name}}
		if page := pageNumber(r.URL.Query().Get("page")); page > 1 {
			landing.Set("page", strconv.Itoa(page))
		}
		corehttp.WriteRedirect(r.Context(), w, RegionsPath+"?"+landing.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		typed := r.PostForm
		typed.Set(formRegionID, id)
		u.renderRegions(w, r, http.StatusUnprocessableEntity, messageFor(err), typed)
	default:
		u.unexpectedFailure(w, r, err, "The region could not be corrected")
	}
}

// sendRegionRevision reads the row's drawn terms and the typed ones, the rate
// typed as a percent, and sends them.
func (u *UI) sendRegionRevision(r *http.Request, reviser RegionReviser, id, name string) error {
	readRate, rateErr := strconv.ParseInt(r.PostFormValue(formRegionReadRate), 10, 64)
	readAutomatic, autoErr := strconv.ParseBool(r.PostFormValue(formRegionReadAutomatic))
	if rateErr != nil || autoErr != nil {
		return errors.Invalid("admin_ui_read_region",
			"The tax rate or the automatic taxes the row was drawn with could not be read; draw the list again.")
	}
	rate, err := parseAmount(strings.TrimSpace(r.PostFormValue(formRegionTaxRate)), 2, false)
	if err != nil {
		return err
	}
	read, err := json.Marshal(regionTerms{
		Name: r.PostFormValue(formRegionReadName), AutomaticTaxes: readAutomatic, TaxRate: readRate,
	})
	if err != nil {
		return err
	}
	next, err := json.Marshal(regionTerms{
		Name: name, AutomaticTaxes: r.PostFormValue(formRegionAutomatic) != "", TaxRate: rate,
	})
	if err != nil {
		return err
	}

	return reviser.ReviseRegion(r.Context(), id, read, next)
}
