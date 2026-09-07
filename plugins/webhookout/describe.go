package webhookout

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The paths this module describes, spelled as chi binds them.
//
// The trailing slash on the collection is NOT a typo and not a style choice: the
// routes are registered inside r.Route("/admin/v1/webhooks", …) with "/" as the
// child pattern, so the pattern chi reports — and the one a description has to
// match — carries it. A description that spelled the tidier "/admin/v1/webhooks"
// would match nothing, and the core would report it through
// [openapi.Doc.UnmatchedDescriptions] rather than fail, which is a warning
// somebody has to be reading.
const (
	pathReceivers  = "/admin/v1/webhooks/"
	pathReceiver   = "/admin/v1/webhooks/{id}"
	pathDeliveries = "/admin/v1/webhooks/deliveries"
	pathRedrive    = "/admin/v1/webhooks/deliveries/{id}/redrive"
	pathDiscard    = "/admin/v1/webhooks/deliveries/{id}/discard"
)

// Describe records this plugin's operator surface in the OpenAPI document.
//
// # A plugin describes its endpoints the same way a module does
//
// The module a plugin brings enters the same registry as the ones in the box, so
// the composition root's type assertion finds this method with no special case.
// Nothing ever stopped this plugin from having one; until 2026-09-07 its six
// endpoints simply had no description and nothing in the repository could say
// so — see ADR 0035 and the ledger the audit there reads.
//
// # It is a package-level method on the module, and the bodies are unexported
//
// The types being described are this package's own ([createRequest],
// [deliveryListResponse] and the rest) and the schema is derived from them by
// reflection. Exporting them so the description could live elsewhere would widen
// the plugin's surface for the sake of documentation, and a plugin's types are
// not a contract anybody outside it should be building against.
func (m *webhookModule) Describe(d *openapi.Doc) {
	m.describeReceivers(d)
	m.describeDeliveries(d)
}

// describeReceivers records the registration surface.
func (m *webhookModule) describeReceivers(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathReceivers, openapi.Operation{
		Summary: "Registers a receiver and returns its signing secret ONCE.",
		Description: "The secret comes back in this response and in NO OTHER — not in the " +
			"listing, not from any other endpoint. It cannot be hashed, because a signature " +
			"has to be computed with it on every delivery attempt, so the only thing between " +
			"it and a screen recording is that nothing else ever returns it. Store it now; " +
			"the way to recover from losing it is to delete the receiver and register it " +
			"again. The response says so in a field of its own rather than only here, " +
			"because a field is read and a paragraph is not." +
			"\n\n" +
			"A topic gobit does not publish is REFUSED at registration rather than ignored " +
			"at delivery. A receiver subscribed to a name nothing emits is a subscription " +
			"that silently never fires, and this request is the only moment somebody is " +
			"present to be told. Read \"forwarded_topics\" on the listing for the set that " +
			"is accepted." +
			"\n\n" +
			"Requires the " + ScopeWrite + " scope.",
		RequestBody: d.RequestBody(createRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The registered receiver AND its secret, the only time",
				d.Item(createResponse{})),
		},
	})

	d.Describe(http.MethodGet, pathReceivers, openapi.Operation{
		Summary: "Lists the registered receivers, without their secrets.",
		Description: "The set of topics gobit forwards travels WITH the listing, under " +
			"\"forwarded_topics\". It is the answer to the question that brings an operator " +
			"here — why a receiver is not getting something — and reading it beside the " +
			"receivers is cheaper than finding it in a changelog." +
			"\n\n" +
			"The listing carries no paging: the number of receivers a shop registers is " +
			"small and bounded by hand, and a page over it would be a page nobody turns." +
			"\n\n" +
			"Requires the " + ScopeRead + " scope.",
		Responses: map[string]any{
			"200": openapi.Response("The receivers and the topics that can be asked for",
				d.SchemaOf(endpointListResponse{})),
		},
	})

	d.Describe(http.MethodDelete, pathReceiver, openapi.Operation{
		Summary: "Removes a receiver.",
		Description: "The deliveries already OWED to it are left alone, and a deleted " +
			"receiver with a dead delivery still appears in the delivery listing. What the " +
			"deletion stops is new deliveries being enqueued, which is what an operator " +
			"means when a receiver has gone away; discarding the pile at the same time " +
			"would destroy the record of what was never delivered." +
			"\n\n" +
			"Requires the " + ScopeWrite + " scope.",
		Responses: map[string]any{
			"200": openapi.Response("The removed receiver's id", d.Item(deletedResponse{})),
		},
	})
}

// describeDeliveries records the dead-letter surface.
func (m *webhookModule) describeDeliveries(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathDeliveries, openapi.Operation{
		Summary: "Shows what was given up on, or what is still owed.",
		Description: "\"state\" chooses between two different questions and the answer's " +
			"SHAPE changes with it. Both carry \"data\", \"count\" and \"state\"." +
			"\n\n" +
			"\"dead\" also carries \"total\" — the WHOLE pile rather than this page, because " +
			"it is the number that decides whether anybody is woken up and a count of what " +
			"fitted would read the same during an incident of any size. A total of ZERO is " +
			"reported rather than omitted: it is how an operator working through a pile " +
			"learns they are finished. Beside it come \"attempts_allowed\", \"retry_window\" " +
			"and \"exits\", the two routes out of the pile." +
			"\n\n" +
			"\"pending\" carries none of those, and the absence is the point: nothing has " +
			"been given up on, so there is no ceiling to report and no exit to offer." +
			"\n\n" +
			"Requires the " + ScopeRead + " scope.",
		Parameters: []openapi.Parameter{
			{
				Name: "state", In: "query",
				Description: "\"dead\" for the pile a human has to look at, \"pending\" for " +
					"what is still being retried. Any other value is refused.",
				Schema: map[string]any{"type": "string", "enum": []string{stateDead, statePending}},
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("The deliveries in the requested state",
				d.SchemaOf(deliveryListResponse{})),
		},
	})

	for _, exit := range []struct {
		path, summary, description string
	}{
		{
			path:    pathRedrive,
			summary: "Puts a dead delivery back in the queue.",
			description: "The attempt counter is reset and the delivery is owed again. Use " +
				"it once the receiver is known to be back; redriving into an outage buys " +
				"the same ladder of retries a second time and puts the delivery back on " +
				"this pile.",
		},
		{
			path:    pathDiscard,
			summary: "Gives up on a dead delivery for good.",
			description: "The row is removed and nothing will be sent for that event again. " +
				"It is the honest end for a delivery whose receiver no longer exists; there " +
				"is no way back from it, which is why it is a separate call from redriving " +
				"rather than a flag on one.",
		},
	} {
		d.Describe(http.MethodPost, exit.path, openapi.Operation{
			Summary: exit.summary,
			Description: exit.description +
				"\n\n" +
				"A delivery that is still being RETRIED cannot be acted on here: the pile is " +
				"what has been given up on, and reaching into the retry ladder would race " +
				"the job that owns it." +
				"\n\n" +
				"\"remaining\" is what is left of the pile afterwards, and " +
				"\"job_alarm_clears\" says whether this action was the one that silenced the " +
				"operator alarm. The second is reported rather than left to be inferred " +
				"from the first: the alarm also watches orphaned rows, so an empty pile is " +
				"not on its own a quiet alarm." +
				"\n\n" +
				"Requires the " + ScopeWrite + " scope.",
			Responses: map[string]any{
				"200": openapi.Response("What is left of the pile, and whether the alarm clears",
					d.Item(deliveryActionResponse{})),
			},
		})
	}
}
