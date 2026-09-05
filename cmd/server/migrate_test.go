package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// migrateConfig is the smallest configuration the migrate path needs.
//
// It carries NO database address: every test in this file stops before the
// first connection, and a DSN here would be an invitation for one of them to
// start reaching for a server that is not running.
func migrateConfig() config.Config {
	return config.Config{ServiceName: "gobit-test"}
}

// TestOnlyAnEmptyArgumentListCanStartTheServer reads the dispatch's SOURCE and
// proves the server has exactly one way in.
//
// # Why this is checked structurally and not by running it
//
// The behavior wanted is "no subcommand boots a server", and the only honest
// runtime proof of it would be to run every wrong invocation and watch nothing
// listen — which is what the smoke scenario does, for a couple of them, at the
// cost of a container and a compiled binary. What cannot be covered that way is
// the invocation NOBODY THOUGHT OF, and that is precisely the one that will be
// added later: a new verb whose branch falls through to serve, or a serve()
// call moved into a helper "just to tidy up". Reading the source answers for
// every argument at once.
//
// It was MEASURED that the old binary had no such invariant: `gobit migrate
// status` and `gobit --help` both booted the full server, applied every forward
// migration and listened on the configured port.
func TestOnlyAnEmptyArgumentListCanStartTheServer(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	files := productionFiles(t, fset)

	var (
		references int
		guarded    int
		locations  []string
	)

	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			decl, isFunc := n.(*ast.FuncDecl)
			if !isFunc || decl.Recv != nil {
				return true
			}
			// The declaration of serve itself is not a reference to it.
			if decl.Name.Name == "serve" {
				return true
			}

			ast.Inspect(decl.Body, func(inner ast.Node) bool {
				ident, isIdent := inner.(*ast.Ident)
				if !isIdent || ident.Name != "serve" {
					return true
				}

				references++
				locations = append(locations,
					fset.Position(ident.Pos()).String()+" in "+decl.Name.Name)

				if decl.Name.Name == "run" && insideEmptyArgsGuard(decl, ident) {
					guarded++
				}

				return true
			})

			return true
		})
	}

	require.Equal(t, 1, references,
		"serve is referenced %d time(s) (%s); it must be reachable from EXACTLY ONE place.\n"+
			"A second call site is a second way to start the server, and the one that gets "+
			"added is never the one somebody meant to add.",
		references, strings.Join(locations, ", "))
	require.Equal(t, 1, guarded,
		"the single serve call (%s) is NOT inside run's `len(args) == 0` branch.\n"+
			"That branch is the whole invariant: it was measured that before it existed, "+
			"`gobit migrate status` booted a server, applied every forward migration and "+
			"listened on the configured port.",
		strings.Join(locations, ", "))
}

// insideEmptyArgsGuard reports whether target sits inside an `if len(args) == 0`
// block of decl.
func insideEmptyArgsGuard(decl *ast.FuncDecl, target *ast.Ident) bool {
	found := false

	ast.Inspect(decl.Body, func(n ast.Node) bool {
		branch, isIf := n.(*ast.IfStmt)
		if !isIf || types.ExprString(branch.Cond) != "len(args) == 0" {
			return true
		}
		ast.Inspect(branch.Body, func(inner ast.Node) bool {
			if inner == target {
				found = true
			}

			return true
		})

		return true
	})

	return found
}

// productionFiles parses the composition root's non-test files.
func productionFiles(t *testing.T, fset *token.FileSet) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	require.NoError(t, err, "the composition root could not be read")

	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr, "%s could not be parsed", name)
		files[name] = parsed
	}

	require.NotEmpty(t, files, "no production file was found in the composition root; "+
		"this check would be BLIND")

	return files
}

