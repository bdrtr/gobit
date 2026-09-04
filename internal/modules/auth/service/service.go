// Package service holds the business logic of the auth module.
//
// # The cross-module surface (ADR 0001)
//
// auth imports NO module. The reverse direction exists: the core's HTTP layer
// wants to authenticate, product wants to know the sales channels in order to
// filter the catalog. This is why auth's surface is split in THREE:
//
//   - The rich in-module surface — it uses [models] types
//     ([Service.CreateUser], [Service.CreateAPIKey] …). Only auth's own API
//     layer calls these methods.
//   - The cross-module surface — it uses ONLY primitive and stdlib types
//     (see interop.go).
//   - The authentication surface — the [Interop] type that satisfies the
//     core's corehttp.Authenticator interface STRUCTURALLY (see interop.go).
//     corehttp IS THE CORE and can be imported; the Principal type is NOT
//     REDEFINED here, the core's own is used.
//
// # Security decisions
//
// The module's security decisions and their reasoning are documented one by
// one:
//
//   - Password storage and timing equality — password.go
//   - Dropping the session (logout and password change) — session.go
//   - JWT production and verification — token.go
//   - API key production, storage and type distinction — apikey.go and
//     [models.APIKey]
//   - The login lock (consecutive failed attempts) — password.go, [Options]
//
// The common rule: a plaintext password and a plaintext API key are NEVER
// stored, are NEVER logged and appear in no error message.
package service

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "auth_invalid_input"
	// CodeInvalidCredentials reports that the login credentials were not
	// accepted.
	//
	// A SINGLE code is deliberate: the cases "no such user", "wrong password",
	// "account locked" and "no password assigned" ALL return with this code
	// (see [Service.Login]).
	CodeInvalidCredentials = "auth_invalid_credentials" //nolint:gosec // G101: not a credential, a constant error CODE returned to the client
	// CodeWeakPassword reports that the password does not meet the policy.
	CodeWeakPassword = "auth_weak_password" //nolint:gosec // G101: not a credential, a constant error CODE returned to the client
	// CodeAPIKeyRevoked reports that the key has been revoked.
	CodeAPIKeyRevoked = "auth_api_key_revoked" //nolint:gosec // G101: not a credential, a constant error CODE returned to the client
	// CodeAPIKeyTypeMismatch reports that the key was presented on the wrong
	// surface.
	CodeAPIKeyTypeMismatch = "auth_api_key_type_mismatch" //nolint:gosec // G101: not a credential, a constant error CODE returned to the client
	// CodeNoSalesChannel reports that the publishable key is not attached to
	// an enabled channel.
	CodeNoSalesChannel = "auth_no_sales_channel"
	// CodeTokenInvalid reports that the session token was not accepted.
	CodeTokenInvalid = "auth_token_invalid" //nolint:gosec // G101: not a credential, a constant error CODE returned to the client
	// CodeNoSession reports that the caller has no session that could be
	// closed.
	//
	// Its only source today is an API key calling the logout endpoint: the key
	// arrives not with a token but with a permanent secret, and there is no
	// such operation as "closing" that secret (see [Service.Logout]).
	CodeNoSession = "auth_no_session"
	// CodeUnconfigured reports that the service has not been configured.
	CodeUnconfigured = "auth_service_unconfigured"
	// CodeSecretMissing reports that the signing secret was not given.
	CodeSecretMissing = "auth_jwt_secret_missing" //nolint:gosec // G101: not a credential, a constant error CODE returned to the client
)

// Paging limits. If no limit is given the default is applied; an excessively
// large limit is rejected, so that a client cannot scan the database with a
// single request.
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be requested in a single
	// request.
	MaxLimit int64 = 100
)

