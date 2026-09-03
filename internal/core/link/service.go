package link

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// Keys used in log fields and error details.
const (
	keyLink   = "link"
	keyFromID = "from_id"
	keyToID   = "to_id"
)

// uniqueViolation is PostgreSQL's SQLSTATE code for a uniqueness violation.
// (Written out as a constant rather than adding a github.com/jackc/pgerrcode
// dependency.)
const uniqueViolation = "23505"

// defineLockKey is the key of the advisory lock Define takes; it is the ASCII
// encoding of the string "link_def". Deriving the constant from a readable
// source makes it harder for another subsystem to pick the same key by
// accident.
const defineLockKey int64 = 0x6C696E6B5F646566

// lockSQL takes the advisory lock that serializes declarations. The lock is
// released by itself at the end of the TRANSACTION; there is no separate
// unlock call (and no risk of forgetting it).
const lockSQL = `SELECT pg_advisory_xact_lock($1)`

// relkindSQL reads the kind (pg_class.relkind) of a relation in the current
// schema. Because tables and indexes share the same namespace, checking the
// kind is mandatory.
const relkindSQL = `SELECT relkind::text FROM pg_class
WHERE relname = $1 AND relnamespace = current_schema()::regnamespace`

// createDefinitionsTableSQL creates the durable ledger.
//
// This is not a "link table"; it holds the link definitions themselves and,
// again, gives no FK to any module table.
const createDefinitionsTableSQL = `CREATE TABLE IF NOT EXISTS ` + definitionsTable + ` (
	name        TEXT PRIMARY KEY,
	from_module TEXT NOT NULL,
	from_field  TEXT NOT NULL,
	to_module   TEXT NOT NULL,
	to_field    TEXT NOT NULL,
	cardinality TEXT NOT NULL,
	created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// upsertDefinitionSQL writes the definition into the ledger and returns the
// version that is IN THE LEDGER.
//
// "DO UPDATE SET name = <table>.name" is a deliberate no-op: had DO NOTHING
// been chosen, RETURNING would give back no row on a conflict and reading the
// stored definition would need a second round trip. In this form the insert
// and the comparison happen in one statement, under one lock.
const upsertDefinitionSQL = `INSERT INTO ` + definitionsTable + `
	(name, from_module, from_field, to_module, to_field, cardinality)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (name) DO UPDATE SET name = ` + definitionsTable + `.name
