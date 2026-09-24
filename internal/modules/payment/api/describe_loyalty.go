package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The schema for the loyalty point endpoints (ADR 0164, ADR 0165).
//
// It is a file of its own for describe_storecredit.go's reason: the two
// endpoints answer one subject, and what a reader needs to be told about them —
// that the points are earned from money that moved, and spent as a tender —
// does not belong in the middle of the sessions.
//
// A point is worth ONE MINOR UNIT of the currency it was earned in (ADR 0165):
// the earn rate reads as the cashback in basis points, and the loyalty_points
// tender spends a balance of points against an amount of money one for one.
// The balance is therefore described in the ledger's unit, which IS the
// currency's smallest coin — the sentence ADR 0164 declined to say while the
// installation held no number to say it with.

// describeLoyaltyPoints describes the two loyalty point endpoints.
func describeLoyaltyPoints(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminLoyaltyPointsBalance, openapi.Operation{
		Summary: "Reads how many loyalty points a customer holds.",
		Description: "The balance is the ledger's SUM: what captures earned, less what " +
			"refunds took back, less what the loyalty_points tender holds or spent, plus " +
			"what it released or refunded. A point is worth ONE MINOR UNIT of the " +
			"currency, so the number reads as an amount of that currency's smallest " +
			"coin, and it can be NEGATIVE: a refund reverses points the customer may " +
			"have spent already, and the next earn fills the hole first. " +
			"\n\n" +
			"A customer with no rows gets zero rather than a 404: somebody who never " +
			"earned and somebody whose points were all reversed hold the same number " +
			"of points. " +
			"\n\n" +
			"Points are held PER CURRENCY, because the money they were earned from " +
			"is: a customer who paid in two currencies has two balances rather than " +
			"one sum that means nothing. " + amountNote,
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
		Description: "The rows are returned newest first and they carry the WHY in " +
			"their reference: an earn or a reverse names the payment collection it " +
			"was earned against, so a balance that looks wrong can be read back to " +
			"the money that produced it; a hold, a release or a refund names the " +
			"loyalty_points tender's own session it was spent through. " +
			"\n\n" +
			"The points field is SIGNED — an earn, a release and a refund are " +
			"positive, a reverse and a hold are negative — so a client that sums " +
			"this page's rows gets the same number the balance endpoint answers " +
			"with. " +
			"\n\n" +
			"An earn or a reverse is written when a capture or a refund moves what a " +
			"collection has earned, and it holds the DIFFERENCE rather than the " +
			"whole: two rows for one collection mean the money moved twice, not that " +
			"anything was counted twice. The tender writes a hold when a session is " +
			"authorized, a release when it is canceled or captured for less than it " +
			"held, and a refund when it is refunded. A point is one minor unit of the " +
			"currency: " + amountNote,
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
