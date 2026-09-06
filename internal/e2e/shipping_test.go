//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	fulfillmentmanual "github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	fulfillmentmodels "github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	fulfillingwf "github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// This file proves the SHIPPING leg of the plan's Phase 7 DoD: "a fulfillment
// can be created for an order".
//
// There are two scenarios and both of them go through a SEPARATE surface of
// the fulfillment module:
//
//   - The fulfillment lifecycle, through the cross-module surface
//     ("fulfillment.interop"). The order saga does not run the shipping step
//     today, which means this surface has no consumer whatsoever in
//     production; the only places pinning its signatures are the
//     [shippingSurface] narrow interface and this file.
//   - The storefront listing, through the HTTP store endpoint
//     (/store/v1/shipping-options). An admin_only option not being visible to
//     the customer is not a service decision, it is a trust decision that the
//     endpoint PINS (the handler does not read the flag from the query, it
//     writes false) and it can only be proven by going through the endpoint.
//
// # The order reference is NOT a foreign key
//
// fulfillment imports no module and does NOT VALIDATE which order a
// fulfillment belongs to (Principle 2.2); reference is free text. Exactly for
// that reason the link's actually being established can only be tested end to
// end: the module's own tests can ask "did the text that was handed in come
// back", they cannot ask "is the text that came back REALLY the identity of
// the order that was created".

// The MANUALLY computed amounts of the shipping scenario.
//
// The region is taxed at 20% and NO shipping METHOD is selected on the cart:
//
//	20_000 × 1 = 20_000 subtotal
//	20_000 × 20% = 4_000 tax
//	20_000 - 0 + 4_000 + 0 = 24_000 grand total
//
// The fee of the shipping option ([shippingOptionFee]) does NOT ENTER the
// cart: listing an option is not adding it to the cart, and the cart's
// shipping total is made up of SELECTED methods only.
const (
	shippingUnitPrice int64 = 20_000
	shippingQuantity  int64 = 1
	shippingSubtotal  int64 = 20_000
	shippingTax       int64 = 4_000
	shippingTotal     int64 = 24_000
	shippingStock     int64 = 5

	// shippingOptionFee is the flat fee of the normal option that reaches the
	// storefront.
	shippingOptionFee int64 = 2_500
	// shippingAdminFee is the fee of the option that is open to the admin
	// surface ONLY.
	//
	// Its being CHEAPER than the normal option is deliberate: the list is
	// sorted ascending by fee, so if this option leaked into the storefront it
	// would show up in FIRST place. Had it been expensive, its absence could
	// have been explained by "sorting/pagination" as well.
	shippingAdminFee int64 = 1_500
)

// shippingOptionRequest is the JSON schema of the
// [shippingSurface.ListOptionsJSON] request.
//
// The field names MUST be EXACTLY identical to fulfillment's interop schema:
// the other side REJECTS unknown fields and, because the two packages cannot
// import each other, the compiler cannot see the match (the accepted price of
// ADR 0006). This is the only place where the schema can be verified.
type shippingOptionRequest struct {
	RegionID           string            `json:"region_id"`
	CurrencyCode       string            `json:"currency_code"`
	CountryCode        string            `json:"country_code"`
	ShippingProfileIDs []string          `json:"shipping_profile_ids"`
	Subtotal           int64             `json:"subtotal"`
	ItemCount          int64             `json:"item_count"`
	TotalWeight        int64             `json:"total_weight"`
	Attributes         map[string]string `json:"attributes"`
	IncludeAdminOnly   bool              `json:"include_admin_only"`
	IsReturn           bool              `json:"is_return"`
}

// shippingOptionResponse is the JSON schema of the
// [shippingSurface.ListOptionsJSON] response.
type shippingOptionResponse struct {
	Options []shippingOption `json:"options"`
}

