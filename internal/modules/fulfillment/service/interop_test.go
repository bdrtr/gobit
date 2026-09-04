package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// interopOptionSchema is the test-side COPY of the response schema.
//
// It deliberately depends not on the type in the service package but on the
// schema DECLARED in interop.go's godoc: the consumer module will do the same
// thing, because it cannot import this package either (ADR 0006). If a field
// name changes the test fails and a silent schema drift is caught.
type interopOptionSchema struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Amount            int64  `json:"amount"`
	CurrencyCode      string `json:"currency_code"`
	PriceType         string `json:"price_type"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	IsReturn          bool   `json:"is_return"`
	AdminOnly         bool   `json:"admin_only"`
}

// interopResponseSchema is the test-side copy of the list response.
type interopResponseSchema struct {
	Options []interopOptionSchema `json:"options"`
}

// TestInteropListOptionsSchema proves that the published JSON schema is the one
// documented.
func TestInteropListOptionsSchema(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_500,
	})

	request, err := json.Marshal(map[string]any{
		"region_id":            "reg_tr",
		"currency_code":        "TRY",
		"country_code":         "TR",
		"shipping_profile_ids": []string{profileID},
		"subtotal":             50_000,
		"item_count":           3,
		"total_weight":         1_500,
		"attributes":           map[string]string{"customer_group_id": "vip"},
		"include_admin_only":   false,
		"is_return":            false,
	})
	require.NoError(t, err)

	raw, err := interop.ListOptionsJSON(context.Background(), request)
	require.NoError(t, err)

	var response interopResponseSchema
	require.NoError(t, json.Unmarshal(raw, &response))
	require.Len(t, response.Options, 1)

	option := response.Options[0]
	assert.Equal(t, optionID, option.ID)
	assert.Equal(t, "Standard shipping", option.Name)
	assert.Equal(t, int64(2_500), option.Amount)
	assert.Equal(t, "TRY", option.CurrencyCode)
	assert.Equal(t, "flat", option.PriceType)
	assert.Equal(t, "fake", option.ProviderID)
	assert.Equal(t, profileID, option.ShippingProfileID)
	assert.False(t, option.IsReturn)
	assert.False(t, option.AdminOnly)

	assert.NotContains(t, string(raw), "\"data\"",
		"the provider's raw data must not reach the cross-module surface")
}

// TestInteropCarriesTheAdminOnlyFlag proves that the admin flows can ask for the
// admin_only options, while the default is the storefront behavior.
func TestInteropCarriesTheAdminOnlyFlag(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)
	profileID := setup.createProfile(t, "default")
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Hand delivery",
		ShippingProfileID: profileID,
		AdminOnly:         true,
	})

	defaultBody, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY"}`))
	require.NoError(t, err)

	var empty interopResponseSchema
	require.NoError(t, json.Unmarshal(defaultBody, &empty))
	assert.Empty(t, empty.Options, "the default has to be the storefront behavior")

	adminBody, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","include_admin_only":true}`))
	require.NoError(t, err)

	var filled interopResponseSchema
	require.NoError(t, json.Unmarshal(adminBody, &filled))
	require.Len(t, filled.Options, 1)
	assert.True(t, filled.Options[0].AdminOnly)
}

// TestInteropSubtotalThresholdIsComparedAsAnInteger proves that at the rule's
// threshold even A SINGLE CENT makes a difference.
//
// The comparison is over integers: a cart one cent below the threshold MUST NOT
// open free shipping, while one at the threshold has to. A string comparison, or
// a rounded subtotal, would shift this boundary.
func TestInteropSubtotalThresholdIsComparedAsAnInteger(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Free shipping",
		ShippingProfileID: profileID,
	})
	_, err := setup.svc.CreateShippingOptionRule(context.Background(), optionID,
		service.CreateRuleInput{
			Attribute: service.AttrSubtotal,
			Operator:  "gte",
			Values:    []string{"50000"},
		})
	require.NoError(t, err)

	below, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","subtotal":49999}`))
	require.NoError(t, err)
	var empty interopResponseSchema
	require.NoError(t, json.Unmarshal(below, &empty))
	assert.Empty(t, empty.Options, "one cent below the threshold must not open the option")

	atThreshold, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","subtotal":50000}`))
	require.NoError(t, err)
	var filled interopResponseSchema
	require.NoError(t, json.Unmarshal(atThreshold, &filled))
	require.Len(t, filled.Options, 1, "at the threshold the option has to be offered")
}

// TestInteropRejectsAnUnrecognizedField proves that the first sign of the schema
// in the two packages diverging is caught.
func TestInteropRejectsAnUnrecognizedField(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	_, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","cart_id":"cart_1"}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
	assert.Equal(t, service.CodeInteropRequestInvalid, errors.CodeOf(err))
}

// TestInteropRejectsAnEmptyRequest proves that an empty body does not silently
// return an empty list.
func TestInteropRejectsAnEmptyRequest(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	for _, body := range []json.RawMessage{nil, json.RawMessage("null")} {
		_, err := interop.ListOptionsJSON(context.Background(), body)
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
	}
}

