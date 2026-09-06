// Package arch_test checks the architectural invariants AUTOMATICALLY.
//
// The tests here contain no production code; they scan the repository and enforce
// the Architectural Principles of plan Section 2 and the conventions of Section 8.
//
// Why here: these violations used to come up as review findings in every phase and
// were fixed by hand. If a rule is caught in a review round it can be caught in a
// test; and once it is caught in a test it is never written again. Two classes
// golangci-lint cannot catch are here in particular: unexported godoc format and
// cross-module foreign keys in SQL files.
package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
)

const (
	modulePath = "github.com/bdrtr/gobit"
	repoRoot   = "../.."
	modulesDir = "internal/modules"
	// goModFileName is the file DECLARING the repository's module path;
	// [modulePrefix] validates the hand-repeated [modulePath] constant against it.
	goModFileName = "go.mod"
	// migrationsDirName is the subdirectory the module migrations live in.
	//
	// The constant sits in one place because THREE checks (cross-module FK, the
	// up/down pair and the integration run) look for the same directory name: when the
	// name drifts, all three find no file and all three pass having found nothing.
	// That the name still holds is validated by the file COUNT found in each of them.
	migrationsDirName = "migrations"
)

// productionTrees are the top-level directories holding shipped Go source.
//
// Seven audits used to keep a private copy of this list, each with a comment
// saying it had to be edited by hand when a tree was added. Promoting the
// twelve core packages out of internal/ (ADR 0026) added a tree, and every one
// of those copies narrowed AT ONCE: a scan whose root list misses a tree finds
// nothing there and passes having found nothing. The list therefore lives here
// once, and [TestTheProductionTreeListCoversTheRepository] checks it against
// the repository — the next promotion cannot blind an audit without failing
// that test first.
//
// The direction of a stale list is loud on the consumer side and silent on the
// declaration side: a consumer in an unscanned tree counts as ABSENT, so a
// capability that was produced is declared dead and the error printed explains
// the wrong thing. That asymmetry is why the declarations never consult this
// list — they come from the same scan.
var productionTrees = []string{repositoryRoot, "cmd", "core", "internal", "plugins"}

// repositoryRoot names the tree that is the repository root itself.
//
// The published facade is a package AT the root (ADR 0027), so the root holds
// production Go source without being a directory anyone would think to list.
// [goFiles] reads it at depth one; every other tree is read whole.
const repositoryRoot = "."

// TestTheProductionTreeListCoversTheRepository keeps [productionTrees] honest.
//
// Every audit in this package narrows to the trees on that list. A tree missing
// from it is not a failure anywhere: the walk simply does not go there, finds
// no violation, and reports success. This test is the one place the list is
// compared against what is actually on disk, so the omission has somewhere to
// fail.
func TestTheProductionTreeListCoversTheRepository(t *testing.T) {
	entries, err := os.ReadDir(repoRoot)
	require.NoError(t, err, "the repository root could not be read")

	listed := map[string]bool{}
	for _, tree := range productionTrees {
		listed[tree] = true
	}

	// The root is not one of its own directory entries, so it is counted here.
	require.True(t, listed[repositoryRoot],
		"the repository root holds the published facade and is not in productionTrees")
	require.True(t, holdsProductionGo(t, repoRoot),
		"no non-test Go file was found at the repository root.\n"+
			"The published facade lives there (ADR 0027); if it moved, the audits that "+
			"read the root are now reading nothing and passing.")
	holdsSource := 1

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !holdsProductionGo(t, filepath.Join(repoRoot, entry.Name())) {
			continue
		}
		holdsSource++
		require.True(t, listed[entry.Name()],
			"the %q tree holds production Go source and is not in productionTrees.\n"+
				"Every audit in this package walks that list; a tree missing from it is "+
				"not scanned, and an unscanned tree produces no finding rather than an "+
				"error. Add it there, in the one place it is written.", entry.Name())
	}

	require.Equal(t, len(productionTrees), holdsSource,
		"productionTrees names %d trees but only %d of them hold production Go source.\n"+
			"A name left behind after a tree is emptied or renamed is worse than a "+
			"missing one: the audits keep walking a path that no longer exists and the "+
			"count of what they checked silently drops.", len(productionTrees), holdsSource)
}

