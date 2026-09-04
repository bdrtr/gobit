package config_test

import (
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"time"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/core/config"
)

// envKeys are all the environment variables Config reads.

var envKeys = []string{
	"APP_ENV", "APP_PORT", "DATABASE_URL", "REDIS_URL",
	"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_INSECURE", "OTEL_SERVICE_NAME",
	"OTEL_TRACES_SAMPLER_ARG", "METRIC_EXPORT_INTERVAL",
	"RATE_LIMIT_PER_MINUTE", "TRUSTED_PROXY_HOPS", "IDEMPOTENCY_TTL",
	"IDEMPOTENCY_MAX_MEMORY_BYTES",
	"LOG_LEVEL", "LOG_FORMAT", "SHUTDOWN_TIMEOUT", "READ_HEADER_TIMEOUT",
	"READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT", "READINESS_DEGRADED_TIMEOUT",
	"EVENT_BUS", "EVENT_BUS_CONSUMER",
	"JWT_SECRET", "JWT_TTL",
	"ADMIN_BOOTSTRAP_EMAIL", "ADMIN_BOOTSTRAP_PASSWORD",
	"GUARD_BACKEND", "REDIS_KEY_PREFIX", "NOTIFICATION_PROVIDER",
	"FILE_PROVIDER", "FILE_ROOT", "FILE_MAX_UPLOAD_BYTES", "FILE_ALLOWED_TYPES",
	"GRAPHQL_MAX_DEPTH", "GRAPHQL_MAX_COMPLEXITY", "GRAPHQL_INTROSPECTION",
	"DB_MAX_CONNS", "DB_MIN_CONNS",
}

// productionJWTSecret is the 32-character signing secret used in production scenarios.
const productionJWTSecret = "0123456789abcdef0123456789abcdef"

// clearEnv temporarily deletes the variables that may be defined in the shell the
// test runs in; that way the default behavior is exercised in isolation.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		if old, ok := os.LookupEnv(k); ok {
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("os.Unsetenv(%q): %v", k, err)
			}
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	// With an empty environment the defaults have to agree with docker-compose.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() beklenmedik wantErr: %v", err)
	}

	if cfg.AppEnv != "development" {
		t.Errorf("AppEnv = %q, expected %q", cfg.AppEnv, "development")
	}
	if cfg.AppPort != 9000 {
		t.Errorf("AppPort = %d, expected 9000", cfg.AppPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, expected %q", cfg.LogLevel, "info")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, expected %q", cfg.LogFormat, "json")
	}
	if !strings.HasPrefix(cfg.DatabaseURL, "postgres://") {
		t.Errorf("DatabaseURL = %q, it has to start with postgres://", cfg.DatabaseURL)
	}
	if !strings.HasPrefix(cfg.RedisURL, "redis://") {
		t.Errorf("RedisURL = %q, it has to start with redis://", cfg.RedisURL)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %s, expected 15s", cfg.ShutdownTimeout)
	}
}

func TestLoadFromEnv(t *testing.T) {
	clearEnv(t)

	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_PORT", "8080")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/x")
	t.Setenv("REDIS_URL", "redis://cache:6379/1")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("JWT_SECRET", productionJWTSecret)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() beklenmedik wantErr: %v", err)
	}

	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, expected true")
	}
	if got, want := cfg.Addr(), ":8080"; got != want {
		t.Errorf("Addr() = %q, expected %q", got, want)
	}
	if got, want := cfg.SlogLevel(), slog.LevelDebug; got != want {
		t.Errorf("SlogLevel() = %v, expected %v", got, want)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %s, expected 30s", cfg.ShutdownTimeout)
	}
}

func TestLoadInvalidEnv(t *testing.T) {
	clearEnv(t)

	tests := map[string]struct {
		key, value string
	}{
		"bilinmeyen environment": {"APP_ENV", "staging-2"},
		"a zero port":            {"APP_PORT", "0"},
		"a port out of range":    {"APP_PORT", "70000"},
		"bilinmeyen seviye":      {"LOG_LEVEL", "trace"},
		"an unknown format":      {"LOG_FORMAT", "logfmt"},
		"bilinmeyen bus":         {"EVENT_BUS", "kafka"},
		"negatif timeout":        {"SHUTDOWN_TIMEOUT", "-1s"},
		"a zero probe budget":    {"READINESS_DEGRADED_TIMEOUT", "0s"},
		"a non-numeric port":     {"APP_PORT", "abc"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			if _, err := config.Load(); err == nil {
				t.Fatalf("Load() should have returned an error (%s=%s)", tt.key, tt.value)
			}
		})
	}
}

func TestSlogLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}

	for level, want := range tests {
		t.Run(level, func(t *testing.T) {
			cfg := config.Config{LogLevel: level}
			if got := cfg.SlogLevel(); got != want {
				t.Errorf("SlogLevel() = %v, expected %v", got, want)
			}
		})
	}
}

func TestValidateRejectsEmptyURLs(t *testing.T) {
	// The base is NOT a hand-built literal but a valid config loaded from the
	// defaults. Were it a literal, every mandatory field added to Config would break
	// this test and produce a maintenance burden with nothing to do with what the
	// test cares about (rejecting an empty URL).
	base := validConfig(t)
	if err := base.Validate(); err != nil {
		t.Fatalf("a valid config was rejected: %v", err)
	}

	noDB := base
	noDB.DatabaseURL = ""
	if err := noDB.Validate(); err == nil {
		t.Error("an empty DATABASE_URL was accepted")
	}

	noRedis := base
	noRedis.RedisURL = ""
	if err := noRedis.Validate(); err == nil {
		t.Error("an empty REDIS_URL was accepted")
	}
}

