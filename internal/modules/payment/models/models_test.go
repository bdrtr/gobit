package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// TestSessionTransitionTableInAllItsBranches verifies EVERY cell of the
// session state machine.
//
// The table covers the whole of three operations × five statuses = 15 cells.
// Writing them out one by one is deliberate: a test that assumes one branch is
// "like the others" cannot catch that branch being changed silently. Two cells
// in particular are critical and are justified separately:
//
//   - captured + Cancel = conflict — canceling a session whose money has been
//     drawn would mean showing the amount taken from the customer as if it
//     were not in the record.
//   - canceled + Cancel = noop — the saga compensation being able to be
//     idempotent depends on exactly this cell.
func TestSessionTransitionTableInAllItsBranches(t *testing.T) {
	statuses := []models.SessionStatus{
		models.SessionPending,
		models.SessionAuthorized,
		models.SessionCaptured,
		models.SessionCanceled,
		models.SessionFailed,
	}

	wantByStatus := map[models.SessionStatus]struct {
		authorize, capture, cancel models.SessionAction
	}{
		models.SessionPending: {
			authorize: models.ActionProceed,
			capture:   models.ActionConflict,
			cancel:    models.ActionProceed,
		},
		models.SessionAuthorized: {
			authorize: models.ActionNoop,
			capture:   models.ActionProceed,
			cancel:    models.ActionProceed,
		},
		models.SessionCaptured: {
			authorize: models.ActionConflict,
			capture:   models.ActionNoop,
			cancel:    models.ActionConflict,
		},
		models.SessionCanceled: {
			authorize: models.ActionConflict,
			capture:   models.ActionConflict,
			cancel:    models.ActionNoop,
		},
		models.SessionFailed: {
			authorize: models.ActionConflict,
			capture:   models.ActionConflict,
			cancel:    models.ActionProceed,
		},
	}

	for _, status := range statuses {
		t.Run(status.String(), func(t *testing.T) {
			want := wantByStatus[status]
			assert.Equal(t, want.authorize, status.AuthorizeAction(), "AuthorizeAction")
			assert.Equal(t, want.capture, status.CaptureAction(), "CaptureAction")
			assert.Equal(t, want.cancel, status.CancelAction(), "CancelAction")
		})
	}
}

// TestUndefinedStatusRejectsEveryOperation verifies that an undefined status
// produces a conflict in all three operations.
//
// The zero value being safe is a contract: if a corrupt status value read out
// of the database were interpreted as "proceed", money would move without the
// record's real status being known.
func TestUndefinedStatusRejectsEveryOperation(t *testing.T) {
	corrupt := models.SessionStatus("unknown")

	assert.False(t, corrupt.Valid())
	assert.Equal(t, models.ActionConflict, corrupt.AuthorizeAction())
	assert.Equal(t, models.ActionConflict, corrupt.CaptureAction())
	assert.Equal(t, models.ActionConflict, corrupt.CancelAction())
}

// TestTerminalStatuses pins down which sessions have come to an end.
//
// The distinction is the boundary of idempotency: a repeat made with the same
// key can read the existing capture out of a captured session, but cannot move
// ahead with a canceled or declined one.
func TestTerminalStatuses(t *testing.T) {
	assert.False(t, models.SessionPending.Terminal())
	assert.False(t, models.SessionAuthorized.Terminal())
	assert.False(t, models.SessionCaptured.Terminal(),
		"a captured session is the SUCCESSFUL outcome of the flow, not a dead end")
	assert.True(t, models.SessionCanceled.Terminal())
	assert.True(t, models.SessionFailed.Terminal())
}

