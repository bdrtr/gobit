package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/auth"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/internal/rig"
)

// seedCommand rebuilds the catalog every performance sentence in this
// repository is measured against.
//
// # The finding this answers
//
// The 52,000-product rig existed as rows in one Docker volume and in nothing
// else. `git ls-files` found no seed file, no seed target and no seed program;
// deploy/docker-compose.yml never creates that database at all, so a fresh
// machine running `make up` has no rig and no way to get one. Twenty-eight
// non-test files carry timing figures that rest on it. Every one of those
// sentences was one `docker compose down -v` away from being unfalsifiable
// prose, which makes the repository's own rule — a performance sentence is
// never written unmeasured — depend on a volume nobody promised to keep.
//
// # Why the rebuild is a subcommand of this binary
//
// Three other homes were weighed and all three were rejected for reasons that
// were measured rather than guessed.
//
// A .sql FILE PLUS A MAKEFILE TARGET is the cheapest to write and is audited by
// NOTHING here. The two architecture gates that read SQL walk only
// internal/modules/<module>/{migrations,queries}, so a seed file at the
// repository root sits outside both; its hand-written column list would break
// the day a migration adds a NOT NULL column, and the only signal would be a
// psql error at the moment somebody tried to rebuild the rig — which, given the
// rig survived for months at a time, could be a year later. That is not a
// hypothetical: the surviving rig's schema is ALREADY five order migrations,
// one payment migration and one product migration behind the repository, and
// missing the invoice, job, outbox and audit schemas entirely. A .sql seeder
// would have institutionalized exactly that drift. It also cannot work at all:
// three of the tables the rig needs — link_product_variant_price_set,
// link_product_variant_inventory and link_product_sales_channel — are created
// by core/link at RUN TIME from a Define call during the product module's
// bootstrap, so they exist in no migration and a freshly migrated database does
// not have them until a process has booted the modules.
//
// A SECOND BINARY UNDER cmd/ is structurally allowed — internal/arch classifies
// anything under cmd/ as legal without further audit — but it is a surface
// decision rather than a neutral one. cmd/server/main.go is twenty lines and
// its own godoc says it is deliberately the SMALLEST program that can run
// gobit, because it is also the example an embedding project copies. A second
// binary is a second thing an embedder sees, and to reach the migrations it
// would need the module registry, which is this package's job; it would end up
// either a thin shim over internal/ or a duplicate composition root.
//
// AN INTEGRATION-TAGGED HELPER NEXT TO internal/e2e/load_test.go is where the
// schema genuinely cannot rot, because it comes from registry.Bootstrap. But
// that harness builds its database with testcontainers against a HARD-CODED
// gobit_e2e, takes the DSN from the container it just started and terminates it
// on the way out; there is no DSN override anywhere in the package. A rig built
// there dies with the container, which is the very property that made the old
// rig worth rebuilding. Pointing the harness at a persistent database is a real
// change and not a flag — two of its fixtures are non-idempotent by
// construction, the admin user against a unique e-mail index and the region
// fixture against a country that can belong to one region at a time.
//
// What is left is where the work already lives. This binary's verb dispatch
// already reaches the database with the server's own configuration
// ([stuckCommand], [recoverCommand], [cmdMigrate]), and [openApplication] —
// which [runRecover] already calls — applies every core and module migration
// and then bootstraps the modules, so the link tables exist before the first
// row is written. The schema therefore comes from the modules themselves, on
// the same commit, with no second place to remember. No new binary, no new
// configuration loading, and a rig that cannot fall behind the code that built
// it.
//
// # Why it is not driven over HTTP
//
// Because it would take hours. The rig is 52,004 products, 54,000 variants,
// 54,000 price sets, 58,000 prices, 54,000 inventory items and levels and
// 52,000 channel assignments; the original was built with generate_series in
// seconds, and a request-per-product seeder would pay a round trip and the
// whole guard stack for each one. The generator therefore writes bulk SQL and
// says so out loud in the internal/rig package godoc, together with what that
// choice costs: no events, so no search index.
const seedCommand = "seed"

