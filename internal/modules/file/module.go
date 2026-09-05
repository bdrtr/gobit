// Package file is the file module (plan Section 5.6 — the FileProvider
// abstraction).
//
// Its responsibility in a single sentence: to validate the ARBITRARY BYTES
// coming from the client, have them written to a store, record what was
// written in a permanent ledger and serve it back when needed. The module is
// the ONLY writer of the file_uploads data (Principle 2.3).
//
// # Why it exists
//
// Until now the product image only accepted a URL; there was no way at all to
// hand a file to the system. The address this module produces plugs DIRECTLY
// into the existing product image flow — that is, a real consumer path is born
// without touching the product module at all.
//
// # The provider abstraction
//
// The side that stores the bytes is not the module but a PROVIDER that
// satisfies the FileProvider contract in core/provider. The module
// keeps the providers in a registry keyed by their ids
// ([service.ProviderRegistry]) and resolves BY NAME at upload time. The only
// provider that comes out of the box is "local", which writes the files to the
// local disk (internal/modules/file/local); the plugin system can add its own
// provider to the registry in the container without touching the core or this
// module (coreplugin.Host.RegisterFileProvider).
//
// Which provider is used is chosen by FILE_PROVIDER. Whether the name is
// REALLY registered cannot be verified here — plugin providers are registered
// AFTER the modules have come up — and that is why the check sits at the
// composition root (internal/app): an unknown name STOPS the startup.
//
// # Security decisions
//
// This is the FIRST place in the repository where arbitrary bytes are accepted
// from the client. The decisions are written one by one, with their reasons,
// in the relevant files; in summary:
//
//   - The client's file name NEVER becomes a path; the storage key is produced
//     by the provider (the local package). Path traversal is impossible
//     STRUCTURALLY, not by being "sanitized".
//   - The content type is NOT ASKED of the client, it is detected from the
//     content (the api package).
//   - The allow list (not a deny list) comes from the configuration and is
//     applied before a single byte is written to the store (the service
//     package). SVG IS NOT in the default.
//   - The size limit is enforced on the body and on the file separately, and
//     it is configurable.
//   - When serving, the Content-Type is written from the STORED type and every
//     response carries X-Content-Type-Options: nosniff (the api package).
//
// # What it does not know
//
// The module imports no module and does not know WHAT the file belongs to: the
// upload record may be bound to a product, to a variant or to nothing at all.
// uploaded_by is free text, it is NOT a foreign key (Principle 2.2).
//
// # The surfaces it opens outwards
//
//   - "file.service" — the rich in-module surface (with the domain types).
//   - "file.providers" — the provider registry; plugins add their provider here.
//   - POST/GET /admin/v1/uploads and DELETE /admin/v1/uploads/{id} — admin.
//   - GET /files/{key} — the UNPROTECTED serving endpoint; its reason is in the
//     api package.
//
// A cross-module "interop" surface and a Query provider are DELIBERATELY
// ABSENT: there is no other module that reads the upload, and if one wanted to
// read it the only thing it would need is the ADDRESS — and that already sits
// in the product image record. Opening a contract that has no consumer would
// produce a surface that could never be closed again.
package file

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/file/api"
	"github.com/bdrtr/gobit/internal/modules/file/local"
	"github.com/bdrtr/gobit/internal/modules/file/repository"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// ModuleName is the module's name; it is the prefix of the container names and
// of the migration version ledger.
const ModuleName = "file"

// ServiceName is the name of the module's service in the container.
const ServiceName = ModuleName + ".service"

// ProvidersName is the name of the provider registry in the container.
//
// The plugin system adds its own FileProvider by resolving this registry; it
// does not need to change the module's code. The value MUST BE THE SAME as
// coreplugin.FileProvidersName and the agreement is pinned down by an
// internal/arch test.
const ProvidersName = ModuleName + ".providers"

// DefaultProviderID is the id used when no provider has been chosen.
//
// The value comes from the local package: if the config's default ("local")
// and the provider's id drifted apart, the installation would come up with an
// upload path that finds no provider at all.
const DefaultProviderID = local.ID

// svcDB is the name of the core database pool in the container.
const svcDB = "core.db"

