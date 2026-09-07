package arch_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds one rule: a piece of SQL whose ANSWER depends on the cluster's
// case folding has to say so.
//
// # Why this exists, which is a mistake made in this repository twice
//
// PostgreSQL's lower(), upper() and ILIKE fold the letters the cluster's CTYPE
// knows. On a database created with --locale=C that is ASCII and nothing else,
// and the failure is never an error — it is a smaller answer. ADR 0015 found it
// in the storefront's search. ADR 0038 found it in the invoice module's erasure,
// where the smaller answer was "we looked and you are not here", said to a
// person whose document was being held.
//
// ADR 0038 then stated, as a consequence, that "nothing in the tree now depends
// on lower() folding non-ASCII". That was checked the next day and it was FALSE:
// three modules guard their e-mail column with CHECK (email = lower(email)), and
// on a --locale=C cluster that constraint refuses the unfolded ASCII address
// "Ada@Example.com" while ACCEPTING the unfolded Turkish one, because lower()
// leaves those capitals alone and the value equals its own lower(). See D28.
//
// The lesson is not "grep harder". It is that a locale-dependent expression
// reads like a rule and is actually a rule PLUS a cluster, and nothing about
// looking at one says which. So each one is made to carry the difference in
// writing.
//
// # What is in scope, and what is deliberately not
//
// CHECK constraints and query predicates: both DECIDE something — what may be
// stored, and which rows come back — so a fold that varies by installation
// varies the answer.
//
// Index expressions are NOT in scope. An index does not decide an answer; it
// decides how fast one is reached, and an expression index that no predicate
// matches is a slow query rather than a wrong one. The predicate that would need
// such an index is caught by the other half of this audit, which is where the
// correctness actually lives.

// caseFoldingDeclarations names every CHECK constraint whose result depends on
// the cluster, and states what it holds and what it does not.
//
// A declaration is not a way to make this audit quiet. It is the sentence
// somebody can disagree with, and the reason it is required is that the
// alternative was tried: the constraints below were written without one, read as
// absolute for a year, and produced a false claim in an accepted ADR.
var caseFoldingDeclarations = map[string]string{
	"auth_user_email_check": "HOLDS ONLY FOR ASCII. `email = lower(email)` refuses an " +
		"unfolded ASCII address and ACCEPTS an unfolded non-ASCII one on a --locale=C " +
		"cluster, because lower() leaves those capitals alone. What actually keeps the " +
		"column folded is models.NormalizeEmail on every write path; this constraint is " +
		"defense in depth against a direct SQL write, and its depth stops at the ASCII " +
		"boundary. The startup probe in core/db/casefold.go reports the cluster's answer " +
		"as case_lower so an operator is told rather than left to assume. D28",
	"customer_email_check": "HOLDS ONLY FOR ASCII, for the same reason as " +
		"auth_user_email_check and with the same consequence: on a --locale=C cluster it " +
		"stops refusing unfolded non-ASCII addresses, and two rows for one person is what " +
		"the unique index on the column then cannot prevent. D28",
	"b2b_company_email_check": "HOLDS ONLY FOR ASCII, as auth_user_email_check does. The " +
		"length half of this constraint is locale-independent and unaffected. D28",
	"promotion_code_check": "SOUND, and it is the contrast worth reading next to the three " +
		"above. `code = upper(code)` is locale-dependent in general, but promotion codes " +
		"cannot contain a non-ASCII letter: service.normalizeCode admits only A-Z, 0-9, " +
		"'-' and '_', and rejects everything else before the value reaches the database. " +
		"The fold is therefore ASCII by construction and upper() cannot disagree with Go " +
		"about it on any cluster. This entry is what makes the difference between the two " +
		"cases a written claim rather than an accident.",
}

// TestEveryLocaleDependentCheckConstraintIsDeclared is the audit.
func TestEveryLocaleDependentCheckConstraintIsDeclared(t *testing.T) {
	t.Parallel()

	found := localeDependentConstraints(t)

	// The blindness guard. This audit's population is small and hand-declared,
	// so a scanner that quietly stopped matching would leave it green and empty
	// — which is the failure this repository has already met in three of its own
	// gates (D23). The number is not pinned; the fact that it found the known
	// ones is.
	require.NotEmpty(t, found,
		"no locale-dependent CHECK constraint was found anywhere in the tree, which cannot "+
			"be true while auth, customer, b2b and promotion each have one; the SQL scan "+
			"has gone blind and this audit is comparing nothing against nothing")

	for _, name := range found {
		reason, declared := caseFoldingDeclarations[name]

		assert.Truef(t, declared,
			"the CHECK constraint %q folds case in SQL and says nothing about the cluster.\n"+
				"lower(), upper() and ILIKE fold the letters the cluster's CTYPE knows, which on "+
				"a --locale=C database is ASCII and nothing else — so this constraint enforces a "+
				"different rule on different installations and reads like one rule.\n"+
				"Add an entry to caseFoldingDeclarations stating what it holds and what it does "+
				"not. If the values it guards cannot contain a non-ASCII letter, say WHERE that "+
				"is enforced, the way promotion_code_check does.", name)

		if declared {
			assert.NotEmptyf(t, strings.TrimSpace(reason),
				"%s is declared with an EMPTY reason, which is a silence with a name on it", name)
		}
	}

	for name := range caseFoldingDeclarations {
		assert.Containsf(t, found, name,
			"caseFoldingDeclarations names %q but no such locale-dependent CHECK constraint "+
				"was found; the map is describing a constraint that has been renamed or dropped", name)
	}
}

