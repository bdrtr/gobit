package adminui

import (
	"context"
	"html/template"
	"net/http"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The person's own second factor, in the panel (ADR 0266).
//
// It is the one screen every signed-in person can open: enrolling is an act of
// identity rather than of privilege (ADR 0264), and a person the installation
// requires a factor of holds no privilege until they enroll (ADR 0265), so this
// is the screen the panel's door sends them to.

// ServiceSecondFactor is the container name of the auth module's surface for a
// person's own second factor. It is spelled by hand, as the other surfaces'
// names are, and pinned against the module's constant in internal/arch.
const ServiceSecondFactor = "auth.admin"

// The second factor's screen and its three forms.
const (
	// SecondFactorPath is the screen.
	SecondFactorPath = URLPrefix + "/second-factor"
	// SecondFactorEnrollPath draws a new secret.
	SecondFactorEnrollPath = SecondFactorPath + "/enroll"
	// SecondFactorConfirmPath proves the waiting secret.
	SecondFactorConfirmPath = SecondFactorPath + "/confirm"
	// SecondFactorRemovePath takes the factor off.
	SecondFactorRemovePath = SecondFactorPath + "/remove"
)

// secondFactorLabel is the screen's name in the menu.
const secondFactorLabel = "Second factor"

// CodeMFALocked and CodeMFAUnavailable are the refusals the screen words for
// itself, beside [CodeMFARequired] and [CodeMFACodeWrong]; they are literals
// for the reason those are, and pinned the same way.
const (
	CodeMFALocked      = "auth_mfa_locked"
	CodeMFAUnavailable = "auth_mfa_unavailable"
)

// SecondFactorAdmin is a person's own second factor, in primitives.
type SecondFactorAdmin interface {
	// SecondFactorStatus reports whether the person has proven a factor,
	// whether an enrolment waits to be proven, and whether the installation
	// requires one they have not proven.
	SecondFactorStatus(ctx context.Context, userID string) (proven, waiting, owed bool, err error)
	// EnrollSecondFactor draws a secret and returns it with its otpauth link,
	// once; replacing a proven factor takes its current code.
	EnrollSecondFactor(ctx context.Context, userID, currentCode string) (secret, uri string, err error)
	// ConfirmSecondFactor proves the waiting enrolment.
	ConfirmSecondFactor(ctx context.Context, userID, code string) error
	// RemoveSecondFactor takes the factor off, given its current code.
	RemoveSecondFactor(ctx context.Context, userID, code string) error
}

// otpauthScheme is the only scheme the enrolment link may carry; anything else
// is printed as text rather than made a link.
const otpauthScheme = "otpauth://"

// showSecondFactor draws the screen for the signed-in person.
func (u *UI) showSecondFactor(w http.ResponseWriter, r *http.Request) {
	u.renderSecondFactor(w, r, http.StatusOK, nil, "")
}

// renderSecondFactor draws the screen, with an enrolment to show once when one
// was just drawn and a sentence when an act was refused.
func (u *UI) renderSecondFactor(w http.ResponseWriter, r *http.Request, status int, enrollment map[string]any, message string) {
	userID, ok := u.secondFactorPerson(w, r)
	if !ok {
		return
	}

	proven, waiting, owed, err := u.secondFactor.SecondFactorStatus(r.Context(), userID)
	if err != nil {
		u.unexpectedFailure(w, r, err, "The second factor could not be read")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	u.templates.render(w, r, status, "second_factor.gohtml", map[string]any{
		titleKey:       secondFactorLabel,
		errorKey:       message,
		"Proven":       proven,
		"Waiting":      waiting,
		"Owed":         owed,
		"Unprivileged": len(principal.Scopes) == 0,
		"Enrollment":   enrollment,
		"EnrollPath":   SecondFactorEnrollPath,
		"ConfirmPath":  SecondFactorConfirmPath,
		"RemovePath":   SecondFactorRemovePath,
	})
}

// submitSecondFactorEnroll draws a new secret and shows it once.
func (u *UI) submitSecondFactorEnroll(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := u.secondFactorForm(w, r)
	if !ok {
		return
	}

	secret, uri, err := u.secondFactor.EnrollSecondFactor(r.Context(), userID, code)
	if err != nil {
		u.afterSecondFactor(w, r, err, "The second factor could not be enrolled")
		return
	}

	enrollment := map[string]any{"Secret": secret, "URI": uri}
	if strings.HasPrefix(uri, otpauthScheme) {
		// html/template refuses a scheme it does not know and would print
		// "#ZgotmplZ"; this one is built by the auth module from a base32
		// secret and is checked for its scheme above.
		enrollment["Link"] = template.URL(uri) //nolint:gosec // G203: the scheme is checked to be otpauth
	}
	u.renderSecondFactor(w, r, http.StatusOK, enrollment, "")
}

// submitSecondFactorConfirm proves the waiting secret.
func (u *UI) submitSecondFactorConfirm(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := u.secondFactorForm(w, r)
	if !ok {
		return
	}

	if err := u.secondFactor.ConfirmSecondFactor(r.Context(), userID, code); err != nil {
		u.afterSecondFactor(w, r, err, "The second factor could not be confirmed")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, SecondFactorPath)
}

// submitSecondFactorRemove takes the factor off.
func (u *UI) submitSecondFactorRemove(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := u.secondFactorForm(w, r)
	if !ok {
		return
	}

	if err := u.secondFactor.RemoveSecondFactor(r.Context(), userID, code); err != nil {
		u.afterSecondFactor(w, r, err, "The second factor could not be removed")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, SecondFactorPath)
}

// secondFactorPerson is the person the session proved, or a page saying why
// there is none: a surface this installation lacks, or a caller that is not a
// person.
func (u *UI) secondFactorPerson(w http.ResponseWriter, r *http.Request) (string, bool) {
	if u.secondFactor == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Second factor unavailable",
			"The identity module's second factor surface is not registered in this installation.")
		return "", false
	}

	principal, ok := corehttp.PrincipalFromContext(r.Context())
	if !ok || principal.ID == "" {
		u.errorPage(w, r, http.StatusUnauthorized, "Not signed in", "Sign in to manage your second factor.")
		return "", false
	}

	return principal.ID, true
}

