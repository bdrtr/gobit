package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// TestNormalizeCurrency proves every branch of the currency validation.
func TestNormalizeCurrency(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"upper case is kept", "TRY", "TRY", true},
		{"lower case is raised", "try", "TRY", true},
		{"mixed case is raised", "TrY", "TRY", true},
		{"leading/trailing space is trimmed", "  eur  ", "EUR", true},
		{"empty is rejected", "", "", false},
		{"two letters are rejected", "TR", "", false},
		{"four letters are rejected", "TRYX", "", false},
		{"a digit is rejected", "TR1", "", false},
		{"a symbol is rejected", "TR$", "", false},
		{"inner space is rejected", "T R", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCurrency(tc.input)
			if !tc.ok {
				require.Error(t, err)
				assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestValidateAmount proves the minor unit bounds of the amount.
//
// The upper bound is not a preference but a necessity: MaxAmount × MaxQuantity
// has to fit into an int64. The test verifies that invariant too.
func TestValidateAmount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount int64
		ok     bool
	}{
		{"zero is accepted", 0, true},
		{"positive is accepted", 1999, true},
		{"the upper bound is accepted", models.MaxAmount, true},
		{"negative is rejected", -1, false},
		{"very negative is rejected", math.MinInt64, false},
		{"above the upper bound is rejected", models.MaxAmount + 1, false},
		{"very large is rejected", math.MaxInt64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAmount(tc.amount)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}

	t.Run("the bounds make overflow impossible", func(t *testing.T) {
		assert.Less(t, models.MaxAmount, math.MaxInt64/int64(models.MaxQuantity),
			"the product amount × quantity has to fit into an int64")
	})
}

// TestNormalizeQuantityRange proves every branch of the quantity range
// validation.
func TestNormalizeQuantityRange(t *testing.T) {
	t.Run("a zero minimum quantity is raised to one", func(t *testing.T) {
		minQty, maxQty, err := normalizeQuantityRange(0, nil)
		require.NoError(t, err)
		assert.Equal(t, int32(1), minQty)
		assert.Nil(t, maxQty)
	})

	t.Run("the upper bound is copied", func(t *testing.T) {
		original := int32(10)
		_, maxQty, err := normalizeQuantityRange(1, &original)
		require.NoError(t, err)
		require.NotNil(t, maxQty)

		original = 99
		assert.Equal(t, int32(10), *maxQty, "the caller's pointer must not be shared")
	})

	for _, tc := range []struct {
		name   string
		minQty int32
		maxQty *int32
	}{
		{"negative minimum", -1, nil},
		{"minimum above the bound", models.MaxQuantity + 1, nil},
		{"zero maximum", 1, ptr(int32(0))},
		{"negative maximum", 1, ptr(int32(-5))},
		{"maximum above the bound", 1, ptr(models.MaxQuantity + 1)},
		{"maximum below the minimum", 10, ptr(int32(5))},
	} {
		t.Run(tc.name+" is rejected", func(t *testing.T) {
			_, _, err := normalizeQuantityRange(tc.minQty, tc.maxQty)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}

	t.Run("the maximum may equal the minimum", func(t *testing.T) {
		_, _, err := normalizeQuantityRange(5, ptr(int32(5)))
		assert.NoError(t, err)
	})
}

// TestValidateRule proves every branch of the rule validation.
func TestValidateRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   RuleInput
		ok   bool
	}{
		{"eq is valid with a single value",
			RuleInput{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}}, true},
		{"in is valid with several values",
			RuleInput{Attribute: "grp", Operator: models.OpIn, Values: []string{"a", "b"}}, true},
		{"gt is valid with a numeric value",
			RuleInput{Attribute: "age", Operator: models.OpGt, Values: []string{"18"}}, true},
		{"a blank field name is rejected",
			RuleInput{Attribute: "  ", Operator: models.OpEq, Values: []string{"a"}}, false},
		{"an undefined operator is rejected",
			RuleInput{Attribute: "k", Operator: models.RuleOperator("regex"), Values: []string{"a"}}, false},
		{"no value is rejected",
			RuleInput{Attribute: "k", Operator: models.OpEq}, false},
		{"two values for a single-value operator are rejected",
			RuleInput{Attribute: "k", Operator: models.OpEq, Values: []string{"a", "b"}}, false},
		{"an empty value is rejected",
			RuleInput{Attribute: "k", Operator: models.OpIn, Values: []string{"a", ""}}, false},
		{"text for a numeric operator is rejected",
			RuleInput{Attribute: "k", Operator: models.OpLte, Values: []string{"eighteen"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRule(tc.in)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestRequireID proves every branch of the id validation.
func TestRequireID(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		ok   bool
	}{
		{"the right prefix is valid", "pset_ABC", true},
		{"empty is rejected", "", false},
		{"a wrong prefix is rejected", "variant_ABC", false},
		{"no prefix is rejected", "ABC", false},
		{"a leading space is rejected", " pset_ABC", false},
		{"a trailing space is rejected", "pset_ABC ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireID(tc.id, models.PriceSetIDPrefix, "price set id")
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}

	t.Run("an excessively long id is rejected", func(t *testing.T) {
		long := models.PriceSetIDPrefix
		for len(long) <= maxIDLen {
			long += "A"
		}
		err := requireID(long, models.PriceSetIDPrefix, "price set id")
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})
}

// TestNormalizePaging proves every branch of the paging clipping.
func TestNormalizePaging(t *testing.T) {
	t.Run("with no limit the default is applied", func(t *testing.T) {
		limit, offset, err := normalizePaging(0, 0)
		require.NoError(t, err)
		assert.Equal(t, DefaultLimit, limit)
		assert.Equal(t, int32(0), offset)
	})

	t.Run("a negative limit falls back to the default", func(t *testing.T) {
		limit, _, err := normalizePaging(-5, 0)
		require.NoError(t, err)
		assert.Equal(t, DefaultLimit, limit)
	})

	t.Run("an excessive limit is clipped to the maximum", func(t *testing.T) {
		limit, _, err := normalizePaging(MaxLimit+1, 0)
		require.NoError(t, err)
		assert.Equal(t, MaxLimit, limit)
	})

	t.Run("a valid limit is kept", func(t *testing.T) {
		limit, offset, err := normalizePaging(7, 21)
		require.NoError(t, err)
		assert.Equal(t, int32(7), limit)
		assert.Equal(t, int32(21), offset)
	})

	t.Run("a negative offset is rejected", func(t *testing.T) {
		_, _, err := normalizePaging(10, -1)
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})
}

// TestClampToInt32 proves that the int -> int32 conversion does not wrap around.
func TestClampToInt32(t *testing.T) {
	assert.Equal(t, int32(42), clampToInt32(42))
	assert.Equal(t, int32(math.MaxInt32), clampToInt32(math.MaxInt32))

	if math.MaxInt > math.MaxInt32 {
		assert.Equal(t, int32(math.MaxInt32), clampToInt32(math.MaxInt32+1))
		assert.Equal(t, int32(math.MinInt32), clampToInt32(math.MinInt32-1))
	}
}

// TestWithIndexAddsDetail proves that, in a bulk write, which input was
// rejected is added to the error.
func TestWithIndexAddsDetail(t *testing.T) {
	err := withIndex(errors.Invalid("x", "broken"), detailIndex, 3)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, 3, typed.Details["index"])
}

// TestWithIndexKeepsNestedLevels proves that two nested indexes do NOT
// OVERWRITE each other.
//
// Had the same key been used twice, errors.WithDetails would overwrite the
// first with the second, and the outer price index would wipe out the inner
// rule index.
func TestWithIndexKeepsNestedLevels(t *testing.T) {
	inner := withIndex(errors.Invalid("x", "broken"), detailRuleIndex, 3)
	err := withIndex(inner, detailIndex, 7)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, 7, typed.Details[detailIndex])
	assert.Equal(t, 3, typed.Details[detailRuleIndex], "the rule index has to be kept")
}
