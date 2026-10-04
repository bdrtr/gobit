package identitypasskey

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The codes this module answers with. A client branches on these.
const (
	// CodeNotSignedIn is a registration attempted by a caller this installation
	// cannot identify.
	CodeNotSignedIn = "identity_passkey_not_signed_in"
	// CodeCeremonyMissing is a finish with no ceremony behind it: the cookie was
	// not sent, was edited, or the two minutes ran out.
	CodeCeremonyMissing = "identity_passkey_ceremony_missing"
	// CodeCeremonyRefused is a ceremony the library would not accept.
	//
	// It is ONE code for every reason — a wrong origin, a bad signature, an
	// unknown credential, a challenge that does not match — because the caller
	// can act on none of them and an attacker can learn from all of them.
	CodeCeremonyRefused = "identity_passkey_refused"
	// CodeUnavailable is a failure on this module's side.
	CodeUnavailable = "identity_passkey_unavailable"
	// CodeKeySuspended is a key whose signature counter did not advance, which is
	// what a copied key does (ADR 0382). It signs nobody in until it is removed.
	//
	// It is not folded into [CodeCeremonyRefused]: it is answered only after the
	// signature checked out, so only somebody holding the key learns it, and they
	// can act on it — sign in another way and remove the key.
	CodeKeySuspended = "identity_passkey_key_suspended"
)

// errHeldKeyReplaced is a registration that names a key the account holds and
// carries another public key (D229). It is answered as every refused ceremony
// is, with [CodeCeremonyRefused].
var errHeldKeyReplaced = errors.New(
	"identity-passkey: a key the account holds came back with another public key")

// ceremonyCookie carries the library's session data between begin and finish.
//
// # Why a cookie and not a table
//
// The data is one challenge and a few flags, it is useless after two minutes,
// and it belongs to ONE browser — which is exactly what a cookie is. A table
// would add a write and a read to every ceremony and a sweep to every
// installation, to hold something that expires before anybody would notice it.
//
// It is SEALED with the session module's key ([identitysession.Sessions.SealValue]),
// so this module holds no secret of its own and a caller can read the challenge
// but not choose it. Reading it is harmless: the challenge is public by design —
// the authenticator signs it and the server checks that signature.
const ceremonyCookie = "gobit_passkey_ceremony"

// beginRegistration starts adding a passkey to the caller's OWN account.
//
// # Why it asks who the caller is first
//
// A registration that took a customer id from the body would let anybody add a
// key to anybody's account, which is a complete account takeover written as a
// feature. The account is whoever the bound verifier proves, and no body field
// can name a different one.
func (m *Module) beginRegistration(w http.ResponseWriter, r *http.Request) {
	customerID, ok := m.callerOrRefuse(w, r)
	if !ok {
		return
	}

	user, err := m.userOf(r, customerID)
	if err != nil {
		m.unavailable(w, r, "the customer's credentials could not be read", err)

		return
	}

	creation, session, err := m.web.BeginRegistration(user,
		// A DISCOVERABLE credential is required, and it is not a preference:
		// beginSignIn is a discoverable ceremony and this module offers no other.
		// Without this the authenticator may mint a key it does not store a user
		// handle for, registration answers 204, and the person can never sign in
		// with it — the library refuses the assertion with "blank User Handle"
		// (gap D65).
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			// BOTH spellings, and the struct is built by hand rather than through
			// the library's selector for that reason: the selector sets the legacy
			// `requireResidentKey` only, and a browser reading the modern
			// `residentKey` would see no requirement at all. Written out, it was
			// also visible that the selector's first argument is the ATTACHMENT
			// and not the attestation preference — the first version of this call
			// put "none" there, which is not an attachment any browser knows.
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			// The ATTACHMENT is left unconstrained: a passkey on a phone and one
			// on a security key are both passkeys, and naming either would refuse
			// the other.
			UserVerification: protocol.VerificationPreferred,
		}),
		// The keys this person already has, so an authenticator that holds one
		// of them says so instead of quietly minting a SECOND credential on the
		// same device. Every "register another passkey first" sentence in this
		// module means another DEVICE, and without this the person can satisfy
		// it twice on one.
		webauthn.WithExclusions(excluding(user.WebAuthnCredentials())))
	if err != nil {
		m.refuse(w, r, err)

		return
	}

	m.holdCeremony(w, r, session)
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, creation)
}

