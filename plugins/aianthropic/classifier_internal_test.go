package aianthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
)

// serve stands a server up that replies with one body and returns a classifier
// pointed at it, plus the headers and body it received.
//
// The two are returned by POINTER because the handler fills them while
// Classify runs: a value returned here would be whatever they held before the
// call, which is nothing.
func serve(t *testing.T, status int, body string) (*Classifier, *http.Header, *[]byte) {
	t.Helper()

	var (
		headers http.Header
		raw     []byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		headers = r.Header.Clone()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	client := &Classifier{
		key: "sk-ant-secret", model: "a-model-3",
		base: server.URL, client: server.Client(),
	}

	return client, &headers, &raw
}

// answered builds a Messages API reply carrying one text block.
func answered(text string) string {
	body, _ := json.Marshal(response{
		Model: "a-model-3-20260101",
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: text}},
	})

	return string(body)
}

// ask is the input every test starts from.
func ask() provider.ClassifyInput {
	return provider.ClassifyInput{
		Instruction: "decide whether this review may be published",
		Text:        "buy cheap shoes at example.test",
		Labels:      []string{"approved", "rejected"},
	}
}

// TestTheKeyTravelsInAHeaderAndNowhereElse is the assertion that matters most,
// because the failure it prevents is silent and permanent.
//
// A key in the URL lands in every proxy log between here and the service, and a
// key in an error message lands in the error reporter — which is an external
// collector by design (ADR 0014).
func TestTheKeyTravelsInAHeaderAndNowhereElse(t *testing.T) {
	t.Parallel()

	client, headers, raw := serve(t, http.StatusOK,
		answered("LABEL: rejected\nREASON: it advertises"))

	_, err := client.Classify(context.Background(), ask())
	require.NoError(t, err)

	assert.Equal(t, "sk-ant-secret", headers.Get("x-api-key"),
		"the key did not travel in the header at all, so nothing was authenticated")
	assert.Equal(t, apiVersion, headers.Get("anthropic-version"))
	assert.NotContains(t, string(*raw), "sk-ant-secret",
		"the key was written into the request BODY")

	// The failing path is the one that would carry it into a report.
	failing, _, _ := serve(t, http.StatusUnauthorized, `{"error":"bad key"}`)
	_, err = failing.Classify(context.Background(), ask())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sk-ant-secret",
		"the key is in the error text, and errors from here reach an external collector")
}

// TestALabelOutsideTheSetIsAFailure holds the contract's own sentence.
//
// The label is on its way into a column with a CHECK. Returning the model's
// word and letting the caller find out it cannot be stored would move the
// failure two layers from what caused it.
func TestALabelOutsideTheSetIsAFailure(t *testing.T) {
	t.Parallel()

	client, _, _ := serve(t, http.StatusOK,
		answered("LABEL: maybe\nREASON: it is hard to say"))

	_, err := client.Classify(context.Background(), ask())
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Contains(t, err.Error(), "maybe")
}

// TestAnAnswerWithNoReasonIsAFailure keeps an unweighable proposal out of the
// column.
//
// A label with no reasoning is a verdict an operator can only ignore, so the
// whole call cost nothing and produced a row somebody has to read past.
func TestAnAnswerWithNoReasonIsAFailure(t *testing.T) {
	t.Parallel()

	client, _, _ := serve(t, http.StatusOK, answered("LABEL: rejected"))

	_, err := client.Classify(context.Background(), ask())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reason")
}

// TestTheCallersSpellingComesBack is the small decision that keeps the failure
// out of the database.
//
// The model may answer "Rejected" and the column's CHECK knows one casing.
func TestTheCallersSpellingComesBack(t *testing.T) {
	t.Parallel()

	client, _, _ := serve(t, http.StatusOK,
		answered("LABEL: REJECTED\nREASON: it advertises another shop"))

	got, err := client.Classify(context.Background(), ask())
	require.NoError(t, err)
	assert.Equal(t, "rejected", got.Label)
	assert.Equal(t, "it advertises another shop", got.Reason)
}

// TestTheModelThatAnsweredIsReportedRatherThanTheOneConfigured pins where the
// stored model name comes from.
//
// The configuration names a family; the service serves a version. A proposal
// stored under the family cannot be retired when a shop stops trusting the
// version that produced it.
func TestTheModelThatAnsweredIsReportedRatherThanTheOneConfigured(t *testing.T) {
	t.Parallel()

	client, _, _ := serve(t, http.StatusOK,
		answered("LABEL: approved\nREASON: it describes the product"))

	got, err := client.Classify(context.Background(), ask())
	require.NoError(t, err)
	assert.Equal(t, "a-model-3-20260101", got.Model)
	assert.NotEqual(t, client.model, got.Model)
}

// TestTheStatusDecidesWhetherTheCallerShouldComeBack separates a service that
// is busy from a request that will never work.
//
// A misconfiguration reported as transient repeats every quarter of an hour
// with nobody told it cannot succeed.
func TestTheStatusDecidesWhetherTheCallerShouldComeBack(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		status int
		kind   coreerrors.Kind
	}{
		{"a rate limit", http.StatusTooManyRequests, coreerrors.KindUnavailable},
		{"the service is down", http.StatusBadGateway, coreerrors.KindUnavailable},
		{"a bad request", http.StatusBadRequest, coreerrors.KindInternal},
		{"a bad key", http.StatusUnauthorized, coreerrors.KindInternal},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			client, _, _ := serve(t, testCase.status, `{"error":{"message":"nope"}}`)

			_, err := client.Classify(context.Background(), ask())
			require.Error(t, err)
			assert.Equal(t, testCase.kind, coreerrors.KindOf(err))
			assert.Contains(t, err.Error(), "nope",
				"the service's own sentence was dropped, and it is what an operator needs")
		})
	}
}

// TestTheRequestCarriesTheVersionAndTheLabels checks the wire shape.
func TestTheRequestCarriesTheVersionAndTheLabels(t *testing.T) {
	t.Parallel()

	client, _, raw := serve(t, http.StatusOK,
		answered("LABEL: approved\nREASON: it describes the product"))

	_, err := client.Classify(context.Background(), ask())
	require.NoError(t, err)

	var sent request
	require.NoError(t, json.Unmarshal(*raw, &sent))
	assert.Equal(t, "a-model-3", sent.Model)
	assert.Contains(t, sent.System, "approved, rejected")
	require.Len(t, sent.Messages, 1)
	assert.Contains(t, sent.Messages[0].Content, "buy cheap shoes")
}

// TestOneLabelIsNotAQuestion refuses the degenerate input before it costs a
// call.
func TestOneLabelIsNotAQuestion(t *testing.T) {
	t.Parallel()

	client, _, _ := serve(t, http.StatusOK, answered("LABEL: approved\nREASON: fine"))

	in := ask()
	in.Labels = []string{"approved"}

	_, err := client.Classify(context.Background(), in)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "two labels"))
}