// holdsProductionGo reports whether the tree contains a non-test .go file.
func holdsProductionGo(t *testing.T, dir string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The walk's own root declares a module when it IS the repository
			// root; skipping it there would end the walk before it read a file.
			if path != dir && isNestedModule(path) {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		found = true

		return filepath.SkipAll
	})
	require.NoError(t, err, "%s could not be walked", dir)

	return found
}

// treeFiles returns the .go files of ONE production tree.
//
// The repository root is a tree like the others except in one respect: every
// other tree is BELOW it. Reading it recursively would walk cmd/, core/,
// internal/ and plugins/ a second time, so each per-file audit would report the
// same violation twice and the file counts the audits use to prove they read
// something would stop meaning anything. The root is therefore read at depth
// one, which is exactly the package the published facade lives in (ADR 0027).
func treeFiles(t *testing.T, tree string) []string {
	t.Helper()

	files := goFiles(t, filepath.Join(repoRoot, tree))
	if tree != repositoryRoot {
		return files
	}

	shallow := files[:0]
	for _, file := range files {
		if filepath.Clean(filepath.Dir(file)) == filepath.Clean(repoRoot) {
			shallow = append(shallow, file)
		}
	}

	return shallow
}

// treeProductionFiles is [treeFiles] without the _test.go files.
func treeProductionFiles(t *testing.T, tree string) []string {
	t.Helper()

	files := productionFiles(t, filepath.Join(repoRoot, tree))
	if tree != repositoryRoot {
		return files
	}

	shallow := files[:0]
	for _, file := range files {
		if filepath.Clean(filepath.Dir(file)) == filepath.Clean(repoRoot) {
			shallow = append(shallow, file)
		}
	}

	return shallow
}

// treeOf reports which production tree a repository-relative path belongs to.
//
// A path with no separator is a file AT the repository root, and its tree is
// the root itself — not, as a plain split would say, a tree named after the
// file.
func treeOf(path string) string {
	slash := strings.Index(path, "/")
	if slash < 0 {
		return repositoryRoot
	}

	return path[:slash]
}

// isNestedModule reports whether the directory declares a module of its own.
//
// A directory with a go.mod is a SEPARATE module: its files are not part of
// this one, `go build ./...` never reaches them, and the rules audited here do
// not apply to them. examples/plugin is one on purpose — see
// [TestTheOutOfTreeExamplesCompile].
func isNestedModule(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, goModFileName))

	return err == nil
}

// moduleNames returns the names of the commerce modules in the repository.
//
// An empty result is NOT ACCEPTED: every check walking the modules takes this list
// as its input, and a walk running on an empty list passes without finding any
// violation. The error is given here rather than in the callers — sitting in one
// place, no caller can forget to add it.
func moduleNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot, modulesDir))
	if err != nil {
		t.Fatalf("the modules could not be read: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}

	require.NotEmpty(t, names,
		"there is NO module directory under %s; every check walking the modules (import "+
			"isolation, cross-module FK, the migration pair, the money integer, the HTTP "+
			"surface) runs on an empty set and passes having found nothing.\n"+
			"If the modules moved to another tree, modulesDir has to move too; if it does "+
			"not, the check is not removed, it only goes BLIND — and a blind check is worse "+
			"than no check at all.", modulesDir)

	return names
}

// modulePrefix returns the import path prefix of the module packages and verifies
// that the [modulePath] constant it rests on still agrees with the module path in
// go.mod.
//
// The prefix is a hand-repeated constant and all three import invariants (module,
// workflow, plugin) depend on it ENTIRELY: the comparison is the question "does this
// import start with our module path". On the day the path in go.mod changes the
// prefix matches no import at all, all three checks find no violation and ALL THREE
// stay silently green — the rule is not removed, only its check is over.
//
// What it does NOT GUARANTEE: the prefix being right does not show that the scan
// walks the right files; that the file set does not stay empty is verified
// separately by the callers.
func modulePrefix(t *testing.T) string {
	t.Helper()

	ham, err := os.ReadFile(filepath.Join(repoRoot, goModFileName))
	require.NoError(t, err, "%s could not be read", goModFileName)

	declared := ""
	for _, line := range strings.Split(string(ham), "\n") {
		if kalan, bulundu := strings.CutPrefix(strings.TrimSpace(line), "module "); bulundu {
			declared = strings.TrimSpace(kalan)
			break
		}
	}

	require.Equal(t, declared, modulePath,
		"the modulePath constant (%q) has DRIFTED from the module path %s declares (%q).\n"+
			"The import scans find a violation by matching this prefix; when the prefix does "+
			"not hold, no import counts as a violation and ALL THREE of the "+
			"module/workflow/plugin isolation checks stay green in a vacuum. The constant "+
			"has to be updated together with go.mod.", modulePath, goModFileName, declared)

	return modulePath + "/" + modulesDir + "/"
}

// goFiles returns all the .go files under the given root, recursively.
//
// Callers that want ONE production tree want [treeFiles] instead: this walk
// from the repository root would read every tree at once, which is right for
// the audits that mean the whole repository and wrong for the ones that mean a
// tree.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s could not be scanned: %v", root, err)
	}
	return out
}

