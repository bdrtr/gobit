// Package repository is the database access of the notification module.
//
// It touches ONLY the table of this module (plan Section 4). The sqlc generated
// code is under repository/notificationdb and is not edited by hand; this
// package adds two things on top of it:
//
//   - Conversion: pgtype and the generated row types DO NOT LEAVE THIS
//     PACKAGE, they are converted to models types.
//   - Classification: driver errors are converted into errors typed by
//     core/errors; a missing row becomes NotFound, a uniqueness violation
//     Conflict (plan Section 2.7 — the handler does not pick the status code).
//
// # THERE IS NO TRANSACTION and none is needed
//
// The repositories of the other modules carry WithTx; here there is none.
// Writing the log consists of two single statements (open the record, write the
// outcome) and the provider is reached BETWEEN the two — that is, taking the
// two into a single transaction would have meant holding a transaction open for
// the duration of a network call. Had the process died while the transaction
// was open, the record would never have been written and the uniqueness key
// that stops a duplicate send would never have come into being either;
// separate statements guarantee the opposite: the record is ALWAYS durable
// before the send.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/repository/notificationdb"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeDeliveryNotFound reports that the requested log record was not found.
	CodeDeliveryNotFound = "notification_delivery_not_found"
	// CodeDeliveryExists reports that a second record was to be opened for the
	// same (template, reference).
	CodeDeliveryExists = "notification_delivery_already_exists"
	// CodeConstraintViolation reports that a database constraint was violated.
	CodeConstraintViolation = "notification_constraint_violation"
	// CodeQueryFailed reports an unexpected database error.
	CodeQueryFailed = "notification_query_failed"
	// CodeCanceled reports a context cancellation.
	CodeCanceled = "notification_canceled"
	// CodeNotReady reports that the repository was constructed without a pool.
	CodeNotReady = "notification_repository_not_ready"
)

// PostgreSQL SQLSTATE codes (the ones that are needed).
const (
	sqlstateUniqueViolation      = "23505"
	sqlstateCheckViolation       = "23514"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// constraintTemplateReferenceUniq is the name of the index that enforces the
// idempotency key; it is EXACTLY the name in the migration.
const constraintTemplateReferenceUniq = "notification_deliveries_template_reference_uniq"

// Repo provides the access to the delivery log. It is safe for concurrent use.
type Repo struct {
	q *notificationdb.Queries
}

// New produces a repository working on the given pool.
//
// When pool is nil this is reported as a typed error on the first call, not at
// construction; the construction path produces no panic.
func New(pool *pgxpool.Pool) *Repo {
	if pool == nil {
		return &Repo{}
	}
	return &Repo{q: notificationdb.New(pool)}
}

// ready verifies that the pool is usable.
func (r *Repo) ready() error {
	if r == nil || r.q == nil {
		return errors.Unavailable(CodeNotReady, "the notification database pool is not set up")
	}
	return nil
}

// ClaimDelivery writes the log record only if that (template, reference) pair
// has NOT BEEN USED YET. The second return value is whether the row WAS
// written.
//
// A conflict IS NOT an error: the same notification being triggered a second
// time is an expected situation (a republished event, a manual trigger) and the
// right answer is not an error but SKIPPING. When the caller sees false it does
// NOT go to the provider at all.
func (r *Repo) ClaimDelivery(ctx context.Context, d models.Delivery) (models.Delivery, bool, error) {
	if err := r.ready(); err != nil {
		return models.Delivery{}, false, err
	}

	row, err := r.q.ClaimNotificationDelivery(ctx, notificationdb.ClaimNotificationDeliveryParams{
		ID:         d.ID,
		Template:   d.Template,
		Channel:    d.Channel,
		Reference:  d.Reference,
		ProviderID: d.ProviderID,
		Status:     d.Status.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Delivery{}, false, nil
		}
		return models.Delivery{}, false, classify(err, "could not open the notification log record: %s/%s",
			d.Template, d.Reference)
	}
	return toDelivery(row), true, nil
}

// FinishDelivery writes the outcome of the send attempt; NotFound when there is
// no record.
func (r *Repo) FinishDelivery(
	ctx context.Context,
	id string,
	status models.DeliveryStatus,
	failure string,
) (models.Delivery, error) {
	if err := r.ready(); err != nil {
		return models.Delivery{}, err
	}

	row, err := r.q.FinishNotificationDelivery(ctx, notificationdb.FinishNotificationDeliveryParams{
		ID:     id,
		Status: status.String(),
		Error:  failure,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Delivery{}, deliveryNotFound(id)
		}
		return models.Delivery{}, classify(err, "could not update the notification log record: %s", id)
	}
	return toDelivery(row), nil
}

// GetDelivery returns the record by its identifier; NotFound when there is
// none.
//
// The reason it stands apart from the admin listing is diagnosis: the last
// state of a delivery record has to be read without passing through the filters
// of the listing.
func (r *Repo) GetDelivery(ctx context.Context, id string) (models.Delivery, error) {
	if err := r.ready(); err != nil {
		return models.Delivery{}, err
	}

	row, err := r.q.GetNotificationDelivery(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Delivery{}, deliveryNotFound(id)
		}
		return models.Delivery{}, classify(err, "could not read the notification log record: %s", id)
	}
	return toDelivery(row), nil
}

