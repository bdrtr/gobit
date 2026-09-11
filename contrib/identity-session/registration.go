package identitysession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The codes self-registration answers with.
const (
	// CodeRegistrationInvalid is a registration this module will not start.
	CodeRegistrationInvalid = "identity_session_registration_invalid"
	// CodeRegistrationNotUsable is a token that is not a pending registration.
	//
	// ONE code for never existed, already used and expired. Telling them apart
	// would answer, for any token somebody cares to try, whether it was ever
	// real — and the person holding a real one is about to be signed in, so
	// nothing is hidden from them that they could have asked for.
	CodeRegistrationNotUsable = "identity_session_registration_not_usable"
)

// DefaultRegistrationTTL is how long a sign-up link works for.
//
// One hour, because the person is AT THE KEYBOARD when they ask: the link is
// used within a minute or two in the ordinary case, and a longer window is a
// longer time during which a message sitting in a mailbox is an account waiting
// to be taken. A shop whose mail is slow raises it.
const DefaultRegistrationTTL = time.Hour

// Accounts is how this module reaches the shop's own notion of a customer.
//
// # Why it is a seam and not a call
//
// Opening an account is TWO writes in two ownerships: a customer record, which
// belongs to whatever holds customers, and a credential, which belongs here.
// This module cannot make the first one — gobit does not produce identity (ADR
// 0008/0043) and a contrib module reaching into another module's tables would
// be worse than not shipping the flow at all.
//
// So the installation binds this. The signatures use only primitive types, which
// is the same discipline every cross-module surface in this repository follows:
// a consumer that cannot import the producer cannot name its types, and a
// primitive signature can be repeated verbatim on both sides.
//
// Self-registration is NOT MOUNTED unless this is bound. A module that cannot
// open an account must not publish an endpoint that offers to.
type Accounts interface {
	// CustomerIDForEmail answers the customer that address already belongs to,
	// or the EMPTY STRING when there is none.
	//
	// Empty rather than a sentinel error: "nobody has this address" is an
	// ordinary answer on this path rather than a fault, and a sentinel cannot
	// cross a boundary that carries only primitives.
	CustomerIDForEmail(ctx context.Context, email string) (string, error)
	// OpenAccount creates a customer for a proven address and answers its id.
	//
	// It is called only AFTER the address has been proven, so an implementation
	// may treat the address as real.
	OpenAccount(ctx context.Context, email string) (string, error)
}

// Verification is how the proof reaches the person.
//
// A shop sends mail with its own client, its own templates and its own sending
// domain; this module has no business choosing any of those. What it needs is
// somebody to carry a token to an address.
//
// # Why there are TWO methods
//
// The registration endpoint answers the same thing whether the address is new or
// already has an account, because anything else is an oracle for whether a given
// person shops here. But answering the same thing and DOING nothing would leave
// somebody who forgot they had an account staring at a form that appears to have
// worked. The second message is what keeps the endpoint from being both an oracle
// and a dead end.
type Verification interface {
	// SendVerification carries a token to an address that has no account yet.
	//
	// The token is the secret: whoever holds it can complete the registration,
	// so an implementation puts it in the message and nowhere else — not in a
	// log line, not in an analytics event.
	SendVerification(ctx context.Context, email, token string) error
	// SendAlreadyRegistered tells somebody they already have an account.
	//
	// It carries no token, because no registration was started.
	SendAlreadyRegistered(ctx context.Context, email string) error
}

// Registrations is the OPTIONAL capability a store offers to hold a pending
// registration.
//
// Separate from [Credentials] for that interface's own reason: an installation
// binding LDAP or its own users table has nowhere to put a row of gobit's, and
// such a store leaves self-registration unmounted rather than half-working.
type Registrations interface {
	// PutRegistration writes a pending registration, REPLACING any the same
	// address already had.
	PutRegistration(ctx context.Context, tokenHash, email, passwordHash string, expiresAt time.Time) error
	// TakeRegistration removes a pending registration and answers what it held.
	//
	// Removing and reading are ONE statement, which is what makes a token
	// single-use: two requests arriving with the same token cannot both be
	// answered, whatever the timing. It answers [ErrNoRegistration] for a token
	// that is unknown, already used or expired.
	TakeRegistration(ctx context.Context, tokenHash string) (email, passwordHash string, err error)
}

// ErrNoRegistration is a token that is not a usable pending registration.
var ErrNoRegistration = errors.New(
	"identity-session: that token is not a pending registration")