RETURNING from_module, from_field, to_module, to_field, cardinality`

// storedDefinition is the raw form of a row in the durable ledger.
//
// Cardinality lives on disk as TEXT (see [Cardinality.String]); that is why
// the row is not converted into a typed LinkDefinition and the comparison runs
// over text. This keeps the comparison meaningful even when the disk holds an
// unrecognized cardinality, and lets the conflict message show the stored
// value as it is.
type storedDefinition struct {
	fromModule  string
	fromField   string
	toModule    string
	toField     string
	cardinality string
}

// matches reports whether the ledger row is the same as the given definition.
func (s storedDefinition) matches(def LinkDefinition) bool {
	return s.fromModule == def.From.Module &&
		s.fromField == def.From.Field &&
		s.toModule == def.To.Module &&
		s.toField == def.To.Field &&
		s.cardinality == def.Cardinality.String()
}

// String writes the ledger row in the same form as LinkDefinition.String.
func (s storedDefinition) String() string {
	return "(" + s.fromModule + "." + s.fromField +
		" -> " + s.toModule + "." + s.toField + ", " + s.cardinality + ")"
}

// service is the PostgreSQL implementation of LinkService.
type service struct {
	pool *db.Pool
	log  *slog.Logger
	defs *definitions
}

// New builds a LinkService working over the given connection pool.
//
// If log is nil nothing is logged. If pool is nil (or closed) the service is
// still constructed, but every call touching the database returns
// errors.Unavailable; a startup-order mistake is therefore reported with a
// typed error rather than a panic.
func New(pool *db.Pool, log *slog.Logger) LinkService {
	return newService(pool, log)
}

// newService builds the concrete service; it is the shared path of New and of
// the in-package tests.
func newService(pool *db.Pool, log *slog.Logger) *service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &service{pool: pool, log: log, defs: newDefinitions()}
}

// Define declares a link definition and creates its table (if absent).
// For the contract and the idempotency rules see [LinkService].
func (s *service) Define(ctx context.Context, def LinkDefinition) error {
	if err := def.Validate(); err != nil {
		return err
	}

	// Fast path: if the same definition was already declared in this process
	// the database is never touched. At startup dozens of modules redeclare
	// the same definitions.
	if existing, ok := s.defs.lookup(def.Name); ok {
		if existing.def != def {
			return conflictWithExisting(existing.def, def)
		}
		return nil
	}

	lt, err := newLinkTable(def)
	if err != nil {
		return err
	}
	if err := s.declare(ctx, lt); err != nil {
		return err
	}

	s.defs.put(lt)
	s.log.InfoContext(ctx, "link declared",
		slog.String(keyLink, def.Name),
		slog.String("table", lt.table),
		slog.String("cardinality", def.Cardinality.String()),
	)
	return nil
}

// declare writes the definition into the durable ledger and creates the link
// table.
//
// All of it runs in a SINGLE transaction under an advisory lock. Two reasons:
//
//  1. If two processes starting at the same time run the same DDL, even
//     "CREATE TABLE IF NOT EXISTS" can race (PostgreSQL raises a
//     catalog-level uniqueness violation). The lock puts every link
//     declaration into a single queue.
//  2. It guarantees that the ledger row and the table are created together;
//     DDL is transactional in PostgreSQL, so a half-finished declaration is
//     rolled back.
func (s *service) declare(ctx context.Context, lt *linkTable) error {
	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, codeDefineFailed, "could not begin the transaction for link %q", lt.def.Name)
	}
	// A Rollback after Commit returns ErrTxClosed; it is meaningless, so it is
	// swallowed.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, lockSQL, defineLockKey); err != nil {
		return wrapDB(err, codeDefineFailed, "could not take the lock for link %q", lt.def.Name)
	}
	if _, err := tx.Exec(ctx, createDefinitionsTableSQL); err != nil {
		return wrapDB(err, codeDefineFailed, "could not create the %s table", definitionsTable)
	}

	var stored storedDefinition
	err = tx.QueryRow(ctx, upsertDefinitionSQL,
		lt.def.Name, lt.def.From.Module, lt.def.From.Field,
		lt.def.To.Module, lt.def.To.Field, lt.def.Cardinality.String(),
	).Scan(&stored.fromModule, &stored.fromField, &stored.toModule, &stored.toField, &stored.cardinality)
	if err != nil {
		return wrapDB(err, codeDefineFailed, "could not write the definition of link %q to the ledger", lt.def.Name)
	}
	if !stored.matches(lt.def) {
		return conflictWithStored(stored, lt.def)
	}

	for _, stmt := range lt.ddl() {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return wrapDB(err, codeDefineFailed, "could not create the table of link %q", lt.def.Name)
		}
	}

	if err := verifySchema(ctx, tx, lt); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, codeDefineFailed, "could not persist the definition of link %q", lt.def.Name)
	}
	return nil
}

// Create links fromID with toID.
// For the idempotency and cardinality rules see [LinkService].
func (s *service) Create(ctx context.Context, name, fromID, toID string) error {
	lt, err := s.linkFor(name)
	if err != nil {
		return err
	}
	if err := validateIDPair(fromID, toID); err != nil {
		return err
	}
	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tag, err := pool.Exec(ctx, lt.insert, fromID, toID)
	if err != nil {
		return lt.writeError(err, fromID, toID)
	}
	if tag.RowsAffected() == 0 {
		// The pair is already linked: an idempotent no-op. Rather than staying
		// silent it is logged, so that unexpected repeats (a saga stuck in a
		// loop, say) become visible.
		s.log.DebugContext(ctx, "link already exists",
			slog.String(keyLink, name), slog.String(keyFromID, fromID), slog.String(keyToID, toID))
	}
	return nil
}

// Delete removes the link between fromID and toID.
// If the link is absent the call is a no-op; for the reasoning see
// [LinkService].
func (s *service) Delete(ctx context.Context, name, fromID, toID string) error {
	lt, err := s.linkFor(name)
	if err != nil {
		return err
	}
	if err := validateIDPair(fromID, toID); err != nil {
		return err
	}
	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tag, err := pool.Exec(ctx, lt.remove, fromID, toID)
	if err != nil {
		return wrapDB(err, codeQueryFailed, "could not delete link %q", name)
	}
	if tag.RowsAffected() == 0 {
		s.log.DebugContext(ctx, "no link to delete",
			slog.String(keyLink, name), slog.String(keyFromID, fromID), slog.String(keyToID, toID))
	}
	return nil
}

// List returns the toIDs bound to fromID in ascending order.
func (s *service) List(ctx context.Context, name, fromID string) ([]string, error) {
	lt, err := s.linkFor(name)
	if err != nil {
		return nil, err
	}
	if err := validateID(fromID, "fromID"); err != nil {
		return nil, err
	}
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, lt.list, fromID)
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "could not read the records of link %q", name)
	}
	// CollectRows closes the rows and folds rows.Err() into the result too; if
	// there is no row at all it returns an EMPTY slice (not nil), so that JSON
	// carries "[]" rather than "null".
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "could not read the records of link %q", name)
	}
	return ids, nil
}

// ListMany returns the links of several fromIDs in a single query.
// For the batching reasoning see [LinkService] and ADR 0004.
func (s *service) ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error) {
	lt, err := s.linkFor(name)
	if err != nil {
		return nil, err
	}
	for _, id := range fromIDs {
		if err := validateID(id, "fromID"); err != nil {
			return nil, err
		}
	}
	// No need to open a query for an empty set; the result is empty anyway.
	if len(fromIDs) == 0 {
		return map[string][]string{}, nil
	}
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, lt.listMany, fromIDs)
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "could not read the records of link %q", name)
	}

	var fromID, toID string
	result := make(map[string][]string, len(fromIDs))
	// ForEachRow closes the rows and returns rows.Err().
	if _, err := pgx.ForEachRow(rows, []any{&fromID, &toID}, func() error {
		result[fromID] = append(result[fromID], toID)
		return nil
	}); err != nil {
		return nil, wrapDB(err, codeQueryFailed, "could not read the records of link %q", name)
	}
	return result, nil
}

// ListManyByTo resolves the reverse direction in bulk: for each of the given
// toIDs it returns the bound fromIDs. A toID with no link is absent from the
// result.
//
// It follows the same pattern as [service.ListMany]; the only difference is
// the direction of the query. The result map is keyed by toID, and the value
// is the fromIDs bound to that toID.
func (s *service) ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error) {
	lt, err := s.linkFor(name)
	if err != nil {
		return nil, err
	}
	for _, id := range toIDs {
		if err := validateID(id, "toID"); err != nil {
			return nil, err
		}
	}
	// No need to open a query for an empty set; the result is empty anyway.
	if len(toIDs) == 0 {
		return map[string][]string{}, nil
	}
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, lt.listManyByTo, toIDs)
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "could not read the reverse-direction records of link %q", name)
	}

	var toID, fromID string
	result := make(map[string][]string, len(toIDs))
	// ForEachRow closes the rows and returns rows.Err().
	if _, err := pgx.ForEachRow(rows, []any{&toID, &fromID}, func() error {
		result[toID] = append(result[toID], fromID)
		return nil
	}); err != nil {
		return nil, wrapDB(err, codeQueryFailed, "could not read the reverse-direction records of link %q", name)
	}
	return result, nil
}

// Definition returns the definition of the named link.
func (s *service) Definition(ctx context.Context, name string) (LinkDefinition, error) {
	// Even a read served from memory must produce no result under a canceled
	// context; if the caller's budget is spent the flow stops here too.
	if err := ctx.Err(); err != nil {
		return LinkDefinition{}, errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			"the definition of link %q was canceled before it was read", name)
	}
	lt, err := s.linkFor(name)
	if err != nil {
		return LinkDefinition{}, err
	}
	return lt.def, nil
}

// linkFor returns the runtime information of the named link; if it is
// undeclared it produces errors.NotFound.
//
// This gate does two jobs at once: diagnostics (which name was looked up, what
// is declared) and security — the table name reaching SQL can come ONLY from a
// validated definition.
func (s *service) linkFor(name string) (*linkTable, error) {
	if lt, ok := s.defs.lookup(name); ok {
		return lt, nil
	}
	return nil, errors.NotFound(codeNotDefined,
		"no link is declared under the name %q; declared links: %s", name, joinNames(s.defs.names())).
		WithDetails(map[string]any{keyLink: name})
}

// rawPool returns the raw pgx pool; if the pool was never built it produces a
// typed error.
func (s *service) rawPool() (*pgxpool.Pool, error) {
	// db.Pool.Pool() is safe against a nil receiver; a nil pool returns nil.
	pool := s.pool.Pool()
	if pool == nil {
		return nil, errors.Unavailable(codeUnavailable,
			"no database pool was built for the link service")
	}
	return pool, nil
}

// writeError turns the raw driver error on the write path into a typed error.
//
// A cardinality violation is read here: on a uniqueness violation PostgreSQL
// reports the name of the VIOLATED index, from which we can produce a message
// saying which end is taken. A violation of the (from_id, to_id) primary key
// never reaches here; the INSERT's ON CONFLICT swallows it and turns it into a
// no-op.
func (lt *linkTable) writeError(err error, fromID, toID string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		details := map[string]any{
			keyLink:       lt.def.Name,
			"cardinality": lt.def.Cardinality.String(),
			keyFromID:     fromID,
			keyToID:       toID,
		}
		switch pgErr.ConstraintName {
		case lt.fromIndex:
			return errors.Conflict(codeCardinalityViolation,
				"link %s has cardinality %s: record %q is already bound to another target",
				lt.def.Name, lt.def.Cardinality, fromID).WithDetails(details)
		case lt.toIndex:
			return errors.Conflict(codeCardinalityViolation,
				"link %s has cardinality %s: target %q is already bound to another record",
				lt.def.Name, lt.def.Cardinality, toID).WithDetails(details)
		default:
			return errors.Wrap(err, errors.KindConflict, codeCardinalityViolation,
				"link %s could not be created: constraint %s was violated",
				lt.def.Name, pgErr.ConstraintName).WithDetails(details)
		}
	}
	return wrapDB(err, codeQueryFailed, "link %q could not be created", lt.def.Name)
}

// conflictWithExisting reports a conflict with the definition in the
// in-process registry.
func conflictWithExisting(existing, incoming LinkDefinition) error {
	return errors.Conflict(codeDefinitionConflict,
		"link %q was already declared differently in this process: stored %s, incoming %s",
		incoming.Name, existing, incoming).
		WithDetails(map[string]any{keyLink: incoming.Name, "stored": existing.String()})
}

// conflictWithStored reports a conflict with the definition in the durable
// ledger.
//
// This path catches a definition that was declared differently in a previous
// RELEASE. Were it accepted silently, a change narrowing the cardinality, say,
// would try to apply the new constraint without seeing the extra links that
// already exist, and startup would fall over with an obscure index error.
func conflictWithStored(stored storedDefinition, incoming LinkDefinition) error {
	return errors.Conflict(codeDefinitionConflict,
		"link %q is declared differently in the %s table: stored %s, incoming %s",
		incoming.Name, definitionsTable, stored, incoming).
		WithDetails(map[string]any{keyLink: incoming.Name, "stored": stored.String()})
}

// validateIDPair validates the two ends of a link together.
func validateIDPair(fromID, toID string) error {
	if err := validateID(fromID, "fromID"); err != nil {
		return err
	}
	return validateID(toID, "toID")
}

// wrapDB turns the raw driver error into a typed error.
//
// A canceled context is reported separately with KindUnavailable: the caller
// must be able to tell "the database is broken" from "my budget ran out" (the
// same distinction as internal/core/db).
func wrapDB(err error, code, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			format+" (the context was canceled)", a...)
	default:
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}
}

// joinNames writes a name list readably for a message.
func joinNames(names []string) string {
	if len(names) == 0 {
		return "(none declared)"
	}
	return strings.Join(names, ", ")
}

// verifySchema checks that the DDL really produced the intended schema.
//
// "CREATE ... IF NOT EXISTS" statements raise a NOTICE and skip, rather than
// erroring, when a relation of ANOTHER KIND already carries that name. Since
// tables and indexes share the same namespace in PostgreSQL, that means the
// cardinality constraint is silently never created (or the link table never
// comes into being). Name validation prevents the known collision patterns;
// this check closes every remaining class and catches the breakage BEFORE the
// data is corrupted.
//
// It runs inside the transaction: if it fails, the deferred Rollback undoes
// the definition as well.
func verifySchema(ctx context.Context, tx pgx.Tx, lt *linkTable) error {
	var relkind string
	err := tx.QueryRow(ctx, relkindSQL, lt.table).Scan(&relkind)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.Internal(codeDefineFailed,
			"the table (%s) of link %q could not be created; a relation with the same name may have made the DDL skip",
			lt.table, lt.def.Name)
	}
	if err != nil {
		return wrapDB(err, codeDefineFailed, "the table of link %q could not be verified", lt.def.Name)
	}
	if relkind != relkindTable {
		return errors.Internal(codeDefineFailed,
			"%s is not a table (relkind=%q); the link name %q collides with an existing relation",
			lt.table, relkind, lt.def.Name)
	}

	for _, index := range lt.requiredIndexes() {
		var kind string
		err := tx.QueryRow(ctx, relkindSQL, index).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind != relkindIndex) {
			return errors.Internal(codeDefineFailed,
				"the %s index enforcing the %s cardinality of link %q could not be created; we do not continue without the constraint",
				index, lt.def.Cardinality, lt.def.Name)
		}
		if err != nil {
			return wrapDB(err, codeDefineFailed, "the index of link %q could not be verified", lt.def.Name)
		}
	}
	return nil
}
