package paymentpaytr

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// Describe records the plugin's operator endpoint in the OpenAPI document.
//
// The module a plugin brings enters the same registry as the ones in the box,
// so the composition root's type assertion finds this method with no special
// case. The callback is not described here: it is PayTR's protocol rather than
// gobit's, answered with a bare token, and the callback ring binds it outside
// the described surfaces (ADR 0028).
//
// The endpoint went undescribed while the plugin was never on the end-to-end
// ground, so the audit that asks never saw it (ADR 0438).
func (m *paytrModule) Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, PendingPath, openapi.Operation{
		Summary: "Lists the PayTR payments PayTR never reported on.",
		Description: "A payment page that was opened and that PayTR posted no outcome for, " +
			"for more than an hour, oldest first and at most two hundred. Most are a customer " +
			"who opened the page and did not pay; the others are a callback that never reached " +
			"this server, which is what a callback address set wrongly in PayTR's merchant " +
			"panel looks like, while the money is taken. A payment PayTR reported as paid and " +
			"that no order took is not listed here: the payment module's reconciliation reports " +
			"it from its pending session (ADR 0438). The list is under data; pending carries the " +
			"same list under the name it had before it was described and is deprecated. Requires " +
			"the " + ScopeRead + " scope.",
		Responses: map[string]any{
			"200": openapi.Response("The payments PayTR never reported on", d.SchemaOf(pendingResponse{})),
		},
	})
}