// shippingOption is the JSON schema of a single priced shipping option.
type shippingOption struct {
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

// TestFulfillmentIsCreatedForOrder runs the shipping leg of the Phase 7 DoD
// end to end.
//
// The chain: cart -> order (the Phase 6 flow) -> shipping profile + option ->
// listing of the eligible options -> fulfillment -> cancellation. The order is
// REAL: a fulfillment opened with a made-up reference would not have tested
// the claim "it carries the order reference".
func TestFulfillmentIsCreatedForOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Shipped Product", map[string]int64{
		taxedCurrency: shippingUnitPrice,
	}, shippingStock)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, shippingQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: shippingSubtotal,
		discount: 0,
		tax:      shippingTax,
		shipping: 0,
		total:    shippingTotal,
	}, "after the cart of the shipping scenario was prepared")

	orderResult, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     shippingTotal,
	})
	require.NoError(t, err, "an order must be creatable through the happy path")
	require.NotEmpty(t, orderResult.OrderID, "an order identity must come back")

	// --- 1) eligible shipping options for the order ---

	profileID := newShippingProfile(ctx, t, "E2E Fulfillment Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Standard Shipping", shippingOptionFee, false)

	options := listShippingOptions(ctx, t, shippingOptionRequest{
		RegionID:     taxedRegionID,
		CurrencyCode: taxedCurrency,
		CountryCode:  taxedCountry,
		// The filter is narrowed down by PROFILE: the options of other
		// scenarios sharing the same database live in the same region too, and
		// an unfiltered listing would tie the test's outcome to the run order.
		ShippingProfileIDs: []string{profileID},
		Subtotal:           shippingSubtotal,
		ItemCount:          shippingQuantity,
	})

	require.Len(t, options, 1,
		"exactly ONE option bound to the profile must come back; any other number "+
			"shows that either the filter was not applied or the option was not "+
			"considered eligible")
	require.Equal(t, optionID, options[0].ID, "the returned option must be the one just set up")
	require.Equal(t, shippingOptionFee, options[0].Amount,
		"the fee of a flat-rate option must be the amount written into the catalog")
	require.Equal(t, taxedCurrency, options[0].CurrencyCode,
		"the fee must come back in the cart's currency; another currency would mean "+
			"summing up amounts stated in two different units")
	require.Equal(t, fulfillmentmanual.ID, options[0].ProviderID,
		"the provider that will carry out the option must be the manual provider that "+
			"comes out of the box")
	require.False(t, options[0].AdminOnly,
		"an option open to the storefront must NOT be admin_only")

	// --- 2) fulfillment for the order ---

	key := "e2e-fulfillment-" + orderResult.OrderID
	fulfillmentID, err := shippingInterop.CreateFulfillment(ctx, orderResult.OrderID, optionID, key)
	require.NoError(t, err, "a fulfillment must be openable for the order")
	require.NotEmpty(t, fulfillmentID, "a fulfillment identity must come back")

	fulfillment, err := shippingSvc.GetFulfillment(ctx, fulfillmentID)
	require.NoError(t, err, "the fulfillment must be readable from the fulfillment module")
	require.Equal(t, orderResult.OrderID, fulfillment.Reference,
		"the fulfillment must carry the ORDER's identity as its reference. fulfillment "+
			"does not validate this field (it is not a foreign key); a wrong or empty "+
			"reference trips no constraint, it merely makes reconciliation impossible — "+
			"the shipping label ends up printed without anyone knowing which order it "+
			"belongs to")
	require.Equal(t, optionID, fulfillment.ShippingOptionID,
		"the fulfillment must document which shipping option it was opened with")
	require.Equal(t, fulfillmentmanual.ID, fulfillment.ProviderID,
		"the fulfillment must be opened with the option's provider")
	require.Equal(t, fulfillmentmodels.StatusPending, fulfillment.Status,
		"a new fulfillment must come out 'pending': the record has been opened but the "+
			"carrier has not taken delivery yet")
	require.NotEmpty(t, fulfillment.ExternalID,
		"the provider's own fulfillment identity must have been written. After an "+
			"ambiguous provider error this is the ONLY field that can match the two "+
			"systems up")

	status, err := shippingInterop.FulfillmentStatus(ctx, fulfillmentID)
	require.NoError(t, err, "the fulfillment status must be readable from the surface")
	require.Equal(t, fulfillmentmodels.StatusPending.String(), status,
		"the status the surface reports must be the same as the record's")

	// --- 3) is fulfillment creation IDEMPOTENT ---

	repeatID, err := shippingInterop.CreateFulfillment(ctx, orderResult.OrderID, optionID, key)
	require.NoError(t, err, "a second call with the same key must NOT return an error")
	require.Equal(t, fulfillmentID, repeatID,
		"the same idempotency key must return the EXISTING fulfillment. A new identity "+
			"coming back would mean that a retried saga step had a SECOND SHIPPING LABEL "+
			"printed")

	reference := orderResult.OrderID
	fulfillments, count, err := shippingSvc.ListFulfillments(ctx, fulfillmentsvc.ListFulfillmentsInput{
		Reference: &reference,
	})
	require.NoError(t, err, "the order's fulfillments must be listable")
	require.Equal(t, int64(1), count,
		"there must be exactly ONE fulfillment in the order's ledger; a second row shows "+
			"that idempotency was provided in the returned value only and that an extra "+
			"row was written into the ledger")
	require.Len(t, fulfillments, 1,
		"the number of listed fulfillments must be consistent with the counter")
	require.Equal(t, fulfillmentID, fulfillments[0].ID,
		"the listed fulfillment must be the one that was opened")

	// --- 4) is cancellation IDEMPOTENT (saga compensation) ---

	require.NoError(t, shippingInterop.CancelFulfillment(ctx, fulfillmentID),
		"the fulfillment must be cancellable")

	canceled, err := shippingSvc.GetFulfillment(ctx, fulfillmentID)
	require.NoError(t, err, "a canceled fulfillment must be readable")
	require.Equal(t, fulfillmentmodels.StatusCanceled, canceled.Status,
		"the fulfillment must be 'canceled'; the record is NOT DELETED, because had it "+
			"been deleted a second cancellation call would return an 'unknown identity' "+
			"error and the compensation could not be run again")
	require.NotNil(t, canceled.CanceledAt, "the moment of cancellation must be stamped")

	require.NoError(t, shippingInterop.CancelFulfillment(ctx, fulfillmentID),
		"the SECOND cancellation call must NOT return an error either. The compensation "+
			"being re-runnable is not a preference, it is the saga's condition of "+
			"operation: a retried rollback chain calls the same step twice")

	secondRead, err := shippingSvc.GetFulfillment(ctx, fulfillmentID)
	require.NoError(t, err, "the fulfillment must still be readable after the second cancellation")
	require.Equal(t, fulfillmentmodels.StatusCanceled, secondRead.Status,
		"the status must stay 'canceled'")
	require.Equal(t, canceled.CanceledAt, secondRead.CanceledAt,
		"the cancellation stamp must NOT CHANGE; a change would show that the second "+
			"call rewrote the record and that the provider was therefore gone to a second "+
			"time as well")
	require.Equal(t, canceled.UpdatedAt, secondRead.UpdatedAt,
		"the record must not be UPDATED AT ALL on the second call. Idempotency does not "+
			"mean 'produce the same result', it means 'do nothing the second time'; "+
			"otherwise every retry would write a new movement into the reconciliation "+
			"ledger")

	err = shippingInterop.CancelFulfillment(ctx, fulfillmentmodels.NewFulfillmentID())
	require.Error(t, err, "cancelling an UNKNOWN identity must return an error")
	require.True(t, errors.IsNotFound(err),
		"the error must be NotFound, it must not be swallowed silently. Idempotency does "+
			"not mean 'accept everything': a REAL fulfillment canceled twice and an "+
			"identity that never existed are different situations, and the second one is "+
			"a bug on the caller's side (got: %v)", err)
}

