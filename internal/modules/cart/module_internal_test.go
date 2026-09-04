package cart

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
)

// The test lives in the INTERNAL package because the types under test
// ([cartOpening], [linePricing], [cartCompletion]) are unexported and must stay
// unexported: all three are this module's own wiring detail, not a surface to
// be set up from outside.

// stubPricing is the fake pricing flow registered with the container.
type stubPricing struct {
	lineID string
	calls  int
}

// That the fake satisfies the expected surface is pinned down at compile time.
var _ api.LinePricing = (*stubPricing)(nil)

// AddPricedLineItem returns the line's id.
func (s *stubPricing) AddPricedLineItem(
	_ context.Context, _, _ string, _ int64, _ json.RawMessage,
) (string, error) {
	s.calls++
	return s.lineID, nil
}

// SetLineItemQuantity reports that the line was not removed.
func (s *stubPricing) SetLineItemQuantity(_ context.Context, _, _ string, _ int64) (bool, error) {
	s.calls++
	return false, nil
}

// foreignType is the type that stands under the right NAME in the container but
// does NOT satisfy the surface.
type foreignType struct{}

// silentLog is the logger that leaves the test's output to the assertions.
func silentLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestLinePricingFlowIsResolvedByName verifies that the flow is resolved from
// the container BY NAME and that the surface is structurally satisfied.
//
// The binding does NOT exist at compile time: the module cannot import
// internal/workflows (ADR 0006), so the only thing tying the concrete flow to
// this interface is the [CartFlowsName] string. The test pins down that this
// string really is the resolution key.
func TestLinePricingFlowIsResolvedByName(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	flow := &stubPricing{lineID: "li_1"}
	require.NoError(t, c.Provide(CartFlowsName, flow))

	wrapper := &linePricing{c: c, log: silentLog()}
	lineID, err := wrapper.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)

	require.NoError(t, err)
	assert.Equal(t, "li_1", lineID)
	assert.Equal(t, 1, flow.calls)
}

// TestLinePricingFlowFailsClosedWhenMissing verifies that the pricing path does
// NOT SILENTLY CARRY ON under incomplete wiring.
//
// What this test exercises is the deliberate difference from the order module's
// spending rule: there, if the provider is missing the rule is not applied and
// the flow continues ("no limit" is the right answer), whereas here, if the
// flow is missing the line is NOT ADDED AT ALL. Carrying on with a zero price
// or with the price the client sent would be selling goods for free, silently.
func TestLinePricingFlowFailsClosedWhenMissing(t *testing.T) {
	t.Parallel()

	wrapper := &linePricing{c: container.New(nil), log: silentLog()}

	lineID, err := wrapper.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)
	require.Error(t, err, "an unresolvable flow must return an error")
	assert.Empty(t, lineID)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"an unregistered name produces KindNotFound in the container; had it been "+
			"inherited the endpoint would return 404 and tell the client \"there is "+
			"no such endpoint\" — whereas the failure is IN THE SERVER "+
			"CONFIGURATION and the 5xx alert must ring")
	assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(err),
		"the assertion is pinned not on the kind's name but on the mapping IN "+
			"PRODUCTION: this is the only place that determines which status code "+
			"the endpoint really returns")
	assert.Contains(t, err.Error(), CartFlowsName,
		"the error must write which name could not be resolved")

	// The quantity update path goes through the same gate.
	_, err = wrapper.SetLineItemQuantity(t.Context(), "cart_1", "li_1", 5)
	assert.Error(t, err)
}

// TestLinePricingFlowRejectsIncompatibleType verifies that a type registered
// under the right name but not satisfying the surface does NOT get a line
// written either.
//
// "Not registered" and "registered but not recognized" are different failures;
// the outcome of both must be the same, because in both there is no party to
// determine the price.
func TestLinePricingFlowRejectsIncompatibleType(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(CartFlowsName, foreignType{}))

	wrapper := &linePricing{c: c, log: silentLog()}
	_, err := wrapper.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)

	require.Error(t, err)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"a registration of the wrong type produces KindInvalid in the container; "+
			"had it been inherited the endpoint would say \"your body is invalid\" "+
			"with 422, whereas the outcome would be the same even with a flawless body")
}

// TestLinePricingDecisionIsMadeOnce verifies that the resolution is not
// repeated on every request.
//
// The flows are registered at startup, before the first request; a name that
// was not found at the first resolution will not be found later either.
// Retrying on every request would do nothing but reproduce the same error
// forever — and the decision being changeable would mean the store silently
// changing its behavior after startup.
func TestLinePricingDecisionIsMadeOnce(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	wrapper := &linePricing{c: c, log: silentLog()}

	_, err := wrapper.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)
	require.Error(t, err)

	// Even if the flow is registered LATER, the decision does not change.
	require.NoError(t, c.Provide(CartFlowsName, &stubPricing{lineID: "li_1"}))

	_, err = wrapper.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)
	assert.Error(t, err, "the decision is made once and stored")
}

// stubCompletion is the fake completion flow registered with the container.
type stubCompletion struct{ response json.RawMessage }

// That the fake satisfies the expected surface is pinned down at compile time.
var _ api.CartCompletion = (*stubCompletion)(nil)

// CompleteCartJSON returns the scripted response.
func (s *stubCompletion) CompleteCartJSON(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return s.response, nil
}

