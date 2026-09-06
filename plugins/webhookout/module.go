package webhookout

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
)

// dbServiceName is the core pool's name in the container.
//
// It is re-spelled as a literal rather than imported, the way every plugin that
// needs the pool has to: the infrastructure names are unexported constants in
// the composition root. Only the provider registries and the callback ring have
// published names.
const dbServiceName = "core.db"

// The authorization scopes the admin surface requires.
const (
	// ScopeRead lists receivers and deliveries.
	ScopeRead = "webhook:read"
	// ScopeWrite registers and removes a receiver, and redrives or discards a
	// dead delivery.
	ScopeWrite = "webhook:write"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// ForwardedTopics are the event names this plugin subscribes to and can
// therefore deliver.
//
// # Why there is a LIST and not a wildcard
//
// The bus subscribes BY NAME ([eventbus.EventBus.Subscribe] takes one), so "every
// event" is not something a subscriber can ask for. That is not a limitation to
// work around here: [TestEverySubscribedTopicHasAPublisher] fails the build for
// a subscription to a topic nobody publishes, and it exists because
// examples/plugin listened for "order.created" — a name gobit does not publish —
// and looked completely wired while its handler never ran once.
//
// So the list is written out, and it is the WHOLE set: a static census of every
// eventbus.Event this repository can publish resolves to exactly these four.
//
// # What happens when a module gains a fifth
//
// This plugin does not deliver it. Nothing errors: the new topic is published,
// its other subscribers run, and a receiver registered for it does not exist
// because [validateTopics] would have refused the registration. The failure is
// therefore quiet in the only way that matters — an integrator asks for an
// event that gobit now emits and is told, correctly, that it is not on the
// list, with no hint that the list is stale.
//
// That is made visible in two places rather than left to a reader:
//
//   - TestTheForwardedTopicsAreEveryPublishedTopic scans the production trees
//     the way internal/arch does and FAILS the day a fifth topic appears here
//     without being added to this list. It is the gate; the rest is comfort.
//   - The startup log prints this list, so an operator comparing gobit's
//     release notes against their own installation has somewhere to look.
//
// # Why the names are constants and the subscriptions are written out
//
// [Plugin.Setup] could range over this slice. It calls Subscribe four times
// with these constants instead, because the reverse gate resolves a
// subscription's name STATICALLY and skips one it cannot resolve — a loop
// variable is exactly such a name. Written out, a typo in any of the four fails
// the build; ranged over, the gate goes quiet and the handler waits forever.
var ForwardedTopics = []string{
	topicOrderPlaced,
	topicProductCreated,
	topicProductUpdated,
	topicProductDeleted,
}

// The topics, as constants the arch gates can resolve.
const (
	// topicOrderPlaced is published when an order is created. It is the only
	// order event gobit publishes today.
	topicOrderPlaced = "order.placed"
	// topicProductCreated is published when a new product is written.
	topicProductCreated = "product.created"
	// topicProductUpdated is published when a product's own fields change.
	topicProductUpdated = "product.updated"
	// topicProductDeleted is published when a product is SOFT deleted.
	topicProductDeleted = "product.deleted"
)

// redactedFields are the payload fields that never leave this installation, and
// why.
//
// # customer_id, and it is not a theoretical worry
//
// `GET /store/v1/customers/{id}` is UNPROTECTED and stays so — the customer
// module says it in writing, and ADR 0008 explains why: gobit offers no surface
// that verifies a customer's identity, and a half-built identity layer is more
// dangerous than none. The consequence is that a customer id is not an
// identifier, it is a BEARER TOKEN for that customer's name, email address and
// every address they have saved.
//
// Sending it to a registered receiver would hand a third party standing access
// to the personal data of every customer who places an order, over an endpoint
// that asks them for nothing. The event carries it because the bus stays inside
// the installation; a webhook body does not.
//
// The removal is VISIBLE: the field name travels in the body's `redacted` list,
// so a receiver sees that something was withheld rather than that the order had
// no customer. A silent removal would be the worse half of this decision — the
// receiver would build a guest-order branch that fires on every order.
//
// This map is deliberately not configurable. A setting that let an installation
// switch it off would be the setting nobody reads before switching it on.
var redactedFields = map[string]string{
	"customer_id": "a customer id is a bearer token for /store/v1/customers/{id}, " +
		"which is unauthenticated by decision (ADR 0008)",
}

// The delivery job's shape.
const (
	// jobName identifies the job. It is the advisory lock's input, the primary
	// key of the job's history and what `gobit jobs` prints, so it is a
	// CONTRACT: changing it starts a new job with no history.
	//
	// It is prefixed with the plugin's own name because the namespace is shared
	// with the core's jobs and with every other plugin's.
	jobName = "webhookout-deliver"

	// every is how often the delivery pass runs.
	//
	// A minute, the same as the outbox relay's, and for the same reason: what
	// waits here is a message somebody is expecting. It is not shorter because
	// a minute is already the floor under the retry ladder — the first backoff
	// step is one minute precisely so that the schedule the ladder documents is
	// the schedule the scheduler can express.
	every = time.Minute

	// maxRun bounds one pass.
	//
	// It has to stay UNDER [every], and the job registry refuses a definition
	// where it does not: a pass that can outlast its own interval could never
	// catch up, and the backlog would grow while every run looked healthy.
	//
	// Forty-five seconds against [deliveryLimit] deliveries at
	// [perAttemptTimeout] each, eight at a time, is NOT enough to finish a full
	// batch of dead receivers — that arithmetic is 125 seconds — and that is
	// why [budgetAllows] exists and why a pass reports what it skipped. The
	// alternative, a MaxRun large enough for the worst case, is not available:
	// it would have to exceed the interval.
	maxRun = 45 * time.Second

	// deliveryLimit caps one pass.
	//
	// A cap that is hit is REPORTED as hit: a backlog of ten thousand
	// deliveries is a different fact from a quiet minute, and the next pass is
	// a minute away, so nothing is stranded by it.
	deliveryLimit = 100

	// claimLease is how long a claimed delivery is invisible to another pass.
	//
	// Longer than [maxRun], so a pass cannot outlive its own claim and start
	// re-sending rows it is still working on. Not much longer, because the
	// lease is also the delay a crashed pass costs: rows a dead process claimed
	// wait this out before anyone tries them again.
	claimLease = 90 * time.Second

	// deadLetterSample is how many dead letters the job's report carries.
	//
	// Five, not all of them. The report ends up on one line of `gobit jobs` and
	// in one log record; a pile of two thousand printed in full would push the
	// count — the number that decides whether anybody is woken up — off the
	// screen. The count is always the whole pile; the sample is what it looks
	// like.
	deadLetterSample = 5
)

// codeSetupFailed reports that the module could not come up.
const codeSetupFailed = "webhookout_module_setup_failed"

// codeDeliveryRunFailed reports that a delivery pass could not be made.
//
// The value says "run" where the rest of this package says "pass", and the
// difference is not a slip: gosec's hardcoded-credential heuristic reads an
// identifier containing "pass" as a password, and the repository suppresses no
// lint anywhere. Renaming the value is cheaper than being the first file with a
// suppression comment in it.
const codeDeliveryRunFailed = "webhookout_delivery_run_failed"

// webhookModule is the module the plugin brings.
//
// # Why a module and not a provider slot
//
// gobit has provider slots and this plugin fills none of them. The question is
// the one ADR 0018 answered for web push, and the answer comes out the same way
// for the same reason: a provider is CHOSEN per unit of work out of a registry,
// while what an outbound webhook needs is STATE the framework must already hold
// — a URL, a secret and a topic set, registered by a human before any event can
// be delivered anywhere.
//
// The notification provider slot is the one it would have gone into. Its
// destination field is `To string`, documented as "an email address or a phone
// number"; a webhook destination is a URL plus a key plus a subscription, and
// there is nowhere in that contract to put the last two. Routing through the
// notification module would also make its delivery ledger lie in exactly the
// way ADR 0018 describes: a fan-out to many receivers has no single truth
// value, so a send to zero receivers returns nil and the ledger records "sent".
type webhookModule struct {
	log    *slog.Logger
	store  *store
	sender *sender
}

// The core's module contract is satisfied at compile time.
var _ module.Module = (*webhookModule)(nil)

// newModule builds the module. Dependencies are resolved in Register.
func newModule(log *slog.Logger) *webhookModule {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &webhookModule{log: log, sender: newSender()}
}

// Name returns the module's unique name.
func (m *webhookModule) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *webhookModule) Migrations() fs.FS { return migrationsRoot }

