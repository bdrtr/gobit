package condition_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/internal/core/condition"
)

// unknown is a word outside the vocabulary, as a row restored by hand could
// carry it.
const unknown = condition.Operator("like")

// TestTheVocabularyIsNineWords pins the spelling of every word and which of
// them are numeric, take several values or read a list.
//
// The spelling is load-bearing outside Go: promotion's table CHECK names
// `any_in`, and a word respelled here would make every stored rule of that word
// unreadable at once.
func TestTheVocabularyIsNineWords(t *testing.T) {
	t.Parallel()

	words := make([]string, 0, len(condition.Operators))
	var numeric, multi, list []condition.Operator
	for _, op := range condition.Operators {
		words = append(words, string(op))
		if op.Numeric() {
			numeric = append(numeric, op)
		}
		if op.MultiValue() {
			multi = append(multi, op)
		}
		if op.ReadsAList() {
			list = append(list, op)
		}
	}

	assert.Equal(t, []string{"eq", "ne", "in", "nin", "gt", "gte", "lt", "lte", "any_in"}, words)
	assert.Equal(t, []condition.Operator{condition.Gt, condition.Gte, condition.Lt, condition.Lte}, numeric)
	assert.Equal(t, []condition.Operator{condition.In, condition.Nin, condition.AnyIn}, multi)
	assert.Equal(t, []condition.Operator{condition.AnyIn}, list)
	assert.False(t, unknown.Numeric() || unknown.MultiValue() || unknown.ReadsAList(),
		"a word outside the vocabulary is none of the three")
}

// matching is, for each word that reads a single value, a condition that holds
// when the attribute is present.
var matching = []struct {
	op     condition.Operator
	values []string
	value  string
}{
	{condition.Eq, []string{"vip"}, "vip"},
	{condition.Ne, []string{"blocked"}, "vip"},
	{condition.In, []string{"vip", "gold"}, "vip"},
	{condition.Nin, []string{"blocked"}, "vip"},
	{condition.Gt, []string{"10"}, "11"},
	{condition.Gte, []string{"10"}, "10"},
	{condition.Lt, []string{"10"}, "9"},
	{condition.Lte, []string{"10"}, "10"},
}

// TestAnAbsentFieldMatchesNothing proves that a condition over an attribute
// the context does not carry does not match, even under ne and nin.
//
// Otherwise a request with an empty context would satisfy every negative rule
// and open a segment price, a restricted option or a segment discount to
// everybody. Each case first matches with the attribute present, so the
// absence is what closes it. `any_in` is not here: it reads only the list
// (TestOnlyAnyInReadsTheList).
func TestAnAbsentFieldMatchesNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range matching {
		require.True(t, condition.Match(tc.op, tc.values, tc.value, true, nil),
			"%s %v holds for %q when the attribute is present", tc.op, tc.values, tc.value)
		assert.False(t, condition.Match(tc.op, tc.values, tc.value, false, nil),
			"%s %v must not hold for an absent attribute", tc.op, tc.values)
	}
}

// TestAValuelessConditionMatchesNothing proves that a rule with no values, a
// row no writer produces but a hand-run script can leave, matches nothing and
// does not panic.
func TestAValuelessConditionMatchesNothing(t *testing.T) {
	t.Parallel()

	for _, op := range append(condition.Operators, unknown) {
		for _, values := range [][]string{nil, {}} {
			assert.NotPanics(t, func() {
				assert.False(t, condition.Match(op, values, "10", true, []string{"10"}),
					"%s with no values must not match", op)
			})
		}
	}
}

// TestAnUnknownOperatorMatchesNothing proves that a word outside the
// vocabulary closes the condition rather than opening it, in Match and in
// Compare alike; Compare answers false for the set operators too.
func TestAnUnknownOperatorMatchesNothing(t *testing.T) {
	t.Parallel()

	assert.False(t, condition.Match(unknown, []string{"vip"}, "vip", true, []string{"vip"}))
	assert.False(t, condition.Compare(unknown, 10, 10))
	for _, op := range []condition.Operator{condition.In, condition.Nin, condition.AnyIn} {
		assert.False(t, condition.Compare(op, 10, 10), "Compare does not read %s", op)
	}
}

