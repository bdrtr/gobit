package http

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The names one sales-channel concept keeps everywhere.
const (
	// SalesChannelIDParam is the path parameter a channel-scoped storefront
	// route names its channel with.
	//
	// It is spelled exactly as the admin surface already spells the concept in
	// DELETE /admin/v1/products/{id}/sales-channels/{sales_channel_id}, so one
	// concept keeps one spelling on every surface (ADR 0044).
	SalesChannelIDParam = "sales_channel_id"

	// CodeSalesChannelNotAuthorized reports that the request's identity does
	// not hold the sales channel its path names.
	//
	// It carries no module prefix, unlike almost every other code in this
	// repository, and that is deliberate: three surfaces in two modules and a
	// plugin now return this one refusal from this one function, so a prefix
	// would name whichever of them happened to be asked rather than the rule
	// that refused. It replaces product's `product_sales_channel_not_authorized`,
	// which is a breaking change made in the same release that moves the paths.
	CodeSalesChannelNotAuthorized = "sales_channel_not_authorized"

	// CodeSalesChannelMissing reports that the path named no channel at all.
	CodeSalesChannelMissing = "sales_channel_missing"
)

// SalesChannelIDs derives the sales channels a request's identity is bound to.
//
// # The three states, and why nil is not the empty set
//
//   - No identity at all gives nil, which every caller reads as "do not
//     filter". An installation that never wired storefront authentication up
//     still serves a storefront, and returning the empty set there would make
//     it serve NOTHING.
//   - An identity holding no channel gives an EMPTY BUT NON-nil slice, which is
//     "this key is bound to no channel". Collapsing it into nil would hand the
//     whole catalog to a key that was granted none of it.
//   - Otherwise the identity's own set comes back unchanged.
//
// The distinction between the second state and the first is the entire security
// content of this function, and it is why it exists once rather than at each
// call site.
//
// # Why this is published
//
// It was written three times — in the product module's GraphQL layer, in the
// cart workflow and in the search plugin — because the three CANNOT import each
// other: a module may not import another module's packages (Principle 2.1), a
// workflow may not import a module (ADR 0006), and a plugin is outside both.
// `internal/arch/sales_channel_scope_test.go` existed to hold the copies
// together by comparing their answers over a table of identities, which is the
// weaker guarantee this repository accepts only when the stronger one is
// unavailable.
//
// Here the stronger one IS available: [Principal] is already published from
// this package, `SalesChannelIDs` is a field on it, and every one of the three
// callers already imports this package. So the agreement becomes true by
// construction instead of by audit — the same trade ADR 0036 made for component
// names — and the cost is one more name in a surface that is a promise kept
// forever (ADR 0026).
//
// The cost is real and worth stating plainly: this function can never be
// withdrawn before 1.0.0 without breaking an embedder who named it. What buys
// it is that an out-of-tree plugin serving a channel-scoped storefront read has
// no other way to derive this set correctly, and the failure mode of deriving
// it wrongly is one storefront reading another's catalog.
func SalesChannelIDs(ctx context.Context) []string {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return nil
	}

	if principal.SalesChannelIDs == nil {
		return []string{}
	}

	return principal.SalesChannelIDs
}

// SalesChannelScope narrows a channel-scoped storefront read to the ONE channel
// its path names, and refuses a channel the request's identity does not hold.
//
// It returns the scope to filter by — always exactly one channel — or a
// Forbidden error. It is the only place in the tree that reads
// [SalesChannelIDParam] off a request, and `internal/arch` holds that: a
// handler registered on a path carrying that segment must reach the value
// through here rather than off the router itself.
//
// # Why the segment can only NARROW
//
// The path is a client-supplied input and the identity is not. Taken alone the
// segment would let any holder of a publishable key read any channel's catalog
// by editing a URL, which is exactly the mistake the query parameter was never
// allowed to make. Intersected with the identity it can only ever pick one of
// the channels the key already carried, so the URL becomes the CACHE KEY of an
// answer the key was already entitled to (ADR 0044) and never a way to ask for
// a wider one.
//
// # Why the refusal is 403 and not 404
//
// A 404 is what a HIDDEN PRODUCT gets, and it is 404 precisely so that a key's
// owner cannot enumerate another storefront's handles one at a time. No such
// leak exists here, because this function never consults the channel table: it
// compares the segment against the key's own set and nothing else. The answer
// is therefore identical whether the named channel exists, belongs to another
// merchant, or was never created — "I know who you are and this is not yours",
// which is what a 403 means in this repository (see [RequireScope]).
//
// # The deployment without storefront authentication
//
// Where there is no identity at all, [SalesChannelIDs] returns nil, there is no
// set to intersect against, and the path value stands alone. That is NARROWER
// than filtering by nothing and never wider, which is why it is allowed to pass
// rather than being made an error.
func SalesChannelScope(r *http.Request) ([]string, error) {
	channelID := strings.TrimSpace(chi.URLParam(r, SalesChannelIDParam))
	if channelID == "" {
		return nil, coreerrors.Invalid(CodeSalesChannelMissing,
			"the %s path parameter is required", SalesChannelIDParam)
	}

	// nil is "no identity in this deployment"; an EMPTY BUT NON-nil slice is an
	// identity that holds no channel, and that one holds nothing to narrow to.
	// Collapsing the two would let a channelless key read whatever channel it
	// names.
	held := SalesChannelIDs(r.Context())
	if held != nil && !slices.Contains(held, channelID) {
		return nil, coreerrors.Forbidden(CodeSalesChannelNotAuthorized,
			"this key is not bound to the %q sales channel", channelID)
	}

	return []string{channelID}, nil
}
