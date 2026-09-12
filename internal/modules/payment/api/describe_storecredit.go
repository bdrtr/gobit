package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The schema for the store-credit endpoints (ADR 0152).
//
// It is a file of its own for the same reason describe_collection.go is: the
// three endpoints answer one subject, and the descriptions that make them usable
// are long enough that folding them into describe.go would bury the sessions.

// describeStoreCredits describes the three store-credit endpoints.
func describeStoreCredits(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminStoreCredits, openapi.Operation{
		Summary: "Puts store credit on a customer's account.",
		Description: "The amount has to be positive and the reason is REQUIRED. " +
			"Taking credit back is not done by sending a negative amount: it is a " +
			"separate decision, and allowing it here would leave nothing between an " +
			"operator and a balance below zero. " +
			"\n\n" +
			"The currency is normalized to upper case and the balance is held PER " +
			"CURRENCY: credit in one currency is not credit in another, because " +
			"converting silently would hand the customer an amount other than the " +
			"one they were promised. " + amountNote,
		RequestBody: d.RequestBody(issueCreditRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The ledger row that was written",
				d.Item(storeCreditEntryDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminStoreCreditBalance, openapi.Operation{
		Summary: "Reads what a customer can spend.",
		Description: "The balance is the ledger's SUM, which is to say with open holds " +
			"already subtracted: while one of the customer's carts is waiting at the " +
			"payment step the balance looks that much lower, and that is the right " +
			"answer — the money is promised. " +
			"\n\n" +
			"A customer with no rows gets zero rather than a 404: somebody who was " +
			"never given credit and somebody who spent all of it hold the same amount " +
			"of money. " + amountNote,
		Parameters: []openapi.Parameter{
			{
				Name: "customer_id", In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Whose balance is read. It is required: store credit is one person's money.",
			},
			{
				Name: "currency_code", In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Which currency's ledger is read. It is required, and the answer repeats it in the normalized form the server used.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("The spendable balance", d.Item(storeCreditBalanceDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminStoreCredits, openapi.Operation{
		Summary: "Pages through a customer's credit history.",
		Description: "The rows are returned newest first and they carry the WHY: the " +
			"operator's question is never only how much is left. " +
			"\n\n" +
			"The amount is SIGNED — a hold is negative, an issue and the returns are " +
			"positive — so a client that sums this page's rows gets the same number " +
			"the balance endpoint answers with. " + amountNote,
		Parameters: []openapi.Parameter{
			{
				Name: "customer_id", In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Whose history is read.",
			},
			{
				Name: "currency_code", In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Which currency's ledger is read.",
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
			"200": openapi.Response("A page of ledger rows", d.List(storeCreditEntryDTO{})),
		},
	})
}
