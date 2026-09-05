package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/bdrtr/gobit/internal/core/audit"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/core/job/jobpg"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
)

// The verbs the binary answers to. They are constants because [run] and
// [usageText] both spell them and a usage text naming a verb the dispatch does
// not have is worse than no usage text at all.
const (
	cmdHelp    = "help"
	cmdMigrate = "migrate"
	// cmdStatus reports; it is the only migrate verb that changes nothing an
	// operator asked to change (but see [migrateStatus] on the version table).
	cmdStatus = "status"
	// cmdDown is the destructive one.
	cmdDown = "down"
)

// binaryName is what the usage text and the rollback plan call this program.
//
// It is a constant rather than os.Args[0]: the plan prints a command line the
// operator is meant to COPY, and os.Args[0] is whatever path the process was
// launched with — "/tmp/go-build.../server" under `go run`, "./bin/gobit" from
// the Makefile. A copied line carrying a build cache path is worse than a name
// the reader has to adapt once. It matches the Makefile's build output.
const binaryName = "gobit"

// The error codes of the operator surface.
const (
	codeUnknownCommand = "cli_unknown_command"
	codeUsage          = "cli_usage"
	codeUnknownOwner   = "cli_unknown_migration_owner"
	// codeRollbackRefused is returned when the rollback was NOT attempted:
	// either no confirmation was given or the ledger is dirty. It is separate
	// from a failure code on purpose — "nothing was touched" and "it broke
	// halfway" call for opposite next steps.
	codeRollbackRefused   = "cli_rollback_refused"
	codeDirtyLedger       = "cli_migration_ledger_dirty"
	codeRollbackStuck     = "cli_rollback_did_not_move"
	codeReportWriteFailed = "cli_report_write_failed"
)

// defaultDownSteps is how many migrations `migrate down` rolls back when the
// operator does not say.
//
// One, and the smallest possible number is the whole point: the flag's other
// interesting value is "all of them", and giving THAT a short spelling would
// make the most destructive request the easiest to type. An operator who wants
// the full rollback reads the number off `migrate status` and passes it, which
// costs one command and forces one look at what is actually there.
const defaultDownSteps = 1

// migrationSource pairs a migration owner with the files it owns.
//
// owner is the module name; it becomes the <owner>_schema_migrations table (see
// [github.com/bdrtr/gobit/internal/core/db.MigrationsTable]), which is why the
// two must always travel together — a source applied under the wrong owner
// writes into another module's version ledger.
type migrationSource struct {
	owner string
	src   fs.FS
}

// coreMigrationSources are the schemas the CORE owns, as opposed to a module's.
//
// They are applied FIRST at startup, and they are listed here rather than
// written into [serve] so that the migrate subcommands and the startup path
// read the SAME list. A second core schema — say a future outbox — added to
// only one of them would either be missing from the status table or missing
// from every boot, and neither absence produces an error.
func coreMigrationSources() []migrationSource {
	return []migrationSource{
		{owner: pgstore.MigrationOwner, src: pgstore.Migrations()},
		{owner: jobpg.MigrationOwner, src: jobpg.Migrations()},
		{owner: outbox.MigrationOwner, src: outbox.Migrations()},
		{owner: audit.MigrationOwner, src: audit.Migrations()},
	}
}

// migrationSources returns every owner this binary migrates, in the order
// startup applies them: the core schemas first, then the modules.
//
// The modules are not listed here; they are BUILT, exactly as [serve] builds
// them, and then asked for their own sources. That is the only construction
// that cannot drift: a module added to [registerModules] or a module carried in
// by a plugin appears here on the same commit, with no second place to
// remember.
//
// The registry is deliberately created with a nil migrate function. Nothing
// here calls Bootstrap, but if something ever did, a registry that CANNOT
// migrate fails loudly instead of applying the forward migrations from inside a
// command whose whole purpose is not to.
//
// The logger discards. The module constructors log at construction (the file
// root warning, the generated JWT secret) and those sentences are about a
// server that is starting; printing them around a status table would mix an
// operator's report with a startup diary, and the JSON handler would mangle the
// table besides.
func migrationSources(ctx context.Context, cfg config.Config) ([]migrationSource, error) {
	log := slog.New(slog.DiscardHandler)

	registry := module.NewRegistry(log, nil)
	registerModules(registry, cfg, log)

	if _, _, err := installPlugins(ctx, cfg, container.New(log), registry, nil, log); err != nil {
		return nil, err
	}

	sources := coreMigrationSources()
	for _, mod := range registry.Modules() {
		src := mod.Migrations()
		if src == nil {
			// A guard on the INTERFACE, not on a known module: Module.Migrations
			// may return nil and today NONE of the registered modules does —
			// measured, all 15 return a source, notification and file included.
			// A module that ever returns nil has no version ledger, so there is
			// nothing to report and nothing to roll back, and its absence from
			// the table is the intended answer rather than a missing row.
			continue
		}
		sources = append(sources, migrationSource{owner: mod.Name(), src: src})
	}

	return sources, nil
}

