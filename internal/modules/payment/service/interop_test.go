package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// paymentInterop is the part of the PRIMITIVE surface the payment module offers
// the sagas (internal/workflows) that this file exercises.
//
// This is the test's real job: the saga CANNOT import this module (ADR 0006) and
// can only define an interface written in primitive types. That the concrete
// [service.Interop] type meets these signatures STRUCTURALLY is proved here by
// the compiler; a signature drift is not left to the moment of resolving from
// the container.
//
// It is not a copy of any workflow's interface: checkout.Payments also asks for
// CheckTender and CapturesLater and asks for none of OpenSession, Refund and
// SessionStatus, and the other flows declare their own subsets. The checkout,
// returns and gift card sale interfaces are pinned against [service.Interop] by
// the compiler in internal/arch (interop_pins_test.go).
type paymentInterop interface {
	CreateCollection(
		ctx context.Context, reference, customerID, currencyCode string, amount int64,
	) (string, error)
	OpenSession(ctx context.Context, collectionID, providerID, idempotencyKey string) (string, error)
	OpenSessionWithData(
		ctx context.Context,
		collectionID, providerID, idempotencyKey string,
		data json.RawMessage,
	) (string, error)
	Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error)
	Capture(ctx context.Context, sessionID string, amount int64) (string, error)
	Cancel(ctx context.Context, sessionID string) error
	Refund(ctx context.Context, paymentID string, amount int64, reason string) (string, error)
	Collection(ctx context.Context, collectionID string) (
		status string,
		amount, authorized, captured, refunded int64,
		err error,
	)
	SessionStatus(ctx context.Context, sessionID string) (string, error)
}

// That Interop meets the PRIMITIVE surface the saga expects is pinned at
// compile time.
var _ paymentInterop = (*service.Interop)(nil)

// newTestInterop builds an interop surface that runs over the fake store.
func newTestInterop(t *testing.T) (*service.Interop, *fakeProvider) {
	t.Helper()

	svc, _, prov := newTestService(t)
	return service.NewInterop(svc), prov
}

// TestInteropEndToEndFlow walks the path the saga will follow, through the
// primitive surface.
func TestInteropEndToEndFlow(t *testing.T) {
	iop, _ := newTestInterop(t)
	ctx := context.Background()

	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)
	assert.NotEmpty(t, colID)

	sesID, err := iop.OpenSession(ctx, colID, testProviderID, "key-1")
	require.NoError(t, err)

	status, authorized, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), status)
	assert.Equal(t, testAmount, authorized, "the surface must carry the authorized AMOUNT too")

	payID, err := iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, payID)

	colStatus, colAmount, colAuthorized, colCaptured, colRefunded := collectionOf(t, iop, colID)
	assert.Equal(t, models.CollectionCaptured.String(), colStatus)
	assert.Equal(t, testAmount, colAmount)
	assert.Zero(t, colAuthorized, "the capture closes the hold")
	assert.Equal(t, testAmount, colCaptured)
	assert.Zero(t, colRefunded)

	refundID, err := iop.Refund(ctx, payID, 0, "test refund")
	require.NoError(t, err)
	assert.NotEmpty(t, refundID)

	colStatus, _, _, _, colRefunded = collectionOf(t, iop, colID)
	assert.Equal(t, models.CollectionRefunded.String(), colStatus)
	assert.Equal(t, testAmount, colRefunded)
}

// collectionOf reads the collection's status and amounts through the interop
// surface.
func collectionOf(t *testing.T, iop *service.Interop, colID string) (
	status string,
	amount, authorized, captured, refunded int64,
) {
	t.Helper()

	status, amount, authorized, captured, refunded, err := iop.Collection(context.Background(), colID)
	require.NoError(t, err)
	return status, amount, authorized, captured, refunded
}

// TestInteropShortCaptureShowsInTheAmounts proves that the saga can verify by
// itself that the payment is FULL.
//
// Phase 6's payment bypass went through exactly here: because the surface only
// returned a status string, the saga had no number to check. Once the amounts
// are returned the rule is a single line — unless captured >= amount, the order
// is not confirmed.
func TestInteropShortCaptureShowsInTheAmounts(t *testing.T) {
	iop, prov := newTestInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)
	sesID, err := iop.OpenSession(ctx, colID, testProviderID, "key-1")
	require.NoError(t, err)
	prov.scenario(coreprovider.SessionAuthorized, 1, "")

	status, authorized, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), status,
		"a partial hold is a success too as far as the provider is concerned")
	assert.Equal(t, int64(1), authorized, "the saga must see the short hold FROM THE NUMBER")

	_, err = iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)

	colStatus, colAmount, _, colCaptured, _ := collectionOf(t, iop, colID)
	assert.Less(t, colCaptured, colAmount, "the payment is SHORT")
	assert.Equal(t, models.CollectionPartiallyCaptured.String(), colStatus,
		"a short capture must NOT make the collection captured")
}

