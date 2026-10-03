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

// TestCreateSessionMakesTheCollectionAwaiting verifies that opening a session
// updates the collection's derived status.
func TestCreateSessionMakesTheCollectionAwaiting(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)

	ses := openPaymentSession(t, svc, col.ID, "key-1")

	assert.Equal(t, models.SessionPending, ses.Status)
	assert.Equal(t, testAmount, ses.Amount, "when no amount is given, the collection's remainder is used")
	assert.Equal(t, testCurrency, ses.CurrencyCode, "the currency comes from the collection")
	assert.Equal(t, "ext_key-1", ses.ExternalID, "the provider's id must be stored")

	updated, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAwaiting, updated.Status)
}

// TestASecondCreateSessionWithTheSameKeyYieldsOneSession verifies the
// idempotency requirement of plan Section 2.6.
//
// It is not enough that the returned id is the same: it is also proved that the
// PROVIDER is not called a second time. An implementation that went to the
// provider every time would have left idempotency entirely at the provider's
// mercy, and not every provider offers it.
func TestASecondCreateSessionWithTheSameKeyYieldsOneSession(t *testing.T) {
	svc, store, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)

	first := openPaymentSession(t, svc, col.ID, "key-1")
	second := openPaymentSession(t, svc, col.ID, "key-1")

	assert.Equal(t, first.ID, second.ID, "the same key must return the same session")
	create, _, _, _, _ := prov.calls()
	assert.Equal(t, 1, create, "the provider must be called ONLY once")

	sessions, err := store.ListPaymentSessionsByCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "there must be a single session record")
}

// TestTheSameKeyOnAnotherCollectionConflicts verifies that we refuse the reuse
// of an idempotency key.
//
// Silently returning the existing session would mean that the session the
// caller believes it opened for ANOTHER order actually belongs to the old order;
// the payment would be written to the wrong collection.
func TestTheSameKeyOnAnotherCollectionConflicts(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	firstCol := openCollection(t, svc, testAmount)
	secondCol := openCollection(t, svc, testAmount)
	openPaymentSession(t, svc, firstCol.ID, "key-1")

	_, err := svc.CreateSession(ctx, secondCol.ID, testProviderID,
		service.CreateSessionInput{IdempotencyKey: "key-1"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateSessionCannotExceedTheRemainingAmount verifies that no more than the
// collection can be authorized.
func TestCreateSessionCannotExceedTheRemainingAmount(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)

	_, err := svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{
		Amount:         testAmount + 1,
		IdempotencyKey: "key-1",
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestASecondFullSessionCannotOpenOnACollectionWithAnOpenSession verifies the
// rule that closes the gate to a DOUBLE CAPTURE.
//
// If the remaining amount is computed by looking only at the AUTHORIZED amount,
// two sessions, each for the FULL amount, can be opened on the same collection
// while neither has been authorized yet. Once both are authorized, TWICE the
// collection is held; once both are captured, the customer is charged twice and
// the collection looks paid. An open session RESERVES an amount too.
func TestASecondFullSessionCannotOpenOnACollectionWithAnOpenSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	first := openPaymentSession(t, svc, col.ID, "key-1")
	require.Equal(t, models.SessionPending, first.Status, "the first session is not authorized yet")

	_, err := svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestOpenSessionsCannotSumPastTheCollection verifies that a split payment has
// a ceiling too.
//
// Because the remaining amount counts the open sessions, the second session can
// open only for the amount LEFT OVER; anything more is a conflict. Splitting the
// payment is legitimate in itself; the total exceeding the collection is not.
func TestOpenSessionsCannotSumPastTheCollection(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	_, err := svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{
		Amount:         testAmount / 4,
		IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{
		Amount:         testAmount,
		IdempotencyKey: "key-2",
	})
	require.Error(t, err, "only three quarters remain")
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))

	remainder, err := svc.CreateSession(ctx, col.ID, testProviderID,
		service.CreateSessionInput{IdempotencyKey: "key-3"})
	require.NoError(t, err, "a session for the whole remainder must be able to open")
	assert.Equal(t, testAmount-testAmount/4, remainder.Amount)
}

// TestACanceledSessionReleasesItsReservation verifies that after compensation
// the collection can be paid again.
//
// That open sessions reserve an amount MUST NOT mean that a canceled session
// locks the collection forever; after the saga compensates a step, the customer
// must be able to try a new payment method.
func TestACanceledSessionReleasesItsReservation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	first := openPaymentSession(t, svc, col.ID, "key-1")
	require.NoError(t, svc.CancelPayment(ctx, first.ID))

	second, err := svc.CreateSession(ctx, col.ID, testProviderID,
		service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.NoError(t, err)
	assert.Equal(t, testAmount, second.Amount, "the canceled session's reservation must drop")
}

// TestATerminalSessionsKeyCannotBeReused verifies that going on with the same
// key after compensation is refused EXPLICITLY.
//
// Returning the canceled session as it is would mean the caller gets an
// incomprehensible transition conflict at the next step: a canceled session
// cannot be authorized, and the saga would fail with the same error forever. The
// error code tells the caller that a NEW key is needed.
func TestATerminalSessionsKeyCannotBeReused(t *testing.T) {
	tests := map[string]func(t *testing.T, svc *service.Service, sessionID string){
		"canceled": func(t *testing.T, svc *service.Service, sessionID string) {
			require.NoError(t, svc.CancelPayment(context.Background(), sessionID))
		},
		"failed": func(t *testing.T, svc *service.Service, sessionID string) {
			_, err := svc.AuthorizePayment(context.Background(), sessionID)
			require.Error(t, err)
		},
	}

	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _, prov := newTestService(t)
			ctx := context.Background()
			col := openCollection(t, svc, testAmount)
			ses := openPaymentSession(t, svc, col.ID, "key-1")
			prov.scenario(coreprovider.SessionFailed, 0, "card declined")
			prepare(t, svc, ses.ID)

			_, err := svc.CreateSession(ctx, col.ID, testProviderID,
				service.CreateSessionInput{IdempotencyKey: "key-1"})

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
			assert.Equal(t, service.CodeSessionTerminal, errors.CodeOf(err))
		})
	}
}

