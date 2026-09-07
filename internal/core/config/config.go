// Package config loads and validates all of the application's settings from
// environment variables, following the 12-factor principle.
package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

// minJWTSecretLen is the shortest signing secret accepted in shared environments.
//
// For HS256 the secret has to carry as much entropy as the output length (32
// bytes); anything shorter can be found by brute force.
const minJWTSecretLen = 32

// MaxSharedJWTTTL is the longest admin session accepted in shared environments.
//
// ADR 0031 ratified a session that ends at a wall-clock deadline and never
// renews. A lifetime is therefore the whole of the exposure: there is no
// refresh to shorten, and no revocation short of a logout or a password change,
// which the identity layer's session anchor does enforce but which nobody
// performs for a token they do not know was taken.
//
// Twenty-four hours is twice the default, and the doubling is where the line
// is drawn rather than at a round number: twelve hours was chosen so that "a
// token taken from a machine at the end of a day is dead by the next one", and
// a day is the last value that still keeps that sentence true. A week-long or
// month-long admin session on a shared deployment is not a preference, it is a
// configuration mistake the framework can see from here, and refusing it at
// boot is cheaper than discovering it in an incident.
//
// It is a SHARED-environment rule only, following the precedent set a few lines
// above it: local development is the one place where the framework's secret and
// TLS requirements are relaxed, and a developer who wants a session that
// outlasts a week of debugging is not creating the risk this bound exists for.
const MaxSharedJWTTTL = 24 * time.Hour

// devAppEnv is the only environment where the secret and TLS requirements are RELAXED.
const devAppEnv = "development"

// MinIdempotencyMemoryBytes is the smallest value accepted for
// [Config.IdempotencyMaxMemoryBytes].
//
// What is rejected is not a small number but a SILENTLY USELESS configuration: if
// the budget cannot carry a single maximum-size response, that response exceeds
// the budget the moment it is written and is dropped right away — the guard has
// closed for large responses without giving any error.
//
// The limit is therefore not EQUAL to the maximum body corehttp buffers (1 MiB)
// but TWICE it. Were it equal, the limit itself would be exactly the situation it
// forbids, and this was measured: writing a 1 MiB response into a 1 MiB budget
// makes the store drop the record at once (the charge goes to 0, the record
// cannot be replayed), because a record's price carries, beside the body, its key
// (up to 255 characters), its fingerprint, its headers and its structural cost.
// Twice is the first round value covering that whole ledger; that it fits is
// NAILED DOWN BY A BEHAVIOR TEST (TestBudgetDefaultAgreesWithConfiguration in
// corehttp).
//
// The value is not imported from corehttp: config does NOT depend on any transport
// layer, and forming that link would mean tying the settings to HTTP. That the two
// constants move together is pinned by the test on the corehttp side.
const MinIdempotencyMemoryBytes int64 = 2 << 20

// BackendRedis is the name of the shared Redis backend.
//
// Both [Config.EventBus] and [Config.GuardBackend] can take this value and the two
// SHARE the same client (see [Config.NeedsRedis]). Reading the name from a single
// constant keeps the two fields from quietly drifting into different spellings.
const BackendRedis = "redis"

// MinBootstrapPasswordLen is the shortest first administrator password accepted in
// shared environments.
//
// 16 is ABOVE the floor of 12 the auth module applies to EVERY administrator
// (MinPasswordLen in the service package), and that is deliberate: the value here
// is not a user password but a DEPLOYMENT SECRET. It sits in an environment file
// or in a secret store, is written once and nobody has to memorize it — that is,
// the length costs the user nothing while in return the search space of the
// system's first and most privileged account grows.
//
// The number is not copied from the auth module, it is chosen INDEPENDENTLY of it;
// because the core does not know modules (Principle 2.4), binding to the constant
// is not possible anyway. Its only requirement is not to fall below auth's floor:
// were it to, config would accept a password the seed step rejects and startup
// would stop inexplicably.
//
// It is NOT ENFORCED in local development: a developer wanting to try things with
// "make up && make run" has to be able to type a short password. The floor is not
// lost there either, the auth module's own policy still applies; only this extra
// layer falls away.
//
// It is EXPORTED so its relation to the auth module's general password floor can
// be pinned by a test (see internal/arch). Were the link kept by hand, the day
// auth's floor rose above this value the gate here would become SILENTLY useless:
// rejecting a password auth already rejects adds nothing, and the claim "a longer
// password is required in a shared environment" would lose its truth.
const MinBootstrapPasswordLen = 16

// DefaultRedisKeyPrefix is the default namespace prefix of the guard keys.
//
// The value is the very prefix that was BAKED INTO redisguard before the prefix
// became configurable. Backward compatibility is not a preference here but a
// requirement: changing the default makes all the rate limit counters of an
// upgraded installation — and, far worse, its in-flight idempotency records —
// invisible at once; every retry in the air at that moment is processed a second
// time, that is, a second order.
const DefaultRedisKeyPrefix = "gobit"

// DefaultNotificationProvider is the identity used when no notification provider
// is chosen.
//
// The value is the same as the identity of the notification module's
// out-of-the-box provider (logonly.ID), but it CANNOT BE BOUND to that package:
// the core cannot import modules (Principle 2.4). Should they drift, the
// installation tries to start with a provider name that is not in the registry and
// internal/app stops startup — it does not stay silent.
//
// It is EXPORTED so its agreement with the envDefault tag can be pinned by a test.
const DefaultNotificationProvider = "log"

// DefaultFileProvider is the identity used when no file provider is chosen.
//
// The value is the same as the identity of the file module's out-of-the-box
// provider (local.ID), but it CANNOT BE BOUND to that package: the core cannot
// import modules (Principle 2.4). The price of drift is exactly
// [DefaultNotificationProvider]'s and is not repeated.
//
// It is EXPORTED so its agreement with the envDefault tag and with the module's
// constant can each be pinned by a test (see internal/arch).
const DefaultFileProvider = "local"

