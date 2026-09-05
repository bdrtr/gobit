// Package jobpg is the PostgreSQL half of the job runner.
//
// It supplies both mechanisms the runner composes: the ROW that elects an
// occurrence, and the session-scoped advisory LOCK that answers whether anybody
// is running the job right now. The reasoning for using both lives in
// internal/core/job's package documentation; what is here is why each is done
// the way it is.
package jobpg

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/job"
)

// MigrationOwner is the name of the core component that owns this schema.
//
// It sits beside "workflow" in the core's migration list; each owner keeps its
// own version ledger (<owner>_schema_migrations), so rolling one back never
// touches another.
const MigrationOwner = "job"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Error codes.
const (
	codeStoreFailed = "job_store_failed"
	codeLockFailed  = "job_lock_failed"
)

// Migrations returns this schema's migration files with the directory prefix
// stripped, the way db.Migrate expects them.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		// The directory name is a compile-time constant and the embed directive
		// has already verified the files exist; returning nil silently would
		// mean the runner coming up without its table.
		panic("jobpg: could not open the embedded migrations directory: " + err.Error())
	}

	return sub
}

// Store is the durable half of the runner.
type Store struct {
	pool *pgxpool.Pool
}

// The core contract is satisfied at compile time.
var _ job.Store = (*Store)(nil)

// New builds a store over the core pool.
func New(pool *db.Pool) *Store { return &Store{pool: pool.Pool()} }

// Claim inserts the occurrence row, and the insert IS the election.
//
// The first instance to reach the primary key wins; every other one gets a
// conflict and is told to do nothing. There is no leader, no coordinator and no
// vote — the uniqueness constraint decides, which means it decides correctly
// even when two instances are partitioned from each other but not from the
// database.
//
// The row is written BEFORE the work, not after. That is what makes a second
// concurrent run impossible rather than merely unlikely, and it is why the job
// contract requires the work to be safe to repeat: a process that dies after
// claiming leaves an occurrence marked taken and never finished, which the
// listing shows as unfinished rather than as never-run.
func (s *Store) Claim(ctx context.Context, name string, due time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO job_run (name, due) VALUES ($1, $2)
		ON CONFLICT (name, due) DO NOTHING`, name, due.UTC())
	if err != nil {
		return false, wrap(err, "the job occurrence could not be claimed")
	}

	return tag.RowsAffected() == 1, nil
}

// Finish records how a run ended.
//
// It writes only to the row this process claimed and only while that row is
// still unfinished, so a late write from a process everybody has given up on
// cannot overwrite a newer outcome.
func (s *Store) Finish(ctx context.Context, name string, due time.Time, outcome job.Outcome) error {
	failure := ""
	if outcome.Err != nil {
		failure = outcome.Err.Error()
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE job_run SET ended_at = now(), failure = $3, detail = $4
		WHERE name = $1 AND due = $2 AND ended_at IS NULL`,
		name, due.UTC(), failure, outcome.Detail)
	if err != nil {
		return wrap(err, "the job outcome could not be recorded")
	}

	return nil
}

// Last returns the most recent run of each named job.
//
// DISTINCT ON rather than a window function: the index is (name, due DESC), so
// this is one ordered scan per job and the planner needs no sort.
func (s *Store) Last(ctx context.Context, names []string) (map[string]job.Run, error) {
	if len(names) == 0 {
		return map[string]job.Run{}, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (name) name, due, started_at, ended_at, failure, detail
		FROM job_run
		WHERE name = ANY($1)
		ORDER BY name, due DESC`, names)
	if err != nil {
		return nil, wrap(err, "the job history could not be read")
	}
	defer rows.Close()

	out := make(map[string]job.Run, len(names))
	for rows.Next() {
		var r job.Run
		var ended *time.Time
		if err := rows.Scan(&r.Name, &r.Due, &r.StartedAt, &ended, &r.Failure, &r.Detail); err != nil {
			return nil, wrap(err, "a job history row could not be read")
		}
		if ended != nil {
			r.EndedAt = *ended
		}
		out[r.Name] = r
	}

	return out, rows.Err()
}

// WithLock runs fn while holding the job's advisory lock.
//
// # Session scoped, and taken with the try form
//
// SESSION, because a job is many statements and usually no transaction:
// pg_advisory_xact_lock outside a transaction is released immediately and
// protects nothing. The lock therefore lives on ONE connection, which is
// acquired from the pool for the whole run and released at the end — that
// connection IS the lock's lifetime.
//
// TRY, because the blocking form waits with no bound. A blocked runner would
// hold a pooled connection while waiting for a job that may be stuck, and the
// wait would not be visible anywhere; failing to take the lock is a fact the
// caller can act on immediately.
//
// # What releases it when a process dies
//
// PostgreSQL releases a session lock when the backend exits, and it does so
// without a timer. That is the reason the lock is the liveness half rather than
// a lease: there is no duration to choose, so there is no bargain between "long
// enough for the longest run" and "how long a dead run stays invisible".
func (s *Store) WithLock(
	ctx context.Context, key int64, fn func(context.Context) error,
) (bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, wrap(err, "a connection could not be acquired for the job lock")
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		return false, wrap(err, "the job lock could not be taken")
	}
	if !locked {
		return false, nil
	}

	defer func() {
		// The unlock runs on a context DETACHED from the caller's: at shutdown
		// the caller's context is already canceled, and an unlock that were
		// skipped would leave the lock held until the connection is closed.
		// That is survivable — the backend eventually exits — but it would keep
		// the job unrunnable for as long as the pooled connection lives.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key)
	}()

	return true, fn(ctx)
}

// wrap classifies a driver error.
func wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeStoreFailed, "%s", message)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return coreerrors.Wrap(err, coreerrors.KindNotFound, codeStoreFailed, "%s", message)
	}

	return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeLockFailed, "%s", message)
}
