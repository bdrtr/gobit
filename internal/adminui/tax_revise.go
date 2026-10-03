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

// Correcting a tax rate (ADR 0378): each rate on the Taxes screen corrects
// its name and its rate from the ones it was drawn with, through the tax
// module's panel surface, for an operator who may write the taxes. The code,
// the default and the stack are not corrected here.

// ServiceTaxAdmin is the tax module's panel surface, spelled by hand and
// pinned against the module's constant in internal/arch.
const ServiceTaxAdmin = "tax.admin"

// TaxRatePath corrects one tax rate.
const TaxRatePath = TaxesPath + "/rates/{id}"

// scopeTaxWrite is the tax module's write privilege.
const scopeTaxWrite = "tax:write"

// The rate form's fields: the terms the rate was drawn with and the typed
// ones, the rate as a percent.
const (
	formTaxReadName = "read_name"
	formTaxReadRate = "read_rate_bps"
	formTaxName     = "name"
	formTaxRate     = "rate"
	formTaxRateID   = "tax_rate_id"
)

// TaxRateReviser is the narrow surface a tax rate is corrected through.
type TaxRateReviser interface {
	// ReviseTaxRate corrects the rate from the terms read, both as JSON, and
	// refuses when they are no longer the ones read.
	ReviseTaxRate(ctx context.Context, id string, read, next json.RawMessage) error
}

// taxRateTerms are a rate's terms as the surface takes them; the json tags
// spell the fields the tax region provider publishes on a rate.
type taxRateTerms struct {
	Name    string `json:"name"`
	RateBps int64  `json:"rate_bps"`
}

// reviseTaxRate corrects the rate in the path from the terms it was drawn
// with and returns to the page, which says so; a refusal comes back on the
// page with what was typed for that rate (ADR 0378).
func (u *UI) reviseTaxRate(w http.ResponseWriter, r *http.Request) {
	if u.taxes == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Taxes unavailable",
			"The tax module's panel surface cannot correct a tax rate in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	name := strings.TrimSpace(r.PostFormValue(formTaxName))
	err := u.sendTaxRateRevision(r, u.taxes, id, name)
	switch {
	case err == nil:
		landing := url.Values{paramWritten: {name}}
		if page := pageNumber(r.URL.Query().Get("page")); page > 1 {
			landing.Set("page", strconv.Itoa(page))
		}
		corehttp.WriteRedirect(r.Context(), w, TaxesPath+"?"+landing.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		typed := r.PostForm
		typed.Set(formTaxRateID, id)
		u.renderTaxes(w, r, http.StatusUnprocessableEntity, messageFor(err), typed)
	default:
		u.unexpectedFailure(w, r, err, "The tax rate could not be corrected")
	}
}

// sendTaxRateRevision reads the rate's drawn terms and the typed ones, the
// rate typed as a percent, and sends them.
func (u *UI) sendTaxRateRevision(r *http.Request, reviser TaxRateReviser, id, name string) error {
	readRate, err := strconv.ParseInt(r.PostFormValue(formTaxReadRate), 10, 64)
	if err != nil {
		return errors.Invalid("admin_ui_read_tax_rate",
			"The rate it was drawn with could not be read; draw the list again.")
	}
	rate, err := parseAmount(strings.TrimSpace(r.PostFormValue(formTaxRate)), 2, false)
	if err != nil {
		return err
	}
	read, err := json.Marshal(taxRateTerms{Name: r.PostFormValue(formTaxReadName), RateBps: readRate})
	if err != nil {
		return err
	}
	next, err := json.Marshal(taxRateTerms{Name: name, RateBps: rate})
	if err != nil {
		return err
	}

	return reviser.ReviseTaxRate(r.Context(), id, read, next)
}
