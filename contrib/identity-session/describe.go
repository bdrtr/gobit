package identitysession

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// docTag groups this module's endpoints in the document.
//
// One tag for all of them, and it is the CONCERN rather than the module: an
// integrator reading the document is looking for "how do I sign somebody in",
// not for which Go module answers it.
const docTag = "Identity"

// Describe writes this module's endpoints into the OpenAPI document.
//
// # Why a module outside gobit describes its own routes
//
// The document an installation publishes is assembled from every module that
// implements [openapi.Describer], and a module that does not is a hole in it:
// endpoints an integrator can call and cannot find. The core reports the
// hole through openapi.Doc.UndescribedRoutes, which is what an embedder who
// skipped this method sees; the reverse — a description matching no route — is
// openapi.Doc.UnmatchedDescriptions, and it is why the paths below have to be
// spelled exactly as [Module.Routes] binds them.
//
// That this method compiles here at all is ADR 0035's point rather than a
// decoration: the schema vocabulary is published, so a module in a separate Go
// module can describe itself.
func (m *Module) Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/auth/sign-in", openapi.Operation{
		Summary:     "Signs a customer in and sets the session cookie.",
		RequestBody: d.RequestBody(signInRequest{}),
		Description: "Takes an e-mail address and a password. On success it sets an " +
			"HttpOnly, SameSite=Lax session cookie and answers 204 with NO BODY: what " +
			"the caller needs is the cookie, and an echoed identifier would land in " +
			"every browser history that logs a URL. The customer the cookie proves is " +
			"read from GET /store/v1/auth/session.\n\n" +
			"The address is compared case-insensitively and with surrounding space " +
			"trimmed, so one person has one account however they type it.\n\n" +
			"The session cookie carries the customer, its expiry and the moment it was " +
			"issued under a MAC, and no record of it is kept server-side, so one session " +
			"cannot be ended alone. Where the credential store keeps the moment a " +
			"customer's sessions count from, a password replaced and " +
			"POST /store/v1/auth/sessions/revoke-others move it, and every cookie issued " +
			"before it proves nobody (ADR 0374).",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The session cookie was set", nil),
			"422": openapi.ErrorResponse(
				"The body could not be parsed, or it carried a field this endpoint does " +
					"not know. Code \"identity_session_invalid\"."),
			"401": openapi.ErrorResponse(
				"The e-mail address and the password do not match. Code " +
					"\"identity_session_rejected\", and it is the SAME answer for an " +
					"address that has no account, a wrong password and a stored hash this " +
					"module cannot read. Telling them apart would let a caller enumerate " +
					"which addresses have accounts, one request at a time, without ever " +
					"signing in."),
			"500": openapi.ErrorResponse(
				"The credential store could not be reached. Code " +
					"\"identity_session_unavailable\"."),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/auth/sign-out", openapi.Operation{
		Summary: "Ends the session.",
		Description: "Overwrites the session cookie with an expired empty one rather " +
			"than only asking the browser to drop it: a client that ignores MaxAge " +
			"still sends what it holds, and what it holds after this proves nobody.\n\n" +
			"It answers 204 whether a session was there or not. Reporting \"you were " +
			"not signed in\" would tell an unauthenticated caller something about the " +
			"cookie they sent.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The session cookie was cleared", nil),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/auth/session", openapi.Operation{
		Summary: "Says which customer the session cookie proves, and until when.",
		Description: "It is how a storefront that signed a shopper in learns the customer " +
			"id the customer routes take in their path: signing in and finishing a " +
			"registration answer with the cookie alone (ADR 0366). The id is in the body " +
			"and never in a URL, and the answer carries Cache-Control: no-store, because " +
			"it names a person.\n\n" +
			"expires_at is when the session ends unless it is ended sooner: by a " +
			"password replaced or by the customer ending their other sessions (ADR 0374).",
		Tags: []string{docTag},
		Responses: map[string]any{
			"200": openapi.Response("The customer the cookie proves", d.Item(sessionAnswer{})),
			"401": openapi.ErrorResponse(
				"The request proves nobody. Code \"identity_session_none\", and it is the " +
					"SAME answer for no cookie, an edited one, one signed with a key this " +
					"installation does not hold, an expired one and one issued before the " +
					"customer's sessions were ended: telling them apart would tell a forger " +
					"which half of a forgery worked."),
			"500": openapi.ErrorResponse(
				"Whether the session still counts could not be read. Code " +
					"\"identity_session_unavailable\"; it is a failure, not a refusal."),
		},
	})

	if m.passwordChangeMounted() {
		d.Describe(http.MethodPost, "/store/v1/auth/password", openapi.Operation{
			Summary:     "Changes the signed-in customer's password, given the current one.",
			RequestBody: d.RequestBody(passwordChange{}),
			Description: "Takes current_password and new_password. The current password is " +
				"asked for because a session is not the person: a cookie left signed in on a " +
				"shared computer would otherwise be enough to lock its owner out (ADR 0375).\n\n" +
				"Every session of the customer issued before the change ends, where the store " +
				"keeps the moment sessions count from, and this browser gets a new session " +
				"cookie (ADR 0374).",
			Tags: []string{docTag},
			Responses: map[string]any{
				"204": openapi.Response("The password is replaced and this session is renewed", nil),
				"401": openapi.ErrorResponse(
					"The request proves nobody. Code \"identity_session_none\"."),
				"403": openapi.ErrorResponse(
					"The current password does not match. Code " +
						"\"identity_session_current_password_wrong\"."),
				"409": openapi.ErrorResponse(
					"The customer signs in some other way than a password kept here. Code " +
						"\"identity_session_no_password_here\"."),
				"422": openapi.ErrorResponse(
					"The new password is one the hash refuses, code " +
						"\"identity_session_password_change_invalid\", or the body could not be " +
						"parsed, code \"identity_session_invalid\"."),
				"500": openapi.ErrorResponse(
					"The credential could not be read or written, or the earlier sessions " +
						"could not be ended. Code \"identity_session_unavailable\"."),
			},
		})
	}

	// Described only where it is mounted: a store that keeps no anchor cannot
	// end anybody's sessions.
	if m.sessionAnchors() != nil {
		d.Describe(http.MethodPost, "/store/v1/auth/sessions/revoke-others", openapi.Operation{
			Summary: "Ends every session of the signed-in customer but this one.",
			Description: "Moves the moment the customer's sessions count from to now, so " +
				"every cookie issued before it proves nobody, and answers with a new " +
				"session cookie for this browser (ADR 0374). It is how a shopper signs out " +
				"a phone they lost or a computer they left signed in.\n\n" +
				"It ends them all or none: a session has no record of its own, so one " +
				"browser cannot be named. A replaced password ends them the same way.",
			Tags: []string{docTag},
			Responses: map[string]any{
				"204": openapi.Response("The other sessions are ended and this one is renewed", nil),
				"401": openapi.ErrorResponse(
					"The request proves nobody. Code \"identity_session_none\"."),
				"409": openapi.ErrorResponse(
					"The customer signs in some other way than a password kept here, so " +
						"there is no moment of theirs to move. Code " +
						"\"identity_session_sessions_not_ended\"."),
				"500": openapi.ErrorResponse(
					"The sessions could not be ended. Code \"identity_session_unavailable\"."),
			},
		})
	}

	// The two registration endpoints are described only when they are MOUNTED.
	//
	// Describing them unconditionally was caught by the description loop the
	// moment it was written, and the loop is right: a description matching no
	// route promises an endpoint that does not exist, which is gap D62's defect
	// with the direction reversed. An integrator reading the document of an
	// installation that binds no Accounts would code against a 404.
	if m.selfRegistrationMounted() {
		m.describeSelfRegistration(d)
	}
	if m.passwordResetMounted() {
		m.describePasswordReset(d)
	}
	if m.addressChangeMounted() {
		m.describeAddressChange(d)
	}

	d.Describe(http.MethodPut, CredentialPath, openapi.Operation{
		Summary:     "Writes or replaces a customer's credential.",
		RequestBody: d.RequestBody(credentialRequest{}),
		Description: "Takes the customer in the path and an email and a password in the body, " +
			"and stores the password " +
			"as an argon2id hash. It REPLACES whatever that customer had, so it is both " +
			"the way an account is created and the way a password is reset. Replacing " +
			"one ends every session of that customer issued before it, where the store " +
			"keeps the moment sessions count from (ADR 0374): an operator replaces a " +
			"password because somebody else may know it.\n\n" +
			"It is on the admin prefix, behind the operator authentication, and demands " +
			"customer-credential:write, a privilege of its own: it writes a credential for " +
			"ANY customer the path names, and whoever writes one can sign in as that " +
			"customer, read what they read and act as them, while the customer is told " +
			"nothing (ADR 0434). The customer is in the path so that the audit log's row " +
			"names them.\n\n" +
			"Storefront self-registration is a DIFFERENT pair of endpoints and is mounted " +
			"only when the installation binds somebody to open an account and somebody to " +
			"carry a verification message (ADR 0133). Where this endpoint takes an " +
			"operator's word for who somebody is, that flow takes a proven address.\n\n" +
			"The customer is not checked for existence. This module owns credentials " +
			"and gobit's customer records belong to another module that it does not " +
			"import; what a credential for a customer who does not exist buys is a " +
			"session naming nobody, which every storefront route then refuses.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The credential was written", nil),
			"422": openapi.ErrorResponse(
				"The email is missing, the password is empty, or the body " +
					"could not be parsed. Code \"identity_session_invalid\"."),
			"409": openapi.ErrorResponse(
				"The e-mail address already belongs to ANOTHER customer. Code " +
					"\"identity_session_not_written\"; one address is one account."),
			"500": openapi.ErrorResponse(
				"The customer's sessions could not be ended, so the password was not " +
					"replaced. Code \"identity_session_unavailable\"."),
		},
	})
}

