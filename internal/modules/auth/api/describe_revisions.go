package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// describeRevisions describes the routes that change what an earlier write
// left: withdrawing a pending invitation (ADR 0453) and revising a secret
// key's privileges (ADR 0455).
func describeRevisions(d *openapi.Doc) {
	d.Describe(http.MethodDelete, "/admin/v1/users/{id}/invitations", openapi.Operation{
		Summary: "Withdraws the user's pending invitation.",
		Description: "The link that was sent stops working at once. Withdraw one sent to the " +
			"wrong address, or one a password set some other way has made moot; a " +
			"password written through POST /admin/v1/users/{id}/password or by " +
			"accepting the invitation spends it already, in the same transaction " +
			"(ADR 0453).",
		Responses: map[string]any{
			"204": emptyResponse("The invitation was withdrawn"),
			"404": openapi.ErrorResponse(
				"No user with that identifier, or the user holds no invitation that could " +
					"still be accepted: code \"auth_invitation_not_pending\"."),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/api-keys/{id}/scopes", openapi.Operation{
		Summary: "Revises a secret key's privileges from the ones read.",
		Description: "The key's secret does not change: every deployment holding it has the " +
			"new privileges on its next request, so a privilege is added or taken " +
			"without minting and redeploying a key. The write is refused with 409 " +
			"\"auth_api_key_scopes_revised\" when the key holds, as a set, other " +
			"privileges than scopes_read by then. A privilege the caller does not hold " +
			"is refused with 403, as on creation; a secret key holds at least one, a " +
			"publishable key holds none (422 \"auth_api_key_type_mismatch\") and a " +
			"revoked key is not revised (409 \"auth_api_key_revoked\"). The audit log " +
			"records the write and its caller (ADR 0455).",
		RequestBody: d.RequestBody(reviseKeyScopesRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The key record with its new privileges", d.Item(apiKeyDTO{})),
			"404": openapi.ErrorResponse("No key with that identifier."),
			"409": openapi.ErrorResponse(
				"The privileges changed since they were read, or the key is revoked."),
		},
	})
}
