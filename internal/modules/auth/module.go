// Package auth is the identity module (plan Section 6, Phase 8).
//
// Its responsibility in one sentence: to say WHOM a request comes from and
// WHAT it is authorized for. The module is the SOLE writer of the User (the
// admin user), AuthIdentity, ApiKey and SalesChannel data (Principle 2.3).
//
// # An administrator, not a customer
//
// The "user" here is NOT the person shopping in the store; that person is
// the customer module's data. Keeping the two concepts in separate modules
// is deliberate: there is no path along which a customer gains admin
// authority, and the two tables are joined nowhere.
//
// # The surfaces it opens to the outside
//
//   - "auth.interop" — the authenticator that satisfies the core's
//     corehttp.Authenticator interface STRUCTURALLY. The core resolves it BY
//     NAME and does not import auth (ADR 0001).
//   - "auth.service" — the cross-module primitive call surface (see
//     internal/modules/auth/service, interop.go).
//   - "sales_channel.query" — the read provider opened to the Query layer
//     (ADR 0004). Users and API keys are NOT OPENED ON THIS SURFACE.
//   - /admin/v1/auth/login, /admin/v1/auth/me, /admin/v1/auth/logout,
//     /admin/v1/users, /admin/v1/api-keys, /admin/v1/sales-channels — the
//     admin API.
//
// # Logging out is WHOLESALE
//
// POST /admin/v1/auth/logout drops ALL of the caller's sessions; a single
// device cannot be picked. The token holds no state, and invalidating a
// single token would have wanted a jti-based blacklist (a new repository);
// instead a single time anchor kept per identity is advanced and every token
// minted before it drops at once (see internal/modules/auth/service,
// session.go).
//
// # AN UNPROTECTED ENDPOINT
//
// POST /admin/v1/auth/login is unprotected by its very nature, and whoever
// wires the router MUST LEAVE this path OUT while mounting
// corehttp.RequireAdmin. The path is published by the api.LoginPath
// constant; it must not be written by hand.
//
// # The secret and the lifetime ARE PARAMETERS
//
// The module does not know the internal/core/config package: the JWT secret
// and its lifetime are given from outside through [Options], and whoever
// wires the application (cmd/server) reads them from the configuration and
// passes them here. If the secret is empty [Module.Register] RETURNS AN
// ERROR — not opening at all is right, rather than opening with an unsigned
// admin surface.
//
// # A note to whoever declares the link
//
// Query finds the target provider of an expansion FROM THE MODULE NAME AT
// THE END of the link definition (the target name + ".query" is looked up).
// The auth end has to be written WITH THE ENTITY NAME, and that name is
// DIFFERENT from the module name:
//
//	link.LinkDefinition{
//	    Name:        "product_sales_channel",
//	    From:        link.LinkSide{Module: "product", Field: "product_id"},
//	    To:          link.LinkSide{Module: "sales_channel", Field: "sales_channel_id"},
//	    Cardinality: link.ManyToMany,
//	}
//
// The provider name has to be read from the [ProviderName] constant.
package auth

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// The names in the container.
const (
	// ModuleName is the module's unique name; it is also the prefix of the
	// migration version table.
	ModuleName = "auth"
	// ServiceName is the name of the service in the container. Consuming
	// modules resolve it under this name and through the narrow interface
	// they define THEMSELVES (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName is the name of the authenticator in the container.
	//
	// The core resolves corehttp.Authenticator under this name; for the name
	// to be written in the core, importing the module is NOT NEEDED
	// (Principle 2.4).
	InteropName = ModuleName + ".interop"
	// ProviderName is the name of the query provider in the container
	// (ADR 0004).
	//
	// The name derives NOT from the MODULE name but from the ENTITY name:
	// the provider serves the "sales_channel" entity.
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName is the name of the core database pool in the container.
	dbServiceName = "core.db"
)

// codeSetupFailed reports that the module's setup failed.
const codeSetupFailed = "auth_module_setup_failed"

// codeSecretMissing reports that the JWT signing secret was not given.
const codeSecretMissing = "auth_jwt_secret_missing" //nolint:gosec // G101: not a credential but a constant error CODE returned to the client

