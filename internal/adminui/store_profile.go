package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The store profile screen (ADR 0336): who the shop is, which every invoice
// is issued under (ADR 0115), read and written through the settings module's
// panel surface from the profile the page was drawn with.

// StoreProfilePath shows the store profile and takes the form that writes it.
const StoreProfilePath = URLPrefix + "/store-profile"

// ServiceSettingsAdmin is the settings module's panel surface, spelled by
// hand and pinned against the module's constant in internal/arch.
const ServiceSettingsAdmin = "settings.admin"

// storeProfileLabel is what the section is called on screen.
const storeProfileLabel = "Store profile"

// The settings module's privileges, as its admin API names them.
const (
	scopeSettingsRead  = "settings:read"
	scopeSettingsWrite = "settings:write"
)

// paramWritten says the form just wrote the profile, in the address it
// lands on.
const paramWritten = "written"

// The profile form's fields: the moment the page was drawn with, and the
// shop's printed identity.
const (
	formReadUpdatedAt  = "read_updated_at"
	formProfileName    = "legal_name"
	formProfileTaxNo   = "tax_number"
	formProfileOffice  = "tax_office"
	formProfileEmail   = "email"
	formProfileAddress = "address"
	formProfileCountry = "country_code"
)

// StoreProfileAdmin is the narrow surface the shop's identity is read and
// written through: the settings module's.
type StoreProfileAdmin interface {
	// StoreProfileJSON is the profile with the moment it was last written, or
	// JSON null while the shop has not said who it is.
	StoreProfileJSON(ctx context.Context) (json.RawMessage, error)
	// ReviseStoreProfile writes the profile from the one read, named by that
	// moment in RFC 3339, empty for none.
	ReviseStoreProfile(ctx context.Context, readUpdatedAt string, profile json.RawMessage) error
}

// storeProfile is the profile as the surface reads and takes it; the json
// tags are the contract with that surface, exercised end to end.
type storeProfile struct {
	LegalName   string     `json:"legal_name"`
	TaxNumber   string     `json:"tax_number"`
	TaxOffice   string     `json:"tax_office"`
	Email       string     `json:"email"`
	Address     string     `json:"address"`
	CountryCode string     `json:"country_code"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

// showStoreProfile renders the profile.
func (u *UI) showStoreProfile(w http.ResponseWriter, r *http.Request) {
	u.renderStoreProfile(w, r, http.StatusOK, "", nil)
}

// writeStoreProfile writes the profile the form describes from the one the
// page was drawn with and returns to the page, which says so; a refusal, a
// profile written since included, comes back with what was typed (ADR 0336).
func (u *UI) writeStoreProfile(w http.ResponseWriter, r *http.Request) {
	if u.settings == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Store profile unavailable",
			"The settings module's panel surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	profile, err := json.Marshal(storeProfile{
		LegalName:   strings.TrimSpace(r.PostFormValue(formProfileName)),
		TaxNumber:   strings.TrimSpace(r.PostFormValue(formProfileTaxNo)),
		TaxOffice:   strings.TrimSpace(r.PostFormValue(formProfileOffice)),
		Email:       strings.TrimSpace(r.PostFormValue(formProfileEmail)),
		Address:     strings.TrimSpace(lineFeeds(r.PostFormValue(formProfileAddress))),
		CountryCode: strings.ToUpper(strings.TrimSpace(r.PostFormValue(formProfileCountry))),
	})
	if err == nil {
		err = u.settings.ReviseStoreProfile(r.Context(), r.PostFormValue(formReadUpdatedAt), profile)
	}
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, StoreProfilePath+"?"+url.Values{paramWritten: {"1"}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderStoreProfile(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The store profile could not be written")
	}
}

// renderStoreProfile shows the profile and, to an operator who may write it,
// the form drawn from it, or from what was typed when a write was refused;
// the moment the form carries is always the profile's as it is now. An
// operator who may write and not read it is told the reason alone (ADR
// 0260).
func (u *UI) renderStoreProfile(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeSettingsRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	if u.settings == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Store profile unavailable",
			"The settings module's panel surface is not registered in this installation.")
		return
	}
	raw, err := u.settings.StoreProfileJSON(r.Context())
	var profile *storeProfile
	if err == nil {
		err = json.Unmarshal(raw, &profile)
	}
	if err != nil {
		u.unexpectedFailure(w, r, err, "The store profile could not be read")
		return
	}

	form := storeProfile{}
	readAt := ""
	if profile != nil {
		form = *profile
		readAt = profile.UpdatedAt.Format(time.RFC3339Nano)
	}
	if typed != nil {
		form = storeProfile{
			LegalName: typed.Get(formProfileName), TaxNumber: typed.Get(formProfileTaxNo),
			TaxOffice: typed.Get(formProfileOffice), Email: typed.Get(formProfileEmail),
			Address: typed.Get(formProfileAddress), CountryCode: typed.Get(formProfileCountry),
		}
	}

	u.templates.render(w, r, code, "store_profile.gohtml", map[string]any{
		titleKey:   storeProfileLabel,
		"Profile":  profile,
		"Form":     form,
		"ReadAt":   readAt,
		"CanWrite": principal.HasScope(scopeSettingsWrite),
		"Written":  r.URL.Query().Get(paramWritten) != "",
		refusedKey: refused,
		pathKey:    StoreProfilePath,
	})
}
