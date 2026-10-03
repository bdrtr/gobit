// Package repository is the payment module's database access.
//
// It touches ONLY this module's tables (plan Section 4). The sqlc-generated
// code is under repository/paymentdb and is not edited by hand; this package
// adds two things on top of it:
//
//   - Conversion: pgtype and the generated row types DO NOT LEAVE THIS
//     PACKAGE; they are converted into models types.
//   - Classification: driver errors are turned into core/errors typed errors;
//     a row not being found becomes NotFound, a uniqueness violation becomes
//     Conflict.
//
// # Carrying the transaction
//
// [Repository.WithTx] opens a transaction and puts it into the CONTEXT; every
// repository method called during the transaction runs in the same transaction
// as long as it receives that context. The alternative was to put a separate
// interface type carrying the transaction handle into the method signatures;
// in that case the service could not match this package STRUCTURALLY with the
// narrow interface it defines in its own package — in Go the named types in a
// signature have to be exactly the same, so the service would have been forced
// to import the repository. Carrying it in the context reduces the signatures
// to types both sides share (context.Context, models.*).
//
// The methods that take a lock (Lock...) return an error if they are called
// OUTSIDE a transaction: since a FOR UPDATE lock is released when the
// transaction ends, a lock without a transaction would silently protect
// nothing.
//
// # Two kinds of owner
//
// This package serves the data of two kinds of owner: the payment module's own
// tables (payment_collections, payment_sessions, payments, refunds, the store
// credit and loyalty ledgers, and the gift cards with their ledger) and the
// PROVIDERS' own session ledgers (payment_manual_sessions and the store-credit,
// loyalty and gift-card providers' sessions). The second kind is not the
// module's domain data; it is the state of the system each provider stands
// for, and only its provider writes it. Two readers sit outside the providers:
// the module's personal-data answer (ADR 0277), which reads those sessions to
// disclose what they keep about a customer, and closing a gift card (ADR 0213),
// which counts the card's sessions still holding part of it. The separation is
// also kept physically: the service's
// [github.com/bdrtr/gobit/internal/modules/payment/service.Store] interface has
// NO manual-ledger methods, and of the providers' sessions it reads only that
// count.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// rollbackTimeout is the time allowed for a rollback in a canceled context.
// The rollback has to be attempted even if the caller's ctx has expired;
// otherwise the transaction would stay open until the connection returned to
// the pool.
const rollbackTimeout = 5 * time.Second

// txKeyType is the type of the context key; it is unexported so that it cannot
// be produced from outside.
type txKeyType struct{}

// txKey is the transaction handle's key in the context.
var txKey = txKeyType{}

// Repository is the access to the payment tables. It is safe for concurrent
// use.
type Repository struct {
	pool *pgxpool.Pool
}

// New builds a Repository that works on the given pool.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// WithTx runs fn in a single database transaction.
//
// The context given to fn carries the transaction; every repository method
// called with that context runs in the same transaction. If fn returns an
// error or panics, the transaction is rolled back and the error (on a panic,
// the panic) is passed up.
//
// If the call comes nested, a new transaction is NOT OPENED; the existing one
// is used: opening a nested transaction means a savepoint in PostgreSQL and
// would give a misleading confidence about the atomicity of the outer
// transaction.
//
// # The isolation level is named, not inherited
//
// The balance tenders lock a balance and then sum it, and the sum is safe only
// because it is a fresh statement with a fresh snapshot: the authorization that
// waited on the lock reads the hold the first one wrote. That is READ
// COMMITTED. Under REPEATABLE READ the snapshot is taken at the transaction's
// first statement — the session lock, before the wait — and the sum after the
// wait reads the balance as it was before the other hold; both authorizations
// pass. A plain BEGIN runs at the server's default, which a role or a database
// can set to anything, so the level this module's locks rest on is written here
// (D119).
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return classify(err, codeTxBeginFailed, "the transaction could not begin")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// A short-lived context detached from the caller's is used: if the
		// caller's ctx was canceled, a rollback made with it would fail at once
		// as well.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, codeTxCommitFailed, "the transaction could not be committed")
	}
	committed = true
	return nil
}

