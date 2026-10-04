package identitypasskey

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// docTag groups this module's endpoints in the document.
const docTag = "Identity"

// Describe writes the four ceremony endpoints, and the two over a person's keys
// (ADR 0130), into the OpenAPI document.
//
// # Why the bodies are not schemas here
//
// Three of the four ceremony bodies carry what the browser's own WebAuthn API
// produces and consumes, verbatim. Writing a schema for those would be this
// module's copy of a specification it does not own, going stale the first time
// an authenticator sends a field the copy has not heard of. What the
// descriptions say instead is which browser call the body belongs to, which is
// the sentence an integrator can act on.
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
			"created_at, last_used_at, suspended_at, transports, removable and — only " +
			"when removable is false — not_removable_reason: " +
			"\"identity_passkey_last_way_in\", or \"identity_passkey_unavailable\" when " +
			"whether the account has another way in could not be checked.\n\n" +
			"removable is ADVISORY. It is what the rule says at the moment of this " +
			"listing; the removal decides again under a lock, so a client that disables " +
			"a button on it is right nearly always and the DELETE is what is " +
			"authoritative.\n\n" +
			"last_used_at is the last sign-in: a sign-in whose record cannot be written " +
			"is refused, so it does not lag.\n\n" +
			"suspended_at is when the key signed with a signature counter that did not " +
			"advance, which is what a copied key does, and null for a key that signs " +
			"in. A suspended key signs nobody in, is always removable, and is not " +
			"counted as a way into the account.\n\n" +
			"What it does NOT carry is deliberate: no public key, no AAGUID, no sign " +
			"counter, no attestation, and no backup/synced flag. The backup flag is the " +
			"one the key's last sign-in reported, and a 'synced' label read from it says " +
			"nothing about the next; the rest are the person's hardware, not their " +
			"account.\n\n" +
			"An empty list is an empty ARRAY under data, never null and never a 404.\n\n" +
			"Keys registered under a DIFFERENT relying party id are not listed. A passkey " +
			"is bound to that id by the authenticator that minted it, so an installation " +
			"that changed it left every earlier key unusable — they are not shown, not " +
			"counted as a way into the account, and not accepted at sign-in. Everybody " +
			"holding one registers again.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"200": openapi.Response("The caller's keys", nil),
			"401": openapi.ErrorResponse(
				"The request proves no customer. Code \"identity_passkey_not_signed_in\"."),
			"500": openapi.ErrorResponse(
				"The keys could not be read, or the account holds one key, it signs in, " +
					"and whether the account has another way in could not be checked. Code " +
					"\"identity_passkey_unavailable\"; the listing is never answered with " +
					"removable omitted, because a client would have to guess and the guess " +
					"that matters offers a button that ends an account. An account that also " +
					"holds a suspended key is listed instead, with the key that signs in not " +
					"removable, because removing the suspended one needs its id."),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/auth/passkey/keys/{credential_id}", openapi.Operation{
		Summary: "Removes one of the CALLER's own passkeys.",
		Description: "The identifier is the id from the listing. A suspended key is " +
			"always removed: it signs nobody in. Any other key is removed only when at " +
			"least one way into the account survives it: another passkey that is not " +
			"suspended, or a way in that is not a passkey at all — which this module " +
			"asks the installation about and never derives, because what counts as a " +
			"way in depends on what the installation bound.\n\n" +
			"The count is of KEYS that sign in and not of devices. Nothing stops one " +
			"authenticator from holding two credentials for this site, so two keys are " +
			"not proof of two devices, and the refusal below says ANOTHER DEVICE for " +
			"that reason.",
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
			"the person proved they own it.\n\n" +
			"The sign-in is recorded before the session is issued. A key that counts its " +
			"signatures and cannot be synced has to present a higher count than the one " +
			"recorded. The same count again within four minutes of its recording is " +
			"refused and suspends nothing, because a finish sent twice looks like that, " +
			"and so does a copy that lands on that count in those minutes; any other " +
			"count that did not advance suspends the key. A key that can be synced, and " +
			"one that always reports zero, is not refused over its count, so a finish of " +
			"theirs sent twice within the ceremony's two minutes signs in twice.",
		Tags: []string{docTag},
		Responses: map[string]any{
			"204": openapi.Response("The session cookie was set", nil),
			"422": openapi.ErrorResponse(
				"No ceremony is in progress. Code \"identity_passkey_ceremony_missing\"."),
			"401": openapi.ErrorResponse(
				"The ceremony was refused, a counting key's finish sent twice among them. Code " +
					"\"identity_passkey_refused\"."),
			"403": openapi.ErrorResponse(
				"The key signed with a signature counter that did not advance, which is " +
					"what a copied key does, so it signs nobody in until it is removed. " +
					"Code \"identity_passkey_key_suspended\"."),
			"500": openapi.ErrorResponse(
				"The sign-in could not be recorded, so no session was issued. Code " +
					"\"identity_passkey_unavailable\"."),
		},
	})
}
