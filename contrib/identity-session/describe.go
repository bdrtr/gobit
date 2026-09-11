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
			"400": openapi.ErrorResponse(
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

	d.Describe(http.MethodPut, "/admin/v1/customer-credentials", openapi.Operation{
		Summary: "Writes or replaces a customer's credential.",
		Description: "Takes customer_id, email and password, and stores the password " +
			"as an argon2id hash. It REPLACES whatever that customer had, so it is both " +
			"the way an account is created and the way a password is reset.\n\n" +
			"It is on the admin prefix and behind the operator authentication, because " +
			"this module ships no storefront self-registration: that flow needs e-mail " +
			"verification, a rate limit and a decision about who may create a customer, " +
			"and none of those is a session's business.\n\n" +
			"The customer is not checked for existence. This module owns credentials " +
			"and gobit's customer records belong to another module that it does not " +
			"import; what a credential for a customer who does not exist buys is a " +
			"session naming nobody, which every storefront route then refuses.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The credential was written", nil),
			"400": openapi.ErrorResponse(
				"customer_id or email is missing, the password is empty, or the body " +
					"could not be parsed. Code \"identity_session_invalid\"."),
			"409": openapi.ErrorResponse(
				"The e-mail address already belongs to ANOTHER customer. Code " +
					"\"identity_session_not_written\"; one address is one account."),
		},
	})
}
