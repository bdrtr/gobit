package identitypasskey

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// docTag groups this module's endpoints in the document.
const docTag = "Identity"

// Describe writes the four ceremony endpoints into the OpenAPI document.
//
// # Why the bodies are not schemas here
//
// Three of the four carry what the browser's own WebAuthn API produces and
// consumes, verbatim. Writing a schema for those would be this module's copy of
// a specification it does not own, going stale the first time an authenticator
// sends a field the copy has not heard of. What the descriptions say instead is
// which browser call the body belongs to, which is the sentence an integrator
// can act on.
func (m *Module) Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/auth/passkey/register/begin", openapi.Operation{
		Summary: "Starts adding a passkey to the CALLER's account.",
		Description: "Answers the options object to hand to navigator.credentials.create(). " +
			"Takes no body: the account is whoever this installation's identity proves, " +
			"and a customer named in a body would let anybody add a key to anybody's " +
			"account.\n\n" +
			"It also sets a short-lived sealed cookie holding the challenge. The finish " +
			"call needs it, so a client that drops cookies cannot complete a ceremony.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"200": openapi.Response("The creation options for the browser", nil),
			"401": openapi.ErrorResponse(
				"The request proves no customer. Code \"identity_passkey_not_signed_in\"; " +
					"sign in with a password first, or with a passkey already registered."),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/auth/passkey/register/finish", openapi.Operation{
		Summary: "Stores the passkey the authenticator just minted.",
		Description: "Takes the credential object navigator.credentials.create() resolved " +
			"with, and the ceremony cookie the begin call set.\n\n" +
			"The ceremony is bound to the account that BEGAN it: signing in as somebody " +
			"else between the two calls does not move the key.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The passkey was stored", nil),
			"400": openapi.ErrorResponse(
				"No ceremony is in progress: the cookie was not sent, was edited, or the " +
					"two minutes ran out. Code \"identity_passkey_ceremony_missing\"."),
			"401": openapi.ErrorResponse(
				"The request proves no customer, or the ceremony was refused. " +
					"\"identity_passkey_refused\" is ONE code for every reason the " +
					"ceremony can fail — a wrong origin, a bad signature, a challenge " +
					"that does not match — because a caller can act on none of them and " +
					"an attacker can learn from all of them."),
			"403": openapi.ErrorResponse(
				"The ceremony was begun by a different account. Code " +
					"\"identity_passkey_refused\"."),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/auth/passkey/sign-in/begin", openapi.Operation{
		Summary: "Starts a passkey sign-in.",
		Description: "Answers the options object to hand to navigator.credentials.get(). " +
			"Takes no body and names NOBODY: the authenticator asks the person which of " +
			"their keys is for this site.\n\n" +
			"That is not only a nicer flow. A begin that took an e-mail address would " +
			"answer differently for an address with keys and one without, which is the " +
			"account enumerator every other endpoint here refuses to be.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"200": openapi.Response("The assertion options for the browser", nil),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/auth/passkey/sign-in/finish", openapi.Operation{
		Summary: "Checks the passkey and issues the session cookie.",
		Description: "Takes the credential object navigator.credentials.get() resolved " +
			"with, and the ceremony cookie the begin call set. On success it sets the " +
			"SAME session cookie a password sign-in sets: a session is a session however " +
			"the person proved they own it.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The session cookie was set", nil),
			"400": openapi.ErrorResponse(
				"No ceremony is in progress. Code \"identity_passkey_ceremony_missing\"."),
			"401": openapi.ErrorResponse(
				"The ceremony was refused. Code \"identity_passkey_refused\"."),
		},
	})
}
