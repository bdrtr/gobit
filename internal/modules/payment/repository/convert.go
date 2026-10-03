package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// This file is the ONLY place for the pgtype <-> domain model conversions and
// for the classification of driver errors.
//
// The boundary being here is deliberate: driver-specific types
// (pgtype.Timestamptz, []byte for jsonb, *pgconn.PgError) do NOT LEAVE the
// repository. The service and API layers see time.Time, json.RawMessage and
// core/errors typed errors.

// Error codes. The caller can look at these with errors.CodeOf; the API layer
// passes the same codes on to the client as well.
const (
	codeCollectionNotFound = "payment_collection_not_found"
	codeSessionNotFound    = "payment_session_not_found"
	// codeStoreCreditSessionNotFound is for a session missing from the
	// store-credit provider's own ledger; that is a SEPARATE record from the
	// module's session, and mixing them up would blur which ledger is speaking
	// (ADR 0152).
	codeStoreCreditSessionNotFound = "payment_store_credit_session_not_found"
	// codeLoyaltySessionNotFound is the same distinction for the loyalty-points
	// provider's own sessions (ADR 0165).
	codeLoyaltySessionNotFound = "payment_loyalty_session_not_found"
	codePaymentNotFound        = "payment_not_found"
	codeManualSessionNotFound  = "payment_manual_session_not_found"
	codeSessionExists          = "payment_session_idempotency_key_exists"
	codePaymentExists          = "payment_session_already_captured"
	codeAmountOutOfRange       = "payment_amount_out_of_range"
	codeInconsistentAmounts    = "payment_amounts_inconsistent"
	codeStatusInvalid          = "payment_status_invalid"
	codeCurrencyInvalid        = "payment_currency_invalid"
	codeDataInvalid            = "payment_json_invalid"
	codeTxRequired             = "payment_tx_required"
	codeTxBeginFailed          = "payment_tx_begin_failed"
	codeTxCommitFailed         = "payment_tx_commit_failed"
	codeQueryFailed            = "payment_query_failed"
	codeConcurrentUpdate       = "payment_concurrent_update"
)

// Constraint and index names; they are used to turn a driver error into a
// meaningful typed error. The names are EXACTLY the names in the migration.
const (
	constraintSessionIdempotencyUniq = "payment_sessions_provider_idempotency_uniq"
	constraintManualIdempotencyUniq  = "payment_manual_sessions_idempotency_uniq"
	constraintPaymentSessionUniq     = "payments_session_uniq"
	// constraintCurrencySuffix is the common suffix of every CHECK constraint
	// that checks the currency format; they are recognized by the suffix
	// instead of being listed one by one.
	constraintCurrencySuffix = "_currency_format"
	// constraintStatusSuffix is the common suffix of the CHECK constraints that
	// check the status value.
	constraintStatusSuffix = "_status_valid"
	// constraintPositiveSuffix is the common suffix of the CHECK constraints
	// that require a positive amount.
	constraintPositiveSuffix = "_amount_positive"
)

// The shared descriptions of the amount constraints.
const (
	// msgAuthorizedNonneg reports that the held amount cannot drop below zero.
	msgAuthorizedNonneg = "the authorized amount cannot be negative"
	// msgCapturedNonneg reports that the captured amount cannot drop below
	// zero.
	msgCapturedNonneg = "the captured amount cannot be negative"
	// msgRefundedNonneg reports that the refunded amount cannot drop below
	// zero.
	msgRefundedNonneg = "the refunded amount cannot be negative"
	// msgRefundLeCapture reports that a refund cannot exceed the capture.
	msgRefundLeCapture = "the refunded amount cannot exceed the captured amount"
)

// amountConstraints are the CHECK constraints that check the consistency
// between the amounts. Their violations are CONFLICT situations the client can
// correct: such as refunding money that does not exist, or taking more than
// was held.
var amountConstraints = map[string]string{
	"payment_collections_refund_le_capture":     msgRefundLeCapture,
	"payment_collections_authorized_le_amount":  "the authorized amount cannot exceed the collection amount",
	"payment_collections_captured_le_amount":    "the captured amount cannot exceed the collection amount",
	"payment_sessions_authorized_le_amount":     "the authorized amount cannot exceed the session amount",
	"payments_refund_le_amount":                 "the refunded amount cannot exceed the capture amount",
	"payment_manual_sessions_captured_le_auth":  "the captured amount cannot exceed the authorized amount",
	"payment_manual_sessions_refund_le_capture": msgRefundLeCapture,
	"payment_collections_authorized_nonneg":     msgAuthorizedNonneg,
	"payment_collections_captured_nonneg":       msgCapturedNonneg,
	"payment_collections_refunded_nonneg":       msgRefundedNonneg,
	"payment_sessions_authorized_nonneg":        msgAuthorizedNonneg,
	"payments_refunded_nonneg":                  msgRefundedNonneg,
	"payment_manual_sessions_authorized_nonneg": msgAuthorizedNonneg,
	"payment_manual_sessions_captured_nonneg":   msgCapturedNonneg,
	"payment_manual_sessions_refunded_nonneg":   msgRefundedNonneg,
}

