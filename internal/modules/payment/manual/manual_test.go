package manual_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// Constants used in the tests.
const (
	testReference = "paycol_TEST"
	testCurrency  = "TRY"
	testAmount    = int64(12_500)
)

// newProvider sets up a provider that works on an in-memory ledger.
func newProvider(t *testing.T) (*manual.Provider, *memStore) {
	t.Helper()

	store := newMemStore()
	return manual.New(store, nil), store
}

// openSession opens a session for a test and returns its identifier.
func openSession(t *testing.T, p *manual.Provider, key string, data map[string]any) string {
	t.Helper()

	ses, err := p.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount:         testAmount,
		CurrencyCode:   testCurrency,
		Reference:      testReference,
		IdempotencyKey: key,
		Data:           data,
	})
	require.NoError(t, err)
	return ses.ID
}

// TestSecondCreateSessionWithTheSameKeyOpensNoNewSession verifies the core
// contract's idempotency requirement.
//
// It is not enough for the returned identifier to be the same: the test also
// proves that a second row was REALLY not written to the ledger. An
// implementation that returned the same identifier while opening a second
// session in the background would lead to two capture attempts on the
// customer.
func TestSecondCreateSessionWithTheSameKeyOpensNoNewSession(t *testing.T) {
	p, store := newProvider(t)
	ctx := context.Background()

	in := coreprovider.CreateSessionInput{
		Amount:         testAmount,
		CurrencyCode:   testCurrency,
		Reference:      testReference,
		IdempotencyKey: "key-1",
	}

	first, err := p.CreateSession(ctx, in)
	require.NoError(t, err)
	second, err := p.CreateSession(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "the same key must return the same session")
	inserts, _ := store.counts()
	assert.Equal(t, 1, inserts, "only ONE row must be written to the ledger")
}