// TestModulesDoNotImportEachOther enforces Principle 2.1/2.4 and ADR 0001.
//
// depguard applies the same rule on the golangci-lint side; this test is the second
// line of defense and catches it anyway if the rule list in .golangci.yml is
// FORGOTTEN while a module is being added.
func TestModulesDoNotImportEachOther(t *testing.T) {
	modules := moduleNames(t)
	prefix := modulePrefix(t)

	for _, mod := range modules {
		root := filepath.Join(repoRoot, modulesDir, mod)
		for _, file := range goFiles(t, root) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("%s could not be parsed: %v", file, err)
			}
			for _, imp := range parsed.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if !strings.HasPrefix(path, prefix) {
					continue
				}
				other := strings.SplitN(strings.TrimPrefix(path, prefix), "/", 2)[0]
				if other != mod {
					t.Errorf("%s: module %q imports module %q (Principle 2.4 / ADR 0001).\n"+
						"Access has to go through the narrow interface the consumer defines in ITS "+
						"OWN package and through name resolution from the container.", file, mod, other)
				}
			}
		}
	}
}

// TestWorkflowsDoNotImportModules enforces ADR 0006.
//
// internal/workflows is not the core (Principle 2.4 does not bind it) and is not a
// module either (the depguard rules are for internal/modules), that is, no existing
// rule restricts it. But had it imported the modules directly it would turn into a
// single node knowing every module, could not be tested without a real database,
// and taking one module out into a separate service would break the workflows at
// compile time.
//
// Access has to go through the narrow interface the workflow defines in ITS OWN
// package and through name resolution from the container.
func TestWorkflowsDoNotImportModules(t *testing.T) {
	// If the tree is missing the check is NOT SKIPPED, it FAILS: a skipped test looks
	// like a passing one in the run output and ADR 0006 is left unchecked from that
	// moment on. The path is taken through [workflowsDirName] so that when the tree
	// moves, two checks at once (the wiring and the import ban) hear about it from the
	// same constant.
	root := filepath.Join(repoRoot, workflowsDirName)
	require.DirExists(t, root,
		"the %s tree is MISSING; no file is left for ADR 0006's import ban to walk and the "+
			"check stays green in a vacuum. If the tree moved, workflowsDirName has to move too.", workflowsDirName)

	files := goFiles(t, root)
	require.NotEmpty(t, files,
		"there is NO Go file under %s; the directory stands but has left nothing to walk.\n"+
			"The check can only find a violation in a file's import list; an empty file set "+
			"shows not that the rule was removed but that it went BLIND.", workflowsDirName)

	prefix := modulePrefix(t)
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s could not be parsed: %v", file, err)
		}
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, prefix) {
				t.Errorf("%s: the workflow imports module %q (ADR 0006).\n"+
					"A workflow has to define the NARROW surface it needs in its own package and "+
					"resolve the concrete service from the container BY NAME.",
					file, strings.TrimPrefix(path, prefix))
			}
		}
	}
}