// WriteOutboxEvent writes the event to the outbox INSIDE THE CALLER'S
// TRANSACTION.
//
// The contract is the same as the order module's, and so is the reason: an
// event has to commit TOGETHER with the write that gives rise to it. Being
// called outside a transaction is a fault and is refused instead of written
// silently — a row that promises an event for work that may never commit
// contradicts the guarantee itself.
//
// The outbox package writes the row; that is also why it is right that this
// module's migrations do not touch the event_outbox table: core/eventbus/outbox
// owns the table.
func (r *Repository) WriteOutboxEvent(
	ctx context.Context, id, name string, data map[string]any,
) error {
	tx, inTx := txFromContext(ctx)
	if !inTx {
		return errors.Internal(codeQueryFailed,
			"an outbox event may only be written inside a transaction (%s); a row written outside one "+
				"promises an event for work that may never commit", name)
	}

	return outbox.Write(ctx, tx, eventbus.Event{ID: id, Name: name, Data: data})
}

// txFromContext returns the transaction handle in the context.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries returns the query set that fits the context: the one bound to the
// transaction if there is one, otherwise the one bound to the pool.
func (r *Repository) queries(ctx context.Context) *paymentdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return paymentdb.New(tx)
	}
	return paymentdb.New(r.pool)
}

// requireTx verifies that the lock-taking methods are called inside a
// transaction.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s has to be called inside a transaction; a lock taken outside one protects nothing", op)
	}
	return nil
}

// --- payment collections -----------------------------------------------------

// CreatePaymentCollection records a new payment collection.
func (r *Repository) CreatePaymentCollection(
	ctx context.Context,
	col models.PaymentCollection,
) (models.PaymentCollection, error) {
	meta, err := fromJSONMap(col.Metadata)
	if err != nil {
		return models.PaymentCollection{}, err
	}

	row, err := r.queries(ctx).CreatePaymentCollection(ctx, paymentdb.CreatePaymentCollectionParams{
		ID:           col.ID,
		Reference:    col.Reference,
		Amount:       col.Amount,
		CurrencyCode: col.CurrencyCode,
		Status:       col.Status.String(),
		Metadata:     meta,
		// An empty customer identifier is written as NULL: "this collection has
		// no owner" and "its owner is the empty string" are not the same thing,
		// and the schema refuses the second anyway.
		CustomerID: nullText(col.CustomerID),
	})
	if err != nil {
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "the payment collection could not be created")
	}
	return toCollection(row)
}

// GetPaymentCollection returns the collection by its identifier; NotFound if
// there is none.
func (r *Repository) GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	row, err := r.queries(ctx).GetPaymentCollection(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentCollection{}, collectionNotFound(id)
		}
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "the payment collection could not be read")
	}
	return toCollection(row)
}

// LockPaymentCollection locks the collection for the rest of the transaction
// and returns its current state. EVERY write flow on a collection starts here;
// it is the first step of the lock order.
func (r *Repository) LockPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	if err := requireTx(ctx, "LockPaymentCollection"); err != nil {
		return models.PaymentCollection{}, err
	}
	row, err := r.queries(ctx).LockPaymentCollection(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentCollection{}, collectionNotFound(id)
		}
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "the payment collection could not be locked")
	}
	return toCollection(row)
}

