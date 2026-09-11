package api

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// CodeMFANotAPerson is a second factor asked for by something that has no phone.
//
// The admin surface is reachable with a session token and with an API KEY, and
// the second is a machine: it holds no authenticator and nobody could scan a code
// for it. Letting a key enroll would write a secret whose only reader is the
// database, which is a credential that weakens the account and protects nothing.
const CodeMFANotAPerson = "auth_mfa_not_a_person"

// mfaEnrollmentDTO is what an enrollment answers with.
//
// It carries the secret, ONCE, and that is not a leak but the point: an
// authenticator app cannot be given a secret without being shown one. What makes
// it safe to write here and nowhere else is that the request is about the caller
// themselves and the value is never readable again — asking twice draws a new
// secret rather than repeating the old one.
type mfaEnrollmentDTO struct {
	// Secret is the base32 value, for somebody typing it by hand.
	Secret string `json:"secret"`
	// OtpauthURI is the same secret as a link, for a QR code.
	OtpauthURI string `json:"otpauth_uri"`
}

// confirmMFARequest is the body that proves the app holds the secret.
type confirmMFARequest struct {
	// Code is the six digits the authenticator shows right now.
	Code string `json:"code"`
}

// adminEnrolMFA draws a second factor for the CALLER
// (POST /admin/v1/auth/mfa).
//
// # Why it names no user
//
// A second factor is a thing a person has. An endpoint that took a user id would
// let an administrator enroll one for a colleague and walk away holding the secret
// of their phone — which is the whole of what the factor is worth. The same
// reasoning keeps an invitation token out of the response its issuer reads
// (ADR 0137), and the passkey listing out of anybody else's hands (ADR 0130).
//
// So it acts on whoever the request PROVED, and a request that proved a machine
// is refused.
func (h *Handler) adminEnrolMFA(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := h.personOrRefuse(w, r)
	if !ok {
		return
	}

	enrollment, err := h.svc.EnrolMFA(ctx, userID, h.mfaIssuer)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, itemEnvelope{Data: mfaEnrollmentDTO{
		Secret:     enrollment.Secret,
		OtpauthURI: enrollment.URI,
	}})
}

// adminConfirmMFA proves the enrollment
// (POST /admin/v1/auth/mfa/confirm).
//
// 204 and nothing else: there is nothing to say back that the caller does not
// already know, and a body would invite a client to read a state out of it that
// the next request could contradict.
func (h *Handler) adminConfirmMFA(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := h.personOrRefuse(w, r)
	if !ok {
		return
	}

	var body confirmMFARequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if err := h.svc.ConfirmMFA(ctx, userID, body.Code); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// personOrRefuse answers the id of the PERSON who made the request.
//
// It refuses two shapes and they are different faults. No principal at all means
// the guard rings are wired wrongly — the ring above already turned away an
// unauthenticated caller — so it is a 500. A principal that is not a user is an
// API key, which is a caller doing something that cannot work rather than a
// broken installation, so it is a 422 that says why.
func (h *Handler) personOrRefuse(w http.ResponseWriter, r *http.Request) (string, bool) {
	ctx := r.Context()

	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok || principal.ID == "" {
		corehttp.WriteError(ctx, w, coreerrors.Internal(CodeInviterUnknown,
			"the caller could not be identified, so there is nobody to act for"))

		return "", false
	}
	if principal.Kind != service.PrincipalKindUser {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(CodeMFANotAPerson,
			"a second factor belongs to a person and this request was made with an API "+
				"key; sign in as the user who will hold the authenticator"))

		return "", false
	}

	return principal.ID, true
}