// DefaultFileRoot is the default root directory of the "local" file provider.
//
// It is a relative and DURABLE path; the reasoning is on the [Config.FileRoot] field.
const DefaultFileRoot = "./data/uploads"

// DefaultFileMaxUploadBytes is the default maximum size of a single upload.
//
// 5 MiB is generous for a product image and tight for a video dragged in by
// accident. The value is repeated in DECIMAL in the envDefault tag (Go struct tags
// do not accept a constant reference) and the agreement is pinned by a test.
const DefaultFileMaxUploadBytes int64 = 5 << 20

// DefaultFileAllowedTypes are the content types accepted by default on upload.
//
// It is a string because of envSeparator: the default in the tag is a single
// string too, and their agreement can only be checked if they are held in the same
// form.
const DefaultFileAllowedTypes = "image/jpeg,image/png,image/gif,image/webp"

// The default limits of the GraphQL read surface.
//
// The values repeat the constants of the package that ENFORCES the limits
// (internal/modules/product/graph); because the core CANNOT import modules
// (Principle 2.4) it cannot bind to them. The price of drift is silent: an
// installation that gave no environment variable at all would run under a limit
// OTHER than the one written both in this file and in the module's documentation.
// The link is therefore pinned by a test (see internal/arch).
//
// The GRAPHQL_ prefix on the names is safe: unlike the situation
// METRIC_EXPORT_INTERVAL avoids (see [Config.MetricInterval]), neither the GraphQL
// specification nor gqlgen has RESERVED any of these names — that is, a borrowed
// name is not owned without its meaning being borrowed too.
const (
	// DefaultGraphQLMaxDepth is the default upper bound on the number of nested
	// fields.
	DefaultGraphQLMaxDepth = 10

	// DefaultGraphQLMaxComplexity is the default cost ceiling of a single document.
	DefaultGraphQLMaxComplexity = 50000

	// DefaultGraphQLIntrospection reports that introspection is open by
	// default.
	DefaultGraphQLIntrospection = true

	// DefaultGraphQLMaxFieldRepetition is the default upper bound on how many times
	// the same field may be selected under the same object.
	DefaultGraphQLMaxFieldRepetition = 20

	// DefaultGraphQLMaxResponseBytes is the default byte ceiling of a single response.
	DefaultGraphQLMaxResponseBytes = 4 << 20

	// DefaultGraphQLMaxIntrospectionRoots is the default upper bound on the number
	// of introspection roots in a document.
	DefaultGraphQLMaxIntrospectionRoots = 2

	// DefaultGraphQLMaxIntrospectionDepth is the default depth ceiling of the
	// introspection subtree.
	DefaultGraphQLMaxIntrospectionDepth = 15

	// DefaultGraphQLMaxSelections is the default maximum number of selections a
	// document can produce once it is expanded.
	DefaultGraphQLMaxSelections = 10000
)

// Connection addresses defaulted for local development only.
// They match deploy/docker-compose.yml. Validate REQUIRES these values to be
// overridden while APP_ENV=production; otherwise a missing secret injection would
// quietly go to production on hard-coded credentials.
//
// CAUTION: the envDefault tags below have to be character for character the same
// as these constants (Go struct tags do not accept a constant reference).
// TestDefaultTagsMatchConstants checks that agreement.
const (
	// The gosec suppressions here are deliberate: these constants are NOT secrets,
	// they are on the contrary the known local development values that have to be
	// REJECTED in production. Validate enforces the guard by comparing against them.
	DefaultDatabaseURL = "postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable" //nolint:gosec // G101: a deliberate local development default; Validate rejects it in production
	DefaultRedisURL    = "redis://:gobit@localhost:6379/0"                             //nolint:gosec // G101: a deliberate local development default; Validate rejects it in production
)

// The default limits of the PostgreSQL pool.
//
// The values are the same as core/db's OWN defaults and have to stay so:
// the only job of these two constants is to preserve the behavior from before the
// pool became configurable — an installation giving no environment variable opens
// exactly today's pool. Drift is failed by TestThePoolDefaultsAgreeWithTheDbPackage
// in internal/arch.
//
// The type is int32 because pgxpool's field is int32; since all of its neighbors
// are int this is a deliberate deviation and the reason was MEASURED. Were it int,
// a narrowing conversion (int32(cfg.DBMaxConns)) would be needed at the binding
// point and the linter rejects it: "G115: integer overflow conversion int ->
// int32". The only way out was a nolint line, that is, turning the check off;
// fitting the type to the consumer makes the conversion not exist at all.
//
// An out-of-range value fails at parsing under BOTH types — the env library bounds
// int at 32 bits too ("strconv.ParseInt: parsing \"2147483648\": value out of
// range", measured while the type was int). That is, the choice here does not
// prevent an overflow; the overflow is already prevented, the choice only prevents
// a suppressed check.
const (
	// DefaultDBMaxConns is the default maximum number of connections the pool may open.
	DefaultDBMaxConns int32 = 10

	// DefaultDBMinConns is the default number of connections the pool tries to keep
	// even while idle.
	DefaultDBMinConns int32 = 2
)

// The valid enum values; Validate checks against these.
var (
	validAppEnvs    = []string{devAppEnv, "staging", "production"}
	validLogLevels  = []string{"debug", "info", "warn", "error"}
	validLogFormats = []string{"json", "text"}
	validEventBuses = []string{"inmemory", BackendRedis}
	// validGuardBackends are the valid backends of the guard components.
	validGuardBackends = []string{"memory", BackendRedis}
)

