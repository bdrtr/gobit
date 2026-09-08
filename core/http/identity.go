package http

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// Identity proves WHICH CUSTOMER a storefront request belongs to.
//
// # gobit does not implement this, and that is the decision rather than a gap
//
// The storefront's only principal is the publishable key, and that key binds a
// request to a SALES CHANNEL, not to a shopper (see [Principal], whose four
// fields carry no customer). gobit holds no proof about the person behind a
// storefront request: it issues no customer session, no cookie, no signing key
// and no rotation policy for a storefront it does not serve. ADR 0008 drew that
// boundary and ADR 0043 extends it without moving it — the framework now
// REQUIRES the embedder's verifier at the address book, and refuses to guess in
// its absence. ADR 0057 carries the same comparison to the b2b storefront and
// the cart without the requirement: there a verifier that is bound is BELIEVED
// against the claim, and one that is absent leaves those surfaces answering as
// they always have.
//
// So the implementation lives outside this repository. gobit publishes the
// shape, resolves it by name and compares what it proves against what the
// request claims; whether the proof is a session cookie, a JWT, a header from
// an upstream proxy or a call to somebody else's identity provider is a
// question this package deliberately cannot ask.
//
// # Why every type in the signature is stdlib
//
// It is what makes the contract satisfiable from OUTSIDE. A signature naming
// anything under internal/ would compile here and strand the embedder: the Go
// toolchain refuses that import to an outside module, so the implementer could
// not write the type. That was measured rather than assumed on 2026-09-07 — a
// separate module with its own go.mod, importing only the published facade,
// core/module and core/container, implementing [Identity] and providing it
// under [IdentityName] from its own Register, builds and exits 0.
//
// # What an implementation owes
//
//   - Return the identifier it can PROVE. Returning the value the request
//     itself supplied — the path segment, a header the client sets — satisfies
//     the interface and proves nothing, and the framework cannot tell the
//     difference. What the framework refuses everywhere is to proceed when the
//     answer CONTRADICTS the request; whether it also refuses when nobody has
//     been asked is decided per surface (ADR 0043, ADR 0057).
//   - Return an ERROR when it cannot prove one, never an empty identifier with
//     a nil error. The caller has no way to tell that pair apart from a proof
//     of the empty customer, so it is refused as [CodeIdentityUnproven].
//   - Choose the STATUS by choosing the error's kind. The error travels to
//     [WriteError] unwrapped, so errors.Unauthorized turns into a 401 asking
//     the shopper to sign in, errors.Forbidden into a 403, and an untyped error
//     into a 500 whose message the client never sees.
//
// # What it is NOT
//
// It is not a middleware. chi panics on a Use that arrives after a route is
// registered and [NewRouter] registers its own before it returns, which is why
// [DeferredAuthenticator] exists at all; and a middleware on the store prefix
// would put a customer in the request context for the WHOLE surface, where the
// cart would then read it — and the cart's guest-to-registered handover, which
// ADR 0043 deferred and ADR 0057 decides, is decided at the HANDLER that reads
// the claim rather than by a context value every route inherits.
//
// It is not a field on [Principal] either. Widening the principal would change
// the admin authorization model for a concept only the storefront reads, and
// keeping it out is one of the four blockers ADR 0008 listed against a customer
// token that this shape clears by construction.
type Identity interface {
	// CustomerID returns the customer identifier the request PROVES.
	//
	// The request is passed whole because an implementation reads what it
	// alone knows about: a cookie, an Authorization header, a header written
	// by an upstream proxy, or the request's context. It must not be
	// modified — the caller goes on serving the same request afterwards.
	CustomerID(r *http.Request) (string, error)
}

// IdentityName is the container name an [Identity] is registered under.
//
// It is PUBLISHED for the reason core/plugin's CallbacksName is: the embedder
// names the slot it registers into, the module that reads it names the same
// slot, and nothing in a build compares two string literals. A renamed slot
// would make every address book request reject — loudly, which is the better
// half of the failure — but nothing would say why. A constant both sides can
// NAME turns that into a compile error; an embedder that types "core.identity"
// as a literal anyway still gets the old hazard, and nothing audits that
// (ADR 0043 accepts it).
//
// The name is core-owned, so the direction of naming runs MODULE TO CORE: the
// customer module resolves a name the core declares, the way it already
// resolves the core pool, and no module owns a slot another module's embedder
// has to fill.
const IdentityName = "core.identity"