// TestNumbersCompareAsNumbers proves that a numeric operator compares integers,
// not strings: compared lexically "9" comes out larger than "50000" and a
// free-shipping threshold is inverted.
func TestNumbersCompareAsNumbers(t *testing.T) {
	t.Parallel()

	assert.False(t, condition.Match(condition.Gte, []string{"50000"}, "9", true, nil))
	assert.True(t, condition.Match(condition.Lt, []string{"50000"}, "9", true, nil))
	assert.True(t, condition.Match(condition.Gte, []string{"50000"}, "100000", true, nil))
	assert.True(t, condition.Match(condition.Gt, []string{"-5"}, "-4", true, nil))
}

// TestEachComparisonAtItsBound proves where each comparison turns, in Match
// and in Compare: at equality only the inclusive words hold.
func TestEachComparisonAtItsBound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		op         condition.Operator
		have, want int64
		holds      bool
	}{
		{condition.Gt, 10, 10, false},
		{condition.Gt, 11, 10, true},
		{condition.Gte, 10, 10, true},
		{condition.Gte, 9, 10, false},
		{condition.Lt, 10, 10, false},
		{condition.Lt, 9, 10, true},
		{condition.Lte, 10, 10, true},
		{condition.Lte, 11, 10, false},
	}
	for _, tc := range cases {
		have, want := strconv.FormatInt(tc.have, 10), strconv.FormatInt(tc.want, 10)
		assert.Equal(t, tc.holds, condition.Match(tc.op, []string{want}, have, true, nil),
			"Match: %s %s against %s", have, tc.op, want)
		assert.Equal(t, tc.holds, condition.Compare(tc.op, tc.have, tc.want),
			"Compare: %d %s %d", tc.have, tc.op, tc.want)
	}

	assert.True(t, condition.Compare(condition.Eq, 10, 10))
	assert.False(t, condition.Compare(condition.Eq, 10, 11))
	assert.False(t, condition.Compare(condition.Ne, 10, 10))
	assert.True(t, condition.Compare(condition.Ne, 10, 11))
}

// TestASpacedNumberDoesNotMatch proves that a number is read exactly as
// written on both sides. Trimming at match would let a `cart.` value the
// storefront wrote with spaces start matching prices and discounts.
func TestASpacedNumberDoesNotMatch(t *testing.T) {
	t.Parallel()

	t.Run("in the context", func(t *testing.T) {
		t.Parallel()
		assert.False(t, condition.Match(condition.Gte, []string{"10"}, " 10", true, nil))
		assert.False(t, condition.Match(condition.Lte, []string{"10"}, "10 ", true, nil))
	})
	t.Run("in the rule", func(t *testing.T) {
		t.Parallel()
		assert.False(t, condition.Match(condition.Gte, []string{" 10"}, "10", true, nil))
		assert.False(t, condition.Match(condition.Lte, []string{"10 "}, "10", true, nil))
	})
}

// TestAFractionOrWordDoesNotMatch proves that a numeric operator reads
// integers only: a fraction or a word on either side closes the condition.
func TestAFractionOrWordDoesNotMatch(t *testing.T) {
	t.Parallel()

	assert.False(t, condition.Match(condition.Gte, []string{"10"}, "10.5", true, nil))
	assert.False(t, condition.Match(condition.Gte, []string{"10"}, "10.0", true, nil))
	assert.False(t, condition.Match(condition.Lte, []string{"500.5"}, "5", true, nil))
	assert.False(t, condition.Match(condition.Gte, []string{"10"}, "ten", true, nil))
	assert.False(t, condition.Match(condition.Lte, []string{"fifty thousand"}, "5", true, nil))
}

// TestANumberIsReadInBaseTen proves that both sides of a numeric condition are
// read in base 10 as written: a leading zero is not octal and a prefix is not
// a base. Read with Go's prefixes, a `cart.` value "010" would be 8 and fail
// `gte 9`, and "0x10" would be 16 and pass `gte 1`.
func TestANumberIsReadInBaseTen(t *testing.T) {
	t.Parallel()

	t.Run("in the context", func(t *testing.T) {
		t.Parallel()
		assert.True(t, condition.Match(condition.Gte, []string{"9"}, "010", true, nil))
		assert.False(t, condition.Match(condition.Gte, []string{"1"}, "0x10", true, nil))
		assert.False(t, condition.Match(condition.Gte, []string{"1"}, "1_000", true, nil))
	})
	t.Run("in the rule", func(t *testing.T) {
		t.Parallel()
		assert.True(t, condition.Match(condition.Lte, []string{"010"}, "9", true, nil))
	})
}