// TestTheSameKeyWithADifferentAmountConflicts verifies that we refuse the reuse
// of an idempotency key.
//
// Silently returning the existing session would mean the amount the caller
// believes it sent is never applied; the result would be capturing an amount
// different from the one expected.
func TestTheSameKeyWithADifferentAmountConflicts(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()

	_, err := p.CreateSession(ctx, coreprovider.CreateSessionInput{
		Amount: testAmount, CurrencyCode: testCurrency, Reference: testReference, IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = p.CreateSession(ctx, coreprovider.CreateSessionInput{
		Amount: testAmount + 1, CurrencyCode: testCurrency, Reference: testReference, IdempotencyKey: "key-1",
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, manual.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateSessionInputValidation exercises every branch of the money
// validation.
func TestCreateSessionInputValidation(t *testing.T) {
	p, _ := newProvider(t)

	tests := []struct {
		name string
		in   coreprovider.CreateSessionInput
	}{
		{"without a key", coreprovider.CreateSessionInput{Amount: testAmount, CurrencyCode: testCurrency, Reference: testReference}},
		{"without a reference", coreprovider.CreateSessionInput{Amount: testAmount, CurrencyCode: testCurrency, IdempotencyKey: "k"}},
		{"zero amount", coreprovider.CreateSessionInput{
			Amount: 0, CurrencyCode: testCurrency, Reference: testReference, IdempotencyKey: "k",
		}},
		{"negative amount", coreprovider.CreateSessionInput{
			Amount: -1, CurrencyCode: testCurrency, Reference: testReference, IdempotencyKey: "k",
		}},
		{"amount above the ceiling", coreprovider.CreateSessionInput{
			Amount: models.MaxAmount + 1, CurrencyCode: testCurrency, Reference: testReference, IdempotencyKey: "k",
		}},
		{"invalid currency", coreprovider.CreateSessionInput{
			Amount: testAmount, CurrencyCode: "TRYY", Reference: testReference, IdempotencyKey: "k",
		}},
		{"unrecognized outcome", coreprovider.CreateSessionInput{
			Amount: testAmount, CurrencyCode: testCurrency, Reference: testReference, IdempotencyKey: "k",
			Data: map[string]any{manual.DataKeyOutcome: "unknown"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.CreateSession(context.Background(), tt.in)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
		})
	}
}

// TestAuthorizeHoldsTheAmount verifies the happy path.
func TestAuthorizeHoldsTheAmount(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)

	result, err := p.Authorize(ctx, id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, testAmount, result.AuthorizedAmount)
	assert.Empty(t, result.DeclineReason)
}

// TestSecondAuthorizeReturnsNoErrorAndLeavesTheLedgerAlone verifies the core
// contract's "can be called again" requirement.
//
// The write counter proves that the second call does not touch the ledger at
// all; a test that looked only at the returned value could not catch an
// implementation that held the amount a second time.
func TestSecondAuthorizeReturnsNoErrorAndLeavesTheLedgerAlone(t *testing.T) {
	p, store := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)

	require.NoError(t, mustAuthorize(t, p, id))
	_, updatesBefore := store.counts()

	result, err := p.Authorize(ctx, id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	_, updatesAfter := store.counts()
	assert.Equal(t, updatesBefore, updatesAfter, "the second Authorize must not write to the ledger")
}

// TestAuthorizeDeclineInjection verifies the behavior the saga tests need to
// be ABLE TO BLOW UP the payment step.
//
// A decline is NOT AN ERROR: the provider has responded successfully and its
// result is "failed". Returning an error would mean the saga triggering its
// compensation chain for the wrong reason and the decline reason being lost.
func TestAuthorizeDeclineInjection(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", map[string]any{
		manual.DataKeyOutcome:       manual.OutcomeDecline,
		manual.DataKeyDeclineReason: "insufficient funds",
	})

	result, err := p.Authorize(ctx, id)

	require.NoError(t, err, "from the provider's point of view a decline is a successful response")
	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	assert.Equal(t, "insufficient funds", result.DeclineReason)
	assert.Zero(t, result.AuthorizedAmount)
}

// TestAuthorizeDeclineWithoutAReasonUsesTheDefault verifies that a reason is
// written even when none is given; an empty reason is of no use in diagnosis.
func TestAuthorizeDeclineWithoutAReasonUsesTheDefault(t *testing.T) {
	p, _ := newProvider(t)
	id := openSession(t, p, "key-1", map[string]any{manual.DataKeyOutcome: manual.OutcomeDecline})

	result, err := p.Authorize(context.Background(), id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	assert.NotEmpty(t, result.DeclineReason)
}

// TestAuthorizeErrorInjectionLeavesTheStateAlone verifies the scenario where
// the provider is unreachable.
//
// The difference between a decline and an error shows here: an error HAS TO
// be RETRYABLE, so the session must stay "pending". An implementation that
// wrote the state as "failed" would turn a transient network error into a
// permanent decline.
func TestAuthorizeErrorInjectionLeavesTheStateAlone(t *testing.T) {
	p, store := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", map[string]any{manual.DataKeyOutcome: manual.OutcomeError})

	_, err := p.Authorize(ctx, id)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "error: %v", err)

	ses, getErr := p.GetSession(ctx, id)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionPending, ses.Status, "the state must not change; the request can be retried")
	_, updates := store.counts()
	assert.Zero(t, updates, "the error branch must not write to the ledger")
}

// TestAuthorizePartialAmount verifies that partial authorization can be
// exercised; the core contract says explicitly that AuthorizedAmount may be
// smaller than the amount requested.
func TestAuthorizePartialAmount(t *testing.T) {
	p, _ := newProvider(t)
	id := openSession(t, p, "key-1", map[string]any{manual.DataKeyAuthorizedAmount: 5_000})

	result, err := p.Authorize(context.Background(), id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, int64(5_000), result.AuthorizedAmount)
}

// TestAuthorizePartialAmountBounds verifies that a partial amount exceeding
// the session amount, or one that is zero, is refused.
func TestAuthorizePartialAmountBounds(t *testing.T) {
	for name, value := range map[string]int64{"above": testAmount + 1, "zero": 0, "negative": -5} {
		t.Run(name, func(t *testing.T) {
			p, _ := newProvider(t)
			id := openSession(t, p, "key-"+name, map[string]any{manual.DataKeyAuthorizedAmount: value})

			_, err := p.Authorize(context.Background(), id)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
		})
	}
}

// TestCaptureAndSecondCapture verifies the behavior of a capture and of its
// repeat.
func TestCaptureAndSecondCapture(t *testing.T) {
	p, store := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	require.NoError(t, p.Capture(ctx, id, 0), "a zero amount takes the whole held amount")

	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCaptured, ses.Status)
	assert.Equal(t, testAmount, ses.CapturedAmount)

	_, updatesBefore := store.counts()
	require.NoError(t, p.Capture(ctx, id, 0), "the second call must not return an error")
	require.NoError(t, p.Capture(ctx, id, testAmount), "a repeat with the same amount must not return an error either")
	_, updatesAfter := store.counts()
	assert.Equal(t, updatesBefore, updatesAfter, "the repeats must not write to the ledger")
}

// TestPartialCaptureReleasesTheRemainingHold verifies that the held amount in
// the ledger drops to the amount actually captured.
//
// Real providers release the remaining hold on capture. Had the imitation not
// released it, the provider's ledger and the module's record would diverge and
// reconciliation would show a hold that is not on the customer; since the
// session is "captured", correcting it by way of a cancel is not possible
// either.
func TestPartialCaptureReleasesTheRemainingHold(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	require.NoError(t, p.Capture(ctx, id, 1))

	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), ses.CapturedAmount)
	assert.Equal(t, int64(1), ses.AuthorizedAmount,
		"the hold that was not taken must not stay HANGING in the ledger")
}