// TestProductionRejectsLocalDefaults verifies that production does not quietly
// fall back to the local development defaults.
//
// The regression: because envDefault was filled, Validate's `== ""` check was never
// triggered from the Load path. A missing (or empty) secret injection would go to
// production with the hard-coded gobit:gobit credential and sslmode=disable.
func TestProductionRejectsLocalDefaults(t *testing.T) {
	tests := map[string]func(t *testing.T){
		"env hic set edilmemis": func(t *testing.T) {},
		"env bos string": func(t *testing.T) {
			t.Setenv("DATABASE_URL", "")
			t.Setenv("REDIS_URL", "")
		},
		"acikca varsayilanla ayni": func(t *testing.T) {
			t.Setenv("DATABASE_URL", config.DefaultDatabaseURL)
			t.Setenv("REDIS_URL", config.DefaultRedisURL)
		},
	}

	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("APP_ENV", "production")
			setup(t)

			cfg, err := config.Load()
			if err == nil {
				t.Fatalf("Load() should have returned an error; DatabaseURL=%q", cfg.DatabaseURL)
			}
			if !strings.Contains(err.Error(), "production") {
				t.Errorf("the error message has to explain the production condition: %v", err)
			}
		})
	}
}

func TestProductionAcceptsOverriddenURLs(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
	t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
	t.Setenv("JWT_SECRET", productionJWTSecret)

	if _, err := config.Load(); err != nil {
		t.Fatalf("Load() failed with overridden URLs: %v", err)
	}
}

func TestDevelopmentAllowsLocalDefaults(t *testing.T) {
	// Local development must not need extra settings with "make up && make run".
	clearEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() wantErr verdi: %v", err)
	}
	if cfg.DatabaseURL != config.DefaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, the default was expected", cfg.DatabaseURL)
	}
}

// TestDefaultTagsMatchConstants checks that the envDefault tags and the constants
// have not drifted apart. Because Go struct tags do not accept a constant
// reference the value has to be repeated in two places; on a drift the production
// guard would quietly go out of service.
func TestDefaultTagsMatchConstants(t *testing.T) {
	want := map[string]string{
		"DatabaseURL":          config.DefaultDatabaseURL,
		"RedisURL":             config.DefaultRedisURL,
		"RedisKeyPrefix":       config.DefaultRedisKeyPrefix,
		"NotificationProvider": config.DefaultNotificationProvider,
		"FileProvider":         config.DefaultFileProvider,
		"FileRoot":             config.DefaultFileRoot,
		"FileAllowedTypes":     config.DefaultFileAllowedTypes,
		"FileMaxUploadBytes":   strconv.FormatInt(config.DefaultFileMaxUploadBytes, 10),
		"GraphQLMaxDepth":      strconv.Itoa(config.DefaultGraphQLMaxDepth),
		"GraphQLMaxComplexity": strconv.Itoa(config.DefaultGraphQLMaxComplexity),
		"GraphQLIntrospection": strconv.FormatBool(config.DefaultGraphQLIntrospection),
		"DBMaxConns":           strconv.FormatInt(int64(config.DefaultDBMaxConns), 10),
		"DBMinConns":           strconv.FormatInt(int64(config.DefaultDBMinConns), 10),
	}

	typ := reflect.TypeOf(config.Config{})
	for field, expected := range want {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Fatalf("Config.%s: no such field", field)
		}
		if got := f.Tag.Get("envDefault"); got != expected {
			t.Errorf("Config.%s envDefault tag is %q, the constant is %q — drifted", field, got, expected)
		}
	}
}

func TestTimeoutValidation(t *testing.T) {
	tests := map[string]struct{ key, value string }{
		"read timeout sifir":            {"READ_TIMEOUT", "0s"},
		"write timeout negatif":         {"WRITE_TIMEOUT", "-1s"},
		"idle timeout sifir":            {"IDLE_TIMEOUT", "0s"},
		"read < read-header (tutarsiz)": {"READ_TIMEOUT", "5s"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			if tt.key == "READ_TIMEOUT" && tt.value == "5s" {
				t.Setenv("READ_HEADER_TIMEOUT", "10s")
			}
			t.Setenv(tt.key, tt.value)
			if _, err := config.Load(); err == nil {
				t.Fatalf("Load() should have returned an error (%s=%s)", tt.key, tt.value)
			}
		})
	}
}

// TestProductionRequiresStrongJWTSecret verifies that a weak or empty signing
// secret is REJECTED in production.
//
// A predictable signing secret means anybody can mint themselves an admin token.
// The application coming up quietly would mean the hole is noticed only once it
// is exploited.
func TestProductionRequiresStrongJWTSecret(t *testing.T) {
	tests := map[string]string{
		"no secret given at all": "",
		"far too short a secret": "short",
		"31 karakter":            "0123456789abcdef0123456789abcde",
	}

	for name, secret := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("APP_ENV", "production")
			t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
			t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
			if secret != "" {
				t.Setenv("JWT_SECRET", secret)
			}

			_, err := config.Load()
			require.Error(t, err, "a weak signing secret cannot be accepted in production")
			assert.Contains(t, err.Error(), "JWT_SECRET")
		})
	}
}

// TestDevelopmentAllowsEmptyJWTSecret verifies that the signing secret is NOT
// mandatory in development; the server has to come up while auth is unregistered too.
func TestDevelopmentAllowsEmptyJWTSecret(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err, "the signing secret must not be mandatory in development")
	assert.Empty(t, cfg.JWTSecret)
	assert.Positive(t, cfg.JWTTTL, "the token lifetime default has to be filled")
}

// validConfig returns a config loaded from the defaults that passes validation.
func validConfig(t *testing.T) config.Config {
	t.Helper()

	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("the default config could not be loaded: %v", err)
	}

	return cfg
}

