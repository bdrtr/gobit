package db

import (
	"context"
	"database/sql"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for database/sql

	"github.com/bdrtr/gobit/internal/core/errors"
)

const (
	// migrationsTableSuffix is the suffix of the per-module version table.
	migrationsTableSuffix = "_schema_migrations"
	// sourceName is the name the iofs driver is registered under with
	// golang-migrate.
	sourceName = "iofs"
	// databaseDriverName is the name of golang-migrate's postgres driver.
	databaseDriverName = "postgres"
	// sqlDriverName is the name pgx/stdlib registers with database/sql.
	sqlDriverName = "pgx"
	// cancelGracePeriod is the time allowed for the running work to actually
	// stop after ctx was canceled and the connection closed. When it runs out
	// the caller still gets an error, but that the goroutine was abandoned is
	// reported EXPLICITLY.
	cancelGracePeriod = 5 * time.Second
)

// ownerPattern defines the valid module names. Because owner turns directly
// into an SQL table name, free text is not allowed; that makes injection
// through the table name structurally impossible.
var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// supportedSchemes are the DSN schemes the migration path accepts.
var supportedSchemes = []string{"postgres", "postgresql"}

// MigrationsTable returns the name of the version table belonging to the owner
// module.
//
// owner is validated HERE and, when invalid, an empty name is returned together
// with a KindInvalid error. Table names cannot be parameterized in SQL, so the
// name is necessarily produced by string concatenation; keeping the validation
// INSIDE the function makes it structurally impossible for an unvalidated
// module name from outside to become a table name.
//
// Every module uses ITS OWN table. Had a shared schema_migrations table been
// used, one module's migration would rewrite another's version ledger and the
// modules would break each other; a separate table is the database-level
// counterpart of the module isolation in plan Sections 2.1/2.3.
func MigrationsTable(owner string) (string, error) {
	if err := validateOwner(owner); err != nil {
		return "", err
	}
	return owner + migrationsTableSuffix, nil
}

// validateScheme checks that the DSN carries a supported PostgreSQL scheme.
//
// database/sql is lazy: sql.Open does not connect, so an unsupported scheme
// would surface only on the first connection attempt and under a misleading
// class such as "unreachable". The scheme is checked here, before connecting.
func validateScheme(databaseURL string) error {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return errors.Wrap(err, errors.KindInvalid, "db_dsn_invalid",
			"the DSN could not be parsed (target: %s)", Redact(databaseURL))
	}
	if !slices.Contains(supportedSchemes, u.Scheme) {
		return errors.Invalid("db_dsn_unsupported",
			"unsupported DSN scheme %q (expected: %s)", u.Scheme, strings.Join(supportedSchemes, ", "))
	}
	return nil
}

// validateOwner checks that the module name can safely become a table name.
func validateOwner(owner string) error {
	if !ownerPattern.MatchString(owner) {
		return errors.Invalid("db_migration_owner_invalid",
			"invalid module name %q (expected pattern: %s)", owner, ownerPattern.String())
	}
	return nil
}

// Migrate applies the owner module's pending migrations.
//
// With no migrations left to apply it returns no error. When ctx is canceled or
// runs out, the running work is STOPPED (see [session.run]) and a
// db_migration_canceled error of class KindUnavailable is returned; in that
// case the schema must be assumed partially applied and checked with
// [Version].
func Migrate(ctx context.Context, databaseURL string, src fs.FS, owner string) error {
	return runMigration(ctx, databaseURL, src, owner, "up", func(m *migrate.Migrate) error {
		return m.Up()
	})
}

// MigrateDown rolls the owner module's migrations back.
//
// With steps zero or negative ALL migrations are rolled back; when positive,
// only that many steps. With no migrations left to roll back it returns no
// error — that is the normal outcome of running a rollback in an environment
// that was never migrated.
func MigrateDown(ctx context.Context, databaseURL string, src fs.FS, owner string, steps int) error {
	return runMigration(ctx, databaseURL, src, owner, "down", func(m *migrate.Migrate) error {
		if steps <= 0 {
			return m.Down()
		}
		return asNoChange(m.Steps(-steps))
	})
}