// runMigrate dispatches the migrate verbs.
//
// It loads the configuration itself rather than taking it from [run]: `gobit
// help` has to work in a shell with no environment at all, and an unknown verb
// must fail on the ARGUMENT — an operator who typed `migrate stauts` is owed
// "unknown migrate command", not a complaint about DATABASE_URL.
func runMigrate(args []string, out io.Writer) error {
	if len(args) == 0 {
		if err := writeReport(out, usageText()); err != nil {
			return err
		}

		return errors.Invalid(codeUsage, "%s needs a subcommand (%s, %s)", cmdMigrate, cmdStatus, cmdDown)
	}

	verb, rest := args[0], args[1:]
	if verb != cmdStatus && verb != cmdDown {
		if err := writeReport(out, usageText()); err != nil {
			return err
		}

		return errors.Invalid(codeUsage, "unknown %s command %q (expected: %s, %s)",
			cmdMigrate, verb, cmdStatus, cmdDown)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// The context is signal-aware for a reason that is not politeness: a
	// rollback holds golang-migrate's advisory lock, and
	// [github.com/bdrtr/gobit/internal/core/db.MigrateDown] turns a canceled
	// context into a real stop — the in-flight statement is cut and the lock is
	// released. Killing the process instead would leave the lock held until the
	// connection is reaped, and every instance trying to boot would wait on it.
	//
	// WAITING ON THAT LOCK IS UNBOUNDED, and this command cannot bound it.
	// golang-migrate takes it with SELECT pg_advisory_lock() on
	// context.Background(), so neither a deadline nor Ctrl-C reaches the wait:
	// measured, a Version() call whose own context expired after 5 s had still
	// not returned 15 s later while another session held the id. It is on the
	// STATUS path too — reading a version creates the missing version table,
	// which takes the lock, once per owner — so "gobit migrate status" run
	// during a deploy's forward migration can sit silent until the deploy
	// finishes. Signals do not cut it; the only exit is the other holder
	// finishing or the connection being killed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sources, err := migrationSources(ctx, cfg)
	if err != nil {
		return err
	}

	if verb == cmdStatus {
		if len(rest) > 0 {
			return errors.Invalid(codeUsage, "%s %s takes no arguments, got %q",
				cmdMigrate, cmdStatus, strings.Join(rest, " "))
		}

		return migrateStatus(ctx, out, cfg.DatabaseURL, sources)
	}

	return migrateDown(ctx, out, cfg.DatabaseURL, sources, rest)
}

// ownerState is one owner's line in the status report.
//
// Version 0 means NO MIGRATION IS APPLIED, and it means that whether the
// database has never been migrated or was rolled all the way back: golang-migrate
// writes its "nothing applied" marker for both and
// [github.com/bdrtr/gobit/internal/core/db.Version] reports both as 0. Nothing
// here tries to tell them apart, because the two states are the same state —
// the schema this owner owns is not there — and inventing a distinction would
// need a second way of reading the ledger that could disagree with the first.
// The numbering makes 0 unambiguous: every migration file in this repository
// starts at 000001.
type ownerState struct {
	owner   string
	version uint
	dirty   bool
}

// migrateStatus prints which owner sits at which version and whether any of
// them is dirty.
//
// # This command WRITES, and the operator has to know
//
// Reading the version goes through golang-migrate's driver, and that driver
// CREATES the <owner>_schema_migrations table when it is missing (the side
// effect is on
// [github.com/bdrtr/gobit/internal/core/db.Version]'s godoc). Run against a
// database that has never been migrated, this command therefore leaves one
// empty version table per owner behind. It is measured, it is harmless — the
// next boot would create exactly those tables — and it is stated in the footer
// rather than hidden, because an operator who runs a "status" command and then
// finds new tables has been lied to by the command's name.
//
// Avoiding it would mean probing information_schema first, which is a SECOND
// way of reading the ledger; the day the driver changes where it keeps the
// version, the probe and the truth part company and the table reports a state
// nothing has.
//
// # Why a dirty owner makes this command FAIL
//
// The table is printed either way — the diagnosis is the point of running it —
// but the exit status is non-zero, because a dirty ledger means the schema is
// stranded between two versions and the next boot of THAT module will refuse to
// go forward. An operator scripting a pre-deploy check reads the exit code, not
// the table.
func migrateStatus(ctx context.Context, out io.Writer, databaseURL string, sources []migrationSource) error {
	states := make([]ownerState, 0, len(sources))
	for _, source := range sources {
		state, err := readOwnerState(ctx, databaseURL, source.owner)
		if err != nil {
			return err
		}
		states = append(states, state)
	}

	if err := writeReport(out, statusText(states)); err != nil {
		return err
	}

	dirty := make([]string, 0, len(states))
	for _, state := range states {
		if state.dirty {
			dirty = append(dirty, state.owner)
		}
	}
	if len(dirty) == 0 {
		return nil
	}

	return errors.Conflict(codeDirtyLedger,
		"%s: a previous migration failed halfway and the schema is stranded between two "+
			"versions; the next start will REFUSE to migrate %s forward. Inspect what the "+
			"failed migration left behind, finish or undo it by hand, then correct the "+
			"version in <owner>%s",
		strings.Join(dirty, ", "),
		pluralOwners(len(dirty)),
		migrationsTableSuffix)
}

// writeReport writes a finished operator report to out in ONE call.
//
// Every report in this file is rendered into a string first, so there is
// exactly one write and exactly one error to answer for. Assembling a report
// out of a dozen unchecked Fprintf calls has the failure mode this repository
// keeps paying for: the write fails on the third line, half a status table
// reaches the terminal, and the process exits 0 — `migrate status >
// /a/full/disk` would report a clean installation it never managed to print.
func writeReport(out io.Writer, text string) error {
	if _, err := io.WriteString(out, text); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, codeReportWriteFailed,
			"the report could not be written to the output")
	}

	return nil
}

