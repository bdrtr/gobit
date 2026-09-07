package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	b2bmodels "github.com/bdrtr/gobit/internal/modules/b2b/models"
	cartmodels "github.com/bdrtr/gobit/internal/modules/cart/models"
	customermodels "github.com/bdrtr/gobit/internal/modules/customer/models"
	invoicemodels "github.com/bdrtr/gobit/internal/modules/invoice/models"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
)

// emailNormalizers are every module's storage-form folder, by module name.
//
// The map is written out and the audit below checks it against the tree, so a
// module added tomorrow cannot join the repository without joining this
// comparison. A list that only ever holds what somebody remembered is the shape
// this repository has already been caught by twice — the depguard matrix and the
// provider registry names.
var emailNormalizers = map[string]func(string) string{
	"auth":     authmodels.NormalizeEmail,
	"b2b":      b2bmodels.NormalizeEmail,
	"cart":     cartmodels.NormalizeEmail,
	"customer": customermodels.NormalizeEmail,
	"invoice":  invoicemodels.NormalizeEmail,
	"order":    ordermodels.NormalizeEmail,
}

// TestEveryModuleFoldsAnEmailToTheSameBytes is the property five copies of one
// function have to hold and nothing else holds for them.
//
// # Why there are five copies
//
// A module may not import another module's models (Principle 2.1), so an
// address folded in auth and the same address folded in order pass through two
// different functions that happen to be identical. The duplication is FORCED by
// the architecture; what it forces is an agreement no compiler can check.
//
// # What breaks when they disagree, and why nobody would see it
//
// Neither failure raises an error. Both look like an absence:
//
//   - A guest order carries an e-mail and no customer id. The day that person
//     registers, the order and the account meet ONLY if both surfaces folded the
//     address to the same bytes. Folded differently, the person's history is
//     simply not there, and the shop concludes they are a new customer.
//   - A data-subject erasure resolves one person across every holder at once
//     (ADR 0033). A holder that folded differently answers "nothing found" for
//     rows it is holding — a green report, a complete-looking answer, and
//     personal data still in the database.
//
// # Why the inputs are the ones below
//
// Each row is a way two implementations can drift apart while both still look
// like "lower-case and trim": the case of the local part (which RFC 5321 leaves
// significant and this repository deliberately does not), the case of the
// domain, whitespace at either end, inner whitespace that trimming must NOT
// touch, a plus-tag that a "clever" normalizer might strip, dots in the local
// part that Gmail treats as insignificant and this repository must not, and
// non-ASCII case folding, where a Turkish locale's dotless i is the classic way
// for two implementations to disagree on one letter.
func TestEveryModuleFoldsAnEmailToTheSameBytes(t *testing.T) {
	t.Parallel()

	require.GreaterOrEqual(t, len(emailNormalizers), 2,
		"fewer than two normalizers are being compared; the audit proves nothing")

	for _, input := range []string{
		"", "   ", "ali@x.com",
		"Ali@X.com", "ALI@X.COM", "aLi@x.CoM",
		"  ali@x.com  ", "\tali@x.com\n",
		"a li@x.com",
		"ali+tag@x.com", "a.l.i@x.com",
		// The two rows below are written as \u escapes rather than as letters, and
		// the reason is the language ratchet rather than taste: this file is
		// English, the detector's diacritic lane reads the SOURCE, and a Turkish
		// letter in it would need an exemption entry for what is plainly data. The
		// escapes are U+0130 (capital I WITH a dot, whose lower case is "i") and
		// U+00D6 (capital O with diaeresis) — the exact letters on which two
		// implementations of "lower-case it" disagree when one of them uses a
		// Turkish locale.
		"AL\u0130@X.COM", "\u0130hsan@\u00d6rnek.com", "ali@x.co.uk",
		"user@localhost", "a@b",
	} {
		t.Run(strings.ReplaceAll(input, " ", "_"), func(t *testing.T) {
			t.Parallel()

			var (
				first     string
				firstName string
			)

			for _, name := range sortedNormalizerNames() {
				got := emailNormalizers[name](input)
				if firstName == "" {
					first, firstName = got, name

					continue
				}

				assert.Equal(t, first, got,
					"%s and %s fold %q differently (%q vs %q).\n"+
						"They are five copies of one function because a module may not import "+
						"another module's models, and the agreement between them is what makes "+
						"a guest order meet the account that person later opens, and what makes "+
						"an erasure find every holder's rows. A disagreement raises no error: "+
						"it looks like an absence.",
					firstName, name, input, first, got)
			}
		})
	}
}