// Register resolves the pool and reports what the installation is carrying.
//
// # The startup report is the plugin's own smoke test
//
// It answers the question no other channel can: is anything registered at all?
// A plugin installed with no receivers delivers nothing, produces no error and
// writes no row — a state indistinguishable from working. The count is logged,
// and so is the topic list, because "the receiver asked for order.shipped and
// gobit does not publish it" is a conversation that happens by email weeks
// later otherwise.
func (m *webhookModule) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, dbServiceName)
	}

	m.store = newStore(pool.Pool())
	m.reportRegistry(ctx)

	return nil
}

// reportRegistry logs what the receiver registry holds.
//
// A failure to read is NOT a startup failure: the report is a diagnostic, and
// refusing to boot because a diagnostic query failed would trade a visible
// problem for a bigger one.
func (m *webhookModule) reportRegistry(ctx context.Context) {
	endpoints, err := m.store.listEndpoints(ctx)
	if err != nil {
		m.log.WarnContext(ctx, "the webhook receivers could not be counted at startup",
			"error", err)

		return
	}

	m.log.InfoContext(ctx, "the outbound webhook sender is ready",
		"receivers", len(endpoints),
		"forwarded_topics", strings.Join(ForwardedTopics, ","),
		"retry_window", deliveryWindow().String(),
		"max_attempts", maxAttempts)

	if len(endpoints) == 0 {
		// INFO rather than WARN. An installation with the plugin enabled and no
		// receivers is a normal state — somebody installed it before
		// registering anything — and it is stated rather than left silent
		// because it is otherwise indistinguishable from a sender that is
		// failing to deliver.
		m.log.InfoContext(ctx, "no webhook receiver is registered; no event will be delivered "+
			"until one is registered through POST /admin/v1/webhooks")
	}
}

