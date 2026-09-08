package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ADR 0061's refusal: gap A11 — where translated content lives
// — is a DECISION and not a build, and the tree may not decide it by accident.
//
// # What the two gates below actually watch
//
// A locale is not a word this repository lacks. It appears in ninety-nine
// places across the production Go files, and NONE of them outside the web push
// plugin is a name: every one is PROSE about the PostgreSQL cluster's collation
// (ADR 0015), written in a comment or inside a log message. The plugin is the
// one component that made the word a key — a column, a struct field, a wire
// field — and it did so for a device rather than for a shopper (ADR 0018).
//
// That partition is the whole of what these gates hold. Prose stays free: a
// comment may say anything about a C-locale cluster, and nothing here reads
// one. What is refused is a NAME — an identifier, a struct tag or a SQL column
// — because a name is a key, and a key is the storage half of A11 arriving
// without the record that decides it.
//
// # Why refusing is the right instrument and not a prohibition
//
// ADR 0050 measured that nothing on a gobit request carries a locale, and
// ADR 0061 measured that this is still true. Neither half of the language axis
// may be built alone: a locale with no source is a column nothing can key into,
// and a locale with nothing to vary is a published URL shape with no consumer,
// which this repository's first-consumer rule refuses outright.
//
// So the second component that names a locale is not a bug — it is ADR 0061's
// TRIGGER arriving, and a red test is how it announces itself. Answering the
// failure means writing the record that supersedes 0061 and deleting these
// gates, exactly as [TestTheOrderAndPaymentModulesDeclareNoSoftDelete] is
// answered.

// localeToken is the word these gates watch for, matched case-insensitively.
const localeToken = "locale"

// localeBearer is the ONE component allowed to name a locale.
//
// It is named rather than derived, and the exemption is checked from BOTH
// sides: a name outside it fails, and the bearer falling silent fails too. A
// list nobody uses any more is an exemption that has stopped meaning anything,
// and it would leave these gates green over a tree that had changed underneath
// them.
const localeBearer = "plugins/webpush"

// localeScan is what one production file yields to [scanFileForLocale].
type localeScan struct {
	// names are the identifiers and struct tags that name a locale.
	names []string
	// identifiers is EVERY identifier the file yielded, locale or not. It is
	// the per-file blindness floor: a parse that silently produced an empty
	// tree would report no name and approve the file.
	identifiers int
}

// scanFileForLocale reads one production Go file for names carrying a locale.
//
// The file is parsed WITHOUT comments on purpose. Comments are where all
// thirty-five non-plugin mentions of the word live, and every one of them is
// about the cluster's collation; reading them would make this gate a style
// rule about a word instead of a rule about a key. Log messages and error
// strings are string literals and are passed over for the same reason — the
// exception being a struct TAG, which is a string literal that IS a wire name.
func scanFileForLocale(t *testing.T, path string) localeScan {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err,
		"%s could not be parsed, so the walk read NO name from it and would have "+
			"approved whatever it holds", short(path))

	var scan localeScan
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			scan.identifiers++
			if strings.Contains(strings.ToLower(typed.Name), localeToken) {
				scan.names = append(scan.names, typed.Name)
			}
		case *ast.Field:
			if typed.Tag != nil && strings.Contains(strings.ToLower(typed.Tag.Value), localeToken) {
				scan.names = append(scan.names, typed.Tag.Value)
			}
		}

		return true
	})

	return scan
}

