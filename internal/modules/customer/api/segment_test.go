package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// fakePreview is the segment flow's preview, recording the rule it was handed.
type fakePreview struct {
	rule  json.RawMessage
	calls int
}

func (f *fakePreview) PreviewSegmentJSON(_ context.Context, rule json.RawMessage) (json.RawMessage, error) {
	f.calls++
	f.rule = rule

	return json.RawMessage(`{"members":12,"customers":480}`), nil
}

// operator is a caller holding every admin scope.
var operator = corehttp.Principal{ID: "user_segment", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}

// adminCall runs one request as the operator.
func adminCall(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	ctx := corehttp.WithPrincipal(context.Background(), operator)
	req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// data decodes the envelope's data object.
func data(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())

	return envelope.Data
}

// adminRouter is the admin router without the preview bound.
func adminRouter(svc api.Customer) chi.Router {
	r := chi.NewRouter()
	api.New(svc, nil).Routes(r)

	return r
}

// previewRouter is the admin router with the preview bound.
func previewRouter(svc api.Customer, preview api.SegmentPreview) chi.Router {
	r := chi.NewRouter()
	api.New(svc, nil).WithPreview(preview).Routes(r)

	return r
}

const spendRule = `{"currency_code":"try","window_days":30,"conditions":[
	{"attribute":"net_spend","operator":"gte","value":100000},
	{"attribute":"country_code","operator":"in","values":["tr"]}]}`

// TestASegmentIsSetWithTheBodysRule is ADR 0217: the route hands the group and
// the rule as written to the service, which normalizes it, and answers the
// group with its rule.
func TestASegmentIsSetWithTheBodysRule(t *testing.T) {
	t.Parallel()

	var asked models.SegmentRule
	svc := &stubCustomer{
		setSegmentFn: func(_ context.Context, id string, rule models.SegmentRule) (models.CustomerGroup, error) {
			asked = rule
			return models.CustomerGroup{ID: id, Name: "big", Segment: &rule}, nil
		},
	}

	rec := adminCall(t, adminRouter(svc), http.MethodPut, "/admin/v1/customer-groups/custgrp_1/segment", spendRule)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "custgrp_1", svc.sonGroupID)
	assert.Equal(t, "try", asked.CurrencyCode)
	assert.Equal(t, int32(30), asked.WindowDays)
	require.Len(t, asked.Conditions, 2)
	assert.JSONEq(t, `100000`, string(asked.Conditions[0].Value))
	assert.Equal(t, []string{"tr"}, asked.Conditions[1].Values)
	segment, ok := data(t, rec)["segment"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Len(t, segment["conditions"], 2)
}

// TestASegmentIsCleared: the route hands the group to the service.
func TestASegmentIsCleared(t *testing.T) {
	t.Parallel()

	svc := &stubCustomer{
		clearSegmentFn: func(_ context.Context, id string) (models.CustomerGroup, error) {
			return models.CustomerGroup{ID: id, Name: "big"}, nil
		},
	}

	rec := adminCall(t, adminRouter(svc), http.MethodDelete, "/admin/v1/customer-groups/custgrp_1/segment", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "custgrp_1", svc.sonGroupID)
	assert.NotContains(t, rec.Body.String(), `"segment"`)
}

// TestAPreviewHandsTheFlowTheNormalizedRule: the flow gets the rule as it would
// be stored, and the answer is the flow's count.
func TestAPreviewHandsTheFlowTheNormalizedRule(t *testing.T) {
	t.Parallel()

	preview := &fakePreview{}

	rec := adminCall(t, previewRouter(&stubCustomer{}, preview), http.MethodPost,
		"/admin/v1/customer-segments/preview", spendRule)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"currency_code":"TRY","window_days":30,"conditions":[
		{"attribute":"net_spend","operator":"gte","value":100000},
		{"attribute":"country_code","operator":"in","values":["TR"]}]}`, string(preview.rule))
	assert.Equal(t, map[string]any{"members": float64(12), "customers": float64(480)}, data(t, rec))
}

// TestAPreviewOfARuleOutsideTheVocabularyAsksNothing: the rule is refused
// before the flow reads a customer.
func TestAPreviewOfARuleOutsideTheVocabularyAsksNothing(t *testing.T) {
	t.Parallel()

	preview := &fakePreview{}

	rec := adminCall(t, previewRouter(&stubCustomer{}, preview), http.MethodPost,
		"/admin/v1/customer-segments/preview",
		`{"conditions":[{"attribute":"lifetime_value","operator":"gt","value":1}]}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), models.CodeSegmentInvalid)
	assert.Zero(t, preview.calls)
}

// TestAPreviewWithNoFlowFailsClosed: a handler built without the flow answers
// an internal error, not a count of nothing.
func TestAPreviewWithNoFlowFailsClosed(t *testing.T) {
	t.Parallel()

	rec := adminCall(t, adminRouter(&stubCustomer{}), http.MethodPost, "/admin/v1/customer-segments/preview",
		`{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}
