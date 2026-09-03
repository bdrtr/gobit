package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"log/slog"

	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/core/query"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// baseConfig is the valid configuration the tests start from.
func baseConfig() config.Config {
	return config.Config{
		ServiceName:        "gobit-test",
		JWTTTL:             time.Hour,
		RateLimitPerMinute: 600,
		IdempotencyTTL:     time.Hour,
		GuardBackend:       "memory",
		RedisKeyPrefix:     config.DefaultRedisKeyPrefix,
	}
}

// guardedRouter builds a router carrying the guard stack produced from the
// given configuration.
//
// The Redis client is nil: these tests exercise the in-memory backend, the
// Redis path has its own tests in the redisguard package.
func guardedRouter(t *testing.T, cfg config.Config, authn corehttp.Authenticator) http.Handler {
	t.Helper()

	guards, err := guardStack(cfg, authn, &adminui.Ring{}, nil, discardLogger())
	require.NoError(t, err, "the guard stack could not be built")

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version:     "test",
		Middlewares: guards,
	})
	r.Get("/admin/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Post("/admin/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return r
}

// call sends the request to the router and returns the recorder.
func call(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))

	return rec
}

// TestGuardStackRejectsBeforeIdentityIsBound exercises the MOST DANGEROUS
// mistake a setup can make: the guard is attached but the authenticator was
// never bound.
//
// Were the surface left open in that state, an installation that forgot to
// register the auth module would serve the entire admin API without identity,
// and nothing would report it.
func TestGuardStackRejectsBeforeIdentityIsBound(t *testing.T) {
	t.Parallel()

	r := guardedRouter(t, baseConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusUnauthorized, call(r, http.MethodGet, "/admin/v1/users").Code)
	assert.Equal(t, http.StatusUnauthorized, call(r, http.MethodGet, "/store/v1/products").Code)
}

// TestGuardStackExemptsTheLoginEndpoint proves the login path is read from the
// auth module's CONSTANT and that the exemption is really applied.
//
// Had the exemption disappeared, nobody could sign in and the system would lock
// itself out.
func TestGuardStackExemptsTheLoginEndpoint(t *testing.T) {
	t.Parallel()

	r := guardedRouter(t, baseConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusOK, call(r, http.MethodPost, "/admin/v1/auth/login").Code,
		"the login endpoint must be reachable even while the authenticator is unbound")
}

// TestGuardStackLeavesTheHealthEndpointsAlone proves the orchestrator's path
// stays outside the stack.
func TestGuardStackLeavesTheHealthEndpointsAlone(t *testing.T) {
	t.Parallel()

	r := guardedRouter(t, baseConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusOK, call(r, http.MethodGet, "/health").Code)
}

// TestRateLimitCanBeTurnedOff proves a zero limit does NOT turn into "reject
// everything".
//
// Reading zero as "0 requests" would make an operator who wanted to turn the
// rate limit off shut down all traffic instead.
func TestRateLimitCanBeTurnedOff(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.RateLimitPerMinute = 0

	off, err := guardStack(cfg, &corehttp.DeferredAuthenticator{}, &adminui.Ring{}, nil, discardLogger())
	require.NoError(t, err)

	cfg.RateLimitPerMinute = 600
	on, err := guardStack(cfg, &corehttp.DeferredAuthenticator{}, &adminui.Ring{}, nil, discardLogger())
	require.NoError(t, err)

	assert.Less(t, len(off), len(on),
		"with the rate limit off there must be fewer rings in the stack")

	// Even with it off, the guard itself must hold.
	r := guardedRouter(t, cfg, &corehttp.DeferredAuthenticator{})
	assert.Equal(t, http.StatusUnauthorized, call(r, http.MethodGet, "/admin/v1/users").Code)
}

// TestSelectPluginsUsesTheCatalog proves the selection is made from the catalog
// and that an unknown name is not SILENTLY skipped.
func TestSelectPluginsUsesTheCatalog(t *testing.T) {
	t.Parallel()

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		registry, err := selectPlugins(nil)
		require.NoError(t, err)
		assert.Empty(t, registry.Plugins())
	})

	t.Run("recognized name", func(t *testing.T) {
		t.Parallel()

		registry, err := selectPlugins([]string{paymentstripe.Name})
		require.NoError(t, err)
		assert.Equal(t, []string{paymentstripe.Name}, registry.Plugins())
	})

	t.Run("unknown name", func(t *testing.T) {
		t.Parallel()

		_, err := selectPlugins([]string{"no-such-plugin"})
		require.Error(t, err, "an unknown plugin must not be skipped silently")
		assert.Contains(t, err.Error(), paymentstripe.Name,
			"the error message must list the recognized names so a typo becomes visible")
	})
}

// TestJWTSecretKeepsTheGivenSecret proves the secret from the configuration is
// used as it is.
func TestJWTSecretKeepsTheGivenSecret(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.JWTSecret = "a-hand-written-secret"

	assert.Equal(t, "a-hand-written-secret", jwtSecret(cfg, discardLogger()))
}

// TestJWTSecretIsRandomWhenAbsent proves a development startup without a secret
// does not fall back to a FIXED default.
//
// A fixed default would mean that in a configuration carried into production by
// accident, anyone could mint themselves a fully privileged admin token.
func TestJWTSecretIsRandomWhenAbsent(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()

	first := jwtSecret(cfg, discardLogger())
	second := jwtSecret(cfg, discardLogger())

	assert.NotEmpty(t, first)
	assert.NotEqual(t, first, second, "every startup must generate its own secret")
	assert.GreaterOrEqual(t, len(first), temporarySecretBytes,
		"the generated secret must carry as much entropy as the HS256 output length")
}

