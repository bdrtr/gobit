package identitysession

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// docTag groups this module's endpoints in the document.
//
// One tag for all three, and it is the CONCERN rather than the module: an
// integrator reading the document is looking for "how do I sign somebody in",
// not for which Go module answers it.
const docTag = "Identity"

// Describe writes this module's three endpoints into the OpenAPI document.
//
// # Why a module outside gobit describes its own routes
//
// The document an installation publishes is assembled from every module that
// implements [openapi.Describer], and a module that does not is a hole in it:
// three endpoints an integrator can call and cannot find. The core reports the
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
		Summary: "Signs a customer in and sets the session cookie.",
		Description: "Takes an e-mail address and a password. On success it sets an " +
			"HttpOnly, SameSite=Lax session cookie and answers 204 with NO BODY: what " +
			"the caller needs is the cookie, and an echoed identifier would land in " +
			"every browser history that logs a URL.\n\n" +
			"The address is compared case-insensitively and with surrounding space " +
			"trimmed, so one person has one account however they type it.\n\n" +
			"The session cookie cannot be revoked before it expires. It carries the " +
			"customer and the expiry under a MAC and no record is kept server-side, " +
			"which is what keeps the twelve storefront routes that name a customer off " +
			"a database query per request.",
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

	d.Describe(http.MethodPut, "/admin/v1/customer-credentials", openapi.Operation{
		Summary: "Writes or replaces a customer's credential.",
		Description: "Takes customer_id, email and password, and stores the password " +
			"as an argon2id hash. It REPLACES whatever that customer had, so it is both " +
			"the way an account is created and the way a password is reset.\n\n" +
			"It is on the admin prefix and behind the operator authentication: it writes " +
			"a credential for ANY customer the caller names, which is an operator's " +
			"power and not a shopper's.\n\n" +
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
				"customer_id or email is missing, the password is empty, or the body " +
					"could not be parsed. Code \"identity_session_invalid\"."),
			"409": openapi.ErrorResponse(
				"The e-mail address already belongs to ANOTHER customer. Code " +
					"\"identity_session_not_written\"; one address is one account."),
		},
	})
}

// describeSelfRegistration writes the two storefront registration endpoints.
func (m *Module) describeSelfRegistration(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/auth/register", openapi.Operation{
		Summary: "Starts a self-registration for an e-mail address.",
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
		Summary: "Proves an address and finishes the registration.",
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
			"An address that gained a customer between the two halves of the flow — they " +
			"checked out as a guest in the meantime — keeps that record rather than " +
			"getting a second one.",
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