// TestOnlyAnyInReadsTheList proves that `any_in` reads only the context's list
// and every other word only the single value (ADR 0144). A shipped `in` rule
// that started reading the list would change a live discount with nothing
// announcing it.
func TestOnlyAnyInReadsTheList(t *testing.T) {
	t.Parallel()

	assert.False(t, condition.Match(condition.In, []string{"vip"}, "retail", true, []string{"vip"}),
		"in reads the single value, not the list")
	assert.False(t, condition.Match(condition.AnyIn, []string{"vip"}, "vip", true, nil),
		"any_in reads the list, not the single value")
	assert.True(t, condition.Match(condition.AnyIn, []string{"vip"}, "retail", true, []string{"retail", "vip"}))
	assert.True(t, condition.Match(condition.AnyIn, []string{"vip", "gold"}, "", false, []string{"gold"}),
		"any_in reads every value of the rule, not only the first")
	assert.True(t, condition.Match(condition.In, []string{"vip", "gold"}, "gold", true, nil),
		"in reads every value of the rule, not only the first")
	assert.True(t, condition.Match(condition.AnyIn, []string{"vip"}, "", false, []string{"vip"}),
		"any_in does not ask whether the single value is present")
	assert.False(t, condition.Match(condition.AnyIn, []string{"vip"}, "", false, []string{"retail"}))
}

// TestReadableRefuses pins what a writer refuses: an empty value under every
// word, and under a numeric word anything that is not a base-10 integer as
// written, Go's base prefixes and digit separators included, since Match reads
// none of them.
func TestReadableRefuses(t *testing.T) {
	t.Parallel()

	for _, op := range condition.Operators {
		assert.False(t, condition.Readable(op, ""), "%s refuses an empty value", op)
	}
	for _, value := range []string{"500.5", " 5", "5 ", "five", "1e3", "0x10", "0b1", "0o7", "1_000"} {
		assert.False(t, condition.Readable(condition.Gte, value), "gte refuses %q", value)
	}
	assert.True(t, condition.Readable(condition.Gte, "-5"))
	assert.True(t, condition.Readable(condition.Eq, " 5"), "a string word reads any non-empty value")
	assert.True(t, condition.Readable(condition.AnyIn, "vip"))
}

// TestMatchAllocatesNothing holds the evaluator to the cost of the copies it
// replaced: a price ladder reads every rule of every candidate price.
func TestMatchAllocatesNothing(t *testing.T) {
	values := []string{"retail", "vip", "gold"}
	list := []string{"wholesale", "gold"}
	allocs := testing.AllocsPerRun(100, func() {
		condition.Match(condition.In, values, "vip", true, nil)
		condition.Match(condition.Gte, []string{"50000"}, "75000", true, nil)
		condition.Match(condition.AnyIn, values, "", false, list)
	})
	assert.Zero(t, allocs)
}

// The value classes every generator below draws from, uniformly.
const (
	classInteger = iota
	classFraction
	classWord
	classSpaced
	classPrefixed
	classEmpty
	classCount
)

// drawValue draws one value of a uniformly chosen class.
func drawValue(t *rapid.T, label string) string {
	value, _ := drawClassedValue(t, label)
	return value
}

// drawClassedValue draws one value of a uniformly chosen class and says which.
func drawClassedValue(t *rapid.T, label string) (value string, class int) {
	class = rapid.IntRange(0, classCount-1).Draw(t, label+" class")
	switch class {
	case classInteger:
		return strconv.FormatInt(rapid.Int64().Draw(t, label+" integer"), 10), class
	case classFraction:
		whole := rapid.Int64Range(-1_000_000, 1_000_000).Draw(t, label+" whole")
		return strconv.FormatInt(whole, 10) + "." + strconv.Itoa(rapid.IntRange(0, 99).Draw(t, label+" part")), class
	case classWord:
		return rapid.StringMatching(`[a-z]{1,8}`).Draw(t, label+" word"), class
	case classSpaced:
		n := strconv.FormatInt(rapid.Int64Range(-1_000_000, 1_000_000).Draw(t, label+" spaced"), 10)
		if rapid.Bool().Draw(t, label+" leading") {
			return " " + n, class
		}
		return n + " ", class
	case classPrefixed:
		// A number Go reads under base 0 and base 10 does not.
		n := rapid.Int64Range(1, 1_000_000).Draw(t, label+" prefixed")
		switch rapid.IntRange(0, 3).Draw(t, label+" prefix") {
		case 0:
			return "0x" + strconv.FormatInt(n, 16), class
		case 1:
			return "0b" + strconv.FormatInt(n, 2), class
		case 2:
			return "0o" + strconv.FormatInt(n, 8), class
		default:
			return strconv.FormatInt(n, 10) + "_000", class
		}
	default:
		return "", class
	}
}