// TestCartCompletionFlowIsResolvedByName verifies that the completion flow is
// resolved BY NAME too.
func TestCartCompletionFlowIsResolvedByName(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(CartCompletionName,
		&stubCompletion{response: json.RawMessage(`{"order_id":"order_1"}`)}))

	wrapper := &cartCompletion{c: c, log: silentLog()}
	response, err := wrapper.CompleteCartJSON(t.Context(), json.RawMessage(`{"cart_id":"cart_1"}`))

	require.NoError(t, err)
	assert.JSONEq(t, `{"order_id":"order_1"}`, string(response))
}

// TestCartCompletionFlowFailsClosedWhenMissing verifies that the cart is NOT
// completed without the flow.
//
// There can be no shortcut called "consider the cart completed": if there is no
// flow there is no order, no payment and no stock reservation either.
func TestCartCompletionFlowFailsClosedWhenMissing(t *testing.T) {
	t.Parallel()

	wrapper := &cartCompletion{c: container.New(nil), log: silentLog()}
	response, err := wrapper.CompleteCartJSON(t.Context(), json.RawMessage(`{"cart_id":"cart_1"}`))

	require.Error(t, err)
	assert.Nil(t, response)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"the completion endpoint must report the setup failure as 5xx too")
}

// TestFlowNamesAreContracts pins down the value of the container names.
//
// The names belong to the internal/workflows packages and are repeated in this
// module as STRINGS (modules cannot import workflow packages). The price of the
// repetition is that when one side changes the name the other silently fails to
// resolve; this test at least pins the value down in ONE place and forces the
// change to be deliberate.
func TestFlowNamesAreContracts(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "workflows.cart.interop", CartFlowsName,
		"the name must be the same as the cart flows' InteropName constant")
	assert.Equal(t, "workflows.checkout.interop", CartCompletionName,
		"the name must be the same as the checkout flow's InteropName constant")
}

// stubOpening is the fake cart opening flow registered with the container.
type stubOpening struct {
	cartID string
	calls  int
}

// That the fake satisfies the expected surface is pinned down at compile time.
var _ api.CartOpening = (*stubOpening)(nil)

// OpenCartForCountry returns the cart's id.
func (s *stubOpening) OpenCartForCountry(
	_ context.Context, _, _, _ string, _ json.RawMessage,
) (string, error) {
	s.calls++
	return s.cartID, nil
}

// TestCartOpeningFlowIsResolvedByName verifies that the cart opening flow is
// resolved from the container BY NAME and that the surface is structurally
// satisfied.
//
// The binding does NOT exist at compile time: the module cannot import
// internal/workflows (ADR 0006), so the only thing tying the concrete flow to
// this interface is the [CartFlowsName] string. The test pins down that this
// string really is the resolution key — and, together with
// [TestLinePricingFlowIsResolvedByName], shows that the same name feeds line
// pricing as well.
func TestCartOpeningFlowIsResolvedByName(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	flow := &stubOpening{cartID: "cart_1"}
	require.NoError(t, c.Provide(CartFlowsName, flow))

	wrapper := &cartOpening{c: c, log: silentLog()}
	cartID, err := wrapper.OpenCartForCountry(t.Context(), "TR", "", "", nil)

	require.NoError(t, err)
	assert.Equal(t, "cart_1", cartID)
	assert.Equal(t, 1, flow.calls)
}

// TestCartOpeningFlowFailsClosedWhenMissing verifies that the cart opening path
// does NOT SILENTLY CARRY ON when the region cannot be derived.
//
// The cart's region selects the tax rate, and the currency derived from it
// selects which price list is applied. Falling back to a default or using what
// the client said would reopen exactly the authorization gate that was closed;
// the only right outcome is for the cart NOT TO BE OPENED AT ALL.
func TestCartOpeningFlowFailsClosedWhenMissing(t *testing.T) {
	t.Parallel()

	wrapper := &cartOpening{c: container.New(nil), log: silentLog()}

	cartID, err := wrapper.OpenCartForCountry(t.Context(), "TR", "", "", nil)
	require.Error(t, err, "an unresolvable flow must return an error")
	assert.Empty(t, cartID, "the cart must NEVER be opened")
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"an unregistered name produces KindNotFound in the container; had it been "+
			"inherited the cart opening endpoint would return 404 and tell the "+
			"client \"there is no such endpoint\" — whereas the failure is IN THE "+
			"SERVER CONFIGURATION")
	assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(err),
		"the assertion is pinned not on the kind's name but on the mapping IN PRODUCTION")
	assert.Contains(t, err.Error(), CartFlowsName,
		"the error must write which name could not be resolved")
}

// TestCartOpeningFlowRejectsIncompatibleType verifies that a type registered
// under the right name but not satisfying the surface does NOT get a cart
// opened either.
//
// "Not registered" and "registered but not recognized" are different failures;
// the outcome of both must be the same, because in both there is no party to
// derive the region.
func TestCartOpeningFlowRejectsIncompatibleType(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(CartFlowsName, foreignType{}))

	wrapper := &cartOpening{c: c, log: silentLog()}
	cartID, err := wrapper.OpenCartForCountry(t.Context(), "TR", "", "", nil)

	require.Error(t, err)
	assert.Empty(t, cartID)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"a registration of the wrong type produces KindInvalid in the container; "+
			"had it been inherited the endpoint would say \"your body is invalid\" "+
			"with 422, whereas the outcome would be the same even with a flawless body")
}
