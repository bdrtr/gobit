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
// # The scan is DEFAULT-DENY, and the first version of this file was not
//
// This audit shipped on 2026-09-07 looking for CHECK constraints and query
// predicates, because those were the two shapes that had actually gone wrong. It
// justified the omission of everything else with a sentence — "an index does not
// decide an answer; it decides how fast one is reached" — which is FALSE for a
// UNIQUE expression index, where the fold decides whether an INSERT succeeds.
// There is no such index in the tree, so nothing was broken; the reasoning was,
// and it was the same mistake one round earlier that this file exists to catch:
// a claim about a whole class, drawn from the members that had already failed.
//
// So the rule is inverted. EVERY lower(), upper() and ILIKE in a migration is a
// site that must be declared, whatever construct it sits in, and the only
// automatic pass is the one case that can be argued to the end:
//
//   - A NON-UNIQUE index expression. It does not decide any answer — an
//     expression index that no predicate matches is an unused index, and a
//     predicate that does match it is caught by the query half below. A UNIQUE
//     one is a different object with a different job and gets no pass.
//
// # The plpgsql blind spot, which was structural rather than an oversight
//
// This repository writes plpgsql function bodies as SINGLE-QUOTED string
// literals, and migration 000002 of the invoice module explains why at length:
// the SQL audits here blank quoted literals before reading, so a dollar-quoted
// body would be parsed as live SQL and cut the file into fragments. The
// consequence for THIS audit is that [blankSQLNoise] erases every trigger body
// before it is looked at — a CHECK's worth of logic could fold case inside one
// and this file would have reported nothing, cleanly and forever.
//
// Function bodies are therefore extracted from the RAW text and scanned
// separately, and [TestThePlpgsqlBodyScannerCanSee] holds that half honest: it
// asserts the extractor finds the bodies that do exist, because a scanner that
// silently stopped matching would leave the check green and empty, which is
// precisely the shape it was added to remove.

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
	"internal/modules/invoice/migrations/000003_the_buyer_address_is_folded_by_go.up.sql": "" +
		"KNOWN WRONG FOR NON-ASCII, ON PURPOSE, and the migration's own header says so at " +
		"length. The statement is the one-time backfill `UPDATE invoices SET " +
		"buyer_email_folded = lower(btrim(buyer_email))`. A migration is SQL, and SQL is the " +
		"thing that cannot fold reliably here, so the rows this backfill gets wrong are " +
		"exactly the rows the defect was about. It is written this way because there is no " +
		"other way to write it, and the correction is a Go pass an operator runs once: " +
		"`gobit refold-invoices`. It is declared here rather than exempted because a reader " +
		"who finds it must not conclude the repository thinks lower() is safe. D27",

	"internal/modules/product/migrations/000003_option_values_carry_a_matching_form.up.sql": "" +
		"KNOWN WRONG FOR NON-ASCII, ON PURPOSE, and the migration's header says so. The " +
		"statement is the one-time backfill `UPDATE product_option_value SET value_folded = " +
		"lower(btrim(value))`. A migration is SQL, and SQL is the fold that cannot be trusted " +
		"here (ADR 0038), so the rows it gets wrong are exactly the non-ASCII ones ADR 0039 " +
		"exists for. It is written this way because a migration has no other way to write it, " +
		"and the convergence belongs at startup as the invoice module's does. It is declared " +
		"rather than exempted so that a reader does not conclude the repository thinks lower() " +
		"is safe. ADR 0039",

	"promotion_code_check": "SOUND, and it is the contrast worth reading next to the three " +
		"above. `code = upper(code)` is locale-dependent in general, but promotion codes " +
		"cannot contain a non-ASCII letter: service.normalizeCode admits only A-Z, 0-9, " +
		"'-' and '_', and rejects everything else before the value reaches the database. " +
		"The fold is therefore ASCII by construction and upper() cannot disagree with Go " +
		"about it on any cluster. This entry is what makes the difference between the two " +
		"cases a written claim rather than an accident.",

	"cart_promotion_code_upper": "SOUND, by the same argument as promotion_code_check and with " +
		"a WIDER guarantee than that one needs. The cart does not restate promotion's alphabet " +
		"— what a code means belongs to that module — so it enforces the property the fold " +
		"actually depends on: service.normalizePromotionCode refuses any byte at or above 0x80 " +
		"before the value reaches the database. ASCII folds identically under every CTYPE, so " +
		"upper() cannot disagree with Go's strings.ToUpper on any cluster. Without that check " +
		"a code carrying \"é\" would be stored on one installation and refused on another. " +
		"ADR 0109",
}