// describeSelfRegistration writes the two storefront registration endpoints.
func (m *Module) describeSelfRegistration(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/auth/register", openapi.Operation{
		Summary:     "Starts a self-registration for an e-mail address.",
		RequestBody: d.RequestBody(registerRequest{}),
		Description: "Takes email and password. It answers 202 and creates NOTHING about " +
			"the person: no customer, no credential, no session. What it writes is one row " +
			"of this module's own, holding the address, the password as an argon2id hash, " +
			"and the hash of a token that is sent to the address.\n\n" +
			"It answers the SAME 202 whether that address already has an account or not. " +
			"Anything else would answer, for any address a caller cares to try, whether " +
			"that person shops here. What differs is the message: an address with an " +
			"account is told it has one, so somebody who forgot is not left staring at a " +
			"form that appeared to work.\n\n" +
			"Asking again REPLACES the pending registration, so the newest link is the one " +
			"that works. A link lasts an hour by default.\n\n" +
			"The endpoint is RATE LIMITED per client address, because one request makes " +
			"this shop send mail to an address a stranger chose. The default bound is kept " +
			"in the process's own memory, so an installation behind several instances gets " +
			"that many times the limit until it binds a shared limiter.\n\n" +
			"It is mounted only when the installation has bound somebody to open an " +
			"account and somebody to carry the message; otherwise it does not exist.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"202": openapi.Response("The registration was accepted; watch the address", nil),
			"422": openapi.ErrorResponse(
				"The address is missing or cannot be an address, the password is empty, " +
					"or the body could not be parsed. Code " +
					"\"identity_session_registration_invalid\"."),
			"429": openapi.ErrorResponse(
				"Too many registrations from this client address. No code: the rate limit " +
					"is gobit's own middleware and answers before this module is reached."),
			"500": openapi.ErrorResponse(
				"The registration could not be recorded, or the message could not be " +
					"sent. Code \"identity_session_unavailable\"; nothing was created, and " +
					"the same request can be made again."),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/auth/register/verify", openapi.Operation{
		Summary:     "Proves an address and finishes the registration.",
		RequestBody: d.RequestBody(verifyRequest{}),
		Description: "Takes the token from the message. It opens the customer account if " +
			"the address has none, writes the credential, and signs the person IN — they " +
			"have just proved they control the address, which is the same proof a password " +
			"reset rests on.\n\n" +
			"The token is SINGLE USE and is consumed before the account is opened, in one " +
			"statement. So a failure after that point loses the registration and the " +
			"person starts again; the other order would leave a link in a mailbox that " +
			"could later set somebody's password back to the one they signed up with.\n\n" +
			"One answer for a token that never existed, one already used and one expired. " +
			"Telling them apart would say, for any token somebody tries, whether it was " +
			"ever real.\n\n" +
			"An address that gained an account between the two halves of the flow keeps " +
			"that account rather than getting a second one. A guest checkout is not an " +
			"account: an address that only ever bought as a guest registers like a new one.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The account is open and the session cookie is set", nil),
			"422": openapi.ErrorResponse(
				"The token is not a usable pending registration: unknown, already used or " +
					"expired. Code \"identity_session_registration_not_usable\"."),
			"429": openapi.ErrorResponse(
				"Too many attempts from this client address. No code: the rate limit is " +
					"gobit's own middleware and answers before this module is reached."),
			"500": openapi.ErrorResponse(
				"The registration could not be read, the account could not be opened or " +
					"the credential could not be written. Code " +
					"\"identity_session_unavailable\"; the token is spent either way."),
		},
	})
}

