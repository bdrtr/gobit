//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file proves ADR 0194 on the production wiring: a parcel opened for an
// order hands its carrier the address the order was placed with.
//
// # Why a spy carrier
//
// The destination is deliberately stored nowhere new. The order holds the
// address and its erasure empties it; the parcel does not keep a copy, and the
// box provider — the shop's own hands — reads none of it. So the one place the
// claim can be seen is where a real carrier plugin would stand: the provider
// itself. The spy is installed as a plugin, through the same host call a
// carrier plugin makes, and the rest of the chain is production code. It is
// the notification spy's argument (notification_test.go), made for a label.

// carrierSpyID is the spy's provider identity.
const carrierSpyID = "e2e-carrier"

// carrierSpy is the spy carrier; it records every shipment it is asked for.
var carrierSpy = &carrierProviderSpy{created: map[string]coreprovider.CreateFulfillmentInput{}}

// carrierProviderSpy is a fulfillment provider that remembers its inputs.
type carrierProviderSpy struct {
	mu sync.Mutex
	// created is keyed by the idempotency key the caller sent, which is the
	// one handle a test chooses itself.
	created map[string]coreprovider.CreateFulfillmentInput
}

// ID returns the spy's identity.
func (*carrierProviderSpy) ID() string { return carrierSpyID }

// Quote prices every option at zero; nothing here is about money.
func (*carrierProviderSpy) Quote(_ context.Context, in coreprovider.QuoteInput) (coreprovider.ShippingQuote, error) {
	return coreprovider.ShippingQuote{OptionID: in.OptionID, CurrencyCode: in.CurrencyCode}, nil
}

// Create records the input and opens a pending shipment.
func (s *carrierProviderSpy) Create(
	_ context.Context, in coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created[in.IdempotencyKey] = in

	return coreprovider.Fulfillment{ID: "spy_" + in.Reference, Status: coreprovider.FulfillmentPending}, nil
}

// Cancel does nothing and succeeds, as an idempotent cancel must.
func (*carrierProviderSpy) Cancel(context.Context, string) error { return nil }

// shipmentFor returns what the spy was handed under the key.
func (s *carrierProviderSpy) shipmentFor(key string) (coreprovider.CreateFulfillmentInput, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.created[key]

	return in, ok
}

// carrierSpyPlugin installs the spy the way a carrier plugin installs itself.
type carrierSpyPlugin struct{}

// Name is the plugin's name.
func (carrierSpyPlugin) Name() string { return "e2e-carrier-spy" }

// Setup registers the spy with the fulfillment module.
func (carrierSpyPlugin) Setup(_ context.Context, h *coreplugin.Host) error {
	h.RegisterFulfillmentProvider(carrierSpy)

	return nil
}

// spyOption creates an admin-only shipping option on the spy carrier.
func spyOption(t *testing.T) string {
	t.Helper()

	ctx := t.Context()
	option, err := shippingSvc.CreateShippingOption(ctx, fulfillmentsvc.CreateOptionInput{
		Name:              fmt.Sprintf("Spy carrier %d", fixtureCounter.Add(1)),
		ProviderID:        carrierSpyID,
		ShippingProfileID: newShippingProfile(ctx, t, "Spy"),
		Amount:            0,
		CurrencyCode:      taxedCurrency,
		RegionID:          taxedRegionID,
		AdminOnly:         true,
	})
	require.NoError(t, err)

	return option.ID
}

// openSpyParcel opens a parcel for the order on the spy carrier and returns the
// idempotency key it was opened under and the parcel's id.
func openSpyParcel(t *testing.T, orderID, optionID string) (key, fulfillmentID string) {
	t.Helper()

	key = fmt.Sprintf("carrier-destination-%d", fixtureCounter.Add(1))
	opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/fulfillments",
		map[string]any{"shipping_option_id": optionID, "idempotency_key": key})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())

	var answer struct {
		Data struct {
			FulfillmentID string `json:"fulfillment_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &answer))

	return key, answer.Data.FulfillmentID
}

// TestTheCarrierIsHandedWhereTheOrderWent opens a parcel for an order through
// the admin surface and reads the destination the carrier received.
func TestTheCarrierIsHandedWhereTheOrderWent(t *testing.T) {
	orderID := addressedOrder(t)
	key, _ := openSpyParcel(t, orderID, spyOption(t))

	handed, ok := carrierSpy.shipmentFor(key)
	require.True(t, ok, "the parcel never reached the carrier")
	require.NotNil(t, handed.Destination, "the carrier was handed no destination")
	assert.Equal(t, coreprovider.Address{
		FirstName:   "Gift",
		LastName:    "Recipient",
		Address1:    "9 Far Road",
		City:        "Elsewhere",
		PostalCode:  "11111",
		CountryCode: taxedCountry,
		Metadata:    map[string]any{"gate_code": "4411"},
	}, *handed.Destination, "the destination is the SHIPPING address, the gift's recipient")
	assert.NotContains(t, handed.Data, "address_1", "the address travels as the destination, not in Data")
}