// statusText renders the status table and its footer.
//
// The columns are padded by hand rather than by text/tabwriter, for a reason
// that is about errors and not about taste: a tabwriter writes THROUGH to the
// output, so every cell write and the final Flush each carry an error, and a
// half-flushed table is a report that lies. Building the whole text first
// leaves one write, and a strings.Builder cannot fail.
func statusText(states []ownerState) string {
	const (
		ownerHeader   = "OWNER"
		versionHeader = "VERSION"
		gap           = 2
	)

	ownerWidth, versionWidth := len(ownerHeader), len(versionHeader)
	for _, state := range states {
		ownerWidth = max(ownerWidth, len(state.owner))
		versionWidth = max(versionWidth, len(state.versionText()))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-*s%-*s%s\n",
		ownerWidth+gap, ownerHeader, versionWidth+gap, versionHeader, "STATE")
	for _, state := range states {
		fmt.Fprintf(&b, "%-*s%-*s%s\n",
			ownerWidth+gap, state.owner,
			versionWidth+gap, state.versionText(),
			state.stateText())
	}

	fmt.Fprintf(&b, "\n%d owner(s). Version 0 (%q) means this owner's schema is not there:\n"+
		"either it was never migrated or it was rolled all the way back.\n"+
		"Reading a version CREATES the owner's version table when it is missing, so on a\n"+
		"fresh database this command leaves one empty %s table behind per owner.\n",
		len(states), stateNone, migrationsTableSuffix)

	return b.String()
}

// The words the status table uses for an owner's state. They are constants
// because the footer of [migrateStatus] and the refusal of [migrateDown] name
// them in prose, and a table saying one thing while the sentence under it says
// another is the cheapest kind of lie to ship.
const (
	stateApplied = "applied"
	stateNone    = "nothing applied"
	stateDirty   = "DIRTY"
)