// TestPluginSettingsComeFromTheEnvironment proves plugin settings are read from
// the environment variables.
func TestPluginSettingsComeFromTheEnvironment(t *testing.T) {
	// t.Setenv cannot be used with a parallel test; this one runs serially on
	// purpose.
	t.Setenv("GOBIT_TEST_PLUGIN_SETTING", "value-42")

	settings := pluginSettings()

	assert.Equal(t, "value-42", settings["GOBIT_TEST_PLUGIN_SETTING"])
}

// discardLogger returns a logger that does not pollute the test output.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// recordCatcher is a slog handler that collects the records produced.
//
// The only job of a startup warning is to BE SEEN: it changes no behavior, so
// it cannot be exercised by any other assertion. A warning quietly deleted, or
// whose gate is built wrong, would fail no test — yet that warning is the only
// chance of noticing a setting that breaks an installation silently.
type recordCatcher struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled accepts every level; the test checks which level was chosen itself.
func (k *recordCatcher) Enabled(context.Context, slog.Level) bool { return true }

// Handle stores a copy of the record.
func (k *recordCatcher) Handle(_ context.Context, record slog.Record) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.records = append(k.records, record.Clone())

	return nil
}

// WithAttrs returns the same catcher; the tests read the attributes from the
// record.
func (k *recordCatcher) WithAttrs([]slog.Attr) slog.Handler { return k }

// WithGroup returns the same catcher.
func (k *recordCatcher) WithGroup(string) slog.Handler { return k }

// logger returns a logger writing into the catcher.
func (k *recordCatcher) logger() *slog.Logger { return slog.New(k) }

// messages returns the messages of the records at the given level.
func (k *recordCatcher) messages(level slog.Level) []string {
	k.mu.Lock()
	defer k.mu.Unlock()

	var found []string
	for i := range k.records {
		if k.records[i].Level == level {
			found = append(found, k.records[i].Message)
		}
	}

	return found
}

// attribute returns one attribute of the first record carrying the given
// message, as a string.
func (k *recordCatcher) attribute(message, name string) string {
	k.mu.Lock()
	defer k.mu.Unlock()

	for i := range k.records {
		if k.records[i].Message != message {
			continue
		}

		value := ""
		k.records[i].Attrs(func(a slog.Attr) bool {
			if a.Key == name {
				value = a.Value.String()

				return false
			}

			return true
		})

		return value
	}

	return ""
}

// fakeUsers stands in for the narrow auth surface in the seeding tests.
//
// A fake is used because the BEHAVIOR being exercised needs no database at
// all: the decision rests entirely on the answer to "how many users are there".
type fakeUsers struct {
	// count is the total user count ListUsers will return.
	count int64
	// listError is the error ListUsers will return.
	listError error
	// createError is the error CreateUser will return.
	createError error

	// listed records whether ListUsers was called at all.
	listed bool
	// created holds the inputs handed to CreateUser.
	created []authservice.CreateUserInput
	// passwords holds the passwords handed to CreateUser as a SEPARATE
	// parameter.
	passwords []string
}

var _ adminUsers = (*fakeUsers)(nil)

// ListUsers returns the page count and records that it was called.
func (s *fakeUsers) ListUsers(
	_ context.Context,
	_ authservice.ListUsersInput,
) (authservice.Page[authmodels.User], error) {
	s.listed = true
	if s.listError != nil {
		return authservice.Page[authmodels.User]{}, s.listError
	}

	return authservice.Page[authmodels.User]{Count: s.count, Limit: 1}, nil
}

// CreateUser records the input and the password and returns a fixed user.
func (s *fakeUsers) CreateUser(
	_ context.Context,
	in authservice.CreateUserInput,
	password string,
) (authmodels.User, error) {
	if s.createError != nil {
		return authmodels.User{}, s.createError
	}

	s.created = append(s.created, in)
	s.passwords = append(s.passwords, password)

	return authmodels.User{ID: "user_01TEST", Email: in.Email}, nil
}

// seededConfig returns a configuration with the seed enabled.
func seededConfig() config.Config {
	cfg := baseConfig()
	cfg.AdminBootstrapEmail = "first.admin@example.com"
	cfg.AdminBootstrapPassword = "a-long-enough-password"

	return cfg
}

// TestSeedAdminCreatesOnAnEmptyInstallation proves the seed's actual job: on a
// server opened against an empty database, the first administrator appears.
//
// Without this step a fresh installation is unusable — the admin endpoints are
// protected and there is no way to create the first administrator over HTTP.
func TestSeedAdminCreatesOnAnEmptyInstallation(t *testing.T) {
	t.Parallel()

	cfg := seededConfig()
	fake := &fakeUsers{count: 0}

	require.NoError(t, seedAdmin(context.Background(), fake, cfg, discardLogger()))

	require.Len(t, fake.created, 1, "exactly one administrator must be created on an empty installation")
	assert.Equal(t, cfg.AdminBootstrapEmail, fake.created[0].Email)
	assert.Equal(t, []string{cfg.AdminBootstrapPassword}, fake.passwords,
		"the password must pass as a SEPARATE parameter; it must not be put into the input struct")
	// A nil scope list means "full privileges" in the auth module. Had an empty
	// slice been passed, an account unable to reach any admin endpoint would be
	// born and the system would still be unusable.
	assert.Nil(t, fake.created[0].Scopes,
		"the first administrator must be fully privileged; no scope list may be given")
}

