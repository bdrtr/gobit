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
			"422": openapi.ErrorResponse(
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

	d.Describe(http.MethodGet, "/store/v1/auth/passkey/keys", openapi.Operation{
		Summary: "The CALLER's own passkeys.",
		Description: "Takes no parameters and names nobody: the account is whoever this " +
			"installation's identity proves, and a listing that accepted a customer id " +
			"would be a listing of anybody's devices.\n\n" +
			"Each key carries id (base64url, the only thing a removal can name), " +
			"created_at, last_used_at, transports, removable and — only when removable " +
			"is false — not_removable_reason.\n\n" +
			"removable is ADVISORY. It is what the rule says at the moment of this " +
			"listing; the removal decides again under a lock, so a client that disables " +
			"a button on it is right nearly always and the DELETE is what is " +
			"authoritative.\n\n" +
			"last_used_at is the last sign-in this module MANAGED TO RECORD, not the " +
			"last sign-in. A failed stamp is deliberately swallowed — refusing somebody " +
			"a session over a timestamp would trade an account for a record — so this " +
			"value can lag reality.\n\n" +
			"What it does NOT carry is deliberate: no public key, no AAGUID, no sign " +
			"counter, no attestation, and no backup/synced flag. The flags are frozen at " +
			"registration and a 'synced' label read from them would be wrong in the " +
			"direction that locks somebody out; the rest are the person's hardware, not " +
			"their account.\n\n" +
			"An empty list is an empty ARRAY under data, never null and never a 404.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"200": openapi.Response("The caller's keys", nil),
			"401": openapi.ErrorResponse(
				"The request proves no customer. Code \"identity_passkey_not_signed_in\"."),
			"500": openapi.ErrorResponse(
				"The keys could not be read, or whether the account has another way in " +
					"could not be checked. Code \"identity_passkey_unavailable\"; the listing " +
					"is never answered with removable omitted, because a client would have " +
					"to guess and the guess that matters offers a button that ends an " +
					"account."),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/auth/passkey/keys/{credential_id}", openapi.Operation{
		Summary: "Removes one of the CALLER's own passkeys.",
		Description: "The identifier is the id from the listing. A removal succeeds only " +
			"when at least one way into the account survives it: another passkey, or a " +
			"way in that is not a passkey at all — which this module asks the " +
			"installation about and never derives, because what counts as a way in " +
			"depends on what the installation bound.\n\n" +
			"The count is of KEYS and not of devices. Nothing stops one authenticator " +
			"from holding two credentials for this site, so two keys are not proof of " +
			"two devices, and the refusal below says ANOTHER DEVICE for that reason.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The passkey was removed", nil),
			"401": openapi.ErrorResponse(
				"The request proves no customer. Code \"identity_passkey_not_signed_in\"."),
			"404": openapi.ErrorResponse(
				"You have no passkey with that identifier. Code " +
					"\"identity_passkey_no_such_key\", and it is ONE code for a key that " +
					"never existed, one already removed, one belonging to somebody else " +
					"and an identifier that is not even base64url. Telling them apart " +
					"would answer, for any id a caller cares to try, whether it belongs " +
					"to somebody; the caller's own ids are in the listing, so nothing is " +
					"hidden from them that they could have asked for."),
			"409": openapi.ErrorResponse(
				"That passkey is the only way into the account. Code " +
					"\"identity_passkey_last_way_in\"; details carry " +
					"credentials_remaining. Register a passkey on ANOTHER device first, or " +
					"ask the shop to set a password."),
			"500": openapi.ErrorResponse(
				"The removal failed, or whether the account has another way in could not " +
					"be checked. Code \"identity_passkey_unavailable\". The message " +
					"distinguishes the two: 'you have no other way in' is an answer and " +
					"'we could not check' is a question, and nothing is removed on the " +
					"strength of a failed query."),
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
			"422": openapi.ErrorResponse(
				"No ceremony is in progress. Code \"identity_passkey_ceremony_missing\"."),
			"401": openapi.ErrorResponse(
				"The ceremony was refused. Code \"identity_passkey_refused\"."),
		},
	})
}