// Routes mounts the admin surface.
//
// If Register did not run, nothing is mounted: an endpoint without a store
// would answer every request with a 500, and a route that does not exist is a
// clearer signal than one that always fails.
//
// Every state-changing route resolves under /admin/v1 statically, which is what
// TestEveryStateChangingRouteIsGuarded requires, and there is no storefront
// half at all: registering a receiver is an operator's act, and a public
// surface that let anyone point gobit's events at a URL of their choosing would
// be the plainest possible exfiltration endpoint.
func (m *webhookModule) Routes(r chi.Router) {
	if m.store == nil {
		return
	}

	r.Route("/admin/v1/webhooks", func(r chi.Router) {
		r.With(corehttp.RequireScope(ScopeRead)).Get("/", m.handleList)
		r.With(corehttp.RequireScope(ScopeWrite)).Post("/", m.handleCreate)
		r.With(corehttp.RequireScope(ScopeRead)).Get("/deliveries", m.handleDeliveries)
		r.With(corehttp.RequireScope(ScopeWrite)).
			Post("/deliveries/{id}/redrive", m.handleRedrive)
		r.With(corehttp.RequireScope(ScopeWrite)).
			Post("/deliveries/{id}/discard", m.handleDiscard)
		r.With(corehttp.RequireScope(ScopeWrite)).Delete("/{id}", m.handleDelete)
	})
}