// TestCaptureWithADifferentAmountConflicts verifies that a captured session
// cannot be captured again with ANOTHER amount; that is not a repeat, it is a
// new request.
func TestCaptureWithADifferentAmountConflicts(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, testAmount))

	err := p.Capture(ctx, id, testAmount-1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestCaptureCannotExceedTheHeldAmount verifies that more than what was held
// cannot be taken.
func TestCaptureCannotExceedTheHeldAmount(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", map[string]any{manual.DataKeyAuthorizedAmount: 5_000})
	require.NoError(t, mustAuthorize(t, p, id))

	err := p.Capture(ctx, id, 5_001)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestCaptureOnAnUnauthorizedSessionConflicts verifies the invalid transition.
func TestCaptureOnAnUnauthorizedSessionConflicts(t *testing.T) {
	p, _ := newProvider(t)
	id := openSession(t, p, "key-1", nil)

	err := p.Capture(context.Background(), id, 0)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestRefundPartialAndFull verifies the refund flow.
func TestRefundPartialAndFull(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))

	require.NoError(t, p.Refund(ctx, id, 2_500))
	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2_500), ses.RefundedAmount)

	require.NoError(t, p.Refund(ctx, id, 0), "a zero amount refunds the REMAINDER")
	ses, err = p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, testAmount, ses.RefundedAmount)
}

// TestSecondZeroRefundAfterAFullRefundReturnsNoError verifies the core
// contract's "Refund can be called again" requirement.
//
// The remainder is zero and nothing is done; that way a full refund request
// can be retried safely.
func TestSecondZeroRefundAfterAFullRefundReturnsNoError(t *testing.T) {
	p, store := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))
	require.NoError(t, p.Refund(ctx, id, 0))
	_, updatesBefore := store.counts()

	require.NoError(t, p.Refund(ctx, id, 0))

	_, updatesAfter := store.counts()
	assert.Equal(t, updatesBefore, updatesAfter, "nothing must be written when nothing is left to refund")
}