// TestStoreSurfaceHidesAdminOnlyOption verifies that an admin_only shipping
// option does NOT REACH the storefront.
//
// Two options are set up on the same profile and in the same region; the only
// difference is the admin_only flag. The admin-only one is deliberately
// CHEAPER: because the list is sorted ascending by fee, it would show up in
// first place had it leaked, which means its absence cannot be explained by
// sorting.
//
// The same context is queried through two surfaces. While the store endpoint
// does not see the option AT ALL, the cross-module surface that turns the
// admin flag on returns both of them; without the second query, the option not
// appearing in the storefront could have been explained by "was not found
// eligible" or "was never created" just as well as by "was hidden".
func TestStoreSurfaceHidesAdminOnlyOption(t *testing.T) {
	ctx := t.Context()

	profileID := newShippingProfile(ctx, t, "E2E Storefront Profile")
	storefrontID := newShippingOption(ctx, t, profileID, "E2E Storefront Shipping", shippingOptionFee, false)
	adminID := newShippingOption(ctx, t, profileID, "E2E Admin Shipping", shippingAdminFee, true)

	query := url.Values{}
	query.Set("region_id", taxedRegionID)
	query.Set("currency_code", taxedCurrency)
	query.Set("country_code", taxedCountry)
	query.Set("shipping_profile_id", profileID)
	query.Set("subtotal", strconv.FormatInt(shippingSubtotal, 10))
	query.Set("item_count", strconv.FormatInt(shippingQuantity, 10))

	storefront := storeShippingOptions(t, query)

	require.Len(t, storefront, 1,
		"exactly ONE option must come back from the store endpoint: only the one that "+
			"is open to the storefront")
	require.Equal(t, storefrontID, storefront[0]["id"],
		"the returned option must be the option that is NOT admin_only")
	require.Equal(t, float64(shippingOptionFee), storefront[0]["amount"],
		"the fee in the storefront must be the amount written into the catalog")

	for _, record := range storefront {
		require.NotEqual(t, adminID, record["id"],
			"an admin_only option must NOT APPEAR in the storefront. The filter is in "+
				"SQL and the row is never even read on the store path; its leaking would "+
				"mean opening a shipping agreement reserved for the admin surface, "+
				"together with its price, to the customer")
	}

	// The storefront representation must not LEAK the catalog's internals either.
	require.NotContains(t, storefront[0], "provider_id",
		"the store representation must not carry the provider identity; which carrier "+
			"the shop works with is the shop's operational information")
	require.NotContains(t, storefront[0], "admin_only",
		"the store representation must not carry the admin_only field at all; the field "+
			"exists in the admin representation only, and the two DTOs being separate "+
			"prevents the leak structurally")
	require.NotContains(t, storefront[0], "shipping_profile_id",
		"the store representation must not carry the profile identity; the profile is "+
			"an internal of the catalog")

	// --- does the option REALLY exist and is it eligible: same context, admin flag ---

	all := listShippingOptions(ctx, t, shippingOptionRequest{
		RegionID:           taxedRegionID,
		CurrencyCode:       taxedCurrency,
		CountryCode:        taxedCountry,
		ShippingProfileIDs: []string{profileID},
		Subtotal:           shippingSubtotal,
		ItemCount:          shippingQuantity,
		IncludeAdminOnly:   true,
	})

	require.Len(t, all, 2,
		"with the admin flag on, BOTH options must come back; had they not, the absence "+
			"in the storefront would be explained not by hiding but by the option never "+
			"having been eligible at all")
	require.Equal(t, adminID, all[0].ID,
		"the list must be sorted ASCENDING by fee and the cheaper admin option must come "+
			"first; this settles that the reason the option does not appear in the "+
			"storefront is neither sorting nor truncation")
	require.True(t, all[0].AdminOnly, "the first option must be admin_only")
	require.Equal(t, storefrontID, all[1].ID,
		"the second option must be the one that is open to the storefront")
	require.False(t, all[1].AdminOnly, "the second option must NOT be admin_only")
}

