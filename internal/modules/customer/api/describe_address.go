package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// describeAddresses records the twelve address endpoints.
//
// # They were undescribed until 2026-09-07, and not by neglect
//
// This package's addressDTO and cart/api's want the same OpenAPI component
// name, because a component's name used to be derived from the Go type name
// alone. Two types wanting one name does not degrade one endpoint: the document
// FAILS to build and /openapi.json returns 500 for every module. Leaving these
// twelve bodiless was the right call over taking the whole schema down, and the
// note this package carried said the fix belonged in the core.
//
// It did, and ADR 0036 made it: the component name is now prefixed with the
// module being described, so this type is "CustomerAddress" and cart's is
// "CartAddress". Nothing in this package had to be renamed.
//
// # Why a separate file
//
// describe.go is one of the files still on the language ledger; ADR 0012's
// ratchet says a NEW file is English and the ledger may only shrink. Splitting
// on the file boundary keeps both true without translating a file this change
// has no reason to touch.
func describeAddresses(d *openapi.Doc) {
	for _, surface := range []struct {
		prefix   string
		audience string
		refusals map[string]any
	}{
		{
			prefix: "/admin/v1",
			audience: "This is the OPERATOR's copy of the address book: it is reached with an " +
				"admin token and can read and write any customer's addresses. ",
		},
		{
			prefix: "/store/v1",
			audience: "This is the STOREFRONT surface and it is reached with the publishable " +
				"key, which identifies the shop rather than the shopper. The customer named " +
				"in the path is therefore checked separately: the installation's own " +
				"identity is asked what this request proves, and the address book answers " +
				"only if the two agree. ",
			refusals: storefrontIdentityRefusals(),
		},
	} {
		var (
			collection = surface.prefix + "/customers/{id}/addresses"
			single     = collection + "/{address_id}"
		)

		d.Describe(http.MethodGet, collection, openapi.Operation{
			Summary: "Lists a customer's addresses.",
			Description: surface.audience +
				"The listing is not paged: an address book is small and bounded by the " +
				"person who filled it, and a page over it would be a page nobody turns. " +
				"\"count\" is the whole of it and \"limit\" is the same number, which is " +
				"what the envelope's fields mean when there is nothing to page through.",
			Responses: answers(surface.refusals, "200",
				openapi.Response("The customer's addresses", d.List(addressDTO{}))),
		})

		d.Describe(http.MethodPost, collection, openapi.Operation{
			Summary: "Adds an address to a customer.",
			Description: surface.audience +
				"The two default flags MAY be set here, and setting one has an effect " +
				"beyond this address: the customer's previous default is cleared. That is " +
				"why they can be sent on creation but NOT on an update — see the two " +
				"endpoints below, which exist so that changing a default is a request " +
				"that says what it is doing." +
				"\n\n" +
				"\"country_code\" is ISO 3166-1 alpha-2 in UPPER case.",
			RequestBody: d.RequestBody(addressRequest{}),
			Responses: answers(surface.refusals, "201",
				openapi.Response("The stored address", d.Item(addressDTO{}))),
		})

		d.Describe(http.MethodPut, single, openapi.Operation{
			Summary: "Updates an address.",
			Description: surface.audience +
				"Every field is a POINTER, and the distinction is the point: a field left " +
				"out is not touched, and a field sent as null or empty is set to that. An " +
				"update body that could not tell the two apart would erase the fields the " +
				"caller did not mention." +
				"\n\n" +
				"The default flags are DELIBERATELY absent from this body. Changing one " +
				"concerns the customer's other addresses, and a flag buried in a general " +
				"update is a side effect the caller did not ask for.",
			RequestBody: d.RequestBody(updateAddressRequest{}),
			Responses: answers(surface.refusals, "200",
				openapi.Response("The updated address", d.Item(addressDTO{}))),
		})

		d.Describe(http.MethodDelete, single, openapi.Operation{
			Summary: "Removes an address.",
			Description: surface.audience +
				"The removal is SOFT: the row is marked rather than dropped, because an " +
				"address is printed on orders that have already shipped and a hard delete " +
				"would leave those records pointing at nothing. The address stops appearing " +
				"in the listing, which is what the caller asked for.",
			Responses: answers(surface.refusals, "204",
				emptyResponse("The address was removed")),
		})

		for _, flag := range []struct {
			path, summary, what string
		}{
			{
				path:    single + "/default-shipping",
				summary: "Makes this the customer's default shipping address.",
				what:    "shipping",
			},
			{
				path:    single + "/default-billing",
				summary: "Makes this the customer's default billing address.",
				what:    "billing",
			},
		} {
			d.Describe(http.MethodPost, flag.path, openapi.Operation{
				Summary: flag.summary,
				Description: surface.audience +
					"It is an endpoint of its own rather than a field on the update body " +
					"because it touches MORE THAN this address: the customer's previous " +
					"default " + flag.what + " address is cleared by the same call. A " +
					"one-line update cannot express that, and a flag that quietly changed " +
					"another row would be a side effect nobody requested." +
					"\n\n" +
					"There is no body. The address is named by the path and there is nothing " +
					"left to choose.",
				Responses: answers(surface.refusals, "200",
					openapi.Response("The address, now the default", d.Item(addressDTO{}))),
			})
		}
	}
}