// PostgreSQL SQLSTATE codes.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// collectionNotFound builds the shared error for a missing collection.
func collectionNotFound(id string) error {
	return errors.NotFound(codeCollectionNotFound, "payment collection not found: %s", id)
}

// sessionNotFound builds the shared error for a missing session.
func sessionNotFound(id string) error {
	return errors.NotFound(codeSessionNotFound, "payment session not found: %s", id)
}

// paymentNotFound builds the shared error for a missing capture.
func paymentNotFound(id string) error {
	return errors.NotFound(codePaymentNotFound, "capture not found: %s", id)
}

// manualSessionNotFound builds the shared error for a missing provider
// session.
func manualSessionNotFound(id string) error {
	return errors.NotFound(codeManualSessionNotFound,
		"manual provider session not found: %s", id)
}

// classify turns a driver error into a typed error.
//
// Uniqueness, foreign key and CHECK violations are situations the client can
// correct; if they were not classified, they would all show up as 500 and the
// real cause would stay only in the log. A deadlock is handled separately for
// the same reason: there is nothing wrong with the transaction itself, it CAN
// BE RETRIED.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		switch pgErr.ConstraintName {
		case constraintSessionIdempotencyUniq, constraintManualIdempotencyUniq:
			return errors.Wrap(err, errors.KindConflict, codeSessionExists,
				"a session opened with this idempotency key already exists")
		case constraintPaymentSessionUniq:
			return errors.Wrap(err, errors.KindConflict, codePaymentExists,
				"a capture has already come out of this session")
		}
	case sqlStateForeignKeyViolation:
		return foreignKeyError(err, pgErr.ConstraintName)
	case sqlStateCheckViolation:
		return checkError(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateDeadlockDetected:
		// Because the lock order is made uniform, this does not happen in the
		// normal flows; this is the last line of defense. The transaction has
		// been rolled back and the same request can be retried as it is —
		// hence Conflict, not Internal (500).
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"the request collided with a concurrent transaction; it can be retried")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// foreignKeyError turns a foreign key violation into a missing-parent-record
// error.
//
// The constraint name tells which parent record is missing: session and
// capture rows belong to a collection, a capture also belongs to a session,
// and a refund belongs to a capture.
func foreignKeyError(err error, constraint string) error {
	switch {
	case strings.Contains(constraint, "payment_session_id"):
		return errors.Wrap(err, errors.KindNotFound, codeSessionNotFound,
			"payment session not found")
	case strings.Contains(constraint, "payment_collection_id"):
		return errors.Wrap(err, errors.KindNotFound, codeCollectionNotFound,
			"payment collection not found")
	case strings.Contains(constraint, "payment_id"):
		return errors.Wrap(err, errors.KindNotFound, codePaymentNotFound,
			"capture not found")
	default:
		return errors.Wrap(err, errors.KindNotFound, codeCollectionNotFound,
			"the linked record was not found")
	}
}