// Error codes.
const (
	codeSetupFailed      = "file_module_setup_failed"
	codeProviderRegister = "file_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with their "migrations/" prefix
// stripped: db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Options are the module's setup settings.
type Options struct {
	// ProviderID is the id of the provider to be used at upload time
	// (FILE_PROVIDER). If it is given empty, [DefaultProviderID] is applied.
	ProviderID string
	// Root is the root directory of the "local" provider (FILE_ROOT).
	//
	// If it is given EMPTY the local provider IS NOT REGISTERED and nothing
	// happens beyond that being logged as a warning; there is NO FALLING BACK
	// to a temporary directory. The reason is in the [local.New] godoc (in
	// short: a temporary directory is silent data loss on restart). If the
	// unregistered provider is the selected one, the startup already stops at
	// the composition root.
	Root string
	// MaxUploadBytes is the maximum size of a single upload
	// (FILE_MAX_UPLOAD_BYTES); it is mandatory.
	MaxUploadBytes int64
	// AllowedTypes are the accepted CONTENT types (FILE_ALLOWED_TYPES); at
	// least one type is mandatory.
	AllowedTypes []string
	// Logger falls back to slog.Default if nil is given.
	Logger *slog.Logger
}

// Module is the implementation the file module offers to the core.
type Module struct {
	opts      Options
	svc       *service.Service
	providers *service.ProviderRegistry
	handler   *api.Handler
}

// That the core contract is satisfied is pinned down at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe the document is pinned down at compile time too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; if the method name or its signature drifted,
// nothing would break at compile time — this module's endpoint would only drop
// out of the document silently.
var _ openapi.Describer = (*Module)(nil)

// New produces a file module ready to be registered.
//
// The dependencies are resolved not here but during Register: until that
// moment the container may not have set up the core services yet.
func New(opts Options) *Module {
	if opts.ProviderID == "" {
		opts.ProviderID = DefaultProviderID
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Module{opts: opts}
}

// Name returns the module's unique name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service and the provider registry into the container.
//
// Only the CORE services are resolved; the services of other modules may not
// be registered yet at this stage (see the module.Module documentation).
//
// The default provider ([local.Provider]) is set up here as well and its root
// directory is created AT STARTUP: if a root that cannot be written to waits
// until the first upload, the failure shows up in front of the customer —
// whereas a mistyped path is a configuration error that can be corrected at
// startup. It is registered even when the selected provider is not "local",
// because the registry is a list, not a choice: when the installation moves to
// an object store the OLD records still sit on the local disk and this
// provider is the only thing that can read and delete them.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}

	log := m.opts.Logger.With("module", ModuleName)

	providers := service.NewProviderRegistry()
	if err := m.registerLocalProvider(ctx, providers, log); err != nil {
		return err
	}

	svc, err := service.New(service.Options{
		Store:          repository.New(pool.Pool()),
		Providers:      providers,
		ProviderID:     m.opts.ProviderID,
		MaxUploadBytes: m.opts.MaxUploadBytes,
		AllowedTypes:   m.opts.AllowedTypes,
		Logger:         log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}

	m.svc = svc
	m.providers = providers
	m.handler = api.New(svc)

	log.DebugContext(ctx, "file module registered",
		"service", ServiceName,
		"providers", providers.IDs(),
		"selected_provider", m.opts.ProviderID,
		"max_upload_bytes", m.opts.MaxUploadBytes,
		"allowed_types", m.opts.AllowedTypes,
	)

	return nil
}

// registerLocalProvider sets up the "local" provider if a root directory was
// given.
//
// If the root is EMPTY the provider is not registered and there is NO FALLING
// BACK to a TEMPORARY DIRECTORY; a warning is logged. The reason the warning
// is not an error is that the installation may be a legitimate one: in an
// installation that writes to an object store (or that uploads no files at
// all) the local root is a pointless setting, and demanding it would be a
// requirement with nothing behind it. If this is the selected provider the
// startup stops anyway — but at the composition root, and together with the
// list of "which provider is registered".
func (m *Module) registerLocalProvider(
	ctx context.Context,
	providers *service.ProviderRegistry,
	log *slog.Logger,
) error {
	if m.opts.Root == "" {
		log.WarnContext(ctx, "the local file provider was not registered: no root directory was given",
			"fix", "set FILE_ROOT",
			"warning", "there is NO FALLING BACK to a temporary directory; it would be silent data loss on restart")

		return nil
	}

	prov, err := local.New(local.Options{Root: m.opts.Root})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"the %s module could not set up the local file provider (%s)", ModuleName, m.opts.Root)
	}

	if err := providers.Register(prov); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"the %s module could not register the default provider", ModuleName)
	}

	return nil
}

// Routes mounts the module's routes on the router.
//
// If Register did not run, no endpoint is mounted: rather than a handler
// without a service panicking on the first request, it is better for the
// endpoint not to exist at all.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the file module without Register, no route was mounted")

		return
	}

	m.handler.Routes(r)
}

// Describe writes the module's admin endpoints into the OpenAPI document.
//
// Unlike [Module.Routes] there is NO Register check, and none is needed: the
// schema comes from the types, not from the service.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the module's service; it is nil if Register was not called.
//
// It is meant for tests and for embedded use; in the normal flow the service
// is resolved from the container under the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// Providers returns the module's provider registry; it is nil if Register was
// not called.
//
// The embedding application can add its own provider here; in the normal flow
// the registry is resolved from the container under the name [ProvidersName].
func (m *Module) Providers() *service.ProviderRegistry { return m.providers }

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is constant at compile time and
// The go:embed directive has already verified at compile time that the files exist. Even so,
// returning nil silently would mean the module coming up without migrations
// (that is, without its table); a setup error must blow up openly.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("file: the embedded migrations directory could not be opened: " + err.Error())
	}

	return sub
}
