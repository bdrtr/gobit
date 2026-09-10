package link

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// testDef is the shared definition of the in-package tests.
func testDef(name string, c Cardinality) LinkDefinition {
	return LinkDefinition{
		Name:        name,
		From:        LinkSide{Module: "product", Field: "variant_id"},
		To:          LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: c,
	}
}

// mustLinkTable builds the runtime information from a validated definition.
func mustLinkTable(t *testing.T, def LinkDefinition) *linkTable {
	t.Helper()

	lt, err := newLinkTable(def)
	require.NoError(t, err)
	return lt
}

// TestDefinitionsRegistry verifies the registry's basic contract: what is
// written is read back, what is not written is not found, and names come back
// sorted.
func TestDefinitionsRegistry(t *testing.T) {
	defs := newDefinitions()

	assert.Empty(t, defs.names())
	_, ok := defs.lookup("product_price")
	assert.False(t, ok, "an empty registry must hold no record")

	defs.put(mustLinkTable(t, testDef("product_price", OneToMany)))
	defs.put(mustLinkTable(t, testDef("cart_customer", OneToOne)))

	assert.Equal(t, []string{"cart_customer", "product_price"}, defs.names(),
		"names must come back sorted so that error messages are reproducible")

	lt, ok := defs.lookup("product_price")
	require.True(t, ok)
	assert.Equal(t, "link_product_price", lt.table)
	assert.Equal(t, OneToMany, lt.def.Cardinality)
}

// TestDefinitionsRegistryIsConcurrencySafe verifies that the registry does not
// race under concurrent use (it is meaningful with -race).
func TestDefinitionsRegistryIsConcurrencySafe(t *testing.T) {
	defs := newDefinitions()
	lt := mustLinkTable(t, testDef("product_price", ManyToMany))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			defs.put(lt)
		}()
		go func() {
			defer wg.Done()
			defs.lookup("product_price")
			defs.names()
		}()
	}
	wg.Wait()

	assert.Equal(t, []string{"product_price"}, defs.names())
}

// TestLinkTableNaming verifies that the derived table and index names have the
// expected shape; the index names are also used in error mapping.
func TestLinkTableNaming(t *testing.T) {
	lt := mustLinkTable(t, testDef("product_price", OneToOne))

	assert.Equal(t, "link_product_price", lt.table)
	assert.Equal(t, "link_product_price_from_uniq", lt.fromIndex)
	assert.Equal(t, "link_product_price_to_uniq", lt.toIndex)
}

// TestLinkTableSQLUsesParameters verifies that the runtime statements carry
// the ids as PARAMETERS and that the ordering is deterministic.
func TestLinkTableSQLUsesParameters(t *testing.T) {
	lt := mustLinkTable(t, testDef("product_price", ManyToMany))

	assert.Contains(t, lt.insert, "$1")
	assert.Contains(t, lt.insert, "$2")
	assert.Contains(t, lt.insert, "ON CONFLICT (from_id, to_id) DO NOTHING",
		"the conflict target must be EXPLICIT; a targetless DO NOTHING would swallow a cardinality violation")
	assert.Contains(t, lt.remove, "WHERE from_id = $1 AND to_id = $2")
	assert.Contains(t, lt.list, "ORDER BY to_id", "the ordering must be deterministic")
	assert.Contains(t, lt.listMany, "= ANY($1)", "a batch read must happen in a single query")
	assert.Contains(t, lt.listMany, "ORDER BY from_id, to_id")

	for _, stmt := range []string{lt.insert, lt.remove, lt.list, lt.listMany} {
		assert.Contains(t, stmt, lt.table)
	}
}

