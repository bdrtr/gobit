// Package api is the settings module's HTTP surface.
//
// It has ONE resource and no store namespace. The shop's identity is printed on
// documents an operator issues; a shopper has no question this endpoint answers,
// and publishing the shop's tax number on a surface reached with a publishable
// key would hand it to every browser.
//
// # Authorization
//
// Two scopes, the way every other module splits them:
//
//   - [ScopeRead] ("settings:read") opens the GET.
//   - [ScopeWrite] ("settings:write") opens the PUT.
//
// corehttp.ScopeAdmin ("admin") is the parent scope and satisfies both.
//
// Handlers do not CHOOSE status codes: the service returns typed errors and
// corehttp.WriteError turns them into codes, so the classification lives in one
// place.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/settings/models"
	"github.com/bdrtr/gobit/internal/modules/settings/service"
)

// The scopes this surface asks for.
const (
	// ScopeRead is what the GET wants.
	ScopeRead = "settings:read"
	// ScopeWrite is what the PUT wants.
	ScopeWrite = "settings:write"
)

// pathAdminStoreProfile is the one address this module binds.
const pathAdminStoreProfile = "/admin/v1/store-profile"

// maxBodyBytes bounds the request body.
//
// The record is six short fields; the bound is generous and exists to stop a
// broken client rather than to express a rule.
const maxBodyBytes = 64 << 10

// codeInvalidRequest reports a body that could not be read or parsed.
const codeInvalidRequest = "settings_invalid_request"

// Profiles is what this surface needs from the service.
//
// It is declared here rather than taken as the concrete type so the handler can
// be tested without a database, which is what every other module's API package
// does.
type Profiles interface {
	// GetProfile returns the shop's identity; NotFound when it has none.
	GetProfile(ctx context.Context) (models.StoreProfile, error)
	// SetProfile writes the identity, replacing what was there.
	SetProfile(ctx context.Context, in service.SetProfileInput) (models.StoreProfile, error)
}

// API is the settings module's HTTP handler set.
type API struct {
	svc Profiles
}

// New builds the API over the given service.
func New(svc Profiles) *API { return &API{svc: svc} }

// Routes binds the module's endpoints.
func (a *API) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	read.Get(pathAdminStoreProfile, a.getStoreProfile)
	write.Put(pathAdminStoreProfile, a.setStoreProfile)
}

// itemEnvelope is the single-record envelope (plan Section 8).
type itemEnvelope struct {
	Data any `json:"data"`
}

// getStoreProfile answers who the shop is.
//
// It answers 404 while no profile has been written, and that is the useful
// answer: a client setting up a shop needs to know the difference between "not
// configured yet" and "configured as nothing".
func (a *API) getStoreProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	profile, err := a.svc.GetProfile(ctx)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, itemEnvelope{Data: toProfileDTO(profile)})
}

// setStoreProfile replaces the shop's identity.
//
// PUT and not PATCH: the record is an identity and a partial write would let a
// shop end up with a legal name from one edit and a tax number from another. The
// first write creates it, so the same verb serves setup and correction.
func (a *API) setStoreProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body storeProfileRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	profile, err := a.svc.SetProfile(ctx, service.SetProfileInput{
		LegalName:   body.LegalName,
		TaxNumber:   body.TaxNumber,
		TaxOffice:   body.TaxOffice,
		Email:       body.Email,
		Address:     body.Address,
		CountryCode: body.CountryCode,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, itemEnvelope{Data: toProfileDTO(profile)})
}

// decodeBody reads the request body into the target.
//
// Unknown fields are REFUSED: a client that sent "legalname" would otherwise
// have its value silently dropped and would find out on the printed document. A
// second JSON document after the first is refused for the same reason.
func decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "the request body cannot be empty")
		}

		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body could not be parsed")
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"the request body has to be a single JSON document")
	}

	return nil
}