// The flags of `gobit seed`.
const (
	flagProducts   = "products"
	flagMulti      = "multi"
	flagCategories = "categories"
	flagTags       = "tags"
	flagChannel    = "channel"
	flagReset      = "reset"
)

// The error codes of the seed command.
const (
	codeSeedFailed = "cli_seed_failed"
	// codeResetRefused is returned when the reset was NOT attempted because the
	// confirmation did not name the database. It is separate from a failure
	// code for the reason [codeRollbackRefused] is: "nothing was touched" and
	// "it broke halfway" call for opposite next steps.
	codeResetRefused = "cli_seed_reset_refused"
)

// defaultSeedChannel is the sales channel the rig's products are assigned to
// when the operator names none.
//
// It is the name the surviving rig's own channel carries, so a rebuild lands
// the assignments where the measured catalog had them.
const defaultSeedChannel = "load"

// seedFlags are the parsed options of `gobit seed`.
type seedFlags struct {
	spec    rig.Spec
	channel string
	reset   bool
	confirm string
}

// runSeed brings the installation up and rebuilds the catalog on it.
//
// The shape is [runRecover]'s and deliberately so: load the configuration the
// server loads, take a signal-aware context, send the logs to stderr so stdout
// stays the operator's report, open the whole application, and then do the one
// thing this verb exists for. Nothing here starts a listener and nothing starts
// the job runner — [serve] does both, and it is the only branch that can.
func runSeed(args []string, out io.Writer, opts Options) error {
	flags, err := parseSeedFlags(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		// The flag set has already printed the usage. Asking what a command
		// does is not a failure.
		return nil
	case err != nil:
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Warn keeps one startup check visible that this command in particular has
	// to see: the case-folding probe in core/db warns when the cluster cannot
	// fold Turkish capitals, and three of the rig's products exist precisely to
	// be matched by a lowercase query. A rig seeded onto such a cluster is
	// still a valid catalog, but that warning is the reader's only notice that
	// its ILIKE figures will not reproduce.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), opts)
	if err != nil {
		return err
	}
	defer closeApp()

	return seedCatalog(ctx, app.container, out, flags)
}

// seedCatalog performs the rebuild against an application that is already up.
//
// The split from [runSeed] is [recoverExecution]'s: everything above is the
// environment, everything here is the decision.
func seedCatalog(ctx context.Context, c *container.Container, out io.Writer, flags seedFlags) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSeedFailed,
			"the database pool %q could not be resolved", svcDB)
	}

	database, err := rig.DatabaseName(ctx, pool)
	if err != nil {
		return err
	}

	var report strings.Builder
	fmt.Fprintf(&report, "%s: database %q\n", seedCommand, database)

	if flags.reset {
		if flags.confirm != database {
			if writeErr := writeReport(out,
				resetPlanText(database, flags.confirm, report.String())); writeErr != nil {
				return writeErr
			}

			return errors.Invalid(codeResetRefused,
				"%s: nothing was changed; repeat the database name to authorize the reset "+
					"(-%s %s)", seedCommand, flagConfirm, database)
		}

		resetStartedAt := time.Now()
		if _, err := rig.Reset(ctx, pool); err != nil {
			return err
		}
		fmt.Fprintf(&report, "%s: the rig's rows were deleted in %s (-%s).\n",
			seedCommand, time.Since(resetStartedAt).Round(time.Millisecond), flagReset)
	}

	storefront, err := container.Resolve[rigStorefront](c, auth.ServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSeedFailed,
			"the auth service %q could not be resolved", auth.ServiceName)
	}

	channel, err := ensureSalesChannel(ctx, storefront, flags.channel)
	if err != nil {
		return err
	}

	spec := flags.spec
	spec.SalesChannelID = channel.ID

	startedAt := time.Now()
	counts, err := rig.Seed(ctx, pool, spec)
	if err != nil {
		return err
	}
	elapsed := time.Since(startedAt)

	// The key is minted only after the rows are in. A key handed out over a
	// failed seed would point at a storefront with nothing behind it.
	_, key, err := storefront.CreateAPIKey(ctx, authservice.CreateAPIKeyInput{
		Type:            authmodels.APIKeyPublishable,
		Title:           "rig " + flags.channel,
		SalesChannelIDs: []string{channel.ID},
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSeedFailed,
			"the publishable key could not be minted; the catalog IS seeded, so a rerun "+
				"only needs the key")
	}

	fmt.Fprintf(&report, "%s: seeded in %s.\n\n%s\n", seedCommand, elapsed.Round(time.Millisecond), counts)
	report.WriteString(keyText(channel, key))

	return writeReport(out, report.String())
}

