package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// amountNote is the unit warning added to the description of endpoints that
// carry an amount.
//
// The schema produces "integer"/"int64" from the type, but the TYPE on its own
// does not state the UNIT: a client developer who sees that 100.50 TL cannot be
// sent may try rounding it or dropping the kurus. Money never passes through
// floating point at any stage (plan Section 8), so the right answer is 10050,
// and this note is the only thing that says so.
//
// The note sits at the OPERATION level, not the FIELD level: the schema is
// derived from the Go type and Go fields carry no description. A per-field
// description would have meant adding a new mechanism to the core (a
// description read from a tag), and that is not a decision to be made from
// inside a single module.
const amountNote = "Amounts are MINOR UNIT integers (kurus/cent): " +
	"for 100.50 TL you send 10050; a fractional value such as 100.50 is invalid."

// Describe records payment's endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies being described are this package's UNEXPORTED DTOs
// (createSessionRequest, sessionDTO …) and the schema is derived from them by
// reflection. Exporting the types so that they could be described would mean
// widening the module's surface merely for the sake of producing a document:
// an exported type is a contract and it would become constructible from
// outside. The module's [openapi.Describer] implementation therefore delegates
// here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from the
// TYPES. Binding the method to [Handler] would say that the document DOES
// depend on the service having been built; yet the document can be produced,
// and must be producible, even when Register has never run.
//
// # Both surfaces are described
//
// The admin surface carries every stage of the payment, the store surface the
// customer's payment step. The store surface is NARROW on purpose (see the
// package doc) and the schema shows it too: there is no capture endpoint there
// and the session-opening body carries no amount.
//
// # The collection endpoints are described in describe_collection.go
//
// The four endpoints that carry a payment COLLECTION body went undescribed
// until ADR 0036: their "Collection" component collided with the product
// module's, and a collision stops the whole document from building. ADR 0036
// namespaces every component by its module, and describe_collection.go
// describes the four.
//
// # There are NO query parameters
//
// None of the endpoints described IN THIS FILE reads the query string: the
// session, capture and refund listings are not paged ([writeList] writes every
// record) and the provider listing is not filtered.
//
// Writing a parameter into the schema anyway would promise the client a feature
// that does NOT work: a client generator puts an argument on the method, the
// caller fills it, and the server ignores it in silence. The endpoints that do
// read the query string — the collection listing among them — are described in
// their own files (describe_collection.go, describe_storecredit.go,
// describe_loyalty.go).
//
// # Known limit: the "required" set of request bodies is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]), and that is the right answer for RESPONSE bodies.
// In a request body, however, "required" means a field the client MUST SEND,
// and the type cannot know that: this package's request DTOs carry no
// omitempty, so every field looks required — for example "data", which is
// optional when opening a session, is demanded too. The field NAMES and TYPES
// are right, so the schema does not invent a wrong field; it only asks for too
// much.
func Describe(d *openapi.Doc) {
	// The provider listing is bound to the same handler on BOTH surfaces but
	// is described separately: the document is written per path+method and
	// one entry does not cover the other.
	d.Describe(http.MethodGet, pathAdminProviders, openapi.Operation{
		Summary: "Lists the ids of the installed payment providers.",
		Responses: map[string]any{
			"200": openapi.Response("Provider ids", d.List([]string{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreProviders, openapi.Operation{
		Summary: "Lists the ids of the payment providers selectable in the storefront.",
		Responses: map[string]any{
			"200": openapi.Response("Provider ids", d.List([]string{})),
		},
	})

	// Store credit's three endpoints; why they live in a separate file is in
	// describe_storecredit.go (ADR 0152).
	describeStoreCredits(d)

	// Loyalty points' two read endpoints; the reason for a separate file is
	// the same (ADR 0164).
	describeLoyaltyPoints(d)
	describeJournal(d)
	describeGiftCards(d)

	describeSessions(d)
	describeCaptures(d)
	describeStore(d)

	// The four collection endpoints; why they are in a separate file and why
	// they went undescribed until ADR 0036 is in describe_collection.go.
	describeCollections(d)
}

// describeSessions describes the payment session endpoints on the admin
// surface.
func describeSessions(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminCollectionSess, openapi.Operation{
		Summary:     "Lists the collection's payment sessions.",
		Description: amountNote,
		Responses: map[string]any{
			// The list is NOT paged; the envelope is still the same (see
			// [writeList]): count is the row count and equals limit.
			"200": openapi.Response("The collection's sessions", d.List(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminCollectionSess, openapi.Operation{
		Summary: "Opens a payment session at a provider for the collection.",
		Description: amountNote + " If amount is not given, the session is opened for " +
			"the whole REMAINING amount of the collection. idempotency_key is required: " +
			"a second request with the same key does NOT open a new session, it returns " +
			"the existing one. A store_credit or loyalty_points session whose data says " +
			"\"partial\": true holds what the customer's balance has when it is smaller " +
			"than the amount, rather than declining (ADR 0269).",
		RequestBody: d.RequestBody(createSessionRequest{}),
		Responses: map[string]any{
			// The handler writes 201 (see handlers.go); a new session is
			// created.
			"201": openapi.Response("The opened payment session", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminSession, openapi.Operation{
		Summary:     "Returns a payment session by its id.",
		Description: amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The payment session", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminSessionAuthorize, openapi.Operation{
		Summary: "Authorizes the payment session and holds the amount.",
		// The provider's REFUSAL is told in the description, NOT as a
		// separate "409" entry: the error body is the core's shared envelope,
		// and the way to refer to it (the name of the "Error" component) is
		// an internal detail of the core. Repeating it here would create a
		// second entry that breaks silently the day the name changes. A
		// refusal is not a server error; it is the requested transition not
		// taking place, and the reason comes back on the session itself.
		Description: amountNote + " If the provider declines, the request returns 409 " +
			"and the reason appears in the session's decline_reason field.",
		Responses: map[string]any{
			// Authorization produces no new record, it advances the existing
			// session: the handler writes 200.
			"200": openapi.Response("The authorized session", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminSessionCapture, openapi.Operation{
		Summary: "Captures the held amount.",
		Description: amountNote + " The body may be omitted entirely; if amount is not " +
			"given or is zero, the WHOLE held amount is captured.",
		RequestBody: optionalBody(d, amountRequest{}),
		Responses: map[string]any{
			// A capture produces a SEPARATE record (payment); the handler
			// writes 201.
			"201": openapi.Response("The resulting capture record", d.Item(paymentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminSessionCancel, openapi.Operation{
		Summary: "Cancels the payment session and releases the held amount.",
		Description: "IDEMPOTENT: it returns 204 for an already canceled session " +
			"too, not an error.",
		Responses: map[string]any{
			"204": emptyResponse("Session canceled"),
		},
	})
}

// describeCaptures describes the capture and refund endpoints.
func describeCaptures(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminCollectionPays, openapi.Operation{
		Summary:     "Lists the collection's captures.",
		Description: amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The collection's captures", d.List(paymentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminPayment, openapi.Operation{
		Summary:     "Returns a capture by its id.",
		Description: amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The capture record", d.Item(paymentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminPaymentRefund, openapi.Operation{
		Summary:     "Lists the capture's refunds.",
		Description: amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The capture's refunds", d.List(refundDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminPaymentRefund, openapi.Operation{
		Summary: "Refunds a capture partly or in full.",
		Description: amountNote + " If amount is not given or is zero, the whole " +
			"REMAINING amount of the capture is refunded.",
		RequestBody: d.RequestBody(refundRequest{}),
		Responses: map[string]any{
			// A refund produces a SEPARATE record; the handler writes 201.
			"201": openapi.Response("The resulting refund record", d.Item(refundDTO{})),
		},
	})
}

// describeStore describes the store surface's payment endpoints.
//
// The surface's narrowness must show in the schema too: there is NO capture
// endpoint here and the session-opening body ([createStoreSessionRequest])
// carries no amount. Had the schema shown the admin body, a client developer
// would send a field the server rejects (see [decodeBody] — unknown fields are
// an error).
func describeStore(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathStoreCollectSess, openapi.Operation{
		Summary: "Opens a payment session on the customer's behalf for the collection's remaining amount.",
		Description: amountNote + " The amount is NOT taken from the client: the " +
			"session always covers the whole remaining amount of the collection. If " +
			"amount is sent in the body the request is rejected; provider behavior " +
			"keys inside data are not accepted either.",
		RequestBody: d.RequestBody(createStoreSessionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The opened payment session", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathStoreSessionCancel, openapi.Operation{
		Summary: "Abandons a payment session the customer opened.",
		Description: "This is how the payment METHOD is changed: an open session " +
			"takes up the collection's remaining amount. IDEMPOTENT: it returns 204 " +
			"for an already canceled session too.",
		Responses: map[string]any{
			"204": emptyResponse("Session abandoned"),
		},
	})
}

// optionalBody produces the definition of an OPTIONAL JSON request body.
//
// [openapi.Doc.RequestBody] ALWAYS marks the body required; on the capture
// endpoint that would be wrong: if no body is sent at all, the whole held
// amount is captured (see [decodeOptionalAmount]), and that is the most common
// call. Calling it required would have meant the client generator forcing the
// caller to build an empty object only because the schema says so.
//
// The schema is still derived from the TYPE; the only thing written by hand is
// the envelope's "required" flag.
func optionalBody(d *openapi.Doc, v any) map[string]any {
	return map[string]any{
		"required": false,
		"content": map[string]any{
			"application/json": map[string]any{"schema": d.SchemaOf(v)},
		},
	}
}

// emptyResponse produces the definition of a response WITHOUT a body.
//
// [openapi.Response] always writes a body schema; a 204, however, HAS NO body
// (see handlers.go, the calls that pass nil to corehttp.WriteJSON). Writing an
// empty schema would say "something comes back but its shape is unknown", and
// the client generator would produce a method that expects a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