// checkError turns a CHECK constraint violation into a meaningful typed error.
func checkError(err error, constraint, code, format string, a ...any) error {
	if message, ok := amountConstraints[constraint]; ok {
		return errors.Wrap(err, errors.KindConflict, codeInconsistentAmounts, "%s", message)
	}
	switch {
	case strings.HasSuffix(constraint, constraintPositiveSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"the amount has to be positive")
	case strings.HasSuffix(constraint, constraintCurrencySuffix):
		return errors.Wrap(err, errors.KindInvalid, codeCurrencyInvalid,
			"the currency has to be a three-letter ISO 4217 code")
	case strings.HasSuffix(constraint, constraintStatusSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeStatusInvalid,
			"undefined status value")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// --- conversion --------------------------------------------------------------

// toTime converts a pgtype timestamp into a UTC time.Time.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr converts a nullable timestamp into a *time.Time.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// fromTime converts a time.Time into a pgtype timestamp.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// fromTimePtr turns an optional moment into a nullable timestamp.
func fromTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return fromTime(*t)
}

// nullString turns an empty string into SQL NULL.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stringValue turns SQL NULL into an empty string.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// jsonOrEmpty fills an empty JSON body with '{}'.
//
// The column is NOT NULL and the distinction between "no data" and "empty
// data" means nothing in this module; a session without provider data carries
// an empty object.
func jsonOrEmpty(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

// toJSONRaw converts a jsonb column into raw JSON. An empty column returns nil.
func toJSONRaw(raw []byte) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

// toJSONMap converts a jsonb column into a map.
//
// An empty or JSON null value returns a nil map; that way, instead of
// "metadata": null, the field does not appear in the API response at all
// (omitempty).
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeDataInvalid,
			"the JSON field could not be decoded")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromJSONMap converts a map into the bytes to write to a jsonb column.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeDataInvalid,
			"the JSON field could not be encoded")
	}
	return raw, nil
}

// toCollection converts a database row into the domain model.
func toCollection(row paymentdb.PaymentCollection) (models.PaymentCollection, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.PaymentCollection{}, err
	}
	return models.PaymentCollection{
		ID:               row.ID,
		Reference:        row.Reference,
		CustomerID:       derefText(row.CustomerID),
		Amount:           row.Amount,
		CurrencyCode:     row.CurrencyCode,
		Status:           models.CollectionStatus(row.Status),
		AuthorizedAmount: row.AuthorizedAmount,
		CapturedAmount:   row.CapturedAmount,
		RefundedAmount:   row.RefundedAmount,
		Metadata:         meta,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}, nil
}

// toSession converts a database row into the domain model.
func toSession(row paymentdb.PaymentSession) models.PaymentSession {
	return models.PaymentSession{
		ID:                  row.ID,
		PaymentCollectionID: row.PaymentCollectionID,
		ProviderID:          row.ProviderID,
		ExternalID:          row.ExternalID,
		Status:              models.SessionStatus(row.Status),
		Amount:              row.Amount,
		AuthorizedAmount:    row.AuthorizedAmount,
		CurrencyCode:        row.CurrencyCode,
		Data:                toJSONRaw(row.Data),
		IdempotencyKey:      row.IdempotencyKey,
		DeclineReason:       stringValue(row.DeclineReason),
		CreatedAt:           toTime(row.CreatedAt),
		UpdatedAt:           toTime(row.UpdatedAt),
	}
}

// toPayment converts a database row into the domain model.
func toPayment(row paymentdb.Payment) models.Payment {
	return models.Payment{
		ID:                  row.ID,
		PaymentSessionID:    row.PaymentSessionID,
		PaymentCollectionID: row.PaymentCollectionID,
		Amount:              row.Amount,
		CurrencyCode:        row.CurrencyCode,
		RefundedAmount:      row.RefundedAmount,
		CapturedAt:          toTime(row.CapturedAt),
		CreatedAt:           toTime(row.CreatedAt),
		UpdatedAt:           toTime(row.UpdatedAt),
	}
}

// toRefund converts a database row into the domain model.
func toRefund(row paymentdb.Refund) models.Refund {
	return models.Refund{
		ID:        row.ID,
		PaymentID: row.PaymentID,
		Amount:    row.Amount,
		Reason:    stringValue(row.Reason),
		Reference: row.Reference,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
	}
}

// toManualSession converts a database row into the provider's ledger model.
func toManualSession(row paymentdb.PaymentManualSession) models.ManualSession {
	return models.ManualSession{
		ID:               row.ID,
		IdempotencyKey:   row.IdempotencyKey,
		Reference:        row.Reference,
		Amount:           row.Amount,
		CurrencyCode:     row.CurrencyCode,
		Status:           models.SessionStatus(row.Status),
		AuthorizedAmount: row.AuthorizedAmount,
		CapturedAmount:   row.CapturedAmount,
		RefundedAmount:   row.RefundedAmount,
		Data:             toJSONRaw(row.Data),
		DeclineReason:    stringValue(row.DeclineReason),
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}
}

// derefText turns a nullable text column into an empty string.
//
// NULL and the empty string mean the SAME thing in this module, and the schema
// refuses to write the second: "has no owner" is stored in one form, and the
// reading side reads it in one form too.
func derefText(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