// rigStorefront is the NARROW surface the seed step wants from the auth module.
//
// Three calls instead of the concrete *service.Service, for [adminUsers]'s
// reason: this command depends not on the whole of auth but on the ability to
// find a channel, create one and mint a key. Naming auth's own input and output
// types is allowed here — what is forbidden is the core knowing the modules or
// the modules knowing each other, and this package is the composition root,
// which already imports every one of them.
type rigStorefront interface {
	ListSalesChannels(ctx context.Context, in authservice.ListSalesChannelsInput) (
		authservice.Page[authmodels.SalesChannel], error)
	CreateSalesChannel(ctx context.Context, in authservice.SalesChannelInput) (
		authmodels.SalesChannel, error)
	CreateAPIKey(ctx context.Context, in authservice.CreateAPIKeyInput) (
		authmodels.APIKey, string, error)
}

// That the real service satisfies this narrow surface is pinned at COMPILE
// time; the runtime type assertion inside container.Resolve would only find a
// drift at the latest possible moment.
var _ rigStorefront = (*authservice.Service)(nil)

// ensureSalesChannel finds the rig's channel by name or creates it.
//
// The lookup exists so a second run does not fail on the unique name and does
// not build a second channel that half the catalog would be assigned to. The
// channel is the one identity the rig CAN be idempotent about; the key is not
// (see [keyText]).
func ensureSalesChannel(ctx context.Context, storefront rigStorefront, name string) (
	authmodels.SalesChannel, error,
) {
	page, err := storefront.ListSalesChannels(ctx, authservice.ListSalesChannelsInput{Name: &name})
	if err != nil {
		return authmodels.SalesChannel{}, errors.Wrap(err, errors.KindOf(err), codeSeedFailed,
			"the sales channels could not be listed")
	}
	if len(page.Items) > 0 {
		return page.Items[0], nil
	}

	channel, err := storefront.CreateSalesChannel(ctx, authservice.SalesChannelInput{
		Name:        name,
		Description: "the measurement rig's storefront",
	})
	if err != nil {
		return authmodels.SalesChannel{}, errors.Wrap(err, errors.KindOf(err), codeSeedFailed,
			"the sales channel %q could not be created", name)
	}

	return channel, nil
}

// keyText renders the publishable key and the sentence that has to travel with
// it.
//
// # Why a new key every run
//
// A key's plaintext exists only in the return value of the call that minted it;
// the database keeps a SHA-256 digest and a four-character tail. That is why
// the surviving rig cannot be handed to anyone even as a dump: its storefront
// has two publishable keys and nobody alive can authenticate against either.
// The rebuild answers that by MINTING rather than restoring — and a mint cannot
// be idempotent, because "reuse the existing key" would mean printing a key
// this command is unable to read. So every run produces one more key, the line
// below says so, and revoking the ones nobody needs is an ordinary admin
// action.
func keyText(channel authmodels.SalesChannel, key string) string {
	return fmt.Sprintf(`sales channel: %s (%s)
publishable key: %s

The key is printed ONCE and is not stored in plaintext anywhere; a rerun mints
another one rather than repeating this. Use it as the %s header against the
store endpoints.
`, channel.Name, channel.ID, key, corehttp.PublishableKeyHeader)
}

