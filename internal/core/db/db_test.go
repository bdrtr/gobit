package db

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// testPassword is the constant embedded in the DSNs of the redaction tests; it
// must NEVER appear in the output.
const testPassword = "a-very-secret-password"

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	const dsn = "postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable"
	cfg := DefaultConfig(dsn)

	assert.Equal(t, dsn, cfg.URL)
	assert.Equal(t, int32(defaultMaxConns), cfg.MaxConns)
	assert.Equal(t, int32(defaultMinConns), cfg.MinConns)
	assert.Equal(t, defaultMaxConnLifetime, cfg.MaxConnLifetime)
	assert.Equal(t, defaultMaxConnIdleTime, cfg.MaxConnIdleTime)
	assert.Equal(t, defaultConnectTimeout, cfg.ConnectTimeout)

	// The defaults being consistent with each other is what makes DefaultConfig
	// usable on its own.
	require.NoError(t, cfg.Validate())
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	base := DefaultConfig("postgres://localhost:5432/gobit")

	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr bool
	}{
		{"the defaults are valid", func(*Config) {}, false},
		{"empty url", func(c *Config) { c.URL = "   " }, true},
		{"zero MaxConns", func(c *Config) { c.MaxConns = 0 }, true},
		{"negative MinConns", func(c *Config) { c.MinConns = -1 }, true},
		{"MinConns > MaxConns", func(c *Config) { c.MinConns = c.MaxConns + 1 }, true},
		{"zero MaxConnLifetime", func(c *Config) { c.MaxConnLifetime = 0 }, true},
		{"zero MaxConnIdleTime", func(c *Config) { c.MaxConnIdleTime = 0 }, true},
		{"idle > lifetime", func(c *Config) { c.MaxConnIdleTime = c.MaxConnLifetime + time.Second }, true},
		{"zero ConnectTimeout", func(c *Config) { c.ConnectTimeout = 0 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			tt.mutate(&cfg)

			err := cfg.Validate()
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the error class must be KindInvalid, got %v", errors.KindOf(err))
			assert.Equal(t, "db_config_invalid", errors.CodeOf(err))
		})
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "the url form strips the password",
			dsn:  "postgres://gobit:" + testPassword + "@db.internal:5432/gobit?sslmode=require",
			want: "db.internal:5432/gobit",
		},
		{
			name: "the url form without a port",
			dsn:  "postgres://gobit:" + testPassword + "@localhost/gobit",
			want: "localhost/gobit",
		},
		{
			name: "the url form with no database name",
			dsn:  "postgres://gobit:" + testPassword + "@localhost:5432",
			want: "localhost:5432/" + unknownDatabase,
		},
		{
			name: "a password in a query parameter is dropped too",
			dsn:  "postgres://localhost:5432/gobit?password=" + testPassword,
			want: "localhost:5432/gobit",
		},
		{
			name: "the key=value form",
			dsn:  "host=db.internal port=5432 user=gobit password=" + testPassword + " dbname=gobit",
			want: "db.internal:5432/gobit",
		},
		{
			name: "the key=value form without a port",
			dsn:  "host=db.internal user=gobit password=" + testPassword,
			want: "db.internal/" + unknownDatabase,
		},
		{
			name: "input that cannot be parsed falls back to the placeholder",
			dsn:  "this is not a dsn " + testPassword,
			want: unknownTarget,
		},
		{
			name: "empty input",
			dsn:  "",
			want: unknownTarget,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Redact(tt.dsn)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, testPassword, "the redacted representation cannot contain the password")
		})
	}
}

// TestNewRejectsBadDSNWithoutLeakingPassword runs without network access: an
// invalid port makes pgx's DSN parser fail BEFORE any connection attempt.
func TestNewRejectsBadDSNWithoutLeakingPassword(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig("postgres://gobit:" + testPassword + "@localhost:not-a-port/gobit")

	pool, err := New(context.Background(), cfg, nil)

	require.Error(t, err)
	assert.Nil(t, pool)
	assert.True(t, errors.IsInvalid(err), "the error class must be KindInvalid, got %v", errors.KindOf(err))
	assert.Equal(t, "db_dsn_invalid", errors.CodeOf(err))
	assert.NotContains(t, err.Error(), testPassword, "the error message cannot leak the password")
}