// TestEveryEmailNormalizerInTheTreeIsCompared is the half that survives a new
// module.
//
// The map above is hand-written, and a hand-written list is right on the day it
// is written. This walks the module tree for every exported NormalizeEmail and
// fails when one of them is not in the comparison — which is the moment a sixth
// module joins the repository with a folding rule nobody checked.
//
// It fails in the other direction too: a name in the map that no longer exists
// in the tree means the map is describing a module that is gone.
func TestEveryEmailNormalizerInTheTreeIsCompared(t *testing.T) {
	t.Parallel()

	found := normalizersInTree(t)

	require.NotEmpty(t, found,
		"no NormalizeEmail was found under %s/; the audit has gone BLIND and the "+
			"comparison above is comparing a list against nothing", modulesDir)

	for _, module := range found {
		assert.Contains(t, emailNormalizers, module,
			"the %s module folds e-mails with its own NormalizeEmail and is NOT in "+
				"emailNormalizers, so nothing checks that it agrees with the others.\n"+
				"Add it to the map; the cost of the disagreement is a person whose guest "+
				"order never meets their account, and an erasure that reports success "+
				"while leaving rows behind.", module)
	}

	for module := range emailNormalizers {
		assert.Contains(t, found, module,
			"emailNormalizers names %q but no NormalizeEmail was found in that module; "+
				"the map is describing a module that has moved or gone", module)
	}
}

// normalizersInTree returns the modules that declare an exported NormalizeEmail.
//
// It parses rather than greps for the reason the other AST audits here do: a
// mention in a comment or a string is not a declaration, and a text scan would
// count one and let a real one hide behind a build tag.
func normalizersInTree(t *testing.T) []string {
	t.Helper()

	root := filepath.Join(repoRoot, modulesDir)
	fset := token.NewFileSet()
	seen := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}

		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil || fn.Name.Name != "NormalizeEmail" {
				continue
			}

			// The module is the first path segment under internal/modules.
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}

			seen[strings.Split(filepath.ToSlash(rel), "/")[0]] = true
		}

		return nil
	})
	require.NoError(t, err, "%s could not be walked", modulesDir)

	out := make([]string, 0, len(seen))
	for module := range seen {
		out = append(out, module)
	}

	return out
}

// sortedNormalizerNames returns the map's keys in a stable order.
//
// The order decides which implementation the others are compared AGAINST, and a
// random one would make the failure message name a different pair on every run —
// which is how a flaky-looking message hides a real disagreement.
func sortedNormalizerNames() []string {
	out := make([]string, 0, len(emailNormalizers))
	for name := range emailNormalizers {
		out = append(out, name)
	}

	slices.Sort(out)

	return out
}

// emailFoldExemptions names a module that stores an e-mail address and is
// deliberately NOT in the comparison above, with the reason it is not.
//
// An exemption is not a way to make the audit quiet. It is the opposite: a
// module that stores an address and folds it somewhere the comparison cannot
// read is the case this audit is WORST at, because it looks identical to a
// module that does not store one at all. Writing the reason down turns a silence
// into a claim somebody can disagree with.
var emailFoldExemptions = map[string]string{
	// EMPTY, and it has been non-empty exactly once. From 2026-09-07 it held
	// "invoice", which stored buyer_email VERBATIM and folded in SQL with
	// lower(buyer_email) = lower($1) — a SIXTH rule whose result depended on the
	// CLUSTER, and on a --locale=C database folded ASCII only. That entry is
	// gone because the defect it described was fixed rather than tolerated:
	// migration 000003 gave the module a Go-folded column, and invoice is in
	// emailNormalizers above, compared against the other five like anything
	// else. D27 has the reproduction and ADR 0038 has the decision.
	//
	// The map stays because the NEXT module to hold an address and fold it
	// somewhere unreachable needs a place to say so, and because an audit whose
	// escape hatch was deleted is an audit somebody will work around instead.
}