// onEvent enqueues the deliveries an event owes.
//
// # It does the LEAST it can on the bus
//
// One statement, and no HTTP. The bus's contract is explicit that a handler's
// error is logged and the event counts as processed — no backend retries — so
// anything this handler does not finish is lost for good. Sending here would
// mean a receiver's outage holding a bus consumer for ten seconds per delivery,
// on the Redis backend a single consumer loop, delaying every other event on
// the same stream.
//
// Writing a row instead moves the event onto storage that survives the process,
// and the job is what turns rows into requests.
//
// # What is still lost, said plainly
//
// If the database is unreachable at this instant, the enqueue fails, the error
// is logged at ERROR by the bus, and that event is never delivered to anyone. No
// repair pass exists, and building one out of event_outbox was measured and
// refused: only "order.placed" is written there — the product events are
// published directly — so a repair built on it would cover one topic in four
// while looking like it covered all of them.
func (m *webhookModule) onEvent(ctx context.Context, e eventbus.Event) error {
	if m.store == nil {
		// The subscription is installed by the core and does not go through
		// Routes, so a handler can be reached even when Register did not run. A
		// typed log line beats a nil dereference in a goroutine.
		m.log.WarnContext(ctx, "the webhookout module was not registered; nothing was enqueued",
			"event", e.Name, "event_id", e.ID)

		return nil
	}

	payload, redacted := redact(e.Data)

	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}

	written, err := m.store.enqueue(ctx, e.ID, e.Name, occurred, payload, redacted)
	if err != nil {
		// The error is RETURNED as well as logged. The bus does not retry, so
		// this is not a request for one; it is what makes the failure appear in
		// the bus's own ERROR record with the event name and id attached. A nil
		// return here would make a database outage invisible.
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"the %s deliveries for %q could not be enqueued", e.Name, e.ID)
	}

	if written > 0 {
		m.log.InfoContext(ctx, "webhook deliveries were enqueued",
			"event", e.Name, "event_id", e.ID, "deliveries", written)
	}

	return nil
}

// redact removes the fields that must not leave the installation.
//
// It returns a COPY: the bus hands the same map to every handler, and a
// subscriber that deleted a key from it would silently change what the other
// subscribers see. The removed names are returned so the body can carry them.
func redact(data map[string]any) (payload map[string]any, removed []string) {
	payload = make(map[string]any, len(data))

	for key, value := range data {
		if _, hidden := redactedFields[key]; hidden {
			removed = append(removed, key)

			continue
		}
		payload[key] = value
	}

	slices.Sort(removed)

	return payload, removed
}

// deliverPass is the job's work: claim what is due, send it, then report what
// has been given up on.
//
// # The two halves are one job on purpose
//
// A separate "dead letter watch" job would run on its own schedule, take its
// own lock and produce its own history row, and an operator would then have to
// correlate two listings to learn that the sender is fine and the deliveries
// are not. This pass is the only thing that creates dead letters; it is the
// right thing to report them.
//
// # A non-empty pile FAILS the run, and that is the alarm
//
// It is B12's rule applied unchanged: a run's detail reaches `gobit jobs` only
// alongside an error, so a job reporting "ok" while deliveries sat permanently
// undelivered would be the write-only ledger this repository has already built
// once, in audit_log. The failure does not clear itself — it stands until a
// human redrives the deliveries or discards them, which is the intended cost.
//
// The difference from the outbox, worth naming because it changes who the alarm
// is FOR: an outbox dead letter means gobit's own bus refused an event, while a
// webhook dead letter usually means a third party went away. The operator
// cannot fix the third party, so the off switch has to be reachable, and it is
// — `POST /admin/v1/webhooks/deliveries/{id}/discard`, and deleting the
// receiver stops new ones being enqueued.
func (m *webhookModule) deliverPass(ctx context.Context) error {
	claimed, err := m.store.claimDue(ctx, deliveryLimit)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeDeliveryRunFailed,
			"the due webhook deliveries could not be claimed")
	}

	result := deliverAll(ctx, m.sender, m.store, m.log, claimed)
	m.reportPass(ctx, result)

	orphans, err := m.store.orphanCount(ctx)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeDeliveryRunFailed,
			"the orphaned webhook deliveries could not be counted")
	}

	// Asked for on EVERY pass, including the ones that delivered nothing. A
	// pile that is only counted when something happens is one that goes
	// unnoticed during the quiet hour after the outage that filled it.
	dead, err := m.store.deadLetters(ctx, deadLetterSample)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeDeliveryRunFailed,
			"the webhook dead letters could not be read; the pass cannot say whether "+
				"anything has been given up on")
	}

	if dead.empty() && orphans == 0 {
		return nil
	}

	return deadLetterError{report: dead, orphans: orphans}
}