// TestNewWarnsAboutIgnoredDSNPoolSettings proves a DSN that tries to size the
// pool is reported instead of being silently discarded.
//
// pgxpool honors pool_max_conns; New then overwrites it from Config, so the
// parameter configures nothing. With DB_MAX_CONNS in the operator's hands there
// are now two plausible places to write the same number and only one of them
// works — the other must not be quiet about it.
func TestNewWarnsAboutIgnoredDSNPoolSettings(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := DefaultConfig("postgres://gobit:" + testPassword +
		"@127.0.0.1:1/gobit?sslmode=disable&pool_max_conns=40&pool_min_conns=8")
	cfg.ConnectTimeout = 50 * time.Millisecond

	// The pool cannot open against a closed port; the warning is written before
	// that and is what this test is about.
	_, err := New(context.Background(), cfg, log)
	require.Error(t, err)

	line := buf.String()
	assert.Contains(t, line, "IGNORED", "the ignored parameters must be reported: %s", line)
	assert.Contains(t, line, "pool_max_conns", "the offending key must be named: %s", line)
	assert.Contains(t, line, "pool_min_conns", "every offending key must be named: %s", line)
	assert.Contains(t, line, "DB_MAX_CONNS", "the line must say where the number belongs: %s", line)
	assert.NotContains(t, line, testPassword, "the log cannot leak the password: %s", line)
}

// TestNewIsQuietWithoutDSNPoolSettings guards the other direction: an ordinary
// address must not produce the warning, or the line becomes noise nobody reads.
func TestNewIsQuietWithoutDSNPoolSettings(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := DefaultConfig("postgres://gobit:" + testPassword + "@127.0.0.1:1/gobit?sslmode=disable")
	cfg.ConnectTimeout = 50 * time.Millisecond

	_, err := New(context.Background(), cfg, log)
	require.Error(t, err)

	assert.NotContains(t, buf.String(), "IGNORED", "no pool parameter was given: %s", buf.String())
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	pool, err := New(context.Background(), Config{}, nil)

	require.Error(t, err)
	assert.Nil(t, pool)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, "db_config_invalid", errors.CodeOf(err))
}

// TestNilPoolMethodsAreSafe guarantees that deferred Close/Ping calls do not
// panic when the setup was cut short.
func TestNilPoolMethodsAreSafe(t *testing.T) {
	t.Parallel()

	var p *Pool

	assert.NotPanics(t, p.Close)
	assert.Nil(t, p.Pool())
	assert.Equal(t, unknownTarget, p.Target())

	err := p.Ping(context.Background())
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
}

func TestMigrationsTable(t *testing.T) {
	t.Parallel()

	t.Run("it produces a table name for a valid owner", func(t *testing.T) {
		t.Parallel()

		product, err := MigrationsTable("product")
		require.NoError(t, err)
		assert.Equal(t, "product_schema_migrations", product)

		alpha, err := MigrationsTable("alpha")
		require.NoError(t, err)
		assert.Equal(t, "alpha_schema_migrations", alpha)

		beta, err := MigrationsTable("beta")
		require.NoError(t, err)
		// Two different modules' tables can never be equal.
		assert.NotEqual(t, alpha, beta)
	})

	// Table names cannot be parameterized in SQL; without the validation being
	// INSIDE this function, a module name from outside could be embedded
	// straight into a query.
	t.Run("it produces no name for an invalid owner", func(t *testing.T) {
		t.Parallel()

		bad := map[string]string{
			"sql injection": `x"; DROP TABLE users; --`,
			"empty":         "",
			"upper case":    "Product",
			"dot":           "public.product",
		}
		for name, owner := range bad {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				table, err := MigrationsTable(owner)
				require.Error(t, err)
				assert.Empty(t, table, "no table name may be produced for an invalid owner")
				assert.True(t, errors.IsInvalid(err))
				assert.Equal(t, "db_migration_owner_invalid", errors.CodeOf(err))
			})
		}
	})
}

func TestValidateOwner(t *testing.T) {
	t.Parallel()

	valid := []string{"product", "p", "order_line", "tax2", "a0_9"}
	for _, owner := range valid {
		t.Run("valid/"+owner, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, validateOwner(owner))
		})
	}

	invalid := map[string]string{
		"empty":                  "",
		"upper case":             "Product",
		"hyphen":                 "order-line",
		"space":                  "order line",
		"starts with a digit":    "1product",
		"starts with underscore": "_product",
		"sql injection":          `product"; DROP TABLE users; --`,
		"dot":                    "public.product",
		"too long":               strings.Repeat("a", 41),
	}
	for name, owner := range invalid {
		t.Run("invalid/"+name, func(t *testing.T) {
			t.Parallel()

			err := validateOwner(owner)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Equal(t, "db_migration_owner_invalid", errors.CodeOf(err))
		})
	}
}