// TestDDLEnforcesCardinality verifies that cardinality is enforced by a
// database constraint and NOT in the application layer. A failure of this test
// means two concurrent requests can silently break the cardinality.
func TestDDLEnforcesCardinality(t *testing.T) {
	tests := map[string]struct {
		cardinality Cardinality
		fromUnique  bool
		toUnique    bool
	}{
		"one_to_one":   {OneToOne, true, true},
		"one_to_many":  {OneToMany, false, true},
		"many_to_many": {ManyToMany, false, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			lt := mustLinkTable(t, testDef("product_price", tc.cardinality))
			stmts := lt.ddl()
			all := strings.Join(stmts, "\n")

			assert.Contains(t, stmts[0], "CREATE TABLE IF NOT EXISTS link_product_price")
			assert.Contains(t, stmts[0], "PRIMARY KEY (from_id, to_id)",
				"the uniqueness of the pair holds under every cardinality")
			assert.NotContains(t, all, "REFERENCES",
				"a link table gives an FK to NO module table (plan Section 2.2)")

			fromIdx := "CREATE UNIQUE INDEX IF NOT EXISTS " + lt.fromIndex
			toIdx := "CREATE UNIQUE INDEX IF NOT EXISTS " + lt.toIndex
			if tc.fromUnique {
				assert.Contains(t, all, fromIdx+" ON link_product_price (from_id)")
			} else {
				assert.NotContains(t, all, fromIdx)
			}
			if tc.toUnique {
				assert.Contains(t, all, toIdx+" ON link_product_price (to_id)")
			} else {
				assert.NotContains(t, all, toIdx)
			}

			// Define is called on every startup, so every statement must be
			// idempotent. Until ADR 0116 that was the same thing as carrying
			// "IF NOT EXISTS", because every statement was a creation; a drop
			// spells the same guarantee "IF EXISTS".
			for _, stmt := range stmts {
				guard := "IF NOT EXISTS"
				if strings.HasPrefix(stmt, "DROP ") {
					guard = "IF EXISTS"
				}
				assert.Contains(t, stmt, guard,
					"Define is called on every startup; the DDL must be idempotent")
			}
		})
	}
}

// TestDefineFastPathDetectsConflict verifies that a different definition under
// the same name is reported as a conflict without EVER going to the database;
// there is no pool, and yet the returned error is Conflict rather than
// Unavailable.
func TestDefineFastPathDetectsConflict(t *testing.T) {
	ctx := context.Background()
	svc := newService(nil, nil)
	svc.defs.put(mustLinkTable(t, testDef("product_price", OneToMany)))

	t.Run("the same definition is idempotent", func(t *testing.T) {
		require.NoError(t, svc.Define(ctx, testDef("product_price", OneToMany)))
	})

	t.Run("a change of cardinality is a conflict", func(t *testing.T) {
		err := svc.Define(ctx, testDef("product_price", ManyToMany))

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err),
			"the error class must be KindConflict, got %v", errors.KindOf(err))
		assert.Equal(t, "link_definition_conflict", errors.CodeOf(err))
		assert.Contains(t, err.Error(), "one_to_many", "the message must show the stored definition")
		assert.Contains(t, err.Error(), "many_to_many", "the message must show the incoming definition")
	})

	t.Run("a change of end is a conflict", func(t *testing.T) {
		def := testDef("product_price", OneToMany)
		def.To.Module = "inventory"

		err := svc.Define(ctx, def)

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Equal(t, "link_definition_conflict", errors.CodeOf(err))
	})
}

// TestStoredDefinitionMatches verifies that the comparison against the durable
// ledger really compares every field. A missing field means a definition that
// changed between releases is accepted silently.
func TestStoredDefinitionMatches(t *testing.T) {
	def := testDef("product_price", OneToMany)
	equal := storedDefinition{
		fromModule:  "product",
		fromField:   "variant_id",
		toModule:    "pricing",
		toField:     "price_set_id",
		cardinality: "one_to_many",
	}

	assert.True(t, equal.matches(def))
	assert.Contains(t, equal.String(), "product.variant_id -> pricing.price_set_id")

	different := map[string]func(s *storedDefinition){
		"from module":           func(s *storedDefinition) { s.fromModule = "cart" },
		"from field":            func(s *storedDefinition) { s.fromField = "cart_id" },
		"to module":             func(s *storedDefinition) { s.toModule = "inventory" },
		"to field":              func(s *storedDefinition) { s.toField = "item_id" },
		"cardinality":           func(s *storedDefinition) { s.cardinality = "many_to_many" },
		"unrecognized cardinal": func(s *storedDefinition) { s.cardinality = "unknown(9)" },
	}
	for name, mutate := range different {
		t.Run(name, func(t *testing.T) {
			s := equal
			mutate(&s)
			assert.False(t, s.matches(def), "a changed field must be seen as a conflict")
		})
	}
}