// TestANewSessionOnAFullyAuthorizedCollectionConflicts verifies that no amount
// is left to open while the whole collection is authorized.
func TestANewSessionOnAFullyAuthorizedCollectionConflicts(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestANewSessionOnACapturedCollectionConflicts verifies the rule that closes
// the gate to a double capture.
func TestANewSessionOnACapturedCollectionConflicts(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestAnUnregisteredProviderIsNotFound verifies that forgetting to register a
// provider gives an error that can be diagnosed.
func TestAnUnregisteredProviderIsNotFound(t *testing.T) {
	svc, _, _ := newTestService(t)
	col := openCollection(t, svc, testAmount)

	_, err := svc.CreateSession(context.Background(), col.ID, "stripe",
		service.CreateSessionInput{IdempotencyKey: "key-1"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)
	assert.Contains(t, err.Error(), testProviderID, "the message must name the REGISTERED providers")
}

// TestCreateSessionWritesNothingOnError verifies that the transaction is rolled
// back when the provider blows up.
func TestCreateSessionWritesNothingOnError(t *testing.T) {
	svc, store, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	prov.createErr = errors.Unavailable("saglayici_kapali", "the provider could not be reached")

	_, err := svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{IdempotencyKey: "key-1"})

	require.Error(t, err)
	sessions, listErr := store.ListPaymentSessionsByCollection(ctx, col.ID)
	require.NoError(t, listErr)
	assert.Empty(t, sessions, "the transaction must be rolled back")

	updated, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.CollectionNotPaid, updated.Status, "the collection's status must not change")
}

// TestAuthorizeMakesTheCollectionAuthorized verifies the happy path.
func TestAuthorizeMakesTheCollectionAuthorized(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")

	updatedSession, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized, updatedSession.Status)
	assert.Equal(t, testAmount, updatedSession.AuthorizedAmount,
		"if the provider reported zero, the whole session counts as authorized")

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAuthorized, updatedCol.Status)
	assert.Equal(t, testAmount, updatedCol.AuthorizedAmount)
}