// TestAnUnknownArgumentIsRefusedBeforeTheConfigurationIsRead proves the
// argument is judged first.
//
// The order matters to whoever mistyped: a binary that answered "DATABASE_URL
// cannot be empty" to `gobit migrate stauts` would send an operator to fix
// their environment over a typo in their command. Every case below runs with no
// environment configured at all, so reaching config.Load would produce that
// error instead of the one asserted.
func TestAnUnknownArgumentIsRefusedBeforeTheConfigurationIsRead(t *testing.T) {
	cases := map[string][]string{
		"an unknown verb":            {"serve"},
		"a flag where a verb goes":   {"--boot"},
		"migrate with no subcommand": {"migrate"},
		"a mistyped subcommand":      {"migrate", "stauts"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer

			err := run(args, &out)

			require.Error(t, err, "%v was accepted; the server must never be the fallback", args)
			assert.NotContains(t, err.Error(), "DATABASE_URL",
				"the configuration was read before the argument was judged: %v", err)
			assert.Contains(t, out.String(), "Usage:",
				"a refused invocation must print the usage the operator needs")
		})
	}
}

// TestHelpPrintsTheUsageAndSucceeds keeps the one non-error argument honest.
func TestHelpPrintsTheUsageAndSucceeds(t *testing.T) {
	for _, arg := range []string{"help", "-h", "-help", "--help"} {
		t.Run(arg, func(t *testing.T) {
			var out bytes.Buffer

			require.NoError(t, run([]string{arg}, &out))
			assert.Contains(t, out.String(), "start the HTTP server")
		})
	}
}

// TestUsageNamesEveryVerbTheDispatchAccepts closes the gap between the help
// text and the switch.
//
// A help text is documentation, and documentation in this repository rots by
// default: the verb gets renamed, the switch follows the constant, the prose
// does not. Generating the text from the same constants is what makes that
// impossible, and this test is what proves the generation still happens.
func TestUsageNamesEveryVerbTheDispatchAccepts(t *testing.T) {
	t.Parallel()

	usage := usageText()

	for _, verb := range []string{cmdHelp, cmdMigrate, cmdStatus, cmdDown, stuckCommand, flagSteps, flagConfirm} {
		assert.Contains(t, usage, verb, "the usage text does not mention %q", verb)
	}

	assert.Contains(t, usage, "NO arguments",
		"the usage must say how the server starts; it is the one thing that cannot be "+
			"derived from the constants")
}

// TestDownNeedsTheOwnerAsItsFirstArgument pins the argument shape.
func TestDownNeedsTheOwnerAsItsFirstArgument(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		args      []string
		wantOwner string
		wantFlags downFlags
		wantErr   string
	}{
		"no arguments at all": {
			args:    nil,
			wantErr: "FIRST argument",
		},
		"a flag where the owner goes": {
			args:    []string{"-confirm", "cart"},
			wantErr: "FIRST argument",
		},
		"the owner alone": {
			args:      []string{"cart"},
			wantOwner: "cart",
			wantFlags: downFlags{steps: defaultDownSteps},
		},
		"owner then flags": {
			args:      []string{"cart", "-steps", "3", "-confirm", "cart"},
			wantOwner: "cart",
			wantFlags: downFlags{steps: 3, confirm: "cart"},
		},
		"a confirmation for another owner still parses": {
			args:      []string{"cart", "-confirm", "order"},
			wantOwner: "cart",
			wantFlags: downFlags{steps: defaultDownSteps, confirm: "order"},
		},
		"a stray argument after the flags": {
			args:    []string{"cart", "-steps", "2", "leftover"},
			wantErr: "unexpected argument",
		},
		"an unknown flag": {
			args:    []string{"cart", "-force"},
			wantErr: "could not be parsed",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			owner, flags, err := parseDownArgs(tc.args)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantOwner, owner)
			assert.Equal(t, tc.wantFlags, flags)
		})
	}
}

// TestStepsBelowOneIsRefused guards the flag's floor by BEHAVIOR.
//
// The number is not a matter of taste: db.MigrateDown reads "steps <= 0" as
// ROLL EVERYTHING BACK, so a value that slipped through would turn `-steps 0`
// — which reads like a request that does nothing — into a request that drops
// the module's whole schema. The refusal is what stands between the two, and
// the message has to name the floor so the operator is not left guessing.
func TestStepsBelowOneIsRefused(t *testing.T) {
	t.Parallel()

	for _, steps := range []string{"0", "-1", "-99"} {
		t.Run(steps, func(t *testing.T) {
			_, _, err := parseDownArgs([]string{"cart", "-steps", steps})

			require.Error(t, err, "-steps %s was accepted; db.MigrateDown reads it as ALL", steps)
			assert.Contains(t, err.Error(), "at least 1",
				"the refusal must state the floor: %v", err)
		})
	}
}

