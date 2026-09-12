// The rules this package enforces on a Config: Validate and everything it
// consults, plus the two questions the composition root asks about a risky but
// legal setting (LocalFileRootIsDurable, profilingBindsToLoopback).
//
// It is kept apart from config.go because the two halves change for different
// reasons: a new environment variable touches the struct and its defaults, while
// a new production guard touches only the rules. 'validate.go' is the name the
// module service packages already give exactly this subject.

package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Validate verifies that the Config fields are consistent among themselves.
// Load calls it automatically; it can also be used for hand-built Configs.
func (c Config) Validate() error {
	if !slices.Contains(validAppEnvs, c.AppEnv) {
		return fmt.Errorf("config: invalid APP_ENV %q (expected: %s)", c.AppEnv, strings.Join(validAppEnvs, ", "))
	}
	if c.AppPort < 1 || c.AppPort > 65535 {
		return fmt.Errorf("config: invalid APP_PORT %d (expected: 1-65535)", c.AppPort)
	}
	if !slices.Contains(validLogLevels, c.LogLevel) {
		return fmt.Errorf("config: invalid LOG_LEVEL %q (expected: %s)", c.LogLevel, strings.Join(validLogLevels, ", "))
	}
	if !slices.Contains(validLogFormats, c.LogFormat) {
		return fmt.Errorf("config: invalid LOG_FORMAT %q (expected: %s)", c.LogFormat, strings.Join(validLogFormats, ", "))
	}
	if !slices.Contains(validGuardBackends, c.GuardBackend) {
		return fmt.Errorf("config: invalid GUARD_BACKEND %q (expected: %s)", c.GuardBackend, strings.Join(validGuardBackends, ", "))
	}
	if !slices.Contains(validEventBuses, c.EventBus) {
		return fmt.Errorf("config: invalid EVENT_BUS %q (expected: %s)", c.EventBus, strings.Join(validEventBuses, ", "))
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL cannot be empty")
	}
	if c.RedisURL == "" {
		return fmt.Errorf("config: REDIS_URL cannot be empty")
	}
	if c.ReadinessDegradedTimeout <= 0 {
		return fmt.Errorf("config: READINESS_DEGRADED_TIMEOUT has to be positive, %s given", c.ReadinessDegradedTimeout)
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("config: SHUTDOWN_TIMEOUT has to be positive, %s given", c.ShutdownTimeout)
	}
	// A NEGATIVE catalog cache TTL is refused while zero is accepted, and the two
	// readings are different on purpose: zero is "write no cache header", which is
	// the default and a real answer, while a negative duration is a value somebody
	// typed wrongly. Treating it as "off" would let "-1h" look like it did what it
	// says (ADR 0151).
	if c.CatalogCacheTTL < 0 {
		return fmt.Errorf(
			"config: STOREFRONT_CATALOG_CACHE_TTL cannot be negative, %s given; zero turns "+
				"the cache header off", c.CatalogCacheTTL)
	}
	for _, t := range []struct {
		name  string
		value time.Duration
	}{
		{"READ_HEADER_TIMEOUT", c.ReadHeaderTimeout},
		{"READ_TIMEOUT", c.ReadTimeout},
		{"WRITE_TIMEOUT", c.WriteTimeout},
		{"IDLE_TIMEOUT", c.IdleTimeout},
	} {
		if t.value <= 0 {
			return fmt.Errorf("config: %s has to be positive, %s given", t.name, t.value)
		}
	}
	if c.ReadTimeout < c.ReadHeaderTimeout {
		return fmt.Errorf("config: READ_TIMEOUT (%s) cannot be smaller than READ_HEADER_TIMEOUT (%s)", c.ReadTimeout, c.ReadHeaderTimeout)
	}

	if c.JWTTTL <= 0 {
		return fmt.Errorf("config: JWT_TTL has to be positive, %s given", c.JWTTTL)
	}

	if c.TraceSampleRatio < 0 || c.TraceSampleRatio > 1 {
		return fmt.Errorf("config: OTEL_TRACES_SAMPLER_ARG has to be in the 0.0-1.0 range, %v given", c.TraceSampleRatio)
	}
	if c.ServiceName == "" {
		return fmt.Errorf("config: OTEL_SERVICE_NAME cannot be empty")
	}
	if c.TrustedProxyHops < 0 {
		return fmt.Errorf("config: TRUSTED_PROXY_HOPS cannot be negative, %d was given", c.TrustedProxyHops)
	}
	if c.IdempotencyTTL <= 0 {
		return fmt.Errorf("config: IDEMPOTENCY_TTL has to be positive, %s given", c.IdempotencyTTL)
	}
	if err := c.validateDBPool(); err != nil {
		return err
	}
	if c.IdempotencyMaxMemoryBytes < MinIdempotencyMemoryBytes {
		return fmt.Errorf(
			"config: IDEMPOTENCY_MAX_MEMORY_BYTES has to be at least %d (the maximum buffered response body), %d given",
			MinIdempotencyMemoryBytes, c.IdempotencyMaxMemoryBytes)
	}
	if err := c.validateRedisKeyPrefix(); err != nil {
		return err
	}
	if err := c.validateEventBusConsumer(); err != nil {
		return err
	}
	if err := c.validateGraphQL(); err != nil {
		return err
	}
	if err := c.validatePlugins(); err != nil {
		return err
	}
	if err := c.validateNotificationProvider(); err != nil {
		return err
	}
	// The file settings carry their own environment gate (an absolute path in a shared
	// environment); that is why they are put next to the ordinary validations rather
	// than into the IsShared block below.
	if err := c.validateFile(); err != nil {
		return err
	}
	// It carries its environment-dependent gate itself; that is why it is put next to
	// the ordinary validations rather than into the IsShared block below.
	if err := c.validateAdminBootstrap(); err != nil {
		return err
	}

	// Falling back to the local development defaults in production means the
	// hard-coded gobit:gobit credential and sslmode=disable. Without this check a
	// missing/empty secret injection would quietly land here.
	if c.IsProduction() {
		if c.DatabaseURL == DefaultDatabaseURL {
			return fmt.Errorf("config: DATABASE_URL has to be overridden while APP_ENV=production (the local development default is in use)")
		}
		if c.RedisURL == DefaultRedisURL {
			return fmt.Errorf("config: REDIS_URL has to be overridden while APP_ENV=production (the local development default is in use)")
		}
	}

	if c.IsShared() {
		// Traces carry request paths, identities and error messages; sending them
		// unencrypted makes them listenable on the network. Even if staging's traffic is
		// counted as "not real", its network and its tokens are real.
		if c.OTLPEndpoint != "" && c.OTLPInsecure {
			return fmt.Errorf("config: OTEL_EXPORTER_OTLP_INSECURE=true is not allowed while APP_ENV=%s", c.AppEnv)
		}
		// The pprof endpoints are unauthenticated and a heap profile carries live
		// memory — tokens, customer data, a password on its way to be hashed. An
		// address that is not loopback publishes all of it to whoever can reach the
		// port. ":6060" is the shape that matters here: it looks local and listens
		// on every interface.
		if c.ProfilingAddr != "" && !c.profilingBindsToLoopback() {
			return fmt.Errorf(
				"config: PROFILING_ADDR=%q listens beyond loopback and is not allowed while APP_ENV=%s (reach it with a port forward instead)",
				c.ProfilingAddr, c.AppEnv)
		}
		// An empty signing secret is two separate faults: a fixed secret means anybody
		// can mint themselves an admin token, while a generated random one means tokens
		// are invalid across instances. Both deserve stopping at startup rather than
		// coming up quietly.
		if len(c.JWTSecret) < minJWTSecretLen {
			return fmt.Errorf("config: JWT_SECRET has to be at least %d characters while APP_ENV=%s", minJWTSecretLen, c.AppEnv)
		}
		// The session never renews (ADR 0031), so the lifetime IS the exposure.
		// The ordinary "> 0" check above accepts a month; this is the half of
		// the range that is a mistake rather than a preference.
		if c.JWTTTL > MaxSharedJWTTTL {
			return fmt.Errorf(
				"config: JWT_TTL cannot be longer than %s while APP_ENV=%s (%s given; the admin session never renews, so its lifetime is the whole exposure)",
				MaxSharedJWTTTL, c.AppEnv, c.JWTTTL)
		}
	}
	return nil
}

