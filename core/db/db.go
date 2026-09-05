// Package db provides the PostgreSQL connection pool and the migration runner.
//
// The package has two independent responsibilities:
//
//   - The connection pool (Pool): a thin wrapper over pgxpool. Repositories
//     reach the raw pool through Pool.Pool(); the pool's lifecycle stays in
//     this package.
//   - The migration runner (migrate.go): it runs golang-migrate with a SEPARATE
//     version table per module. That is the database-level counterpart of the
//     module isolation in plan Sections 2.1/2.3.
//
// A rule holds across the package: the DSN never appears raw in any error
// message or log record (plan Section 8, "sensitive data is not logged").
// Every target representation leaving the package goes through Redact.
package db

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
)

// The pool's default settings; DefaultConfig uses them.
const (
	defaultMaxConns        = 10
	defaultMinConns        = 2
	defaultMaxConnLifetime = time.Hour
	defaultMaxConnIdleTime = 30 * time.Minute
	defaultConnectTimeout  = 5 * time.Second
)

// The placeholders used for unknown parts of the redacted representation.
const (
	unknownTarget   = "<unknown-target>"
	unknownDatabase = "<no-database>"
)

// Config holds the settings of the PostgreSQL connection pool.
type Config struct {
	// URL is the connection address in pgx form
	// (e.g. postgres://user:password@host:5432/database?sslmode=disable).
	URL string
	// MaxConns is the maximum number of connections the pool may open.
	MaxConns int32
	// MinConns is the minimum number of connections the pool tries to keep even
	// when idle.
	MinConns int32
	// MaxConnLifetime is a connection's maximum lifetime; when it runs out the
	// connection is closed and replaced. It stops long-lived connections from
	// hiding a server-side resource leak or a load balancer dropping them.
	MaxConnLifetime time.Duration
	// MaxConnIdleTime is the maximum time a connection may stay idle.
	MaxConnIdleTime time.Duration
	// ConnectTimeout is the maximum time allowed for establishing a single
	// connection and for the health check at startup.
	ConnectTimeout time.Duration
}

// DefaultConfig returns a Config filled with sensible defaults for the given
// DSN. The caller overwrites only the fields it wants to change.
func DefaultConfig(dsn string) Config {
	return Config{
		URL:             dsn,
		MaxConns:        defaultMaxConns,
		MinConns:        defaultMinConns,
		MaxConnLifetime: defaultMaxConnLifetime,
		MaxConnIdleTime: defaultMaxConnIdleTime,
		ConnectTimeout:  defaultConnectTimeout,
	}
}

// Validate checks that the Config fields are consistent with each other.
// New calls it automatically; it can also be used for hand-built Configs.
func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return errors.Invalid("db_config_invalid", "the database address (URL) cannot be empty")
	}
	if c.MaxConns < 1 {
		return errors.Invalid("db_config_invalid", "MaxConns must be at least 1, got %d", c.MaxConns)
	}
	if c.MinConns < 0 {
		return errors.Invalid("db_config_invalid", "MinConns cannot be negative, got %d", c.MinConns)
	}
	if c.MinConns > c.MaxConns {
		return errors.Invalid("db_config_invalid",
			"MinConns (%d) cannot be greater than MaxConns (%d)", c.MinConns, c.MaxConns)
	}
	if c.MaxConnLifetime <= 0 {
		return errors.Invalid("db_config_invalid",
			"MaxConnLifetime must be positive, got %s", c.MaxConnLifetime)
	}
	if c.MaxConnIdleTime <= 0 {
		return errors.Invalid("db_config_invalid",
			"MaxConnIdleTime must be positive, got %s", c.MaxConnIdleTime)
	}
	if c.MaxConnIdleTime > c.MaxConnLifetime {
		return errors.Invalid("db_config_invalid",
			"MaxConnIdleTime (%s) cannot be greater than MaxConnLifetime (%s)",
			c.MaxConnIdleTime, c.MaxConnLifetime)
	}
	if c.ConnectTimeout <= 0 {
		return errors.Invalid("db_config_invalid",
			"ConnectTimeout must be positive, got %s", c.ConnectTimeout)
	}
	return nil
}

// Pool is the application's shared PostgreSQL connection pool.
// It is safe for concurrent use; a single instance suffices for the
// application's lifetime.
type Pool struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	// target is the representation of the target that is safe to log
	// (host/database).
	target string
}

