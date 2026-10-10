package webhookout

import (
	"fmt"
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/core/webhook"
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
// # It is a package-level method on the module, and the admin bodies are unexported
//
// The admin types being described are this package's own ([createRequest],
// [deliveryListResponse] and the rest) and the schema is derived from them by
// reflection. Exporting them so the description could live elsewhere would widen
// the plugin's surface for the sake of documentation, and a plugin's types are
// not a contract anybody outside it should be building against.
//
// The one body that IS a contract is the delivery a receiver gets, and it is
// not this package's: it is [webhook.Delivery], published in core/ (ADR 0435),
// and [webhookModule.describeWebhooks] describes it from that type.
func (m *webhookModule) Describe(d *openapi.Doc) {
	m.describeReceivers(d)
	m.describeDeliveries(d)
	m.describeWebhooks(d)
}

// narrowing describes a receiver's filters and fields (ADR 0218).
const narrowing = "\"filters\" names, per topic, payload fields and the values one of which an " +
	"event has to carry in each of them, compared as text; an event that does not match is " +
	"queued for this receiver not at all. \"fields\" names, per topic, the payload fields the " +
	"receiver is sent; a topic it leaves out is sent whole, and the envelope and its " +
	"\"redacted\" list are always sent. Both name only topics the receiver takes and only " +
	"fields those topics carry, which \"topic_fields\" on the listing lists, and so does " +
	"each topic's \"data\" under this document's \"webhooks\"; a redacted field is none of " +
	"them."

// rate describes a receiver's cap (ADR 0275, ADR 0436).
const rate = "\"max_per_minute\" is how many of the receiver's due deliveries are taken for " +
	"sending in a clock minute at most, from 1 to 10000, counted across every process that sends, " +
	"oldest first; the rest wait for the next minute with no attempt counted. A delivery taken at " +
	"the end of one minute can arrive in the next, so across a minute's boundary the receiver can " +
	"be sent up to twice that many within sixty seconds. Left out or zero at registration, and " +
	"zero on a change, it takes whatever the senders can send. Deliveries keep no order: a failed " +
	"one arrives after later ones, so a receiver reads the record an event names rather than " +
	"trusting the sequence."

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
			"present to be told. The set that is accepted is \"forwarded_topics\" on the " +
			"listing, and the names under this document's \"webhooks\", which ask for no key." +
			"\n\n" + narrowing +
			"\n\n" + rate +
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

	d.Describe(http.MethodPatch, pathReceiver, openapi.Operation{
		Summary: "Changes a receiver's topics, filters, fields, description or rate.",
		Description: "A field left out of the body is kept; a field given replaces the whole " +
			"of it. The URL and the secret are not changed here: a new URL is a new receiver, " +
			"with a new secret. A topic taken away while a filter or a field list still names " +
			"it is refused, so the request that drops it sends those too. The change applies " +
			"to the events after it; the deliveries already queued are sent as they were " +
			"written." +
			"\n\n" + narrowing +
			"\n\n" + rate +
			"\n\n" +
			"Requires the " + ScopeWrite + " scope.",
		RequestBody: d.RequestBody(updateRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The receiver as it is now, without its secret",
				d.Item(endpointResponse{})),
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
				Schema: map[string]any{schemaType: "string", "enum": []string{stateDead, statePending}},
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
				"the senders that own it." +
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

// webhookTag groups the delivery descriptions apart from the admin surface.
const webhookTag = "webhooks"

// schemaType is JSON Schema's type keyword, spelled once: a typo in a map key
// compiles and says nothing, which is why core/openapi spells its own keys as
// constants too.
const schemaType = "type"

// describeWebhooks records what this plugin SENDS: one entry under the
// document's "webhooks" per forwarded topic (ADR 0435).
//
// # Every part is derived from what the sender sends with
//
// The names are [ForwardedTopics], the list the subscriptions are held to; the
// fields of each topic's data are [TopicFields], which the census reads out of
// the publishers; the headers are core/webhook's constants, which the sender
// sets; the envelope is [webhook.Delivery], which the sender marshals. Nothing
// here is a second spelling of any of them, so the document cannot go on
// describing a topic, a field or a header after the sender stops sending it.
//
// # Why the document and not Go constants
//
// A receiver runs beside a gobit of some release, often as another process
// with its own go.mod. A topic constant compiled from the release in that
// go.mod says nothing about the server the receiver talks to; the served
// document does, it asks for no key, and a receiver can check at startup that
// the topics and fields it acts on are there. It is also the promise HTTP
// paths and response fields already have.
func (m *webhookModule) describeWebhooks(d *openapi.Doc) {
	for _, topic := range ForwardedTopics {
		d.DescribeWebhook(topic, openapi.Operation{
			Summary:     "Delivers " + topic + " to a receiver registered for it.",
			Description: deliveryDescription,
			Tags:        []string{webhookTag},
			Parameters:  deliveryHeaders(topic),
			RequestBody: deliveryBody(d, topic),
			Responses:   deliveryAnswers(),
		})
	}
}

// deliveryDescription is what every delivery says about itself.
//
// The rule is spelled with core/webhook's names, so a renamed header renames
// it too; the package's documentation is the long form, and its
// VerifySignature is the check for a receiver written in Go.
const deliveryDescription = "Signed with the receiver's secret, the one its registration " +
	"returned. " + webhook.HeaderSignature + " is \"" + webhook.SignatureVersion + "=\" " +
	"followed by the standard base64 of an HMAC-SHA256 over six parts, each written as its " +
	"length in bytes, a colon and the part, with nothing between them: \"" +
	webhook.SignatureVersion + "\", then " + webhook.HeaderTimestamp + ", " +
	webhook.HeaderDelivery + ", " + webhook.HeaderEvent + " and " + webhook.HeaderAttempt +
	" as they travel, then the raw body. Read the body's bytes before parsing them, compare " +
	"the whole header in constant time, verify nothing under an empty secret (anybody can " +
	"sign under the empty key, and a registration never returns one), refuse a timestamp " +
	"more than a few minutes from now " +
	"(it is the attempt's, so a retry is signed when it is sent) and drop a delivery id " +
	"already handled. A receiver in Go imports core/webhook, whose VerifySignature is that " +
	"check and whose Delivery is this body." +
	"\n\n" +
	"\"data\" carries the topic's payload as the event carried it, every value a string, " +
	"and may carry fewer fields than are listed: a receiver's field list narrows it, and a " +
	"withheld field is named in \"redacted\" instead. A field or a topic added later is " +
	"not a breaking change, so a receiver ignores what it does not know."

// deliveryHeaders are the headers every delivery carries, the topic's own
// value pinned on the first.
func deliveryHeaders(topic string) []openapi.Parameter {
	header := func(name, description string) openapi.Parameter {
		return openapi.Parameter{
			Name: name, In: "header", Required: true,
			Schema:      textSchema(),
			Description: description,
		}
	}

	event := header(webhook.HeaderEvent, "The topic; the body's \"event\" is the same.")
	event.Schema["const"] = topic

	return []openapi.Parameter{
		event,
		header(webhook.HeaderDelivery, "The delivery id, the same on every attempt. Delivery is at "+
			"least once — an attempt whose answer was lost, or that a stopping process could not "+
			"record, is sent again — so deduplicate on it. The body's \"id\" is the same."),
		header(webhook.HeaderAttempt, "The attempt number as decimal text, 1 on the first try."),
		header(webhook.HeaderTimestamp, "The unix seconds of this attempt as decimal text."),
		header(webhook.HeaderSignature, "\""+webhook.SignatureVersion+"=\" and the MAC; the "+
			"operation's description says what it is taken over."),
	}
}

// deliveryBody is a topic's request body: the published envelope, with the
// topic's name pinned on "event" and its fields listed under "data".
//
// The envelope is the WRITTEN form of [webhook.Delivery]
// ([openapi.Doc.SchemaOf]), so its seven fields are required: gobit writes
// every one on every delivery. The data's fields are listed, none is required
// and the object is left open, for the two reasons [deliveryDescription] gives
// a receiver. Each is a string, and that is a promise a gate keeps rather than
// a habit: TestEveryPayloadValueIsAString holds every publisher's values to the
// type string.
//
// The body is built on [openapi.Response] because a request body and a
// response share their content shape; [openapi.Doc.RequestBody] would derive
// the request form, which requires nothing (ADR 0363).
func deliveryBody(d *openapi.Doc, topic string) map[string]any {
	fields := map[string]any{}
	for _, field := range TopicFields[topic] {
		fields[field] = textSchema()
	}

	body := openapi.Response("The delivery; "+webhook.HeaderSignature+" signs these bytes.",
		map[string]any{
			"allOf": []any{
				d.SchemaOf(webhook.Delivery{}),
				map[string]any{
					schemaType: "object",
					"properties": map[string]any{
						"event": map[string]any{"const": topic},
						"data":  map[string]any{schemaType: "object", "properties": fields},
					},
				},
			},
		})
	body["required"] = true

	return body
}

// textSchema is the schema of a header and of a payload field: a string, the
// bus's rule for every payload value. It is a fresh map on every call, because
// the event header's copy is then pinned to its topic.
func textSchema() map[string]any { return map[string]any{schemaType: "string"} }

// deliveryAnswers is what a receiver's answer does, spelled from the constants
// the sender and the dead-letter listing use.
func deliveryAnswers() map[string]any {
	return map[string]any{
		"2XX": map[string]any{
			"description": fmt.Sprintf("Delivered: any 2xx within %s closes the delivery, "+
				"and it is not sent again.", perAttemptTimeout),
		},
		"default": map[string]any{
			"description": fmt.Sprintf("A 307 or 308 is followed, at most %d times and only to "+
				"an address a registration would accept, and the answer at its end decides. Any "+
				"other answer, another redirect or a 4xx included, or none within %s, is a "+
				"failed attempt, retried on a doubling ladder: %d attempts over %s, then "+
				"the delivery is given up on and listed by GET %s?state=dead, to be redriven "+
				"or discarded.", maxRedirects, perAttemptTimeout, maxAttempts, deliveryWindow(), pathDeliveries),
		},
	}
}