// minSecretLenWarn is the secret length below which a warning is logged.
//
// For HS256 the secret has to carry as much entropy as the output length
// (32 bytes); anything shorter can be found by brute force. A short secret
// is NOT REJECTED here — the production gate is in internal/core/config,
// inside Validate, and working with a short secret in local development is
// practical. But it is not passed over silently either.
const minSecretLenWarn = 32

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix
// stripped: the golang-migrate source reads FROM THE ROOT and embed.FS would
// have carried the files together with the folder name.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Options holds the setup settings of the auth module.
//
// The JWT secret and its lifetime come FROM HERE: the module does not know
// the internal/core/config package and config is not registered in the
// container either, which is why the values are taken AS A PARAMETER from
// whoever wires the application.
type Options struct {
	// JWTSecret is the secret with which session tokens are signed using
	// HS256; it is MANDATORY.
	//
	// If left empty [Module.Register] returns an error. It is NEVER logged.
	JWTSecret string
	// JWTTTL is the lifetime of the session token; if 0,
	// service.DefaultJWTTTL.
	JWTTTL time.Duration
	// JWTIssuer is the token's "iss" claim; if empty, service.DefaultIssuer.
	JWTIssuer string
	// BcryptCost is the cost parameter of the password hash; if 0,
	// service.DefaultBcryptCost. It has to be raised as hardware gets faster.
	BcryptCost int
	// LoginFailureThreshold is the number of consecutive failed attempts that
	// triggers the lock; if 0, service.DefaultLoginFailureThreshold.
	LoginFailureThreshold int
	// LoginLockDuration is the duration of the lock; if 0,
	// service.DefaultLoginLockDuration.
	LoginLockDuration time.Duration
	// Logger is the structured log target; if nil, the logs are discarded.
	Logger *slog.Logger
}

// Module is the auth module's implementation of [module.Module].
type Module struct {
	opts    Options
	svc     *service.Service
	handler *api.Handler
	log     *slog.Logger
}

var _ module.Module = (*Module)(nil)

// That it can describe itself in the document is pinned at compile time too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it WITH A TYPE ASSERTION; if the method name or its signature drifts,
// nothing breaks at compile time, only auth's endpoints would silently drop
// out of the document. The price would be the admin client left without a
// body: the endpoints that create users and keys would turn into methods
// whose payload is unknown.
var _ openapi.Describer = (*Module)(nil)

// New produces an auth module that is not set up; the service is set up
// inside [Module.Register].
//
// The main application calls it like this:
//
//	registry.Add(auth.New(auth.Options{
//	    JWTSecret: cfg.JWTSecret,
//	    JWTTTL:    cfg.JWTTTL,
//	    Logger:    log,
//	}))
func New(opts Options) *Module {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{opts: opts, log: log}
}

// Name returns the module's name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service, the authenticator and the query provider
// in the container.
//
// auth needs NO MODULE's service; it resolves only the core pool. Because
// the pool is registered BEFORE Bootstrap, resolving it directly here is
// safe.
//
// If the signing secret is empty the setup stops WITH AN ERROR. The
// rationale: an auth module without a secret would produce an admin surface
// that cannot be logged into but looks protected; the error is visible at
// startup, while a silent setup would have surfaced on the first login
// attempt. In local development, if the auth module is not registered at
// all, the application opens without a secret too (see internal/core/config,
// JWTSecret).
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	if m.opts.JWTSecret == "" {
		return errors.Invalid(codeSecretMissing,
			"the %s module cannot be registered without a JWT signing secret; JWT_SECRET has to be set", ModuleName)
	}
	if len(m.opts.JWTSecret) < minSecretLenWarn {
		// The secret is not logged; only its length is reported.
		m.log.WarnContext(ctx, "auth: JWT signing secret is short",
			slog.Int("length", len(m.opts.JWTSecret)),
			slog.Int("recommended_min", minSecretLenWarn),
		)
	}

	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the %q service", ModuleName, dbServiceName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{
		Logger:                m.log,
		JWTSecret:             m.opts.JWTSecret,
		JWTTTL:                m.opts.JWTTTL,
		JWTIssuer:             m.opts.JWTIssuer,
		BcryptCost:            m.opts.BcryptCost,
		LoginFailureThreshold: m.opts.LoginFailureThreshold,
		LoginLockDuration:     m.opts.LoginLockDuration,
	})
	m.handler = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(m.svc)); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "auth module registered",
		slog.String("service", ServiceName),
		slog.String("authenticator", InteropName),
		slog.String("provider", ProviderName),
		slog.String("unprotected_endpoint", api.LoginPath),
	)
	return nil
}

// Routes mounts the module's admin routes on the router.
//
// It is called AFTER Register (see module.Registry.Bootstrap); the handler
// is set up by then. There is a nil check all the same: if Register errors
// and Bootstrap is cut short, Routes is never called, but if the module is
// used by hand a silent no-op is safer than a panic.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's admin endpoints into the OpenAPI document.
//
// The description itself is in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting the types merely for
// the sake of the document would have widened the module's surface.
//
// Unlike [Module.Routes] there is NO handler check, and none is needed: the
// schema comes from the types, not from the service. Putting a check there
// would have silently emptied the document of a module that was not set up
// too.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the service that was set up; nil if Register was not
// called.
//
// It is for the tests that use the module directly and for the applications
// that embed it; in the normal flow the service is resolved from the
// container under the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens the subtree of the embedded file system.
//
// The path is constant at compile time; landing here means the migrations
// folder was not embedded and it cannot be passed over silently — a module
// opened without migrations would start working without its tables.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("auth: could not open the migration source: " + err.Error())
	}
	return sub
}