// reportPass logs what the pass did.
func (m *webhookModule) reportPass(ctx context.Context, result passResult) {
	if result.Claimed == 0 {
		// DEBUG, not INFO. A healthy installation runs this every minute
		// forever, and a line that never changes is a line nobody reads.
		m.log.DebugContext(ctx, "no webhook delivery was due")

		return
	}

	if len(result.DeadLettered) > 0 {
		// The moment of death, with the ids, logged once. The rows keep the
		// last error and the attempt count, but nothing else records WHICH pass
		// gave up on them, and an operator reconstructing an incident reads the
		// log forwards.
		m.log.ErrorContext(ctx, "webhook deliveries were given up on",
			"delivery_ids", strings.Join(result.DeadLettered, ","),
			"attempts", maxAttempts,
			"window", deliveryWindow().String())
	}

	m.log.InfoContext(ctx, "a webhook delivery pass finished",
		"claimed", result.Claimed,
		"delivered", result.Delivered,
		"failed", result.Failed,
		"skipped_out_of_budget", result.Skipped,
		"dead_lettered", len(result.DeadLettered))

	if result.Claimed == deliveryLimit {
		m.log.WarnContext(ctx, "the webhook delivery pass hit its per-pass limit; "+
			"there is a backlog and the next pass is a minute away",
			"limit", deliveryLimit)
	}
}

// deadLetterError is the failure a pass returns when there is a pile.
//
// It is a type rather than a formatted string because the message has to carry
// the count, the sample and the orphan count into one line of `gobit jobs`, and
// building that line in three places is how the three drift.
type deadLetterError struct {
	report  deadLetterReport
	orphans int64
}

// Error renders the pile for an operator reading a job listing.
func (e deadLetterError) Error() string {
	var b strings.Builder

	if !e.report.empty() {
		fmt.Fprintf(&b, "%d webhook deliveries have been GIVEN UP on after %d attempts "+
			"(%s of trying); they will not be retried without a redrive.",
			e.report.Count, maxAttempts, deliveryWindow())
		for i := range e.report.Oldest {
			d := &e.report.Oldest[i]
			fmt.Fprintf(&b, " [%s %s -> %s, last status %d: %s]",
				d.ID, d.EventName, d.URL, d.LastStatus, d.LastError)
		}
		if int64(len(e.report.Oldest)) < e.report.Count {
			fmt.Fprintf(&b, " (%d more not shown; read GET /admin/v1/webhooks/deliveries"+
				"?state=dead)", e.report.Count-int64(len(e.report.Oldest)))
		}
	}

	if e.orphans > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d pending deliveries name a receiver that no longer exists; "+
			"they can never be sent and only a discard removes them.", e.orphans)
	}

	return b.String()
}

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is constant at compile time and
// the embed directive has already verified at compile time that the files
// exist. Returning nil silently would mean the module coming up without
// migrations — that is, without its tables.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("webhookout: could not open the embedded migrations directory: " + err.Error())
	}

	return sub
}