// drawOperator draws uniformly over the nine words and one word outside them.
func drawOperator(t *rapid.T) condition.Operator {
	return rapid.SampledFrom(append(append([]condition.Operator{}, condition.Operators...), unknown)).Draw(t, "op")
}

// TestAnUnreadableValueNeverMatches is the property that ties Match to the
// writers: a condition holding a value [condition.Readable] refuses matches
// nothing, whatever the context says. It is why a hand-written `ne ""` no
// longer matches every present value.
func TestAnUnreadableValueNeverMatches(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		op := drawOperator(t)
		values := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string { return drawValue(t, "rule") }), 1, 3).
			Draw(t, "values")
		bad := rapid.IntRange(0, len(values)-1).Draw(t, "unreadable at")
		switch {
		case !condition.Readable(op, values[bad]):
		case op.Numeric():
			for condition.Readable(op, values[bad]) {
				values[bad] = drawValue(t, "unreadable")
			}
		default:
			values[bad] = ""
		}

		value := drawValue(t, "context")
		if rapid.Bool().Draw(t, "context echoes the rule") {
			value = rapid.SampledFrom(values).Draw(t, "echoed")
		}
		list := append([]string{}, values...)
		present := rapid.Bool().Draw(t, "present")

		require.False(t, condition.Match(op, values, value, present, list),
			"%s %q matched %q although %q is unreadable", op, values, value, values[bad])
	})
}

// TestAReadableValueMatchesItself is the converse: whatever a writer accepts,
// Match can read. A readable value set against itself holds under the words
// that include equality and fails under those that exclude it.
//
// What a writer accepts is stated here from the value's class rather than
// asked of Readable, so a Readable grown too strict fails here instead of
// making the property vacuous: every non-empty value reads under a string
// word, and only an integer as written under a numeric one.
func TestAReadableValueMatchesItself(t *testing.T) {
	t.Parallel()

	holds := map[condition.Operator]bool{
		condition.Eq: true, condition.In: true, condition.Gte: true, condition.Lte: true, condition.AnyIn: true,
		condition.Ne: false, condition.Nin: false, condition.Gt: false, condition.Lt: false, unknown: false,
	}
	rapid.Check(t, func(t *rapid.T) {
		op := drawOperator(t)
		v, class := drawClassedValue(t, "value")
		readable := class != classEmpty && (!op.Numeric() || class == classInteger)
		require.Equal(t, readable, condition.Readable(op, v), "is %q readable under %s", v, op)
		if !readable {
			return
		}
		require.Equal(t, holds[op], condition.Match(op, []string{v}, v, true, []string{v}),
			"%s %q against itself", op, v)
	})
}

// TestNegationsAreComplements proves that each negative word is exactly the
// complement of its positive one once the condition can be read: the
// attribute is present, the rule's values are readable and, for a numeric
// pair, the context's value is an integer too.
func TestNegationsAreComplements(t *testing.T) {
	t.Parallel()

	pairs := [][2]condition.Operator{
		{condition.Ne, condition.Eq},
		{condition.Nin, condition.In},
		{condition.Lt, condition.Gte},
		{condition.Gt, condition.Lte},
	}
	rapid.Check(t, func(t *rapid.T) {
		pair := rapid.SampledFrom(pairs).Draw(t, "pair")
		negative, positive := pair[0], pair[1]

		var values []string
		var value string
		if positive.Numeric() {
			values = []string{strconv.FormatInt(rapid.Int64Range(-50, 50).Draw(t, "want"), 10)}
			value = strconv.FormatInt(rapid.Int64Range(-50, 50).Draw(t, "have"), 10)
		} else {
			n := 1
			if positive.MultiValue() {
				n = rapid.IntRange(1, 3).Draw(t, "n")
			}
			for i := range n {
				v := drawValue(t, "rule "+strconv.Itoa(i))
				for v == "" {
					v = drawValue(t, "rule "+strconv.Itoa(i)+" again")
				}
				values = append(values, v)
			}
			value = drawValue(t, "context")
			if rapid.Bool().Draw(t, "context echoes the rule") {
				value = rapid.SampledFrom(values).Draw(t, "echoed")
			}
		}

		require.Equal(t, !condition.Match(positive, values, value, true, nil),
			condition.Match(negative, values, value, true, nil),
			"%s must be the complement of %s for %q against %q", negative, positive, value, values)
	})
}