// createTableRe and referencesRe catch the table declarations and the foreign key
// targets in the migration files.
var (
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([a-z_][a-z0-9_]*)"?`)
	referencesRe  = regexp.MustCompile(`(?is)REFERENCES\s+"?([a-z_][a-z0-9_]*)"?`)
	// sqlCommentRe catches line and block comments. The comments are stripped BEFORE
	// the scan: an explanation like "-- no REFERENCES to another module's table" would
	// otherwise be taken for a violation.
	sqlLineCommentRe = regexp.MustCompile(`--[^\n]*`)
	sqlBlokYorumRe   = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// stripSQLComments removes the comments from an SQL body.
func stripSQLComments(body string) string {
	body = sqlBlokYorumRe.ReplaceAllString(body, " ")
	return sqlLineCommentRe.ReplaceAllString(body, " ")
}

// migrationSet is one directory of migrations and the name it is audited under.
type migrationSet struct {
	// owner is the repository-relative path of the package that owns the
	// directory ("internal/modules/product", "core/audit"). It is what the
	// failure messages name, because it is what somebody has to go and open.
	owner string
	// dir is the migrations directory itself.
	dir string
}

// migrationDirs finds EVERY directory of migrations in the repository.
//
// # Why this is not moduleNames
//
// Three gates used to derive their input from [moduleNames] and reach
// internal/modules/<mod>/migrations by hand: the reversibility pair, the
// cross-module foreign key, and the real up/down round trip. The rules they
// enforce are not about modules. "A migration is reversible" and "a foreign key
// stays inside what this migration set creates" are properties of a MIGRATION,
// and a migration set that is not a module's obeys them for the same reasons
// and breaks in the same way.
//
// Measured on 2026-09-06: the repository holds 24 migration directories and
// those three gates read 16 of them. The eight outside were the four plugins
// (paymentpaytr, searchpg, webhookout, webpush) and the four core schemas
// (core/audit, core/eventbus/outbox, internal/core/workflow/pgstore,
// internal/core/job/jobpg). The core four are the worse omission by a distance:
// openApplication applies them BEFORE any module migration on every startup, so
// a down that fails there leaves golang-migrate's ledger dirty in front of the
// whole boot — which is, verbatim, the Phase 5 fault
// [TestMigrationsCanReallyBeRolledBack] was written for. All eight were
// compliant when they were measured. They were compliant unaudited.
//
// A directory with no .sql in it is not returned: it carries no migration, and
// counting it would let an empty directory stand in for the tree the walk was
// supposed to find.
func migrationDirs(t *testing.T) []migrationSet {
	t.Helper()

	var sets []migrationSet
	collect := func(dir string) {
		sqls, globErr := filepath.Glob(filepath.Join(dir, "*.sql"))
		require.NoError(t, globErr, "%s could not be scanned", dir)
		if len(sqls) == 0 {
			return
		}
		owner, relErr := filepath.Rel(repoRoot, filepath.Dir(dir))
		require.NoError(t, relErr, "%s could not be made relative to %s", dir, repoRoot)
		sets = append(sets, migrationSet{owner: filepath.ToSlash(owner), dir: dir})
	}

	for _, tree := range productionTrees {
		root := filepath.Join(repoRoot, tree)

		// The repository root is a tree like the others except that every other
		// tree is BELOW it, so walking it recursively would find every migration
		// directory a SECOND time — each gate would then report the same missing
		// down pair twice and the counters proving the walk read something would
		// stop meaning anything. It is read at depth one, exactly as [treeFiles]
		// reads it, which is the only depth a directory can be at without also
		// belonging to one of the named trees.
		if tree == repositoryRoot {
			collect(filepath.Join(root, migrationsDirName))

			continue
		}

		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			// A directory declaring a module of its own is a separate module and
			// its migrations are not this binary's; examples/plugin is one.
			if path != root && isNestedModule(path) {
				return filepath.SkipDir
			}
			if entry.Name() != migrationsDirName {
				return nil
			}
			collect(path)

			return filepath.SkipDir
		})
		require.NoError(t, err, "%s could not be walked for migrations", tree)
	}

	// The reader's floor, and it is checked against a list built somewhere else.
	// A counter comparing this walk with itself would go quiet together with it;
	// every module is known to carry migrations, so their absence is the walk
	// breaking rather than the repository changing.
	for _, mod := range moduleNames(t) {
		owner := modulesDir + "/" + mod
		found := slices.ContainsFunc(sets, func(set migrationSet) bool { return set.owner == owner })
		require.True(t, found,
			"no migration directory was found for the %q module; the migration walk has "+
				"gone BLIND.\nEvery module carries one, so finding none means the directory "+
				"name drifted from %q or the walk stopped descending — and a gate whose input "+
				"is empty reports a clean repository it never read.", mod, migrationsDirName)
	}

	// The second floor, and it is the one that guards the widening itself. The
	// three gates fed from here spent their lives reading internal/modules and
	// were green on eight directories they never opened; if this walk ever
	// narrows back to the modules — a changed root list, a walk that stops
	// descending into core/ — the counters above would all stay satisfied and
	// the loss would be exactly as quiet as it was the first time.
	outside := 0
	for _, set := range sets {
		if !strings.HasPrefix(set.owner, modulesDir+"/") {
			outside++
		}
	}
	require.Positive(t, outside,
		"every migration directory found belongs to a module; the walk has NARROWED "+
			"back to %s.\nThe plugins carry four migration sets and the core carries four "+
			"more — core/audit, core/eventbus/outbox, internal/core/workflow/pgstore and "+
			"internal/core/job/jobpg — and the core four are applied BEFORE any module's "+
			"on every startup. Reading only the modules is the state this walk was written "+
			"to end.", modulesDir)

	return sets
}

