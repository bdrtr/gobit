// Package graph is the GraphQL storefront read surface of the product module.
//
// # Why a second read surface
//
// The storefront client wants the title, the variants, the price and the
// inventory of a single product page together; in REST that means pulling the
// whole of a fixed body. GraphQL lets the client say what it wants, and the
// core's query layer (fetch the root -> resolve the link -> batch fetch ->
// merge) already works in that shape.
//
// # The scope is NARROW
//
// Read only: products and product. There is NO mutation and no admin surface
// (the rationale is at the top of schema.graphqls). Keeping it narrow is about
// seeing the pattern settle correctly on a small surface first.
//
// # It REACHES the service, NOT the repository
//
// The resolvers call the storefront methods of [service.Service]; reaching down
// to the repository or writing new SQL is FORBIDDEN. The sales channel
// visibility rule lives in ONE place and a second implementation silently
// diverges: every bug in this repository belonged to the class "the rule is
// defined in one place and not applied in another" — the search plugin dodged
// the same trap deliberately (the plugin only indexes identity, product fetches
// the records).
//
// # Protection
//
// The endpoint sits under /store/v1 and that is not a placement choice but a
// protection choice: publishable key verification and the rate limit come
// AUTOMATICALLY from the stack bound to that prefix (see corehttp.APIGuards)
// and the sales channel identities are filled into the Principal from there.
// Opening a separate prefix would mean writing the identity and quota rules a
// second time.
//
// # Generated code
//
// generated.go and models_gen.go are produced by gqlgen (make gen,
// configuration ../gqlgen.yml) and are NOT EDITED. Everything written by hand
// lives in separate files: schema.graphqls, graph.go, resolver.go, handler.go.
package graph

import (
	"context"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Path is the full path of the GraphQL endpoint.
//
// The constant is exported because the place that binds the endpoint
// (api/routes.go) and the place that describes it (api/describe.go) are two
// separate places; repeating the path as a string would mean one of them
// changing and the other being forgotten — the documentation would then
// describe an endpoint that does not exist, and the OpenAPI test only catches
// that when the path name matches BY HAND.
const Path = "/store/v1/graphql"

// Storefront is the SURFACE the resolvers need from the service.
//
// The reason an interface is used instead of the concrete service is testing:
// verifying that the channel filter is really passed through must not require a
// database. The surface is deliberately TWO methods; growing it would mean
// GraphQL spilling out of the storefront service.
type Storefront interface {
	ListStoreProducts(
		ctx context.Context,
		opts service.StoreListOptions,
	) (service.ListResult[service.StoreProduct], error)

	GetStoreProduct(
		ctx context.Context,
		idOrHandle string,
		salesChannelIDs []string,
	) (service.StoreProduct, error)
}

// ProductList is the GraphQL counterpart of the storefront list.
//
// It is an ALIAS, NOT a new type: the schema envelope and the envelope the
// service returns are the same thing, and holding the two as two types would
// require a converter between them — that is, a second definition of the
// envelope. If the service adds a field to [service.ListResult] the schema does
// not show it (a field not written into the schema is invisible), but if a
// field name changes the generated code DOES NOT COMPILE.
type ProductList = service.ListResult[service.StoreProduct]

// SalesChannelIDsFromContext reads the sales channels the request is bound to
// from the VERIFIED IDENTITY.
//
// The rule lives here, in the single place both read surfaces (the REST
// handlers and the GraphQL resolvers) can reach. A second copy would produce
// exactly the class of bug this module avoids: the rule being fixed in one
// place and forgotten in the other means a catalog leak on one of the surfaces.
//
// The channel CANNOT be a value the client states; that is why the only input
// is the context. Had a query argument or a query string been accepted, the
// filter would stop being an authorization and turn into a display preference,
// and a client arriving with any publishable key it happened to hold could read
// ANOTHER channel's catalog. corehttp.RequireStore puts the identity in place;
// the channel list comes from the key's record.
//
// Whether the return value is nil or not is MEANINGFUL
// (see [service.StoreListOptions]):
//
//   - If there is NO identity, nil is returned. That is the case where store
//     authentication was never wired up in this deployment (product can be
//     deployed on its own) and the filter is not applied; otherwise the
//     storefront would silently empty out in a deployment without auth.
//   - If there IS an identity, nil is NEVER returned: an identity without
//     channels means the EMPTY SET, it does not mean "no filtering". Treating
//     those two cases as one would open the catalog of all channels to an
//     identity that has no channel.
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