// TestValidateChecksTheNewSettings verifies that the bounds of the settings that
// arrived with Phase 9 really are enforced.
func TestValidateChecksTheNewSettings(t *testing.T) {
	base := validConfig(t)

	tests := map[string]func(c *config.Config){
		"a negative sample ratio":    func(c *config.Config) { c.TraceSampleRatio = -0.1 },
		"a sample ratio above one":   func(c *config.Config) { c.TraceSampleRatio = 1.1 },
		"a zero metric interval":     func(c *config.Config) { c.MetricInterval = 0 },
		"a negative metric interval": func(c *config.Config) { c.MetricInterval = -time.Second },
		"an empty service name":      func(c *config.Config) { c.ServiceName = "" },
		"a negative proxy hop count": func(c *config.Config) { c.TrustedProxyHops = -1 },
		"a zero idempotency TTL":     func(c *config.Config) { c.IdempotencyTTL = 0 },
		"a zero idempotency budget": func(c *config.Config) {
			c.IdempotencyMaxMemoryBytes = 0
		},
		"a negative idempotency budget": func(c *config.Config) {
			c.IdempotencyMaxMemoryBytes = -1
		},
		// A budget that cannot carry a single maximum-size record means the guard is
		// quietly closed for large responses while looking open; because the number
		// looks valid, this is the sneakiest value.
		"an idempotency budget below the floor": func(c *config.Config) {
			c.IdempotencyMaxMemoryBytes = config.MinIdempotencyMemoryBytes - 1
		},
	}

	for name, boz := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := base
			boz(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("an invalid value was accepted")
			}
		})
	}

	// The boundary values have to be ACCEPTED.
	for name, ayarla := range map[string]func(c *config.Config){
		"a zero sample ratio":    func(c *config.Config) { c.TraceSampleRatio = 0 },
		"a sample ratio of one":  func(c *config.Config) { c.TraceSampleRatio = 1 },
		"a zero proxy hop count": func(c *config.Config) { c.TrustedProxyHops = 0 },
		"a zero rate limit":      func(c *config.Config) { c.RateLimitPerMinute = 0 },
		"an idempotency budget exactly at the floor": func(c *config.Config) {
			c.IdempotencyMaxMemoryBytes = config.MinIdempotencyMemoryBytes
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			ayarla(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Errorf("a valid boundary value was rejected: %v", err)
			}
		})
	}
}

// TestProductionRejectsUnencryptedTracing verifies that a TLS-less OTLP
// connection is not accepted in production.
//
// Traces carry request paths, identities and error messages; sending them
// unencrypted makes them listenable on the network.
func TestProductionRejectsUnencryptedTracing(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
	t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
	t.Setenv("JWT_SECRET", productionJWTSecret)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector.internal:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	if _, err := config.Load(); err == nil {
		t.Error("unencrypted OTLP was accepted in production")
	}

	// If no collector is configured at all the insecure flag is meaningless and must
	// not block the installation.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	if _, err := config.Load(); err != nil {
		t.Errorf("an installation without a collector was rejected: %v", err)
	}
}

// setUpSharedEnvironment prepares the given environment with connection addresses
// that are NOT the defaults.
//
// Overriding the URLs is not the subject of the test; had they been left
// unoverridden, Validate would return a URL error in the production scenario
// without ever reaching the JWT/OTLP gate and the test would not have exercised
// what it means to.
func setUpSharedEnvironment(t *testing.T, environment string) {
	t.Helper()

	clearEnv(t)
	t.Setenv("APP_ENV", environment)
	t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
	t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
}

// TestSharedEnvironmentsRequireASigningSecret verifies that the signing secret
// gate works in every environment OTHER THAN local development.
//
// The regression: the gate was only inside APP_ENV=production. staging is usually
// multi-instance; when the secret is left empty every instance produces its own
// random secret at startup (see cmd/server's jwtSecret) and a token taken from one
// instance returns a 401 on another. Because it depends on the load balancer's
// distribution the fault is intermittent and hard to diagnose.
func TestSharedEnvironmentsRequireASigningSecret(t *testing.T) {
	tests := map[string]struct {
		environment string
		secret      string
		rejected    bool
	}{
		"staging, no secret given at all": {environment: "staging", secret: "", rejected: true},
		"staging, far too short a secret": {environment: "staging", secret: "short", rejected: true},
		"staging 31 karakter":             {environment: "staging", secret: "0123456789abcdef0123456789abcde", rejected: true},
		"staging, a strong secret":        {environment: "staging", secret: productionJWTSecret},
		// The production rows are the regression shield of the existing guard: while
		// the gate is widened we also check that production was not loosened.
		"production, no secret given at all": {environment: "production", secret: "", rejected: true},
		"production, a strong secret":        {environment: "production", secret: productionJWTSecret},
		// A convenience in local development: a single instance runs and the price of
		// the token dropping on a restart is next to nothing.
		"development, no secret given at all": {environment: "development", secret: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			setUpSharedEnvironment(t, tt.environment)
			if tt.secret != "" {
				t.Setenv("JWT_SECRET", tt.secret)
			}

			_, err := config.Load()
			if !tt.rejected {
				require.NoError(t, err, "a valid configuration was rejected")

				return
			}

			require.Error(t, err, "a weak signing secret cannot be accepted in a shared environment")
			assert.Contains(t, err.Error(), "JWT_SECRET")
			assert.Contains(t, err.Error(), tt.environment, "the error message has to say which environment is enforcing it")
		})
	}
}

