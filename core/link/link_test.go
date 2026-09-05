package link_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
)

// validDefinition is the valid definition the tests use as their starting
// point.
func validDefinition() link.LinkDefinition {
	return link.LinkDefinition{
		Name:        "product_price",
		From:        link.LinkSide{Module: "product", Field: "variant_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToMany,
	}
}

func TestCardinalityString(t *testing.T) {
	assert.Equal(t, "one_to_one", link.OneToOne.String())
	assert.Equal(t, "one_to_many", link.OneToMany.String())
	assert.Equal(t, "many_to_many", link.ManyToMany.String())
	assert.Equal(t, "unknown(9)", link.Cardinality(9).String(),
		"an undefined value must not silently turn into a valid name")

	// The zero value must be the STRICTEST constraint; otherwise an undeclared
	// cardinality would silently allow a free relation.
	assert.Equal(t, link.OneToOne, link.Cardinality(0))
}

func TestCardinalityValid(t *testing.T) {
	assert.True(t, link.OneToOne.Valid())
	assert.True(t, link.OneToMany.Valid())
	assert.True(t, link.ManyToMany.Valid())
	assert.False(t, link.Cardinality(3).Valid())
	assert.False(t, link.Cardinality(255).Valid())
}

func TestLinkSideString(t *testing.T) {
	side := link.LinkSide{Module: "product", Field: "variant_id"}
	assert.Equal(t, "product.variant_id", side.String())
}

func TestLinkDefinitionString(t *testing.T) {
	assert.Equal(t,
		"product_price(product.variant_id -> pricing.price_set_id, one_to_many)",
		validDefinition().String())
}

func TestLinkDefinitionValidateAccepts(t *testing.T) {
	valid := []link.LinkDefinition{
		validDefinition(),
		{
			Name:        "a",
			From:        link.LinkSide{Module: "a", Field: "b"},
			To:          link.LinkSide{Module: "c", Field: "d"},
			Cardinality: link.OneToOne,
		},
		{
			// A relation of a module with itself (related products, say) is
			// valid: because the column names in the link table are fixed, the
			// two ends carrying the same field name is not a problem.
			Name:        "product_related",
			From:        link.LinkSide{Module: "product", Field: "product_id"},
			To:          link.LinkSide{Module: "product", Field: "product_id"},
			Cardinality: link.ManyToMany,
		},
		{
			Name:        strings.Repeat("a", 40),
			From:        link.LinkSide{Module: "product", Field: "variant_id"},
			To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
			Cardinality: link.ManyToMany,
		},
	}

	for _, def := range valid {
		t.Run(def.Name, func(t *testing.T) {
			require.NoError(t, def.Validate())

			table, err := link.TableName(def.Name)
			require.NoError(t, err)
			assert.Equal(t, "link_"+def.Name, table)
			assert.LessOrEqual(t, len(table+"_from_uniq"), 63,
				"the longest derived name must not exceed the PostgreSQL identifier limit")
		})
	}
}

// TestLinkDefinitionValidateRejectsNames proves that the only place a name
// becomes a table name goes through validation. Injection attempts are in the
// list on purpose: had these names passed, they would have gone straight into
// DDL text.
func TestLinkDefinitionValidateRejectsNames(t *testing.T) {
	badNames := map[string]string{
		"empty":                     "",
		"semicolon":                 "product; DROP TABLE customers",
		"comment":                   "product--",
		"double quote":              `product" (x int); --`,
		"single quote":              "product' OR '1'='1",
		"parenthesis":               "product)",
		"space":                     "product price",
		"tab":                       "product\tprice",
		"newline":                   "product\nprice",
		"dot":                       "pg_catalog.pg_tables",
		"uppercase":                 "Product",
		"starts with a digit":       "1product",
		"starts with an underscore": "_product",
		"dash":                      "product-price",
		"non-ASCII letter":          "café_price",
		"asterisk":                  "*",
		"too long":                  strings.Repeat("a", 41),
		"reserved (ledger table)":   "definitions",
	}

	for label, name := range badNames {
		t.Run(label, func(t *testing.T) {
			def := validDefinition()
			def.Name = name

			err := def.Validate()

			require.Error(t, err, "an invalid name must not be accepted: %q", name)
			assert.True(t, errors.IsInvalid(err),
				"the error class must be KindInvalid, got %v", errors.KindOf(err))
			assert.Equal(t, "link_name_invalid", errors.CodeOf(err))

			table, tableErr := link.TableName(name)
			require.Error(t, tableErr, "TableName must reject the same name")
			assert.Empty(t, table, "no table name may be built from an invalid name")
		})
	}
}

func TestLinkDefinitionValidateRejectsSides(t *testing.T) {
	brokenSides := map[string]link.LinkDefinition{
		"From module empty": {
			Name: "x", From: link.LinkSide{Field: "a"},
			To: link.LinkSide{Module: "b", Field: "c"},
		},
		"From field empty": {
			Name: "x", From: link.LinkSide{Module: "a"},
			To: link.LinkSide{Module: "b", Field: "c"},
		},
		"To module empty": {
			Name: "x", From: link.LinkSide{Module: "a", Field: "b"},
			To: link.LinkSide{Field: "c"},
		},
		"To field empty": {
			Name: "x", From: link.LinkSide{Module: "a", Field: "b"},
			To: link.LinkSide{Module: "c"},
		},
		"injection in the From module": {
			Name: "x", From: link.LinkSide{Module: "a; DROP TABLE t", Field: "b"},
			To: link.LinkSide{Module: "c", Field: "d"},
		},
		"injection in the To field": {
			Name: "x", From: link.LinkSide{Module: "a", Field: "b"},
			To: link.LinkSide{Module: "c", Field: `d" --`},
		},
	}

	for label, def := range brokenSides {
		t.Run(label, func(t *testing.T) {
			def.Cardinality = link.OneToOne

			err := def.Validate()

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err),
				"the error class must be KindInvalid, got %v", errors.KindOf(err))
			assert.Equal(t, "link_side_invalid", errors.CodeOf(err))
		})
	}
}