// Config holds every setting the server needs in order to run.
//
// The default values agree with deploy/docker-compose.yml; locally "make up &&
// make run" works without extra settings. In production DatabaseURL and RedisURL
// MUST be overridden with environment variables.
type Config struct {
	// AppEnv is the running environment: development | staging | production.
	AppEnv string `env:"APP_ENV" envDefault:"development"`
	// AppPort is the TCP port the HTTP server listens on.
	AppPort int `env:"APP_PORT" envDefault:"9000"`

	// DatabaseURL is the PostgreSQL connection address (pgx DSN format).
	DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable"`
	// RedisURL is the Redis connection address.
	RedisURL string `env:"REDIS_URL" envDefault:"redis://:gobit@localhost:6379/0"`

	// DBMaxConns is the maximum number of connections the PostgreSQL pool may open.
	//
	// This number is the database concurrency ceiling not of a SINGLE request but of
	// THE WHOLE PROCESS: HTTP requests, the workflow engine (pgstore) and the event
	// consumer all draw from the same pool. When the ceiling is reached a request does
	// NOT get an error, it queues — pgxpool.Acquire waits until a connection frees up
	// or the request's deadline expires.
	//
	// The easily missed side of the ceiling is in GraphQL: gqlgen resolves root fields
	// CONCURRENTLY and does NOT BOUND the count — graphql.FieldSet.Dispatch calls the
	// first in the caller's goroutine and opens one more for each of the rest. With
	// GRAPHQL_MAX_FIELD_REPETITION=20 a single LEGITIMATE storefront document can
	// carry 20 aliased "products" and 20 aliased "product"; that is, one request opens
	// 40 concurrent reads and each of them makes a few round trips in turn.
	//
	// The table below was measured with 40 concurrent LIST fields, and that does not
	// come out of a single document: because the repetition limit is counted per
	// (object, field) pair, a single document gives at most 20 list + 20 single
	// fields. So the table is a load LEVEL, not "one request does this" — two
	// documents or two concurrent clients do.
	//
	// # The measurement
	//
	// On a 52k-product catalog, with real storefront queries, 40 concurrent root
	// fields × 5 rounds:
	//
	//	max_conns=10   p50 298.9 ms   771 of 813 acquires waited   avg wait 65.3 ms
	//	max_conns=20   p50 313.8 ms   740 of 813 acquires waited   avg wait 37.4 ms
	//	max_conns=40   p50 314.4 ms    38 of 813 acquires waited   avg wait  0.7 ms
	//
	// The same root field running on its own takes 63.2 ms; that is, at 10 connections
	// the latency goes up 4.7×. But growing the pool does NOT BRING IT BACK: while the
	// database is on the same box and the query is CPU-bound, the bottleneck is not
	// the pool but the server itself, and the pool only moves where the queue sits.
	//
	// When the HOLD time of a connection depends not on CPU but on NETWORK LATENCY —
	// the usual case in production, a separate database server — the table turns. The
	// latency was modeled by waiting WHILE THE CONNECTION IS HELD: no server CPU is
	// spent, and a network hop does exactly that to the pool. The SAME list root
	// field, the same fan-out of 40, with latency added per round trip:
	//
	//	latency   max_conns=10    max_conns=40
	//	none      p50 306 ms      p50 368 ms
	//	5 ms      p50 459 ms      p50 348 ms
	//	20 ms     p50 638 ms      p50 351 ms
	//
	// That is, what the knob wins depends on the topology and is, measurably,
	// moderate on the LIST path: 1.3× at a 5 ms hop, 1.8× at 20 ms. On cheaper root
	// fields the effect grows — on the three-round-trip SINGLE product field, with the
	// same fan-out and 5 ms of latency, the p50 drops from 69.2 ms to 18.0 ms (3.8×) —
	// because there the query itself is almost free and the time spent is almost
	// entirely waiting. The two lines must not be mixed up: same knob, same fan-out,
	// different root field.
	//
	// # Why the default stayed 10
	//
	// The measurement does not say "bigger is always better"; it says what was missing
	// was NOT A NUMBER BUT A KNOB. Raising the default would also multiply every
	// instance's connection budget ON THE SERVER: on a cluster with
	// max_connections=100 a pool of 40 leaves room for two instances and the third
	// comes up with "sorry, too many clients already". That price would fall on every
	// installation while the gain in return would fall only on latency-bound
	// topologies.
	//
	// # Why there is no startup WARNING
	//
	// A "the pool (10) is smaller than the GraphQL fan-out (40)" warning would fire on
	// every CORRECTLY built installation: the pool is a budget shared by all
	// concurrent requests, not by a single document, and sizing it for the worst
	// single document is something nobody does. ADR 0015 decision 4's criterion does
	// not hold either: an exhausted pool is not SILENT — when measured, all 20 of the
	// 20 requests whose deadline expired returned an error ("context deadline
	// exceeded"). The error cannot be told apart from a slow query, but the pool's
	// limits are already logged at startup (see db.New, "the postgres connection pool
	// is ready").
	DBMaxConns int32 `env:"DB_MAX_CONNS" envDefault:"10"`

	// DBMinConns is the number of connections the pool tries to keep even while idle.
	//
	// It opens TOGETHER WITH DB_MAX_CONNS. A ceiling opened on its own could only be
	// turned up: with the lower bound fixed at 2, giving DB_MAX_CONNS=1 hits the
	// pool's own validation ("MinConns cannot be greater than MaxConns") and the
	// process never comes up. Shrinking is not an invented need — the only remedy for
	// an installation connecting to a shared cluster with many instances is to narrow
	// the pool per instance.
	DBMinConns int32 `env:"DB_MIN_CONNS" envDefault:"2"`

	// JWTSecret is the secret admin session tokens are signed with.
	//
	// It has NO default and must not have one: a predictable signing secret means
	// anybody can mint themselves an admin token. When left empty the application
	// comes up ONLY IN LOCAL DEVELOPMENT; in every shared environment (staging and
	// production) Validate REJECTS it. See IsShared.
	JWTSecret string `env:"JWT_SECRET"`
	// JWTTTL is the validity period of an admin session token.
	JWTTTL time.Duration `env:"JWT_TTL" envDefault:"12h"`

	// AdminBootstrapEmail is the e-mail of the FIRST admin user to be created at startup.
	//
	// A server coming up on an empty database has no administrator, and because the
	// admin endpoints are guarded there is no way to create the first one over HTTP
	// either; without these two variables a fresh installation would stay UNUSABLE.
	//
	// It is given TOGETHER WITH [Config.AdminBootstrapPassword]; if only one of them
	// is given Validate returns an error. If both are empty the seed step never runs,
	// and that is a legitimate choice: the environment of an INSTALLED system does not
	// have to carry these variables.
	//
	// "Installed" is a condition validation CANNOT SEE: whether the database has any
	// users at all cannot be known from here. The check therefore belongs to the seed
	// step, and there a fresh database plus these two variables empty STOPS startup in
	// shared environments; the reasoning is in internal/app's
	// reportUnmanageableInstallation godoc.
	AdminBootstrapEmail string `env:"ADMIN_BOOTSTRAP_EMAIL"`
	// AdminBootstrapPassword is the password of the first admin user.
	//
	// It is NEVER logged and appears in no error message; validation reports only its
	// LENGTH. In shared environments it has to be at least
	// [MinBootstrapPasswordLen] characters.
	//
	// Because the seed step runs only while there are no users at all (see
	// internal/app's seedAdmin), forgetting this value in the environment does NOT
	// CHANGE the password of an existing administrator.
	AdminBootstrapPassword string `env:"ADMIN_BOOTSTRAP_PASSWORD"`
	// EventBus is the backend of the event bus: inmemory | redis.
	//
	// inmemory is single-process and NOT DURABLE: delivery is asynchronous, and if the
	// process crashes or shutdown does not finish within [Config.ShutdownTimeout] the
	// undelivered events vanish without a trace — the order was placed, the
	// confirmation notification never went out. In shared environments this risk is
	// WARNED about at startup (see internal/app's warnAboutEventBus); it is not stopped,
	// because on a single-instance staging installation inmemory is still a legitimate
	// choice and the same concession is made with GUARD_BACKEND=memory.
	//
	// If more than one instance is being run, redis has to be used (plan Section 3);
	// then [Config.RedisKeyPrefix] determines the namespace of the events.
	EventBus string `env:"EVENT_BUS" envDefault:"inmemory"`

	// EventBusConsumer is the name identifying this process within the consumer group
	// (used only while EVENT_BUS=redis).
	//
	// Left empty, "<hostname>-<pid>" is used, and that is the right thing on every
	// deployment running one process per container. The only legitimate reason to give
	// it explicitly is a DURABLE identity (a StatefulSet pod name, say): the bus asks
	// for the pending list only under its OWN name, that is, if the process comes up
	// with a new name every time, the messages of the previous run that were processed
	// but not ACKed are never delivered.
	//
	// Giving the SAME name to two processes is the worst option and it is silent: both
	// read that name's pending list at startup, that is, each also takes the messages
	// the other is STILL processing and the same event is processed twice. Validation
	// cannot see this — a single process knows nothing besides itself — which is why
	// the name used is LOGGED at startup; the collision is only visible once two
	// startup logs are put side by side.
	EventBusConsumer string `env:"EVENT_BUS_CONSUMER"`

	// NotificationProvider is the identity of the provider that will send notifications.
	//
	// The default is [DefaultNotificationProvider], that is, the "log" provider that
	// SENDS nowhere: the framework cannot know which e-mail/SMS service will be used,
	// and the name of the default has to say openly that it does not send.
	//
	// Which names are VALID config does not and cannot know: providers come from
	// plugins and the plugin list is fixed at compile time (the same distinction holds
	// for [Config.Plugins]). Only the FORM is validated here; whether the name is
	// really registered is checked by the composition root (internal/app) after all the
	// plugins are loaded, and an unknown name STOPS startup. Falling back to the
	// default quietly would produce an installation that believes it sends e-mail in
	// production but reaches no customer at all.
	NotificationProvider string `env:"NOTIFICATION_PROVIDER" envDefault:"log"`

	// FileProvider is the identity of the provider that will store uploaded files.
	//
	// The default is [DefaultFileProvider], that is, the "local" provider writing the
	// files under [Config.FileRoot]. Whether the name is VALID config cannot know —
	// the reasoning is character for character [Config.NotificationProvider]'s and is
	// not repeated; only the FORM is validated here.
	FileProvider string `env:"FILE_PROVIDER" envDefault:"local"`

	// FileRoot is the root directory the "local" provider writes the files into.
	//
	// # Why NOT A TEMPORARY DIRECTORY
	//
	// Writing to os.TempDir() when it is not configured is tempting but WRONG: the
	// address of an uploaded image is written PERMANENTLY into the product record, and
	// when the operating system cleans that directory (or the process restarts on
	// another machine) every image in the storefront returns a 404. Nobody sees an
	// error; only the pictures disappear. Silent data loss is always more expensive
	// than a configuration error blowing up at startup.
	//
	// The default is therefore a DURABLE and visible path: "./data/uploads", relative
	// to the repository root. Local development needs no extra setting for "make up &&
	// make run" (the repository's rule) and the files stay in place across restarts.
	//
	// In SHARED environments an absolute path (a mounted volume) has to be given; a
	// relative path there is the same silent loss in slow motion. This does NOT STOP
	// STARTUP, it is warned about — the reasoning is in the
	// [Config.LocalFileRootIsDurable] godoc.
	//
	// Leaving it empty is a separate decision and config REJECTS it: without a root
	// the local provider cannot be registered, and if an unregistered provider is
	// selected startup stops anyway. Having the rejection here gives the same outcome
	// two steps earlier and says which variable is missing.
	FileRoot string `env:"FILE_ROOT" envDefault:"./data/uploads"`

	// FileMaxUploadBytes is the maximum size of a single upload (in bytes).
	//
	// An unbounded body is the cheapest way to fill the disk with a single request;
	// that is why there is a limit and why it is configurable. The default is
	// [DefaultFileMaxUploadBytes] (5 MiB) — generous for a product image, tight for
	// uploading a video by accident.
	FileMaxUploadBytes int64 `env:"FILE_MAX_UPLOAD_BYTES" envDefault:"5242880"`

	// FileAllowedTypes are the CONTENT types accepted on upload.
	//
	// The list is an ALLOW LIST: a type not here is rejected. Were it a deny list,
	// every format nobody has thought of today (a document, an archive, a script)
	// would be accepted by default.
	//
	// The values are compared against the type detected FROM THE CONTENT
	// (net/http.DetectContentType), NOT against the client's Content-Type header. That
	// is why the form is kept narrow too: lower case, without parameters
	// ("image/png"). An entry written as "Image/PNG" or "image/png; charset=..." never
	// matches and the allow list would quietly NARROW — a line sitting in the list and
	// letting no file through is the worst kind of configuration error.
	//
	// The default is [DefaultFileAllowedTypes] and it DOES NOT INCLUDE SVG; the
	// reasoning is in the godoc of the content type constants in
	// core/provider (in short: an SVG is a document, it carries script, and
	// served from the same origin it becomes stored XSS).
	FileAllowedTypes []string `env:"FILE_ALLOWED_TYPES" envSeparator:"," envDefault:"image/jpeg,image/png,image/gif,image/webp"`

	// CORSAllowedOrigins are the sites allowed to call the STORE surface from a
	// browser. Empty — the default — means no CORS at all.
	//
	// # Why it defaults to closed
	//
	// A default-open policy is a security decision, and one nobody made is one
	// nobody can be asked about. An installation opts IN to being callable from
	// other sites.
	//
	// # Why it covers the store surface only
	//
	// The store surface authenticates with a publishable key, which is not a
	// secret and is expected to live in a browser. The admin surface
	// authenticates with a bearer token, and ADR 0011 refused CORS there for
	// exactly that reason — the token would have to be kept in a browser.
	//
	// "*" is accepted and means every site. It is honest for a storefront API
	// that is public by design; credentials are never allowed either way, so
	// the wildcard cannot be ridden by an ambient cookie.
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`

	// LogLevel is the structured log level: debug | info | warn | error.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	// LogFormat is the log output format: json | text.
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"`

	// ShutdownTimeout is the maximum time granted to open requests to finish after
	// SIGTERM.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
	// ReadinessDegradedTimeout is the PER-PROBE budget of the /ready endpoint for the
	// dependencies that DEGRADE it (today only Redis).
	//
	// Nobody is waiting for the answer of a degrading probe — the instance serves in
	// either case — so the only thing its slowness can do is FAIL THE PROBE: a single
	// Ping to an unreachable Redis takes 1.7 seconds (the client tries five times) and
	// kubelet's readinessProbe.timeoutSeconds defaults to 1, that is, an unbudgeted
	// probe would make the pod NotReady — bringing back through the side door the very
	// full outage the distinction exists to prevent.
	//
	// The default is 250 ms and being adjustable is essential: if Redis is across the
	// network, even a healthy Ping can exceed this budget and the installation reads
	// "degraded" permanently. The budget is NOT SILENT — when it is exceeded, the
	// message in the /ready body and the WARN line write the budget itself.
	ReadinessDegradedTimeout time.Duration `env:"READINESS_DEGRADED_TIMEOUT" envDefault:"250ms"`
	// ReadHeaderTimeout is the time granted for reading the request HEADERS only.
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"10s"`
	// ReadTimeout is the time granted for reading the headers plus the whole body.
	// ReadHeaderTimeout on its own does not stop the Slowloris variant streaming the
	// body byte by byte; without this limit every connection holds a goroutine + fd forever.
	ReadTimeout time.Duration `env:"READ_TIMEOUT" envDefault:"15s"`
	// WriteTimeout is the time granted for writing the response.
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" envDefault:"30s"`
	// IdleTimeout is how long a keep-alive connection may wait idle.
	IdleTimeout time.Duration `env:"IDLE_TIMEOUT" envDefault:"120s"`

	// ProfilingAddr is the address the pprof listener binds to (e.g.
	// "127.0.0.1:6060"). EMPTY, which is the default, means no profiling
	// listener is opened at all.
	//
	// # Why it is a separate listener and not a route on the API
	//
	// A profile takes as long as it was asked to take, and WriteTimeout would
	// cut it off: the default 30s kills the 30s CPU profile that is the first
	// thing anybody reaches for. The commerce surface cannot have its write
	// budget widened to fit a debugging tool, and the debugging tool cannot be
	// made to fit in 30 seconds, so they do not share a server.
	//
	// It also means no mistake in the guard stack can put a heap dump on the
	// public surface: the profiles are not on that surface to begin with.
	//
	// # Why a routable address is REFUSED in shared environments
	//
	// These endpoints are unauthenticated — that is what makes the separate
	// listener cheap — and a heap profile carries the CONTENTS of live memory:
	// tokens, customer data, a password in flight. Binding them anywhere
	// reachable would publish all of it. Validate refuses a non-loopback
	// address while APP_ENV is not development; reach the listener with a port
	// forward, which is what the loopback bind is for.
	ProfilingAddr string `env:"PROFILING_ADDR"`

	// OTLPEndpoint is the gRPC address of the OpenTelemetry collector (host:port).
	//
	// It has NO default: left empty, ALL telemetry is turned off — traces AND
	// metrics — and the application attempts no outbound connection. Putting a
	// default address here would produce a constant stream of connection errors
	// in every development environment without a collector.
	//
	// ~~left empty, tracing is turned off entirely~~ **Corrected 2026-09-07:** the
	// sentence named tracing alone and was read that way. observability.Setup
	// returns before it builds EITHER provider, so the
	// meter provider is not installed and the two instruments this repository
	// records into resolve to no-ops. In the default install there are no metrics
	// at all, which is the opposite of what a reader planning a Prometheus
	// scrape would take from this comment.
	OTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	// OTLPInsecure reports that the collector will be connected to without TLS.
	//
	// Being true in shared environments (staging and production) is REJECTED by
	// Validate: traces carry request paths, identities and error messages; sending
	// them unencrypted makes them listenable on the network.
	OTLPInsecure bool `env:"OTEL_EXPORTER_OTLP_INSECURE" envDefault:"false"`
	// ServiceName is the service name reported in traces and metrics.
	ServiceName string `env:"OTEL_SERVICE_NAME" envDefault:"gobit"`
	// TraceSampleRatio is the fraction of traces to sample (0.0 - 1.0).
	//
	// The default is 1.0 because a sampling decision cannot be undone: a trace that
	// was not recorded cannot be recovered later. It should be lowered as load grows.
	TraceSampleRatio float64 `env:"OTEL_TRACES_SAMPLER_ARG" envDefault:"1.0"`
	// MetricInterval is how often metrics are sent to the collector.
	//
	// Its name deliberately does NOT carry the OTEL_ prefix, while its neighbors do.
	// The OpenTelemetry specification has RESERVED the name
	// OTEL_METRIC_EXPORT_INTERVAL and defines its value as an INTEGER IN
	// MILLISECONDS; this package, on the other hand, reads every duration as a Go
	// duration. Two meanings do not fit in one name and the clash cuts both ways:
	//
	//   - A value following the specification (60000) gives a "missing unit" error
	//     here and the application DOES NOT COME UP AT ALL.
	//   - A value fitting here (60s) logs a parse error in the OTel SDK's own reader
	//     at every startup.
	//
	// The neighboring OTEL_* names are kept because their meaning AGREES with the
	// specification; a borrowed name is right only when the meaning can be borrowed too.
	MetricInterval time.Duration `env:"METRIC_EXPORT_INTERVAL" envDefault:"60s"`

	// RateLimitPerMinute is the number of requests a client may make per minute.
	//
	// A zero or negative value TURNS the rate limit OFF; it does not mean "0
	// requests". See ADR 0007.
	RateLimitPerMinute int `env:"RATE_LIMIT_PER_MINUTE" envDefault:"600"`
	// TrustedProxyHops is the number of TRUSTED reverse proxies between us and the request.
	//
	// If it is zero, X-Forwarded-For is never read and the rate limit key falls back
	// to the connection's RemoteAddr. A wrong (too large) value, on the other hand,
	// leads to taking the address the client made up for real and to the rate limit
	// being bypassed entirely.
	//
	// The price of the two mistakes is NOT THE SAME, and that is what makes the
	// default zero: a value given too large DESTROYS the guard (the attacker gets a
	// fresh bucket on every request), while a value given too small only LOOSENS it —
	// the quota falls into a single bucket for the whole store. The first is a
	// security hole, the second a capacity problem; that is why the safe default is zero.
	//
	// The price of the too-small value is not negligible either, and it is SILENT:
	// behind a reverse proxy, an ingress or a CDN, RemoteAddr is the proxy's address on
	// every request, that is, RATE_LIMIT_PER_MINUTE becomes a ceiling not "per
	// customer" but "for THE WHOLE STORE" and a single customer can lock up the
	// storefront. In shared environments this situation is WARNED about at startup;
	// the reasoning and why startup does not stop are in the
	// [Config.RateLimitKeyIsPerClient] godoc.
	TrustedProxyHops int `env:"TRUSTED_PROXY_HOPS" envDefault:"0"`
	// IdempotencyTTL is how long idempotency records are kept.
	IdempotencyTTL time.Duration `env:"IDEMPOTENCY_TTL" envDefault:"24h"`
	// IdempotencyMaxMemoryBytes is the byte budget the IN-MEMORY idempotency store may
	// spend on completed records.
	//
	// It is read only under GUARD_BACKEND=memory; on the redis backend the records and
	// the memory are managed by Redis.
	//
	// When the budget FILLS UP the store DROPS the oldest record, and a retry arriving
	// with the dropped key is processed again — that is, a duplicate side effect. The
	// whole trade-off and why dropping was preferred to rejecting are in the
	// MemoryIdempotencyStore godoc in the corehttp package. The limit is not silent:
	// eviction is logged at WARN and the budget is written at every startup.
	//
	// Without a budget the only limit was the TTL and that limit did not stop the
	// growth; measured (same godoc): 10,000 records with a 64 KiB body held 630.69 MiB
	// and 1,000 records with a 1 MiB body held 999.58 MiB, and THE CLIENT picks the key
	// that opens a record.
	IdempotencyMaxMemoryBytes int64 `env:"IDEMPOTENCY_MAX_MEMORY_BYTES" envDefault:"67108864"`
	// GuardBackend is the backend of the rate limit and the idempotency store:
	// memory | redis.
	//
	// The default is "memory" and it is for a SINGLE-INSTANCE installation. If more
	// than one instance is being run, "redis" is MANDATORY; with the in-memory store
	// the rate limit is multiplied by the instance count (a speed problem) and the
	// idempotency guard does not work between instances AT ALL — two requests with the
	// same key landing on different instances are processed twice, that is, two orders.
	// The second is a correctness problem.
	//
	// That a single key picks both is deliberate: were they selectable separately, a
	// half configuration such as making idempotency shared and forgetting the rate
	// limit would be possible, and that halfness would only show up under load.
	GuardBackend string `env:"GUARD_BACKEND" envDefault:"memory"`
	// RedisKeyPrefix is the installation's namespace prefix in Redis.
	//
	// It covers THREE kinds of key at once: the guard keys "<prefix>:rl:<client>" and
	// "<prefix>:idem:<key>" (see the core/http/redisguard package godoc),
	// while the event streams are written as "<prefix>:events:<event name>"; the event
	// bus's consumer group name is the prefix itself (see
	// eventbus.RedisConfig.WithNamespace).
	//
	// Two gobit installations sharing the SAME Redis (staging with production, or two
	// stores in the same cluster) have to give this value DIFFERENTLY. Left the same,
	// three faults are born at once and their weights differ:
	//
	//   - They spend each other's rate limit quota. A speed problem.
	//   - They READ each other's idempotency record: one installation's response goes
	//     to the other's client. A correctness problem.
	//   - They connect to the SAME consumer group. This is the heaviest: by the
	//     definition of a group, only ONE of the two installations receives an event,
	//     that is, production's "order.placed" event can be consumed and swallowed by
	//     staging and the order confirmation goes nowhere.
	//
	// CHANGING the prefix is not free either and has to be done knowingly: a new prefix
	// means a new stream and a new group, that is, the undelivered events waiting in
	// the old stream STAY there. The change is made in order to separate installations;
	// the prefix of a running installation is not moved around.
	//
	// A separate Redis DB (redis://.../1) or a separate instance separates them too,
	// but both are INFRASTRUCTURE decisions: Redis Cluster does not support numbered
	// DBs and a separate instance is a money/operations cost. The prefix makes the same
	// separation with configuration.
	//
	// It is a SEPARATE variable; it is not bound to the existing OTEL_SERVICE_NAME.
	// That name is the service name visible on the dashboards and changing it for
	// observability is an ORDINARY job; binding the two to one variable would mean a
	// rename made on a dashboard quietly abandoning all the idempotency records of a
	// running installation.
	//
	// The default is [DefaultRedisKeyPrefix] and it preserves today's behavior; in
	// single-installation environments it does not have to be touched. Its form is
	// validated independently of GUARD_BACKEND: on the in-memory backend the value is
	// inert, but a typo that only blows up once the backend is changed would be hiding
	// the fault until the very worst moment — the moment of the cutover.
	RedisKeyPrefix string `env:"REDIS_KEY_PREFIX" envDefault:"gobit"`

	// GraphQLMaxDepth is the upper bound on the number of fields that may nest in a
	// GraphQL document.
	//
	// This setting closes a risk that has NO counterpart on the REST side: there the
	// server determines a request's cost (the path is fixed, the body is fixed), while
	// in GraphQL the client writes the SHAPE of the query, that is, its cost. The rate
	// limiter counts the same thing on both surfaces — one request.
	//
	// A ZERO OR NEGATIVE VALUE IS INVALID and stops startup; a reading like "0 =
	// unlimited" deliberately DOES NOT EXIST. It must not be confused with zero
	// meaning "off" in RATE_LIMIT_PER_MINUTE: turning the rate limit off is a capacity
	// choice and its effect is seen at once, while turning the depth limit off is
	// letting a single query consume the server.
	GraphQLMaxDepth int `env:"GRAPHQL_MAX_DEPTH" envDefault:"10"`

	// GraphQLMaxComplexity is the estimated cost ceiling of a single GraphQL document.
	//
	// The unit is "how many fields get resolved" and it is multiplied by the element
	// count on list fields; that is why the number is large (see the module's graph
	// package). It does not replace the depth limit: a shallow but wide document —
	// hundreds of root queries with aliases, or all the variants of a hundred products
	// with limit=100 — passes the depth test and cannot pass this one.
	//
	// A zero or negative value is INVALID; the reasoning is the same as
	// [Config.GraphQLMaxDepth]'s.
	GraphQLMaxComplexity int `env:"GRAPHQL_MAX_COMPLEXITY" envDefault:"50000"`

	// GraphQLIntrospection determines whether the GraphQL schema can be read by
	// introspection.
	//
	// The default is ON: the storefront schema is a file sitting inside this repository
	// and every installation serves the same one, that is, turning it off hides nothing
	// from an attacker and only blinds client tooling (code generators, IDEs). The
	// endpoint is behind the publishable key and the rate limit already; its cost is
	// bound by GRAPHQL_MAX_INTROSPECTION_ROOTS and GRAPHQL_MAX_INTROSPECTION_DEPTH.
	// Those two settings are SEPARATE because the introspection subtree stays OUTSIDE
	// the depth and complexity accounting — not turning it off would be the same as
	// leaving it unbounded.
	//
	// For an installation adding its own fields to the schema the accounting changes,
	// and that is why the switch exists: given false, the query dumping the whole
	// surface in one request closes. The existence of the switch makes being closed a
	// decision rather than an accident.
	GraphQLIntrospection bool `env:"GRAPHQL_INTROSPECTION" envDefault:"true"`

	// GraphQLMaxFieldRepetition is the upper bound on how many times the same field
	// may be selected under the same object.
	//
	// It closes the risk the complexity limit CANNOT SEE: that model prices the NUMBER
	// of fields, not the BYTES. A document selecting the same heavy field — a product
	// description, say — hundreds of times with aliases stays UNDER the ceiling and
	// multiplies the response hundredfold. When measured, an 8 KiB request produced a
	// 191 MiB response and the rate limiter counted it as ONE request.
	//
	// The counting is sibling-scoped and aliases do not enter the key: the attack's
	// only tool is the alias, while a legitimate client asking for the same field twice
	// under different names is ordinary.
	//
	// A zero or negative value is INVALID; the reasoning is the same as
	// [Config.GraphQLMaxDepth]'s.
	GraphQLMaxFieldRepetition int `env:"GRAPHQL_MAX_FIELD_REPETITION" envDefault:"20"`

	// GraphQLMaxResponseBytes is the maximum size of a single GraphQL response.
	//
	// What sets it apart from the other limits is what it measures: they look at the
	// document and ESTIMATE the cost, this limit COUNTS the bytes actually produced. It
	// is the last gate for the day the estimation model turns out to be wrong, and it
	// stands exactly where being wrong cannot be seen.
	//
	// A zero or negative value is INVALID.
	GraphQLMaxResponseBytes int `env:"GRAPHQL_MAX_RESPONSE_BYTES" envDefault:"4194304"`

	// GraphQLMaxIntrospectionRoots is the upper bound on the number of __schema/__type
	// roots in a document.
	//
	// The introspection subtree is OUTSIDE both the depth and the complexity accounting
	// (gqlgen skips it in its own walk too), that is, those two limits never see it.
	// When measured, a 63 KB aliased document gave a 7.3 MiB response and passed even
	// under the strictest depth/complexity setting — while the smallest legitimate data
	// query was being rejected under that same setting.
	//
	// Two roots are enough: no client tool asks for the schema twice in the same
	// document. A zero or negative value is INVALID.
	GraphQLMaxIntrospectionRoots int `env:"GRAPHQL_MAX_INTROSPECTION_ROOTS" envDefault:"2"`

	// GraphQLMaxIntrospectionDepth is the depth ceiling of the introspection subtree.
	//
	// Being SEPARATE from GRAPHQL_MAX_DEPTH is essential: the measured depth of the
	// standard introspection query is 13, and had the data surface's limit been
	// calibrated to it, it would stay far looser than the storefront's real queries
	// need. Because the two counters are separated, the data limit can stop at 10.
	//
	// A zero or negative value is INVALID.
	GraphQLMaxIntrospectionDepth int `env:"GRAPHQL_MAX_INTROSPECTION_DEPTH" envDefault:"15"`

	// GraphQLMaxSelections is the maximum number of selections a document produces
	// ONCE IT IS EXPANDED.
	//
	// Fragment expansion is EXPONENTIAL: a 26-level fragment chain calling itself twice
	// is a valid, acyclic document of 1.1 KB but opens 2^26 selections. The trap is not
	// in a single counter — depth, field repetition and gqlgen's complexity walk all
	// descend into a fragment definition without memory — which is why the budget runs
	// BEFORE all of them and the traversal is cut short when it is exhausted: so as not
	// to do the very work the limit forbids while enforcing the limit.
	//
	// A zero or negative value is INVALID.
	GraphQLMaxSelections int `env:"GRAPHQL_MAX_SELECTIONS" envDefault:"10000"`

	// Plugins are the names of the plugins to be installed (comma separated).
	//
	// The default is EMPTY: the presence of a compiled plugin is not enough to install
	// it, it has to be chosen explicitly. The reason is concrete: plugins want
	// configuration (STRIPE_API_KEY for payment-stripe, say) and the installation gives
	// an ERROR at startup if that setting is missing. Installing every compiled plugin
	// automatically would mean the application never coming up because of a single
	// missing environment variable.
	//
	// An unknown name gives an error at startup; ignoring it quietly would lead to a
	// plugin whose name was misspelled being taken for "installed".
	Plugins []string `env:"PLUGINS" envSeparator:","`
}

