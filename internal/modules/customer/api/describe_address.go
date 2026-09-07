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
	}{
		{
			prefix: "/admin/v1",
			audience: "This is the OPERATOR's copy of the address book: it is reached with an " +
				"admin token and can read and write any customer's addresses. ",
		},
		{
			prefix: "/store/v1",
			audience: "This is the STOREFRONT surface and it is reached with the publishable " +
				"key, which identifies the shop rather than the shopper. The customer id in " +
				"the path is therefore the only thing naming whose address book this is — " +
				"treat it as a capability and keep it off shared screens and out of URLs " +
				"that leave the browser. ",
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
			Responses: map[string]any{
				"200": openapi.Response("The customer's addresses", d.List(addressDTO{})),
			},
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
			Responses: map[string]any{
				"201": openapi.Response("The stored address", d.Item(addressDTO{})),
			},
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
			Responses: map[string]any{
				"200": openapi.Response("The updated address", d.Item(addressDTO{})),
			},
		})

		d.Describe(http.MethodDelete, single, openapi.Operation{
			Summary: "Removes an address.",
			Description: surface.audience +
				"The removal is SOFT: the row is marked rather than dropped, because an " +
				"address is printed on orders that have already shipped and a hard delete " +
				"would leave those records pointing at nothing. The address stops appearing " +
				"in the listing, which is what the caller asked for.",
			Responses: map[string]any{
				"204": emptyResponse("The address was removed"),
			},
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
				Responses: map[string]any{
					"200": openapi.Response("The address, now the default", d.Item(addressDTO{})),
				},
			})
		}
	}
}