// ListPaymentCollections returns the collections, filtered and paged.
// The second return value is the count of ALL the rows that match the filter.
//
// The total comes from a SEPARATE query and applies the same filters as the
// list; it is correct even when the page is out of range and no row comes back.
// A row written between the two queries can change the total by one: the total
// is an informational field of the paging envelope, and no decision to act is
// based on it.
func (r *Repository) ListPaymentCollections(
	ctx context.Context,
	filter models.CollectionFilter,
) ([]models.PaymentCollection, int64, error) {
	rows, err := r.queries(ctx).ListPaymentCollections(ctx, paymentdb.ListPaymentCollectionsParams{
		Reference: filter.Reference,
		Status:    filter.Status,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the payment collections could not be listed")
	}

	total, err := r.queries(ctx).CountPaymentCollections(ctx, paymentdb.CountPaymentCollectionsParams{
		Reference: filter.Reference,
		Status:    filter.Status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the payment collections could not be counted")
	}

	out := make([]models.PaymentCollection, 0, len(rows))
	for i := range rows {
		col, convErr := toCollection(rows[i])
		if convErr != nil {
			return nil, 0, convErr
		}
		out = append(out, col)
	}
	return out, total, nil
}

// PaymentMomentsByCollectionIDs returns, for each collection, WHEN its money
// moved — in a SINGLE query.
//
// It is separate from the collection read on purpose. The moments live in two
// other tables and cost two correlated aggregates; charging every collection
// read for them would make the common question (how much) pay for the rare one
// (when). The caller asks only when it needs them.
func (r *Repository) PaymentMomentsByCollectionIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentMoments, error) {
	if len(ids) == 0 {
		return []models.PaymentMoments{}, nil
	}

	rows, err := r.queries(ctx).PaymentMomentsByCollectionIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the payment moments could not be read")
	}

	out := make([]models.PaymentMoments, 0, len(rows))
	for i := range rows {
		out = append(out, models.PaymentMoments{
			CollectionID:    rows[i].PaymentCollectionID,
			FirstCapturedAt: toTimePtr(rows[i].FirstCapturedAt),
			LastRefundedAt:  toTimePtr(rows[i].LastRefundedAt),
		})
	}

	return out, nil
}

// PaymentMovementsByCollectionIDs returns every capture and refund of the
// collections, in one query, ordered by collection and then by moment.
func (r *Repository) PaymentMovementsByCollectionIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentMovement, error) {
	if len(ids) == 0 {
		return []models.PaymentMovement{}, nil
	}

	rows, err := r.queries(ctx).PaymentMovementsByCollectionIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the payment movements could not be read")
	}

	out := make([]models.PaymentMovement, 0, len(rows))
	for i := range rows {
		out = append(out, models.PaymentMovement{
			CollectionID: rows[i].PaymentCollectionID,
			ID:           rows[i].MovementID,
			PaymentID:    rows[i].PaymentID,
			Kind:         rows[i].Kind,
			Amount:       rows[i].Amount,
			At:           toTime(rows[i].MovedAt),
			Reference:    rows[i].Reference,
		})
	}

	return out, nil
}

// PaymentCollectionsByIDs returns the collections of the given identifiers in
// a SINGLE query. No row comes back for an identifier that is not found; that
// is not an error.
func (r *Repository) PaymentCollectionsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentCollection, error) {
	if len(ids) == 0 {
		return []models.PaymentCollection{}, nil
	}
	rows, err := r.queries(ctx).GetPaymentCollectionsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the payment collections could not be read")
	}

	out := make([]models.PaymentCollection, 0, len(rows))
	for i := range rows {
		col, convErr := toCollection(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, col)
	}
	return out, nil
}

// UpdatePaymentCollectionTotals writes the collection's amounts and its
// derived status as ABSOLUTE values.
//
// An incremental update is deliberately not used: the new value is computed
// from the value read under the lock, and the number the deciding code saw is
// the number that gets written.
func (r *Repository) UpdatePaymentCollectionTotals(
	ctx context.Context,
	id string,
	status models.CollectionStatus,
	authorized, captured, refunded int64,
) (models.PaymentCollection, error) {
	row, err := r.queries(ctx).UpdatePaymentCollectionTotals(ctx, paymentdb.UpdatePaymentCollectionTotalsParams{
		ID:               id,
		Status:           status.String(),
		AuthorizedAmount: authorized,
		CapturedAmount:   captured,
		RefundedAmount:   refunded,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentCollection{}, collectionNotFound(id)
		}
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "the payment collection could not be updated")
	}
	return toCollection(row)
}

// --- payment sessions --------------------------------------------------------