// finishRegistration stores the key the authenticator just minted.
func (m *Module) finishRegistration(w http.ResponseWriter, r *http.Request) {
	customerID, ok := m.callerOrRefuse(w, r)
	if !ok {
		return
	}

	session, ok := m.takeCeremony(w, r)
	if !ok {
		return
	}

	// The ceremony is bound to the account that BEGAN it. Without this a person
	// could begin as themselves, sign in as somebody else between the two calls,
	// and finish — writing their key onto the second account.
	if string(session.UserID) != customerID {
		corehttp.WriteError(r.Context(), w, coreerrors.Forbidden(CodeCeremonyRefused,
			"this ceremony was begun by a different account"))

		return
	}

	user, err := m.userOf(r, customerID)
	if err != nil {
		m.unavailable(w, r, "the customer's credentials could not be read", err)

		return
	}

	credential, err := m.web.FinishRegistration(user, *session, r)
	if err != nil {
		m.refuse(w, r, err)

		return
	}

	// A key the account holds may come back only as itself, and as itself it
	// writes nothing (D229). The library does not compare the id with the keys
	// it was given, and the store writes a known id over its row, so whatever
	// the client sent would land on the owner's key: another public key, or the
	// same one with flags the owner's authenticator will not match at sign-in.
	// Neither is hard to send — a "none" attestation proves nothing about
	// holding the key, and a public key is no secret — and either would leave
	// the owner locked out while the listing showed the key they know. The
	// same key is a client repeating a finish whose answer it lost, and is
	// answered as the first finish was.
	if held := heldKey(user.WebAuthnCredentials(), credential.ID); held != nil {
		if !bytes.Equal(held.PublicKey, credential.PublicKey) {
			m.refuse(w, r, errHeldKeyReplaced)

			return
		}
		corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)

		return
	}

	switch err := m.store.Put(r.Context(), customerID, *credential); {
	case errors.Is(err, ErrCredentialBelongsToAnother):
		// Another account's key id is a refused ceremony, not a fault here.
		m.refuse(w, r, err)

		return
	case err != nil:
		m.unavailable(w, r, "the credential could not be stored", err)

		return
	}
	m.notifyKeyAdded(r.Context(), customerID, credential.ID)

	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// beginSignIn starts a DISCOVERABLE sign-in.
//
// # Why the caller names nobody
//
// A discoverable ceremony asks the authenticator "which key of yours is for this
// site", and the person picks. Nothing is sent about who they are, so nothing
// here can be probed: a begin that took an e-mail address would answer
// differently for an address with keys and one without, which is the address
// enumerator the session module's sign-in refuses to be.
func (m *Module) beginSignIn(w http.ResponseWriter, r *http.Request) {
	assertion, session, err := m.web.BeginDiscoverableLogin()
	if err != nil {
		m.refuse(w, r, err)

		return
	}

	m.holdCeremony(w, r, session)
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, assertion)
}