// newShippingProfile opens a shipping profile for the test and returns its
// identity.
//
// The name is made unique: fulfillment rejects a second profile with the same
// name, and the tests share a single database.
func newShippingProfile(ctx context.Context, t *testing.T, name string) string {
	t.Helper()

	profile, err := shippingSvc.CreateShippingProfile(ctx, fulfillmentsvc.CreateProfileInput{
		Name: fmt.Sprintf("%s %d", name, fixtureCounter.Add(1)),
	})
	require.NoError(t, err, "the fixture shipping profile could not be created")
	return profile.ID
}

// newShippingOption opens a FLAT-rate shipping option on the given profile and
// returns its identity.
//
// The option is ruleless, that is, it is eligible unconditionally: what this
// file tests is not the eligibility rules but the admin_only filter together
// with the fulfillment lifecycle. An option with rules would never have been
// listed at the store endpoint anyway, because cart facts cannot be verified
// there, and the two separate reasons would have got mixed up with each other.
func newShippingOption(
	ctx context.Context,
	t *testing.T,
	profileID, name string,
	fee int64,
	adminOnly bool,
) string {
	t.Helper()

	option, err := shippingSvc.CreateShippingOption(ctx, fulfillmentsvc.CreateOptionInput{
		Name:              fmt.Sprintf("%s %d", name, fixtureCounter.Add(1)),
		ProviderID:        fulfillmentmanual.ID,
		ShippingProfileID: profileID,
		Amount:            fee,
		CurrencyCode:      taxedCurrency,
		RegionID:          taxedRegionID,
		AdminOnly:         adminOnly,
	})
	require.NoError(t, err, "the fixture shipping option could not be created")
	return option.ID
}