// Load reads the environment variables and returns a validated Config.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: the environment variables could not be read: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// RateLimitKeyIsPerClient reports whether the rate limit quota really falls PER CLIENT.
//
// The limiter reads the client from the X-Forwarded-For chain while
// TRUSTED_PROXY_HOPS is positive, and otherwise from the connection's RemoteAddr
// (see corehttp.TrustedProxyIPKey). Behind a reverse proxy, an ingress or a CDN,
// RemoteAddr is the proxy's address ON EVERY REQUEST: in that installation
// RATE_LIMIT_PER_MINUTE is not "600 per customer" but "600 per minute FOR THE WHOLE
// STORE" and a single customer can lock up the entire storefront. Since running
// behind a reverse proxy is almost the only deployment shape in headless commerce,
// staying silent would make this the most frequently met case.
//
// While the limit is OFF (RATE_LIMIT_PER_MINUTE <= 0) the question is moot and it
// returns true: warning about the key of a limiter that was never installed would
// send the operator to an unrelated setting. That situation has its own separate
// report (see internal/app's warnAboutRateLimit).
//
// # Why the default did NOT change and why startup does NOT stop
//
// Zero hops is the RIGHT answer on an installation facing the internet directly,
// and the configuration cannot know which one holds — which is why
// [Config.LocalFileRootIsDurable]'s criterion applies here as well: not certainly
// wrong, only risky. Pulling the default to 1 is the easy answer but a more
// expensive one: reading an untrusted X-Forwarded-For means taking the address the
// client made up for real, and the attacker bypasses the limit ENTIRELY by getting
// a fresh bucket on every request. A silent loosening is a capacity problem, while
// forgery destroys the guard itself; between the two the safe default is zero.
func (c Config) RateLimitKeyIsPerClient() bool {
	return c.RateLimitPerMinute <= 0 || c.TrustedProxyHops > 0
}