// describePasswordReset writes the two storefront password reset endpoints
// (ADR 0373).
func (m *Module) describePasswordReset(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/auth/password-reset", openapi.Operation{
		Summary:     "Sends a password reset link to the address of an account.",
		RequestBody: d.RequestBody(passwordResetRequest{}),
		Description: "Takes email. It answers 202 whether that address signs in here or " +
			"not, because anything else would answer, for any address a caller cares to " +
			"try, whether that person shops here. An address with a password gets a link; " +
			"one without gets nothing, so the endpoint cannot be used to mail strangers " +
			"from this shop.\n\n" +
			"Asking again REPLACES the pending reset, so the newest link is the one that " +
			"works. A link lasts an hour by default.\n\n" +
			"It shares the registration's rate limit per client, because one request makes " +
			"this shop send mail. It is mounted only when the installation has bound " +
			"somebody to carry the link and a store that keeps pending resets.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"202": openapi.Response("The request was accepted; watch the address", nil),
			"422": openapi.ErrorResponse(
				"The address is missing or cannot be an address, or the body could not be " +
					"parsed. Code \"identity_session_password_reset_invalid\"."),
			"429": openapi.ErrorResponse(
				"Too many requests from this client. No code: the rate limit is gobit's " +
					"own middleware and answers before this module is reached."),
			"500": openapi.ErrorResponse(
				"The reset could not be recorded, or the message could not be sent. Code " +
					"\"identity_session_unavailable\"; the same request can be made again."),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/auth/password-reset/confirm", openapi.Operation{
		Summary:     "Sets a new password from a reset link and signs the person in.",
		RequestBody: d.RequestBody(passwordResetConfirmation{}),
		Description: "Takes the token from the message and the new password. The password " +
			"is checked first, so one the hash refuses does not spend the link; then the " +
			"token is consumed, in one statement, and the credential is replaced. The " +
			"person is signed in: following the link is the proof a reset rests on.\n\n" +
			"One answer for a token that never existed, one already used and one expired.\n\n" +
			"Every session of that customer issued before the reset is ended, where the " +
			"store keeps the moment sessions count from (ADR 0374): a reset is how " +
			"somebody locks out whoever learned the old password.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The password is replaced and the session cookie is set", nil),
			"422": openapi.ErrorResponse(
				"The password is empty, or the token is not a usable pending reset: " +
					"unknown, already used or expired. Codes " +
					"\"identity_session_password_reset_invalid\" and " +
					"\"identity_session_password_reset_not_usable\"."),
			"429": openapi.ErrorResponse(
				"Too many attempts from this client. No code: the rate limit is gobit's " +
					"own middleware and answers before this module is reached."),
			"500": openapi.ErrorResponse(
				"The reset could not be read, the earlier sessions could not be ended or " +
					"the password could not be written. Code \"identity_session_unavailable\"; " +
					"the token is spent either way."),
		},
	})
}