// validateDBPool verifies that the PostgreSQL pool limits are consistent among
// themselves.
//
// The same three rules also exist in core/db's Config.Validate and the
// repetition is DELIBERATE; the reasoning is of the same class as the one in
// [Config.validateRedisKeyPrefix]. What this copy concretely wins is THE NAMES:
// db's error says "MinConns (5) cannot be greater than MaxConns (1)" and the
// operator has no lever called MinConns — which environment variable to fix is
// what this copy says. The copy in db, in turn, guards the callers that do NOT
// come through config (tests, embedding applications).
//
// No UPPER bound was set; the reasoning is the same as [Config.validateGraphQL]'s.
// Config cannot know the cluster's max_connections nor how many instances will
// connect to that cluster, that is, it could only guess at "too large".
func (c Config) validateDBPool() error {
	if c.DBMaxConns < 1 {
		return fmt.Errorf("config: DB_MAX_CONNS has to be at least 1, %d given", c.DBMaxConns)
	}
	if c.DBMinConns < 0 {
		return fmt.Errorf("config: DB_MIN_CONNS cannot be negative, %d was given", c.DBMinConns)
	}
	if c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("config: DB_MIN_CONNS (%d) cannot be greater than DB_MAX_CONNS (%d)",
			c.DBMinConns, c.DBMaxConns)
	}
	return nil
}