func TestLinkDefinitionValidateRejectsUnknownCardinality(t *testing.T) {
	def := validDefinition()
	def.Cardinality = link.Cardinality(7)

	err := def.Validate()

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, "link_cardinality_invalid", errors.CodeOf(err))
}

// TestUndefinedLinkIsNotFound verifies that every call made on an undeclared
// link produces a diagnosable NotFound without ever going to the database.
// This gate is also a security boundary: an undeclared name NEVER reaches SQL.
func TestUndefinedLinkIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc := link.New(nil, nil)

	t.Run("Create", func(t *testing.T) {
		requireNotDeclared(t, svc.Create(ctx, "absent", "a", "b"))
	})
	t.Run("Delete", func(t *testing.T) {
		requireNotDeclared(t, svc.Delete(ctx, "absent", "a", "b"))
	})
	t.Run("List", func(t *testing.T) {
		ids, err := svc.List(ctx, "absent", "a")
		assert.Nil(t, ids)
		requireNotDeclared(t, err)
	})
	t.Run("ListMany", func(t *testing.T) {
		ids, err := svc.ListMany(ctx, "absent", []string{"a"})
		assert.Nil(t, ids)
		requireNotDeclared(t, err)
	})
	t.Run("Definition", func(t *testing.T) {
		def, err := svc.Definition(ctx, "absent")
		assert.Equal(t, link.LinkDefinition{}, def)
		requireNotDeclared(t, err)
	})
}

// requireNotDeclared verifies the class and the code of the undeclared-link
// error.
func requireNotDeclared(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err),
		"the error class must be KindNotFound, got %v", errors.KindOf(err))
	assert.Equal(t, "link_not_defined", errors.CodeOf(err))
	assert.Contains(t, err.Error(), "declared links", "the message must list the declared names")
}