// TestSeedAdminSkipsWhenUsersExist proves restarting is safe.
//
// Had the seed touched an existing installation's privileges and password, an
// ADMIN_BOOTSTRAP_PASSWORD forgotten in an env file would silently roll back
// the production administrator's password on every restart.
func TestSeedAdminSkipsWhenUsersExist(t *testing.T) {
	t.Parallel()

	fake := &fakeUsers{count: 1}

	require.NoError(t, seedAdmin(context.Background(), fake, seededConfig(), discardLogger()))

	assert.True(t, fake.listed, "the decision to skip must be made from the count")
	assert.Empty(t, fake.created, "no new administrator may be written to an installation that has users")
}

// TestSeedAdminCreatesNothingWhenUnconfigured proves the seed is OPTIONAL: an
// INSTALLED system's environment need not carry these variables, and their
// absence must create no user.
//
// The count is READ anyway, and that is a deliberate change: the answer to "is
// this installed?" lives only there, and while it went unasked an installation
// with zero users opened in silence (see [reportUnmanageableInstallation]).
func TestSeedAdminCreatesNothingWhenUnconfigured(t *testing.T) {
	t.Parallel()

	fake := &fakeUsers{count: 3}

	require.NoError(t, seedAdmin(context.Background(), fake, baseConfig(), discardLogger()))

	assert.True(t, fake.listed, "the count is what tells us the installation is manageable")
	assert.Empty(t, fake.created, "no user may be created while the seed is off")
}

// TestUnmanageableInstallationStopsStartupInASharedEnvironment proves an
// installation with zero users and no seed does not open in SILENCE.
//
// In that installation the admin surface is fully protected apart from the
// login endpoint and there is no way to create the first user over HTTP; the
// storefront surface is closed as well, because the publishable key is minted
// by an admin endpoint. The server opens all the same, /health and /ready
// return green — so without a check the failure is invisible until the first
// sign-in attempt.
func TestUnmanageableInstallationStopsStartupInASharedEnvironment(t *testing.T) {
	t.Parallel()

	for _, env := range []string{"staging", "production"} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()

			cfg := baseConfig()
			cfg.AppEnv = env
			fake := &fakeUsers{count: 0}

			err := seedAdmin(context.Background(), fake, cfg, discardLogger())

			require.Error(t, err, "an unmanageable installation must not open in silence")
			assert.Equal(t, "admin_bootstrap_required", errors.CodeOf(err),
				"the error must carry its own code naming the setting that is missing")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_EMAIL",
				"the message must tell the operator which variables to provide")
		})
	}
}

// TestUnmanageableInstallationOpensInDevelopment proves the same state does NOT
// stop startup in local development.
//
// The repository's promise is "make up && make run works even without a .env",
// and a developer opening for the first time against a fresh database lands
// exactly in this state. The distinction is the same as JWT_SECRET's: a warning
// in development, a refusal in a shared environment.
func TestUnmanageableInstallationOpensInDevelopment(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.AppEnv = "development"
	fake := &fakeUsers{count: 0}

	require.NoError(t, seedAdmin(context.Background(), fake, cfg, discardLogger()),
		"local development must be able to open without extra settings")
	assert.Empty(t, fake.created)
}

// TestSeedAdminPropagatesTheError proves a seeding failure is not passed over in
// SILENCE.
//
// An administrator that could not be created means a system with no admin
// surface; had the error been swallowed, the server would open but accept no
// admin request and the failure would only be seen at the first sign-in
// attempt.
func TestSeedAdminPropagatesTheError(t *testing.T) {
	t.Parallel()

	// A CONFLICT is NOT here, and that is deliberate: that case is not a
	// failure but a concurrent-startup race and is exercised separately (see
	// [TestSeedAdminSwallowsTheConcurrentRace]).
	tests := map[string]*fakeUsers{
		"the count cannot be read":   {listError: errors.Unavailable("db_down", "there is no database")},
		"the user cannot be written": {createError: errors.Unavailable("db_down", "there is no database")},
	}

	for name, fake := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := seedAdmin(context.Background(), fake, seededConfig(), discardLogger())

			require.Error(t, err, "a seeding failure must stop startup")
			assert.Equal(t, "admin_bootstrap_failed", errors.CodeOf(err),
				"the error must be wrapped with a code belonging to the seeding step")
		})
	}
}

// TestRateLimitOffIsReportedInASharedEnvironment proves the state where the
// limiter is NOT built at all is reported.
//
// RATE_LIMIT_PER_MINUTE <= 0 is a legitimate choice (in ADR 0007 zero means
// "off") but when it leaves not one line of trace it is indistinguishable from
// a zero typed by accident: no endpoint has a quota, the login endpoint
// included.
func TestRateLimitOffIsReportedInASharedEnvironment(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.AppEnv = "production"
	cfg.RateLimitPerMinute = 0
	catcher := &recordCatcher{}

	_, err := guardStack(cfg, validIdentity{}, &adminui.Ring{}, nil, catcher.logger())

	require.NoError(t, err)
	assert.Contains(t, catcher.messages(slog.LevelWarn), "the rate limiter was NOT ATTACHED",
		"a rate limit that is off must not stay silent in a shared environment")
}