// validateRedisKeyPrefix validates the form of the guard key namespace prefix.
//
// Accepted: at least one character, and only ASCII letters, digits, '-', '_' and
// '.'. The rule is REPEATED here; the redisguard constructors make the same check
// inside themselves too. The repetition is deliberate: config CANNOT import that
// package (redisguard carries a Redis client and config is the bottommost layer),
// and besides, a library must not trust its caller. The copy here moves the fault
// to STARTUP and tells the operator which environment variable is wrong; the copy
// in redisguard guards the callers that do not come through config.
//
// The reasoning for the rejected characters is in the redisguard.validatePrefix
// godoc; in short: ':' can make the keys of two installations COLLIDE, glob
// characters break the operator's "<prefix>:idem:*" scan, and whitespace and
// control characters, being invisible, move the installation into another
// namespace unnoticed.
func (c Config) validateRedisKeyPrefix() error {
	if c.RedisKeyPrefix == "" {
		return fmt.Errorf("config: REDIS_KEY_PREFIX cannot be empty (default: %q)", DefaultRedisKeyPrefix)
	}
	if strings.ContainsFunc(c.RedisKeyPrefix, func(r rune) bool { return !validPrefixRune(r) }) {
		return fmt.Errorf(
			"config: invalid REDIS_KEY_PREFIX %q (only ASCII letters, digits, '-', '_' and '.' are accepted)",
			c.RedisKeyPrefix)
	}
	return nil
}

// validateEventBusConsumer validates the FORM of the event bus consumer name.
//
// [validateName] is NOT USED because it rejects the empty value; here the empty
// value is valid and means "produce the name yourself" (see
// [Config.EventBusConsumer]). Leading/trailing whitespace is nevertheless
// rejected: Redis accepts a value like " gobit-1" as a consumer name without
// complaint, that is, the typo produces no error at all — only, at the next
// startup, the process cannot find its own pending list and those messages are
// delivered to nobody.
//
// That the name is UNIQUE cannot be checked here: a single process does not know
// the other processes bound to the same group. That is why the name used is
// logged at startup.
func (c Config) validateEventBusConsumer() error {
	if c.EventBusConsumer == "" {
		return nil
	}
	if strings.TrimSpace(c.EventBusConsumer) != c.EventBusConsumer {
		return fmt.Errorf("config: EVENT_BUS_CONSUMER %q cannot contain leading/trailing whitespace",
			c.EventBusConsumer)
	}
	return nil
}

