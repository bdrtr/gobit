package graph

import (
	"context"
	"strings"

	"github.com/99designs/gqlgen/graphql"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Error codes; the client can look them up with errors.CodeOf (or from the
// extensions.code field of the GraphQL response).
const (
	codeBadArgument = "product_graphql_bad_argument"
	codePanic       = "product_graphql_panic"
	// codeBodyTooLarge shows up in the core's error envelope, not in the
	// GraphQL one: the document never reached the executor (see bodyLimit).
	codeBodyTooLarge = "product_graphql_body_too_large"
)

// Resolver binds the GraphQL root to the storefront service.
//
// Resolvers call the SERVICE, they do not reach down to the repository and they
// do not write new SQL; for the rationale see the package documentation.
type Resolver struct {
	svc Storefront
}

// NewResolver builds a root resolver with the given storefront service.
func NewResolver(svc Storefront) *Resolver { return &Resolver{svc: svc} }

// That the generated contract is satisfied is pinned at COMPILE time: when a
// field is added to the schema or its signature changes, the error shows up in
// the build rather than in a test — that is the real gain of schema-first
// generation.
var _ ResolverRoot = (*Resolver)(nil)

// Query returns the query resolver the generated code expects.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

// Variant returns the variant resolver the generated code expects.
func (r *Resolver) Variant() VariantResolver { return &variantResolver{r} }

// queryResolver serves the root queries.
type queryResolver struct{ *Resolver }

// variantResolver serves the field resolutions of the variant.
type variantResolver struct{ *Resolver }

// Products lists the published products.
//
// # The sales channel DOES NOT COME FROM THE QUERY
//
// The channels are read from the request's VERIFIED identity with
// [SalesChannelIDsFromContext]; there is no such argument in the schema and
// there will not be one (see schema_test.go). Had it been an argument, the
// filter would stop being an authorization and turn into a display preference.
//
// # The paging default is NOT HERE
//
// A limit/offset that is not given is passed on as 0; turning 0 into the
// default and clamping it to the ceiling is the service's job (see the service
// normalizePaging). Picking a default here would be a second definition of the
// same rule and the two read surfaces would start returning different page
// sizes.
//
// # An empty text argument counts as NOT GIVEN
//
// If q and collectionId are empty after trimming, nil is passed to the service;
// the rule is the SAME as stringParam in REST and [singleSelector] on the
// single-item endpoint makes the same distinction. Passing them through as they
// are would make the two surfaces diverge: `collectionId: ""` filters by an
// empty identity and returns nothing, while `q: ""` adds an ILIKE scan that
// matches every row without touching the result at all — neither is the
// client's intent and both are silent.
//
// # The count is computed ONLY IF IT IS ASKED FOR
//
// In GraphQL "count" is a field and if the client did not select it, it does
// not show up in the response anyway. In spite of that the query WAS RUNNING: a
// client saying `{ products { items { id } } }` was paying the cost of the
// count query for a number it could never see. Measured (gobit_load, 52,004
// products, LIMIT 20, median): 67.00 ms with the count, 0.65 ms without it —
// that is, a single unselected field was writing 99% of the request's SQL.
//
// [isSelected] asks whether the field was selected. This is NOT A CONTRACT
// CHANGE: the "count: Int!" in the schema stands as it is and always comes back
// filled when the field is selected; the only thing that changed is that an
// unselected field no longer causes work either. REST's "with_count" parameter
// is exactly this behavior's counterpart in the query string (see
// api.Handler.storeListProducts).
func (r *queryResolver) Products(
	ctx context.Context,
	limit, offset *int,
	after, q, collectionID, categoryID, tagID *string,
) (*ProductList, error) {
	// "after" and "offset" name two different positions; honoring both would
	// serve the page N rows past the cursor, which is a position neither of
	// them asked for.
	if after != nil && *after != "" && intValue(offset) != 0 {
		return nil, coreerrors.Invalid(corepage.CodeInvalidCursor,
			`"after" and "offset" name two different positions; send one of them`)
	}

	cursor, err := corepage.Decode(service.ProductListing, stringValue(after))
	if err != nil {
		return nil, err
	}

	result, err := r.svc.ListStoreProducts(ctx, service.StoreListOptions{
		CollectionID:    trimmedPointer(collectionID),
		CategoryID:      trimmedPointer(categoryID),
		TagID:           trimmedPointer(tagID),
		Search:          trimmedPointer(q),
		SalesChannelIDs: SalesChannelIDsFromContext(ctx),
		Limit:           intValue(limit),
		Offset:          intValue(offset),
		After:           cursor,
		SkipCount:       !isSelected(ctx, fieldCount),
	})
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// stringValue reads an optional string argument; a nil pointer is the empty
// string.
func stringValue(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}

// Product returns a single storefront product by identity or by handle.
//
// The channel filter is the SAME as the one on the list: showing a product
// hidden from the list through the single-item endpoint would make hiding
// entirely pointless, and because storefront addresses carry the handle this is
// exactly the guessable query.
func (r *queryResolver) Product(ctx context.Context, id, handle *string) (*service.StoreProduct, error) {
	selector, err := singleSelector(id, handle)
	if err != nil {
		return nil, err
	}

	product, err := r.svc.GetStoreProduct(ctx, selector, SalesChannelIDsFromContext(ctx))
	if err != nil {
		return nil, err
	}

	return &product, nil
}

// PriceSet returns the variant's price set (the pricing module's record).
//
// Why the field is resolved BY HAND: the record arrives in this module as a
// query.Record, while the carrier of the JSON scalar is map[string]any. The two
// are assignable in Go but they are not the SAME type for the generator, so the
// field cannot be bound automatically. The alternative was teaching the core's
// query.Record about GraphQL serialization; that road carries a module's
// presentation concern into the core and would bind the core to a GraphQL
// library (the spirit of Principle 2.4). The conversion here is free: it is an
// assignment, not a copy.
func (r *variantResolver) PriceSet(_ context.Context, obj *service.StoreVariant) (map[string]any, error) {
	return obj.PriceSet, nil
}

// InventoryItem returns the variant's inventory item (the inventory module's
// record).
//
// The rationale is the same as [variantResolver.PriceSet].
func (r *variantResolver) InventoryItem(_ context.Context, obj *service.StoreVariant) (map[string]any, error) {
	return obj.InventoryItem, nil
}

// singleSelector builds the selector handed to the service out of the
// id/handle arguments.
//
// The service takes the two in a SINGLE parameter and tells them apart by the
// prefix (prod_… is an identity, the rest is a handle); that is why the only
// thing done here is the question "was exactly one of them given". Accepting
// both and giving one priority would mean silently interpreting a contradictory
// request: the client would think it wrote the handle and would get the
// identity's answer.
//
// An empty value and one made up only of whitespace count as NOT GIVEN;
// otherwise a client could send `id: ""`, pass the "one of the two arguments
// was given" test and get the error a layer further on, with a far more opaque
// message.
func singleSelector(id, handle *string) (string, error) {
	identity := trim(id)
	name := trim(handle)

	switch {
	case identity != "" && name != "":
		return "", coreerrors.Invalid(codeBadArgument,
			"id and handle cannot be given together; only one of them must be given")
	case identity != "":
		return identity, nil
	case name != "":
		return name, nil
	default:
		return "", coreerrors.Invalid(codeBadArgument,
			"one of the id or handle arguments must be given")
	}
}

// trim trims the optional text argument; returns an empty string if it was not
// given.
func trim(v *string) string {
	if v == nil {
		return ""
	}

	return strings.TrimSpace(*v)
}

// trimmedPointer trims the optional text argument; returns nil if it comes out
// empty.
//
// It applies the same rule as [trim] to FILTER arguments. The reason it is a
// separate function is the return type: filters travel to the service as
// pointers and the only thing that carries the "not given" versus "given empty"
// distinction is nil.
//
// The returned pointer is not the ARGUMENT itself but a copy of the trimmed
// value; passing the caller's pointer through would push a value like
// " t-shirt " into the service untrimmed.
func trimmedPointer(v *string) *string {
	trimmed := trim(v)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

// intValue reads the optional integer argument; returns 0 if it was not given.
//
// For the service 0 means "apply the default"; see [queryResolver.Products].
func intValue(v *int) int {
	if v == nil {
		return 0
	}

	return *v
}

// fieldCount is the SCHEMA name of ProductList's total count field.
//
// The reason it is repeated as a string is that the generated code does not
// export this name as a Go constant; if it were removed from the schema or
// renamed, this place would silently go stale. The binding is established by
// TestProductsArgumentsMatchWhatTheServiceReads in schema_test.go: it asks
// whether the field corresponding to StoreListOptions.SkipCount really exists
// on ProductList, so silent staleness fails in a test.
const fieldCount = "count"

// isSelected reports whether name is asked for in the selection set of the
// field being executed.
//
// gqlgen's [graphql.FieldRequested] also respects the @skip/@include
// directives — that is, a client writing `count @skip(if: true)` does not count
// either.
//
// # Without a field context there is NO GUARD, and there must not be one
//
// There once was a guard here that caught a nil context and said "it was
// requested"; its rationale was "the resolver can be called without an
// executor" and that was WRONG. The only caller is the generated code and that
// code dereferences the context before it enters the resolver (generated.go:
// fc := graphql.GetFieldContext(ctx); immediately followed by fc.Args[...]),
// so a nil context already panics further up. No test calls the resolver
// directly either. The branch was unreachable; it was verified by mutation,
// flipping the answer to false failed no test.
//
// Removing it is also the SAFE option: with the guard in place, a refactor that
// loses the field context silently falls back to "count on every request"; with
// no guard the same refactor panics loudly.
func isSelected(ctx context.Context, name string) bool {
	return graphql.FieldRequested(ctx, name)
}