// TestSharedEnvironmentsRejectUnencryptedTracing verifies that the TLS-less OTLP
// ban works in every environment OTHER THAN local development.
//
// Traces carry request paths, identities and error messages; even if staging's
// traffic is counted as "not real", its network and its tokens are real.
func TestSharedEnvironmentsRejectUnencryptedTracing(t *testing.T) {
	tests := map[string]struct {
		environment string
		endpoint    string
		insecure    string
		rejected    bool
	}{
		"staging, an unencrypted collector": {
			environment: "staging", endpoint: "collector.internal:4317", insecure: "true", rejected: true,
		},
		"staging, a TLS collector": {
			environment: "staging", endpoint: "collector.internal:4317", insecure: "false",
		},
		// If no collector is configured at all the flag is meaningless and must not
		// block the installation.
		"staging, no collector": {
			environment: "staging", endpoint: "", insecure: "true",
		},
		"production, an unencrypted collector": {
			environment: "production", endpoint: "collector.internal:4317", insecure: "true", rejected: true,
		},
		// Locally the collector is most often a docker container without a certificate.
		"development, an unencrypted collector": {
			environment: "development", endpoint: "localhost:4317", insecure: "true",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			setUpSharedEnvironment(t, tt.environment)
			t.Setenv("JWT_SECRET", productionJWTSecret)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.insecure)

			_, err := config.Load()
			if !tt.rejected {
				require.NoError(t, err, "a valid configuration was rejected")

				return
			}

			require.Error(t, err, "unencrypted OTLP cannot be accepted in a shared environment")
			assert.Contains(t, err.Error(), "OTEL_EXPORTER_OTLP_INSECURE")
			assert.Contains(t, err.Error(), tt.environment, "the error message has to say which environment is enforcing it")
		})
	}
}

// TestIsSharedLeavesOutDevelopmentOnly documents at a glance which environments
// the gate covers.
func TestIsSharedLeavesOutDevelopmentOnly(t *testing.T) {
	tests := map[string]bool{
		"development": false,
		"staging":     true,
		"production":  true,
	}

	for environment, expected := range tests {
		t.Run(environment, func(t *testing.T) {
			cfg := config.Config{AppEnv: environment}
			assert.Equal(t, expected, cfg.IsShared())
		})
	}
}

// seedEmail is the e-mail used in the first administrator scenarios.
const seedEmail = "first.admin@example.com"

// TestTheFirstAdminSeedWantsBothTogether verifies that a half configuration is
// not SILENTLY skipped.
//
// An operator writing one of the two variables and forgetting the other believes,
// under a silent skip, that the seed ran; they discover what is missing only at
// the first login attempt, often days after the installation. Stopping at startup
// moves the fault to the moment the configuration is still at hand.
func TestTheFirstAdminSeedWantsBothTogether(t *testing.T) {
	tests := map[string]struct {
		email    string
		password string
		rejected bool
	}{
		// The seed configuration is NOT mandatory: the environment of an installed
		// system does not have to carry these variables.
		"neither given":     {},
		"both given":        {email: seedEmail, password: "development-password"},
		"only the e-mail":   {email: seedEmail, rejected: true},
		"only the password": {password: "development-password", rejected: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			if tt.email != "" {
				t.Setenv("ADMIN_BOOTSTRAP_EMAIL", tt.email)
			}
			if tt.password != "" {
				t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", tt.password)
			}

			cfg, err := config.Load()
			if !tt.rejected {
				require.NoError(t, err, "a valid seed configuration was rejected")
				assert.Equal(t, tt.email, cfg.AdminBootstrapEmail)
				assert.Equal(t, tt.password, cfg.AdminBootstrapPassword)

				return
			}

			require.Error(t, err, "a half seed configuration must not be skipped quietly")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_EMAIL")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_PASSWORD",
				"the error message has to make visible which of the two may be missing")
		})
	}
}

// TestASharedEnvironmentWantsASeedPasswordLength verifies that the minimum
// length gate works only in shared environments.
//
// The first administrator password is not a user password but a deployment secret:
// it sits in an environment file and nobody has to memorize it, that is, the length
// costs nothing. Locally, on the other hand, convenience wins — a developer wanting
// to try things with "make up && make run" has to be able to type a short password.
func TestASharedEnvironmentWantsASeedPasswordLength(t *testing.T) {
	tests := map[string]struct {
		environment string
		password    string
		rejected    bool
	}{
		"staging 15 karakter":           {environment: "staging", password: "onbes-karakter1", rejected: true},
		"staging 16 karakter":           {environment: "staging", password: "onalti-karakter1"},
		"production, a short password":  {environment: "production", password: "short", rejected: true},
		"production, a long password":   {environment: "production", password: "a-sufficiently-long-password"},
		"development, a short password": {environment: "development", password: "short"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			setUpSharedEnvironment(t, tt.environment)
			t.Setenv("JWT_SECRET", productionJWTSecret)
			t.Setenv("ADMIN_BOOTSTRAP_EMAIL", seedEmail)
			t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", tt.password)

			_, err := config.Load()
			if !tt.rejected {
				require.NoError(t, err, "a valid seed configuration was rejected")

				return
			}

			require.Error(t, err, "a short seed password cannot be accepted in a shared environment")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_PASSWORD")
			assert.Contains(t, err.Error(), tt.environment, "the error message has to say which environment is enforcing it")
			assert.NotContains(t, err.Error(), tt.password,
				"the password MUST NOT appear in the error message; the message goes from stderr to the log collector")
		})
	}
}

// TestTheDefaultRedisKeyPrefixIsBackwardCompatible verifies that today's
// behavior is preserved while the prefix becomes configurable.
//
// The expected value is not read from the constant but written BY HAND: if the
// constant changes the test fails and the price of the change becomes visible.
// That price is concrete — all the rate limit counters and in-flight idempotency
// records of an upgraded installation move to another namespace at once, that is,
// every retry in the air at that moment is processed a second time.
func TestTheDefaultRedisKeyPrefixIsBackwardCompatible(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err, "it has to come up without the prefix being configured too")
	assert.Equal(t, "gobit", cfg.RedisKeyPrefix,
		"the default prefix has to stay the same as the old prefix baked into redisguard")
}