// asNoChange turns the driver errors meaning "there is nothing to do" into
// ErrNoChange. On the step-counted rollback path golang-migrate returns
// os.ErrNotExist when no migration was ever applied and ErrShortLimit when more
// steps were asked for than exist; neither is a failure.
func asNoChange(err error) error {
	var short migrate.ErrShortLimit
	if errors.Is(err, os.ErrNotExist) || errors.As(err, &short) {
		return migrate.ErrNoChange
	}
	return err
}

// Version returns the owner module's current migration version in the database.
//
// With no migration ever applied it returns (0, false, nil). A true dirty means
// there is a half-finished migration; automatic progress is then impossible and
// manual intervention (golang-migrate force) is required.
//
// Note: the golang-migrate driver creates the version table when it is missing,
// so this call is not entirely free of side effects.
func Version(ctx context.Context, databaseURL, owner string) (version uint, dirty bool, err error) {
	if err = ctx.Err(); err != nil {
		return 0, false, errors.Wrap(err, errors.KindUnavailable, "db_migration_canceled",
			"the %s module's migration version was canceled before it could be read", owner)
	}

	s, err := openSession(ctx, databaseURL, nil, owner)
	if err != nil {
		return 0, false, err
	}
	defer s.close()

	var (
		current int
		isDirty bool
	)
	runErr := s.run(ctx, "version", func(*migrate.Migrate) error {
		var verErr error
		current, isDirty, verErr = s.driver.Version()
		return verErr
	})
	if runErr != nil {
		return 0, false, runErr
	}

	// database.NilVersion (-1) means "no migration was ever applied".
	if current < 0 {
		return 0, isDirty, nil
	}
	return uint(current), isDirty, nil
}

// runMigration validates the inputs, opens the session and runs the operation
// within the bounds of ctx. action is only the readable action name appearing
// in the error message.
func runMigration(
	ctx context.Context,
	databaseURL string,
	src fs.FS,
	owner, action string,
	run func(*migrate.Migrate) error,
) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "db_migration_canceled",
			"the %s module's migration (%s) was canceled before it started", owner, action)
	}
	if src == nil {
		return errors.Invalid("db_migration_source_missing",
			"no migration source was given for the %s module", owner)
	}

	s, err := openSession(ctx, databaseURL, src, owner)
	if err != nil {
		return err
	}
	defer s.close()

	return s.run(ctx, action, run)
}

// session is a migration session bound to ctx, with a SINGLE connection.
//
// A single connection is mandatory: golang-migrate's postgres driver has to use
// the same connection when it takes and releases the advisory lock.
type session struct {
	db      *sql.DB
	conn    *sql.Conn
	driver  database.Driver
	source  source.Driver
	migrate *migrate.Migrate
	owner   string
}