// TestThereIsNoCrossModuleForeignKey enforces Principle 2.2.
//
// A migration may only give a foreign key to the tables ITS OWN directory
// creates. Giving a REFERENCES to another owner's table makes taking that owner
// out into a separate service impossible, and means the relation is formed with
// a database constraint instead of Module Links.
//
// The scope is every migration directory in the repository (see
// [migrationDirs]), not only the modules'. A plugin is the case that makes the
// widening obvious: it is INSTALLABLE, so its tables exist only where it is
// switched on, and a foreign key from a plugin into a module — or from a module
// into a plugin — is a constraint whose other end may simply not be there.
func TestThereIsNoCrossModuleForeignKey(t *testing.T) {
	// Three counters prove THREE separate links of the walk separately: that the files
	// were found, that table ownership could be read, and that a foreign key to compare
	// was found. A single counter would hide the link that broke.
	var filesRead, tablesOwned, linksSeen int

	for _, set := range migrationDirs(t) {
		mod, migDir := set.owner, set.dir

		sqls, err := filepath.Glob(filepath.Join(migDir, "*.sql"))
		if err != nil {
			t.Fatalf("%s could not be scanned: %v", migDir, err)
		}
		filesRead += len(sqls)

		// First collect the tables the module OWNS.
		owned := map[string]bool{}
		var bodies []string
		for _, f := range sqls {
			raw, readErr := os.ReadFile(f)
			if readErr != nil {
				t.Fatalf("%s could not be read: %v", f, readErr)
			}
			body := stripSQLComments(string(raw))
			bodies = append(bodies, body)
			for _, m := range createTableRe.FindAllStringSubmatch(body, -1) {
				owned[strings.ToLower(m[1])] = true
				tablesOwned++
			}
		}

		for i, body := range bodies {
			for _, m := range referencesRe.FindAllStringSubmatch(body, -1) {
				target := strings.ToLower(m[1])
				linksSeen++
				if !owned[target] {
					t.Errorf("%s: %q gives a REFERENCES to table %q which it does NOT own "+
						"(Principle 2.2: a cross-owner FK is forbidden; the relation is formed with Module Links).",
						sqls[i], mod, target)
				}
			}
		}
	}

	require.Positive(t, filesRead,
		"no SQL file was found in any migration directory; the check must have gone BLIND — the "+
			"migrations may be kept in a directory other than %q or with an extension other than .sql. "+
			"A scan that finds no file approves every violation.", migrationsDirName)
	// The MIDDLE one of these three does a different job from the other two and the
	// difference has to be written down: when the ownership set empties out the check
	// does NOT FALL SILENT, on the contrary it takes every REFERENCES for a violation
	// and produces a pile of false accusations. The value of the line is that it names
	// the REASON for that pile — a pile of false accusations whose reason is not
	// visible talks people into silencing the check.
	require.Positive(t, tablesOwned,
		"no CREATE TABLE was found in the scanned SQL; the ownership read must have gone BLIND "+
			"(createTableRe may no longer recognize today's DDL form).\n"+
			"All the \"gives a REFERENCES to a table it does not own\" findings above may "+
			"have come out for this reason: when NO table is OWNED, every link looks "+
			"cross-module.")
	require.Positive(t, linksSeen,
		"no REFERENCES was found in the scanned SQL; the foreign key read must have gone BLIND "+
			"(referencesRe does not match, or the relations are now formed with ALTER TABLE ... ADD CONSTRAINT "+
			"ile kuruluyor olabilir).\n"+
			"If the repository really did drop all its foreign keys, the scope of this check "+
			"has to be rewritten as well; staying silently green would be a claim that the ban "+
			"izlenimini verir.")
}