// TestNoQueryPredicateFoldsCaseInSQL is the other half, and today it is a
// blindness guard rather than a list.
//
// ADR 0038 removed the last one — the invoice module's erasure count, which read
// `WHERE lower(buyer_email) = lower($1)` and answered zero for a person whose
// document was in the table. The decision it settled is that an identity is
// folded in Go, so a predicate that folds in SQL is the shape of the defect
// coming back rather than a style question.
//
// It is a REFUSAL rather than a declaration map on purpose. A constraint is
// enforced once per write and can honestly be defense in depth; a predicate is
// the answer itself, and there is no version of "this lookup returns different
// rows on different clusters" that belongs in this repository without a decision
// record of its own. If one ever does, this test is where the argument goes.
func TestNoQueryPredicateFoldsCaseInSQL(t *testing.T) {
	t.Parallel()

	scanned, offenders := foldingInQueryFiles(t)

	require.NotEmptyf(t, scanned,
		"no query file was read under %s; the scan has gone blind and the refusal below "+
			"is refusing nothing", modulesDir)

	assert.Emptyf(t, offenders,
		"these query predicates fold case in SQL:\n  %s\n"+
			"lower(), upper() and ILIKE fold what the cluster's CTYPE knows, so the rows one "+
			"of these returns depend on how the database was created. ADR 0038 removed the "+
			"last one after it answered \"nothing found\" for a person whose invoice was in "+
			"the table. Fold in Go and compare the stored bytes, or bring a decision record "+
			"saying why this one is different.", strings.Join(offenders, "\n  "))
}

// localeDependentConstraints returns the name of every CHECK constraint whose
// body calls lower() or upper().
//
// It tokenizes rather than greps, for the reason the other SQL audits here do: a
// mention in a comment or inside a quoted string is not an expression, and a
// line-based scan would count one of those and miss a CHECK written across two
// lines.
func localeDependentConstraints(t *testing.T) []string {
	t.Helper()

	var names []string

	forEachSQLFile(t, "migrations", func(path string, body string) {
		tokens := tokenizeSQL(blankSQLNoise(body))

		for i := range tokens {
			if !wordAt(tokens, i, "check") {
				continue
			}

			inner, _, ok := parenBody(tokens, i+1)
			if !ok || !foldsCase(inner) {
				continue
			}

			// The name is the token after CONSTRAINT, which is the form every
			// table constraint in this repository uses. A CHECK written without
			// one is reported rather than skipped: an unnamed constraint cannot
			// be declared, and cannot be dropped by an operator either.
			name := ""
			if wordAt(tokens, i-2, "constraint") {
				name = tokens[i-1].text
			}

			require.NotEmptyf(t, name,
				"%s has a CHECK that folds case and no CONSTRAINT name, so it cannot be "+
					"declared in caseFoldingDeclarations and an operator cannot name it to "+
					"drop it. Give it a name.", path)

			names = append(names, name)
		}
	})

	slices.Sort(names)

	return slices.Compact(names)
}

// foldingInQueryFiles returns how many query files were read and every one whose
// SQL calls lower(), upper() or ILIKE.
func foldingInQueryFiles(t *testing.T) (scanned int, offenders []string) {
	t.Helper()

	forEachSQLFile(t, "queries", func(path string, body string) {
		scanned++

		tokens := tokenizeSQL(blankSQLNoise(body))
		if foldsCase(tokens) {
			offenders = append(offenders, path)
		}
	})

	slices.Sort(offenders)

	return scanned, offenders
}

// foldsCase reports whether the tokens call lower() or upper(), or use ILIKE.
func foldsCase(tokens []sqlToken) bool {
	for i, token := range tokens {
		if !token.word {
			continue
		}
		if token.text == "ilike" {
			return true
		}
		if (token.text == "lower" || token.text == "upper") && i+1 < len(tokens) && tokens[i+1].text == "(" {
			return true
		}
	}

	return false
}

// forEachSQLFile calls visit for every .sql file in a directory of the given
// name, under both the module tree and the plugin tree.
//
// Plugins are walked as well as modules because a plugin's migration is applied
// by the same runner into the same database, and searchpg — the one plugin that
// exists to search text — is the likeliest place for the next one of these.
func forEachSQLFile(t *testing.T, dirName string, visit func(path, body string)) {
	t.Helper()

	for _, tree := range []string{modulesDir, "plugins"} {
		root := filepath.Join(repoRoot, tree)
		if _, err := os.Stat(root); err != nil {
			continue
		}

		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".sql") {
				return nil
			}
			if filepath.Base(filepath.Dir(path)) != dirName {
				return nil
			}

			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}

			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}

			visit(filepath.ToSlash(rel), string(body))

			return nil
		})
		require.NoError(t, err, "%s could not be walked for %s", tree, dirName)
	}
}