// resetPlanText renders what the reset WOULD delete. It is the operator's dry
// run, and it is a REFUSAL rather than a preview that exits zero: a script that
// forgot the flag would otherwise report success over a deletion that never
// happened.
func resetPlanText(database, confirm, prefix string) string {
	var b strings.Builder

	// The two refusals are told apart, because they are different mistakes. An
	// empty confirmation is a forgotten flag; a confirmation naming another
	// database is an operator who believes they are pointed somewhere else, and
	// printing "no confirmation given" at them would answer a question they did
	// not ask.
	reason := "no confirmation given"
	if confirm != "" {
		reason = fmt.Sprintf("the confirmation names %q, but this connection is to %q",
			confirm, database)
	}

	b.WriteString(prefix)
	fmt.Fprintf(&b, "%s: REFUSED - %s. Nothing was changed.\n", seedCommand, reason)
	fmt.Fprintf(&b, "  -%s deletes every row the rig owns in %q: the generated products and\n"+
		"  everything cascading off them, the price sets, the inventory items, the\n"+
		"  warehouse and the rig's categories and tags. Sales channels and API keys\n"+
		"  are NOT touched.\n", flagReset, database)
	fmt.Fprintf(&b, "  To go ahead, repeat the database name:\n      %s %s -%s -%s %s\n",
		binaryName, seedCommand, flagReset, flagConfirm, database)

	return b.String()
}

// parseSeedFlags turns the command line into a spec.
//
// Every count is a flag with the rig's own number as its default, so the
// documented shape and the buildable shape are the same thing: `gobit seed`
// with no arguments rebuilds the 52,004-product catalog, and `gobit seed
// -products 200 -multi 20` gives a developer something to work against in under
// a second.
func parseSeedFlags(args []string) (seedFlags, error) {
	flags := flag.NewFlagSet(binaryName+" "+seedCommand, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	spec := rig.DefaultSpec()
	products := flags.Int(flagProducts, spec.SingleVariantProducts,
		"how many single-variant products to build")
	multi := flags.Int(flagMulti, spec.MultiVariantProducts,
		"how many two-variant products to build")
	categories := flags.Int(flagCategories, spec.Categories,
		"how many categories to spread the products over")
	tags := flags.Int(flagTags, spec.Tags, "how many tags to spread the products over")
	channel := flags.String(flagChannel, defaultSeedChannel,
		"the sales channel the products are assigned to; it is created if it does not exist")
	reset := flags.Bool(flagReset, false,
		"delete the rig's rows before seeding; needs -"+flagConfirm)
	confirm := flags.String(flagConfirm, "",
		"repeat the database name to authorize -"+flagReset)

	if err := flags.Parse(args); err != nil {
		return seedFlags{}, errors.Wrap(err, errors.KindInvalid, codeSeedFailed,
			"the flags of %s could not be parsed", seedCommand)
	}
	if rest := flags.Args(); len(rest) > 0 {
		return seedFlags{}, errors.Invalid(codeSeedFailed,
			"unexpected argument %q after the flags of %s", rest[0], seedCommand)
	}

	// A confirmation without -reset is refused rather than ignored. The two
	// mean opposite things to whoever typed them: an operator who passed the
	// database name believes something is about to be deleted, and a command
	// that quietly seeded on top of the old rows instead would have left them
	// measuring a catalog they think they replaced.
	if *confirm != "" && !*reset {
		return seedFlags{}, errors.Invalid(codeSeedFailed,
			"-%s was given without -%s; nothing would be deleted, so the confirmation "+
				"guards nothing", flagConfirm, flagReset)
	}

	return seedFlags{
		spec: rig.Spec{
			SingleVariantProducts: *products,
			MultiVariantProducts:  *multi,
			Categories:            *categories,
			Tags:                  *tags,
		},
		channel: *channel,
		reset:   *reset,
		confirm: *confirm,
	}, nil
}