// TestDefineWithoutPoolIsUnavailable verifies that a service without a pool
// returns a typed error instead of panicking; that is how startup-order
// mistakes become visible.
func TestDefineWithoutPoolIsUnavailable(t *testing.T) {
	err := link.New(nil, nil).Define(context.Background(), validDefinition())

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the error class must be KindUnavailable, got %v", errors.KindOf(err))
	assert.Equal(t, "link_db_unavailable", errors.CodeOf(err))
}

// TestDefineValidatesBeforeTouchingDatabase shows that an invalid definition
// yields a validation error even without a pool, that is, that validation runs
// BEFORE the database.
func TestDefineValidatesBeforeTouchingDatabase(t *testing.T) {
	def := validDefinition()
	def.Name = "product; DROP TABLE customers"

	err := link.New(nil, nil).Define(context.Background(), def)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err),
		"the validation error must come back BEFORE the pool error; got %v", errors.KindOf(err))
	assert.Equal(t, "link_name_invalid", errors.CodeOf(err))
}

// TestListManyWithNoIDsSkipsQuery shows that an empty set never goes to the
// database: the call succeeds even though there is no pool.
func TestListManyWithNoIDsSkipsQuery(t *testing.T) {
	svc := declaredService(t)

	result, err := svc.ListMany(context.Background(), validDefinition().Name, nil)

	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestDefinitionReturnsDeclaredDefinition verifies that the Query layer can
// learn which module a link resolves to (ADR 0004).
func TestDefinitionReturnsDeclaredDefinition(t *testing.T) {
	def, err := declaredService(t).Definition(context.Background(), validDefinition().Name)

	require.NoError(t, err)
	assert.Equal(t, validDefinition(), def)
}

// TestDefinitionHonorsCanceledContext verifies that even the path served from
// memory honors the context budget (plan Section 8).
func TestDefinitionHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := declaredService(t).Definition(ctx, validDefinition().Name)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the error class must be KindUnavailable, got %v", errors.KindOf(err))
	assert.Equal(t, "link_canceled", errors.CodeOf(err))
	assert.True(t, errors.Is(err, context.Canceled))
}

// TestIDValidationRunsBeforeDatabase shows that id validation runs before
// reaching the pool: even on a pool-less service the error is KindInvalid.
func TestIDValidationRunsBeforeDatabase(t *testing.T) {
	ctx := context.Background()
	name := validDefinition().Name
	svc := declaredService(t)
	longID := strings.Repeat("x", 256)

	calls := map[string]func() error{
		"Create fromID empty":    func() error { return svc.Create(ctx, name, "", "b") },
		"Create fromID blank":    func() error { return svc.Create(ctx, name, "   ", "b") },
		"Create toID empty":      func() error { return svc.Create(ctx, name, "a", "") },
		"Create toID too long":   func() error { return svc.Create(ctx, name, "a", longID) },
		"Delete fromID empty":    func() error { return svc.Delete(ctx, name, "", "b") },
		"Delete toID empty":      func() error { return svc.Delete(ctx, name, "a", "") },
		"List fromID empty":      func() error { _, err := svc.List(ctx, name, ""); return err },
		"ListMany fromID empty":  func() error { _, err := svc.ListMany(ctx, name, []string{"a", ""}); return err },
		"ListMany id too long":   func() error { _, err := svc.ListMany(ctx, name, []string{longID}); return err },
		"List fromID too long":   func() error { _, err := svc.List(ctx, name, longID); return err },
		"Create fromID too long": func() error { return svc.Create(ctx, name, longID, "b") },

		// Ids carrying leading/trailing whitespace: because their trimmed form
		// is not empty, they used to pass validation and be written to the
		// database.
		"Create fromID trailing space": func() error { return svc.Create(ctx, name, "var_1 ", "ps_1") },
		"Create fromID leading space":  func() error { return svc.Create(ctx, name, " var_1", "ps_1") },
		"Create toID newline":          func() error { return svc.Create(ctx, name, "var_1", "ps_1\n") },
		"Delete fromID tab":            func() error { return svc.Delete(ctx, name, "var_1\t", "ps_1") },
		"List fromID trailing space":   func() error { _, err := svc.List(ctx, name, "var_1 "); return err },
		"ListMany id newline": func() error {
			_, err := svc.ListMany(ctx, name, []string{"var_1", "var_2\n"})
			return err
		},
		"ListManyByTo id leading space": func() error {
			_, err := svc.ListManyByTo(ctx, name, []string{" ps_1"})
			return err
		},
	}

	for label, call := range calls {
		t.Run(label, func(t *testing.T) {
			err := call()

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err),
				"id validation must run BEFORE the pool check; got %v", errors.KindOf(err))
			assert.Equal(t, "link_id_invalid", errors.CodeOf(err))
		})
	}
}