// migrationsTableSuffix is the suffix of the per-owner version table.
//
// It duplicates an unexported constant of
// [github.com/bdrtr/gobit/internal/core/db] and is used ONLY in prose: the
// operator who has to repair a dirty ledger by hand needs the table's name, and
// sending them to go read a package's source for it would be the difference
// between an actionable refusal and a dead end. The name it produces is checked
// against the real one in TestMigrationsTableSuffixMatchesTheDatabasePackage.
const migrationsTableSuffix = "_schema_migrations"

// versionText renders the version column.
func (s ownerState) versionText() string {
	return fmt.Sprintf("%d", s.version)
}

// stateText renders the state column.
func (s ownerState) stateText() string {
	switch {
	case s.dirty:
		return stateDirty
	case s.version == 0:
		return stateNone
	default:
		return stateApplied
	}
}

// readOwnerState reads one owner's version out of the database.
func readOwnerState(ctx context.Context, databaseURL, owner string) (ownerState, error) {
	version, dirty, err := db.Version(ctx, databaseURL, owner)
	if err != nil {
		return ownerState{}, err
	}

	return ownerState{owner: owner, version: version, dirty: dirty}, nil
}

// migrateDown rolls ONE owner back, and only after the operator has said the
// owner's name twice.
//
// # Why a confirmation at all, and why THIS one
//
// Rolling forward and rolling back are not symmetric. A forward migration that
// was not wanted can be rolled back; a `.down.sql` DROPs what its `.up.sql`
// created, so the rows are gone and rolling forward again does not bring them
// back. The command therefore does nothing at all unless -confirm repeats the
// owner named on the command line.
//
// The alternatives were weighed and rejected:
//
//   - A bare -yes flag travels with the command when it is copied out of a
//     runbook, so it confirms the runbook's owner rather than this
//     invocation's. Repeating the owner cannot be copied wrong without also
//     getting the positional argument wrong.
//   - A prompt on stdin cannot be answered where this command is most likely to
//     run — a Kubernetes Job, a CI step, `kubectl exec` without a TTY — and
//     would either hang forever or read EOF and be taken for a "no".
//
// # The refusal IS the preview
//
// Without -confirm the command still reads the ledger and prints what it WOULD
// do — which owner, at which version, how many steps — and then returns an
// error. Printing the plan and exiting 0 was rejected: a script that forgot the
// flag would report success over a rollback that never happened, which is the
// silent no-op this repository keeps paying for. A refusal that a script
// notices and a human can read as a dry run is both.
//
// # A dirty ledger is refused, and -confirm does not override it
//
// Dirty means a previous run failed halfway: some of the current version's
// `.up.sql` ran and some did not. The matching `.down.sql` was written against
// the state where ALL of it ran, so running it now is a guess — it can fail on
// the first missing object and leave the ledger dirty at a second point, or
// succeed while dropping a table that another statement never got to fill.
// There is no confirmation an operator can give that makes that guess safe,
// because the command does not know what the half-applied schema contains. The
// only correct next step is a human reading the failed migration, so that is
// what the error asks for.
//
// # What is reported afterwards is what the DATABASE says
//
// The closing line is not "rolled back N steps"; it is the version read BACK
// out of the ledger after the call. golang-migrate can legitimately move fewer
// steps than were asked for (asking for more than exist is not an error), so a
// message built from the request would be a number the operator believes and
// the schema does not have. If the version did not move at all, that is
// reported as a failure.
func migrateDown(
	ctx context.Context,
	out io.Writer,
	databaseURL string,
	sources []migrationSource,
	args []string,
) error {
	owner, flags, err := parseDownArgs(args)
	if err != nil {
		if writeErr := writeReport(out, usageText()); writeErr != nil {
			return writeErr
		}

		return err
	}

	source, found := findSource(sources, owner)
	if !found {
		return errors.NotFound(codeUnknownOwner,
			"unknown migration owner %q (known: %s)", owner, strings.Join(ownerNames(sources), ", "))
	}

	before, err := readOwnerState(ctx, databaseURL, owner)
	if err != nil {
		return err
	}

	if before.dirty {
		if writeErr := writeReport(out, fmt.Sprintf(
			"%s %s: %s is at version %d and the ledger is %s.\nNothing was changed.\n",
			cmdMigrate, cmdDown, owner, before.version, stateDirty)); writeErr != nil {
			return writeErr
		}

		return errors.Conflict(codeDirtyLedger,
			"%s: a previous migration failed halfway, so the schema sits between version %d "+
				"and the next one and the %s.down.sql for %d assumes its .up.sql ran WHOLE. "+
				"Read what the failed migration left behind, finish or undo it by hand, then "+
				"set the version in %s%s. No confirmation overrides this",
			owner, before.version, owner, before.version, owner, migrationsTableSuffix)
	}

	if flags.confirm != owner {
		if writeErr := writeReport(out, downPlanText(before, flags.steps)); writeErr != nil {
			return writeErr
		}

		return errors.Invalid(codeRollbackRefused,
			"%s %s: nothing was changed; repeat the owner name to authorize it "+
				"(-%s %s)", cmdMigrate, cmdDown, flagConfirm, owner)
	}

	// This line goes out BEFORE the rollback, and separately from the one
	// below: a rollback can take minutes on a large table, and an operator
	// staring at a silent terminal cannot tell a slow migration from a hung one.
	if err := writeReport(out, fmt.Sprintf(
		"%s %s: rolling %s back %d step(s) from version %s...\n",
		cmdMigrate, cmdDown, owner, flags.steps, before.versionText())); err != nil {
		return err
	}

	if err := db.MigrateDown(ctx, databaseURL, source.src, owner, flags.steps); err != nil {
		return err
	}

	after, err := readOwnerState(ctx, databaseURL, owner)
	if err != nil {
		return err
	}

	closing := fmt.Sprintf("%s %s: %s is now at version %s (%s).\n",
		cmdMigrate, cmdDown, owner, after.versionText(), after.stateText())
	if before.version == 0 {
		// There was nothing to roll back. Saying so is not a failure: it is the
		// normal outcome db.MigrateDown's godoc promises for an environment
		// that was never migrated.
		closing += fmt.Sprintf("%s had nothing applied; there was nothing to roll back.\n", owner)
	}
	if err := writeReport(out, closing); err != nil {
		return err
	}

	if after.dirty {
		return errors.Conflict(codeDirtyLedger,
			"%s: the rollback failed partway and left the ledger DIRTY at version %d; the "+
				"schema is now between two versions and the next start will refuse to migrate "+
				"this module forward", owner, after.version)
	}

	if before.version == 0 {
		return nil
	}

	if after.version == before.version {
		return errors.Internal(codeRollbackStuck,
			"%s: %d step(s) were requested but the version is still %d; the ledger did not "+
				"move and the schema was NOT rolled back", owner, flags.steps, after.version)
	}

	return nil
}

