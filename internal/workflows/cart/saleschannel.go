package cart

import (
	"context"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// This file carries the sales channel scope of the cart WRITE path.
//
// # The hole that was closed
//
// Channel scope was for a while enforced ONLY on the read surface: the storefront
// list, its counter, the single-item endpoint and the bulk read all went through a
// single SQL template (product/repository/saleschannel.go), but this workflow
// variant, the one that adds a line to the cart, read ONLY BY ID, unfiltered. The
// result made the rule itself meaningless: a client arriving with channel B's
// publishable key could take the ID of a variant sold only in channel A, write it
// into the POST /store/v1/carts/{id}/line-items body, and so add it to the cart and
// buy it. A product hidden in the storefront was sellable in the cart.
//
// # The rule is NOT DEFINED HERE
//
// This package does NOT know the rule "a product with no channel assignment is
// visible everywhere, one with an assignment is visible only in the channels it is
// assigned to", and it must not: the rule is the product module's data and it lives
// there in a single place. The only thing the workflow does is to ADD the request's
// channels to the catalog read and to accept the answer as it comes (see
// [Workflows.variantTitle]). Restating the rule here — reading the links of the
// variant's product and looking at the intersection — would be a second definition;
// the day the two diverged, the storefront would hide a product while the cart went
// on selling it, that is, the closed hole would reopen in its own mirror.
//
// # Channels DO NOT COME FROM THE CLIENT
//
// The only source is corehttp.Principal: the channel list coming from the
// publishable key's record. Had it been carried as a parameter, nothing would stand
// between the caller and filling it in itself — the same reasoning is why this
// workflow rejects PRICE and TITLE parameters as well (see [Interop]). The same
// decision has been made twice more in the repository: the GraphQL schema has NO
// channel argument, and ADR 0008 keeps the client's declaration outside the
// authorization decision.
//
// # Why it is read from the context
//
// The channel set was not added to the workflow's signature; it is read from the
// context. The pattern is new in the repository but it is not alone: the auth module
// too checks privilege escalation inside a service method, with
// corehttp.PrincipalFromContext (see authsvc.requireGrantableScopes), and there too
// it says "if there is no principal, the check is not applied".
//
// The alternative — carrying the channels from the handler in a primitive parameter
// — would open three new boundaries (the cart module's api.LinePricing interface,
// this package's [Interop] surface and the adapter in between) and the compiler
// checks none of those boundaries (ADR 0006). More importantly, the scope decision
// would THEN be in the caller's hands: when a new write path forgot to pass the
// parameter nothing would blow up, the scope would simply disappear in silence.
// Reading from the context ties the decision to the catalog read itself — every path
// that gets a variant into a cart goes through that read.
//
// The price, stated honestly: the dependency is INVISIBLE in the signature. That is
// why a new variant read skipping the channel decision is not a compile error, and
// an invariant under internal/arch walks the structure to close that gap
// (see TestVariantReadsGoThroughTheChannelDecision).
//
// # The scope is enforced AT THE ENTRANCE
//
// The check is where the variant ENTERS the cart. The path that updates a line's
// quantity (see [Workflows.UpdateLineItem]) and the workflow that turns the cart
// into an order (internal/workflows/checkout) do NOT ask for the catalog scope
// AGAIN, and this is not an omission but a decision that was made:
//
//   - The only path that can get a variant into a cart is adding a line; while that
//     door is closed a foreign channel's variant can never enter the cart, so on the
//     quantity update and completion paths there is nothing left to close.
//   - That a line which HAS ENTERED the cart is unaffected by the catalog changing
//     afterwards is the product module's WRITTEN decision ("the name of a product
//     added to a cart must remain resolvable even if that product is later moved to
//     another channel", see productsvc.productProvider.List). Filtering again at
//     completion contradicts that decision and would render the customer's full cart
//     unpayable because of an administrator's catalog edit.
//
// Where the boundary stands is written in the README as well; an unwritten boundary
// is a boundary that does not exist.

// SalesChannelIDsFromContext reads the sales channels the request is bound to FROM
// THE AUTHENTICATED PRINCIPAL.
//
// Whether the return value is nil or not IS MEANINGFUL, and the distinction is
// EXACTLY the same as the one on the read surface
// (see product/graph.SalesChannelIDsFromContext):
//
//   - If there is NO principal it returns nil and no filter is applied. This is the
//     counterpart of the setup in which store authentication was never wired up at
//     all; workflows can also be called from inside the process (seed data, tests, a
//     batch job later on) and having those calls see an empty catalog would mean
//     filtering by a principal that does not exist.
//   - If there IS a principal, nil is NEVER returned: a principal without channels
//     is the EMPTY SET, not "no filtering". Treating the two as one would open the
//     catalog of every channel to a key with no channels — and that is exactly the
//     hole that was closed.
//
// That the two surfaces separate the same three cases is nailed down by an arch
// test; a divergence would be silent, because it would only become visible under a
// particular shape of principal.
func SalesChannelIDsFromContext(ctx context.Context) []string {
	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}

	if principal.SalesChannelIDs == nil {
		return []string{}
	}

	return principal.SalesChannelIDs
}

// salesChannelFilter produces the channel filter to be added to the catalog query.
//
// The second return value says WHETHER the filter is to be APPLIED; writing a nil
// into the filter map to mean "absent" could not be told apart, on the provider
// side, from the empty set (a principal without channels) — Query filters are
// map[string]any and what carries the meaning there is THE PRESENCE OF THE KEY.
func salesChannelFilter(ctx context.Context) (channels []string, apply bool) {
	channels = SalesChannelIDsFromContext(ctx)
	return channels, channels != nil
}