// TestEverySqlThatFoldsCaseIsDeclared is the audit.
//
// It is keyed on the SITE rather than on the construct, so a fold that moves
// from a CHECK into a DEFAULT, a unique index or a trigger body does not escape
// by changing shape.
func TestEverySqlThatFoldsCaseIsDeclared(t *testing.T) {
	t.Parallel()

	found := localeDependentSites(t)

	// The blindness guard. This audit's population is small and hand-declared,
	// so a scanner that quietly stopped matching would leave it green and empty
	// — which is the failure this repository has already met in three of its own
	// gates (D23). The number is not pinned; the fact that it found the known
	// ones is.
	require.NotEmpty(t, found,
		"no locale-dependent SQL was found anywhere in the migration tree, which cannot "+
			"be true while auth, customer, b2b and promotion each carry a folding CHECK; "+
			"the SQL scan has gone blind and this audit is comparing nothing against nothing")

	declared := map[string]bool{}

	for _, site := range found {
		if site.autoPass {
			continue
		}

		reason, ok := caseFoldingDeclarations[site.name]
		declared[site.name] = true

		assert.Truef(t, ok,
			"%s in %s folds case in SQL and says nothing about the cluster (%s).\n"+
				"lower(), upper() and ILIKE fold the letters the cluster's CTYPE knows, which on "+
				"a --locale=C database is ASCII and nothing else — so this enforces a different "+
				"rule on different installations while reading like one rule.\n"+
				"Add an entry to caseFoldingDeclarations stating what it holds and what it does "+
				"not. If the values it touches cannot contain a non-ASCII letter, say WHERE that "+
				"is enforced, the way promotion_code_check does.",
			site.kind, site.path, nameOrUnnamed(site.name))

		if ok {
			assert.NotEmptyf(t, strings.TrimSpace(reason),
				"%s is declared with an EMPTY reason, which is a silence with a name on it", site.name)
		}
	}

	for name := range caseFoldingDeclarations {
		assert.Truef(t, declared[name],
			"caseFoldingDeclarations names %q but no locale-dependent SQL was found under that "+
				"name; the map is describing something that has been renamed or dropped", name)
	}
}

// nameOrUnnamed renders a site's key for the failure message.
func nameOrUnnamed(name string) string {
	if name == "" {
		return "it has no name to declare it by, which is the first thing to fix"
	}

	return "named " + name
}