// CreatePaymentSession records a new payment session.
// If the same (provider, idempotency key) pair already exists, Conflict is
// returned.
func (r *Repository) CreatePaymentSession(
	ctx context.Context,
	ses models.PaymentSession,
) (models.PaymentSession, error) {
	row, err := r.queries(ctx).CreatePaymentSession(ctx, paymentdb.CreatePaymentSessionParams{
		ID:                  ses.ID,
		PaymentCollectionID: ses.PaymentCollectionID,
		ProviderID:          ses.ProviderID,
		ExternalID:          ses.ExternalID,
		Status:              ses.Status.String(),
		Amount:              ses.Amount,
		AuthorizedAmount:    ses.AuthorizedAmount,
		CurrencyCode:        ses.CurrencyCode,
		Data:                jsonOrEmpty(ses.Data),
		IdempotencyKey:      ses.IdempotencyKey,
	})
	if err != nil {
		return models.PaymentSession{}, classify(err, codeQueryFailed, "the payment session could not be created")
	}
	return toSession(row), nil
}

// GetPaymentSession returns the session by its identifier; NotFound if there is
// none.
func (r *Repository) GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	row, err := r.queries(ctx).GetPaymentSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, sessionNotFound(id)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "the payment session could not be read")
	}
	return toSession(row), nil
}

// LockPaymentSession locks the session for the rest of the transaction and
// returns its current state.
//
// State transitions happen only under this lock: of two calls trying to
// authorize the same session at the same time, the second sees the state the
// first wrote and does NOT go to the provider A SECOND TIME.
func (r *Repository) LockPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	if err := requireTx(ctx, "LockPaymentSession"); err != nil {
		return models.PaymentSession{}, err
	}
	row, err := r.queries(ctx).LockPaymentSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, sessionNotFound(id)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "the payment session could not be locked")
	}
	return toSession(row), nil
}

// PaymentSessionByIdempotencyKey returns the session opened with the same
// key; NotFound if there is none.
func (r *Repository) PaymentSessionByIdempotencyKey(
	ctx context.Context,
	providerID, key string,
) (models.PaymentSession, error) {
	row, err := r.queries(ctx).GetPaymentSessionByIdempotencyKey(ctx,
		paymentdb.GetPaymentSessionByIdempotencyKeyParams{
			ProviderID:     providerID,
			IdempotencyKey: key,
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, errors.NotFound(codeSessionNotFound,
				"no session was opened with this idempotency key: %s/%s", providerID, key)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "the payment session could not be read")
	}
	return toSession(row), nil
}

// ListPaymentSessionsByCollection returns the collection's sessions.
func (r *Repository) ListPaymentSessionsByCollection(
	ctx context.Context,
	collectionID string,
) ([]models.PaymentSession, error) {
	rows, err := r.queries(ctx).ListPaymentSessionsByCollection(ctx, collectionID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the payment sessions could not be listed")
	}

	out := make([]models.PaymentSession, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}
	return out, nil
}

// SessionCounts counts the collection's sessions by status in a SINGLE query.
func (r *Repository) SessionCounts(ctx context.Context, collectionID string) (models.SessionCounts, error) {
	row, err := r.queries(ctx).CountPaymentSessionStates(ctx, collectionID)
	if err != nil {
		return models.SessionCounts{}, classify(err, codeQueryFailed, "the payment sessions could not be counted")
	}
	return models.SessionCounts{
		Live:     row.LiveCount,
		Canceled: row.CanceledCount,
		Failed:   row.FailedCount,
		Total:    row.TotalCount,
	}, nil
}

// LiveSessionAmount returns, in a SINGLE query, the total amount the
// collection's LIVE sessions have reserved; 0 if there is no live session.
//
// A pending session reserves its own amount, an authorized session the amount
// it held (the rationale is next to the query). It has to be read UNDER the
// collection lock: a total read without the lock would go stale with a
// session opening in between, and more than the collection's amount could be
// reserved.
func (r *Repository) LiveSessionAmount(ctx context.Context, collectionID string) (int64, error) {
	reserved, err := r.queries(ctx).SumLiveSessionAmounts(ctx, collectionID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the live session amounts could not be summed")
	}
	return reserved, nil
}