// validateGraphQL validates the limits of the read surface.
//
// The rule is one line and it is deliberate: A LIMIT CAN BE RAISED, NOT REMOVED.
// A zero or negative value, even written with the intent "let no limit apply",
// means handing resource consumption over to the query the client writes; that is
// why it is not accepted and startup stops. Falling back to the default quietly
// would be even worse: the operator would believe the value they gave is in force.
//
// No UPPER bound was set. Config cannot guess the difference between a "very
// large" value and "unlimited"; rejecting at startup a ceiling that could be
// legitimate on a huge catalog would mean stopping a working installation for
// something it does not guard. The gate here only weeds out the MEANINGLESS value.
func (c Config) validateGraphQL() error {
	if c.GraphQLMaxDepth < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_DEPTH has to be at least 1, %d given (a limit can be raised, not removed)",
			c.GraphQLMaxDepth)
	}
	if c.GraphQLMaxFieldRepetition < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_FIELD_REPETITION has to be at least 1, %d given (a limit can be raised, not removed)",
			c.GraphQLMaxFieldRepetition)
	}
	if c.GraphQLMaxResponseBytes < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_RESPONSE_BYTES has to be at least 1, %d given (a limit can be raised, not removed)",
			c.GraphQLMaxResponseBytes)
	}
	if c.GraphQLMaxIntrospectionRoots < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_INTROSPECTION_ROOTS has to be at least 1, %d given (a limit can be raised, not removed)",
			c.GraphQLMaxIntrospectionRoots)
	}
	if c.GraphQLMaxIntrospectionDepth < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_INTROSPECTION_DEPTH has to be at least 1, %d given (a limit can be raised, not removed)",
			c.GraphQLMaxIntrospectionDepth)
	}
	if c.GraphQLMaxSelections < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_SELECTIONS has to be at least 1, %d given (a limit can be raised, not removed)",
			c.GraphQLMaxSelections)
	}
	if c.GraphQLMaxComplexity < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_COMPLEXITY has to be at least 1, %d given (a limit can be raised, not removed)",
			c.GraphQLMaxComplexity)
	}
	return nil
}

// validPrefixRune reports whether the character can be used in a namespace prefix.
func validPrefixRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return r == '-' || r == '_' || r == '.'
	}
}

// validatePlugins validates the form of the plugin list.
//
// Empty and repeated names are REJECTED: a value like "PLUGINS=stripe,,stripe" is
// almost always a mistake in a hand-edited environment file, and a repeated name
// would produce a collision in the plugin registry anyway. Which names are VALID
// config does not know; the side building the application (internal/app) knows, and
// it rejects an unknown name there.
func (c Config) validatePlugins() error {
	gorulen := make(map[string]struct{}, len(c.Plugins))
	for i, name := range c.Plugins {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("config: there is an empty name at position %d of the PLUGINS list", i+1)
		}
		if _, dup := gorulen[name]; dup {
			return fmt.Errorf("config: %q appears twice in the PLUGINS list", name)
		}
		gorulen[name] = struct{}{}
	}
	return nil
}

// validateNotificationProvider validates the FORM of the notification provider name.
//
// The form only: whether the name is registered config cannot know (see
// [Config.NotificationProvider]). An empty value is REJECTED — because of
// envDefault it can only be produced by writing "NOTIFICATION_PROVIDER=", and in
// that case an empty identity would be looked for in the provider registry; since
// nobody can register under an empty identity, the result would be a notification
// path returning an error on every order.
//
// Leading/trailing whitespace is rejected as well. In environment files this is
// the invisible and most frequent mistake; trimming it quietly would be wrong too:
// the value the operator wrote and the value the system uses would diverge, and
// the next typo (a two-word name, say) would again quietly give a different result.
func (c Config) validateNotificationProvider() error {
	return validateName("NOTIFICATION_PROVIDER", c.NotificationProvider, DefaultNotificationProvider)
}

// validateFile validates the FORM of the file upload settings and the root
// directory rule.
//
// Whether the provider name is registered cannot be known here (see
// [Config.FileProvider]); the registry check is in the composition root.
//
// The root directory is validated for its form even when the provider is NOT
// "local": the value is used only by the local provider, but a root left empty
// would blow up the day the provider is switched to "local" — that is, the fault
// would be hidden until the very worst moment, the moment of the cutover. The same
// reasoning is written on REDIS_KEY_PREFIX.
//
// THAT THE ROOT IS DURABLE IS NOT CHECKED HERE and that is deliberate. The rule is
// not a validation but a WARNING: the question is asked by
// [Config.LocalFileRootIsDurable] and the answer is written at startup by
// cmd/server (see warnAboutFileRoot). Put into validation, every shared
// installation that never uses the file upload feature would be unable to come up
// without giving an environment variable it gets nothing for; the whole reasoning
// is in that godoc. The only job here is the FORM.
func (c Config) validateFile() error {
	if err := validateName("FILE_PROVIDER", c.FileProvider, DefaultFileProvider); err != nil {
		return err
	}
	if err := validateName("FILE_ROOT", c.FileRoot, DefaultFileRoot); err != nil {
		return err
	}
	if c.FileMaxUploadBytes <= 0 {
		return fmt.Errorf("config: FILE_MAX_UPLOAD_BYTES has to be positive, %d given (default: %d)",
			c.FileMaxUploadBytes, DefaultFileMaxUploadBytes)
	}
	return c.validateFileTypes()
}

