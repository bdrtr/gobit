package paymentpaytr

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// The statuses a payment row can hold. They are PayTR's own vocabulary rather
// than the core's, and deliberately so: this table records what PayTR said, and
// translating on the way in would lose the distinction between "PayTR has not
// called yet" and "PayTR called and said no".
const (
	statusPending = "pending"
	statusSuccess = "success"
	statusFailed  = "failed"
)

// Error codes.
const (
	codeStoreFailed = "paytr_store_failed"
	codeNotFound    = "paytr_payment_not_found"
)

// payment is one row: what we asked PayTR for and what it answered.
type payment struct {
	MerchantOID    string
	Amount         int64
	CurrencyCode   string
	Status         string
	PaidAmount     int64
	FailureReason  string
	RefundedAmount int64
	CallbackAt     *time.Time
}

// store is the plugin's data access; it owns exactly one table.
type store struct {
	pool *pgxpool.Pool
}

// newStore builds the store over the core pool.
func newStore(pool *pgxpool.Pool) *store { return &store{pool: pool} }

// open records a session before the customer is sent to PayTR.
//
// It is written BEFORE the get-token call rather than after, and the order
// matters: if the row were written afterwards, a process that died between
// PayTR accepting the payment and us recording the session would leave a
// customer able to pay against an order id this system has never heard of, and
// the callback would arrive for a row that does not exist.
//
// The insert is idempotent on the order id. A saga step can be retried
// (Principle 2.6), and a retry must reuse the session rather than open a second
// payment for the same cart.
func (s *store) open(ctx context.Context, p payment) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO paytr_payment (merchant_oid, amount, currency_code, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (merchant_oid) DO NOTHING`,
		p.MerchantOID, p.Amount, p.CurrencyCode, statusPending)
	if err != nil {
		return wrapDB(err, "the payment session could not be recorded")
	}

	return nil
}

// get reads what is known about a payment.
func (s *store) get(ctx context.Context, merchantOID string) (payment, error) {
	var p payment
	err := s.pool.QueryRow(ctx, `
		SELECT merchant_oid, amount, currency_code, status, paid_amount,
		       failure_reason, refunded_amount, callback_at
		FROM paytr_payment WHERE merchant_oid = $1`, merchantOID).
		Scan(&p.MerchantOID, &p.Amount, &p.CurrencyCode, &p.Status, &p.PaidAmount,
			&p.FailureReason, &p.RefundedAmount, &p.CallbackAt)
	if err != nil {
		return payment{}, wrapDB(err, "the payment could not be read")
	}

	return p, nil
}

// recordCallback stores what PayTR reported.
//
// # It is written ONCE
//
// PayTR retries a callback it believes was not acknowledged, so the same
// notification arrives more than once as a matter of course. The update is
// therefore guarded on the status still being pending: a second callback for a
// payment already reported changes nothing, and a LATER callback cannot
// overturn an earlier outcome.
//
// That guard is the whole defense against a replayed notification. A forged one
// cannot get this far — the signature is verified first — but a genuine one
// captured and re-sent could otherwise flip a failed payment to success, or
// re-run whatever the first success triggered.
func (s *store) recordCallback(
	ctx context.Context, merchantOID, status string, paidAmount int64, reason string,
) (applied bool, err error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE paytr_payment
		SET status = $2, paid_amount = $3, failure_reason = $4,
		    callback_at = now(), updated_at = now()
		WHERE merchant_oid = $1 AND status = $5`,
		merchantOID, status, paidAmount, reason, statusPending)
	if err != nil {
		return false, wrapDB(err, "the callback could not be recorded")
	}

	return tag.RowsAffected() == 1, nil
}

// addRefund accumulates a refunded amount.
//
// PayTR offers no "how much has been refunded" query, so this column is the
// only ledger of it. Without one, a retried refund step would send the money
// back twice and nothing on either side would say so.
func (s *store) addRefund(ctx context.Context, merchantOID string, amount int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE paytr_payment
		SET refunded_amount = refunded_amount + $2, updated_at = now()
		WHERE merchant_oid = $1`, merchantOID, amount)
	if err != nil {
		return wrapDB(err, "the refund could not be recorded")
	}

	return nil
}

// pending lists payments PayTR has not reported on.
//
// This is the operator's view of the gap that has no automatic answer: a
// customer who paid and then closed the browser leaves a row here with money
// taken and no order. gobit already has the vocabulary for that class of
// half-finished work (ADR 0016/0017) and this list is what points at it.
func (s *store) pending(ctx context.Context, olderThan time.Duration, limit int) ([]payment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT merchant_oid, amount, currency_code, status, paid_amount,
		       failure_reason, refunded_amount, callback_at
		FROM paytr_payment
		WHERE status = $1 AND created_at < now() - $2::interval
		ORDER BY created_at
		LIMIT $3`, statusPending, olderThan.String(), limit)
	if err != nil {
		return nil, wrapDB(err, "the pending payments could not be listed")
	}
	defer rows.Close()

	var out []payment
	for rows.Next() {
		var p payment
		if err := rows.Scan(&p.MerchantOID, &p.Amount, &p.CurrencyCode, &p.Status,
			&p.PaidAmount, &p.FailureReason, &p.RefundedAmount, &p.CallbackAt); err != nil {
			return nil, wrapDB(err, "a payment row could not be read")
		}
		out = append(out, p)
	}

	return out, rows.Err()
}

// wrapDB turns a driver error into a classified one.
func wrapDB(err error, message string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return coreerrors.Wrap(err, coreerrors.KindNotFound, codeNotFound, "%s", message)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeStoreFailed, "%s", message)
	}

	return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeStoreFailed, "%s", message)
}
