package webhookout

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/core/webhook"
)

// object reads a JSON object out of a decoded document, or stops the test.
func object(t *testing.T, value any, where string) map[string]any {
	t.Helper()

	m, ok := value.(map[string]any)
	require.Truef(t, ok, "%s has to be an object, got %T", where, value)

	return m
}

// array reads a JSON array out of a decoded document, or stops the test.
func array(t *testing.T, value any, where string) []any {
	t.Helper()

	a, ok := value.([]any)
	require.Truef(t, ok, "%s has to be an array, got %T", where, value)

	return a
}

// text reads a JSON string out of a decoded document, or stops the test.
func text(t *testing.T, value any, where string) string {
	t.Helper()

	s, ok := value.(string)
	require.Truef(t, ok, "%s has to be a string, got %T", where, value)

	return s
}

// TestTheDocumentDescribesEveryForwardedTopic holds the document's "webhooks" to
// what the sender sends with (ADR 0435).
//
// Each comparison is against the value the sender uses, never against a list
// written here: the names against [ForwardedTopics], each topic's data against
// [TopicFields], the headers against core/webhook's constants, and the envelope
// against the keys [webhook.Delivery] marshals to. So the section cannot skip a
// topic, drop a field, miss a header or describe some other body while this
// passes — and the e2e test then compares the section with a delivery a real
// order produced.
//
// The router is empty: the section has no route behind it, so the admin
// descriptions go unmatched and the build does not care.
func TestTheDocumentDescribesEveryForwardedTopic(t *testing.T) {
	t.Parallel()

	d := openapi.New("test", "v1")
	d.ForModule(ModuleName, func() { newModule(nil).Describe(d) })

	built, err := d.Build(chi.NewRouter())
	require.NoError(t, err)
	raw, err := json.Marshal(built)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	webhooks := object(t, doc["webhooks"], "webhooks")
	assert.ElementsMatch(t, ForwardedTopics, slices.Collect(maps.Keys(webhooks)),
		"the document's webhooks are not the forwarded topics")

	schemas := object(t, object(t, doc["components"], "components")["schemas"], "components.schemas")
	headers := []string{
		webhook.HeaderEvent, webhook.HeaderDelivery, webhook.HeaderAttempt,
		webhook.HeaderTimestamp, webhook.HeaderSignature,
	}
	envelope := keysOf(t, webhook.Delivery{})

	for _, topic := range ForwardedTopics {
		post := object(t, object(t, webhooks[topic], topic)["post"], topic+".post")

		var named []string
		for _, value := range array(t, post["parameters"], topic+".parameters") {
			parameter := object(t, value, topic+" parameter")
			assert.Equal(t, "header", parameter["in"])
			assert.Equal(t, true, parameter["required"], "%s: every header travels on every delivery", topic)
			named = append(named, text(t, parameter["name"], topic+" parameter name"))
			if parameter["name"] == webhook.HeaderEvent {
				assert.Equal(t, topic, object(t, parameter["schema"], "event header")["const"],
					"%s: the event header is not pinned to the topic", topic)
			}
		}
		assert.ElementsMatch(t, headers, named, "%s: the headers are not the ones the sender sets", topic)

		body := object(t, post["requestBody"], topic+".requestBody")
		assert.Equal(t, true, body["required"])
		media := object(t, object(t, body["content"], "content")["application/json"], "application/json")
		parts := array(t, object(t, media["schema"], "schema")["allOf"], topic+" allOf")
		require.Len(t, parts, 2, "%s: the body is the envelope and the topic's narrowing of it", topic)

		ref := text(t, object(t, parts[0], "envelope")["$ref"], topic+" envelope $ref")
		component := object(t, schemas[strings.TrimPrefix(ref, "#/components/schemas/")], ref)
		assert.ElementsMatch(t, envelope, slices.Collect(maps.Keys(object(t, component["properties"], ref))),
			"%s: the body refers to %s, which is not webhook.Delivery's shape", topic, ref)
		assert.ElementsMatch(t, envelope, component["required"],
			"%s: gobit writes every envelope field on every delivery, so every one is required", topic)

		narrowing := object(t, object(t, parts[1], "narrowing")["properties"], "narrowing.properties")
		assert.Equal(t, topic, object(t, narrowing["event"], "event")["const"])
		data := object(t, narrowing["data"], "data")
		assert.NotContains(t, data, "required", "%s: a receiver's field list narrows data, so no field is required", topic)
		assert.NotContains(t, data, "additionalProperties", "%s: data is left open to a field added later", topic)
		fields := object(t, data["properties"], topic+".data.properties")
		assert.ElementsMatch(t, TopicFields[topic], slices.Collect(maps.Keys(fields)),
			"%s: the document's data fields are not TopicFields'", topic)
		for field, schema := range fields {
			assert.Equal(t, map[string]any{"type": "string"}, schema,
				"%s: the document types %q as something other than the string every publisher "+
					"writes (TestEveryPayloadValueIsAString)", topic, field)
		}

		responses := object(t, post["responses"], topic+".responses")
		assert.Contains(t, responses, "2XX")
		assert.Contains(t, responses, "default")
		assert.Equal(t, []any{}, post["security"], "%s: a delivery carries its own proof, its signature", topic)
	}
}