// UpdatePaymentSessionState writes the session's status, authorized amount,
// raw provider data and decline reason as ABSOLUTE values.
func (r *Repository) UpdatePaymentSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorizedAmount int64,
	data []byte,
	declineReason string,
) (models.PaymentSession, error) {
	row, err := r.queries(ctx).UpdatePaymentSessionState(ctx, paymentdb.UpdatePaymentSessionStateParams{
		ID:               id,
		Status:           status.String(),
		AuthorizedAmount: authorizedAmount,
		Data:             jsonOrEmpty(data),
		DeclineReason:    nullString(declineReason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, sessionNotFound(id)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "the payment session could not be updated")
	}
	return toSession(row), nil
}

// --- captures ----------------------------------------------------------------

// CreatePayment records a new capture.
// If the session already has a capture, Conflict is returned.
func (r *Repository) CreatePayment(ctx context.Context, pay models.Payment) (models.Payment, error) {
	row, err := r.queries(ctx).CreatePayment(ctx, paymentdb.CreatePaymentParams{
		ID:                  pay.ID,
		PaymentSessionID:    pay.PaymentSessionID,
		PaymentCollectionID: pay.PaymentCollectionID,
		Amount:              pay.Amount,
		CurrencyCode:        pay.CurrencyCode,
		CapturedAt:          fromTime(pay.CapturedAt),
	})
	if err != nil {
		return models.Payment{}, classify(err, codeQueryFailed, "the capture could not be created")
	}
	return toPayment(row), nil
}

// GetPayment returns the capture by its identifier; NotFound if there is none.
func (r *Repository) GetPayment(ctx context.Context, id string) (models.Payment, error) {
	row, err := r.queries(ctx).GetPayment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, paymentNotFound(id)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "the capture could not be read")
	}
	return toPayment(row), nil
}

// LockPayment locks the capture for the rest of the transaction and returns its
// current state. The refunded amount is updated only under this lock.
func (r *Repository) LockPayment(ctx context.Context, id string) (models.Payment, error) {
	if err := requireTx(ctx, "LockPayment"); err != nil {
		return models.Payment{}, err
	}
	row, err := r.queries(ctx).LockPayment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, paymentNotFound(id)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "the capture could not be locked")
	}
	return toPayment(row), nil
}

// PaymentBySession returns the capture that came from the session; NotFound if
// there is none.
func (r *Repository) PaymentBySession(ctx context.Context, sessionID string) (models.Payment, error) {
	row, err := r.queries(ctx).GetPaymentBySession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, errors.NotFound(codePaymentNotFound,
				"no capture came from the session: %s", sessionID)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "the capture could not be read")
	}
	return toPayment(row), nil
}

// ListPaymentsByCollection returns the collection's captures.
func (r *Repository) ListPaymentsByCollection(ctx context.Context, collectionID string) ([]models.Payment, error) {
	rows, err := r.queries(ctx).ListPaymentsByCollection(ctx, collectionID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the captures could not be listed")
	}

	out := make([]models.Payment, 0, len(rows))
	for i := range rows {
		out = append(out, toPayment(rows[i]))
	}
	return out, nil
}

// UpdatePaymentRefundedAmount writes the capture's refunded amount as an
// ABSOLUTE value.
func (r *Repository) UpdatePaymentRefundedAmount(
	ctx context.Context,
	id string,
	refunded int64,
) (models.Payment, error) {
	row, err := r.queries(ctx).UpdatePaymentRefundedAmount(ctx, paymentdb.UpdatePaymentRefundedAmountParams{
		ID:             id,
		RefundedAmount: refunded,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, paymentNotFound(id)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "the capture could not be updated")
	}
	return toPayment(row), nil
}

// --- refunds -----------------------------------------------------------------

// CreateRefund records a new refund.
func (r *Repository) CreateRefund(ctx context.Context, ref models.Refund) (models.Refund, error) {
	row, err := r.queries(ctx).CreateRefund(ctx, paymentdb.CreateRefundParams{
		ID:        ref.ID,
		PaymentID: ref.PaymentID,
		Amount:    ref.Amount,
		Reason:    nullString(ref.Reason),
		Reference: ref.Reference,
	})
	if err != nil {
		return models.Refund{}, classify(err, codeQueryFailed, "the refund could not be created")
	}
	return toRefund(row), nil
}

