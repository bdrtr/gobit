package identitypasskey

import (
	"context"
	"errors"
	"fmt"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
)

// OtherSignIn answers the one question this module cannot answer itself.
//
// # Why the question is phrased this way
//
// The rule is that removing a passkey must leave a way into the account. Half of
// that is a count of this module's own rows. The other half is everything else,
// and this module must not guess what "everything else" is: an installation may
// have bound a verifier that is not the session module at all, in which case a
// password in the session module's table is not a way in — it is a row nothing
// reads. Asking "does this person have a password" would be right in one shop and
// wrong in the next, with the same confidence in both.
//
// So the question asked across the boundary is the module's OWN question, and the
// answer is the installation's. What a way in is, is decided where the modules are
// assembled.
type OtherSignIn interface {
	// Exists reports whether the customer can get in without a passkey.
	//
	// An error is NOT a false. A caller that folded them together would remove
	// somebody's last key on the strength of a failed query, which is why the
	// removal answers "we could not check" rather than proceeding.
	Exists(ctx context.Context, customerID string) (bool, error)
}

// PasswordSignIn states that a password in the session module IS a way in.
//
// It is the ordinary answer and it is still an installation's STATEMENT rather
// than this module's assumption: an SSO shop that keeps the session module only
// for its signing key would be wrong to wire this, and nothing here can tell.
// What the name buys is that wiring it is one readable line in a composition
// root and skipping it is another.
func PasswordSignIn(session *identitysession.Module) OtherSignIn {
	return passwordSignIn{session: session}
}

// passwordSignIn asks the session module.
type passwordSignIn struct{ session *identitysession.Module }

// Exists reports whether the customer has a password.
//
// [identitysession.ErrPasswordUnknown] travels out unchanged. It means the bound
// credential store cannot answer — an LDAP directory that implements no lookup,
// say — and the removal turns it into a refusal that says so rather than into a
// removal that guessed.
func (p passwordSignIn) Exists(ctx context.Context, customerID string) (bool, error) {
	if p.session == nil {
		return false, fmt.Errorf(
			"%w: identitypasskey.PasswordSignIn was given no session module",
			identitysession.ErrPasswordUnknown)
	}

	return p.session.HasPassword(ctx, customerID)
}

// NoOtherSignIn states that a passkey is the ONLY way into an account here.
//
// A shop with no passwords wires this, and the consequence is the honest one: the
// last passkey is never removable, because removing it would end the account. It
// is a statement and not a limitation — the alternative is a person deleting
// their way in and calling support.
func NoOtherSignIn() OtherSignIn { return noOtherSignIn{} }

// noOtherSignIn always answers no.
type noOtherSignIn struct{}

// Exists answers no, without asking anybody.
func (noOtherSignIn) Exists(context.Context, string) (bool, error) { return false, nil }

// unknownOtherSignIn reports whether an error means "could not check" rather than
// a failure of this module's own.
//
// It is a function rather than a comparison at each call site because the two
// modules' errors arrive through one interface and the caller has to tell
// "nobody could answer" from "the database is down" in one place.
func unknownOtherSignIn(err error) bool {
	return errors.Is(err, identitysession.ErrPasswordUnknown)
}