// finishSignIn checks the signature, records the sign-in, and only then issues
// the session cookie.
func (m *Module) finishSignIn(w http.ResponseWriter, r *http.Request) {
	session, ok := m.takeCeremony(w, r)
	if !ok {
		return
	}

	// Parsing is split from validating, which are the two calls the library's
	// FinishDiscoverableLogin makes, because the counter this assertion carried
	// is needed: on a count that did not advance the library returns the
	// credential with the STORED count and a clone warning (ADR 0382).
	parsed, err := protocol.ParseCredentialRequestResponse(r)
	if err != nil {
		m.refuse(w, r, err)

		return
	}

	var signedIn string
	user, credential, err := m.web.ValidatePasskeyLogin(
		func(_, userHandle []byte) (webauthn.User, error) {
			// The handle IS the customer id (see [passkeyUser.WebAuthnID]), and
			// the library has already matched the credential the authenticator
			// presented against the ones this returns. Looking the credential up
			// by its own id as well would be a second answer to a question the
			// library is about to answer itself.
			signedIn = string(userHandle)

			return m.userOf(r, signedIn)
		}, *session, parsed)
	if err != nil {
		m.refuse(w, r, err)

		return
	}

	data := parsed.Response.AuthenticatorData
	// The count the library compared with, for the log: on a count that
	// advanced, the credential it returned already carries the new one.
	var storedWhenRead uint32
	if held := heldKey(user.WebAuthnCredentials(), credential.ID); held != nil {
		storedWhenRead = held.Authenticator.SignCount
	}
	err = m.store.SignedIn(r.Context(), Assertion{
		CredentialID: credential.ID,
		PublicKey:    credential.PublicKey,
		SignCount:    data.Counter,
		BackupState:  data.Flags.HasBackupState(),
		UserVerified: data.Flags.HasUserVerified(),
	})
	keyID := encodeCredentialID(credential.ID)
	// ErrKeyCopied wraps ErrKeySuspended, so it is asked first.
	switch {
	case errors.Is(err, ErrKeyCopied):
		m.log.WarnContext(r.Context(),
			"identity-passkey: a key's signature counter did not advance, so the key is suspended",
			"customer_id", signedIn, "credential_id", keyID,
			"presented", data.Counter, "stored_when_read", storedWhenRead)
		m.notifyKeySuspended(r.Context(), signedIn, credential.ID)
		m.suspended(w, r)

		return
	case errors.Is(err, ErrKeySuspended):
		m.log.InfoContext(r.Context(), "identity-passkey: a suspended key tried to sign in",
			"customer_id", signedIn, "credential_id", keyID)
		m.suspended(w, r)

		return
	case errors.Is(err, ErrNoCredential), errors.Is(err, ErrCountRepeated):
		m.refuse(w, r, err)

		return
	case err != nil:
		m.unavailable(w, r, "the sign-in could not be recorded", err)

		return
	}

	// A store that answers nil to a count the library saw not advance has not
	// checked it. A correct one never reaches this: the count it reads under its
	// lock is at least the one the library read. A key that may live on several
	// devices is not compared, here as in the store.
	if credential.Authenticator.CloneWarning && !credential.Flags.BackupEligible {
		m.log.ErrorContext(r.Context(),
			"identity-passkey: the store recorded a count the library saw not advance; the sign-in was refused",
			"customer_id", signedIn, "credential_id", keyID,
			"presented", data.Counter, "stored_when_read", storedWhenRead)
		m.refuse(w, r, ErrKeyCopied)

		return
	}

	// Only now: Issue sets a header, so anything written before it carries no
	// session and anything refused after it would carry a valid one.
	m.sessions.Issue(w, signedIn)
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// callerOrRefuse answers the customer the request proves, or refuses it.
func (m *Module) callerOrRefuse(w http.ResponseWriter, r *http.Request) (string, bool) {
	customerID, err := m.identity.CustomerID(r)
	if err != nil || customerID == "" {
		corehttp.WriteError(r.Context(), w, coreerrors.Unauthorized(CodeNotSignedIn,
			"a passkey is added to an account, so this request has to prove one"))

		return "", false
	}

	return customerID, true
}

// userOf builds what the library is told about a customer.
func (m *Module) userOf(r *http.Request, customerID string) (webauthn.User, error) {
	credentials, err := m.store.ForCustomer(r.Context(), customerID)
	if err != nil {
		return nil, err
	}

	return passkeyUser{
		customerID:  customerID,
		displayName: m.displayName,
		credentials: credentials,
	}, nil
}

// holdCeremony seals the library's session data into a short-lived cookie.
func (m *Module) holdCeremony(w http.ResponseWriter, r *http.Request, session *webauthn.SessionData) {
	raw, err := json.Marshal(session)
	if err != nil {
		m.log.ErrorContext(r.Context(), "identity-passkey: the ceremony could not be sealed",
			"error", err)

		return
	}

	http.SetCookie(w, m.sessions.Cookie(
		ceremonyCookie, m.sessions.SealValue(string(raw), CeremonyTTL), CeremonyTTL))
}

// takeCeremony reads the ceremony back and CLEARS it.
//
// Clearing stops a browser from finishing the same ceremony twice. It cannot
// stop a client that kept its copy, because the ceremony is sealed into the
// cookie rather than recorded here, and the library cannot notice a second use
// of a challenge whose signature is valid; what such a client can finish again
// is bounded by [CeremonyTTL] and, for a registration, by the rule for a key
// the account holds (D229). A sign-in finished again by a device-bound key that
// counts carries the count the first finish recorded and is refused (ADR 0382);
// a key that reports zero, and one that can be synced, can still finish it
// again within [CeremonyTTL], since neither is compared by its count.
func (m *Module) takeCeremony(w http.ResponseWriter, r *http.Request) (*webauthn.SessionData, bool) {
	cookie, err := r.Cookie(ceremonyCookie)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeCeremonyMissing,
			"no ceremony is in progress; begin one first"))

		return nil, false
	}
	http.SetCookie(w, m.sessions.ClearCookie(ceremonyCookie))

	sealed, err := m.sessions.OpenValue(cookie.Value)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeCeremonyMissing,
			"the ceremony has expired or was not begun here"))

		return nil, false
	}

	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(sealed), &session); err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeCeremonyMissing,
			"the ceremony could not be read"))

		return nil, false
	}

	return &session, true
}

