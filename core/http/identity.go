package http

import "net/http"

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
// its absence.
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
//     difference. What the framework refuses is to proceed when NOBODY has been
//     asked.
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
// cart would then read it — and the cart's guest-to-registered handover is
// exactly what ADR 0043 does not decide.
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
// The core declares them and does not itself return any of them, which is
// deliberate. The module that binds the contract is the one that can refuse a
// request, and there is more than one such module coming: the b2b storefront
// reads a customer out of its own path parameter today and ADR 0008 names a
// third place in the cart. One fact — "nobody was asked" — spelled three ways
// would make a client keep a dictionary per module, and the spelling of a code
// is the part of an error a client is entitled to branch on.
const (
	// CodeIdentityNotBound means no [Identity] was registered under
	// [IdentityName], so nobody could be asked whether the caller is the
	// customer the request names.
	//
	// It is a REFUSAL and not a fallback. ADR 0007's row for an unconfigured
	// authenticator is to reject every request, and this is that row: a
	// missing spending policy means "this installation has no B2B", which is a
	// complete answer, while a missing identity does not mean "every caller is
	// who they say they are" — it means nobody looked.
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