// TestOnlyOneComponentNamesALocale is the Go half of ADR 0061's refusal.
//
// # Where the population comes from
//
// From [productionTrees] and the walk over them, never from the property. A
// file that gained a locale name is still in the population — it is the tree
// that puts it there — so a violation FAILS this test rather than quietly
// leaving its scope. The three floors are what stop a narrowed walk from
// approving by reading nothing: every tree must yield files, the parse must
// yield identifiers, and the bearer must still hold names of its own.
func TestOnlyOneComponentNamesALocale(t *testing.T) {
	t.Parallel()

	identifiers, bearer := 0, 0
	for _, tree := range productionTrees {
		files := treeProductionFiles(t, tree)
		require.NotEmpty(t, files,
			"the %q tree yielded no production Go file. The walk has gone BLIND there, "+
				"and a locale arriving in it would be read by nothing.", tree)

		for _, path := range files {
			scan := scanFileForLocale(t, path)
			identifiers += scan.identifiers

			if strings.HasPrefix(short(path), localeBearer+"/") {
				bearer += len(scan.names)

				continue
			}

			assert.Empty(t, scan.names,
				"%s NAMES a locale: %v.\n"+
					"Prose about the cluster's collation is free and this gate does not read "+
					"it; a NAME is different, because a name is a key. gap A11 — where "+
					"translated content lives — is a decision (ADR 0061) and not a gap, and "+
					"the second component to key on a locale is that record's trigger "+
					"arriving rather than a routine addition. Write the record that "+
					"supersedes 0061 and delete this gate, or take the name out.",
				short(path), scan.names)
		}
	}

	require.NotZero(t, identifiers,
		"the walk parsed files and read NOT ONE identifier out of them; every file "+
			"would have reported no locale name and passed")
	require.NotZero(t, bearer,
		"%s no longer names a locale anywhere. The exemption is the one thing this "+
			"gate forgives, so an exemption nobody uses is not a saving — it means the "+
			"tree moved and this gate is holding a shape that is gone. Retire it "+
			"deliberately, in the record that supersedes ADR 0061.", localeBearer)
}

// localeUpMigrations returns every forward migration in the repository.
//
// The walk is over the whole tree rather than over a list of owners, for the
// reason D10 and D13 paid for twice: a population derived from who is expected
// to have migrations cannot see the owner nobody expected.
func localeUpMigrations(t *testing.T) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The root is compared by PATH and not by name: repoRoot is "../..",
			// whose base name begins with a dot, so a name test alone would skip
			// the whole repository on the walk's very first call and leave this
			// gate reading nothing.
			if path == repoRoot {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" {
				return filepath.SkipDir
			}

			return nil
		}
		if strings.HasSuffix(path, upMigrationSuffix) {
			out = append(out, path)
		}

		return nil
	})
	require.NoError(t, err, "the migration walk from %s failed", repoRoot)

	return out
}

// localeStripSQLComments removes the `--` line comments from a migration.
//
// It is deliberately the only stripping done. Every migration that mentions a
// locale outside the web push plugin mentions it in a `--` comment, describing
// a C-locale cluster, so that is the one form that has to be removed for the
// gate to mean what it says. A `/* */` block carrying the word would survive
// and FAIL this test — which is the safe direction for a mistake to fall,
// because the failure is loud and the alternative is a column read by nobody.
func localeStripSQLComments(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if at := strings.Index(line, "--"); at >= 0 {
			lines[i] = line[:at]
		}
	}

	return strings.Join(lines, "\n")
}

// TestNoTableOutsideTheDeviceRegistryDeclaresALocale is the schema half.
//
// The Go half above refuses the READ of a locale; this one refuses the ROW.
// They are separate because the halves can arrive separately: a column added
// "for later" reads to a migration reviewer as free, and it is exactly the
// shape ADR 0050 warned about — the sibling-column answer arriving one column
// at a time, without a record.
func TestNoTableOutsideTheDeviceRegistryDeclaresALocale(t *testing.T) {
	t.Parallel()

	migrations := localeUpMigrations(t)
	require.NotEmpty(t, migrations,
		"no forward migration was found at all; the schema scan has gone BLIND")

	bearer := 0
	for _, path := range migrations {
		body, err := os.ReadFile(path)
		require.NoError(t, err, "%s could not be read", short(path))

		statements := strings.ToLower(localeStripSQLComments(string(body)))
		hits := strings.Count(statements, localeToken)

		if strings.HasPrefix(short(path), localeBearer+"/") {
			bearer += hits

			continue
		}

		assert.Zero(t, hits,
			"%s declares a locale in SQL.\n"+
				"A locale column is the STORAGE half of gap A11, and ADR 0061 decided that "+
				"neither half of the language axis is built alone: a column keyed by a "+
				"locale no request can supply is a column nothing can key into. If this is "+
				"the trigger arriving, it arrives with the record that supersedes 0061.",
			short(path))
	}

	require.NotZero(t, bearer,
		"%s no longer declares a locale column. That column is what this gate "+
			"forgives, and forgiving something that is gone is how an exemption goes "+
			"quietly stale.", localeBearer)
}