// refuse answers a ceremony the library would not accept.
//
// One code and one sentence for every reason. A caller can act on none of them —
// there is nothing a browser does differently for a bad origin than for a bad
// signature — and an attacker learns from all of them.
func (m *Module) refuse(w http.ResponseWriter, r *http.Request, err error) {
	m.log.InfoContext(r.Context(), "identity-passkey: a ceremony was refused", "error", err)
	corehttp.WriteError(r.Context(), w, coreerrors.Unauthorized(CodeCeremonyRefused,
		"the passkey ceremony was refused"))
}

// suspended answers a key that signs nobody in (ADR 0382).
func (m *Module) suspended(w http.ResponseWriter, r *http.Request) {
	corehttp.WriteError(r.Context(), w, coreerrors.Forbidden(CodeKeySuspended,
		"this passkey signs nobody in: its signature counter did not advance, which is "+
			"what a copied key does; sign in another way and remove it"))
}

// unavailable answers a failure on this module's side.
func (m *Module) unavailable(w http.ResponseWriter, r *http.Request, what string, err error) {
	m.log.ErrorContext(r.Context(), "identity-passkey: "+what, "error", err)
	corehttp.WriteError(r.Context(), w, coreerrors.Internal(CodeUnavailable, "%s", what))
}

// heldKey finds the key with the given id among an account's, or nil.
func heldKey(credentials []webauthn.Credential, id []byte) *webauthn.Credential {
	for i := range credentials {
		if bytes.Equal(credentials[i].ID, id) {
			return &credentials[i]
		}
	}

	return nil
}

// excluding turns a person's credentials into the list an authenticator checks
// itself against.
func excluding(credentials []webauthn.Credential) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(credentials))
	for i := range credentials {
		out = append(out, credentials[i].Descriptor())
	}

	return out
}
