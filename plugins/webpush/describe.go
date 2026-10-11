package webpush

import (
	"net/http"
	"strconv"

	"github.com/bdrtr/gobit/core/openapi"
)

// The paths this module describes, spelled as chi reports the patterns
// [webpushModule.Routes] binds under its two prefixes.
const (
	pathKey           = "/store/v1/webpush/vapid-key"
	pathSubscribe     = "/store/v1/webpush/subscribe"
	pathUnsubscribe   = "/store/v1/webpush/unsubscribe"
	pathUnbind        = "/store/v1/webpush/unbind"
	pathSubscriptions = "/admin/v1/webpush/subscriptions"
	pathSubscription  = "/admin/v1/webpush/subscriptions/{id}"
	pathBroadcast     = "/admin/v1/webpush/broadcast"
)

// The JSON Schema words the parameters below are written with.
const (
	schemaType = "type"
	typeString = "string"
	inQuery    = "query"
)

// deprecatedTopLevel ends the description of every answer that carries its
// values twice.
const deprecatedTopLevel = " The same values stand at the top level of the answer, where they " +
	"were before the plugin was described; they are deprecated, and a client reads them under data."

// emptyResponse describes a 204: a description and no content, since an empty
// content would say the shape is unknown rather than that there is none.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}

// Describe records the plugin's seven endpoints in the OpenAPI document.
//
// The module a plugin brings enters the same registry as the ones in the box,
// so the composition root's type assertion finds this method with no special
// case. The endpoints went undescribed while the plugin was never on the
// end-to-end ground, so the audit that asks never saw them (D291).
func (m *webpushModule) Describe(d *openapi.Doc) {
	m.describeStore(d)
	m.describeAdmin(d)
}

// describeStore records the storefront's four endpoints.
func (m *webpushModule) describeStore(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathKey, openapi.Operation{
		Summary: "Answers the key a browser subscribes to pushes with.",
		Description: "A storefront passes data.public_key to PushManager.subscribe() as its " +
			"applicationServerKey. The key is derived from the signing key the plugin is " +
			"configured with, so it cannot disagree with the key the pushes are signed with; a " +
			"device subscribed under a key the server no longer holds receives nothing until it " +
			"subscribes again." + deprecatedTopLevel,
		Responses: map[string]any{
			"200": openapi.Response("The application server key", d.SchemaOf(keyResponse{})),
		},
	})

	d.Describe(http.MethodPost, pathSubscribe, openapi.Operation{
		Summary: "Stores a browser's push subscription.",
		Description: "The body is what the browser's PushSubscription.toJSON() returns, the " +
			"endpoint and keys.p256dh and keys.auth, with customer_id and locale beside it; a " +
			"field the request does not name is refused. The keys are checked here: a p256dh " +
			"that is not a P-256 point, or an auth that is not " + strconv.Itoa(authSecretLength) +
			" bytes, is refused, because a device stored with them could never be sent to." +
			"\n\n" +
			"An endpoint already stored keeps its row: its keys are replaced, and its customer " +
			"and locale are kept unless the request names new ones. An empty customer_id never " +
			"clears a binding; " + pathUnbind + " does." +
			"\n\n" +
			"customer_id is the storefront's word for who is signed in, and it is believed, not " +
			"proven: the device receives that customer's order confirmations. An operator " +
			"removes a wrong binding with DELETE " + pathSubscription + ". locale is a language " +
			"tag such as tr or en-gb and chooses the device's template." +
			"\n\n" +
			"The answer names the stored device and not its endpoint, because whoever holds an " +
			"endpoint can push to that browser." + deprecatedTopLevel,
		RequestBody: d.RequestBody(subscribeRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The stored device", d.SchemaOf(subscribedResponse{})),
		},
	})

	d.Describe(http.MethodPost, pathUnsubscribe, openapi.Operation{
		Summary: "Removes a browser's push subscription.",
		Description: "The endpoint is the proof: whoever holds it can already push to that " +
			"browser, so asking more to remove it would protect nothing. An endpoint that is " +
			"not stored is answered the same, so a browser that unsubscribes twice is not told " +
			"it failed.",
		RequestBody: d.RequestBody(endpointRequest{}),
		Responses: map[string]any{
			"204": emptyResponse("The device is not stored"),
		},
	})

	d.Describe(http.MethodPost, pathUnbind, openapi.Operation{
		Summary: "Unbinds a browser from its customer and keeps the subscription.",
		Description: "What a sign-out calls: the device stays subscribed and receives no " +
			"customer's order confirmation until a subscription names a customer again. It is " +
			"a POST so that navigator.sendBeacon can send it. An endpoint that is not stored " +
			"is answered the same.",
		RequestBody: d.RequestBody(endpointRequest{}),
		Responses: map[string]any{
			"204": emptyResponse("The device is bound to no customer"),
		},
	})
}

// describeAdmin records the operator's three endpoints.
func (m *webpushModule) describeAdmin(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathSubscriptions, openapi.Operation{
		Summary: "Lists the stored devices, without their endpoints.",
		Description: "With customer_id, every device claimed for that customer, oldest first. " +
			"Without it, every device in id order, " + strconv.Itoa(broadcastPageLimit) +
			" at most; the next page is asked for with after set to the last id of this one, " +
			"and an empty page is the end. vapid_fingerprint names the signing key a device " +
			"subscribed under, and a device under another key than the server's own can no " +
			"longer be reached. The list stands under data; subscriptions carries the same " +
			"list under the name it had before the plugin was described and is deprecated. " +
			"Requires the " + ScopeRead + " scope.",
		Parameters: []openapi.Parameter{
			{
				Name: "customer_id", In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Lists the devices claimed for this customer, every one of them.",
			},
			{
				Name: "after", In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "The last id of the page before; ignored with customer_id.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("The devices", d.SchemaOf(deviceListResponse{})),
		},
	})

	d.Describe(http.MethodDelete, pathSubscription, openapi.Operation{
		Summary: "Removes a stored device.",
		Description: "The way an operator undoes a binding the storefront claimed for the " +
			"wrong customer: the device receives nothing more until its browser subscribes " +
			"again. An id that is not stored is answered the same. Requires the " +
			ScopeWrite + " scope.",
		Responses: map[string]any{
			"204": emptyResponse("The device is not stored"),
		},
	})

	d.Describe(http.MethodPost, pathBroadcast, openapi.Operation{
		Summary: "Pushes one message to every stored device.",
		Description: "event names a template the plugin loaded, and data fills it; each " +
			"device is sent its own locale's rendering, or the default. The template is " +
			"rendered once before any device is read, so an unknown event or a message too " +
			"large to push is refused with nothing sent. The answer counts what happened, " +
			"because a push to many devices has no single outcome: a device the push service " +
			"calls gone, or one subscribed under another key, is deleted and counted as " +
			"removed." + deprecatedTopLevel + " Requires the " + ScopeWrite + " scope.",
		RequestBody: d.RequestBody(broadcastRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("What the broadcast did", d.SchemaOf(broadcastResponse{})),
		},
	})
}