// TestTheRedisKeyPrefixFormIsValidated verifies that a prefix containing a
// separator or an invisible character is not accepted SILENTLY.
//
// The prefix is the only thing separating two installations sharing the same
// Redis. An accepted malformed prefix produces two separate faults: ':' can make
// the keys of two installations collide, while a trailing space moves the
// installation into ANOTHER namespace in a way nobody will notice — the counters
// reset, the in-flight idempotency records are ignored.
func TestTheRedisKeyPrefixFormIsValidated(t *testing.T) {
	tests := map[string]struct {
		prefix   string
		rejected bool
	}{
		"sade name":                 {prefix: "gobit"},
		"tireli name":               {prefix: "gobit-staging"},
		"a name with an underscore": {prefix: "gobit_prod"},
		"a name with a dot":         {prefix: "magaza.42"},
		"containing a separator":    {prefix: "gobit:staging", rejected: true},
		"ending with a separator":   {prefix: "gobit:", rejected: true},
		"with a trailing space":     {prefix: "gobit ", rejected: true},
		"containing a glob mark":    {prefix: "gobit*", rejected: true},
		"containing a slash":        {prefix: "gobit/prod", rejected: true},
		"with a non-latin letter":   {prefix: "ma\u011faza", rejected: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("REDIS_KEY_PREFIX", tt.prefix)

			cfg, err := config.Load()
			if !tt.rejected {
				require.NoError(t, err, "a valid prefix was rejected")
				assert.Equal(t, tt.prefix, cfg.RedisKeyPrefix)

				return
			}

			require.Error(t, err, "a malformed prefix must not be accepted quietly")
			assert.Contains(t, err.Error(), "REDIS_KEY_PREFIX",
				"the error message has to say which variable is wrong")
		})
	}
}

// TestAnEmptyRedisKeyPrefixIsRejected verifies that a hand-built Config cannot
// leave the namespace prefix empty.
//
// From the environment variable path an empty value already falls back to the
// default; this gate is for the callers calling Validate without going through
// Load (an embedding or a testing one, say). An empty prefix makes the keys
// ":idem:...", which means no namespace at all — and the namespace is the only
// reason the prefix is configurable.
func TestAnEmptyRedisKeyPrefixIsRejected(t *testing.T) {
	cfg := validConfig(t)
	cfg.RedisKeyPrefix = ""

	err := cfg.Validate()

	require.Error(t, err, "an empty prefix must not be accepted")
	assert.Contains(t, err.Error(), "REDIS_KEY_PREFIX")
}

// TestTheNotificationProviderDefaultIsTheOneThatDoesNotSend verifies that the
// default provider REALLY is the "log" one that does not send.
//
// A drift of the default would be a silent fault: the installation comes up, the
// endpoints work, and it would only be noticed while customers wait for their
// order confirmation.
func TestTheNotificationProviderDefaultIsTheOneThatDoesNotSend(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, config.DefaultNotificationProvider, cfg.NotificationProvider)
	assert.Equal(t, "log", cfg.NotificationProvider,
		"the name of the default provider has to say that it DOES NOT send")
}

// TestTheNotificationProviderFormIsValidated verifies the form check of the name.
//
// Config cannot know whether the name is REGISTERED (providers come from plugins);
// what is exercised here is only the form. That an unrecognized name stops startup
// is exercised on the cmd/server side.
func TestTheNotificationProviderFormIsValidated(t *testing.T) {
	tests := map[string]struct {
		value    string
		rejected bool
	}{
		"a plugin name":    {value: "sendgrid"},
		"the default":      {value: "log"},
		"a leading space":  {value: " log", rejected: true},
		"a trailing space": {value: "log ", rejected: true},
		"whitespace only":  {value: "   ", rejected: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("NOTIFICATION_PROVIDER", tt.value)

			cfg, err := config.Load()
			if !tt.rejected {
				require.NoError(t, err, "a valid provider name was rejected")
				assert.Equal(t, tt.value, cfg.NotificationProvider)

				return
			}

			require.Error(t, err, "a malformed provider name must not be accepted quietly")
			assert.Contains(t, err.Error(), "NOTIFICATION_PROVIDER",
				"the error message has to say which variable is wrong")
		})
	}
}

// TestAnEmptyNotificationProviderIsRejected verifies that a hand-built Config
// cannot leave the provider name empty.
//
// From the environment variable path an empty value already falls back to the
// default; this gate is for the callers calling Validate without going through
// Load (an embedding or a testing one, say). An empty name means looking for an
// empty identity in the provider registry, and because no provider can be
// registered under an empty identity every notification would return an error.
func TestAnEmptyNotificationProviderIsRejected(t *testing.T) {
	cfg := validConfig(t)
	cfg.NotificationProvider = ""

	err := cfg.Validate()

	require.Error(t, err, "an empty provider name must not be accepted")
	assert.Contains(t, err.Error(), "NOTIFICATION_PROVIDER")
}

// TestTheFileSettingsDefaultToADurableDirectory verifies that the
// out-of-the-box upload does not write into a TEMPORARY directory.
//
// A temporary directory would be tempting ("let it work without configuring
// anything") but would quietly lose the images on a restart: the address stays in
// the product record permanently while the file is gone. That is why the assertion
// pins not only "what the default is" but also "what it is NOT".
func TestTheFileSettingsDefaultToADurableDirectory(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, config.DefaultFileProvider, cfg.FileProvider)
	assert.Equal(t, config.DefaultFileRoot, cfg.FileRoot)
	assert.NotContains(t, cfg.FileRoot, os.TempDir(),
		"the default root cannot be a TEMPORARY directory; that means silent data loss on a restart")
	assert.Equal(t, config.DefaultFileMaxUploadBytes, cfg.FileMaxUploadBytes)
	assert.Equal(t,
		[]string{"image/jpeg", "image/png", "image/gif", "image/webp"}, cfg.FileAllowedTypes)
	assert.NotContains(t, cfg.FileAllowedTypes, "image/svg+xml",
		"an SVG is a document and carries script; it MUST NOT be on the default allow list")
}