// listShippingOptions lists the eligible shipping options through the
// cross-module surface.
//
// The request and the response are carried by JSON schemas defined SEPARATELY
// in the two packages, and the other side REJECTS unknown fields; should a
// field name drift, the call fails right here with an explicit error.
func listShippingOptions(
	ctx context.Context,
	t *testing.T,
	request shippingOptionRequest,
) []shippingOption {
	t.Helper()

	body, err := json.Marshal(request)
	require.NoError(t, err, "the shipping option request could not be encoded")

	raw, err := shippingInterop.ListOptionsJSON(ctx, body)
	require.NoError(t, err, "the shipping options could not be listed")

	var response shippingOptionResponse
	require.NoError(t, json.Unmarshal(raw, &response),
		"the shipping option response could not be decoded; the schema may have drifted "+
			"apart in the two packages")
	return response.Options
}

// storeShippingOptions calls the /store/v1/shipping-options endpoint and
// returns the records that come back as UNDECODED maps.
//
// Decoding the records into a map rather than into a typed struct is
// deliberate: which fields are NOT PRESENT in the store representation is an
// assertion too, and a typed struct would silently drop an extra field
// standing in the response.
func storeShippingOptions(t *testing.T, query url.Values) []map[string]any {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet,
		"/store/v1/shipping-options?"+query.Encode(), http.NoBody)
	// The store surface has been demanding a publishable key since Phase 8; a
	// request without a key becomes a 401 before it even reaches the router
	// (see identity_test.go).
	request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code,
		"the store endpoint must return 200; body: %s", recorder.Body.String())

	var envelope struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"the store response could not be decoded; body: %s", recorder.Body.String())
	require.Equal(t, int64(len(envelope.Data)), envelope.Count,
		"the counter in the envelope must be consistent with the number of returned "+
			"records")

	return envelope.Data
}