// describeAddressChange writes the two storefront address change endpoints
// (ADR 0377).
func (m *Module) describeAddressChange(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/auth/email", openapi.Operation{
		Summary:     "Sends a link that moves the signed-in customer's account to a new address.",
		RequestBody: d.RequestBody(addressChangeRequest{}),
		Description: "Takes new_email and current_password from a request whose session " +
			"proves a customer. The current password is asked for because a session is " +
			"not the person. The account does not move until the link sent to the new " +
			"address is followed.\n\n" +
			"It answers 202 whether the new address is free or another account's, and " +
			"only a free one is mailed: anything else would tell any account holder which " +
			"addresses have accounts. Asking again REPLACES the pending change; a link " +
			"lasts an hour by default. It shares the registration's rate limit per client.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"202": openapi.Response("The request was accepted; watch the new address", nil),
			"401": openapi.ErrorResponse(
				"The request proves nobody. Code \"identity_session_none\"."),
			"403": openapi.ErrorResponse(
				"The current password does not match. Code " +
					"\"identity_session_current_password_wrong\"."),
			"409": openapi.ErrorResponse(
				"The customer signs in some other way than a password kept here. Code " +
					"\"identity_session_no_password_here\"."),
			"422": openapi.ErrorResponse(
				"The new address cannot be an address or is the one the account has, code " +
					"\"identity_session_address_change_invalid\", or the body could not be " +
					"parsed, code \"identity_session_invalid\"."),
			"429": openapi.ErrorResponse(
				"Too many requests from this client. No code: the rate limit is gobit's " +
					"own middleware and answers before this module is reached."),
			"500": openapi.ErrorResponse(
				"The change could not be recorded or the message could not be sent. Code " +
					"\"identity_session_unavailable\"."),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/auth/email/confirm", openapi.Operation{
		Summary:     "Moves the account to the address a link was sent to.",
		RequestBody: d.RequestBody(addressChangeConfirmation{}),
		Description: "Takes the token from the message. It needs no session: following the " +
			"link is the proof the change waited for, and it may be opened on another " +
			"device. The token is consumed in one statement; the account's record moves, " +
			"then the address its password signs in under. Nobody is signed in or out.\n\n" +
			"One answer for a token that never existed, one already used and one expired.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The account signs in and is mailed at the new address", nil),
			"409": openapi.ErrorResponse(
				"Another account took the address after the link was sent. Code " +
					"\"identity_session_address_taken\"."),
			"422": openapi.ErrorResponse(
				"The token is not a usable pending change: unknown, already used or " +
					"expired. Code \"identity_session_address_change_not_usable\"."),
			"429": openapi.ErrorResponse(
				"Too many attempts from this client. No code: the rate limit is gobit's " +
					"own middleware and answers before this module is reached."),
			"500": openapi.ErrorResponse(
				"The change could not be read or the account could not be moved. Code " +
					"\"identity_session_unavailable\"; the token is spent either way."),
		},
	})
}