// TestEveryModuleThatStoresAnEmailFoldsItInGoOrSaysWhyNot closes the hole the
// two audits above leave between them.
//
// The comparison checks the modules that HAVE a Go NormalizeEmail, and the tree
// walk checks that none of those is missing from it. Neither asks the question
// that matters: which modules store an address at all? A module that holds one
// and folds it somewhere other than a Go function — in SQL, in a migration, in a
// handler — is invisible to both, and invisible is the same shape as absent.
//
// The ground truth is the SCHEMA rather than a list written here. A module that
// declares a column with "email" in its name holds addresses, that fact is in
// the migration where it cannot be forgotten, and a module that grows one
// tomorrow joins this audit the day the column lands.
func TestEveryModuleThatStoresAnEmailFoldsItInGoOrSaysWhyNot(t *testing.T) {
	t.Parallel()

	holders := modulesWithAnEmailColumn(t)

	require.NotEmpty(t, holders,
		"no module was found to declare an e-mail column, which cannot be true while "+
			"%d of them fold one; the schema scan has gone blind", len(emailNormalizers))

	for _, module := range holders {
		_, folds := emailNormalizers[module]
		reason, exempt := emailFoldExemptions[module]

		assert.Truef(t, folds || exempt,
			"the %s module declares an e-mail column and neither folds it with a Go "+
				"NormalizeEmail nor says why not.\n"+
				"Either give it one — so the comparison above covers it — or add an entry "+
				"to emailFoldExemptions stating where it folds instead and what that costs. "+
				"A holder nobody compares is indistinguishable from a module that stores no "+
				"address, and the difference is a person whose data is found or not found "+
				"when they ask to be forgotten.", module)

		if exempt {
			assert.NotEmpty(t, strings.TrimSpace(reason),
				"%s is exempt from the fold comparison with an EMPTY reason, which is a "+
					"silence with a name on it", module)
			assert.NotContains(t, emailNormalizers, module,
				"%s is both compared and exempted; one of the two is a leftover and a "+
					"reader cannot tell which", module)
		}
	}
}

// modulesWithAnEmailColumn reads every module's up-migrations and returns the
// modules that declare a column whose name contains "email".
//
// It reads the MIGRATIONS rather than the Go models because the migration is
// what the database actually holds: a struct field can be dropped from a model
// while the column keeps the data, and it is the data a data subject is asking
// about.
func modulesWithAnEmailColumn(t *testing.T) []string {
	t.Helper()

	root := filepath.Join(repoRoot, modulesDir)
	// A column definition inside CREATE TABLE: a name containing "email",
	// followed by a text-ish type. Anchored on the name so a mention of the word
	// in a constraint or a comment does not count as a column.
	column := regexp.MustCompile(`(?i)^\s*"?(\w*email\w*)"?\s+(text|varchar|citext)\b`)
	seen := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".up.sql") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		for _, line := range strings.Split(string(body), "\n") {
			// Strip a trailing line comment so a column named in prose after --
			// cannot be mistaken for a declaration.
			if i := strings.Index(line, "--"); i >= 0 {
				line = line[:i]
			}
			if !column.MatchString(line) {
				continue
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}

			seen[strings.Split(filepath.ToSlash(rel), "/")[0]] = true
		}

		return nil
	})
	require.NoError(t, err, "%s could not be walked for migrations", modulesDir)

	out := make([]string, 0, len(seen))
	for module := range seen {
		out = append(out, module)
	}

	slices.Sort(out)

	return out
}
