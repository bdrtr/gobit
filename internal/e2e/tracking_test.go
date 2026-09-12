//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fulfillmentmanual "github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	fulfillmentmodels "github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves what asking a carrier where a parcel is really answers
// (ADR 0149), through the production stack.
//
// # Why the manual provider makes this a REAL test rather than a tautology
//
// The provider's ledger is a SEPARATE table from the module's record — the
// provider may not read the module's tables and does not — so the two can hold
// different things, and here they do: the module's status moves when an operator
// marks a parcel handed over, while the provider's row keeps the status and the
// tracking number the label was opened with. The endpoint reports both, and the
// difference between them is a label printed under one number and recorded under
// another.
//
// That is exactly what no test could see before: the provider's own view was
// reachable only through a method the core contract did not carry
// (`manual.Provider.GetShipment`, whose godoc says a drift between the two
// ledgers "can only be seen that way").

// trackingResponse is the answer of GET /admin/v1/fulfillments/{id}/tracking.
type trackingResponse struct {
	Data struct {
		FulfillmentID string `json:"fulfillment_id"`
		ProviderID    string `json:"provider_id"`
		ExternalID    string `json:"external_id"`
		Answer        string `json:"answer"`
		Reason        string `json:"reason"`
		Local         struct {
			Status         string `json:"status"`
			TrackingNumber string `json:"tracking_number"`
		} `json:"local"`
		Provider *struct {
			Status         string `json:"status"`
			TrackingNumber string `json:"tracking_number"`
			Detail         string `json:"detail"`
			MovedAt        string `json:"moved_at"`
		} `json:"provider"`
		TrackingNumbersAgree bool `json:"tracking_numbers_agree"`
	} `json:"data"`
}

// trackParcel asks the admin endpoint where the parcel is.
func trackParcel(t *testing.T, fulfillmentID string) trackingResponse {
	t.Helper()

	recorder, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/fulfillments/"+fulfillmentID+"/tracking", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the tracking read must answer; body: %s", recorder.Body.String())

	var out trackingResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &out),
		"the tracking answer could not be decoded; body: %s", recorder.Body.String())

	return out
}

// TestTheCarrierIsAskedWhereTheParcelIs is the decision, end to end.
func TestTheCarrierIsAskedWhereTheParcelIs(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Tracked Product", map[string]int64{
		taxedCurrency: shippingUnitPrice,
	}, shippingStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, shippingQuantity)
	order, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     shippingTotal,
	})
	require.NoError(t, err, "the order must be placed")

	profileID := newShippingProfile(ctx, t, "E2E Tracking Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Tracked Shipping", shippingOptionFee, false)

	fulfillmentID, err := shippingInterop.CreateFulfillment(ctx, order.OrderID, optionID,
		"e2e-tracking-"+order.OrderID)
	require.NoError(t, err, "the parcel must be openable")

	// The operator hands the parcel over and types the number from the label they
	// actually stuck on it. The provider's row does not move: with a real carrier
	// the carrier reports, and with this one the shop is the carrier.
	operatorNumber := fmt.Sprintf("OPERATOR-%s", fulfillmentID)
	shipped, err := shippingSvc.MarkShipped(ctx, fulfillmentID, operatorNumber, "")
	require.NoError(t, err, "the parcel must be markable as handed over")
	require.Equal(t, fulfillmentmodels.StatusShipped, shipped.Status)

	tracking := trackParcel(t, fulfillmentID)

	assert.Equal(t, "answered", tracking.Data.Answer,
		"the provider that ships in the box answers; reason: %q", tracking.Data.Reason)
	assert.Equal(t, fulfillmentmanual.ID, tracking.Data.ProviderID)
	assert.NotEmpty(t, tracking.Data.ExternalID,
		"the carrier is addressed by ITS OWN identifier")

	assert.Equal(t, "shipped", tracking.Data.Local.Status,
		"the module's half is what the operator recorded")
	assert.Equal(t, operatorNumber, tracking.Data.Local.TrackingNumber)

	require.NotNil(t, tracking.Data.Provider, "the carrier's half has to be there")
	assert.Equal(t, "pending", tracking.Data.Provider.Status,
		"the provider's ledger is its own and nothing moved it; the two statuses "+
			"differing is the fact this endpoint exists to show")
	assert.NotEqual(t, operatorNumber, tracking.Data.Provider.TrackingNumber,
		"the number on the carrier's row is the one the label was opened with")
	assert.NotEmpty(t, tracking.Data.Provider.MovedAt,
		"the provider's own moment comes through")

	assert.False(t, tracking.Data.TrackingNumbersAgree,
		"a parcel recorded under a different number than its label is visible HERE "+
			"and nowhere else")
}

// TestTrackingAParcelThatDoesNotExistIs404 keeps a mistyped identifier loud.
//
// A parcel that cannot be TRACKED is a 200 whose answer says why; a parcel that
// does not exist is a 404. Collapsing them would tell an operator their carrier
// has no tracking when they simply typed the wrong id.
func TestTrackingAParcelThatDoesNotExistIs404(t *testing.T) {
	recorder, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/fulfillments/ful_01NOSUCHPARCEL00000/tracking", nil)
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotFound, recorder.Code, "body: %s", recorder.Body.String())
}