// registerRequest is the body of a self-registration.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// verifyRequest is the body of a verification.
type verifyRequest struct {
	Token string `json:"token"`
}

// selfRegistrationMounted says whether the flow has everything it needs.
//
// All three: somebody to open an account, somebody to carry the proof, and a
// store that can hold a pending row. Missing any one of them and the endpoints
// are not mounted at all, which is the only honest state — an endpoint that
// accepts a registration it can never complete has taken somebody's password
// and given them nothing.
func (m *Module) selfRegistrationMounted() bool {
	if isNil(m.opts.Accounts) || isNil(m.opts.Verification) || m.store == nil {
		return false
	}
	_, ok := m.store.(Registrations)

	return ok
}

// isNil answers whether a bound seam is really there.
//
// `x == nil` is not enough and the difference is not academic: an interface
// holding a NIL POINTER is not nil, so an installation writing
// `Verification: shop.Mailer()` where that constructor returns a typed nil when
// it is unconfigured would mount these endpoints and panic on the first
// registration — inside a handler, on a path that has already taken somebody's
// password.
//
// Found by a test that built exactly that shape by accident. The reflection runs
// once, at Routes time.
func isNil(seam any) bool {
	if seam == nil {
		return true
	}

	value := reflect.ValueOf(seam)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

// register starts a self-registration.
//
// # It answers the same thing for a new address and for one that has an account
//
// 202 either way, with no detail. Anything else is an oracle for whether a
// person shops here, which is a question their neighbor, their employer and
// whoever bought a list of addresses would all like answered. What differs is
// the message that goes out, so the person who already has an account is not
// left staring at a form that appeared to work.
//
// # Nothing about the person is created here
//
// One row in this module's own table, holding the address, the hashed password
// and the hash of a token. No customer, no credential, no session. Creating a
// customer for an unproven address lets anybody fill the shop's table with
// addresses that are not theirs.
func (m *Module) register(w http.ResponseWriter, r *http.Request) {
	var body registerRequest
	if !decode(w, r, &body) {
		return
	}

	email, err := registrationAddress(body.Email)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeRegistrationInvalid,
			"%s", err.Error()))

		return
	}

	// The password is hashed BEFORE anything else is decided, so the plaintext
	// does not outlive the shortest path through this handler.
	passwordHash, err := HashPassword(body.Password)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeRegistrationInvalid,
			"%s", err.Error()))

		return
	}

	existing, err := m.opts.Accounts.CustomerIDForEmail(r.Context(), email)
	if err != nil {
		m.unavailable(w, r, "whether that address already has an account could not be read", err)

		return
	}
	if existing != "" {
		if err := m.opts.Verification.SendAlreadyRegistered(r.Context(), email); err != nil {
			m.unavailable(w, r, "the already-registered message could not be sent", err)

			return
		}
		corehttp.WriteJSON(r.Context(), w, http.StatusAccepted, nil)

		return
	}

	token, tokenHash, err := newRegistrationToken()
	if err != nil {
		m.unavailable(w, r, "the registration token could not be minted", err)

		return
	}

	store, _ := m.store.(Registrations)
	if err := store.PutRegistration(
		r.Context(), tokenHash, email, passwordHash, m.registrationDeadline()); err != nil {
		m.unavailable(w, r, "the registration could not be recorded", err)

		return
	}

	// Recorded first, then sent. The other order hands somebody a link that
	// cannot work; this order can leave a row nobody uses, and that row expires
	// and is replaced the moment they ask again.
	if err := m.opts.Verification.SendVerification(r.Context(), email, token); err != nil {
		m.unavailable(w, r, "the verification message could not be sent", err)

		return
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusAccepted, nil)
}