// TestTheFileSettingsFormIsValidated verifies that invalid file settings stop startup.
//
// The form assertions on the allow list matter in particular: a type with
// parameters or in upper case never matches the type detected FROM THE CONTENT.
// Were it accepted quietly, a line would stay in the list and let no file through
// — the operator would believe they had "opened" the type.
func TestTheFileSettingsFormIsValidated(t *testing.T) {
	base := validConfig(t)

	tests := map[string]struct {
		boz      func(c *config.Config)
		degisken string
	}{
		"an empty provider":          {func(c *config.Config) { c.FileProvider = "" }, "FILE_PROVIDER"},
		"a provider with whitespace": {func(c *config.Config) { c.FileProvider = " local" }, "FILE_PROVIDER"},
		"an empty root":              {func(c *config.Config) { c.FileRoot = "" }, "FILE_ROOT"},
		"a root with whitespace":     {func(c *config.Config) { c.FileRoot = "/data/uploads " }, "FILE_ROOT"},
		"a zero maximum size":        {func(c *config.Config) { c.FileMaxUploadBytes = 0 }, "FILE_MAX_UPLOAD_BYTES"},
		"azami boyut negatif":        {func(c *config.Config) { c.FileMaxUploadBytes = -1 }, "FILE_MAX_UPLOAD_BYTES"},
		"an empty allow list":        {func(c *config.Config) { c.FileAllowedTypes = nil }, "FILE_ALLOWED_TYPES"},
		"an empty type":              {func(c *config.Config) { c.FileAllowedTypes = []string{"image/png", ""} }, "FILE_ALLOWED_TYPES"},
		"tip parametreli":            {func(c *config.Config) { c.FileAllowedTypes = []string{"text/plain; charset=utf-8"} }, "FILE_ALLOWED_TYPES"},
		"a type in upper case":       {func(c *config.Config) { c.FileAllowedTypes = []string{"Image/PNG"} }, "FILE_ALLOWED_TYPES"},
		"a type without a slash":     {func(c *config.Config) { c.FileAllowedTypes = []string{"png"} }, "FILE_ALLOWED_TYPES"},
		"tip iki kez":                {func(c *config.Config) { c.FileAllowedTypes = []string{"image/png", "image/png"} }, "FILE_ALLOWED_TYPES"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := base
			tt.boz(&cfg)

			err := cfg.Validate()

			require.Error(t, err, "a malformed file setting must not be accepted quietly")
			assert.Contains(t, err.Error(), tt.degisken,
				"the error message has to say which variable is wrong")
		})
	}
}

// TestANonDurableFileRootOpensTheWarningGate pins which installations the warning
// gate opens on.
//
// The rule DOES NOT STOP STARTUP (the reasoning is in the
// config.LocalFileRootIsDurable godoc), so this test is its only guard: if the gate
// closes quietly the warning is never written and a production deployment going
// out with a non-durable root leaves no trace at all.
//
// The TEMPORARY root cases are counted separately here because the criterion once
// looked only at filepath.IsAbs: "/tmp/gobit-uploads" is absolute, passes that
// criterion, and still empties every time the container restarts — that is, the
// silent data loss config REJECTS in its own default reasoning would come back
// without the warning ever being written.
func TestANonDurableFileRootOpensTheWarningGate(t *testing.T) {
	base := validConfig(t)

	tests := map[string]struct {
		environment string
		saglayici   string
		root        string
		kalici      bool
	}{
		"development, a relative root":          {"development", "local", "./data/uploads", false},
		"production, a relative root":           {"production", "local", "./data/uploads", false},
		"production, an absolute root":          {"production", "local", "/var/lib/gobit/uploads", true},
		"staging, a relative root":              {"staging", "local", "data/uploads", false},
		"production, a plugin store":            {"production", "s3", "./data/uploads", true},
		"production, a temporary root":          {"production", "local", "/tmp/gobit-uploads", false},
		"production, the temporary root itself": {"production", "local", "/tmp", false},
		"production, var/tmp":                   {"production", "local", "/var/tmp/gobit", false},
		"production, dev/shm":                   {"production", "local", "/dev/shm/gobit", false},
		// Prefix similarity is not enough: "/tmpfoo" is NOT UNDER the temporary
		// directory and a plain strings.HasPrefix comparison would count it as
		// non-durable unfairly.
		"production, a similarly named root": {"production", "local", "/tmpfoo/uploads", true},
		// While a plugin store is selected the root is never used; even a temporary
		// path must not produce a warning, or the warning fires at every startup while
		// guarding nothing.
		"production, a plugin store with a temporary root": {"production", "s3", "/tmp/gobit", true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := base
			cfg.AppEnv = tt.environment
			cfg.FileProvider = tt.saglayici
			cfg.FileRoot = tt.root

			assert.Equal(t, tt.kalici, cfg.LocalFileRootIsDurable())
		})
	}
}

