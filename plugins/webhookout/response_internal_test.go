package webhookout

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keysOf returns the top-level keys of a marshaled value.
func keysOf(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return slices.Sorted(maps.Keys(decoded))
}

// TestAnEmptyDeadPileStillReportsItsZero is the reason the optional fields are
// POINTERS.
//
// # The failure it exists for is a number that disappears when it matters
//
// The dead-letter listing carries "total" — the WHOLE pile, not the page — and
// it is the number that decides whether anybody is woken up. When the pile is
// empty that number is ZERO, and zero is the most useful answer the endpoint
// ever gives: it is how an operator working through an incident learns they are
// finished.
//
// A plain `int64` with `omitempty` drops exactly that value. The listing would
// then answer "the pile is clear" by saying nothing at all, and a client reading
// the field would see an absence where it expected a count. The pointer is what
// separates "zero" from "this listing does not carry a total", and this test is
// what stops somebody simplifying the pointer away — the code compiles either
// way and every other case looks identical.
//
// # What this proves, and what it does NOT
//
// It pins the TYPE's marshaling. It says nothing about whether a handler sets
// the field, and that is not a theoretical gap: mutating the pending branch to
// write retry advice left this file green. The endpoint-level claim is in
// internal/e2e (TestTheDeliveryListingReportsAnEmptyPileAsZero), where a real
// server answers a real request.
func TestAnEmptyDeadPileStillReportsItsZero(t *testing.T) {
	t.Parallel()

	var (
		empty   int64
		allowed = maxAttempts
	)

	dead := deliveryListResponse{
		Data:            []deliveryResponse{},
		Count:           0,
		State:           stateDead,
		Total:           &empty,
		AttemptsAllowed: &allowed,
		RetryWindow:     deliveryWindow().String(),
		Exits:           []string{"POST /admin/v1/webhooks/deliveries/{id}/redrive"},
	}

	raw, err := json.Marshal(dead)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	total, carried := decoded["total"]
	require.True(t, carried,
		"an empty dead pile must still report its zero; without the field an operator "+
			"cannot tell \"the pile is clear\" from \"this listing has no total\"")
	assert.InDelta(t, 0.0, total, 0.0)
}

// TestTheTwoDeliveryListingsCarryTheFieldsTheyShould pins both shapes of the
// one response type.
//
// The endpoint answers two different questions under one path, and the extra
// facts belong to only one of them: a pending listing has no ceiling to report
// and no exits to offer, because nothing has been given up on. Writing them
// anyway would document retry advice for deliveries that are still being
// retried, and the OpenAPI schema derived from this type declares them optional
// on exactly that basis.
//
// Like the test above this pins the TYPE. Which shape each branch of the handler
// actually writes is checked against a running server in internal/e2e.
func TestTheTwoDeliveryListingsCarryTheFieldsTheyShould(t *testing.T) {
	t.Parallel()

	var (
		total   int64 = 7
		allowed       = maxAttempts
	)

	dead := deliveryListResponse{
		Data: []deliveryResponse{}, Count: 0, State: stateDead,
		Total: &total, AttemptsAllowed: &allowed,
		RetryWindow: deliveryWindow().String(),
		Exits:       []string{"POST /admin/v1/webhooks/deliveries/{id}/discard"},
	}
	assert.Equal(t,
		[]string{"attempts_allowed", "count", "data", "exits", "retry_window", "state", "total"},
		keysOf(t, dead),
		"the dead listing carries the five facts an operator needs to act")

	pending := deliveryListResponse{Data: []deliveryResponse{}, Count: 0, State: statePending}
	assert.Equal(t, []string{"count", "data", "state"}, keysOf(t, pending),
		"a pending listing must NOT carry retry advice; nothing has been given up on")
}

// TestTheReceiverListingCarriesTheForwardedSet keeps the answer next to the
// question.
//
// The set travels inside the listing because it is what brought the operator
// here — "why is my receiver not getting X" — and a listing that dropped it
// would send them to a changelog to find out.
func TestTheReceiverListingCarriesTheForwardedSet(t *testing.T) {
	t.Parallel()

	body := endpointListResponse{
		Data: []endpointResponse{}, Count: 0, ForwardedTopics: ForwardedTopics,
	}

	assert.Equal(t, []string{"count", "data", "forwarded_topics"}, keysOf(t, body))

	raw, err := json.Marshal(body)
	require.NoError(t, err)

	var decoded struct {
		Topics []string `json:"forwarded_topics"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, ForwardedTopics, decoded.Topics,
		"the listing must report the set gobit really forwards, not a copy of it")
}