// The identity codes. Clients branch on these; the messages may change.
//
// The core declares them so that one fact — "nobody was asked" — is not spelled
// three ways by three modules; the spelling of a code is the part of an error a
// client is entitled to branch on, and a dictionary per module is what the
// alternative costs.
//
// Until ADR 0057 the core declared them and returned none of them, on the
// reasoning that the module binding the contract is the one that can refuse a
// request. What changed is not that reasoning but the COUNT: three surfaces
// name a customer today — the address book, the b2b storefront and the cart —
// and the comparison that produces these codes is an authorization rule. Three
// copies of one is three chances for one to answer differently while still
// answering, which is why [ProvenCustomer] is here and returns them.
const (
	// CodeIdentityNotBound means no [Identity] was registered under
	// [IdentityName], so nobody could be asked whether the caller is the
	// customer the request names.
	//
	// Where it is returned it is a REFUSAL and not a fallback: ADR 0007's row
	// for an unconfigured authenticator is to reject, because a missing
	// spending policy means "this installation has no B2B" — a complete answer
	// — while a missing identity does not mean "every caller is who they say
	// they are". It means nobody looked.
	//
	// WHETHER to return it is the caller's decision and not this package's,
	// and the tree makes it both ways on purpose. The address book refuses
	// (ADR 0043): no anonymous caller has a correct use for somebody's street
	// address. The cart and the b2b storefront do not (ADR 0057): they are in
	// service in installations that bound nothing, and withdrawing a working
	// surface is a bigger change than the leak it would close. Those two log a
	// WARN naming this slot instead, and their records say the oracle stays
	// open until a verifier is bound.
	CodeIdentityNotBound = "identity_not_bound"

	// CodeIdentityMismatch means the identity proved a DIFFERENT customer than
	// the request named.
	//
	// The kind is Forbidden rather than NotFound: the caller is authenticated
	// (the publishable key was accepted) and the resource exists; what is
	// missing is the right to it. A 404 would hide the record's existence, and
	// there is nothing to hide — the caller supplied the identifier.
	CodeIdentityMismatch = "identity_mismatch"

	// CodeIdentityUnproven means the bound [Identity] returned neither an
	// identifier nor an error.
	//
	// That is an implementation fault, not a request fault, so it is
	// KindInternal: no change the client makes to its request would alter the
	// answer, and a 403 here would send the shopper looking for a permission
	// problem that does not exist.
	CodeIdentityUnproven = "identity_unproven"
)

// ProvenCustomer returns the customer a storefront request may act on, or the
// error that ends the request.
//
// claimed is what the request SAYS — a path segment, or a field in the body.
// identity is the verifier to ask. The two must name the same customer and
// every other outcome is a refusal:
//
//   - no identity — [CodeIdentityNotBound], Unauthorized. Nobody could be
//     asked, so nothing was checked, and the answer to "was this checked" must
//     never be a silent yes.
//
//     Reaching this function with a nil identity is therefore a DECISION to
//     refuse, and the caller makes it before calling: the address book passes
//     what it holds and takes the refusal (ADR 0043), while the cart and the
//     b2b storefront do not call at all when nothing is bound, because
//     withdrawing a surface they ship working is a larger change than the leak
//     it closes (ADR 0057). This branch is what an unbound proof MEANS, not a
//     ruling on which surfaces must demand one.
//
//   - the identity returns an error — passed through UNWRAPPED, so the embedder
//     picks the status by picking the error's kind. Wrapping it here would
//     overwrite the embedder's answer with a guess made by a package that knows
//     nothing about how the proof was obtained.
//
//   - the identity proves nothing and reports no error — [CodeIdentityUnproven],
//     Internal. The pair cannot be told apart from a proof of the empty
//     customer, so it is refused BEFORE the comparison rather than by it:
//     falling through would report a broken implementation as a mismatch and
//     send an operator looking for a wrong session instead of a wrong verifier.
//
//   - the two disagree — [CodeIdentityMismatch], Forbidden. Not a 404: the
//     caller supplied the identifier, so there is no existence to hide, and a
//     404 would send an honest client debugging a record that is right there.
//
// # Why it is HERE and not copied into each module
//
// It is an authorization rule with three call sites, and a wrong copy of an
// authorization rule keeps answering. The customer module wrote it first
// (ADR 0043), the b2b storefront and the cart needed the same comparison, and
// the b2b package doc had already refused to take the contract in passing for
// exactly this reason. The three modules keep their own LAZY BINDING — the
// per-module resolve wrapper is an established idiom here and its failure mode
// is loud — and share the comparison, which is the half that must not diverge.
//
// What each module keeps is the POLICY around the comparison: which surfaces
// ask, and what an installation with no verifier is told. Those differ by
// surface and are argued where the surface is (ADR 0043, ADR 0057). What may
// not differ is the answer to "do these two name the same customer", which is
// the only question here.
//
// # Why it takes the claim instead of reading it
//
// The three surfaces spell the claim three ways: the address book reads the
// path parameter "id", the b2b storefront reads "customer_id", and the cart
// reads a field out of a decoded body. A helper that went looking for the claim
// would have to know all three and would silently find nothing on the fourth.
//
// # Why the comparison is exact
//
// A customer identifier is an opaque token this framework generates, not a name
// a person types. Folding case here would read ADR 0038's and ADR 0039's rule
// backwards: two DIFFERENT identifiers would match, and the caller holding the
// folded twin would act as somebody else.
//
// # Why it returns the PROVEN identifier
//
// The two are equal by the time it returns, so it makes no difference today —
// which is exactly why the proven one is the safer habit. The day a caller
// grows a case where they may legitimately differ (an operator acting for a
// customer, say), handing back the claim would pass the unverified string on
// and turn the comparison above into decoration.
func ProvenCustomer(identity Identity, r *http.Request, claimed string) (string, error) {
	if identity == nil {
		return "", coreerrors.Unauthorized(CodeIdentityNotBound,
			"this installation has bound no customer identity under %q, so a request "+
				"naming a customer cannot be served", IdentityName)
	}

	proven, err := identity.CustomerID(r)
	if err != nil {
		return "", err
	}
	if proven == "" {
		return "", coreerrors.Internal(CodeIdentityUnproven,
			"the bound customer identity returned neither an identifier nor an error")
	}
	if proven != claimed {
		return "", coreerrors.Forbidden(CodeIdentityMismatch,
			"the request names customer %q and the bound identity proves a different one",
			claimed)
	}

	return proven, nil
}