// openSession sets up the connection and the drivers the migration needs.
// With src nil no migrate instance is built; that is enough for callers who
// only want to read the version through the driver (see [Version]).
func openSession(ctx context.Context, databaseURL string, src fs.FS, owner string) (*session, error) {
	table, err := MigrationsTable(owner)
	if err != nil {
		return nil, err
	}

	if err := validateScheme(databaseURL); err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open(sqlDriverName, databaseURL)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, "db_migration_dsn_invalid",
			"the migration DSN for the %s module could not be parsed (target: %s)", owner, Redact(databaseURL))
	}
	sqlDB.SetMaxOpenConns(1)

	s := &session{db: sqlDB, owner: owner}

	// Conn(ctx) binds establishing the connection to ctx; against an
	// unreachable server the call returns at the ctx bound rather than waiting
	// for the operating system's TCP timeout.
	s.conn, err = sqlDB.Conn(ctx)
	if err != nil {
		s.close()
		// When the failure is caused by ctx running out, report it as a
		// cancellation; the caller must be able to tell "the server is
		// unreachable" from "my budget ran out".
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errors.Wrap(ctxErr, errors.KindUnavailable, "db_migration_canceled",
				"establishing the migration connection for the %s module was canceled (target: %s)",
				owner, Redact(databaseURL))
		}
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_migration_connect_failed",
			"the migration connection for the %s module could not be established (target: %s)", owner, Redact(databaseURL))
	}

	cfg := &postgres.Config{MigrationsTable: table}
	if deadline, ok := ctx.Deadline(); ok {
		// A defensive layer: it stops a single statement from running forever
		// in a case the connection-closing path missed.
		if remaining := time.Until(deadline); remaining > 0 {
			cfg.StatementTimeout = remaining
		}
	}

	s.driver, err = postgres.WithConnection(ctx, s.conn, cfg)
	if err != nil {
		s.close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errors.Wrap(ctxErr, errors.KindUnavailable, "db_migration_canceled",
				"building the migration driver for the %s module was canceled (target: %s)",
				owner, Redact(databaseURL))
		}
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_migration_connect_failed",
			"the migration driver for the %s module could not be built (target: %s)", owner, Redact(databaseURL))
	}

	if src == nil {
		return s, nil
	}

	s.source, err = iofs.New(src, ".")
	if err != nil {
		s.close()
		return nil, errors.Wrap(err, errors.KindInvalid, "db_migration_source_invalid",
			"the %s module's migration source could not be read", owner)
	}

	s.migrate, err = migrate.NewWithInstance(sourceName, s.source, databaseDriverName, s.driver)
	if err != nil {
		s.close()
		return nil, errors.Wrap(err, errors.KindInternal, "db_migration_init_failed",
			"the migration instance for the %s module could not be built", owner)
	}
	return s, nil
}

// close releases every resource of the session. It may be called more than
// once.
func (s *session) close() {
	// migrate.Close closes the source and database drivers; when the driver is
	// already closed the error returned is meaningless, so it is swallowed.
	if s.migrate != nil {
		_, _ = s.migrate.Close()
	} else if s.driver != nil {
		_ = s.driver.Close()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if s.db != nil {
		_ = s.db.Close()
	}
}

// run executes the operation in a separate goroutine and REALLY stops the work
// when ctx is canceled.
//
// After setup, golang-migrate's postgres driver uses context.Background() for
// every query (and Lock waits indefinitely on pg_advisory_lock). Cancellation is
// therefore applied from two sides:
//
//  1. GracefulStop stops the NEXT migration from STARTING,
//  2. closing the connection cuts off the IN-FLIGHT statement.
//
// The work is then waited on until it really ends; the goroutine is not
// abandoned. When it does not end within cancelGracePeriod, that is stated
// explicitly in the error message.
func (s *session) run(ctx context.Context, action string, fn func(*migrate.Migrate) error) error {
	done := make(chan error, 1)
	go func() { done <- fn(s.migrate) }()

	select {
	case err := <-done:
		return s.classify(err, action)

	case <-ctx.Done():
		// The work may have finished just before ctx ran out; select picks
		// randomly between two ready cases. It is checked first so a success is
		// not mistaken for a cancellation.
		select {
		case err := <-done:
			return s.classify(err, action)
		default:
		}

		// On the Version path no migrate instance is built; GracefulStop only
		// means something while a real migration is running.
		if s.migrate != nil {
			select {
			case s.migrate.GracefulStop <- true:
			default: // a full channel means the signal was already sent
			}
		}
		if s.conn != nil {
			_ = s.conn.Close()
		}

		select {
		case <-done:
			return errors.Wrap(ctx.Err(), errors.KindUnavailable, "db_migration_canceled",
				"the %s module's migration (%s) was cut short", s.owner, action)
		case <-time.After(cancelGracePeriod):
			return errors.Wrap(ctx.Err(), errors.KindUnavailable, "db_migration_canceled",
				"the %s module's migration (%s) was canceled but did not stop within %s",
				s.owner, action, cancelGracePeriod)
		}
	}
}

// classify turns the raw error returned by golang-migrate into a typed error.
func (s *session) classify(err error, action string) error {
	// ErrNoChange is not an error: having no migration left to apply or roll
	// back is the migration runner's normal outcome.
	if err == nil || errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return errors.Wrap(err, errors.KindInternal, "db_migration_failed",
		"the %s module's migration (%s) could not be applied", s.owner, action)
}