// TestPaddedIDIsRejectedNotTrimmed pins that an id is NOT TRIMMED silently but
// rejected outright.
//
// Trimming was an option too; rejection was chosen because trimming separates
// the id the caller sent from the id we store, and the difference only becomes
// visible after the data is corrupted. Rejection reports the drift on the
// first call and with a typed error.
//
// Inner whitespace is NOT RESTRICTED: an id is a free-form string, and the
// rule targets only the leading/trailing whitespace bleeding in from an
// external source (CSV, HTTP header, JSON).
func TestPaddedIDIsRejectedNotTrimmed(t *testing.T) {
	ctx := context.Background()
	name := validDefinition().Name
	svc := declaredService(t)

	err := svc.Create(ctx, name, "var_1 ", "ps_1")

	require.Error(t, err, "a padded id must not be accepted even though its trimmed form is non-empty")
	assert.True(t, errors.IsInvalid(err),
		"a whitespace drift is a data error, not an infrastructure error to be retried; got %v",
		errors.KindOf(err))
	assert.Equal(t, "link_id_invalid", errors.CodeOf(err))
	assert.Contains(t, err.Error(), "fromID", "the error must say which end is at fault")

	// Inner whitespace is still valid: because the call passes validation and
	// falls through to the pool check, the error must be KindUnavailable and
	// not KindInvalid.
	innerSpace := svc.Create(ctx, name, "var 1", "ps_1")

	require.Error(t, innerSpace)
	assert.True(t, errors.HasKind(innerSpace, errors.KindUnavailable),
		"inner whitespace must not be forbidden; got %v", errors.KindOf(innerSpace))
}

// TestWritePathsWithoutPoolAreUnavailable verifies that calls which pass
// validation but find no pool return a typed KindUnavailable.
func TestWritePathsWithoutPoolAreUnavailable(t *testing.T) {
	ctx := context.Background()
	name := validDefinition().Name
	svc := declaredService(t)

	calls := map[string]func() error{
		"Create":   func() error { return svc.Create(ctx, name, "a", "b") },
		"Delete":   func() error { return svc.Delete(ctx, name, "a", "b") },
		"List":     func() error { _, err := svc.List(ctx, name, "a"); return err },
		"ListMany": func() error { _, err := svc.ListMany(ctx, name, []string{"a"}); return err },
	}

	for label, call := range calls {
		t.Run(label, func(t *testing.T) {
			err := call()

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindUnavailable),
				"the error class must be KindUnavailable, got %v", errors.KindOf(err))
			assert.Equal(t, "link_db_unavailable", errors.CodeOf(err))
		})
	}
}

// declaredService builds a service with no pool but with the definition
// registered; that is enough to exercise the paths needing no database.
func declaredService(t *testing.T) link.LinkService {
	t.Helper()

	svc := link.New(nil, nil)
	require.NoError(t, link.DefineForTest(svc, validDefinition()))
	return svc
}
