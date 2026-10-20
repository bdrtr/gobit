package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// transferNote is what every transfer route says about what a transfer moves
// (ADR 0450).
const transferNote = "Accepting moves the order, and every order that adds to it, to the " +
	"receiving customer: \"customer_id\" becomes theirs and the order's e-mail becomes the " +
	"address the customer module holds for them, and the order's access tokens are withdrawn, " +
	"so the links mailed to the previous holder stop opening it and the receiver opens it by " +
	"their proof. Each moved order publishes order.transferred. What the sale recorded stays: " +
	"the shipping and billing addresses, the invoice, the payment and whose balance paid it, " +
	"and the returns, claims, exchanges and reviews already written."

// transferStatusNote names the statuses a transfer answers with.
const transferStatusNote = "\"status\" is requested, accepted, declined, canceled, or expired " +
	"for a request still waiting past \"expires_at\"."

// transferAnswerNote is what the two answering routes say about their caller.
const transferAnswerNote = "The caller is whoever reads the order's mailbox: the request's " +
	"mail carried \"transfer_id\" and \"transfer_token\" in the data of the template " +
	"\"order.transfer_requested\". Send the token in the body, never in a URL. No sign-in " +
	"is asked. A token that is not the transfer's is answered as an unknown transfer. " +
	"The answer carries Cache-Control: no-store. "