// The flag names of `migrate down`.
const (
	flagSteps   = "steps"
	flagConfirm = "confirm"
)

// downFlags are the parsed flags of `migrate down`.
type downFlags struct {
	steps   int
	confirm string
}

// parseDownArgs pulls the owner and the flags out of `migrate down`'s arguments.
//
// The owner is required to be the FIRST argument and the flags to follow it.
// That is not the [flag] package's own habit — it stops at the first
// non-flag — and the alternative reorderings were worse: making the owner a
// flag too (-owner cart -confirm cart) reads like a form, and scanning for
// "the first argument that does not start with a dash" would happily take the
// 2 in `-steps 2 cart` for the owner name. One fixed position has no such
// corner.
func parseDownArgs(args []string) (owner string, parsed downFlags, err error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", downFlags{}, errors.Invalid(codeUsage,
			"%s %s needs the owner as its FIRST argument (%s %s <owner> [-%s N] -%s <owner>)",
			cmdMigrate, cmdDown, cmdMigrate, cmdDown, flagSteps, flagConfirm)
	}

	set := flag.NewFlagSet(cmdMigrate+" "+cmdDown, flag.ContinueOnError)
	// The default help output goes to stderr and describes a flag set whose
	// name is not the command; [usageText] describes the real surface and the
	// caller prints it.
	set.SetOutput(io.Discard)
	steps := set.Int(flagSteps, defaultDownSteps, "how many migrations to roll back")
	confirm := set.String(flagConfirm, "", "repeat the owner name to authorize the rollback")

	if err := set.Parse(args[1:]); err != nil {
		return "", downFlags{}, errors.Wrap(err, errors.KindInvalid, codeUsage,
			"the flags of %s %s could not be parsed", cmdMigrate, cmdDown)
	}
	if rest := set.Args(); len(rest) > 0 {
		return "", downFlags{}, errors.Invalid(codeUsage,
			"unexpected argument %q after the flags of %s %s", rest[0], cmdMigrate, cmdDown)
	}

	// Zero and negative are REFUSED rather than accepted with a meaning.
	// db.MigrateDown reads "steps <= 0" as ALL OF THEM, so a mistyped -steps 0
	// would drop a module's entire schema while looking like a request that did
	// nothing. The floor is stated here, in the message the operator gets.
	if *steps < 1 {
		return "", downFlags{}, errors.Invalid(codeUsage,
			"-%s must be at least 1, got %d; to roll everything back, pass the version "+
				"%s %s reports", flagSteps, *steps, cmdMigrate, cmdStatus)
	}

	return args[0], downFlags{steps: *steps, confirm: *confirm}, nil
}