// TestTheDefaultIsTheSmallestRollback states the safe default as a test.
//
// The default is the number an operator gets when they say nothing, so it is
// the one number that must be the least destructive one available.
func TestTheDefaultIsTheSmallestRollback(t *testing.T) {
	t.Parallel()

	_, flags, err := parseDownArgs([]string{"cart"})

	require.NoError(t, err)
	assert.Equal(t, 1, flags.steps,
		"the default rollback must be one step; anything larger makes silence destructive")
	assert.Empty(t, flags.confirm,
		"the default must carry NO confirmation, or the guard is off by default")
}

// TestStateWordsFollowTheVersion checks the three words the status table uses.
func TestStateWordsFollowTheVersion(t *testing.T) {
	t.Parallel()

	assert.Equal(t, stateNone, ownerState{version: 0}.stateText())
	assert.Equal(t, stateApplied, ownerState{version: 3}.stateText())
	assert.Equal(t, stateDirty, ownerState{version: 3, dirty: true}.stateText(),
		"dirty must win over the version: an operator reading \"applied\" next to a "+
			"half-finished migration would deploy on top of it")
	assert.Equal(t, stateDirty, ownerState{version: 0, dirty: true}.stateText())
}

// TestMigrationsTableSuffixMatchesTheDatabasePackage keeps the name printed in
// the dirty-ledger message true.
//
// The suffix is spelled twice — once here, once unexported inside
// core/db — because the operator repairing a dirty ledger by hand
// needs the table's name in the error message and cannot be sent to read a
// package's source for it. The copy is only safe while it AGREES, and the
// agreement is asserted against the real name-building function rather than
// against a second constant.
func TestMigrationsTableSuffixMatchesTheDatabasePackage(t *testing.T) {
	t.Parallel()

	table, err := db.MigrationsTable("cart")
	require.NoError(t, err)

	assert.Equal(t, "cart"+migrationsTableSuffix, table,
		"the suffix printed to operators has drifted from the table db actually uses")
}

// TestMigrationSourcesCoverTheCoreAndEveryModule proves the operator surface
// sees the same owners the startup path migrates.
//
// # The failure this closes
//
// A module registered at the composition root but missing from this list is
// invisible to the operator in the worst possible way: `migrate status` omits
// an owner whose tables ARE in the database, and `migrate down` answers
// "unknown owner" about a module the server migrates on every boot. Nothing
// errors — the report is simply short by one line — which is the same
// shape as the registration failures internal/arch was built for.
func TestMigrationSourcesCoverTheCoreAndEveryModule(t *testing.T) {
	t.Parallel()

	sources, err := migrationSources(t.Context(), migrateConfig())
	require.NoError(t, err)

	owners := ownerNames(sources)
	assert.Contains(t, owners, pgstore.MigrationOwner,
		"the core's own schema is missing; it is applied before every module's and would "+
			"be the one nobody could inspect")

	// The exact modules are not listed here on purpose — that would be the
	// second list this whole design exists to avoid. What is pinned is the
	// FLOOR and the shape: a collection that quietly emptied would otherwise
	// pass every assertion above.
	assert.GreaterOrEqual(t, len(sources), 10,
		"only %d owner(s) were collected (%s); the module registration is no longer "+
			"reaching this list", len(sources), strings.Join(owners, ", "))

	seen := map[string]bool{}
	for _, source := range sources {
		assert.NotNil(t, source.src, "%s was collected with no migration source", source.owner)
		assert.False(t, seen[source.owner],
			"%s appears twice; two sources under one owner write the same version ledger",
			source.owner)
		seen[source.owner] = true
	}
}