// TestASecondAuthorizeDoesNotReachTheProvider verifies that the idempotent
// branch does not go to the provider and does not add the amount A SECOND TIME.
//
// Adding to the collection's authorized amount twice would make it look as if
// twice the amount were held, and the collection's remainder would be computed
// wrongly.
func TestASecondAuthorizeDoesNotReachTheProvider(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	again, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err, "a second authorization must NOT fail")
	assert.Equal(t, models.SessionAuthorized, again.Status)
	_, authorize, _, _, _ := prov.calls()
	assert.Equal(t, 1, authorize, "the provider must be called ONLY once")

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, updatedCol.AuthorizedAmount, "the authorized amount must not DOUBLE")
}

// TestAuthorizeDeclineReturnsAnErrorButPersistsTheSession verifies the behavior
// that makes the payment step of the Phase 6 saga fail.
//
// Two claims are critical at once, and they complement each other:
//
//   - The method returns an ERROR. Had a decline silently counted as success, a
//     flow that forgets to check the status would confirm an unpaid order.
//   - The session is nevertheless PERSISTED as "failed". An implementation that
//     rolled the transaction back in order to return an error would erase the
//     decline too, and the session would look "pending" forever.
func TestAuthorizeDeclineReturnsAnErrorButPersistsTheSession(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	prov.scenario(coreprovider.SessionFailed, 0, "insufficient funds")

	_, err := svc.AuthorizePayment(ctx, ses.ID)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeAuthorizationDeclined, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "insufficient funds")

	updatedSession, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionFailed, updatedSession.Status, "the decline must be PERSISTED")
	assert.Equal(t, "insufficient funds", updatedSession.DeclineReason)

	updatedCol, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Zero(t, updatedCol.AuthorizedAmount)
	assert.Equal(t, models.CollectionNotPaid, updatedCol.Status,
		"a collection whose only session was declined must be retryable")
}

// TestAPartialAuthorizationLeavesTheCollectionAwaiting verifies that a partial
// authorization does NOT make the collection "authorized".
//
// Counting an under-authorized collection as "authorized" would mean the capture
// step trying to take money that is not there.
func TestAPartialAuthorizationLeavesTheCollectionAwaiting(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	prov.scenario(coreprovider.SessionAuthorized, testAmount/2, "")

	updatedSession, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.Equal(t, testAmount/2, updatedSession.AuthorizedAmount)

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAwaiting, updatedCol.Status)
	assert.Equal(t, testAmount/2, updatedCol.AuthorizedAmount)
}

// TestAnEmptyProviderResponseDoesNotEraseSessionData verifies that a provider
// response WITHOUT A BODY keeps the data stored on the session.
//
// Most real providers return no body in their authorization response. An
// implementation that overwrote with the empty response would erase what was
// stored when the session was opened (e.g. the client_secret the client will
// use), and the bug would only show in production, in the middle of a payment
// flow.
func TestAnEmptyProviderResponseDoesNotEraseSessionData(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	require.NotEmpty(t, ses.Data, "the session must store provider data when it opens")
	prov.setAuthorizeData(nil)

	updated, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.JSONEq(t, string(ses.Data), string(updated.Data),
		"an empty response must NOT ERASE the existing data")
}

// TestAProviderResponseOverwritesSessionData verifies that a non-empty response
// really is applied; the keeping rule does not mean "never update".
func TestAProviderResponseOverwritesSessionData(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	prov.setAuthorizeData(json.RawMessage(`{"client_secret":"cs_1"}`))

	updated, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.JSONEq(t, `{"client_secret":"cs_1"}`, string(updated.Data))
}

// TestAuthorizeInvalidTransitions verifies the state machine's conflict
// branches.
func TestAuthorizeInvalidTransitions(t *testing.T) {
	tests := map[string]func(t *testing.T, svc *service.Service, sessionID string){
		"captured": func(t *testing.T, svc *service.Service, sessionID string) {
			_, err := svc.AuthorizePayment(context.Background(), sessionID)
			require.NoError(t, err)
			_, err = svc.CapturePayment(context.Background(), sessionID, 0)
			require.NoError(t, err)
		},
		"canceled": func(t *testing.T, svc *service.Service, sessionID string) {
			require.NoError(t, svc.CancelPayment(context.Background(), sessionID))
		},
	}

	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _, _ := newTestService(t)
			col := openCollection(t, svc, testAmount)
			ses := openPaymentSession(t, svc, col.ID, "key-1")
			prepare(t, svc, ses.ID)

			_, err := svc.AuthorizePayment(context.Background(), ses.ID)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
			assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
		})
	}
}

