package identitypasskey

import (
	"github.com/go-webauthn/webauthn/webauthn"
)

// passkeyUser is what the library is told about a customer.
//
// # Why it carries so little
//
// The library wants a handle, a name, a display name and the customer's
// credentials. This module knows the handle and the credentials and knows
// NOTHING else: the customer's name and e-mail belong to gobit's customer
// module, which this one does not import and has no business reading to sign
// somebody in.
//
// What the two name fields are for is the authenticator's own picker — the list
// a person sees when their device asks which key to use. A module that guessed
// at a name would put a guess on somebody's phone; an installation that wants
// real names there sets [Options.DisplayName] and gets its own sentence.
type passkeyUser struct {
	customerID  string
	displayName string
	credentials []webauthn.Credential
}

// WebAuthnID is the user handle, and it is the CUSTOMER IDENTIFIER.
//
// # Why the identifier and not a random handle
//
// The specification recommends a random 64-byte handle, and the reason it gives
// is that the handle must not be a personal identifier a relying party would
// otherwise display. A gobit customer id is neither: it is an opaque token this
// framework minted, it is not derived from anything about the person, and it
// already travels in every order response.
//
// What it buys is that a discoverable sign-in — where the authenticator names
// the handle and the relying party knows nothing else — resolves the customer
// without a second table mapping handles to customers. A random handle would
// need that table and it would be the only thing in it.
func (u passkeyUser) WebAuthnID() []byte { return []byte(u.customerID) }

// WebAuthnName is what the authenticator's picker shows.
func (u passkeyUser) WebAuthnName() string { return u.displayName }

// WebAuthnDisplayName is the same string.
//
// The specification separates a NAME from a DISPLAY NAME so a picker can show
// "ada@example.test" beside "Ada Lovelace". This module knows neither, so
// answering two different guesses would be worse than answering one honest
// value twice.
func (u passkeyUser) WebAuthnDisplayName() string { return u.displayName }

// WebAuthnCredentials is every key this customer registered.
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// The library's contract is satisfied at compile time.
var _ webauthn.User = passkeyUser{}