// TestInteropRejectsAFractionalNumber proves that the money and count fields are
// INTEGERS.
//
// An implementation decoding through float64 instead of json.Number would
// SILENTLY truncate the same body to 100 and the subtotal would lose a cent; this
// test pins that path shut.
func TestInteropRejectsAFractionalNumber(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	_, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","subtotal":100.5}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
}

// TestInteropCreateAndCancelFulfillment proves that the saga surface works end to
// end: two calls with the same key produce ONE fulfillment, the cancellation IS
// IDEMPOTENT and the status is readable.
func TestInteropCreateAndCancelFulfillment(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)
	optionID := readyOption(t, setup)

	first, err := interop.CreateFulfillment(context.Background(), "order_1", optionID, "key-1")
	require.NoError(t, err)
	require.NotEmpty(t, first)

	second, err := interop.CreateFulfillment(context.Background(), "order_1", optionID, "key-1")
	require.NoError(t, err)
	assert.Equal(t, first, second, "the same key has to produce a single fulfillment")

	status, err := interop.FulfillmentStatus(context.Background(), first)
	require.NoError(t, err)
	assert.Equal(t, "pending", status)

	require.NoError(t, interop.CancelFulfillment(context.Background(), first))
	require.NoError(t, interop.CancelFulfillment(context.Background(), first),
		"the compensation has to be callable twice")

	status, err = interop.FulfillmentStatus(context.Background(), first)
	require.NoError(t, err)
	assert.Equal(t, "canceled", status, "the trace of the compensation has to be readable from the status")

	_, create, cancel := setup.provider.callCounts()
	assert.Equal(t, 1, create, "a single fulfillment has to be opened at the provider")
	assert.Equal(t, 1, cancel, "a single cancellation has to go to the provider")
}

// TestInteropRankLocationsIsDeterministic proves that a second call with the same
// candidates returns the same order, and that the result is independent of the
// candidates' ARRIVAL ORDER.
//
// The claim is for the caller: a loop that calls this method again after dropping
// the exhausted candidates from the list would, with an order-dependent
// selection, be shown a different warehouse on every round and the first round's
// reservation would be orphaned.
//
// There is NO policy record at all in the setup; that is why the test is at the
// same time the proof of backward compatibility: the elimination and the ranking
// fall away, the tie-breaking rule (the candidate with the smallest identifier)
// stands alone, and the result is identical to the behavior BEFORE the policy.
func TestInteropRankLocationsIsDeterministic(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	first, err := interop.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_izmir", "sloc_ankara", "sloc_bursa"})
	require.NoError(t, err)

	second, err := interop.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_izmir", "sloc_ankara", "sloc_bursa"})
	require.NoError(t, err)
	assert.Equal(t, first, second, "the same candidates have to give the same order")

	shuffled, err := interop.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_bursa", "sloc_izmir", "sloc_ankara"})
	require.NoError(t, err)
	assert.Equal(t, first, shuffled, "the order must not depend on the candidates' arrival order")

	assert.Equal(t, []string{"sloc_ankara", "sloc_bursa", "sloc_izmir"}, first,
		"with no policy record the order has to be built by identifier alone")
}

// TestInteropRankLocationsWithASingleCandidate proves that with a single-candidate
// list that candidate is returned.
func TestInteropRankLocationsWithASingleCandidate(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	ranked, err := interop.RankLocations(context.Background(), testRegionID, []string{"sloc_single"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_single"}, ranked)
}

// TestInteropRankLocationsEmptyListIsAConflict proves that an empty candidate
// list does not SILENTLY return an empty order.
//
// Had an empty order been returned, the caller would leave the loop without
// trying a single warehouse and the line would drop without its reason being
// written. The kind is Conflict: there is nothing to fix in the request, there is
// simply not enough stock in any location.
func TestInteropRankLocationsEmptyListIsAConflict(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	for _, candidates := range [][]string{nil, {}} {
		ranked, err := interop.RankLocations(context.Background(), testRegionID, candidates)
		require.Error(t, err)
		assert.Empty(t, ranked)
		assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
		assert.Equal(t, service.CodeNoShippingLocation, errors.CodeOf(err))
	}
}

// TestInteropRankLocationsRejectsAnEmptyCandidate proves that an empty identifier
// in the list does not enter the order.
//
// The tie-breaking rule (the candidate with the smallest identifier) would pick
// the empty string; the test pins that path shut.
func TestInteropRankLocationsRejectsAnEmptyCandidate(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	ranked, err := interop.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "   "})
	require.Error(t, err)
	assert.Empty(t, ranked)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestInteropCancelOfAnUnknownFulfillmentReturnsNotFound proves that the
// compensation does not silently swallow a record that does not exist.
func TestInteropCancelOfAnUnknownFulfillmentReturnsNotFound(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)

	err := interop.CancelFulfillment(context.Background(), "ful_NOSUCHTHING")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the error has to be errors.NotFound: %v", err)
}
