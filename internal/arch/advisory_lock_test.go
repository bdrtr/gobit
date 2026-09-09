package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// advisoryLockArgument matches a lock call in SQL and captures what it is
// given.
//
// Every form is matched — transaction-scoped, session-scoped, the try variants
// and the unlock — because all of them take a key out of the SAME space and
// differ only in when the lock is released.
var advisoryLockArgument = regexp.MustCompile(
	`(pg_(?:try_)?advisory_(?:xact_)?(?:un)?lock)\(([^)]*)\)`)

// The two ways a lock's number announces itself in a declaration.
//
// The population is derived from the NAMES rather than from a list, for the
// reason every audit in this package gives: a list is a record of what somebody
// remembered, and the lock added tomorrow is exactly the one that would be
// missing from it.
//
// Both shapes are in the tree and both are correct. A CLASS is the upper half
// of a key and the module fills the lower half per row or per customer; a KEY
// is the whole number, which is what a lock with exactly one instance needs
// (core/link takes one for the whole declaration pass, keyed on the ASCII of
// "link_def"). Their classes are compared in the same space, because that is
// the space PostgreSQL keeps them in.
const (
	lockClassSuffix = "LockClass"
	lockKeySuffix   = "LockKey"
)

// lockClassFloor is the smallest population that could be the real one.
//
// Two classes exist today (the order module's customer spending lock and the
// product module's category reparent lock). A run that finds fewer has a blind
// detector rather than a tidy repository, and a population of one has no
// duplicates by definition.
const lockClassFloor = 2

// TestAdvisoryLockClassesAreUnique holds the rule the key space forces.
//
// # Why the classes matter at all
//
// PostgreSQL's advisory locks live in ONE key space across the whole database.
// Two locks taken for unrelated purposes on the same number block each other,
// and nothing anywhere reports it: the second caller simply waits, then
// proceeds, and every test still passes because waiting is not failing. The
// convention that closes it is the order module's — the upper 32 bits of the
// key are a CLASS number — and a convention nothing checks is a convention
// until the day two people pick 2.
//
// # What is checked, and what it deliberately cannot see
//
// Every constant whose name ends in "LockClass" or "LockKey" must land on a
// distinct class, a key being read as its upper 32 bits. What this does NOT
// check is that a lock site uses one: a key written as a bare number at the
// call would pass, which is why the sibling assertion below requires every
// advisory lock call to sit in a file that declares one.
//
// It found a collision on the run it was written: the category reparent lock
// (ADR 0091) had been given class 2, which `internal/core/job` has held since
// it was written (D47).
func TestAdvisoryLockClassesAreUnique(t *testing.T) {
	t.Parallel()

	classes := lockClassesInTree(t)
	require.GreaterOrEqualf(t, len(classes), lockClassFloor,
		"only %d lock class constant(s) were found; the detector has gone blind",
		len(classes))

	byValue := map[int64][]string{}
	for name, value := range classes {
		byValue[value] = append(byValue[value], name)
	}

	for value, names := range byValue {
		assert.Lenf(t, names, 1,
			"advisory lock class %d is claimed by %s; the key space is ONE across the "+
				"database, so two classes with the same number hold each other up and "+
				"nothing reports the wait",
			value, strings.Join(names, " and "))
	}
}

// TestNoAdvisoryLockEmbedsItsKey keeps every key where it can be compared.
//
// The uniqueness check above reads DECLARATIONS. A lock whose number is written
// inside the SQL — "pg_advisory_xact_lock(42)" — is invisible to it: nothing
// declares 42, nothing compares it, and the day another subsystem picks the
// same number the two wait on each other in silence. Every lock in the tree
// therefore passes its key as a parameter, and this is the assertion that keeps
// it that way.
//
// The call sites are not all in the same package as their class: the job
// scheduler computes the key in internal/core/job and takes the lock in
// internal/core/job/jobpg, which is why this is written per CALL rather than
// per file.
func TestNoAdvisoryLockEmbedsItsKey(t *testing.T) {
	t.Parallel()

	var calls int
	for _, tree := range productionTrees {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			require.NoErrorf(t, err, "%s could not be parsed", file)

			// STRING LITERALS only. The same words appear in the comments that
			// explain the locks — internal/app/migrate.go writes
			// "SELECT pg_advisory_lock() on context.Background()" while
			// describing golang-migrate's unbounded wait — and a gate that read
			// prose would be auditing the explanation instead of the statement.
			ast.Inspect(parsed, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				for _, match := range advisoryLockArgument.FindAllStringSubmatch(literal.Value, -1) {
					calls++
					assert.Truef(t, strings.HasPrefix(match[2], "$"),
						"%s: %s takes %q as its key; a number written into the statement "+
							"is declared nowhere and compared with nothing",
						file, match[1], match[2])
				}

				return true
			})
		}
	}

	require.GreaterOrEqualf(t, calls, lockClassFloor,
		"only %d advisory lock call(s) were found; the detector has gone blind", calls)
}

// lockClassesInTree returns every class constant by name.
//
// The value is read from the declaration's literal, so a class computed at
// runtime is not seen — and that is the point of requiring a constant: a class
// that cannot be read here cannot be compared with the others either.
func lockClassesInTree(t *testing.T) map[string]int64 {
	t.Helper()

	out := map[string]int64{}
	for _, tree := range productionTrees {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			require.NoErrorf(t, err, "%s could not be parsed", file)

			for _, decl := range parsed.Decls {
				general, ok := decl.(*ast.GenDecl)
				if !ok || general.Tok != token.CONST {
					continue
				}
				collectLockClasses(t, file, general, out)
			}
		}
	}

	return out
}

// collectLockClasses reads the class constants of one declaration.
func collectLockClasses(t *testing.T, file string, decl *ast.GenDecl, out map[string]int64) {
	t.Helper()

	for _, spec := range decl.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range value.Names {
			isClass := strings.HasSuffix(name.Name, lockClassSuffix)
			isKey := strings.HasSuffix(name.Name, lockKeySuffix)
			if (!isClass && !isKey) || i >= len(value.Values) {
				continue
			}

			literal, ok := value.Values[i].(*ast.BasicLit)
			if !ok {
				// A number built from another constant — the category
				// reparent key is its own class shifted — carries no new
				// class, and the constant it is built from is counted on its
				// own line. A class must still be a literal, because one that
				// cannot be read here cannot be compared with the others.
				require.Falsef(t, isClass,
					"%s: %s is not a plain number; a class has to be readable from its "+
						"declaration for the classes to be comparable at all",
					file, name.Name)

				continue
			}

			number, err := strconv.ParseInt(literal.Value, 0, 64)
			require.NoErrorf(t, err, "%s: %s is not an integer", file, name.Name)
			if isKey {
				number >>= 32
			}
			out[file+":"+name.Name] = number
		}
	}
}