// downPlanText renders what the rollback WOULD do. It is the operator's dry run.
func downPlanText(before ownerState, steps int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s %s: REFUSED — no confirmation given. Nothing was changed.\n",
		cmdMigrate, cmdDown)
	fmt.Fprintf(&b, "  owner:   %s\n", before.owner)
	fmt.Fprintf(&b, "  version: %s (%s)\n", before.versionText(), before.stateText())
	fmt.Fprintf(&b, "  steps:   %d\n", steps)
	fmt.Fprintf(&b, "  Rolling back runs .down.sql files, which DROP what their .up.sql\n"+
		"  created. The rows they hold go with them and rolling forward again\n"+
		"  does not bring them back.\n")
	fmt.Fprintf(&b, "  To go ahead, repeat the owner name:\n      %s %s %s %s -%s %d -%s %s\n",
		binaryName, cmdMigrate, cmdDown, before.owner, flagSteps, steps, flagConfirm, before.owner)

	return b.String()
}

// findSource looks an owner up among the collected sources.
func findSource(sources []migrationSource, owner string) (migrationSource, bool) {
	for _, source := range sources {
		if source.owner == owner {
			return source, true
		}
	}

	return migrationSource{}, false
}

// ownerNames returns the known owners sorted, for the error message.
func ownerNames(sources []migrationSource) []string {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.owner)
	}
	slices.Sort(names)

	return names
}

// pluralOwners picks the noun the dirty-ledger message needs.
func pluralOwners(n int) string {
	if n == 1 {
		return "module"
	}

	return "modules"
}

// usageText renders the whole operator surface.
//
// Every line of it is generated from the same constants the dispatch switches
// on, so a verb cannot be renamed without the help text following. The one
// thing spelled out in prose is the sentence that matters most and cannot be
// derived: the server starts with NO arguments.
func usageText() string {
	return fmt.Sprintf(`%s %s — headless commerce, one binary.

Usage:
  %s %-34s start the HTTP server
  %s %-34s report every owner's schema version
  %s %-34s roll ONE owner back
  %s %-34s list executions left half-done (read only)
  %s %-34s compensate ONE half-done execution
  %s %-34s report each scheduled job's last run
  %s %-34s print this text

%s %s flags:
  -%-15s how many migrations to roll back (default %d, minimum 1)
  -%-15s repeat the owner name to authorize the rollback

%s flags:
  -%-15s repeat the execution id to authorize the compensation

The server starts when there are NO arguments and in no other way; no
subcommand starts one. Forward migrations stay automatic at startup —
there is deliberately no "migrate up", so a deploy cannot forget it.
`,
		binaryName, version,
		binaryName, "",
		binaryName, cmdMigrate+" "+cmdStatus,
		binaryName, cmdMigrate+" "+cmdDown+" <owner> [flags]",
		binaryName, stuckCommand+" [flags]",
		binaryName, recoverCommand+" <execution-id> [flags]",
		binaryName, jobsCommand,
		binaryName, cmdHelp,
		cmdMigrate, cmdDown,
		flagSteps+" N", defaultDownSteps,
		flagConfirm+" OWNER",
		recoverCommand,
		flagConfirm+" ID")
}