// Default setup values.
const (
	// DefaultBcryptCost is the default bcrypt cost parameter.
	//
	// bcrypt.DefaultCost (10) was chosen for the hardware of 2011; 12 means
	// ~250 ms per password on today's server and lowers the offline attempt
	// rate accordingly. The value IS NOT FIXED ([Options.BcryptCost]) because
	// hardware gets faster and the cost has to be raised along with it; since
	// the cost is stored INSIDE the bcrypt hash, raising it does not
	// invalidate old passwords — old hashes continue to be verified with their
	// own cost.
	DefaultBcryptCost = 12
	// DefaultJWTTTL is the default lifetime of the session token.
	DefaultJWTTTL = 12 * time.Hour
	// DefaultIssuer is the default "iss" claim of the session token.
	DefaultIssuer = "gobit"
	// DefaultLoginFailureThreshold is the number of consecutive failed
	// attempts that triggers the lock.
	DefaultLoginFailureThreshold = 5
	// DefaultLoginLockDuration is the default duration of the lock.
	DefaultLoginLockDuration = 15 * time.Minute
	// DefaultUsageThrottle is how often at most the api_key.last_used_at
	// column will be written.
	DefaultUsageThrottle = time.Minute
)

// Page is a paginated list result.
//
// Limit and Offset are not the request's raw values but the APPLIED ones; the
// API envelope writes these fields as they are, so that the client learns
// about a limit that fell back to the default.
type Page[T any] struct {
	// Items are the records on the current page.
	Items []T
	// Count is the TOTAL number of records matching the filter (not the page
	// size).
	Count int64
	// Limit is the applied page size.
	Limit int64
	// Offset is the applied skip count.
	Offset int64
}

// Repository is the data access surface the service needs.
//
// The interface is defined on the CONSUMING side (here); the concrete
// implementation is in the internal/modules/auth/repository package. This is
// the IN-MODULE counterpart of ADR 0001's pattern and it lets the service be
// tested without a database — decisions such as password timing, token
// verification and key type distinction can all be exercised with a fake
// repository.
type Repository interface {
	CreateUser(ctx context.Context, u models.User, identity *models.AuthIdentity) (models.User, error)
	GetUser(ctx context.Context, id string) (models.User, error)
	GetUserByEmail(ctx context.Context, email string) (models.User, error)
	ListUsers(ctx context.Context, filter models.UserFilter, limit, offset int64) ([]models.User, int64, error)
	GetUsersByIDs(ctx context.Context, ids []string) ([]models.User, error)
	UpdateUser(ctx context.Context, id string, patch models.UserPatch, now time.Time) (models.User, error)
	DeleteUser(ctx context.Context, id string, now time.Time) error

	GetIdentity(ctx context.Context, userID, provider string) (models.AuthIdentity, error)
	SetPasswordHash(ctx context.Context, userID, provider, providerIdentity, hash string, now time.Time) (models.AuthIdentity, error)
	SessionAnchor(ctx context.Context, userID string) (time.Time, error)
	RevokeSessions(ctx context.Context, userID string, now time.Time) ([]models.AuthIdentity, error)
	RegisterLoginFailure(ctx context.Context, identityID string, threshold int, lockUntil, now time.Time) (models.AuthIdentity, error)
	RegisterLoginSuccess(ctx context.Context, identityID string, now time.Time) error

	CreateAPIKey(ctx context.Context, k models.APIKey) (models.APIKey, error)
	GetAPIKey(ctx context.Context, id string) (models.APIKey, error)
	GetAPIKeyByHash(ctx context.Context, tokenHash string) (models.APIKey, error)
	ListAPIKeys(ctx context.Context, filter models.APIKeyFilter, limit, offset int64) ([]models.APIKey, int64, error)
	RevokeAPIKey(ctx context.Context, id, revokedBy string, now time.Time) (models.APIKey, error)
	DeleteAPIKey(ctx context.Context, id string, now time.Time) error
	MarkAPIKeyUsed(ctx context.Context, id string, usedAt, staleBefore time.Time) error
	LinkSalesChannel(ctx context.Context, apiKeyID, channelID string, now time.Time) error
	UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error
	ChannelIDsOfKey(ctx context.Context, apiKeyID string) ([]string, error)
	ChannelsOfKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error)

	CreateSalesChannel(ctx context.Context, c models.SalesChannel) (models.SalesChannel, error)
	GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error)
	ListSalesChannels(ctx context.Context, filter models.SalesChannelFilter, limit, offset int64) ([]models.SalesChannel, int64, error)
	GetSalesChannelsByIDs(ctx context.Context, ids []string) ([]models.SalesChannel, error)
	UpdateSalesChannel(ctx context.Context, id string, patch models.SalesChannelPatch, now time.Time) (models.SalesChannel, error)
	DeleteSalesChannel(ctx context.Context, id string, now time.Time) error
}