// TestAShipmentOpenedThroughTheFlowIsBoundToItsOrder closes the gap the test
// above names in its own words.
//
// That test opens a fulfillment through the fulfillment module's interop and
// then says, at the assertion on Reference, exactly what is wrong with it: the
// field is not a foreign key, a wrong or empty value trips no constraint, and
// "the shipping label ends up printed without anyone knowing which order it
// belongs to". Reference is a string the module never validates, by decision.
//
// The fulfilling flow is what makes the association a FACT rather than a
// convention, and this is the only place it can be proved: the binding is a
// row written by the real link service under a definition the real fulfillment
// module declared, and a fake link store agreeing with itself says nothing
// about either.
func TestAShipmentOpenedThroughTheFlowIsBoundToItsOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Bound Shipment Product", map[string]int64{
		taxedCurrency: shippingUnitPrice,
	}, shippingStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, shippingQuantity)
	orderResult, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     shippingTotal,
	})
	require.NoError(t, err)

	profileID := newShippingProfile(ctx, t, "E2E Bound Shipment Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Bound Shipping", shippingOptionFee, false)

	flow, err := fulfillingwf.FromContainer(ctr)
	require.NoError(t, err,
		"the fulfilling flow must resolve from the same container the composition root uses; "+
			"a surface it cannot find is one no installation has")

	key := "e2e-bound-" + orderResult.OrderID
	opened, err := flow.OpenForOrder(ctx, orderResult.OrderID, optionID, key)
	require.NoError(t, err)
	require.NotEmpty(t, opened.FulfillmentID)
	assert.False(t, opened.AlreadyOpen, "the first press cannot be a repeat")

	// The binding is read through the LINK SERVICE rather than through the flow:
	// asking the flow whether it wrote what the flow says it wrote would prove
	// nothing about the row.
	bound, err := links.ListMany(ctx, fulfillingwf.LinkOrderFulfillment,
		[]string{orderResult.OrderID})
	require.NoError(t, err)
	assert.Equal(t, []string{opened.FulfillmentID}, bound[orderResult.OrderID],
		"the shipment was opened and NOT bound to its order; nothing can answer which "+
			"order the parcel belongs to, which is the whole point of the flow")

	// A second press with the same key: no second parcel, no second binding,
	// and the repeat is REPORTED.
	repeat, err := flow.OpenForOrder(ctx, orderResult.OrderID, optionID, key)
	require.NoError(t, err)
	assert.Equal(t, opened.FulfillmentID, repeat.FulfillmentID,
		"the same idempotency key opened a SECOND parcel")
	assert.True(t, repeat.AlreadyOpen,
		"the second press was reported as new; an operator would believe two parcels exist")

	stillBound, err := links.ListMany(ctx, fulfillingwf.LinkOrderFulfillment,
		[]string{orderResult.OrderID})
	require.NoError(t, err)
	assert.Len(t, stillBound[orderResult.OrderID], 1,
		"the repeat wrote a second binding row")

	shipments, err := flow.ShipmentsOfOrder(ctx, orderResult.OrderID)
	require.NoError(t, err)
	require.Len(t, shipments, 1, "the order's shipments could not be read back")
	assert.Equal(t, opened.FulfillmentID, shipments[0].FulfillmentID)
	assert.Equal(t, string(fulfillmentmodels.StatusPending), shipments[0].Status,
		"the status came back empty, which is what the flow reports when the fulfillment "+
			"module could not be asked at all")

	// An order that does not exist opens NOTHING. The fulfillment module cannot
	// refuse it — it never validates the reference — so this flow is the only
	// place the refusal can happen.
	_, err = flow.OpenForOrder(ctx, "order_does_not_exist", optionID, key+"-missing")
	require.Error(t, err, "a parcel was opened for an order that does not exist")

	// --- a SECOND parcel on the same order ---
	//
	// The cardinality is one to many and the reason is concrete: an order can
	// ship in several parcels. Asserting it with one parcel would leave both
	// halves untested — the constraint would accept the second row either way,
	// and a FetchByIDs that read only the first id would still look correct.
	secondOpened, err := flow.OpenForOrder(ctx, orderResult.OrderID, optionID, key+"-2")
	require.NoError(t, err,
		"a second parcel could not be opened for the order; the link declares one to many")
	require.NotEqual(t, opened.FulfillmentID, secondOpened.FulfillmentID)
	assert.False(t, secondOpened.AlreadyOpen, "a different key is not a repeat")

	// --- the binding is EXPANDABLE, not merely readable ---
	//
	// A link whose far side has no Query provider can be read through the link
	// service and cannot be walked by a Graph request — and an expansion is what
	// an order's timeline is made of. That is why the shipment got a provider of
	// its own; this is the assertion that says the two halves meet.
	catalog, err := container.Resolve[query.Query](ctr, svcQuery)
	require.NoError(t, err)

	records, err := catalog.Graph(ctx, query.GraphSpec{
		Entity:  "order",
		Fields:  []string{query.IDField},
		Filters: map[string]any{query.IDField: orderResult.OrderID},
		Limit:   1,
		Expand: []query.Expansion{{
			Link:   fulfillingwf.LinkOrderFulfillment,
			As:     "shipments",
			Fields: []string{query.IDField, "status", "tracking_number", "shipped_at"},
		}},
	})
	require.NoError(t, err,
		"the order_fulfillment link could not be walked by the read layer")
	require.Len(t, records, 1)

	expanded, ok := records[0]["shipments"].([]query.Record)
	require.True(t, ok,
		"the expansion produced no shipment records; the far side of the link has no "+
			"provider, so the timeline cannot be assembled from a single request")
	require.Len(t, expanded, 2,
		"both parcels have to come back in ONE request; a FetchByIDs that reads the ids "+
			"one at a time is the N+1 the read layer exists to make impossible")

	byID := map[string]query.Record{}
	for _, record := range expanded {
		id, isText := record[query.IDField].(string)
		require.True(t, isText)
		byID[id] = record
	}
	require.Contains(t, byID, opened.FulfillmentID)
	require.Contains(t, byID, secondOpened.FulfillmentID)

	assert.Equal(t, string(fulfillmentmodels.StatusPending),
		byID[opened.FulfillmentID]["status"])
	assert.Nil(t, byID[opened.FulfillmentID]["shipped_at"],
		"a shipment that has not shipped must report a NULL moment rather than a zero time; "+
			"a zero time reads as 1 January year one on a timeline")
}

