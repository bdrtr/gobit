package webpush

import (
	"context"
	"crypto/ecdsa"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/module"
)

// dbServiceName is the core pool's name in the container.
const dbServiceName = "core.db"

// The authorization scopes the admin surface requires.
const (
	// ScopeRead lists devices.
	ScopeRead = "webpush:read"
	// ScopeWrite deletes a device and sends a broadcast.
	ScopeWrite = "webpush:write"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// moduleOptions is what the plugin hands the module at Setup.
type moduleOptions struct {
	key         *ecdsa.PrivateKey
	publicKey   string
	fingerprint string
	subject     string
	templates   templateSet
	log         *slog.Logger
}

// webpushModule is the module the plugin brings.
type webpushModule struct {
	opts   moduleOptions
	store  *store
	sender *sender
}

// The core's module contract is satisfied at compile time.
var _ module.Module = (*webpushModule)(nil)

// newModule builds the module. Dependencies are resolved in Register.
func newModule(opts moduleOptions) *webpushModule {
	if opts.log == nil {
		opts.log = slogDiscard()
	}

	return &webpushModule{opts: opts}
}

// Name returns the module's unique name.
func (m *webpushModule) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *webpushModule) Migrations() fs.FS { return migrationsRoot }

// Register resolves the pool and counts the device registry.
//
// # The startup count is the plugin's own smoke test
//
// It answers two questions nothing else can. "Is the storefront wired at all" —
// a plugin installed with no subscriber has zero rows, and zero rows produce
// zero pushes and zero errors, which is indistinguishable from working. And
// "was the signing key rotated" — more than one fingerprint means an older
// group of devices exists that can only ever answer 401, and 401 correctly
// never deletes, so nothing else would ever report them.
func (m *webpushModule) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, dbServiceName)
	}

	m.store = newStore(pool.Pool())
	m.sender = &sender{
		client:  &http.Client{},
		key:     m.opts.key,
		subject: m.opts.subject,
		now:     time.Now,
	}

	m.reportRegistry(ctx)

	return nil
}

// reportRegistry logs what the device registry holds.
//
// A failure to count is NOT a startup failure: the count is a diagnostic, and
// refusing to boot because a diagnostic query failed would trade a visible
// problem for a bigger one.
func (m *webpushModule) reportRegistry(ctx context.Context) {
	counts, err := m.store.countByFingerprint(ctx)
	if err != nil {
		m.opts.log.WarnContext(ctx, "the push subscriptions could not be counted at startup",
			"error", err)

		return
	}

	var total, current int64
	for fingerprint, n := range counts {
		total += n
		if fingerprint == m.opts.fingerprint {
			current = n
		}
	}

	m.opts.log.InfoContext(ctx, "the push device registry is ready",
		"subscriptions", total,
		"under_the_current_key", current,
		"fingerprint", m.opts.fingerprint)

	if stale := total - current; stale > 0 {
		// ERROR rather than WARN: every one of these devices is unreachable
		// and no user action will fix it — they have to visit the site and
		// subscribe again. The rows drain themselves as each is next used, but
		// the operator has to know why the audience shrank.
		m.opts.log.ErrorContext(ctx, "push subscriptions were minted under a DIFFERENT VAPID key "+
			"and can no longer be reached; they are removed as each is next used",
			"stale_subscriptions", stale,
			"current_fingerprint", m.opts.fingerprint)
	}
}

// Routes mounts the storefront and admin endpoints.
//
// If Register did not run, nothing is mounted: an endpoint without a store
// would answer every request with a 500, and a route that does not exist is a
// clearer signal than one that always fails.
func (m *webpushModule) Routes(r chi.Router) {
	if m.store == nil {
		return
	}

	r.Route("/store/v1/webpush", func(r chi.Router) {
		r.Get("/vapid-key", m.handleVAPIDKey)
		r.Post("/subscribe", m.handleSubscribe)
		r.Post("/unsubscribe", m.handleUnsubscribe)
		r.Post("/unbind", m.handleUnbind)
	})

	// The admin surface carries scopes; the identity layer that fills them in
	// (corehttp.RequireAdmin) is attached by whoever builds the router, the same
	// way searchpg's admin endpoint does it.
	r.Route("/admin/v1/webpush", func(r chi.Router) {
		r.With(corehttp.RequireScope(ScopeRead)).Get("/subscriptions", m.handleList)
		r.With(corehttp.RequireScope(ScopeWrite)).Delete("/subscriptions/{id}", m.handleDelete)
		r.With(corehttp.RequireScope(ScopeWrite)).Post("/broadcast", m.handleBroadcast)
	})
}