// New opens a connection pool with the given settings and verifies that the
// database is really reachable. The pool returned must be closed with Close
// after use. With log nil nothing is logged.
func New(ctx context.Context, cfg Config, log *slog.Logger) (*Pool, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	target := Redact(cfg.URL)

	pgCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// The underlying error text can carry the DSN as it is, so it is NOT
		// WRAPPED; only the redacted target representation is handed out.
		return nil, errors.Invalid("db_dsn_invalid",
			"the database address could not be parsed (target: %s)", target)
	}

	warnAboutDSNPoolSettings(ctx, cfg.URL, log)

	pgCfg.MaxConns = cfg.MaxConns
	pgCfg.MinConns = cfg.MinConns
	pgCfg.MaxConnLifetime = cfg.MaxConnLifetime
	pgCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	pgCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_pool_open_failed",
			"the database pool could not be opened (target: %s)", target)
	}

	// pgxpool connects lazily; without verifying here that the pool really
	// works, the failure stays hidden until the first request.
	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_unreachable",
			"the database is unreachable (target: %s)", target)
	}

	// The case-folding probe runs here, on a pool that has just answered a
	// Ping, so it costs one extra round trip at startup and nothing per
	// request. See casefold.go for what it protects.
	checkCaseFolding(ctx, pool, log)

	log.InfoContext(ctx, "the postgres connection pool is ready",
		slog.String("target", target),
		slog.Int64("max_conns", int64(cfg.MaxConns)),
		slog.Int64("min_conns", int64(cfg.MinConns)),
	)

	return &Pool{pool: pool, log: log, target: target}, nil
}

// Pool returns the raw pgxpool pool. Repositories run their queries through
// it. The returned pool's lifecycle belongs to the wrapper; the caller must not
// call Close on it.
func (p *Pool) Pool() *pgxpool.Pool {
	if p == nil {
		return nil
	}
	return p.pool
}

// Ping verifies the database is reachable; it is for health checks.
func (p *Pool) Ping(ctx context.Context) error {
	if p == nil || p.pool == nil {
		return errors.Unavailable("db_pool_closed", "the database pool was never built")
	}
	if err := p.pool.Ping(ctx); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "db_unreachable",
			"the database is unreachable (target: %s)", p.target)
	}
	return nil
}

// Target returns the representation of the target that is safe to log (the
// host and database name). The password and other credentials do not appear in
// it.
func (p *Pool) Target() string {
	if p == nil {
		return unknownTarget
	}
	return p.target
}

// Close closes every connection in the pool and waits for the open ones to
// finish.
//
// Because pgxpool.Close takes no context and cannot be canceled, this signature
// takes none either; it is a deliberate exception to plan Section 8's rule that
// "every method takes a context".
func (p *Pool) Close() {
	if p == nil || p.pool == nil {
		return
	}
	p.pool.Close()
	p.log.Info("the postgres connection pool was closed", slog.String("target", p.target))
}

// dsnPoolKeys are the pool settings pgxpool.ParseConfig reads out of a DSN and
// this package then OVERWRITES from [Config].
var dsnPoolKeys = []string{
	"pool_max_conns",
	"pool_min_conns",
	"pool_min_idle_conns",
	"pool_max_conn_lifetime",
	"pool_max_conn_idle_time",
	"pool_max_conn_lifetime_jitter",
	"pool_health_check_period",
}

// warnAboutDSNPoolSettings reports a DSN that carries pool_* parameters, which
// this package silently discards.
//
// pgxpool.ParseConfig honors them; [New] then assigns every one of those fields
// from [Config], so a DATABASE_URL ending in "?pool_max_conns=40" configures
// nothing and says nothing. That was harmless while the pool size was
// hardcoded and nobody had a reason to write it. It stopped being harmless the
// moment DB_MAX_CONNS existed: an operator who has just been told the pool is
// sizeable has two plausible places to say so, and one of them is a no-op.
//
// It WARNS rather than refusing, by ADR 0015 decision 4's criterion: refusing
// would stop a process that has been starting successfully for as long as the
// parameter has been ignored, and the failure this prevents — a pool that is
// not the size the operator asked for — is a performance surprise, not a
// correctness one. Making it loud is what it was missing.
func warnAboutDSNPoolSettings(ctx context.Context, dsn string, log *slog.Logger) {
	found := make([]string, 0, len(dsnPoolKeys))
	for _, key := range dsnPoolKeys {
		if strings.Contains(dsn, key+"=") {
			found = append(found, key)
		}
	}
	if len(found) == 0 {
		return
	}

	log.WarnContext(ctx, "the database address carries pool settings that are IGNORED; set DB_MAX_CONNS and DB_MIN_CONNS instead",
		"ignored", strings.Join(found, ","), "target", Redact(dsn))
}

// Redact strips the credentials out of a connection address and returns a
// representation safe to log, containing only the host and the database name.
//
// It recognizes both the URL form (postgres://user:password@host:5432/name) and
// the key=value form pgx accepts (host=... dbname=... password=...). For input
// it cannot parse it returns a fixed placeholder: the raw DSN is never handed
// back under any circumstance, because this function's only purpose is to make
// a password leak structurally impossible.
func Redact(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Host != "" {
		// u.User sits in a separate field; Host holds only host[:port].
		return u.Host + "/" + databaseOrPlaceholder(strings.TrimPrefix(u.Path, "/"))
	}

	var host, port, name string
	for _, field := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "host":
			host = value
		case "port":
			port = value
		case "dbname":
			name = value
		}
	}
	if host == "" {
		return unknownTarget
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return host + "/" + databaseOrPlaceholder(name)
}

// databaseOrPlaceholder turns an empty database name into a readable
// placeholder.
func databaseOrPlaceholder(name string) string {
	if name == "" {
		return unknownDatabase
	}
	return name
}