// TestAnOrderCanSayWHENItsMoneyMoved is the money half of an order's timeline.
//
// Until now an order could say HOW MUCH at every stage — the payment
// collection's amounts come through the order_payment link — and could not say
// WHEN any of it happened. The amounts are on the collection row; the moments
// are not, because a capture is a payments row and a refund is a refunds row,
// and neither table had a reader an order could reach.
//
// This is the only place the chain can be proved: the aggregate is SQL, the
// field is the payment module's Query provider, and the read is the order
// module's — three layers that an in-memory fake agreeing with itself says
// nothing about.
func TestAnOrderCanSayWHENItsMoneyMoved(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Money Moment Product", map[string]int64{
		taxedCurrency: shippingUnitPrice,
	}, shippingStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, shippingQuantity)

	before := time.Now().UTC()
	orderResult, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     shippingTotal,
	})
	require.NoError(t, err)
	after := time.Now().UTC()

	payment, bound, err := orderSvc.PaymentOf(ctx, orderResult.OrderID)
	require.NoError(t, err)
	require.True(t, bound, "the order has no payment collection bound to it")

	require.NotNil(t, payment.FirstCapturedAt,
		"the order cannot say WHEN it was paid. The money moved — the checkout saga "+
			"captured it — so a nil here means the moment is written in the payments table "+
			"and no layer between it and the order carries it")
	captured := *payment.FirstCapturedAt
	assert.False(t, captured.Before(before.Add(-time.Minute)),
		"the capture moment is older than the test itself: %s", captured)
	assert.False(t, captured.After(after.Add(time.Minute)),
		"the capture moment is in the future: %s", captured)

	assert.Nil(t, payment.LastRefundedAt,
		"an order that was never refunded reported a refund moment; a zero time would "+
			"read as 1 January year one on a timeline, and a wrong time is worse than none")

	// The amounts still come back on the same read: asking for the moments must
	// not cost the caller the figures it was already getting.
	assert.Positive(t, payment.CapturedAmount,
		"the captured amount was lost when the moment fields were added")
	assert.Equal(t, taxedCurrency, payment.CurrencyCode)
}

// TestTheOrderTimelineComposesWhatTheModulesRecord is the support desk's view,
// end to end.
//
// It is the only place the composition can be proved: the moments come from
// three modules — the order's own row, the payment collection's capture, and
// each parcel's transitions — reached through two links that their far sides
// declared. Every layer under it has its own tests and none of them can say
// that the pieces meet.
func TestTheOrderTimelineComposesWhatTheModulesRecord(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Timeline Product", map[string]int64{
		taxedCurrency: shippingUnitPrice,
	}, shippingStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, shippingQuantity)
	orderResult, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     shippingTotal,
	})
	require.NoError(t, err)

	profileID := newShippingProfile(ctx, t, "E2E Timeline Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Timeline Shipping", shippingOptionFee, false)

	flow, err := fulfillingwf.FromContainer(ctr)
	require.NoError(t, err)
	opened, err := flow.OpenForOrder(ctx, orderResult.OrderID, optionID,
		"e2e-timeline-"+orderResult.OrderID)
	require.NoError(t, err)

	entries, err := orderSvc.Timeline(ctx, orderResult.OrderID)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	byKind := map[string]ordersvc.TimelineEntry{}
	for _, entry := range entries {
		byKind[entry.Kind] = entry
	}

	require.Contains(t, byKind, ordersvc.KindOrderPlaced,
		"the order's own moment is missing from its timeline")
	assert.Equal(t, orderResult.OrderID, byKind[ordersvc.KindOrderPlaced].RefID)
	assert.Equal(t, ordersvc.ClockDatabase, byKind[ordersvc.KindOrderPlaced].Clock)

	require.Contains(t, byKind, ordersvc.KindPaymentCaptured,
		"the money moment is missing; the timeline did not reach the payment module "+
			"through the order_payment link")
	assert.Equal(t, ordersvc.ClockApplication, byKind[ordersvc.KindPaymentCaptured].Clock,
		"the capture is stamped by the process that captured, and the timeline has to say so")
	assert.Positive(t, byKind[ordersvc.KindPaymentCaptured].Amount)

	require.Contains(t, byKind, ordersvc.KindShipmentOpened,
		"the parcel is missing; the timeline did not reach the fulfillment module "+
			"through the order_fulfillment link")
	assert.Equal(t, opened.FulfillmentID, byKind[ordersvc.KindShipmentOpened].RefID)

	// A parcel that has not shipped contributes ONE moment, not four: the three
	// transitions are null and a null moment is not an event.
	assert.NotContains(t, byKind, ordersvc.KindShipmentShipped,
		"a parcel that never shipped reported a shipping moment")

	// Newest first, and every dated entry before every undated one.
	seenUndated := false
	for i := range entries {
		if entries[i].At == nil {
			seenUndated = true

			continue
		}
		assert.False(t, seenUndated,
			"a dated entry came after an undated one; the undated have to be last")
		if i > 0 && entries[i-1].At != nil {
			assert.False(t, entries[i-1].At.Before(*entries[i].At),
				"the timeline is not newest first at %d", i)
		}
	}
}

