// Package identitysession is a working customer identity for gobit.
//
// # What it is
//
// gobit requires a [corehttp.Identity] and issues none (ADR 0008, ADR 0043), and
// since ADR 0125 every storefront route naming a customer refuses until one is
// bound. This module is that one, ready to add:
//
//	shop := gobit.New().Add(identitysession.New(identitysession.Options{
//		Secret: []byte(os.Getenv("SESSION_SECRET")),
//	}))
//
// It brings a signed session cookie, argon2id passwords, a credential table and
// two storefront endpoints. It passes `core/identitytest.Contract`.
//
// # Why it is a SEPARATE Go module
//
// gobit is imported, so a line in its go.mod is a line in every embedder's
// module graph, vulnerability scan and legal review. This package grows towards
// WebAuthn, and the library for that belongs in the graph of whoever asked for
// it — not in the graph of a shop that wanted a product catalog (ADR 0127).
//
// The cost is that gobit's own arch gates do not walk this tree. What holds it
// instead is the compliance suite gobit publishes for exactly this interface.
//
// # What it does NOT do
//
// It does not register customers from the storefront. That flow needs e-mail
// verification, a rate limit and a decision about who may create a customer,
// and none of those is a session's business; credentials are written by an
// operator endpoint here. It holds no server-side session record — a signed
// cookie cannot be revoked before it expires, which is the price of not having a
// table on the read path and is stated where an operator reads it.
//
// The signing key CAN be rotated without logging anybody out: see
// [Options.RetiredSecrets].
package identitysession

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sessions verifies a request against a signed cookie.
//
// It is the type that satisfies [corehttp.Identity], and it holds no store: a
// session cookie carries the customer identifier and its own expiry, so reading
// one costs no query. That is the whole reason the shape is a signed cookie —
// the identity is consulted on twelve storefront routes and a table on that path
// would be a query on every one of them.
type Sessions struct {
	secret []byte
	// retired are keys this verifier still ACCEPTS and never signs with.
	//
	// They are what makes a rotation something other than logging everybody out:
	// the new key signs from the moment it is set, and a cookie carrying the old
	// one keeps working until it expires (ADR 0129).
	retired    [][]byte
	ttl        time.Duration
	cookieName string
	secure     bool
	now        func() time.Time
}

// CustomerID returns the customer the cookie PROVES.
//
// # What it refuses, and why each refusal is separate
//
// A missing cookie is an anonymous caller; a malformed one is a caller who wrote
// their own; a bad signature is a caller who edited one; an expired one is a
// caller whose session ended. All four answer the same error TEXT on purpose —
// telling them apart tells an attacker which half of a forgery worked.
//
// It does not touch the request. The cookie is read from the header and nothing
// else is looked at, which is what keeps the handler's body intact — the mistake
// gobit's own compliance suite refuses.
func (s *Sessions) CustomerID(r *http.Request) (string, error) {
	cookie, err := r.Cookie(s.cookieName)
	if err != nil {
		return "", errNoSession
	}

	customerID, expiry, err := s.open(cookie.Value)
	if err != nil {
		return "", err
	}
	if !s.now().Before(expiry) {
		return "", errNoSession
	}

	return customerID, nil
}

// errNoSession is the single answer every failed read gives.
var errNoSession = errors.New("identity-session: the request carries no valid session")

// Issue writes the session cookie onto the response.
//
// The cookie is HttpOnly and SameSite=Lax, and Secure unless the installation
// said it is running without TLS: a session a script can read is a session an
// injected script can steal, and those three attributes are the whole of what a
// cookie can do about it.
func (s *Sessions) Issue(w http.ResponseWriter, customerID string) {
	http.SetCookie(w, s.Cookie(s.cookieName, s.seal(customerID, s.now().Add(s.ttl)), s.ttl))
}

// Cookie builds a cookie carrying this installation's security attributes.
//
// It exists for a module that needs a cookie of its own with the same
// protections and no second opinion about them — the passkey ceremony's
// short-lived token is the case it was written for (ADR 0128). What it hands
// back is the ATTRIBUTES rather than a second way to issue a session: the value
// is the caller's, and a caller wanting one this key vouches for seals it with
// [Sessions.SealValue] first.
//
// Whether Secure is set is the installation's answer and not the caller's, which
// is the whole reason this is here rather than in every caller.
func (s *Sessions) Cookie(name, value string, ttl time.Duration) *http.Cookie {
	//nolint:gosec // G124 wants Secure as a literal; it is the installation's
	// choice here and defaults to on. See Options.Insecure, which exists because
	// a Secure cookie is not sent over plain HTTP at all and local development
	// on a name that is not localhost would have no session.
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  s.now().Add(ttl),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearCookie builds one that ends a cookie of that name.
//
// It overwrites with an expired EMPTY value rather than only asking the browser
// to drop it, for [Sessions.Clear]'s reason: a client that ignores MaxAge still
// sends what it holds.
func (s *Sessions) ClearCookie(name string) *http.Cookie {
	//nolint:gosec // G124, for [Sessions.Cookie]'s reason: the attributes have to
	// match the cookie being replaced or the browser keeps the old one.
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// Clear ends the session.
//
// It overwrites the cookie with an expired empty one rather than only asking the
// browser to drop it: a client that ignores MaxAge still sends what it holds, and
// what it holds after this proves nobody.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, s.ClearCookie(s.cookieName))
}

// The purposes this installation's keys sign for.
//
// The CURRENT key signs and every key verifies, so a purpose has to be the same
// string across a rotation or a retired key would stop opening what it sealed.
//
// # Why a signed value carries what it is FOR
//
// One key signs more than one kind of value — a session, and a short-lived token
// another module asked for ([Sessions.SealValue]). Without a purpose in the MAC
// they are interchangeable: a token minted for a passkey ceremony, whose payload
// a caller can often influence, would verify as a SESSION and name whatever
// customer it happened to spell. Domain separation is one string and it makes
// that impossible rather than unlikely.
//
// The labels are written out rather than derived from a caller's argument, so
// two purposes cannot collide by one of them containing the other's name.
const (
	purposeSession = "s1"
	purposeValue   = "v1"
)

// seal produces the cookie value: the identifier, the expiry and a MAC over both.
//
// The expiry is INSIDE the MAC and not only in the cookie's own Expires
// attribute, because the attribute is a request to the browser and the value is
// what the server reads. A caller who edits the attribute changes nothing.
func (s *Sessions) seal(customerID string, expiry time.Time) string {
	payload := customerID + "." + strconv.FormatInt(expiry.Unix(), 10)

	return payload + "." + base64.RawURLEncoding.EncodeToString(s.sign(purposeSession, payload))
}

// SealValue signs an arbitrary short-lived value with this installation's key.
//
// It exists for a module that needs a token this one's key can verify and has no
// business holding a second secret — the passkey ceremony's challenge is the
// case it was written for. The value is SIGNED and not encrypted: a caller can
// read it and cannot change it.
//
// A sealed value is NOT a session and cannot become one; see the purposes above.
func (s *Sessions) SealValue(value string, ttl time.Duration) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(value)) +
		"." + strconv.FormatInt(s.now().Add(ttl).Unix(), 10)

	return payload + "." + base64.RawURLEncoding.EncodeToString(s.sign(purposeValue, payload))
}

