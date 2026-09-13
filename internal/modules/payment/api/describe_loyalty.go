package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The schema for the loyalty point endpoints (ADR 0164).
//
// It is a file of its own for describe_storecredit.go's reason: the two
// endpoints answer one subject, and what a reader needs to be told about them —
// that the points are earned from money that moved, and that nothing but money
// moves them — does not belong in the middle of the sessions.
//
// There is no amountNote anywhere here, and its absence is the decision. That
// note says a number is in MINOR UNITS, and a point is not money: a shop decides
// what a point is worth, and telling a client that these are the smallest coin
// of a currency would be a false sentence about the one field this document
// exists to explain.

// describeLoyaltyPoints describes the two loyalty point endpoints.
func describeLoyaltyPoints(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminLoyaltyPointsBalance, openapi.Operation{
		Summary: "Reads how many loyalty points a customer holds.",
		Description: "The balance is the ledger's SUM: what captures earned less what " +
			"refunds took back. It is a COUNT and not an amount of money — what a " +
			"point is worth is the shop's decision and this installation does not " +
			"hold it. " +
			"\n\n" +
			"A customer with no rows gets zero rather than a 404: somebody who never " +
			"earned and somebody whose points were all reversed hold the same number " +
			"of points. " +
			"\n\n" +
			"Points are held PER CURRENCY, because the money they were earned from " +
			"is: a customer who paid in two currencies has two balances rather than " +
			"one sum that means nothing.",
		Parameters: []openapi.Parameter{
			{
				Name: paramCustomerID, In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Whose points are read. It is required: points belong to one named person, and a payment with no customer — a guest's — earns none.",
			},
			{
				Name: paramCurrencyCode, In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Which currency's ledger is read. It is required, and the answer repeats it in the normalized form the server used.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("The customer's point balance", d.Item(loyaltyBalanceDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminLoyaltyPoints, openapi.Operation{
		Summary: "Pages through a customer's loyalty point history.",
		Description: "The rows are returned newest first and they carry the WHY: each " +
			"one names the payment collection it was earned against, so a balance " +
			"that looks wrong can be read back to the money that produced it. " +
			"\n\n" +
			"The points field is SIGNED — an earn is positive, a reverse is negative " +
			"— so a client that sums this page's rows gets the same number the " +
			"balance endpoint answers with. " +
			"\n\n" +
			"A row is written when a capture or a refund moves what a collection has " +
			"earned, and it holds the DIFFERENCE rather than the whole: two rows for " +
			"one collection mean the money moved twice, not that anything was " +
			"counted twice.",
		Parameters: append([]openapi.Parameter{
			{
				Name: paramCustomerID, In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Whose history is read.",
			},
			{
				Name: paramCurrencyCode, In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Which currency's ledger is read.",
			},
		}, pagingParameters()...),
		Responses: map[string]any{
			"200": openapi.Response("A page of ledger rows", d.List(loyaltyEntryDTO{})),
		},
	})
}