// TestThePlpgsqlBodyScannerCanSee keeps the trigger-body half from being green
// and empty.
//
// [plpgsqlBodies] reads the raw file because every other reader in this package
// blanks quoted literals, and a plpgsql body IS a quoted literal here. That makes
// it the one part of this audit that could silently stop working without any
// declaration going missing: no body would be found, none would fold, and the
// gate would pass. So the extractor is asked to find what is known to be there.
//
// The invoice module's 000002 is the only migration in the repository with
// procedural code — its own header says so, and says it was verified before it
// was written — so these three are the whole population.
func TestThePlpgsqlBodyScannerCanSee(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot, modulesDir,
		"invoice", "migrations", "000002_an_issued_invoice_is_retained.up.sql"))
	require.NoError(t, err, "the only migration carrying plpgsql could not be read")

	bodies := plpgsqlBodies(string(raw))

	for _, name := range []string{
		"invoices_refuse_delete", "invoice_lines_refuse_delete", "invoices_refuse_truncate",
	} {
		body, found := bodies[name]
		assert.Truef(t, found,
			"the plpgsql body of %s was not extracted; the trigger-body half of this audit "+
				"is reading nothing and would stay green through anything written in one", name)
		assert.Containsf(t, body, "RAISE EXCEPTION",
			"the body extracted for %s is not the function's code, so what this audit scans "+
				"is not what the database runs", name)
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

// foldingSite is one place in a migration where SQL folds case.
type foldingSite struct {
	// name is what a declaration is keyed by: a constraint name, an index name,
	// a function name. Empty when the construct carries none, which is itself a
	// finding.
	name string
	// kind says what the fold sits in, for the failure message.
	kind string
	// path is the migration it was found in.
	path string
	// autoPass is true for the one construct argued out of scope in the header:
	// a non-unique index expression.
	autoPass bool
}

// localeDependentSites returns every place in the migration tree where SQL folds
// case, whatever construct it sits in.
//
// It tokenizes rather than greps, for the reason the other SQL audits here do: a
// mention in a comment or inside a quoted string is not an expression, and a
// line-based scan would count one of those and miss an expression written across
// two lines. Function bodies ARE quoted strings in this repository, so they are
// recovered separately by [plpgsqlBodies] — see the header.
func localeDependentSites(t *testing.T) []foldingSite {
	t.Helper()

	var sites []foldingSite

	forEachSQLFile(t, "migrations", func(path string, body string) {
		for _, statement := range sqlStatements(blankSQLNoise(body)) {
			sites = append(sites, foldingSitesIn(t, path, statement)...)
		}

		for name, code := range plpgsqlBodies(body) {
			if !foldsCase(tokenizeSQL(code)) {
				continue
			}

			sites = append(sites, foldingSite{name: name, kind: "plpgsql function body", path: path})
		}
	})

	slices.SortFunc(sites, func(a, b foldingSite) int { return strings.Compare(a.name+a.path, b.name+b.path) })

	return sites
}

// foldingSitesIn returns the folding sites of one statement.
func foldingSitesIn(t *testing.T, path string, statement []sqlToken) []foldingSite {
	t.Helper()

	if !foldsCase(statement) {
		return nil
	}

	// An index is the one construct with an automatic pass, and only when it is
	// NOT unique. The distinction is the whole correction this file carries: a
	// unique index decides whether a write succeeds, which is an answer.
	if wordAt(statement, 0, "create") {
		unique := wordAt(statement, 1, "unique")

		at := 1
		if unique {
			at = 2
		}

		if wordAt(statement, at, "index") {
			at = skipWords(statement, at+1, "concurrently")
			at = skipWords(statement, at, "if", "not", "exists")
			name, _ := qualifiedSQLName(statement, at)

			kind := "index expression"
			if unique {
				kind = "UNIQUE index expression"
			}

			return []foldingSite{{name: name, kind: kind, path: path, autoPass: !unique}}
		}

		if wordAt(statement, 1, "function") {
			// The signature folds case, which is not the body; the body is read
			// separately. Reported so it cannot hide here.
			name, _ := qualifiedSQLName(statement, 2)

			return []foldingSite{{name: name, kind: "function signature", path: path}}
		}
	}

	// Everything else: find the CHECK constraints inside, and if the statement
	// folds somewhere that is NOT a CHECK, report that too rather than assuming
	// the CHECKs account for all of it.
	var (
		sites   []foldingSite
		inCheck int
	)

	for i := range statement {
		if !wordAt(statement, i, "check") {
			continue
		}

		inner, next, ok := parenBody(statement, i+1)
		if !ok {
			continue
		}
		if foldsCase(inner) {
			name := ""
			if wordAt(statement, i-2, "constraint") {
				name = statement[i-1].text
			}

			require.NotEmptyf(t, name,
				"%s has a CHECK that folds case and no CONSTRAINT name, so it cannot be "+
					"declared in caseFoldingDeclarations and an operator cannot name it to "+
					"drop it. Give it a name.", path)

			sites = append(sites, foldingSite{name: name, kind: "CHECK constraint", path: path})
		}

		inCheck += next - i
	}

	// A statement that folds case in more places than its CHECKs account for is
	// a DEFAULT, a GENERATED expression or something nobody has met yet. It is
	// reported without a name so the failure says where to look, rather than
	// being silently covered by a sibling CHECK's declaration.
	if len(sites) == 0 {
		// Keyed by PATH, because the construct carries no name. That is stable
		// enough to declare against: a migration is immutable once it has been
		// applied anywhere, which is the same property the migration runner
		// depends on.
		sites = append(sites, foldingSite{
			name: path,
			kind: "an expression outside any CHECK (a DEFAULT, a GENERATED column, a data " +
				"migration, or a shape this audit has not met)",
			path: path,
		})
	}

	return sites
}

// plpgsqlBodies returns the code of every plpgsql function in an SQL file, by
// function name.
//
// It reads the RAW text, because the bodies are single-quoted string literals
// and every other reader in this package blanks those away. Doubled quotes are
// the escape, which is the convention migration 000002 of the invoice module
// documents and depends on.
func plpgsqlBodies(raw string) map[string]string {
	bodies := map[string]string{}
	lowered := strings.ToLower(raw)

	for at := 0; ; {
		marker := strings.Index(lowered[at:], "language plpgsql as")
		if marker < 0 {
			return bodies
		}

		marker += at
		open := strings.IndexByte(raw[marker:], '\'')
		if open < 0 {
			return bodies
		}

		open += marker

		// Walk to the closing quote, treating '' as an escaped quote.
		end := open + 1
		for end < len(raw) {
			if raw[end] != '\'' {
				end++

				continue
			}
			if end+1 < len(raw) && raw[end+1] == '\'' {
				end += 2

				continue
			}

			break
		}

		bodies[plpgsqlNameBefore(raw[:marker])] = raw[open+1 : min(end, len(raw))]
		at = min(end+1, len(raw))
	}
}

// plpgsqlNameBefore returns the function name of the CREATE FUNCTION that the
// given prefix ends with.
func plpgsqlNameBefore(prefix string) string {
	lowered := strings.ToLower(prefix)

	marker := strings.LastIndex(lowered, "create function")
	if marker < 0 {
		marker = strings.LastIndex(lowered, "create or replace function")
	}
	if marker < 0 {
		return ""
	}

	rest := strings.TrimSpace(prefix[marker+len("create function"):])
	if paren := strings.IndexByte(rest, '('); paren >= 0 {
		rest = rest[:paren]
	}

	return strings.TrimSpace(rest)
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