// TestWriteErrorMapsCardinalityViolation verifies that the database's
// uniqueness error is turned into a typed and READABLE conflict: the caller
// must be able to tell from the error which end is taken.
func TestWriteErrorMapsCardinalityViolation(t *testing.T) {
	lt := mustLinkTable(t, testDef("product_price", OneToOne))

	t.Run("the from end is taken", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "23505", ConstraintName: lt.fromIndex}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err),
			"the error class must be KindConflict, got %v", errors.KindOf(err))
		assert.Equal(t, "link_cardinality_violation", errors.CodeOf(err))
		assert.Contains(t, err.Error(), "var_1")
		assert.Contains(t, err.Error(), "one_to_one")
	})

	t.Run("the to end is taken", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "23505", ConstraintName: lt.toIndex}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Equal(t, "link_cardinality_violation", errors.CodeOf(err))
		assert.Contains(t, err.Error(), "ps_1")
	})

	t.Run("an unrecognized constraint is still a conflict", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "23505", ConstraintName: "other_constraint"}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Contains(t, err.Error(), "other_constraint",
			"the message must write which constraint was violated")
	})

	t.Run("a non-uniqueness error is an internal error", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "42P01", Message: "relation does not exist"}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindInternal),
			"the error class must be KindInternal, got %v", errors.KindOf(err))
		assert.Equal(t, "link_query_failed", errors.CodeOf(err))
	})
}

// TestWrapDB verifies the classification of driver errors; above all, a
// cancellation must not be confused with "the database is broken".
func TestWrapDB(t *testing.T) {
	assert.NoError(t, wrapDB(nil, codeQueryFailed, "cannot happen"))

	canceled := wrapDB(context.Canceled, codeQueryFailed, "link %q could not be read", "product_price")
	require.Error(t, canceled)
	assert.True(t, errors.HasKind(canceled, errors.KindUnavailable),
		"the error class must be KindUnavailable, got %v", errors.KindOf(canceled))
	assert.Equal(t, "link_canceled", errors.CodeOf(canceled))
	assert.True(t, errors.Is(canceled, context.Canceled), "the wrapped error must stay in the chain")
	assert.Contains(t, canceled.Error(), "product_price")

	expired := wrapDB(context.DeadlineExceeded, codeQueryFailed, "cannot happen")
	assert.True(t, errors.HasKind(expired, errors.KindUnavailable))
	assert.Equal(t, "link_canceled", errors.CodeOf(expired))

	other := wrapDB(errors.New("unknown"), codeDefineFailed, "did not work")
	assert.True(t, errors.HasKind(other, errors.KindInternal))
	assert.Equal(t, "link_define_failed", errors.CodeOf(other))
}

// TestReservedNameDerivesFromDefinitionsTable verifies that the reserved name
// is DERIVED from the ledger's name; if the ledger is renamed the ban must
// follow.
func TestReservedNameDerivesFromDefinitionsTable(t *testing.T) {
	require.Len(t, reservedNames, 1)

	table, err := TableName(reservedNames[0])
	require.Error(t, err, "a reserved name must not be convertible into a table name")
	assert.Empty(t, table)

	// Does the ban really prevent a collision with the ledger?
	assert.Equal(t, definitionsTable, tablePrefix+reservedNames[0])
}

// TestJoinNames verifies that error messages stay readable with an empty
// registry too.
func TestJoinNames(t *testing.T) {
	assert.Equal(t, "(none declared)", joinNames(nil))
	assert.Equal(t, "a, b", joinNames([]string{"a", "b"}))
}