// storefrontIdentityRefusals describes the statuses the STOREFRONT copy of
// these endpoints can produce and the admin copy cannot.
//
// The core gives every operation a 401 from the path alone and a 403 only to
// the admin surface, on the sound reasoning that the storefront had no
// authorization step: the publishable key carries no scope. Since ADR 0043 the
// storefront routes naming a customer do have one, so the codes are described
// HERE, by the package that knows the rule, rather than by widening a core
// default onto every store route that still cannot produce them.
//
// Each of these REPLACES the core's generic sentence for that status (a
// hand-given code wins), and the difference is worth the words: "authentication
// is missing or invalid" sends the reader to check the publishable key, which
// is the one credential that was accepted.
//
// # Why FOUR statuses and not the two this started with
//
// Because [Handler.storeCustomerID] passes the bound identity's error through
// UNWRAPPED, and that is the decision rather than an accident: the embedder
// picks the status by picking its error's kind. So the status set of these
// endpoints is not this package's to enumerate — an expired session is the
// embedder's 401, a suspended account its 403, an unreachable identity provider
// its 503, and any untyped error a 500. Describing only the two codes this
// package itself returns would have documented half the surface and left the
// other half to be discovered in production. What is enumerable, and what a
// client is entitled to branch on, is the three CODES the core publishes; the
// rest is named as the embedder's and not guessed at.
func storefrontIdentityRefusals() map[string]any {
	return map[string]any{
		"401": openapi.ErrorResponse(
			"The publishable key was accepted, but the customer named in the path is not " +
				"proven. Either this installation has bound no customer identity at all — " +
				"code \"identity_not_bound\", and every one of these endpoints refuses " +
				"until it binds one — or the identity it bound refused this request with " +
				"an error of its own that asks the shopper to sign in, and the code is the " +
				"embedder's."),
		"403": openapi.ErrorResponse(
			"Either the request proves a DIFFERENT customer than the one named in the " +
				"path — code \"identity_mismatch\", which does not depend on whether the " +
				"named customer exists, because the comparison is between the path and the " +
				"proof and no record is read to make it — or the bound identity refused " +
				"this request with a forbidding error of its own (a suspended account, " +
				"say), and the code is the embedder's."),
		"500": openapi.ErrorResponse(
			"Either the bound customer identity returned neither an identifier nor an " +
				"error — code \"identity_unproven\", which is a fault in the " +
				"installation's implementation and not in this request, so no change the " +
				"client makes will alter the answer — or something else failed on the " +
				"server."),
		"503": openapi.ErrorResponse(
			"The bound customer identity could not answer: its own dependency, such as " +
				"the identity provider it calls, is unreachable. The code is the " +
				"embedder's and the request is worth retrying."),
	}
}

// answers merges an operation's success response with the refusals its surface
// can produce.
//
// The success code is passed separately because it differs per endpoint while
// the refusals do not: an identity that cannot be proven refuses a listing and
// a deletion the same way, and writing the pair out twelve times would be
// twelve chances for one of them to fall behind.
func answers(refusals map[string]any, code string, response any) map[string]any {
	out := map[string]any{code: response}
	for status, refusal := range refusals {
		out[status] = refusal
	}

	return out
}
