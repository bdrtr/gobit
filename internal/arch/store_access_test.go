package arch_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/plugins/webhookout"
)

// This file holds ADR 0439: an id gobit hands out opens nothing at the
// storefront.
//
// The two halves are one rule seen from both ends. The order routes take the
// order id gobit forwards to every order topic's receiver, so each has to put
// the caller to the order's own decision; and a field webhook-out forwards may
// not be the id a storefront route answers on alone, which is the cart's id
// until it was withheld and the payment collection's until its routes went.

// orderAccessModule and orderAccessFunction are where the order module
// decides a storefront caller's access.
const (
	orderAccessModule   = "order"
	orderAccessFunction = "storeOrderAccess"
)

// storeOrderPath is the storefront path of one order; every route at or under
// it names that order.
const storeOrderPath = "/store/v1/orders/{id}"

// TestEveryStorefrontOrderRouteDecidesItsAccess holds the order half: a route
// under /store/v1/orders/{id} reaches the order module's access decision,
// directly or through its own package's helpers.
//
// The population is the router's registrations under the path, which a
// handler that stopped asking keeps; the subject named below is the read the
// defect was found on, so a scan that went blind fails here first.
func TestEveryStorefrontOrderRouteDecidesItsAccess(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)
	require.Contains(t, surface.declared, orderAccessModule+"."+orderAccessFunction,
		"the order module's api package declares no %s; the decision was renamed or moved, "+
			"and this gate would pass whatever the routes believe", orderAccessFunction)
	reaching := functionsReachingWithin(surface.calls, orderAccessModule, orderAccessFunction)

	var named []storefrontRoute
	for _, route := range slices.Concat(surface.reads, surface.writes) {
		if route.path == storeOrderPath || strings.HasPrefix(route.path, storeOrderPath+"/") {
			named = append(named, route)
		}
	}
	require.True(t, slices.ContainsFunc(named, func(route storefrontRoute) bool {
		return route.verb == "GET" && route.path == storeOrderPath
	}), "GET %s was not resolved among the storefront routes; the route scan has gone BLIND", storeOrderPath)

	for _, route := range named {
		if route.module == orderAccessModule && slices.Contains(reaching, route.key()) {
			continue
		}

		t.Errorf("%s %s names an order and never reaches %s.\n"+
			"ADR 0439: gobit sends an order's id to every webhook receiver of an order "+
			"topic, so the id opens nothing; a route on an order answers one of its access "+
			"tokens or its customer's proof, through the order module's one decision.",
			route.verb, route.path, orderAccessFunction)
	}
}

// cartGateModule and cartGateFunction are where the cart module refuses a
// storefront request on a completed cart.
const (
	cartGateModule   = "cart"
	cartGateFunction = "completedCartRefused"
)

// storeCartPath is the storefront path of one cart.
const storeCartPath = "/store/v1/carts/{id}"

// TestEveryStorefrontCartRouteRefusesACompletedCart holds the cart half: a
// storefront route at or under /store/v1/carts/{id} reaches the cart module's
// completed-cart refusal, so a completed cart answers every one the unknown
// cart's 404. The subject named below is the read the review found answering
// 200 after the cart's own read was closed.
func TestEveryStorefrontCartRouteRefusesACompletedCart(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)
	require.Contains(t, surface.declared, cartGateModule+"."+cartGateFunction,
		"the cart module's api package declares no %s; the refusal was renamed or moved", cartGateFunction)
	reaching := functionsReachingWithin(surface.calls, cartGateModule, cartGateFunction)

	var named []storefrontRoute
	for _, route := range slices.Concat(surface.reads, surface.writes) {
		if route.path == storeCartPath || strings.HasPrefix(route.path, storeCartPath+"/") {
			named = append(named, route)
		}
	}
	require.True(t, slices.ContainsFunc(named, func(route storefrontRoute) bool {
		return route.verb == "GET" && route.path == storeCartPath+"/shipping-options"
	}), "GET %s/shipping-options was not resolved; the route scan has gone BLIND", storeCartPath)

	for _, route := range named {
		if route.module == cartGateModule && slices.Contains(reaching, route.key()) {
			continue
		}

		t.Errorf("%s %s acts on a cart and never reaches %s.\n"+
			"ADR 0439: a completed cart's addresses and customer are the order's, and the "+
			"storefront answers every route on it the 404 an unknown cart gets.",
			route.verb, route.path, cartGateFunction)
	}
}

// publicStorefrontIDs are the forwarded fields a storefront route may take as
// its only key, because what they open is public by design.
var publicStorefrontIDs = map[string]string{
	"product_id": "a product is the catalog's: the storefront shows it to every visitor, and " +
		"its id opens what any of them reads",
	"region_id": "a region is a market the shop sells in, which GET /store/v1/regions lists to " +
		"every visitor",
}