// TestADeclinedSessionCannotBeReauthorized: a decline is final; a new session
// has to be opened.
func TestADeclinedSessionCannotBeReauthorized(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	prov.scenario(coreprovider.SessionFailed, 0, "card declined")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err)

	prov.scenario(coreprovider.SessionAuthorized, 0, "")
	_, err = svc.AuthorizePayment(ctx, ses.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestAuthorizeProviderContractViolations verifies that responses outside the
// contract are classified as Internal.
//
// A contract violation is not something the client can fix; returning 409 would
// send whoever wrote the integration looking for the problem on their own side.
func TestAuthorizeProviderContractViolations(t *testing.T) {
	tests := map[string]struct {
		status coreprovider.SessionStatus
		amount int64
	}{
		"unexpected status":             {status: coreprovider.SessionPending},
		"unknown status":                {status: coreprovider.SessionStatus("weird")},
		"hold above the amount":         {status: coreprovider.SessionAuthorized, amount: testAmount + 1},
		"negative authorized amount":    {status: coreprovider.SessionAuthorized, amount: -1},
		"canceled reported as a result": {status: coreprovider.SessionCanceled},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _, prov := newTestService(t)
			ctx := context.Background()
			col := openCollection(t, svc, testAmount)
			ses := openPaymentSession(t, svc, col.ID, "key-1")
			prov.scenario(tt.status, tt.amount, "")

			_, err := svc.AuthorizePayment(ctx, ses.ID)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInternal), "error: %v", err)
			assert.Equal(t, service.CodeProviderContract, errors.CodeOf(err))
		})
	}
}

// TestAuthorizeLockOrder verifies that the locks are taken in the CANONICAL
// order.
//
// The order is a concurrency contract: the collection is always locked BEFORE
// the session. On the real database a violation would only show under a race,
// as a deadlock; here the order is read directly.
func TestAuthorizeLockOrder(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")

	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	order := store.lockOrder()
	require.GreaterOrEqual(t, len(order), 2)
	assert.Equal(t, []string{"collection", "collection", "session"}, order,
		"first the collection while the session opens, then collection -> session in the authorization")
}

// TestCancelCanBeCalledTwice verifies that the saga's compensation is
// IDEMPOTENT.
//
// The Phase 6 saga calls this when the payment step fails. It is not enough
// that the second call does not fail: it is also proved that it does NOT GO to
// the provider a second time and does not touch the collection's authorized
// amount A SECOND TIME — otherwise the amount would drop below zero.
func TestCancelCanBeCalledTwice(t *testing.T) {
	svc, store, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))
	colWritesBefore, _ := store.writes()

	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "a second cancel must NOT fail")

	colWritesAfter, _ := store.writes()
	assert.Equal(t, colWritesBefore, colWritesAfter, "a second cancel must not write to the collection")
	_, _, _, _, cancel := prov.calls()
	assert.Equal(t, 1, cancel, "the provider must be called ONLY once")
}

// TestCancelReleasesTheHold verifies that a cancellation takes the
// collection's authorized amount back and makes its status "canceled".
func TestCancelReleasesTheHold(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	updatedSession, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, updatedSession.Status)
	assert.Zero(t, updatedSession.AuthorizedAmount)

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, updatedCol.AuthorizedAmount, "the hold must drop from the collection as well")
	assert.Equal(t, models.CollectionCanceled, updatedCol.Status)
}

// TestCancelWorksOnAPendingSession verifies that a session that was never
// authorized can be closed too; if the saga fails at another step after
// opening the session, this is exactly the state the compensation finds.
func TestCancelWorksOnAPendingSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	updatedSession, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, updatedSession.Status)
}

// TestCancelClosesADeclinedSession verifies that compensating a flow that
// failed because of a decline does NOT return an error.
func TestCancelClosesADeclinedSession(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	prov.scenario(coreprovider.SessionFailed, 0, "card declined")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	updatedSession, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionCanceled, updatedSession.Status)
	assert.Equal(t, "card declined", updatedSession.DeclineReason, "the decline reason must be kept")
}