// TestRateLimitWarnsAboutTheKeyBehindAProxy proves the state where the quota
// does not fall per client is reported.
//
// With TRUSTED_PROXY_HOPS=0, X-Forwarded-For is never read and the key falls on
// the connection's address; behind a reverse proxy that address is the proxy's
// on every request, so the quota is a single bucket for the whole store.
func TestRateLimitWarnsAboutTheKeyBehindAProxy(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.AppEnv = "production"
	cfg.RateLimitPerMinute = 600
	cfg.TrustedProxyHops = 0
	catcher := &recordCatcher{}

	_, err := guardStack(cfg, validIdentity{}, &adminui.Ring{}, nil, catcher.logger())

	require.NoError(t, err, "the warning must NOT stop startup: zero hops is the right "+
		"answer for an installation facing the internet directly")
	assert.Contains(t, catcher.messages(slog.LevelWarn),
		"the rate limit key falls on the CONNECTION, not the client")
}

// TestRateLimitIsSilentWhenSetUpCorrectly proves the warning is not NOISE.
//
// A warning printed on every startup drowns a real warning; the states where
// the gate stays shut are therefore pinned separately.
func TestRateLimitIsSilentWhenSetUpCorrectly(t *testing.T) {
	t.Parallel()

	tests := map[string]func(c *config.Config){
		"proxy hops given": func(c *config.Config) {
			c.AppEnv = "production"
			c.TrustedProxyHops = 1
		},
		"local development, limit off": func(c *config.Config) {
			c.AppEnv = "development"
			c.RateLimitPerMinute = 0
		},
		"local development, no proxy": func(c *config.Config) {
			c.AppEnv = "development"
			c.TrustedProxyHops = 0
		},
	}

	for name, apply := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := baseConfig()
			apply(&cfg)
			catcher := &recordCatcher{}

			_, err := guardStack(cfg, validIdentity{}, &adminui.Ring{}, nil, catcher.logger())

			require.NoError(t, err)
			// The in-memory guard warning is OUTSIDE this gate and keeps being
			// printed in a shared environment; only the two rate-limit
			// messages are looked for here.
			warnings := catcher.messages(slog.LevelWarn)
			assert.NotContains(t, warnings, "the rate limiter was NOT ATTACHED",
				"no warning may be printed for a correctly configured rate limit")
			assert.NotContains(t, warnings, "the rate limit key falls on the CONNECTION, not the client",
				"no warning may be printed for a correctly configured rate limit")
		})
	}
}

// TestEventBusTakesTheNamespaceFromTheKeyPrefix proves the Redis bus is NOT
// built with a zero-valued configuration.
//
// A zero-valued RedisConfig drops both the stream prefix and the consumer group
// to the package default, and REDIS_KEY_PREFIX NEVER reaches the event side:
// the guard keys of two installations sharing the same Redis are separated,
// their events are not. The heaviest consequence is the shared group — only one
// of the two installations receives a given event.
//
// The assertion goes through the LOG because the configuration built stays
// behind the [eventbus.EventBus] interface; the log line itself is necessary
// too (see [eventbus.ConsumerName]).
func TestEventBusTakesTheNamespaceFromTheKeyPrefix(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EventBus = config.BackendRedis
	cfg.RedisKeyPrefix = "gobit-staging"
	cfg.EventBusConsumer = "gobit-0"
	catcher := &recordCatcher{}

	bus, err := setupEventBus(context.Background(), cfg, unconnectedRedis(), catcher.logger())

	require.NoError(t, err)
	require.NotNil(t, bus)

	const message = "event bus: Redis Streams"
	assert.Equal(t, "gobit-staging:events", catcher.attribute(message, "stream_prefix"))
	assert.Equal(t, "gobit-staging", catcher.attribute(message, "group"),
		"the consumer group must be separated too; otherwise only one of the two installations gets an event")
	assert.Equal(t, "gobit-0", catcher.attribute(message, "consumer"))
}

// TestEventBusGeneratesAConsumerNameWhenNoneIsGiven proves the resolved
// consumer name is logged in every case.
//
// Giving two processes the same name silently causes double processing and
// validation cannot see it; putting two startup logs side by side is the only
// chance of noticing. Had the name not been logged, that chance would be gone
// too.
func TestEventBusGeneratesAConsumerNameWhenNoneIsGiven(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EventBus = config.BackendRedis
	catcher := &recordCatcher{}

	_, err := setupEventBus(context.Background(), cfg, unconnectedRedis(), catcher.logger())

	require.NoError(t, err)
	assert.NotEmpty(t, catcher.attribute("event bus: Redis Streams", "consumer"),
		"if the consumer name is not logged, two processes using the same name can never be seen")
}

// TestInMemoryEventBusWarnsInASharedEnvironment proves the non-durable bus does
// not stay silent in a shared environment.
//
// Its cost is in the same class as GUARD_BACKEND=memory's and that one already
// warned; logging the two at different levels would have meant the same
// trade-off being visible in one and invisible in the other.
func TestInMemoryEventBusWarnsInASharedEnvironment(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env   string
		level slog.Level
	}{
		"production":        {"production", slog.LevelWarn},
		"staging":           {"staging", slog.LevelWarn},
		"local development": {"development", slog.LevelInfo},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := baseConfig()
			cfg.AppEnv = tt.env
			cfg.EventBus = "inmemory"
			catcher := &recordCatcher{}

			_, err := setupEventBus(context.Background(), cfg, nil, catcher.logger())

			require.NoError(t, err)
			assert.Contains(t, catcher.messages(tt.level), "event bus: in-memory (single process)")
		})
	}
}