// TestMigrationsCanBeRolledBack enforces the up/down requirement of plan Section 8.
//
// The scope is every migration directory in the repository ([migrationDirs]) and
// not only the modules'. Until 2026-09-06 it read internal/modules alone, which
// left the four plugin schemas and the four CORE schemas — the ones openApplication
// applies before any module's, on every startup — with no reversibility rule at
// all. All eight had their down file when they were measured; none of them was
// being asked for it.
//
// A directory whose sets are not counted is worse than one with a missing pair,
// because the counter is per DIRECTORY rather than per file: a set that stops
// producing *.up.sql matches nothing and asks for nothing, and Glob reports that
// as an empty list rather than as an error.
func TestMigrationsCanBeRolledBack(t *testing.T) {
	pairsChecked := 0
	setsWithUps := 0

	for _, set := range migrationDirs(t) {
		ups, err := filepath.Glob(filepath.Join(set.dir, "*"+upMigrationSuffix))
		if err != nil {
			t.Fatalf("%s could not be scanned: %v", set.dir, err)
		}
		if len(ups) == 0 {
			t.Errorf("%s holds .sql files but not one *%s; the reversibility rule has "+
				"nothing to ask for here.\n"+
				"A migration set this walk cannot name is a set nobody checks: either the "+
				"naming left the \"%s\" pattern, or these files are not migrations and the "+
				"directory should not be called %q.",
				set.owner, upMigrationSuffix, upMigrationSuffix, migrationsDirName)

			continue
		}
		setsWithUps++
		pairsChecked += len(ups)

		for _, up := range ups {
			down := strings.TrimSuffix(up, upMigrationSuffix) + ".down.sql"
			if _, statErr := os.Stat(down); statErr != nil {
				t.Errorf("%s: no down pair (%s). Plan Section 8 requires migrations to be "+
					"reversible.", up, filepath.Base(down))
			}
		}
	}

	// When Glob finds no match it does NOT RETURN AN ERROR, it returns an empty list:
	// when the directory name or the file naming drifts, this test would pass without
	// looking for a single pair.
	require.Positive(t, pairsChecked,
		"no *%s was found in any migration directory; the check must have gone BLIND — the "+
			"migrations may have been moved outside the %q directory or the naming may have "+
			"left the \"%s\" pattern.\nA migration whose down pair is never looked for "+
			"says it cannot be rolled back only in production.",
		upMigrationSuffix, migrationsDirName, upMigrationSuffix)
	require.Positive(t, setsWithUps, "no migration directory produced a single up file")
}

// moneyWords are the words suggesting that a field name holds money.
//
// The name is tried SPLIT into its camelCase/snake_case parts; no pattern anchored
// at the start is used. The reason is a measured blind spot: the previous pattern
// was of the form "^(amount|price|…)" and did not match PREFIXED names such as
// "UnitPrice", "NetAmount", "OriginalPrice" — that is, a float field on those names
// would pass the check silently.
//
// A word BOUNDARY is looked for, not a substring: "Discountable" (a bool) is not a
// money field and must not be caught because of the "discount" substring.
var moneyWords = map[string]bool{
	"amount": true, "price": true, "total": true, "subtotal": true,
	"cost": true, "fee": true, "discount": true, "tax": true, "shipping": true,
}

// isMoneyName says whether a field name suggests that it holds money.
func isMoneyName(ad string) bool {
	for _, sozcuk := range nameParts(ad) {
		if moneyWords[sozcuk] {
			return true
		}
	}
	return false
}

// nameParts splits a Go field name into its lower-cased words.
//
// Both camelCase ("UnitPrice" -> unit, price) and snake_case ("unit_price") are
// handled; consecutive capitals ("URLTotal") are not counted as separate parts,
// because splitting an acronym would produce meaningless parts like "u", "r", "l".
func nameParts(ad string) []string {
	var parcalar []string
	var kelime strings.Builder

	end := func() {
		if kelime.Len() > 0 {
			parcalar = append(parcalar, strings.ToLower(kelime.String()))
			kelime.Reset()
		}
	}
	for i, r := range ad {
		switch {
		case r == '_':
			end()
		case unicode.IsUpper(r) && i > 0 && !unicode.IsUpper(rune(ad[i-1])):
			end()
			kelime.WriteRune(r)
		default:
			kelime.WriteRune(r)
		}
	}
	end()

	return parcalar
}