// Options are the service's setup settings.
//
// Every field other than JWTSecret has a reasonable default; the secret
// ACCEPTS no default (see [Options.JWTSecret]).
type Options struct {
	// Logger is the structured log target; if nil, logs are dropped.
	Logger *slog.Logger
	// Now is the time source; if nil, time.Now is used. Tests fill this in
	// with a fixed clock to make the time-dependent branches (token lifetime,
	// lock duration) deterministic.
	Now func() time.Time

	// JWTSecret is the secret session tokens are signed with using HS256.
	//
	// IT HAS NO DEFAULT and must not have one: a guessable signing secret
	// means everybody being able to produce an admin token for themselves. If
	// it is left empty, [New] sets the service up but token production and
	// verification return errors.Unavailable — stopping openly is preferable
	// to running unsigned in silence.
	//
	// The value arrives as a PARAMETER from the core configuration
	// (cfg.JWTSecret); the auth module does not know the config package. It is
	// NEVER logged.
	JWTSecret string
	// JWTTTL is the token's validity duration; [DefaultJWTTTL] if 0.
	JWTTTL time.Duration
	// JWTIssuer is the token's "iss" claim; [DefaultIssuer] if empty.
	JWTIssuer string

	// BcryptCost is the cost parameter of the password hash;
	// [DefaultBcryptCost] if 0. A value out of range is pulled back to the
	// default and a warning is logged (see [DefaultBcryptCost]).
	BcryptCost int

	// LoginFailureThreshold is the number of consecutive failed attempts that
	// triggers the lock; [DefaultLoginFailureThreshold] if 0.
	LoginFailureThreshold int
	// LoginLockDuration is the duration of the lock;
	// [DefaultLoginLockDuration] if 0.
	LoginLockDuration time.Duration
	// UsageThrottle is the write frequency limit of api_key.last_used_at;
	// [DefaultUsageThrottle] if 0.
	UsageThrottle time.Duration
}

// Service is the public service of the auth module. It is safe for concurrent
// use.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time

	secret    []byte
	tokenTTL  time.Duration
	issuer    string
	cost      int
	threshold int
	lockFor   time.Duration
	throttle  time.Duration

	// dummyHash is the dummy bcrypt hash used for timing equality; it is
	// produced once on first need (see password.go).
	dummyHash func() []byte
}

// New produces a service that runs on the given repository.
//
// If repo is nil, this is reported as a typed error not at setup time but on
// the first call; the setup path produces no panic.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	cost := opts.BcryptCost
	if cost == 0 {
		cost = DefaultBcryptCost
	}
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		// Had an out-of-range cost been accepted silently, bcrypt would return
		// an error on every call and no password could be verified.
		log.Warn("auth: invalid bcrypt cost, falling back to the default",
			slog.Int("given", cost), slog.Int("default", DefaultBcryptCost))
		cost = DefaultBcryptCost
	}

	svc := &Service{
		repo:      repo,
		log:       log,
		now:       now,
		secret:    []byte(opts.JWTSecret),
		tokenTTL:  orDuration(opts.JWTTTL, DefaultJWTTTL),
		issuer:    orString(opts.JWTIssuer, DefaultIssuer),
		cost:      cost,
		threshold: orInt(opts.LoginFailureThreshold, DefaultLoginFailureThreshold),
		lockFor:   orDuration(opts.LoginLockDuration, DefaultLoginLockDuration),
		throttle:  orDuration(opts.UsageThrottle, DefaultUsageThrottle),
	}
	svc.dummyHash = newDummyHash(cost)
	return svc
}

// ready verifies that the repository is configured.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable(CodeUnconfigured, "auth service is not configured")
	}
	return nil
}

// clock returns the current moment as UTC.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// orDuration replaces a zero duration with the default.
func orDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

// orInt replaces a zero or negative number with the default.
func orInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// orString replaces an empty string with the default.
func orString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