// unconnectedRedis produces a Redis client that does not connect but is not nil
// either.
//
// go-redis establishes the connection on the first command; the redisguard
// constructors only validate settings and run no command at all. That makes the
// SETUP logic of the Redis path testable without Docker.
func unconnectedRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
}

// TestGuardStackPassesTheKeyPrefixToTheConstructors proves the prefix from the
// configuration REALLY reaches the redisguard constructors.
//
// If the constructors are not given the prefix nothing blows up: every
// installation silently keeps writing into the same namespace, so two
// installations sharing one Redis spend each other's rate-limit quota and read
// each other's idempotency records. The test catches this by giving a prefix
// the constructors will REJECT: if no error comes back, the prefix was lost on
// the way.
//
// config.Validate already enforces the same format; the repetition here is not
// pointless, because the stack can also be called by callers that build Config
// by hand (tests, embedding installations).
func TestGuardStackPassesTheKeyPrefixToTheConstructors(t *testing.T) {
	t.Parallel()

	for name, prefix := range map[string]string{
		"empty prefix":         "",
		"containing separator": "gobit:staging",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := baseConfig()
			cfg.GuardBackend = config.BackendRedis
			cfg.RedisKeyPrefix = prefix

			_, err := guardStack(cfg, &corehttp.DeferredAuthenticator{}, &adminui.Ring{}, unconnectedRedis(), discardLogger())

			require.Error(t, err, "an invalid prefix must reach the constructor and stop startup")
			assert.Equal(t, "redisguard_invalid_config", errors.CodeOf(err),
				"the error must come from the redisguard constructor")
		})
	}
}

// TestGuardStackBuildsTheRedisBackend proves that with a valid prefix the Redis
// path is built all the way through.
//
// Exercising only the error path would be misleading: a constructor that
// rejected every prefix would pass that test too.
func TestGuardStackBuildsTheRedisBackend(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.GuardBackend = config.BackendRedis
	cfg.RedisKeyPrefix = "gobit-staging"

	guards, err := guardStack(cfg, &corehttp.DeferredAuthenticator{}, &adminui.Ring{}, unconnectedRedis(), discardLogger())

	require.NoError(t, err, "the redis backend must be buildable with a valid prefix")
	assert.NotEmpty(t, guards, "the guard stack must not come back empty")
}

// TestGuardStackStopsWhenRedisIsChosenWithoutAClient proves a half
// configuration does not silently fall back to the in-memory store.
//
// Had it done so, the operator would believe they asked for a shared
// idempotency store while every instance keeps its own record and two requests
// with the same key are processed twice — that is, two orders. This would only
// be seen in production and under load.
func TestGuardStackStopsWhenRedisIsChosenWithoutAClient(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.GuardBackend = "redis"

	_, err := guardStack(cfg, &corehttp.DeferredAuthenticator{}, &adminui.Ring{}, nil, discardLogger())

	require.Error(t, err, "the redis backend must not be built without a Redis client")
	assert.Contains(t, err.Error(), "GUARD_BACKEND")
}

// TestSeedAdminSwallowsTheConcurrentRace proves that several instances opening
// against an empty database AT THE SAME TIME does not bring startup down.
//
// The race is real and has been observed with a real server: when three
// instances open concurrently all three see "no users", all three try to
// create one, and email uniqueness rejects two. Treating the conflict as an
// error would mean two of three replicas entering a restart loop on the first
// deployment.
//
// What the test exercises is not "is the error swallowed" but that the DESIRED
// END STATE holds: there is an administrator for the losing instance too.
func TestSeedAdminSwallowsTheConcurrentRace(t *testing.T) {
	t.Parallel()

	fake := &fakeUsers{
		count: 0,
		createError: errors.Conflict("auth_email_taken",
			"the email %q is already in use", "first.admin@example.com"),
	}

	err := seedAdmin(context.Background(), fake, seededConfig(), discardLogger())

	assert.NoError(t, err, "a concurrent seeding conflict must NOT bring startup down")
}

// TestSeedAdminDoesNotSwallowOtherErrors proves the swallowing is specific to a
// CONFLICT.
//
// For a connection error or an invalid password the desired end state does NOT
// hold: there is no administrator and the system is unusable. Swallowing those
// too would mean silently presenting an installation without an administrator
// as healthy.
func TestSeedAdminDoesNotSwallowOtherErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]error{
		"invalid input": errors.Invalid("auth_weak_password", "the password is too short"),
		"subsystem":     errors.Unavailable("db_unreachable", "the database could not be reached"),
		"internal":      errors.Internal("unexpected", "an unexpected error"),
	}

	for name, failure := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeUsers{count: 0, createError: failure}

			err := seedAdmin(context.Background(), fake, seededConfig(), discardLogger())

			require.Error(t, err, "an error other than a conflict must stop startup")
			assert.Contains(t, err.Error(), "the first administrator could not be created")
		})
	}
}

// fakeNotificationRegistry is the test counterpart of the notification provider
// registry.
//
// Building the real registry (notification/service.ProviderRegistry) was
// possible too, but it would have been wrong: what is under examination is the
// SETUP's behavior — how it meets an unknown name — and that behavior is
// independent of the registry's concrete type. The fake satisfies the setup's
// narrow interface.
type fakeNotificationRegistry struct {
	ids []string
}