// TestTheCartsAddressReachesTheOrder closes the gap the cart's own schema
// comment describes.
//
// cart_addresses says the address is COPIED from the customer's address book so
// that a shopper who later edits it does not rewrite history — and the comment
// names the thing being protected: "the past cart (and the order born out of
// it)". The order born out of it had no address at all. The cart is reused or
// dropped after checkout, so the copy the cart made protected nothing.
//
// This is the only place the chain can be proved: the cart's own table, its
// interop surface, the checkout plan, the order snapshot and the order's
// transaction are five layers, and each has tests that cannot see the next.
func TestTheCartsAddressReachesTheOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Address Product", map[string]int64{
		taxedCurrency: shippingUnitPrice,
	}, shippingStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, shippingQuantity)

	shipping := cartsvc.AddressInput{
		FirstName: "Ayse", LastName: "Yilmaz", Company: "Gobit AS",
		Address1: "Ataturk Cad. 12", Address2: "Daire 3",
		City: "Istanbul", Province: "Kadikoy", PostalCode: "34710",
		CountryCode: taxedCountry, Phone: "+905551112233",
	}
	_, err := cartSvc.SetShippingAddress(ctx, cartID, shipping)
	require.NoError(t, err)

	billing := shipping
	billing.Company = "Gobit Muhasebe"
	billing.Address1 = "Bagdat Cad. 99"
	_, err = cartSvc.SetBillingAddress(ctx, cartID, billing)
	require.NoError(t, err)

	orderResult, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     shippingTotal,
	})
	require.NoError(t, err)

	order, err := orderSvc.GetOrder(ctx, orderResult.OrderID)
	require.NoError(t, err)

	require.NotNil(t, order.ShippingAddress,
		"the order has no shipping address. The cart had one and the order is what "+
			"survives checkout: without it nothing can say where the parcel goes")
	assert.Equal(t, "Ataturk Cad. 12", order.ShippingAddress.Address1)
	assert.Equal(t, "Kadikoy", order.ShippingAddress.Province,
		"the province was lost on the way; a domestic carrier prices on the district")
	assert.Equal(t, "34710", order.ShippingAddress.PostalCode)
	assert.Equal(t, taxedCountry, order.ShippingAddress.CountryCode)
	assert.Equal(t, ordermodels.AddressShipping, order.ShippingAddress.Type)

	require.NotNil(t, order.BillingAddress,
		"the order has no billing address; an invoice cannot print a buyer")
	assert.Equal(t, "Gobit Muhasebe", order.BillingAddress.Company)
	assert.Equal(t, "Bagdat Cad. 99", order.BillingAddress.Address1,
		"the billing address came back with the SHIPPING address's street; the two "+
			"were not kept apart")
	assert.Equal(t, ordermodels.AddressBilling, order.BillingAddress.Type)

	// The two are separate rows on one order, which is what the unique index on
	// (order_id, address_type) allows and no more.
	assert.NotEqual(t, order.ShippingAddress.ID, order.BillingAddress.ID)
}