// TestInteropSameKeyOneSession verifies that the saga retrying a step does not
// lead to a second attempt to charge the customer.
func TestInteropSameKeyOneSession(t *testing.T) {
	iop, prov := newTestInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)

	first, err := iop.OpenSession(ctx, colID, testProviderID, "key-1")
	require.NoError(t, err)
	second, err := iop.OpenSession(ctx, colID, testProviderID, "key-1")
	require.NoError(t, err)

	assert.Equal(t, first, second)
	create, _, _, _, _ := prov.calls()
	assert.Equal(t, 1, create)
}

// TestInteropCancelCanBeCalledTwice verifies that the saga's compensation is
// idempotent on the primitive surface too.
func TestInteropCancelCanBeCalledTwice(t *testing.T) {
	iop, _ := newTestInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)
	sesID, err := iop.OpenSession(ctx, colID, testProviderID, "key-1")
	require.NoError(t, err)
	_, _, err = iop.Authorize(ctx, sesID)
	require.NoError(t, err)

	require.NoError(t, iop.Cancel(ctx, sesID))
	require.NoError(t, iop.Cancel(ctx, sesID), "a second compensation must NOT fail")

	status, err := iop.SessionStatus(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled.String(), status)
}

// TestInteropAuthorizeDeclineReturnsAnError verifies that the saga's payment
// step FAILS.
//
// Phase 6's DoD requires it: when the payment step fails, the compensation chain
// must run, and for the chain to be triggered the step has to return an error.
func TestInteropAuthorizeDeclineReturnsAnError(t *testing.T) {
	iop, prov := newTestInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)
	sesID, err := iop.OpenSession(ctx, colID, testProviderID, "key-1")
	require.NoError(t, err)
	prov.scenario(coreprovider.SessionFailed, 0, "test decline")

	_, _, err = iop.Authorize(ctx, sesID)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeAuthorizationDeclined, errors.CodeOf(err))
}

// TestInteropOpenSessionWithDataDoesNotCorruptNumbers verifies that a behavior
// key carrying money reaches the provider without passing through floating
// point.
//
// An integer that passes through a map and turns into a float64 can slip into
// exponent notation ("1e+15") when it is encoded again, and then cannot be
// decoded as an integer on the provider's side. Money must never pass through
// floating point at any stage (plan Section 8).
func TestInteropOpenSessionWithDataDoesNotCorruptNumbers(t *testing.T) {
	iop, _ := newTestInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, models.MaxAmount)
	require.NoError(t, err)

	sesID, err := iop.OpenSessionWithData(ctx, colID, testProviderID, "key-1",
		json.RawMessage(`{"manual_authorized_amount":1000000000000}`))

	require.NoError(t, err)
	assert.NotEmpty(t, sesID)
}

// TestInteropMalformedDataIsRejected verifies that a request whose body is not a
// JSON object is rejected explicitly.
func TestInteropMalformedDataIsRejected(t *testing.T) {
	iop, _ := newTestInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)

	_, err = iop.OpenSessionWithData(ctx, colID, testProviderID, "key-1", json.RawMessage(`[1,2]`))

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
}

// TestInteropCarriesErrorsAsTheyAre verifies that the surface does NOT CHANGE
// the classification of the errors it wraps.
//
// Interop makes no decisions; reclassifying an error here would mean the same
// rule diverging in two places.
func TestInteropCarriesErrorsAsTheyAre(t *testing.T) {
	iop, _ := newTestInterop(t)
	ctx := context.Background()

	_, err := iop.CreateCollection(ctx, "", "", testCurrency, testAmount)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)

	_, err = iop.OpenSession(ctx, "paycol_MISSING", testProviderID, "key-1")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, _, err = iop.Authorize(ctx, "payses_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, err = iop.Capture(ctx, "payses_MISSING", 0)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	assert.True(t, errors.HasKind(iop.Cancel(ctx, "payses_MISSING"), errors.KindNotFound))

	_, err = iop.Refund(ctx, "pay_MISSING", 0, "")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, _, _, _, _, err = iop.Collection(ctx, "paycol_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, err = iop.SessionStatus(ctx, "payses_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)
}