// LocalFileRootIsDurable reports whether the root directory of the "local"
// provider will stay IN PLACE when the process restarts.
//
// Two separate paths lead to the same outcome and the second is sneakier than the first:
//
//   - A RELATIVE root is resolved against the process's WORKING DIRECTORY and in a
//     container almost always lands on a NON-durable layer.
//   - A TEMPORARY root (see [temporaryRoots]) is ABSOLUTE, that is, it passes the
//     "give an absolute path" advice and raises no suspicion at all; but the
//     operating system cleans it, and since it is tmpfs on most distributions it
//     does not even wait for a restart.
//
// The outcome is the same in both: at the next deployment the uploaded images are
// gone while the address in the product record stays — that is, every image in the
// storefront returns a 404 without any error being visible. This is the very
// silent data loss the [Config.FileRoot] godoc REJECTS for the default; were the
// criterion only filepath.IsAbs, the rejected behavior would come back in one line
// by writing FILE_ROOT=/tmp/... and the warning would fall silent.
//
// # Why it does NOT STOP STARTUP
//
// Had the rule been put into [Config.Validate], every production installation that
// never uses the file upload feature (entering image addresses by hand) would be
// unable to come up without giving an environment variable it gets nothing for.
// The same concession was made on GUARD_BACKEND: the in-memory guard is BROKEN in
// a multi-instance deployment but does not stop startup, a warning is logged (see
// internal/app's guardStack). The decision here is consistent with it — and the
// reason is shared: it is not certain that the configuration is WRONG, only that it
// is RISKY. A temporary root can be a deliberate choice in an installation that
// does not want the files to be durable (a preview environment, a one-off demo).
//
// The reason the decision sits in config is that the definition of "risky" is here:
// the side writing the warning (internal/app) only calls.
func (c Config) LocalFileRootIsDurable() bool {
	if c.FileProvider != DefaultFileProvider {
		return true
	}
	if !filepath.IsAbs(c.FileRoot) {
		return false
	}

	root := filepath.Clean(c.FileRoot)
	// os.TempDir is looked at IN ADDITION to the list: on an installation with TMPDIR
	// set, the temporary directory may not be /tmp and a fixed list could not see it.
	if isUnder(root, filepath.Clean(os.TempDir())) {
		return false
	}
	for _, gecici := range temporaryRoots {
		if isUnder(root, gecici) {
			return false
		}
	}

	return true
}

// temporaryRoots are the known absolute root directories the operating system cleans.
//
// The list is KEPT short: a long list would give the impression "if it is not here
// it is durable" and turn the warning into a guarantee — whereas this is not an
// exact classification but the catching of the typical mistakes that pass the
// absolute-path requirement.
var temporaryRoots = []string{"/tmp", "/var/tmp", "/dev/shm"}