// TestTheRateLimitKeyDoesNotFallToTheClientBehindAProxy pins which installations
// the warning gate opens on.
//
// While TRUSTED_PROXY_HOPS=0, X-Forwarded-For is never read and the rate limit key
// falls back to the connection's address; behind a reverse proxy that address is
// the proxy's ON EVERY REQUEST, that is, the quota becomes a single bucket for the
// whole store rather than per customer. Startup does NOT STOP (the reasoning is in
// the config.RateLimitKeyIsPerClient godoc), so this test is the gate's only guard.
func TestTheRateLimitKeyDoesNotFallToTheClientBehindAProxy(t *testing.T) {
	base := validConfig(t)

	tests := map[string]struct {
		limit     int
		atlama    int
		perClient bool
	}{
		"limit on, no hops":       {600, 0, false},
		"limit on, a single hop":  {600, 1, true},
		"limit on, two hops":      {600, 2, true},
		"limit off, no hops":      {0, 0, true},
		"limit negative, no hops": {-1, 0, true},
		"limit off, hops given":   {0, 2, true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := base
			cfg.RateLimitPerMinute = tt.limit
			cfg.TrustedProxyHops = tt.atlama

			assert.Equal(t, tt.perClient, cfg.RateLimitKeyIsPerClient())
		})
	}
}

// TestTheEventBusConsumerNameFormIsValidated pins the form gate of the consumer name.
//
// The empty value is VALID and means "produce the name yourself"; leading/trailing
// whitespace, on the other hand, is rejected. Redis accepts a name like " gobit-1"
// without complaint, that is, the typo produces no error at all — only, at the next
// startup, the process cannot find its own pending list and those messages are
// delivered to nobody.
func TestTheEventBusConsumerNameFormIsValidated(t *testing.T) {
	base := validConfig(t)

	tests := map[string]struct {
		name     string
		rejected bool
	}{
		"empty (an automatic name)": {name: ""},
		"a pod name":                {name: "gobit-0"},
		"a name with a dot":         {name: "gobit.eu.0"},
		"a leading space":           {name: " gobit-0", rejected: true},
		"a trailing space":          {name: "gobit-0 ", rejected: true},
		"a trailing newline":        {name: "gobit-0\n", rejected: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := base
			cfg.EventBusConsumer = tt.name

			err := cfg.Validate()

			if tt.rejected {
				require.Error(t, err, "a malformed consumer name must not be accepted quietly")
				assert.Contains(t, err.Error(), "EVENT_BUS_CONSUMER",
					"the error message has to say which variable is wrong")

				return
			}

			require.NoError(t, err)
		})
	}
}

// TestTheEventBusConsumerNameDefaultsToEmpty pins that the setting's default is EMPTY.
//
// Empty means "produce the name yourself, per process", and that is the only right
// default: a fixed default ("gobit", say) gives THE SAME name to every process in
// the group, that is, at every startup they also take the messages the others are
// processing and the same event is processed twice.
func TestTheEventBusConsumerNameDefaultsToEmpty(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Empty(t, cfg.EventBusConsumer,
		"a default consumer name would mean giving every instance the same name")
}

// TestTheGraphQLLimitsHaveFilledDefaults verifies that an installation with no
// settings given does not open the GraphQL endpoint UNBOUNDED.
//
// This is the only way the hardening could quietly disappear: without an
// environment variable the fields stay at Go's zero value, and had zero been read
// as "apply no limit" on the enforcing side, an unguarded endpoint would open
// without any error being visible.
func TestTheGraphQLLimitsHaveFilledDefaults(t *testing.T) {
	cfg := validConfig(t)

	assert.Equal(t, config.DefaultGraphQLMaxDepth, cfg.GraphQLMaxDepth)
	assert.Equal(t, config.DefaultGraphQLMaxComplexity, cfg.GraphQLMaxComplexity)
	assert.Equal(t, config.DefaultGraphQLIntrospection, cfg.GraphQLIntrospection,
		"the introspection default is a decision; it must not change quietly")
	assert.Positive(t, cfg.GraphQLMaxDepth)
	assert.Positive(t, cfg.GraphQLMaxComplexity)
}

// TestTheGraphQLLimitsAreValidatedAtStartup verifies that a meaningless limit
// stops the application AT STARTUP.
//
// Zero and negative values are deliberately rejected: a limit CAN BE RAISED, NOT
// REMOVED. It must not be confused with zero meaning "off" in
// RATE_LIMIT_PER_MINUTE — turning the rate limit off is a capacity choice, while
// turning the depth limit off is letting a single query consume the server.
//
// Non-numeric values already fail at parsing; they are exercised here too so that
// an "let an invalid value fall back to the default quietly" behavior is never
// added: that behavior would make the operator believe the value they gave is in force.
func TestTheGraphQLLimitsAreValidatedAtStartup(t *testing.T) {
	// expected checks that the error tells the operator WHICH setting is wrong.
	// Parsing errors come from the library and carry the field name rather than the
	// name of the environment variable; because both take the user to the right place,
	// the expected text is written per case.
	tests := map[string]struct{ key, value, expected string }{
		"a zero depth":                {"GRAPHQL_MAX_DEPTH", "0", "GRAPHQL_MAX_DEPTH"},
		"derinlik negatif":            {"GRAPHQL_MAX_DEPTH", "-1", "GRAPHQL_MAX_DEPTH"},
		"a non-numeric depth":         {"GRAPHQL_MAX_DEPTH", "deep", "GraphQLMaxDepth"},
		"a zero complexity":           {"GRAPHQL_MAX_COMPLEXITY", "0", "GRAPHQL_MAX_COMPLEXITY"},
		"a negative complexity":       {"GRAPHQL_MAX_COMPLEXITY", "-100", "GRAPHQL_MAX_COMPLEXITY"},
		"a non-numeric complexity":    {"GRAPHQL_MAX_COMPLEXITY", "lots", "GraphQLMaxComplexity"},
		"a non-boolean introspection": {"GRAPHQL_INTROSPECTION", "maybe", "GraphQLIntrospection"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := config.Load()
			require.Error(t, err, "an invalid value has to stop startup (%s=%s)", tt.key, tt.value)
			assert.Contains(t, err.Error(), tt.expected)
		})
	}
}

