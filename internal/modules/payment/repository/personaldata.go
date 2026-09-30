package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// The reads behind the payment module's answers about one person (ADR 0277);
// the statements are in queries/personaldata.sql. Nothing here locks or
// writes, and a caller that needs the reads of one answer to agree wraps them
// in [Repository.WithReadTx].

// WithReadTx runs fn in one read-only snapshot, so the reads of one answer see
// one instant. It takes no lock and writes nothing; a call already inside a
// transaction runs in it.
func (r *Repository) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return classify(err, codeTxBeginFailed, "the read-only transaction could not begin")
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	return fn(context.WithValue(ctx, txKey, tx))
}

// CollectionsOfCustomer reads the collections that name the customer.
func (r *Repository) CollectionsOfCustomer(
	ctx context.Context, customerID string,
) ([]models.PaymentCollection, error) {
	rows, err := r.queries(ctx).ListPaymentCollectionsOfCustomer(ctx, customerID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the customer's payment collections could not be read")
	}

	out := make([]models.PaymentCollection, 0, len(rows))
	for i := range rows {
		collection, err := toCollection(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, collection)
	}

	return out, nil
}

// SessionsOfCollections reads the payment sessions of the given collections.
func (r *Repository) SessionsOfCollections(
	ctx context.Context, collectionIDs []string,
) ([]models.PaymentSession, error) {
	if len(collectionIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListPaymentSessionsOfCollections(ctx, collectionIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the collections' payment sessions could not be read")
	}

	out := make([]models.PaymentSession, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}

	return out, nil
}

// PaymentsOfCollections reads the captures of the given collections.
func (r *Repository) PaymentsOfCollections(
	ctx context.Context, collectionIDs []string,
) ([]models.Payment, error) {
	if len(collectionIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListPaymentsOfCollections(ctx, collectionIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the collections' payments could not be read")
	}

	out := make([]models.Payment, 0, len(rows))
	for i := range rows {
		out = append(out, toPayment(rows[i]))
	}

	return out, nil
}

// RefundsOfPayments reads the refunds of the given captures.
func (r *Repository) RefundsOfPayments(ctx context.Context, paymentIDs []string) ([]models.Refund, error) {
	if len(paymentIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListRefundsOfPayments(ctx, paymentIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the payments' refunds could not be read")
	}

	out := make([]models.Refund, 0, len(rows))
	for i := range rows {
		out = append(out, toRefund(rows[i]))
	}

	return out, nil
}

// ManualSessionsOfCollections reads the manual provider's sessions opened for
// the given collections.
func (r *Repository) ManualSessionsOfCollections(
	ctx context.Context, collectionIDs []string,
) ([]models.ManualSession, error) {
	if len(collectionIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListManualSessionsOfCollections(ctx, collectionIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the collections' manual sessions could not be read")
	}

	out := make([]models.ManualSession, 0, len(rows))
	for i := range rows {
		out = append(out, toManualSession(rows[i]))
	}

	return out, nil
}

// GiftCardSessionsOfCollections reads the gift card sessions opened for the
// given collections.
func (r *Repository) GiftCardSessionsOfCollections(
	ctx context.Context, collectionIDs []string,
) ([]models.TenderSession, error) {
	if len(collectionIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListGiftCardSessionsOfCollections(ctx, collectionIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the collections' gift card sessions could not be read")
	}

	out := make([]models.TenderSession, 0, len(rows))
	for i := range rows {
		out = append(out, toGiftCardSession(rows[i]))
	}

	return out, nil
}

// StoreCreditEntriesOfCustomer reads the customer's store credit ledger, every
// currency.
func (r *Repository) StoreCreditEntriesOfCustomer(
	ctx context.Context, customerID string,
) ([]models.StoreCreditEntry, error) {
	rows, err := r.queries(ctx).ListStoreCreditEntriesOfCustomer(ctx, customerID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the customer's store credit could not be read")
	}

	out := make([]models.StoreCreditEntry, 0, len(rows))
	for i := range rows {
		out = append(out, toStoreCreditEntry(rows[i]))
	}

	return out, nil
}

// StoreCreditSessionsOfCustomer reads the sessions that spent the customer's
// store credit.
func (r *Repository) StoreCreditSessionsOfCustomer(
	ctx context.Context, customerID string,
) ([]models.TenderSession, error) {
	rows, err := r.queries(ctx).ListStoreCreditSessionsOfCustomer(ctx, customerID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the customer's store credit sessions could not be read")
	}

	out := make([]models.TenderSession, 0, len(rows))
	for i := range rows {
		out = append(out, toStoreCreditSession(rows[i]))
	}

	return out, nil
}

// LoyaltyEntriesOfCustomer reads the customer's loyalty ledger, every
// currency.
func (r *Repository) LoyaltyEntriesOfCustomer(
	ctx context.Context, customerID string,
) ([]models.LoyaltyEntry, error) {
	rows, err := r.queries(ctx).ListLoyaltyEntriesOfCustomer(ctx, customerID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the customer's loyalty points could not be read")
	}

	out := make([]models.LoyaltyEntry, 0, len(rows))
	for i := range rows {
		out = append(out, toLoyaltyEntry(rows[i]))
	}

	return out, nil
}

// LoyaltySessionsOfCustomer reads the sessions that spent the customer's
// points.
func (r *Repository) LoyaltySessionsOfCustomer(
	ctx context.Context, customerID string,
) ([]models.TenderSession, error) {
	rows, err := r.queries(ctx).ListLoyaltySessionsOfCustomer(ctx, customerID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the customer's loyalty sessions could not be read")
	}

	out := make([]models.TenderSession, 0, len(rows))
	for i := range rows {
		out = append(out, toLoyaltySession(rows[i]))
	}

	return out, nil
}