// TestCancelConflictsOnACapturedSession verifies that money taken cannot be
// given back by a cancellation; the way is a refund.
func TestCancelConflictsOnACapturedSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	err = svc.CancelPayment(ctx, ses.ID)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestCancelOfAnUnknownSessionIsNotFound verifies that idempotency does NOT mean
// "silently swallow everything".
func TestCancelOfAnUnknownSessionIsNotFound(t *testing.T) {
	svc, _, _ := newTestService(t)

	err := svc.CancelPayment(context.Background(), "payses_MISSING")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)
}

// TestCancelWritesNothingOnAProviderError verifies that when the provider
// refuses the cancellation, the module does not change its record either.
//
// Writing a hold that is still open at the provider as "canceled" in the module
// would mean the customer's money is left hanging and nobody notices.
func TestCancelWritesNothingOnAProviderError(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	prov.cancelErr = errors.Unavailable("saglayici_kapali", "the provider could not be reached")

	err = svc.CancelPayment(ctx, ses.ID)

	require.Error(t, err)
	updatedSession, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionAuthorized, updatedSession.Status, "the transaction must be rolled back")

	updatedCol, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Equal(t, testAmount, updatedCol.AuthorizedAmount)
}

// TestCancelLockOrder verifies the cancel flow's lock order.
func TestCancelLockOrder(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	store.locks = nil

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	assert.Equal(t, []string{"collection", "session"}, store.lockOrder())
}

// TestANewSessionCanOpenForThePartialCapturesRemainder is the path ADR 0118
// opened: the remainder of a partially captured collection can be collected.
//
// A partial capture is not an invented case but a first-class state produced by
// a published endpoint: the admin capture endpoint takes the amount as OPTIONAL
// and [Service.CapturePayment] bounds it only FROM ABOVE, so the operator can
// take part of the authorized amount. A provider authorizing only part of the
// amount ends up in the same place. The schema knows that case by name
// ([models.CollectionPartiallyCaptured]).
//
// Until ADR 0118 the remainder could never be collected again: the gate asked
// whether the collection had received ANYTHING at all, and a partial capture
// counted as "received". Arithmetic tells apart the two states the flag could
// not.
func TestANewSessionCanOpenForThePartialCapturesRemainder(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	first := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, first.ID)
	require.NoError(t, err)

	_, err = svc.CapturePayment(ctx, first.ID, testAmount/4)
	require.NoError(t, err, "part of the authorized amount can be captured")

	remainder, err := svc.CreateSession(ctx, col.ID, testProviderID,
		service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.NoError(t, err, "the remainder of a partially captured collection must be collectable")
	assert.Equal(t, testAmount-testAmount/4, remainder.Amount,
		"the new session opens only for the remaining amount")
}

// TestNoMoreThanTheRemainderCanOpenAfterAPartialCapture verifies that the
// remainder is a CEILING, not merely a permission.
//
// This is the gate to ADR 0118's most expensive bug: an arithmetic that does not
// count what was captured shows the FULL amount as the remainder, the session
// opens, the provider takes the money, and only after that does it hit the
// `captured_amount <= amount` constraint — money at the provider, nothing in the
// ledger.
func TestNoMoreThanTheRemainderCanOpenAfterAPartialCapture(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	first := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, first.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, first.ID, testAmount/4)
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, testProviderID, service.CreateSessionInput{
		Amount:         testAmount,
		IdempotencyKey: "key-2",
	})

	require.Error(t, err, "only three quarters remain")
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestAFullyRefundedCollectionDoesNotReopen is what ADR 0118 DELIBERATELY does
// not do.
//
// A refund does not shrink the captured total, so the remaining capacity stays
// at zero and the collection does NOT become payable again. ADR 0117 named the
// trigger for this ("once it owes nothing"), and that trigger has no consumer
// today: all four refund callers in production (the admin refund endpoint and
// the returns flow's refund, claim and exchange-difference paths) only send the
// money back, and none captures again afterwards. A capability without a
// consumer is not published (ADR 0063), and this test ties that decision to a
// gate.
func TestAFullyRefundedCollectionDoesNotReopen(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	first := openPaymentSession(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, first.ID)
	require.NoError(t, err)
	payment, err := svc.CapturePayment(ctx, first.ID, 0)
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, payment.ID, 0, "")
	require.NoError(t, err, "all of it is refunded")

	_, err = svc.CreateSession(ctx, col.ID, testProviderID,
		service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.Error(t, err, "a refund does not make the collection payable again")
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}