// TestRefundCannotExceedTheRemainder verifies that a request to refund money
// that does not exist is refused.
func TestRefundCannotExceedTheRemainder(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))

	err := p.Refund(ctx, id, testAmount+1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestRefundOnAnUncapturedSessionConflicts verifies the invalid transition.
func TestRefundOnAnUncapturedSessionConflicts(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	err := p.Refund(ctx, id, 100)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestCancelCanBeCalledTwice verifies that the saga compensation is
// IDEMPOTENT.
//
// The Phase 6 saga calls this when the payment step blows up, and when a
// workflow is retried or triggered twice the second call must not blow up the
// flow. The write counter also proves that the second call does not touch the
// ledger at all.
func TestCancelCanBeCalledTwice(t *testing.T) {
	p, store := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	require.NoError(t, p.Cancel(ctx, id))
	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, ses.Status)
	assert.Zero(t, ses.AuthorizedAmount, "the hold must be released")

	_, updatesBefore := store.counts()
	require.NoError(t, p.Cancel(ctx, id), "the second cancel must NOT return an error")
	_, updatesAfter := store.counts()
	assert.Equal(t, updatesBefore, updatesAfter, "the second cancel must not write to the ledger")
}

// TestCancelClosesADeclinedSessionAndKeepsTheReason verifies that a declined
// session can be canceled and that its decline reason is NOT LOST.
//
// The saga calls Cancel as the compensation of the step that opened the
// session; in a flow that blew up because of a decline, that session is in the
// "failed" state and the compensation must not return an error.
func TestCancelClosesADeclinedSessionAndKeepsTheReason(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", map[string]any{
		manual.DataKeyOutcome:       manual.OutcomeDecline,
		manual.DataKeyDeclineReason: "card declined",
	})
	_, err := p.Authorize(ctx, id)
	require.NoError(t, err)

	require.NoError(t, p.Cancel(ctx, id))

	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, ses.Status)
	assert.Equal(t, "card declined", ses.DeclineReason, "the decline reason must be kept")
}

// TestCancelOnACapturedSessionConflicts verifies that money that has been
// taken cannot be reversed with a cancel; the way is a refund.
func TestCancelOnACapturedSessionConflicts(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()
	id := openSession(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))

	err := p.Cancel(ctx, id)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestUnknownSessionIsNotFound verifies that idempotency does NOT mean
// "silently swallow everything".
//
// A real session canceled twice and an identifier that never existed are
// different situations; the second is a fault on the caller's side and has to
// be visible.
func TestUnknownSessionIsNotFound(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()

	assert.True(t, errors.HasKind(p.Cancel(ctx, "manses_MISSING"), errors.KindNotFound))
	assert.True(t, errors.HasKind(p.Capture(ctx, "manses_MISSING", 0), errors.KindNotFound))
	assert.True(t, errors.HasKind(p.Refund(ctx, "manses_MISSING", 0), errors.KindNotFound))
	_, err := p.Authorize(ctx, "manses_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound))
}

// TestEmptySessionIdentifierIsInvalid verifies that an empty identifier is
// "invalid", not "not found"; the two are different errors for the caller.
func TestEmptySessionIdentifierIsInvalid(t *testing.T) {
	p, _ := newProvider(t)
	ctx := context.Background()

	assert.True(t, errors.HasKind(p.Cancel(ctx, "  "), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Capture(ctx, "", 0), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Refund(ctx, "", 0), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Capture(ctx, "manses_X", -1), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Refund(ctx, "manses_X", -1), errors.KindInvalid))
	_, err := p.Authorize(ctx, "")
	assert.True(t, errors.HasKind(err, errors.KindInvalid))
	_, err = p.GetSession(ctx, "")
	assert.True(t, errors.HasKind(err, errors.KindInvalid))
}

// TestProviderIdentity verifies the identifier the registration and the flows
// use.
func TestProviderIdentity(t *testing.T) {
	p, _ := newProvider(t)
	assert.Equal(t, manual.ID, p.ID())
	assert.Equal(t, "manual", p.ID())
}

// mustAuthorize authorizes the session and verifies that the result really is
// "authorized".
func mustAuthorize(t *testing.T, p *manual.Provider, sessionID string) error {
	t.Helper()

	result, err := p.Authorize(context.Background(), sessionID)
	if err != nil {
		return err
	}
	require.Equal(t, coreprovider.SessionAuthorized, result.Status)
	return nil
}