// ListRefundsByPayment returns the capture's refunds.
func (r *Repository) ListRefundsByPayment(ctx context.Context, paymentID string) ([]models.Refund, error) {
	rows, err := r.queries(ctx).ListRefundsByPayment(ctx, paymentID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the refunds could not be listed")
	}

	out := make([]models.Refund, 0, len(rows))
	for i := range rows {
		out = append(out, toRefund(rows[i]))
	}
	return out, nil
}

// --- the manual provider's ledger --------------------------------------------

// InsertManualSessionIfAbsent writes the session only if the idempotency key
// has not been used yet. The second return value reports whether the row WAS
// WRITTEN.
//
// A conflict is NOT an error: the provider contract requires a second call
// with the same key to return the existing session. Combining the write and
// the read in one statement also keeps a concurrent call that slips in between
// "read first, then write" from running into the unique index.
func (r *Repository) InsertManualSessionIfAbsent(
	ctx context.Context,
	ses models.ManualSession,
) (models.ManualSession, bool, error) {
	row, err := r.queries(ctx).InsertManualSessionIfAbsent(ctx, paymentdb.InsertManualSessionIfAbsentParams{
		ID:             ses.ID,
		IdempotencyKey: ses.IdempotencyKey,
		Reference:      ses.Reference,
		Amount:         ses.Amount,
		CurrencyCode:   ses.CurrencyCode,
		Status:         ses.Status.String(),
		Data:           jsonOrEmpty(ses.Data),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, false, nil
		}
		return models.ManualSession{}, false, classify(err, codeQueryFailed,
			"the manual provider session could not be created")
	}
	return toManualSession(row), true, nil
}

// ManualSession returns the provider session by its identifier; NotFound if
// there is none.
func (r *Repository) ManualSession(ctx context.Context, id string) (models.ManualSession, error) {
	row, err := r.queries(ctx).GetManualSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, manualSessionNotFound(id)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"the manual provider session could not be read")
	}
	return toManualSession(row), nil
}

// ManualSessionByIdempotencyKey returns the provider session by its key;
// NotFound if there is none.
func (r *Repository) ManualSessionByIdempotencyKey(ctx context.Context, key string) (models.ManualSession, error) {
	row, err := r.queries(ctx).GetManualSessionByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, errors.NotFound(codeManualSessionNotFound,
				"no provider session was opened with this idempotency key: %s", key)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"the manual provider session could not be read")
	}
	return toManualSession(row), nil
}

// LockManualSession locks the provider session for the rest of the
// transaction and returns its current state. The provider's state transitions
// happen only under this lock.
func (r *Repository) LockManualSession(ctx context.Context, id string) (models.ManualSession, error) {
	if err := requireTx(ctx, "LockManualSession"); err != nil {
		return models.ManualSession{}, err
	}
	row, err := r.queries(ctx).LockManualSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, manualSessionNotFound(id)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"the manual provider session could not be locked")
	}
	return toManualSession(row), nil
}

// UpdateManualSessionState writes the provider session's status and amounts as
// ABSOLUTE values.
func (r *Repository) UpdateManualSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.ManualSession, error) {
	row, err := r.queries(ctx).UpdateManualSessionState(ctx, paymentdb.UpdateManualSessionStateParams{
		ID:               id,
		Status:           status.String(),
		AuthorizedAmount: authorized,
		CapturedAmount:   captured,
		RefundedAmount:   refunded,
		DeclineReason:    nullString(declineReason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, manualSessionNotFound(id)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"the manual provider session could not be updated")
	}
	return toManualSession(row), nil
}

// nullText writes an empty string as NULL.
//
// "This collection has no owner" and "its owner is the empty string" are not
// the same thing, and the schema refuses the second anyway; writing a single
// form leaves a single check on the reading side too (see derefText).
func nullText(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