// describeTransfers records the transfer routes (ADR 0450).
func describeTransfers(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathStoreTransferRequests, openapi.Operation{
		Summary: "Asks for the order to become the signed-in shopper's.",
		Description: "The request needs the order's access, as every route under the order " +
			"does, and a shopper the customer identity proves: they are the receiver. Only a " +
			"guest's order is asked for, so the access is one of its tokens; an order that has " +
			"a customer is refused, since the address typed at its checkout may not be the " +
			"account's, and an operator moves it (ADR 0459). An order takes three requests " +
			"within seven days, whatever became of them. gobit mails a " +
			"token to the order's address with the template \"order.transfer_requested\"; " +
			"whoever reads it accepts or declines at /store/v1/order-transfers/{transferId}, " +
			"within seven days. No body is read. " + storeOrderAccessNote + " " +
			transferNote + " " + transferStatusNote,
		Parameters: []openapi.Parameter{orderTokenParameter()},
		Responses: map[string]any{
			"201": openapi.Response("The request, waiting for its answer", d.Item(storeTransferDTO{})),
			"401": openapi.ErrorResponse("The request proves no customer (\"order_transfer_unproven\")."),
			"404": openapi.ErrorResponse(storeOrderRefusal),
			"409": openapi.ErrorResponse("\"order_not_transferable\" for an order that has a " +
				"customer, a canceled or erased order or one with no address to ask, " +
				"\"order_transfer_addition\" for an order that adds to another (it moves with " +
				"that one), and \"order_transfer_pending\" while another request on the order " +
				"waits."),
			"429": openapi.ErrorResponse("The order was asked for three times within seven days " +
				"(\"order_transfer_requests_spent\"); each request mailed its address."),
			"503": openapi.ErrorResponse("The token could not be mailed and the request was " +
				"withdrawn (\"order_transfer_mail_failed\")."),
		},
	})

	answerResponses := func(done string) map[string]any {
		return map[string]any{
			"200": openapi.Response(done, d.Item(storeTransferDTO{})),
			"404": openapi.ErrorResponse("No such transfer, or the token is not its " +
				"(\"order_transfer_not_found\")."),
			"422": openapi.ErrorResponse("The body is unreadable or names a field it does not have."),
		}
	}

	accept := answerResponses("The accepted transfer; accepting it again answers it as it is")
	accept["409"] = openapi.ErrorResponse("\"order_transfer_closed\" for a request declined or " +
		"withdrawn, \"order_transfer_expired\" past its bound, \"order_transfer_stale\" when " +
		"the order changed hands, address or state since it was asked for, and " +
		"\"order_transfer_receiver_unknown\" when the customer module no longer holds the " +
		"receiver, and \"order_not_transferable\" for a request written on a customer's " +
		"order before ADR 0459.")
	d.Describe(http.MethodPost, pathStoreTransferAccept, openapi.Operation{
		Summary:     "Accepts a transfer request with the token mailed for it.",
		Description: transferAnswerNote + transferNote + " " + transferStatusNote,
		RequestBody: d.RequestBody(transferAnswerRequest{}),
		Responses:   accept,
	})

	decline := answerResponses("The declined transfer; declining it again answers it as it is")
	decline["409"] = openapi.ErrorResponse("\"order_transfer_closed\" for a request accepted or " +
		"withdrawn, and \"order_transfer_expired\" past its bound.")
	d.Describe(http.MethodPost, pathStoreTransferDecline, openapi.Operation{
		Summary:     "Declines a transfer request with the token mailed for it.",
		Description: transferAnswerNote + "Nothing moves. " + transferStatusNote,
		RequestBody: d.RequestBody(transferAnswerRequest{}),
		Responses:   decline,
	})

	d.Describe(http.MethodPost, pathStoreTransferCancel, openapi.Operation{
		Summary: "Withdraws the signed-in shopper's own transfer request.",
		Description: "The request proves the customer who asked; any other caller is answered " +
			"as for an unknown transfer. Withdrawing it again answers it as it is. No body is " +
			"read. " + transferStatusNote,
		Responses: map[string]any{
			"200": openapi.Response("The withdrawn request", d.Item(storeTransferDTO{})),
			"401": openapi.ErrorResponse("The request proves no customer (\"order_transfer_unproven\")."),
			"404": openapi.ErrorResponse("No such transfer, or another customer asked for it " +
				"(\"order_transfer_not_found\")."),
			"409": openapi.ErrorResponse("\"order_transfer_closed\" for a request already answered, " +
				"and \"order_transfer_expired\" past its bound."),
		},
	})

	d.Describe(http.MethodGet, pathAdminOrderTransfers, openapi.Operation{
		Summary: "Lists the order's transfers, newest first.",
		Description: "Each transfer names who held the order before (\"from_customer_id\", " +
			"absent for a guest's order, and \"from_email\", the address a request asked, " +
			"absent once an erasure emptied it), who received or asked for it, and whether a " +
			"shopper asked or an operator moved it. " + transferStatusNote,
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Page of the order's transfers", d.List(adminTransferDTO{})),
			"404": openapi.ErrorResponse("No such order."),
		},
	})

	d.Describe(http.MethodPost, pathAdminOrderTransfers, openapi.Operation{
		Summary: "Moves the order to another customer at once.",
		Description: "\"expected_customer_id\" is the customer the order was read with, an " +
			"empty string for a guest's order, and it is required: a move drawn from a page " +
			"another move overtook is refused rather than undoing it. The move is recorded as " +
			"an accepted transfer written by an operator, closes a shopper's request still " +
			"waiting on the order, and mails nothing. " + transferNote,
		RequestBody: d.RequestBody(moveOrderRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The operator's transfer", d.Item(adminTransferDTO{})),
			"404": openapi.ErrorResponse("No such order."),
			"409": openapi.ErrorResponse("\"order_transfer_customer_revised\" when the order " +
				"holds another customer than \"expected_customer_id\", " +
				"\"order_transfer_own_order\" for the customer it already belongs to, " +
				"\"order_not_transferable\" for a canceled or erased order, " +
				"\"order_transfer_addition\" for an order that adds to another, and " +
				"\"order_transfer_receiver_unknown\" for a customer the customer module " +
				"does not hold."),
			"422": openapi.ErrorResponse("\"customer_id\" or \"expected_customer_id\" is missing " +
				"or unusable, or the body is unreadable."),
		},
	})
}