// TestErrorDetailKeysAreStable pins the KEYS of the error details.
//
// # The gap this closes
//
// An error's message is human prose and may be reworded — this package's
// messages were rewritten wholesale when it was translated out of Turkish
// (ADR 0012). Its detail KEYS are not prose: an operator filters on them and a
// log pipeline indexes them. Renaming one is silent for the compiler and, as
// measured, was silent for every test in this package too: renaming
// "cardinality" to "card" and "stored" to "prev" left both `go test -race
// ./core/link/` and the integration suite green. The dashboard that
// stopped matching would have been the first report.
//
// The keys asserted here are therefore a CONTRACT, unlike the sentences around
// them.
func TestErrorDetailKeysAreStable(t *testing.T) {
	svc := newService(nil, nil)
	svc.defs.put(mustLinkTable(t, testDef("product_price", OneToMany)))

	t.Run("undeclared link", func(t *testing.T) {
		_, err := svc.linkFor("absent")

		assert.Equal(t, map[string]any{"link": "absent"}, detailsOf(t, err))
	})

	t.Run("definition conflict", func(t *testing.T) {
		err := svc.Define(context.Background(), testDef("product_price", ManyToMany))

		require.Error(t, err)

		// The whole map, VALUES included, like the sibling subtests. Asserting
		// only that the key is present leaves the value free: swapping
		// existing.String() for incoming.String() in conflictWithExisting
		// compiles and survives both suites, and an operator debugging a
		// redeclare would read the stored and the incoming definition as the
		// same thing — which is the one fact the error exists to separate.
		assert.Equal(t, map[string]any{
			"link":   "product_price",
			"stored": testDef("product_price", OneToMany).String(),
		}, detailsOf(t, err))
	})

	t.Run("cardinality violation", func(t *testing.T) {
		lt := mustLinkTable(t, testDef("product_price", OneToOne))
		err := lt.writeError(&pgconn.PgError{Code: "23505", ConstraintName: lt.fromIndex}, "var_1", "ps_1")

		assert.Equal(t, map[string]any{
			"link":        "product_price",
			"cardinality": "one_to_one",
			"from_id":     "var_1",
			"to_id":       "ps_1",
		}, detailsOf(t, err))
	})
}

// detailsOf pulls the detail map out of a typed error.
func detailsOf(t *testing.T, err error) map[string]any {
	t.Helper()

	var typed *errors.Error
	require.True(t, errors.As(err, &typed), "the error must be a typed *errors.Error: %v", err)
	return typed.Details
}

// TestParseCardinalityIsTheInverseOfString verifies the only conversion that
// reaches disk in both directions.
//
// The pair is what makes a widening decidable (ADR 0116): the ledger holds
// TEXT, and comparing a stored spelling against a declared one for ORDER means
// reading it back into a value. A spelling nothing writes must not parse.
func TestParseCardinalityIsTheInverseOfString(t *testing.T) {
	for _, c := range []Cardinality{OneToOne, OneToMany, ManyToMany} {
		got, ok := parseCardinality(c.String())
		require.True(t, ok, "the spelling %q of %s must read back", c.String(), c)
		assert.Equal(t, c, got)
	}

	for _, spelling := range []string{"", "one-to-one", "OneToOne", "unknown(9)", "one_to_all"} {
		_, ok := parseCardinality(spelling)
		assert.False(t, ok, "%q is not a cardinality this binary knows", spelling)
	}
}

// TestWiderThanOrdersTheCardinalities verifies the relation the widening rests
// on: a wider cardinality admits every pair the narrower one did.
func TestWiderThanOrdersTheCardinalities(t *testing.T) {
	assert.True(t, OneToMany.widerThan(OneToOne))
	assert.True(t, ManyToMany.widerThan(OneToOne))
	assert.True(t, ManyToMany.widerThan(OneToMany))

	assert.False(t, OneToOne.widerThan(OneToMany))
	assert.False(t, OneToOne.widerThan(ManyToMany))
	assert.False(t, OneToMany.widerThan(ManyToMany))

	for _, c := range []Cardinality{OneToOne, OneToMany, ManyToMany} {
		assert.False(t, c.widerThan(c), "%s is not wider than itself", c)
	}

	// An undefined value has no place in the order: it is neither wider nor
	// narrower than anything, so it can never be read as a widening.
	undefined := Cardinality(9)
	assert.False(t, undefined.widerThan(OneToOne))
	assert.False(t, ManyToMany.widerThan(undefined))
}