func (s *fakeNotificationRegistry) Get(id string) (coreprovider.NotificationProvider, error) {
	if slices.Contains(s.ids, id) {
		return nil, nil //nolint:nilnil // the setup only looks at the ERROR, it does not use the provider
	}

	return nil, errors.NotFound("notification_provider_not_found",
		"the notification provider %q is not registered", id)
}

func (s *fakeNotificationRegistry) IDs() []string { return s.ids }

// notificationContainer produces a container filled with the given registry.
func notificationContainer(t *testing.T, registry *fakeNotificationRegistry) *container.Container {
	t.Helper()

	c := container.New(discardLogger())
	require.NoError(t, c.Provide(notification.ProvidersName, registry))

	return c
}

// TestNotificationProviderRejectsAnUnknownName proves a misconfigured
// installation does NOT open.
//
// Silently falling back to the default ("log") was the worst option: the
// installation opens, no error appears, and order confirmations are only
// written to the log — the failure is noticed while customers are waiting for
// confirmations, usually days later.
func TestNotificationProviderRejectsAnUnknownName(t *testing.T) {
	t.Parallel()

	c := notificationContainer(t, &fakeNotificationRegistry{ids: []string{"log"}})

	err := verifyNotificationProvider(c, "sendgrid")

	require.Error(t, err, "an unknown provider name must STOP startup")
	assert.Equal(t, codeUnknownNotificationProvider, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "sendgrid", "the rejected name must be written out")
	assert.Contains(t, err.Error(), "log", "the registered names must be written out so a typo becomes visible")
	assert.Contains(t, err.Error(), "PLUGINS",
		"an operator who forgot a name coming from a plugin must be pointed the right way")
}

// TestNotificationProviderAcceptsARegisteredName proves a name coming from a
// plugin passes.
//
// The check runs AFTER Start; an earlier gate would reject a VALID name coming
// from a plugin as "unknown".
func TestNotificationProviderAcceptsARegisteredName(t *testing.T) {
	t.Parallel()

	c := notificationContainer(t, &fakeNotificationRegistry{ids: []string{"log", "sendgrid"}})

	assert.NoError(t, verifyNotificationProvider(c, "sendgrid"))
	assert.NoError(t, verifyNotificationProvider(c, config.DefaultNotificationProvider))
}

// TestNotificationProviderStopsWhenTheRegistryIsAbsent proves startup does not
// silently continue when the notification module was never installed.
//
// Without the registry no notification can be sent at all; opening with a
// configuration that says "I chose a provider" would be presenting a capability
// that does not exist.
func TestNotificationProviderStopsWhenTheRegistryIsAbsent(t *testing.T) {
	t.Parallel()

	err := verifyNotificationProvider(container.New(discardLogger()), "log")

	require.Error(t, err)
	assert.Equal(t, codeUnknownNotificationProvider, errors.CodeOf(err))
	assert.Contains(t, err.Error(), notification.ProvidersName)
}

// validIdentity is an authenticator that accepts every request.
//
// The other tests in this file work with an unbound authenticator because their
// assertions are about REJECTION; reaching the idempotency ring, however,
// requires identity to PASS (the ring comes after identity).
type validIdentity struct{}

// AuthenticateAdmin returns a fixed admin principal.
func (validIdentity) AuthenticateAdmin(_ context.Context, _, _ string) (corehttp.Principal, error) {
	return corehttp.Principal{ID: "usr_1", Kind: "user"}, nil
}

// AuthenticateStore returns a fixed store principal.
func (validIdentity) AuthenticateStore(_ context.Context, _ string) (corehttp.Principal, error) {
	return corehttp.Principal{ID: "pk_1", Kind: "api_key"}, nil
}

// TestGuardStackExemptsCartCreationFromIdempotency proves the composition root
// really carries the exemption, in the REAL stack.
//
// Its own e2e suite cannot prove this: that suite builds its own guard options,
// so deleting the line from the composition root leaves every e2e test green.
// This test is what makes the line load-bearing.
//
// What the exemption prevents is a cross-shopper leak rather than a waste. The
// idempotency namespace is the caller's Principal, and on the storefront the
// Principal is the publishable key — the STORE's identity, the same for every
// shopper. Cart creation is the one storefront POST whose path carries no
// capability and whose response creates one, so two shoppers sending the same
// client-chosen key and the same body were handed the SAME cart id.
//
// The handler below therefore answers with a DIFFERENT body each time: with the
// endpoint recorded, the second caller would be given the first one's answer,
// which is exactly the leak.
func TestGuardStackExemptsCartCreationFromIdempotency(t *testing.T) {
	t.Parallel()

	guards, err := guardStack(baseConfig(), validIdentity{}, &adminui.Ring{}, nil, discardLogger())
	require.NoError(t, err)

	created := 0

	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "test", Middlewares: guards})
	r.Post(cartapi.StoreCartsPath, func(w http.ResponseWriter, _ *http.Request) {
		created++
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"data":{"id":"cart_%d"}}`, created)
	})

	makeRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, cartapi.StoreCartsPath,
			strings.NewReader(`{"country_code":"tr"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(corehttp.PublishableKeyHeader, "pk_test")
		req.Header.Set(corehttp.IdempotencyKeyHeader, "the-same-key")

		return req
	}

	first := httptest.NewRecorder()
	r.ServeHTTP(first, makeRequest())
	require.Equal(t, http.StatusCreated, first.Code)

	second := httptest.NewRecorder()
	r.ServeHTTP(second, makeRequest())

	require.Equal(t, http.StatusCreated, second.Code)
	assert.Empty(t, second.Header().Get(corehttp.IdempotencyReplayedHeader),
		"cart creation is not recorded, so it cannot be replayed")
	assert.Contains(t, second.Body.String(), `"cart_2"`,
		"the second shopper must get their OWN cart, not the first shopper's")
	assert.Equal(t, 2, created, "both requests must reach the handler")
}