// OpenValue reads a value back and refuses one none of this installation's keys
// sealed — the current one or any retired one (ADR 0129).
//
// An expired value is refused with the same error as a forged one, for
// [Sessions.CustomerID]'s reason: telling them apart tells a forger which half
// of the forgery worked.
func (s *Sessions) OpenValue(sealed string) (string, error) {
	payload, signature, found := cutLast(sealed)
	if !found {
		return "", errNoSession
	}
	raw, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !s.verify(purposeValue, payload, raw) {
		return "", errNoSession
	}

	encoded, stamp, found := strings.Cut(payload, ".")
	if !found {
		return "", errNoSession
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || !s.now().Before(time.Unix(seconds, 0)) {
		return "", errNoSession
	}
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", errNoSession
	}

	return string(value), nil
}

// open reads a cookie value and refuses one none of this installation's keys
// sealed. A retired key still opens what it sealed, which is what makes a
// rotation something other than logging everybody out (ADR 0129).
func (s *Sessions) open(value string) (customerID string, expiry time.Time, err error) {
	// The signature is the LAST segment: the identifier may not contain a dot
	// (it is a ULID-shaped token this framework mints) but saying so here would
	// be a second copy of that rule, and cutting from the right needs no copy.
	payload, signature, found := cutLast(value)
	if !found {
		return "", time.Time{}, errNoSession
	}

	raw, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", time.Time{}, errNoSession
	}
	// The MAC is checked BEFORE the payload is parsed: everything after this line
	// is a value one of this installation's keys produced, and everything before
	// it is a string a caller sent.
	if !s.verify(purposeSession, payload, raw) {
		return "", time.Time{}, errNoSession
	}

	id, stamp, found := strings.Cut(payload, ".")
	if !found || id == "" {
		return "", time.Time{}, errNoSession
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return "", time.Time{}, errNoSession
	}

	return id, time.Unix(seconds, 0), nil
}

// verify accepts a MAC produced by the CURRENT key or by any retired one.
//
// # Why every key is tried and the loop does not stop early
//
// It stops as soon as one matches, and that is fine: what an attacker could
// learn from the timing is WHICH of an installation's own keys signed a cookie
// they already hold, which tells them nothing they can use. The comparison
// against each key is still constant time, which is the part that matters — that
// is about the MAC's bytes, not about which key produced them.
//
// A retired key is removed by taking it out of the list. Until then a cookie
// signed with it works, which is the whole point of a rotation and also its one
// hazard: a key that LEAKED must be dropped outright rather than retired, and
// the record says so where an operator reads it (ADR 0129).
func (s *Sessions) verify(purpose, payload string, mac []byte) bool {
	if hmac.Equal(mac, s.sign(purpose, payload)) {
		return true
	}
	for _, key := range s.retired {
		if hmac.Equal(mac, macWith(key, purpose, payload)) {
			return true
		}
	}

	return false
}

// sign is the MAC over a payload, bound to what the payload is FOR.
//
// The purpose goes in first and is followed by a separator no purpose contains,
// so no pair of (purpose, payload) can produce the bytes of another pair.
func (s *Sessions) sign(purpose, payload string) []byte {
	return macWith(s.secret, purpose, payload)
}

// macWith is the MAC under a given key.
//
// It is a function rather than a method because a retired key has no Sessions of
// its own, and writing the construction twice is how the two would drift.
func macWith(key []byte, purpose, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))

	return mac.Sum(nil)
}

// String keeps the signing secret out of a log line.
//
// A struct with a []byte field is printed in full by %v, and a session secret
// printed once into a log file is a secret an operator has to rotate. There is
// nothing else in here worth printing.
func (s *Sessions) String() string { return fmt.Sprintf("identitysession.Sessions(%s)", s.cookieName) }

// cutLast splits a value at its LAST dot.
func cutLast(value string) (before, after string, found bool) {
	i := strings.LastIndex(value, ".")
	if i < 0 {
		return "", "", false
	}

	return value[:i], value[i+1:], true
}