// TestMigrationSourcesIncludeAModuleAPluginBrings is the half a hand-written
// list would always miss.
//
// searchpg has a table and a migration of its own, and it exists in NO list at
// the composition root: it arrives through the plugin host during Install. An
// implementation that collected only the modules registered inline would pass
// every other assertion in this file and still leave the operator unable to see
// or roll back a schema their database contains.
func TestMigrationSourcesIncludeAModuleAPluginBrings(t *testing.T) {
	t.Parallel()

	cfg := migrateConfig()
	cfg.Plugins = []string{searchpg.Name}

	sources, err := migrationSources(t.Context(), cfg)
	require.NoError(t, err)

	assert.Contains(t, ownerNames(sources), searchpg.ModuleName,
		"the module the %q plugin brings is missing from the migrate surface", searchpg.Name)

	withoutPlugin, err := migrationSources(t.Context(), migrateConfig())
	require.NoError(t, err)
	assert.NotContains(t, ownerNames(withoutPlugin), searchpg.ModuleName,
		"the plugin's module appears even when the plugin is NOT installed; the list is "+
			"not being built from the configured plugins at all")
}

// TestMigrationSourcesRefuseAnUnknownPlugin proves the migrate path fails the
// same way startup does.
//
// A command that shrugged at a PLUGINS value the server rejects would report a
// status for an installation that cannot boot.
func TestMigrationSourcesRefuseAnUnknownPlugin(t *testing.T) {
	t.Parallel()

	cfg := migrateConfig()
	cfg.Plugins = []string{"no-such-plugin"}

	_, err := migrationSources(t.Context(), cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-plugin")
}

// TestAnUnknownOwnerIsNamedWithTheKnownOnes checks the dead end has a way out.
func TestAnUnknownOwnerIsNamedWithTheKnownOnes(t *testing.T) {
	t.Parallel()

	sources := []migrationSource{{owner: "cart"}, {owner: "order"}}

	var out bytes.Buffer
	err := migrateDown(t.Context(), &out, "postgres://unused", sources, []string{"crat"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "crat")
	assert.Contains(t, err.Error(), "cart",
		"an unknown owner must be answered with the list of real ones")
	assert.Empty(t, out.String(),
		"an owner that does not exist must not produce a rollback plan")
}

// TestTheRollbackPlanNamesTheConfirmationThatWouldRunIt checks the refusal is
// usable as a dry run.
//
// The plan is the only preview this surface has, so it has to carry the three
// facts an operator needs before authorizing a destructive act — which owner,
// at which version, how many steps — and the exact command line that would do
// it. A refusal that says only "no" sends them to the source for the flag name.
func TestTheRollbackPlanNamesTheConfirmationThatWouldRunIt(t *testing.T) {
	t.Parallel()

	plan := downPlanText(ownerState{owner: "cart", version: 4}, 2)

	assert.Contains(t, plan, "cart")
	assert.Contains(t, plan, "4", "the plan must state the version it starts from")
	assert.Contains(t, plan, "-"+flagConfirm+" cart",
		"the plan must spell the exact confirmation that would run it")
	assert.Contains(t, plan, "-"+flagSteps+" 2",
		"the plan must repeat the step count, or the copied command silently uses the default")
	assert.Contains(t, plan, "Nothing was changed",
		"the plan must say that nothing happened; a preview read as a receipt is worse "+
			"than no preview")
}

// brokenWriter fails every write, the way a full disk or a closed pipe does.
type brokenWriter struct{}

// Write always fails.
func (brokenWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

// TestAReportThatCouldNotBeWrittenIsAnError closes the "it looked like it
// worked" hole in the output path.
//
// Every report here is rendered into a string and written ONCE precisely so
// there is an error to return. Without this test the check is decoration: a
// `gobit migrate status > /a/full/disk` that printed nothing and exited 0 would
// tell an operator their installation is clean, and no assertion anywhere would
// notice.
func TestAReportThatCouldNotBeWrittenIsAnError(t *testing.T) {
	t.Parallel()

	require.Error(t, writeReport(brokenWriter{}, "anything"),
		"a failed write was swallowed")

	// The dispatch path too: `help` writes nothing else, so a swallowed write
	// there is a command that produces no output and reports success.
	require.Error(t, run([]string{cmdHelp}, brokenWriter{}),
		"help exited successfully having printed nothing")
}