// floatTypeNames are the two types this rule refuses to see money written in.
var floatTypeNames = map[string]bool{"float32": true, "float64": true}

// floatMoneyType returns the float type a field is declared with, or "".
//
// Three shapes are accepted where the first version of this rule accepted one.
// A bare `float64` was the only one it read, so `*float64` and `[]float64`
// walked past it — and a NULLABLE money column is exactly the field somebody
// reaches for a pointer to write. Measured on 2026-09-06 before the widening:
// the repository holds no money-named field of any of the three shapes, so the
// two new ones cost nothing today and close the cheapest way around the rule.
//
// A named type (`type Amount float64`) is still invisible, and that is a
// deliberate stop rather than an oversight: resolving it means binding this
// scan to go/types and to the whole build graph, and the declaration of such a
// type would itself be a reviewable event. What is closed here is the shape
// that needs no declaration at all.
func floatMoneyType(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		if floatTypeNames[typed.Name] {
			return typed.Name
		}
	case *ast.StarExpr:
		if inner := floatMoneyType(typed.X); inner != "" {
			return "*" + inner
		}
	case *ast.ArrayType:
		if inner := floatMoneyType(typed.Elt); inner != "" {
			return "[]" + inner
		}
	}

	return ""
}

// TestMoneyIsAnInteger enforces plan Section 8: money is stored as an INTEGER in
// minor units.
//
// Floating point money produces silent rounding errors while summing, and you see
// the error only at reconciliation time.
//
// # ~~"the modules are where money lives"~~ — measured wrong on 2026-09-06
//
// Until that date this walk read [modulesDir] and nothing else, and the sentence
// justifying it was never written down because nobody had counted. The count:
// 682 money-named fields in the production trees, 564 of them under
// internal/modules and 118 OUTSIDE it — 93 in internal/workflows, 10 in the
// admin panel, 9 in core, 6 in the plugins. The 93 are the worst of the three
// groups to have left unread: internal/workflows/checkout and its siblings are
// where the totals are actually COMPUTED — price, discount, tax, shipping and
// the payment amount meet there — so a float declared in that tree is the
// rounding error the rule is named after, and it was outside the rule.
//
// The gate was green on all 118 because none of them is a float. It was green
// the way an audit that never opened the file is green.
//
// The walk is now [productionTrees], the list every other audit here shares,
// which is the one place a new tree has to be declared
// ([TestTheProductionTreeListCoversTheRepository] holds it against the disk).
//
// # Why two counters and not one
//
// The file counter and the field counter break for different reasons and a
// single total hides whichever one broke. Files-per-tree going to zero means
// the WALK stopped reaching a tree — the failure the paragraph above describes,
// and the reason it is counted per tree rather than in total: 564 fields in the
// modules keep any total comfortably positive while a smaller tree reads
// nothing at all. The field counter going to zero means the NAME pattern stopped
// recognizing how money is spelled, which leaves every tree read and no field
// examined.
func TestMoneyIsAnInteger(t *testing.T) {
	// The field counter counts the fields whose NAME looks like money — without
	// looking at the type. What is measured is not violations but the FIELD OF
	// VIEW: if the naming moves one day to a form the pattern does not recognize
	// (if money fields start being written with a "Money" suffix, say), this scan
	// looks at no field at all and a float money field would pass silently.
	// Prefixed names (UnitPrice, NetAmount) are in scope; see [nameParts].
	moneyFieldsSeen := 0
	filesRead := map[string]int{}

	for _, tree := range productionTrees {
		for _, file := range treeProductionFiles(t, tree) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s could not be parsed: %v", file, err)
			}
			filesRead[tree]++

			ast.Inspect(parsed, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, f := range st.Fields.List {
					floatType := floatMoneyType(f.Type)
					for _, name := range f.Names {
						if !isMoneyName(name.Name) {
							continue
						}
						moneyFieldsSeen++
						if floatType == "" {
							continue
						}
						t.Errorf("%s:%d: the %q field is of type %s. Plan Section 8: money is stored "+
							"as an INTEGER in minor units (kurus/cent), NEVER as a float.",
							repoPath(file), fset.Position(name.Pos()).Line, name.Name, floatType)
					}
				}
				return true
			})
		}
	}

	for _, tree := range productionTrees {
		require.Positive(t, filesRead[tree],
			"not one production Go file was read in the %q tree; the walk has gone BLIND "+
				"there.\nA tree that is not read produces no finding rather than an error, "+
				"which is how this rule spent its life reading internal/modules alone while "+
				"118 money-named fields sat outside it. Either the tree moved — and "+
				"productionTrees has to move with it — or the walk is broken.", tree)
	}

	require.Positive(t, moneyFieldsSeen,
		"not a SINGLE field named like money was found in the production trees; "+
			"the check must have gone BLIND.\n"+
			"The name is matched by its camelCase/snake_case PARTS (see moneyWords and "+
			"[nameParts]), so a naming that stops producing those parts — a \"Money\" "+
			"suffix, an acronym, a named type standing in for the field — leaves the scan "+
			"looking at no field at all while a float money field passes silently. The "+
			"word list has to be updated together with the naming in the repository.")
}

