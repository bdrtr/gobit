package arch_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ONE invariant: WHEN LIVE CODE NAMES A DATABASE CONSTRAINT
// OR INDEX, THAT OBJECT IS IN THE SCHEMA.
//
// It is the schema half of the class doc_references_test.go opens with. A godoc
// that names `order_summaries_refund_within_paid` sends a reader to grep the
// migrations, exactly as a godoc link to a renamed function sends them to grep the packages,
// and Go says nothing about either — a constraint name is a word in a comment.
//
// # Why it was written
//
// On 2026-09-10 ADR 0120 replaced order_exchanges_completed_owes_nothing with
// order_exchanges_completed_is_settled, and FOUR live sentences went on naming
// the dropped one: a query file's header, its generated copy, a service godoc
// and a vocabulary test's reasoning. Each of them told a reader that an exchange
// owing money cannot be completed, which had stopped being true, and each of
// them offered a name to check it against that no longer existed. They were
// found by hand while sweeping something else. Gap D59.
//
// # The vocabulary is CLOSED, and it is the tree's own
//
// A snake_case word in a comment can be a column, a JSON key, a field or an
// English phrase with underscores. So the audit does not ask "does this look
// like a constraint" — it asks whether the tree has EVER defined an object by
// that name, and then whether that object is still there. Names the migrations
// never defined are outside the audit entirely, which is the same closing move
// count_claims_test.go makes and for the same reason (ADR 0070).
//
// # What it does NOT catch
//
// A constraint whose PREDICATE changed while its name stayed. That is the
// larger half of D59 and no name-based audit can reach it: the sentence is
// wrong and every word in it resolves.
//
// # Why documents are out of scope
//
// A migration is a dated record, and so are the changelog, the decision records
// and the measurements — this repository amends them by ADDING rather than by
// editing, so a 2026-09-06 entry naming a constraint dropped on 2026-09-10 is
// CORRECT and striking it through is the convention for changing it. Live code
// carries no such convention: its comments describe the world as it is now.

// schemaNameExemption is one place a DEAD name is written on purpose.
//
// Live code in this repository records what a sentence used to say — "this
// godoc said X until ADR N" is a shape it uses deliberately, because the reader
// who was misled by the old sentence is the reader who needs to know it changed.
// Naming the constraint that used to hold the rule is the same act. An audit
// that forbade it would buy its own greenness by deleting history.
//
// So the list exists, and it is short on purpose: every entry is a sentence that
// names a dead object BECAUSE it is dead. [TestTheSchemaNameExemptionsHold]
// refuses one that has stopped being either.
type schemaNameExemption struct {
	// file is the repository-relative path the sentence lives in.
	file string
	// name is the dead object it names.
	name string
	// reason is why naming it is right here.
	reason string
}

// schemaNameExemptions are the deliberate mentions.
var schemaNameExemptions = []schemaNameExemption{
	{
		file: "internal/arch/schema_names_test.go",
		name: "order_exchanges_completed_owes_nothing",
		reason: "This audit's own account of why it exists. The defect it was built " +
			"for is four live sentences naming this constraint after ADR 0120 " +
			"dropped it, and an explanation that could not name it would be an " +
			"explanation of nothing.",
	},
}

// schemaObject is one constraint or index the migrations define.
type schemaObject struct {
	// name is the object's identifier.
	name string
	// live reports whether the LAST thing the migrations did was create it.
	live bool
	// where is the migration that last touched it.
	where string
}

// schemaObjectAdded matches the two ways a migration creates a named object.
var schemaObjectAdded = regexp.MustCompile(
	`(?i)CONSTRAINT\s+([a-z_][a-z0-9_]*)|CREATE\s+(?:UNIQUE\s+)?INDEX(?:\s+CONCURRENTLY)?(?:\s+IF\s+NOT\s+EXISTS)?\s+([a-z_][a-z0-9_]*)`)

// schemaObjectDropped matches the two ways a migration removes one.
var schemaObjectDropped = regexp.MustCompile(
	`(?i)DROP\s+(?:CONSTRAINT|INDEX)(?:\s+CONCURRENTLY)?(?:\s+IF\s+EXISTS)?\s+([a-z_][a-z0-9_]*)`)

// schemaObjects walks every up-migration IN ORDER and reports what the schema
// holds.
//
// The order is the whole computation: a name is routinely dropped and recreated
// in the same file, one line apart, which is how a CHECK is widened. Collecting
// the adds and the drops into two sets and subtracting gets that backwards —
// measured while writing this, on fulfillments_returned_stamp, which the naive
// version called dead.
//
// Down-migrations are skipped. They describe the schema of a rollback, not the
// schema, and a name they restore is not a name the tree has.
func schemaObjects(t *testing.T) map[string]schemaObject {
	t.Helper()

	var files []string
	for _, tree := range productionTrees {
		root := filepath.Join(repoRoot, tree)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && slices.Contains(skippedDirs, entry.Name()) {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".up.sql") {
				return nil
			}
			if !strings.Contains(filepath.ToSlash(path), "/migrations/") {
				return nil
			}
			files = append(files, path)

			return nil
		})
		require.NoError(t, err, "%s could not be walked", tree)
	}
	require.NotEmpty(t, files, "no up-migration was found; the walk looked in %v", productionTrees)

	// Sorted so the numbered files of one module are read in migration order.
	sort.Strings(files)

	objects := map[string]schemaObject{}
	for _, path := range files {
		body, err := os.ReadFile(path)
		require.NoError(t, err, "%s could not be read", path)

		relative := schemaRelative(path)
		for _, line := range strings.Split(string(body), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "--") {
				continue
			}
			// The drop is read FIRST so that a single line saying both — which
			// no migration writes today — would settle as created rather than
			// as removed.
			for _, match := range schemaObjectDropped.FindAllStringSubmatch(line, -1) {
				objects[match[1]] = schemaObject{name: match[1], live: false, where: relative}
			}
			for _, match := range schemaObjectAdded.FindAllStringSubmatch(line, -1) {
				name := match[1]
				if name == "" {
					name = match[2]
				}
				objects[name] = schemaObject{name: name, live: true, where: relative}
			}
		}
	}

	return objects
}

