package models

import "time"

// The outcomes a session report settles with (ADR 0438).
const (
	// ReportCompleted: the report's session paid for an order.
	ReportCompleted = "completed"
	// ReportRefunded: the payment was given back and placed no order — the
	// cart no longer matched it, or its session was canceled first.
	ReportRefunded = "refunded"
	// ReportClosed: the provider declined the session first.
	ReportClosed = "closed"
	// ReportLeft: the session was not opened for a cart's checkout, and it is
	// a person's to settle.
	ReportLeft = "left"
	// ReportGaveUp: the last attempt to complete the cart failed; the payment
	// stays held and reconciliation reports it.
	ReportGaveUp = "gave_up"
)

// ReportFirstRetry is how long after a report is opened it is first due: the
// checkout attempts the completion when the report arrives, and the sweep
// takes it only once that attempt had its time. It is the checkout's
// ReportRetryBase, and internal/arch holds the two equal.
const ReportFirstRetry = 5 * time.Minute

// SessionReport is a provider's report of a session whose customer paid on the
// provider's page, and what the checkout did with it (ADR 0438).
type SessionReport struct {
	SessionID     string
	ReportedAt    time.Time
	Attempts      int
	NextAttemptAt time.Time
	LastFailure   string
	// Outcome is empty while the report is open.
	Outcome   string
	SettledAt *time.Time
}