// onOrderPlaced pushes the confirmation to the customer's devices.
//
// # It never fails the event
//
// A subscriber that returns an error makes the event bus retry, and a retry
// re-pushes to every device that already received the message. The push is a
// courtesy on top of an order that is already written; nothing about it is
// worth replaying an event for. Faults are logged with counts instead.
func (m *webpushModule) onOrderPlaced(ctx context.Context, e eventbus.Event) error {
	// Every field is read as a STRING, because that is how the order module
	// publishes them and the reason is the transport: the Redis backend
	// serializes to JSON, where a number comes back as float64 while the
	// in-memory backend keeps it an int64. A field read as a number would
	// therefore have a different Go type in development and in production.
	orderID := field(e, "order_id")
	customerID := field(e, "customer_id")

	if customerID == "" {
		// A guest order has no customer, so it has no devices. This is the
		// normal case for a storefront that does not require sign-in, and it is
		// not logged per order: it would be the loudest line in the log and it
		// carries no information.
		return nil
	}

	if m.store == nil {
		// The subscription is installed by the core and does not go through
		// Routes, so a handler can be reached even when Register did not run.
		// A typed log line beats a nil dereference in a goroutine.
		m.opts.log.WarnContext(ctx, "the webpush module was not registered; nothing was pushed",
			"order_id", orderID)

		return nil
	}

	devices, err := m.store.byCustomer(ctx, customerID)
	if err != nil {
		m.opts.log.WarnContext(ctx, "the customer's push devices could not be read",
			"order_id", orderID, "error", err)

		return nil
	}
	if len(devices) == 0 {
		// Logged once per order, at INFO, and this is the truthful record that
		// replaces the notification ledger's "sent": there was nobody to send
		// to. A delivery row saying "sent" here would be a lie that also
		// blocks the resend.
		m.opts.log.InfoContext(ctx, "the customer has no registered push device; nothing was sent",
			"order_id", orderID)

		return nil
	}

	// The default template carries the display id and the item count and NOT
	// the money, and that is a decision rather than an omission: a push renders
	// on a locked screen where anyone holding the phone can read it, and the
	// customer-to-device binding is an unverified claim (ADR 0008). An
	// installation that wants the amount can put it in its own template.
	data := map[string]string{
		"order_id":   orderID,
		"display_id": field(e, "display_id"),
		"item_count": field(e, "item_count"),
	}

	result := fanOut(ctx, m.sender, m.store, m.opts.log, devices, m.opts.fingerprint,
		func(sub subscription) ([]byte, error) {
			return m.opts.templates.render(orderPlacedEvent, sub.Locale, data)
		},
		// The topic collapses a duplicate the event bus delivered twice. It is
		// per order and per event, so two different orders never collapse into
		// one another.
		topicFor(orderPlacedEvent, orderID))

	m.opts.log.InfoContext(ctx, "the order confirmation was pushed",
		"order_id", orderID,
		"attempted", result.Attempted,
		"delivered", result.Delivered,
		"gone_removed", result.Gone,
		"failed", result.Failed)

	return nil
}

// field reads one string value out of an event payload.
//
// A value of another type reads as empty rather than panicking: the payload
// crosses a JSON boundary in production, and a subscriber that dies on an
// unexpected type takes the whole consumer down with it.
func field(e eventbus.Event, name string) string {
	if v, ok := e.Data[name].(string); ok {
		return v
	}

	return ""
}

// topicFor builds the RFC 8030 Topic header value.
//
// It has to be a short base64url token, so the event and the id are hashed
// rather than concatenated — an order id alone is already longer than the
// field allows at some services.
func topicFor(event, id string) string {
	return fingerprintOf(event + ":" + id)
}

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is constant at compile time and
// the embed directive has already verified at compile time that the files
// exist. Returning nil silently would mean the module coming up without
// migrations — that is, without its table.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("webpush: could not open the embedded migrations directory: " + err.Error())
	}

	return sub
}