// forwardedAliases are the forwarded fields that carry another record's id
// under a name of their own.
var forwardedAliases = map[string][]string{
	// A parcel's reference is what it was opened for: the order, or the
	// return a return parcel brings back (fulfillment's EventFieldReference).
	"reference": {"order_id", "return_id"},
}

// TestNoForwardedFieldIsAStorefrontKey holds the webhook half: no field
// webhook-out sends a receiver is the id a storefront route answers on with
// no proof (ADR 0439).
//
// A route's key is its first path parameter: a named one is its own field
// ({customer_id}), and "{id}" is the resource before it made singular
// (/store/v1/carts/{id} is cart_id). A route proves when it reaches the
// customer comparison or the order's access decision. The cart is the worked
// example — its routes prove nothing by design, so its id is withheld — and
// the scan has to find it, or it has gone blind.
func TestNoForwardedFieldIsAStorefrontKey(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)
	proving := slices.Concat(functionsReachingTheProof(surface.calls),
		functionsReachingWithin(surface.calls, orderAccessModule, orderAccessFunction))

	unproven := map[string][]storefrontRoute{}
	for _, route := range slices.Concat(surface.reads, surface.writes) {
		field := storefrontKey(route.path)
		if field == "" || slices.Contains(proving, route.key()) {
			continue
		}
		unproven[field] = append(unproven[field], route)
	}
	require.NotEmpty(t, unproven["cart_id"],
		"no storefront route keyed on a cart id was found unproven, which cannot be true while "+
			"the cart's id is its access; the key scan has gone BLIND")

	for field, reason := range publicStorefrontIDs {
		assert.NotEmptyf(t, unproven[field],
			"publicStorefrontIDs excuses %q (%s), which no unproven storefront route takes any more; "+
				"delete the entry", field, reason)
	}

	for _, topic := range webhookout.ForwardedTopics {
		for _, field := range webhookout.TopicFields[topic] {
			if _, public := publicStorefrontIDs[field]; public {
				continue
			}
			for _, carried := range append([]string{field}, forwardedAliases[field]...) {
				for _, route := range unproven[carried] {
					t.Errorf("webhook-out forwards %s's %q, and %s %s answers on a %s with no proof.\n"+
						"ADR 0439: an id gobit hands a receiver is no storefront access. Withhold "+
						"the field in redactedFields, or put the route to the customer's proof "+
						"or the record's own access decision.",
						topic, field, route.verb, route.path, carried)
				}
			}
		}
	}
}

// storefrontKey is the field a storefront path's first parameter names, or ""
// for a path that takes none.
func storefrontKey(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/store/v1/"), "/")
	for i, segment := range segments {
		if !strings.HasPrefix(segment, "{") {
			continue
		}
		param := strings.Trim(segment, "{}")
		if param != "id" || i == 0 {
			return param
		}

		return strings.ReplaceAll(strings.TrimSuffix(segments[i-1], "s"), "-", "_") + "_id"
	}

	return ""
}

// TestTheStorefrontKeyIsTheResourcesID pins the derivation the gate above
// reads every route through.
func TestTheStorefrontKeyIsTheResourcesID(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"/store/v1/carts/{id}":                                      "cart_id",
		"/store/v1/carts/{id}/line-items/{line_item_id}":            "cart_id",
		"/store/v1/orders/{id}/timeline":                            "order_id",
		"/store/v1/payment-collections/{id}":                        "payment_collection_id",
		"/store/v1/b2b/customers/{customer_id}/company":             "customer_id",
		"/store/v1/sales-channels/{sales_channel_id}/products/{id}": "sales_channel_id",
		"/store/v1/payment-providers":                               "",
		"/store/v1/carts":                                           "",
	} {
		assert.Equal(t, want, storefrontKey(path), path)
	}
}

// functionsReachingWithin returns every function of module that reaches the
// module's own function, directly or through the module's helpers, the
// function itself included.
//
// It is [functionsReachingTheProof] seeded with a function of one module
// rather than a core one: another module's helper of the same name is not this
// module's decision, so the walk never leaves the module.
func functionsReachingWithin(calls map[string][]string, module, function string) []string {
	reaching := map[string]bool{module + "." + function: true}

	for grew := true; grew; {
		grew = false
		for caller, callees := range calls {
			if reaching[caller] || !strings.HasPrefix(caller, module+".") {
				continue
			}
			for _, callee := range callees {
				if reaching[module+"."+callee] {
					reaching[caller] = true
					grew = true

					break
				}
			}
		}
	}

	out := make([]string, 0, len(reaching))
	for caller := range reaching {
		out = append(out, caller)
	}
	slices.Sort(out)

	return out
}