// ListDeliveries returns the records filtered and paged.
// The second return value is the count of ALL the rows matching the filter.
//
// The total comes from a SEPARATE query and applies the same filters as the
// listing; it is correct even when the page is out of range and no row is
// returned.
func (r *Repo) ListDeliveries(
	ctx context.Context,
	filter models.DeliveryFilter,
) ([]models.Delivery, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListNotificationDeliveries(ctx, notificationdb.ListNotificationDeliveriesParams{
		Reference: filter.Reference,
		Status:    filter.Status,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, "could not list the notification log")
	}

	total, err := r.q.CountNotificationDeliveries(ctx, notificationdb.CountNotificationDeliveriesParams{
		Reference: filter.Reference,
		Status:    filter.Status,
	})
	if err != nil {
		return nil, 0, classify(err, "could not count the notification log")
	}

	out := make([]models.Delivery, 0, len(rows))
	// The slice is walked BY INDEX: walking it by value would have copied the
	// whole row struct on every iteration.
	for i := range rows {
		out = append(out, toDelivery(rows[i]))
	}
	return out, total, nil
}

// toDelivery converts the generated row into the domain model.
func toDelivery(row notificationdb.NotificationDelivery) models.Delivery {
	return models.Delivery{
		ID:         row.ID,
		Template:   row.Template,
		Channel:    row.Channel,
		Reference:  row.Reference,
		ProviderID: row.ProviderID,
		Status:     models.DeliveryStatus(row.Status),
		Error:      row.Error,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
	}
}

// toTime converts a NOT NULL timestamp into a UTC time.Time.
//
// An invalid (NULL) stamp returns the zero time: on NOT NULL columns this case
// cannot arise, and if it does the zero time is a value that produces no panic
// and stands out in a test.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// sprintf formats the error message exactly once.
//
// On calls without arguments the format is returned UNCHANGED; otherwise a
// percent sign in the message (e.g. "%!d(MISSING)") would corrupt the
// diagnostic text.
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// deliveryNotFound produces the typed error for a record that was not found.
func deliveryNotFound(id string) error {
	return errors.NotFound(CodeDeliveryNotFound, "notification log record not found: %s", id)
}

// classify converts a raw database error into a typed error.
//
// The classification is deliberate: a uniqueness violation is a CONFLICT (409)
// and says the idempotency key was breached; a constraint violation is a client
// error (422); a cancellation is a temporary unavailability (503); everything
// else is a server error and its message IS NOT LEAKED to the client (see
// core/http).
//
// A uniqueness violation DOES NOT LAND HERE in the normal flow — opening the
// record uses ON CONFLICT DO NOTHING and reports the conflict by returning no
// row. It is mapped nonetheless: had it not been mapped, a case where the index
// was breached by hand or by some other path would have shown up as a "server
// error" and been harder to diagnose.
func classify(err error, format string, a ...any) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlstateUniqueViolation:
			if pgErr.ConstraintName == constraintTemplateReferenceUniq {
				return errors.Wrap(err, errors.KindConflict, CodeDeliveryExists,
					"a notification record already exists for this template and reference")
			}
		case sqlstateCheckViolation, sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (constraint: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}