// schemaRelative turns a walked path back into a repository-relative one.
func schemaRelative(path string) string {
	cleaned := filepath.ToSlash(filepath.Clean(path))

	return strings.TrimPrefix(cleaned, "../../")
}

// schemaProseComment matches a line that is a comment in Go or in SQL.
var schemaProseComment = regexp.MustCompile(`^\s*(//|--)`)

// schemaWord matches a snake_case token: at least three segments, which is the
// shape every constraint in this tree has (table, column, rule).
var schemaWord = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+){2,}\b`)

// TestTheSchemaNamesInLiveCodeExist fails when live code names a constraint or
// an index the schema no longer has.
func TestTheSchemaNamesInLiveCodeExist(t *testing.T) {
	t.Parallel()

	objects := schemaObjects(t)
	require.Greater(t, len(objects), 100,
		"the migration walk found %d named objects, which is too few to be an answer; "+
			"a schema this size defines hundreds, so the walk is looking in the wrong "+
			"place or the patterns stopped matching", len(objects))

	for _, path := range schemaProseFiles(t) {
		file, err := os.Open(path)
		require.NoError(t, err, "%s could not be opened", path)

		relative := schemaRelative(path)
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for number := 1; scanner.Scan(); number++ {
			line := scanner.Text()
			if !schemaProseComment.MatchString(line) {
				continue
			}
			for _, word := range schemaWord.FindAllString(line, -1) {
				object, defined := objects[word]
				if !defined || object.live || schemaNameExempt(relative, word) {
					continue
				}
				assert.Fail(t, "a schema name that the tree no longer defines",
					"%s:%d names %q.\n"+
						"The migrations dropped it and did not put it back (%s).\n"+
						"A constraint name in a comment is a REFERENCE: it sends the reader to "+
						"grep the migrations, and what they find is nothing. Either the object "+
						"was renamed and this sentence has to follow it, or the rule it "+
						"described is gone and so is the reason the sentence gave.\n"+
						"Dated records — the changelog, the decision records, the measurements "+
						"and the migrations themselves — are outside this audit; they record "+
						"what was true when they were written.",
					relative, number, word, object.where)
			}
		}
		require.NoError(t, scanner.Err(), "%s could not be read", relative)
		require.NoError(t, file.Close())
	}
}

// schemaNameExempt reports whether this file may name this dead object.
func schemaNameExempt(file, name string) bool {
	return slices.ContainsFunc(schemaNameExemptions, func(e schemaNameExemption) bool {
		// The suffix rather than equality, for [countDatedRecord]'s reason: an
		// agent's worktree brings the same file back under a longer path, and an
		// exemption that stopped applying there would make this audit report a
		// defect the tree does not have.
		return e.name == name && strings.HasSuffix(file, e.file)
	})
}

// TestTheSchemaNameExemptionsHold keeps the list from outliving its entries.
//
// An exemption is a hole, and a hole nobody checks widens: the sentence can be
// deleted, or the object can come back, and either way the entry goes on
// forgiving something that is no longer there. Both are failures here.
func TestTheSchemaNameExemptionsHold(t *testing.T) {
	t.Parallel()

	objects := schemaObjects(t)
	for _, exemption := range schemaNameExemptions {
		object, defined := objects[exemption.name]
		require.True(t, defined,
			"the exemption for %s names %q and the migrations never defined it; an "+
				"exemption for a name that was never an object forgives a typo",
			exemption.file, exemption.name)
		assert.False(t, object.live,
			"the exemption for %s names %q and the schema HAS it again (%s); the "+
				"sentence needs no forgiving and the entry has to go",
			exemption.file, exemption.name, object.where)

		body, err := os.ReadFile(filepath.Join(repoRoot, exemption.file))
		require.NoError(t, err, "the exempted file %s could not be read", exemption.file)
		assert.Contains(t, string(body), exemption.name,
			"the exemption for %s names %q and the file no longer says it; a hole "+
				"kept for a sentence that was deleted forgives whatever is written there next",
			exemption.file, exemption.name)
		assert.NotEmpty(t, exemption.reason, "an exemption without a reason is a hole nobody can weigh")
	}
}

// schemaProseFiles returns the LIVE files whose comments this audit reads.
//
// Go source and the hand-written query files, and nothing else. The generated
// .sql.go files are included deliberately rather than skipped: they carry a
// verbatim copy of the query file's comment, they are what a reader lands in
// from a stack trace, and ADR 0122's gate keeps the two copies equal — so a
// stale name here is a stale name there.
func schemaProseFiles(t *testing.T) []string {
	t.Helper()

	var out []string
	for _, tree := range productionTrees {
		root := filepath.Join(repoRoot, tree)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && slices.Contains(skippedDirs, entry.Name()) {
				return filepath.SkipDir
			}
			if entry.IsDir() {
				return nil
			}
			slashed := filepath.ToSlash(path)
			if strings.Contains(slashed, "/migrations/") {
				return nil
			}
			if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".sql") {
				out = append(out, path)
			}

			return nil
		})
		require.NoError(t, err, "%s could not be walked", tree)
	}

	require.NotEmpty(t, out, "no live Go or query file was found")

	return out
}