// TestTheGraphQLLimitsCanBeRaised verifies that the setting really is read.
//
// The validation tests alone are incomplete: a gate rejecting every value would
// pass them too. That a limit CAN BE RAISED requires the accepting side of the gate
// to be exercised as well.
func TestTheGraphQLLimitsCanBeRaised(t *testing.T) {
	clearEnv(t)

	t.Setenv("GRAPHQL_MAX_DEPTH", "25")
	t.Setenv("GRAPHQL_MAX_COMPLEXITY", "250000")
	t.Setenv("GRAPHQL_INTROSPECTION", "false")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, 25, cfg.GraphQLMaxDepth)
	assert.Equal(t, 250000, cfg.GraphQLMaxComplexity)
	assert.False(t, cfg.GraphQLIntrospection)
}

// TestThePoolLimitsHaveFilledDefaults verifies that an installation given no
// environment variable opens the pool UNCHANGED.
//
// The knob has to be backward compatible: before these two variables existed the
// process came up with 10/2, and the upgrade must not quietly grow the pool of an
// installation that never opens its .env. That the value stays the same as the db
// package's is bound separately by TestHavuzVarsayilanlariDbIleUyusuyor in internal/arch.
func TestThePoolLimitsHaveFilledDefaults(t *testing.T) {
	cfg := validConfig(t)

	assert.Equal(t, config.DefaultDBMaxConns, cfg.DBMaxConns)
	assert.Equal(t, config.DefaultDBMinConns, cfg.DBMinConns)
	assert.Equal(t, int32(10), cfg.DBMaxConns,
		"the default is a decision (see the measurement in the DBMaxConns godoc); it must not change quietly")
	assert.Equal(t, int32(2), cfg.DBMinConns)
}

// TestThePoolLimitsAreValidatedAtStartup verifies that a meaningless pool size
// stops the application AT STARTUP.
//
// A pool of zero connections does not mean "unlimited" but that NO query can run;
// the lower bound exceeding the upper one, in turn, hits pgxpool's own validation.
// Both stop at startup — but which environment variable is wrong can only be said
// by the gate here: the db package's error says "MinConns" and the operator has no
// lever by that name.
//
// A number out of range is exercised too: 2^31 fails AT PARSING, that is, the pool
// never opens at all. This case does not depend on the field's TYPE, and that it
// does not was measured — the env library bounds int at 32 bits as well, so this
// line stays green when the type is turned into int. Keeping it is still right; its
// claim is not "the type is int32" but "a number of meaningless size is not
// accepted quietly".
//
// # Why only the NAME of the environment variable is not expected
//
// In its first writing this table looked for "DB_MAX_CONNS" to appear inside the
// error, and in that form it MISSED a mutation: when the floor check was made "< 0"
// instead of "< 1", DB_MAX_CONNS=0 hits the third rule instead and, because that
// message carries both names at once, the test stayed green. That is, the name does
// NOT TELL the rules apart. The expected text is therefore the rule's own sentence,
// and two cases are set up with DB_MIN_CONNS=0: so the third rule is out of the way
// and the floor check is exercised on its own.
func TestThePoolLimitsAreValidatedAtStartup(t *testing.T) {
	tests := map[string]struct {
		env      map[string]string
		expected string
	}{
		"a zero maximum": {
			map[string]string{"DB_MAX_CONNS": "0", "DB_MIN_CONNS": "0"},
			"DB_MAX_CONNS has to be at least 1",
		},
		"azami negatif": {
			map[string]string{"DB_MAX_CONNS": "-1", "DB_MIN_CONNS": "0"},
			"DB_MAX_CONNS has to be at least 1",
		},
		"asgari negatif": {
			map[string]string{"DB_MIN_CONNS": "-1"},
			"DB_MIN_CONNS negatif olamaz",
		},
		"the minimum greater than the maximum": {
			map[string]string{"DB_MAX_CONNS": "4", "DB_MIN_CONNS": "5"},
			"DB_MIN_CONNS (5) cannot be greater than DB_MAX_CONNS (4)",
		},
		"a non-numeric maximum":  {map[string]string{"DB_MAX_CONNS": "lots"}, "DBMaxConns"},
		"a maximum out of range": {map[string]string{"DB_MAX_CONNS": "2147483648"}, "DBMaxConns"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := config.Load()
			require.Error(t, err, "an invalid pool size has to stop startup (%v)", tt.env)
			assert.Contains(t, err.Error(), tt.expected)
		})
	}
}

// TestThePoolLimitsCanBeTunedInBothDirections verifies that the knob really is
// read and that it turns in BOTH directions.
//
// A gate that only rejects would pass the same test as a gate rejecting every
// value; the accepting side has to be exercised too. The shrinking direction is
// exercised separately because that is the genuinely fragile one: what an
// installation connecting to a shared cluster with many instances needs is to
// NARROW the pool, and if the lower bound could not be overridden DB_MAX_CONNS=1
// would only be a value that breaks startup.
func TestThePoolLimitsCanBeTunedInBothDirections(t *testing.T) {
	t.Run("up", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DB_MAX_CONNS", "40")
		t.Setenv("DB_MIN_CONNS", "8")

		cfg, err := config.Load()
		require.NoError(t, err)

		assert.Equal(t, int32(40), cfg.DBMaxConns)
		assert.Equal(t, int32(8), cfg.DBMinConns)
	})

	t.Run("down", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DB_MAX_CONNS", "1")
		t.Setenv("DB_MIN_CONNS", "1")

		cfg, err := config.Load()
		require.NoError(t, err)

		assert.Equal(t, int32(1), cfg.DBMaxConns)
		assert.Equal(t, int32(1), cfg.DBMinConns)
	})
}