// secondFactorForm reads the person and the code a form carries.
func (u *UI) secondFactorForm(w http.ResponseWriter, r *http.Request) (userID, code string, ok bool) {
	if userID, ok = u.secondFactorPerson(w, r); !ok {
		return "", "", false
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return "", "", false
	}

	return userID, strings.TrimSpace(r.PostFormValue("code")), true
}

// afterSecondFactor draws the screen again with the sentence a refusal
// deserves, or the error page for anything a person cannot act on.
func (u *UI) afterSecondFactor(w http.ResponseWriter, r *http.Request, err error, title string) {
	switch errors.CodeOf(err) {
	case CodeMFARequired:
		u.renderSecondFactor(w, r, http.StatusForbidden, nil,
			"Enter the six digits your current authenticator shows to change it.")
	case CodeMFACodeWrong:
		u.renderSecondFactor(w, r, http.StatusForbidden, nil,
			"That code is not the one the authenticator shows now.")
	case CodeMFALocked:
		u.renderSecondFactor(w, r, http.StatusForbidden, nil,
			"Too many wrong codes have locked this account. Wait and try again.")
	case CodeMFAUnavailable:
		u.renderSecondFactor(w, r, http.StatusServiceUnavailable, nil,
			"This installation cannot store a second factor: MFA_SECRET_KEY is not set.")
	default:
		if errors.IsInvalid(err) || errors.IsConflict(err) {
			u.renderSecondFactor(w, r, http.StatusUnprocessableEntity, nil, messageFor(err))
			return
		}
		u.unexpectedFailure(w, r, err, title)
	}
}
