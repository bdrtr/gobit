package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names used by the query parameters below.
//
// The core's counterparts are unexported and repeating them here is not a cost
// but a guard against SILENCE: a type name written as "strig" compiles, the
// document is produced, and it surfaces only when a generated client sends the
// parameter with the wrong type. The same three constants exist in review/api
// for the same reason.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
	// inQuery is where the parameters below are read from. It is a constant
	// because the linter counts four literals and is right to: a fifth spelled
	// "quey" would compile, produce a document, and only surface when a
	// generated client stopped sending the filter.
	inQuery = "query"
)

// describeCollections records the four payment-collection endpoints.
//
// # They were undescribed until 2026-09-07, and not by neglect
//
// [collectionDTO] and [createCollectionRequest] wanted the component names
// "Collection" and "CreateCollectionRequest", which the product module's
// collection types already held. Two types wanting one name does not degrade one
// endpoint: the document FAILS to build and /openapi.json returns 500 for every
// module, so leaving these four bodiless was the cheaper mistake and the note
// this package carried said the fix belonged in the core.
//
// It did. ADR 0036 prefixes a component name with the module being described, so
// these are "PaymentCollection" and "PaymentCreateCollectionRequest" while
// product keeps "ProductCollection". No type in this package was renamed.
//
// # Why a separate file
//
// describe.go is on the language ledger and ADR 0012's ratchet says a new file
// is English. Splitting on the file boundary keeps both true without translating
// a file this change has no reason to touch.
func describeCollections(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminCollections, openapi.Operation{
		Summary: "Opens a payment collection for an order or a cart.",
		Description: "A collection is the AMOUNT OWED, and the sessions and captures that " +
			"answer it hang beneath it. It is opened before any provider is chosen, which " +
			"is why this body carries no provider: what is being recorded is the debt, not " +
			"how it will be settled." +
			"\n\n" +
			"\"reference\" is the caller's own identifier for what is being paid for — an " +
			"order id in practice. It is how a second request finds the collection instead " +
			"of opening another one for the same debt." +
			"\n\n" +
			"\"amount\" is a POINTER in this body and the reason is a distinction the type " +
			"would otherwise lose: a missing amount and an amount of zero are both refused, " +
			"and they are refused with DIFFERENT messages. A collection for nothing is a " +
			"mistake worth naming rather than a debt of zero. " + amountNote,
		RequestBody: d.RequestBody(createCollectionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The opened collection", d.Item(collectionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminCollections, openapi.Operation{
		Summary: "Lists payment collections.",
		Description: "This is the ONLY endpoint in this module that reads the query string, " +
			"and the parameters below are exactly the ones the handler reads — no more. " +
			"A parameter written into the schema that the server ignores is worse than a " +
			"missing one: the generated client puts an argument on the method, the caller " +
			"fills it in, and the filtering silently does not happen." +
			"\n\n" +
			"The three amount fields say where a collection stands without a second " +
			"request: authorized is what is held, captured is what has moved, refunded is " +
			"what has gone back. " + amountNote,
		Parameters: []openapi.Parameter{
			{
				Name: "reference", In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Limits the listing to one caller reference. It is what the CALLER wrote and this module never validates it; the checkout writes the CART's identifier there, not the order's. An order is reached from a collection over the order_payment link instead.",
			},
			{
				Name: "status", In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Limits the listing to one collection status.",
			},
			{
				Name: "limit", In: inQuery,
				Schema:      map[string]any{schemaType: typeInteger},
				Description: "Page size; the service's default applies when it is not given.",
			},
			{
				Name: "offset", In: inQuery,
				Schema:      map[string]any{schemaType: typeInteger},
				Description: "Number of records to skip.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of collections", d.List(collectionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminCollection, openapi.Operation{
		Summary: "Reads one payment collection.",
		Description: "The operator's view of a debt and what has been done about it. " +
			amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The collection", d.Item(collectionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreCollection, openapi.Operation{
		Summary: "Reads a payment collection from the storefront.",
		Description: "The SAME record the admin surface returns, and that is deliberate: " +
			"the fields a shopper's checkout needs to decide whether the payment step is " +
			"finished are the fields an operator needs to decide the same thing, and two " +
			"shapes for one record would give a client two classes for one concept." +
			"\n\n" +
			"The storefront reaches it with the publishable key, which identifies the shop " +
			"rather than the shopper, so the collection id in the path is the only thing " +
			"naming whose debt this is — treat it as a capability. " + amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The collection", d.Item(collectionDTO{})),
		},
	})
}