// isUnder reports whether the path is the given root itself or below it.
//
// The separator condition is necessary: a plain prefix comparison would count the
// path "/tmpfoo" as being under "/tmp" too.
func isUnder(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// browserExecutableTypes are the content types that CANNOT be put on the upload
// allow list.
//
// A form check alone is not enough: on an installation writing
// FILE_ALLOWED_TYPES=image/png,text/html the whole chain works —
// http.DetectContentType really does return "text/html" for an HTML file, the
// allow list lets it through and the file is served FROM THE SAME ORIGIN. The
// result is stored XSS.
//
// X-Content-Type-Options: nosniff does NOT STOP this, and thinking it does is a
// misunderstanding of what the header is for: nosniff prevents the browser from
// replacing the declared type BY GUESSING. There is no guess here — the response
// really is text/html and the browser runs it correctly.
//
// The whole text/* prefix is rejected: a new text type (text/vtt, text/xsl…)
// cannot wait to be added to the list, and every rule written as a deny list
// accepts what it does not list by default.
var browserExecutableTypes = map[string]struct{}{
	"application/xhtml+xml":  {},
	"application/xml":        {},
	"image/svg+xml":          {},
	"application/pdf":        {},
	"application/javascript": {},
	"application/ecmascript": {},
}

// validateFileTypes validates the form of the allow list.
//
// An empty list is REJECTED: an upload endpoint accepting zero types is a gate
// that rejects every request and yet goes on existing. The way to say "accept
// everything" is NOT to empty the list — that decision has to be deliberate enough
// to require writing the types out one by one.
func (c Config) validateFileTypes() error {
	if len(c.FileAllowedTypes) == 0 {
		return fmt.Errorf("config: FILE_ALLOWED_TYPES cannot be empty (default: %q)",
			DefaultFileAllowedTypes)
	}

	gorulen := make(map[string]struct{}, len(c.FileAllowedTypes))
	for i, mediaType := range c.FileAllowedTypes {
		switch {
		case strings.TrimSpace(mediaType) == "":
			return fmt.Errorf("config: there is an empty type at position %d of the FILE_ALLOWED_TYPES list", i+1)
		case strings.TrimSpace(mediaType) != mediaType:
			return fmt.Errorf("config: %q in FILE_ALLOWED_TYPES cannot contain leading/trailing whitespace", mediaType)
		// A type with parameters or in upper case NEVER matches the detected type;
		// accepting it quietly would leave a line sitting in the list and letting no
		// file through.
		case strings.ContainsAny(mediaType, ";"), mediaType != strings.ToLower(mediaType), !strings.Contains(mediaType, "/"):
			return fmt.Errorf(
				"config: invalid FILE_ALLOWED_TYPES entry %q (it has to be lower case and without parameters, e.g. %q)",
				mediaType, "image/png")
		}

		if _, tehlikeli := browserExecutableTypes[mediaType]; tehlikeli || strings.HasPrefix(mediaType, "text/") {
			return fmt.Errorf(
				"config: FILE_ALLOWED_TYPES cannot accept %q: the browser runs this type as a DOCUMENT "+
					"and, because the files are served from the same origin, it becomes stored XSS (nosniff does not stop this, "+
					"because the response really is of that type)", mediaType)
		}

		if _, dup := gorulen[mediaType]; dup {
			return fmt.Errorf("config: %q appears twice in the FILE_ALLOWED_TYPES list", mediaType)
		}
		gorulen[mediaType] = struct{}{}
	}

	return nil
}

// validateName validates the form of a single-line setting that cannot be left empty.
//
// Leading/trailing whitespace is REJECTED and not trimmed; the reasoning is in the
// [Config.validateNotificationProvider] godoc (in short: trimming quietly
// separates the value the operator wrote from the value the system uses).
func validateName(variable, value, fallback string) error {
	if value == "" {
		return fmt.Errorf("config: %s cannot be empty (default: %q)", variable, fallback)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("config: %s %q cannot contain leading/trailing whitespace", variable, value)
	}

	return nil
}

// validateAdminBootstrap validates the configuration of the first administrator seed.
//
// A HALF CONFIGURATION IS REJECTED. An operator writing one of the two variables
// and forgetting the other believes, under a silent skip, that the seed ran, and
// discovers what is missing only at the first login attempt — often days after the
// installation, at a moment when nobody is going to look at the environment file.
// Stopping at startup moves that fault to the moment the configuration is still at
// hand.
//
// The password DOES NOT APPEAR IN THE ERROR MESSAGE; only the expected length is
// reported. The error text goes to stderr and from there, in most installations,
// to the log collector.
func (c Config) validateAdminBootstrap() error {
	if (c.AdminBootstrapEmail == "") != (c.AdminBootstrapPassword == "") {
		return fmt.Errorf("config: ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD have to be given together (only one was given)")
	}
	if c.AdminBootstrapPassword == "" {
		return nil
	}
	if c.IsShared() && len(c.AdminBootstrapPassword) < MinBootstrapPasswordLen {
		return fmt.Errorf("config: while APP_ENV=%s, ADMIN_BOOTSTRAP_PASSWORD has to be at least %d characters",
			c.AppEnv, MinBootstrapPasswordLen)
	}
	return nil
}

// profilingBindsToLoopback reports that ProfilingAddr can be reached from this
// machine only.
//
// An address with no host — ":6060" — is NOT loopback: it listens on every
// interface. That is the case this function exists for, because it is the one
// that reads as local and is not.
func (c Config) profilingBindsToLoopback() bool {
	host, _, err := net.SplitHostPort(c.ProfilingAddr)
	if err != nil {
		// An unparseable address is refused rather than trusted: net/http will
		// reject it too, and guessing on its behalf could only guess wrong in
		// the permissive direction.
		return false
	}
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}