// verifyRegistration completes a registration and signs the person in.
//
// # The token is consumed BEFORE the account is opened
//
// [Registrations.TakeRegistration] removes the row and returns it in one
// statement, so the token stops working the instant it is used, even if
// everything after this fails. The cost is real and chosen: a failure in the
// next two steps loses the registration and the person has to start again.
//
// The other order — open the account, then delete the row — would leave a
// replayable token. Replaying it looks harmless because the address and the hash
// are the same, but the hash is the one FROM THE REGISTRATION: somebody who
// changed their password afterwards would have it silently set back to the one
// they signed up with, by a link sitting in their mailbox.
//
// # Why it signs them in
//
// They just proved they control the address. That is the same proof every
// password reset in the world rests on, and asking them to type the password
// they chose ninety seconds ago adds a step and proves nothing further.
func (m *Module) verifyRegistration(w http.ResponseWriter, r *http.Request) {
	var body verifyRequest
	if !decode(w, r, &body) {
		return
	}

	store, _ := m.store.(Registrations)
	email, passwordHash, err := store.TakeRegistration(
		r.Context(), hashRegistrationToken(strings.TrimSpace(body.Token)))
	switch {
	case errors.Is(err, ErrNoRegistration):
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeRegistrationNotUsable,
			"that link is not usable; it may have been used already or expired, so "+
				"start the registration again"))

		return
	case err != nil:
		m.unavailable(w, r, "the registration could not be read", err)

		return
	}

	customerID, err := m.opts.Accounts.CustomerIDForEmail(r.Context(), email)
	if err != nil {
		m.unavailable(w, r, "the customer of a proven address could not be read", err)

		return
	}
	if customerID == "" {
		// Opened only now, with the address proven. A customer that exists by
		// this point is somebody who registered, or checked out as a guest,
		// between the two halves of this flow — and that person keeps their
		// record rather than getting a second one.
		customerID, err = m.opts.Accounts.OpenAccount(r.Context(), email)
		if err != nil {
			m.unavailable(w, r, "the account could not be opened", err)

			return
		}
	}

	if err := m.store.Put(r.Context(), customerID, email, passwordHash); err != nil {
		m.unavailable(w, r, "the credential could not be written", err)

		return
	}

	m.log.InfoContext(r.Context(), "identity-session opened an account from a proven address",
		"customer_id", customerID)

	m.sessions.Issue(w, customerID)
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// registrationAddress folds an address and refuses one this module will not send
// to.
//
// The check is deliberately MINIMAL. A shop's real address validation is the
// message arriving, and a regular expression that rejects valid addresses — the
// long tail of them is genuinely strange — turns a working sign-up into a
// support ticket. What is refused is only what cannot be an address at all, and
// what would put a header or a second recipient into somebody's mailer.
func registrationAddress(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))

	switch {
	case email == "":
		return "", errors.New("an e-mail address is required")
	case strings.ContainsAny(email, " \t\r\n,;<>\""):
		return "", errors.New("that e-mail address contains a character an address cannot have")
	case strings.Count(email, "@") != 1:
		return "", errors.New("an e-mail address has exactly one @")
	case strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@"):
		return "", errors.New("an e-mail address has something on both sides of its @")
	case len(email) > maxAddressLen:
		return "", fmt.Errorf("an e-mail address is at most %d characters", maxAddressLen)
	}

	return email, nil
}

// maxAddressLen is the longest address this module accepts.
//
// 254 is the longest a path can carry per RFC 5321, and a bound exists at all so
// that the column, the template and the log line all have one.
const maxAddressLen = 254

// registrationTokenLen is how many random bytes a sign-up link carries.
//
// 32 bytes, which is the same floor the session secret has for the same reason:
// whoever guesses it becomes the account.
const registrationTokenLen = 32

// newRegistrationToken mints a token and the hash that is stored for it.
func newRegistrationToken() (token, tokenHash string, err error) {
	raw := make([]byte, registrationTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("identity-session: the token could not be read: %w", err)
	}

	token = hex.EncodeToString(raw)

	return token, hashRegistrationToken(token), nil
}

// hashRegistrationToken is what the table holds instead of the token.
//
// SHA-256 and not argon2id, and the difference is the input rather than
// laziness: a password is chosen by a person and has to survive an offline
// attack on a leaked hash, while this is 32 bytes from crypto/rand and has
// nothing to guess. What the hash buys here is that a leaked TABLE is not a list
// of working links.
func hashRegistrationToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

// registrationDeadline is when a link minted now stops working.
func (m *Module) registrationDeadline() time.Time {
	ttl := m.opts.RegistrationTTL
	if ttl <= 0 {
		ttl = DefaultRegistrationTTL
	}

	return time.Now().UTC().Add(ttl)
}

// unavailable logs the cause and answers without it.
func (m *Module) unavailable(w http.ResponseWriter, r *http.Request, what string, err error) {
	m.log.ErrorContext(r.Context(), "identity-session: "+what, "error", err)
	corehttp.WriteError(r.Context(), w, coreerrors.Internal(CodeUnavailable, "%s", what))
}
