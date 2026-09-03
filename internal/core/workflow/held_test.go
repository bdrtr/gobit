package workflow_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/workflow"
)

// storeSourcePath is the file that declares the step statuses.
//
// The list below is READ from it rather than typed here: a hand-written copy of
// the four constants would agree with itself forever, and the one thing this
// test exists to catch is a FIFTH status being added without anyone deciding
// whether its side effect is held.
const storeSourcePath = "store.go"

// stepStatusTypeName is the declared type whose constants are collected.
const stepStatusTypeName = "StepStatus"

// TestHeldStatusListMatchesThePredicate keeps the SQL parameter and the Go
// predicate from drifting apart.
//
// [workflow.HeldStepStatuses] exists only because PostgreSQL cannot call
// [workflow.StepStatus.Held]; the listing surface sends the slice as a query
// parameter. That makes it a second copy of the same decision, and a second
// copy is exactly the failure this repository keeps paying for: the engine
// would go on treating a status as "work was done" while the operator's listing
// silently skipped it, so a hanging reservation would exist and appear nowhere.
func TestHeldStatusListMatchesThePredicate(t *testing.T) {
	t.Parallel()

	declared := declaredStepStatuses(t)
	require.Len(t, declared, 4,
		"expected the four documented step statuses, found %v.\nIf a status was added or "+
			"removed, decide what Held() means for it and update this count deliberately.",
		declared)

	held := workflow.HeldStepStatuses()
	require.NotEmpty(t, held, "no status is held; every abandoned execution would look clean")

	for _, status := range declared {
		wantHeld := slices.Contains(held, status)
		assert.Equal(t, wantHeld, status.Held(),
			"%q: HeldStepStatuses() says held=%v but Held() says held=%v.\n"+
				"The list is what reaches the database; the predicate is what the engine "+
				"uses. They must be the same decision.", status, wantHeld, status.Held())
	}

	for _, status := range held {
		assert.Contains(t, declared, status,
			"HeldStepStatuses() names %q, which is not a declared %s constant; the list "+
				"would filter on a value no row can ever carry", status, stepStatusTypeName)
	}
}

// TestHeldStatusListIsFreshPerCall proves the caller cannot reach into the
// engine's decision.
//
// A package-level slice returned as-is would let any caller sort it, truncate
// it or overwrite an element, and the engine's own abandonment decision reads
// the same values. The corruption would be silent and would show up as an
// execution the operator's listing never mentions.
func TestHeldStatusListIsFreshPerCall(t *testing.T) {
	t.Parallel()

	first := workflow.HeldStepStatuses()
	require.NotEmpty(t, first)
	first[0] = "clobbered"

	second := workflow.HeldStepStatuses()
	assert.NotContains(t, second, workflow.StepStatus("clobbered"),
		"a caller's write reached the next caller's list")
	assert.True(t, second[0].Held(), "the first entry no longer satisfies the predicate")
}

// declaredStepStatuses reads every StepStatus constant out of the source.
//
// The scan is deliberately shape-based (a typed constant whose declared type is
// the status type) rather than name-based: a status added as StepSkipped would
// not match a "Step*Failed" name pattern, and the check would go quiet at the
// exact moment it mattered.
func declaredStepStatuses(t *testing.T) []workflow.StepStatus {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, storeSourcePath, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "%s could not be parsed", storeSourcePath)

	var found []workflow.StepStatus
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := value.Type.(*ast.Ident)
			if !ok || ident.Name != stepStatusTypeName {
				continue
			}
			for _, expr := range value.Values {
				literal, ok := expr.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				text, err := strconv.Unquote(literal.Value)
				require.NoError(t, err)
				found = append(found, workflow.StepStatus(text))
			}
		}
	}

	require.NotEmpty(t, found,
		"no %s constant was found in %s; the scan has gone blind and would accept any "+
			"held list at all", stepStatusTypeName, storeSourcePath)

	return found
}