// TestMigrateValidatesBeforeConnecting checks the inputs that must be rejected
// without touching the database at all.
func TestMigrateValidatesBeforeConnecting(t *testing.T) {
	t.Parallel()

	const dsn = "postgres://gobit:" + testPassword + "@localhost:5432/gobit"
	src := fstest.MapFS{
		"000001_init.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_init.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}

	tests := []struct {
		name     string
		call     func(ctx context.Context) error
		wantCode string
	}{
		{
			name:     "invalid owner",
			call:     func(ctx context.Context) error { return Migrate(ctx, dsn, src, "Product Module") },
			wantCode: "db_migration_owner_invalid",
		},
		{
			name:     "no source",
			call:     func(ctx context.Context) error { return Migrate(ctx, dsn, nil, "product") },
			wantCode: "db_migration_source_missing",
		},
		{
			name:     "unsupported scheme",
			call:     func(ctx context.Context) error { return Migrate(ctx, "mysql://h/db", src, "product") },
			wantCode: "db_dsn_unsupported",
		},
		{
			name:     "the rollback is validated too",
			call:     func(ctx context.Context) error { return MigrateDown(ctx, dsn, src, "UPPER", 1) },
			wantCode: "db_migration_owner_invalid",
		},
		{
			name:     "the version query is validated too",
			call:     func(ctx context.Context) error { _, _, err := Version(ctx, dsn, "-"); return err },
			wantCode: "db_migration_owner_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.call(context.Background())
			require.Error(t, err)
			assert.Equal(t, tt.wantCode, errors.CodeOf(err))
			assert.True(t, errors.IsInvalid(err), "the error class must be KindInvalid, got %v", errors.KindOf(err))
			assert.NotContains(t, err.Error(), testPassword, "the error message cannot leak the password")
		})
	}
}

// TestMigrateHonorsCancelledContext checks that no connection is attempted
// with a canceled context.
func TestMigrateHonorsCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := fstest.MapFS{
		"000001_init.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_init.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}
	const dsn = "postgres://gobit:gobit@localhost:5432/gobit"

	err := Migrate(ctx, dsn, src, "product")
	require.Error(t, err)
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))
	assert.True(t, errors.Is(err, context.Canceled))

	_, _, verErr := Version(ctx, dsn, "product")
	require.Error(t, verErr)
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(verErr))
}

// TestMigrationHonorsDeadlineOnStalledServer checks that the ctx bound is
// REALLY applied against a server that accepts the connection and never
// completes the handshake: a database behind a firewall dropping packets
// behaves in exactly this way.
func TestMigrationHonorsDeadlineOnStalledServer(t *testing.T) {
	t.Parallel()

	dsn := stalledServerDSN(t)
	src := fstest.MapFS{
		"000001_init.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_init.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}

	// The context bound is far shorter than the time allowed for the call to
	// return; the gap makes the test insensitive to timing.
	const deadline = 500 * time.Millisecond
	const tolerance = 15 * time.Second

	calls := map[string]func(ctx context.Context) error{
		"migrate":      func(ctx context.Context) error { return Migrate(ctx, dsn, src, "product") },
		"migrate down": func(ctx context.Context) error { return MigrateDown(ctx, dsn, src, "product", 1) },
		"version":      func(ctx context.Context) error { _, _, err := Version(ctx, dsn, "product"); return err },
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- call(ctx) }()

			select {
			case err := <-done:
				require.Error(t, err)
				assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))
				assert.True(t, errors.HasKind(err, errors.KindUnavailable),
					"the error class must be KindUnavailable, got %v", errors.KindOf(err))
				assert.True(t, errors.Is(err, context.DeadlineExceeded))
				assert.NotContains(t, err.Error(), testPassword, "the error message cannot leak the password")
			case <-time.After(tolerance):
				t.Fatalf("the call did not return within %s: the ctx bound is not applied", tolerance)
			}
		})
	}
}

// stalledServerDSN opens a listener that accepts the connection and never
// answers, and returns a DSN pointing at it. It needs no Docker.
func stalledServerDSN(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var mu sync.Mutex
	var accepted []net.Conn

	t.Cleanup(func() {
		require.NoError(t, listener.Close())
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range accepted {
			_ = conn.Close()
		}
	})

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			// The connection is left silent on purpose; closing belongs to
			// Cleanup.
			mu.Lock()
			accepted = append(accepted, conn)
			mu.Unlock()
		}
	}()

	return "postgres://gobit:" + testPassword + "@" + listener.Addr().String() + "/gobit?sslmode=disable"
}