// SlogLevel converts the LogLevel field to a slog.Level.
// For a Config that has passed Validate it always returns a valid level.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// IsProduction reports whether we are running in the production environment.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

// IsShared reports that the environment is a SHARED one (that is, not local development).
//
// This, not IsProduction, is the gate of the secret and TLS requirements: there is
// no security-meaningful difference between staging and production, both are
// environments shared by more than one developer and more than one SERVER INSTANCE.
//
// The concrete fault: if JWT_SECRET is left empty while staging runs multi-instance,
// every instance produces its own random secret at startup (see internal/app's
// jwtSecret); a token taken from instance A returns a 401 on instance B. Because it
// depends on the load balancer's distribution the fault is INTERMITTENT and hard to
// diagnose — it is not even of the class that has to be caught before going to
// production, because the same setting is mandatory in production anyway.
//
// The convenience is granted to local development only: there a single instance
// runs, the price of the token dropping on a restart is next to nothing, and "make
// up && make run" must not ask for extra settings.
func (c Config) IsShared() bool { return c.AppEnv != devAppEnv }

// NeedsRedis reports whether the configuration requires a Redis connection.
//
// Two independent features share the same client: the event bus and the guard
// backend. Asking the question in a single place prevents "who is going to open
// Redis" from being answered differently in two places — that drift would mean one
// of them being on Redis while the other quietly stays in memory.
func (c Config) NeedsRedis() bool {
	return c.EventBus == BackendRedis || c.GuardBackend == BackendRedis
}

// Addr returns the address the HTTP server will listen on.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.AppPort) }
