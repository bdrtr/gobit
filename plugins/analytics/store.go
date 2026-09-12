package analytics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// codeQueryFailed reports a failed statement.
const codeQueryFailed = "analytics_query_failed"

// eventStore is the funnel's persistence surface.
//
// It is an interface so the handlers can be tested without a server, and it is
// declared here — the consumer's side — for the reason every narrow interface in
// this tree is (ADR 0001).
type eventStore interface {
	// Record writes one event, ignoring one it already holds.
	Record(ctx context.Context, row eventRow) error
	// Funnel returns the daily counts inside the window, oldest first.
	Funnel(ctx context.Context, from, to time.Time) ([]funnelRow, error)
}

// eventRow is one recorded event.
type eventRow struct {
	// ID is the event's own identifier, derived by the publisher.
	ID string
	// Topic is which of the three moments this is.
	Topic string
	// OccurredAt is the moment the publisher stamped.
	OccurredAt time.Time
	// RegionID is the shop's dimension.
	RegionID string
}

// funnelRow is one day's counts in one region.
type funnelRow struct {
	Day            time.Time
	RegionID       string
	CartsCreated   int64
	CartsCompleted int64
	OrdersPlaced   int64
}

// pgStore is the PostgreSQL-backed event store.
type pgStore struct {
	pool *pgxpool.Pool
}

// That the store satisfies the surface the module expects is fixed at compile
// time.
var _ eventStore = (*pgStore)(nil)

// newEventStore builds the store over the pool.
func newEventStore(pool *pgxpool.Pool) eventStore { return &pgStore{pool: pool} }

// recordSQL writes the event and DROPS a redelivery.
//
// `ON CONFLICT (id) DO NOTHING` is the whole idempotency argument: the bus
// delivers at least once, the publisher derives the id from the record, so the
// second arrival of one event is the same key twice. Nothing is compared and no
// counter is adjusted — the second insert simply does not happen.
//
// The day is derived HERE rather than passed in, so the column and the timestamp
// cannot disagree: two places computing one value is two places for it to drift.
const recordSQL = `
    INSERT INTO analytics_events (id, topic, occurred_at, day, region_id)
    VALUES ($1, $2, $3, ($3 AT TIME ZONE 'UTC')::date, $4)
    ON CONFLICT (id) DO NOTHING`

// Record writes one event.
func (s *pgStore) Record(ctx context.Context, row eventRow) error {
	_, err := s.pool.Exec(ctx, recordSQL, row.ID, row.Topic, row.OccurredAt, row.RegionID)
	if err != nil {
		return coreerrors.Internal(codeQueryFailed,
			"the analytics event could not be recorded (%s): %v", row.Topic, err)
	}

	return nil
}

// funnelSQL counts the three topics per day and region.
//
// The counts are conditional sums over ONE pass rather than three queries: the
// three numbers of a row have to come from the same snapshot, or a shop reading
// during a busy minute sees more completions than creations.
//
// The window is closed on the left and OPEN on the right ($1 <= day < $2), which
// is the only form that tiles: two adjacent windows asked for separately add up
// to the wide one, and no row is counted twice at the boundary.
const funnelSQL = `
    SELECT day,
           region_id,
           count(*) FILTER (WHERE topic = 'cart.created')   AS carts_created,
           count(*) FILTER (WHERE topic = 'cart.completed') AS carts_completed,
           count(*) FILTER (WHERE topic = 'order.placed')   AS orders_placed
      FROM analytics_events
     WHERE day >= $1 AND day < $2
     GROUP BY day, region_id
     ORDER BY day, region_id`

// Funnel returns the daily counts inside the window.
func (s *pgStore) Funnel(ctx context.Context, from, to time.Time) ([]funnelRow, error) {
	rows, err := s.pool.Query(ctx, funnelSQL, from, to)
	if err != nil {
		return nil, coreerrors.Internal(codeQueryFailed,
			"the analytics funnel could not be read: %v", err)
	}
	defer rows.Close()

	out := []funnelRow{}
	for rows.Next() {
		var row funnelRow
		if scanErr := rows.Scan(&row.Day, &row.RegionID,
			&row.CartsCreated, &row.CartsCompleted, &row.OrdersPlaced); scanErr != nil {
			return nil, coreerrors.Internal(codeQueryFailed,
				"an analytics funnel row could not be read: %v", scanErr)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, coreerrors.Internal(codeQueryFailed,
			"the analytics funnel could not be read to the end: %v", err)
	}

	return out, nil
}