// TestStoredDefinitionChange verifies what a declaration asks of the row
// already in the ledger: nothing, a widening, or a conflict.
//
// The distinction is the whole of ADR 0116. Reading a NARROWING as a widening
// would apply a constraint without seeing the rows that violate it; reading a
// widening as a conflict is what kept a declared cardinality from ever moving.
func TestStoredDefinitionChange(t *testing.T) {
	stored := func(c string) storedDefinition {
		return storedDefinition{
			fromModule:  "product",
			fromField:   "variant_id",
			toModule:    "pricing",
			toField:     "price_set_id",
			cardinality: c,
		}
	}

	cases := map[string]struct {
		stored   storedDefinition
		incoming Cardinality
		want     definitionChange
	}{
		"the normal startup":        {stored("one_to_one"), OneToOne, definitionUnchanged},
		"one step wider":            {stored("one_to_one"), OneToMany, definitionWidened},
		"two steps wider":           {stored("one_to_one"), ManyToMany, definitionWidened},
		"the last step":             {stored("one_to_many"), ManyToMany, definitionWidened},
		"a narrowing":               {stored("one_to_many"), OneToOne, definitionConflicts},
		"a narrowing by two":        {stored("many_to_many"), OneToOne, definitionConflicts},
		"a spelling nothing writes": {stored("unknown(9)"), ManyToMany, definitionConflicts},
		"an empty ledger cardinal":  {stored(""), OneToMany, definitionConflicts},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.stored.change(testDef("product_price", tc.incoming)))
		})
	}

	// A side that moved is a DIFFERENT relation wearing a taken name, and no
	// cardinality makes that a widening.
	sideMoved := map[string]func(s *storedDefinition){
		"from module": func(s *storedDefinition) { s.fromModule = "cart" },
		"from field":  func(s *storedDefinition) { s.fromField = "cart_id" },
		"to module":   func(s *storedDefinition) { s.toModule = "inventory" },
		"to field":    func(s *storedDefinition) { s.toField = "item_id" },
	}
	for name, mutate := range sideMoved {
		t.Run(name+" under a widening", func(t *testing.T) {
			s := stored("one_to_one")
			mutate(&s)
			assert.Equal(t, definitionConflicts, s.change(testDef("product_price", OneToMany)),
				"a moved side is a conflict even when the cardinality would widen")
		})
	}
}

// TestObsoleteIndexesAreTheComplementOfRequired verifies that the two index
// lists partition the names a link table can carry.
//
// They are derived from one another on purpose: an index that is in NEITHER
// list is one nothing creates and nothing drops, and it would survive a
// widening still enforcing the constraint the definition dropped.
func TestObsoleteIndexesAreTheComplementOfRequired(t *testing.T) {
	for _, c := range []Cardinality{OneToOne, OneToMany, ManyToMany} {
		t.Run(c.String(), func(t *testing.T) {
			lt := mustLinkTable(t, testDef("product_price", c))

			required := lt.requiredIndexes()
			obsolete := lt.obsoleteIndexes()

			assert.ElementsMatch(t, lt.allIndexes(), append(slices.Clone(required), obsolete...),
				"every index name must be required or obsolete, and never both")
			for _, index := range obsolete {
				assert.NotContains(t, required, index)
			}
		})
	}
}

// TestDDLDropsWhatTheCardinalityNoLongerNeeds verifies the statements a
// widening depends on.
//
// A unique index is not removed by declaring a looser cardinality, so without
// these drops the OLD constraint would keep being enforced while the ledger
// promised the new one.
func TestDDLDropsWhatTheCardinalityNoLongerNeeds(t *testing.T) {
	table, err := TableName("product_price")
	require.NoError(t, err)

	cases := map[Cardinality][]string{
		OneToOne:   {table + toLookupSuffix},
		OneToMany:  {table + fromIndexSuffix, table + toLookupSuffix},
		ManyToMany: {table + fromIndexSuffix, table + toIndexSuffix},
	}
	for c, dropped := range cases {
		t.Run(c.String(), func(t *testing.T) {
			stmts := mustLinkTable(t, testDef("product_price", c)).ddl()
			joined := strings.Join(stmts, "\n")

			for _, index := range dropped {
				assert.Contains(t, joined, "DROP INDEX IF EXISTS "+index)
			}
			assert.Equal(t, len(dropped), strings.Count(joined, "DROP INDEX"),
				"nothing but the obsolete indexes may be dropped")

			// The creations come first. Under ManyToMany the lookup index
			// replaces the unique one on the same column, and the reverse
			// query must never be left without an index in between.
			firstDrop := slices.IndexFunc(stmts, func(s string) bool {
				return strings.HasPrefix(s, "DROP INDEX")
			})
			require.NotEqual(t, -1, firstDrop, "every cardinality drops something")
			for i, stmt := range stmts {
				if strings.Contains(stmt, "CREATE") {
					assert.Less(t, i, firstDrop, "a creation may not follow a drop")
				}
			}
		})
	}
}