// godocComma catches whether a godoc first line continues with a comma right after
// the name.
var godocComma = regexp.MustCompile(`^// ([A-Za-z_][A-Za-z0-9_]*), `)

// TestGodocFormat enforces that a godoc starts with the identifier's name and
// continues without a comma.
//
// revive's "exported" rule only checks EXPORTED identifiers; on unexported ones the
// same mistake stays silent. This test covers both, and additionally catches a godoc
// block being attached to the WRONG identifier — if the name on the block's first
// line does not match the name of the definition it is attached to, it errors.
func TestGodocFormat(t *testing.T) {
	// The unit being checked is the DEFINITION with a godoc, not the file: the scope
	// can empty out while the files stay in place (if the generated-code sieve widens,
	// or if the godocs come loose from their definitions and the doc field stays nil).
	denetlenenTanim := 0

	var files []string
	for _, tree := range productionTrees {
		files = append(files, treeFiles(t, tree)...)
	}

	for _, file := range files {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s could not be parsed: %v", file, err)
		}
		// Generated code (sqlc) is outside this rule.
		if len(parsed.Comments) > 0 && strings.Contains(parsed.Comments[0].Text(), "Code generated by") {
			continue
		}

		for _, decl := range parsed.Decls {
			var (
				doc  *ast.CommentGroup
				name string
			)
			switch d := decl.(type) {
			case *ast.FuncDecl:
				doc, name = d.Doc, d.Name.Name
			case *ast.GenDecl:
				doc = d.Doc
				if len(d.Specs) == 1 {
					switch sp := d.Specs[0].(type) {
					case *ast.TypeSpec:
						name = sp.Name.Name
					case *ast.ValueSpec:
						if len(sp.Names) > 0 {
							name = sp.Names[0].Name
						}
					}
				}
			}
			// The name of the "var _ Iface = (*T)(nil)" idiom is "_"; its godoc naturally
			// describes not a name but the contract being verified.
			if doc == nil || name == "" || name == "_" || len(doc.List) == 0 {
				continue
			}
			denetlenenTanim++

			first := doc.List[0].Text
			if m := godocComma.FindStringSubmatch(first); m != nil {
				t.Errorf("%s:%d: the godoc first line is %q — there is a COMMA right after the name.\n"+
					"The right form: \"// %s ...\". revive applies this rule only to exported "+
					"identifiers.",
					file, fset.Position(doc.List[0].Pos()).Line, strings.TrimSpace(first), m[1])
				continue
			}
			// If it does not start with the name of the definition it is attached to, the
			// godoc has most likely stuck to the wrong identifier.
			prefix := "// " + name
			if !strings.HasPrefix(first, prefix) && !strings.HasPrefix(first, "// Package ") &&
				!strings.HasPrefix(first, "//nolint") && !strings.HasPrefix(first, "//go:") {
				t.Errorf("%s:%d: the godoc of the %q definition starts with %q.\n"+
					"A godoc block has to start with the NAME of the definition it is attached "+
					"to; this block has most likely stuck to the wrong definition because no "+
					"blank line was put in between.",
					file, fset.Position(doc.List[0].Pos()).Line, name, strings.TrimSpace(first))
			}
		}
	}

	require.Positive(t, denetlenenTanim,
		"not a SINGLE definition with a checked godoc was found in the production trees; "+
			"the check must have gone BLIND.\n"+
			"Possible reasons: the sources moved outside productionTrees, the parsing is done "+
			"without comments (parser.ParseComments dropped), or the \"Code generated by\" "+
			"sieve covers all the files. Because revive does not look at unexported "+
			"definitions, nothing else checks that class when this scan falls silent.")
}