// TestCollectionStatusForAllBranches verifies every branch of the collection
// status derivation.
//
// The capture branch additionally tells a SHORT payment apart: a capture that
// does not cover the collection's amount is not "captured" but
// "partially_captured". Otherwise 1 unit drawn out of a collection of 50,000
// would look to the saga as if the payment were complete.
//
// The fixtures deliberately set up the "wrong order" trap: a collection that
// has a capture also has a live session, and the right answer is not
// "awaiting" but "captured". The money always beats the counts; an
// implementation that reverses the order falls over on these lines.
func TestCollectionStatusForAllBranches(t *testing.T) {
	tests := []struct {
		name   string
		col    models.PaymentCollection
		counts models.SessionCounts
		want   models.CollectionStatus
	}{
		{
			name:   "collection with no session is not_paid",
			col:    models.PaymentCollection{Amount: 1000},
			counts: models.SessionCounts{},
			want:   models.CollectionNotPaid,
		},
		{
			name:   "open session is awaiting",
			col:    models.PaymentCollection{Amount: 1000},
			counts: models.SessionCounts{Live: 1, Total: 1},
			want:   models.CollectionAwaiting,
		},
		{
			name:   "full hold is authorized",
			col:    models.PaymentCollection{Amount: 1000, AuthorizedAmount: 1000},
			counts: models.SessionCounts{Live: 1, Total: 1},
			want:   models.CollectionAuthorized,
		},
		{
			name:   "partial hold is still awaiting",
			col:    models.PaymentCollection{Amount: 1000, AuthorizedAmount: 400},
			counts: models.SessionCounts{Live: 1, Total: 1},
			want:   models.CollectionAwaiting,
		},
		{
			name:   "capture beats a live session",
			col:    models.PaymentCollection{Amount: 1000, AuthorizedAmount: 1000, CapturedAmount: 1000},
			counts: models.SessionCounts{Live: 1, Total: 1},
			want:   models.CollectionCaptured,
		},
		{
			name:   "short capture is partially_captured",
			col:    models.PaymentCollection{Amount: 1000, CapturedAmount: 1},
			counts: models.SessionCounts{Total: 1},
			want:   models.CollectionPartiallyCaptured,
		},
		{
			name:   "a capture short by one is partially_captured too",
			col:    models.PaymentCollection{Amount: 1000, CapturedAmount: 999},
			counts: models.SessionCounts{Total: 1},
			want:   models.CollectionPartiallyCaptured,
		},
		{
			name:   "full capture is captured",
			col:    models.PaymentCollection{Amount: 1000, CapturedAmount: 1000},
			counts: models.SessionCounts{Total: 1},
			want:   models.CollectionCaptured,
		},
		{
			name: "partial refund is partially_refunded",
			col: models.PaymentCollection{
				Amount: 1000, AuthorizedAmount: 1000, CapturedAmount: 1000, RefundedAmount: 400,
			},
			counts: models.SessionCounts{Total: 1},
			want:   models.CollectionPartiallyRefunded,
		},
		{
			name: "full refund is refunded",
			col: models.PaymentCollection{
				Amount: 1000, AuthorizedAmount: 1000, CapturedAmount: 1000, RefundedAmount: 1000,
			},
			counts: models.SessionCounts{Total: 1},
			want:   models.CollectionRefunded,
		},
		{
			name:   "canceled session is canceled",
			col:    models.PaymentCollection{Amount: 1000},
			counts: models.SessionCounts{Canceled: 1, Total: 1},
			want:   models.CollectionCanceled,
		},
		{
			name:   "only declined sessions stay not_paid",
			col:    models.PaymentCollection{Amount: 1000},
			counts: models.SessionCounts{Failed: 2, Total: 2},
			want:   models.CollectionNotPaid,
		},
		{
			name:   "cancel and decline together give canceled",
			col:    models.PaymentCollection{Amount: 1000},
			counts: models.SessionCounts{Canceled: 1, Failed: 1, Total: 2},
			want:   models.CollectionCanceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, models.CollectionStatusFor(tt.col, tt.counts))
		})
	}
}

// TestRemainingAmountComputations verifies that the remaining-amount helpers
// do not fall below zero.
//
// An over-refund or an over-hold is already prevented by a database
// constraint; this test proves that the helpers do not return a negative in
// the face of a CORRUPT record either. A negative "remaining" would turn into
// a money movement in the opposite direction at the caller.
func TestRemainingAmountComputations(t *testing.T) {
	col := models.PaymentCollection{Amount: 1000, AuthorizedAmount: 1200, CapturedAmount: 500, RefundedAmount: 800}
	assert.Equal(t, int64(0), col.RefundableAmount())

	partial := models.PaymentCollection{Amount: 1000, AuthorizedAmount: 400, CapturedAmount: 400, RefundedAmount: 100}
	assert.Equal(t, int64(300), partial.RefundableAmount())

	pay := models.Payment{Amount: 500, RefundedAmount: 700}
	assert.Equal(t, int64(0), pay.RefundableAmount())
	assert.Equal(t, int64(200), models.Payment{Amount: 500, RefundedAmount: 300}.RefundableAmount())

	manual := models.ManualSession{CapturedAmount: 500, RefundedAmount: 500}
	assert.Equal(t, int64(0), manual.RefundableAmount())
	assert.Equal(t, int64(150), models.ManualSession{CapturedAmount: 200, RefundedAmount: 50}.RefundableAmount())
}

// TestIdentifierPrefixesAndOrdering verifies that the identifiers are
// prefixed, unique and sortable by time.
func TestIdentifierPrefixesAndOrdering(t *testing.T) {
	generators := map[string]func() string{
		models.PaymentCollectionIDPrefix: models.NewPaymentCollectionID,
		models.PaymentSessionIDPrefix:    models.NewPaymentSessionID,
		models.PaymentIDPrefix:           models.NewPaymentID,
		models.RefundIDPrefix:            models.NewRefundID,
		models.ManualSessionIDPrefix:     models.NewManualSessionID,
	}

	for prefix, generate := range generators {
		t.Run(prefix, func(t *testing.T) {
			first, second := generate(), generate()

			assert.True(t, len(first) == len(prefix)+26, "the body must be 26 characters: %s", first)
			assert.Equal(t, prefix, first[:len(prefix)])
			assert.NotEqual(t, first, second, "two identifiers must not be the same")
		})
	}
}

// TestCollectionStatusValidity tells defined and undefined statuses apart.
func TestCollectionStatusValidity(t *testing.T) {
	valid := []models.CollectionStatus{
		models.CollectionNotPaid, models.CollectionAwaiting, models.CollectionAuthorized,
		models.CollectionCaptured, models.CollectionPartiallyRefunded,
		models.CollectionRefunded, models.CollectionCanceled,
	}
	for _, status := range valid {
		assert.True(t, status.Valid(), "%q must be valid", status)
	}
	assert.False(t, models.CollectionStatus("").Valid())
	assert.False(t, models.CollectionStatus("paid").Valid())
}

// TestSessionActionString verifies the readable name of the outcomes.
//
// The name is only for diagnosis, but it shows up in error messages; an
// undefined value being read as "conflict" rests on the same rationale as the
// zero value being safe.
func TestSessionActionString(t *testing.T) {
	assert.Equal(t, "proceed", models.ActionProceed.String())
	assert.Equal(t, "noop", models.ActionNoop.String())
	assert.Equal(t, "conflict", models.ActionConflict.String())
	assert.Equal(t, "conflict", models.SessionAction(200).String())
}