// TestGuardStackExemptsTheGraphQLEndpointFromIdempotency proves the exemption
// is applied in the REAL stack and read from the module's CONSTANT.
//
// The exemption belongs here because the core cannot know the path (it may not
// import the modules); the path passes through the composition root, and had it
// been a hand-written string the exemption would silently fall away the day
// graph.Path changed — the GraphQL endpoint would start being recorded again
// and nobody would notice.
//
// The handler imitates the measured behavior of the GraphQL contract: it
// returns 200 even on an internal error. Idempotency's "5xx is not recorded"
// protection therefore never engages here; without the exemption a transient
// failure would be replayed for the whole IDEMPOTENCY_TTL.
func TestGuardStackExemptsTheGraphQLEndpointFromIdempotency(t *testing.T) {
	t.Parallel()

	guards, err := guardStack(baseConfig(), validIdentity{}, &adminui.Ring{}, nil, discardLogger())
	require.NoError(t, err)

	failing := true

	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "test", Middlewares: guards})
	r.Post(graph.Path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)

		if failing {
			_, _ = w.Write([]byte(`{"errors":[{"message":"an unexpected server error occurred"}]}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":{"products":{"count":42}}}`))
	})

	makeRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, graph.Path,
			strings.NewReader(`{"query":"{ products { count } }"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(corehttp.PublishableKeyHeader, "pk_test")
		req.Header.Set(corehttp.IdempotencyKeyHeader, "the-same-key")

		return req
	}

	first := httptest.NewRecorder()
	r.ServeHTTP(first, makeRequest())
	require.Equal(t, http.StatusOK, first.Code)
	require.Contains(t, first.Body.String(), "server error")

	failing = false

	second := httptest.NewRecorder()
	r.ServeHTTP(second, makeRequest())

	assert.Empty(t, second.Header().Get(corehttp.IdempotencyReplayedHeader),
		"the GraphQL endpoint is not recorded, so it cannot be replayed")
	assert.Contains(t, second.Body.String(), `"count":42`,
		"once the failure is fixed the client must get the CURRENT response")
}

// panelIdentity accepts exactly one token and rejects everything else.
//
// [validIdentity] accepts every request, which is the right fake for the tests
// whose subject is a ring further down the stack. It is the WRONG fake here:
// the claim below is that a request WITHOUT a header is refused, and an
// authenticator that says yes to everything would make that claim pass no
// matter how the guard behaved.
type panelIdentity struct{ token string }

// AuthenticateAdmin accepts only the Bearer scheme and the known token.
func (p panelIdentity) AuthenticateAdmin(_ context.Context, scheme, token string) (corehttp.Principal, error) {
	if scheme != corehttp.SchemeBearer || token != p.token {
		return corehttp.Principal{}, errors.Unauthorized("auth_invalid_token", "invalid token")
	}

	return corehttp.Principal{ID: "usr_panel", Kind: "user"}, nil
}

// AuthenticateStore is never reached by these tests.
func (p panelIdentity) AuthenticateStore(_ context.Context, _ string) (corehttp.Principal, error) {
	return corehttp.Principal{}, errors.Unauthorized("auth_invalid_key", "invalid key")
}

// panelCatalog is the panel's read surface; the tests below never read.
type panelCatalog struct{}

func (panelCatalog) Graph(context.Context, query.GraphSpec) ([]query.Record, error) {
	return nil, nil
}

// panelSession is the panel's identity surface.
//
// It accepts ONE pair of credentials and returns the same token the
// authenticator recognizes, so the sign-in path can be exercised end to end
// through the real guard stack.
type panelSession struct {
	email    string
	password string
	token    string
}

func (p panelSession) Login(_ context.Context, email, password string) (string, time.Time, error) {
	if email != p.email || password != p.password {
		return "", time.Time{}, errors.Unauthorized("auth_invalid_credentials", "invalid credentials")
	}

	return p.token, time.Now().Add(time.Hour), nil
}

func (p panelSession) Logout(context.Context, string, string) (time.Time, error) {
	return time.Time{}, nil
}

// TestPanelCookieIsNotAcceptedByTheAdminAPI is the load-bearing claim of ADR
// 0011 and the reason the panel has its own tree at all.
//
// The admin API's CSRF immunity does not come from a defense. It comes from the
// token living in a header the browser never attaches BY ITSELF: a form posted
// from another site carries the victim's cookies but cannot set an
// Authorization header. The moment the panel's session cookie were also
// accepted at /admin/v1, that property would be gone and EVERY admin endpoint —
// every write, every deletion — would enter a new attack surface, with nothing
// failing to announce it.
//
// The claim is exercised on the REAL stack, not on a hand-built middleware
// chain, because what is being asserted is a property of the SCOPING: guard
// scope matches on a segment boundary, /admin/ui is not under /admin/v1, and
// the panel's identity ring is attached only to the panel prefix. A test that
// rebuilt the chain would be asserting its own copy.
//
// The third case is what makes the first two mean anything. Without it a 401
// from the API prefix would also be satisfied by a cookie that simply never
// worked anywhere, and the test would keep passing after the panel had stopped
// authenticating altogether.
func TestPanelCookieIsNotAcceptedByTheAdminAPI(t *testing.T) {
	t.Parallel()

	const token = "a-valid-admin-token"

	identity := panelIdentity{token: token}

	c := container.New(discardLogger())
	require.NoError(t, c.Provide(adminui.ServiceQuery, panelCatalog{}))
	require.NoError(t, c.Provide(adminui.ServiceAuth, panelSession{
		email:    "operator@example.com",
		password: "a-long-enough-password",
		token:    token,
	}))
	require.NoError(t, c.Provide(adminui.InteropAuth, identity))

	panel, err := adminui.FromContainer(c, false)
	require.NoError(t, err)

	ring := &adminui.Ring{}
	ring.Bind(panel)

	guards, err := guardStack(baseConfig(), identity, ring, nil, discardLogger())
	require.NoError(t, err)

	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "test", Middlewares: guards})
	r.Get("/admin/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	panel.Routes(r)

	withCookie := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.AddCookie(&http.Cookie{Name: adminui.CookieName, Value: token})

		return req
	}

	t.Run("the cookie does not open the admin API", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, withCookie("/admin/v1/users"))

		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"the admin API must read ONLY the Authorization header; the moment it accepts the "+
				"panel cookie, its CSRF immunity is gone")
	})

	t.Run("the header opens the admin API", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code,
			"the same token must still work through the header; otherwise the case above proves "+
				"nothing but a broken token")
	})

	t.Run("signing in needs no identity of its own", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodPost, adminui.LoginPath,
			strings.NewReader("email=operator@example.com&password=a-long-enough-password"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://"+req.Host)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// This is what the exemption on the identity ring costs when it is
		// dropped: the ring would demand a cookie from the very request that is
		// about to establish one, and NOBODY could ever sign in. The failure
		// would not look like a bug — the login page would simply come back
		// again, with a 401, forever.
		require.Equal(t, http.StatusSeeOther, rec.Code,
			"a correct sign-in must be able to pass the identity ring without a cookie")
		assert.Equal(t, adminui.URLPrefix, rec.Header().Get("Location"))

		cookies := rec.Result().Cookies()
		var session *http.Cookie
		for _, ck := range cookies {
			if ck.Name == adminui.CookieName {
				session = ck
			}
		}
		require.NotNil(t, session, "sign-in must write the session cookie")
		assert.Equal(t, adminui.URLPrefix, session.Path,
			"the cookie's path must be pinned to the panel tree; that pin is what keeps the admin "+
				"API's CSRF immunity intact")
	})

	t.Run("the panel is closed without the cookie", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, adminui.URLPrefix, http.NoBody))

		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"the panel prefix is in OpenPrefixes so it enters the core's stack for the QUOTA, not "+
				"for identity; if its own ring is not attached the tree opens with no identity at all")
	})

	t.Run("a cross-site form submission is rejected", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodPost, adminui.LoginPath,
			strings.NewReader("email=a@example.com&password=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://another.example")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code,
			"CSRF's second layer must be attached AT THE COMPOSITION ROOT. That the middleware "+
				"itself works is proven in the adminui package; what is proven here is that it is "+
				"WIRED — and an unwired ring fails nothing on its own")
	})

	t.Run("a cross-site product edit is rejected", func(t *testing.T) {
		t.Parallel()

		// The login form is not the only state-changing path any more. This
		// case exists because the origin ring is scoped to a PREFIX, not to a
		// list of paths: a new write route added under the panel prefix is
		// protected automatically, and this assertion is what proves that
		// sentence rather than assuming it.
		req := httptest.NewRequest(http.MethodPost,
			adminui.ProductsPath+"/prod_1/edit",
			strings.NewReader("title=Coffee&handle=coffee&status=draft"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://another.example")
		req.AddCookie(&http.Cookie{Name: adminui.CookieName, Value: token})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code,
			"a write path under the panel prefix must be covered by the origin ring even "+
				"though nobody added it to a list")
	})

	t.Run("a same-origin form submission passes the origin ring", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodPost, adminui.LoginPath,
			strings.NewReader("email=a@example.com&password=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://"+req.Host)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// The credentials are wrong, so the login page comes back with a 401.
		// The point is the 403 above NOT appearing: a ring that rejected
		// everything would pass the previous case just as well.
		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"a request from the panel's own origin must pass the origin ring and reach the "+
				"identity service")
	})

	t.Run("the cookie opens the panel", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, withCookie(adminui.URLPrefix))

		// The panel's entry point redirects to the catalog, so passing the
		// guard shows up as a 303 rather than a 200. The Location is asserted
		// as well: a 303 to somewhere else would mean the guard passed and the
		// routing did not, and only the status would still look right.
		require.Equal(t, http.StatusSeeOther, rec.Code,
			"the cookie must work inside the panel tree; without this the 401 above could just as "+
				"well come from a cookie that never worked at all")
		assert.Equal(t, adminui.ProductsPath, rec.Header().Get("Location"))
	})
}
