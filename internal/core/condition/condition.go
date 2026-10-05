// Package condition reads one rule condition: an attribute's value, an
// operator and the rule's values (ADR 0396).
//
// # Why one package
//
// A price, a shipping option and a promotion carry rules of the same shape and
// read them with the same nine words. Each module used to read them with its
// own copy, and the copies drifted: one trimmed a number before comparing it,
// one wrote a numeric rule it could never match (D252). A rule copied per
// module keeps answering after one copy drifts, which is why the storefront's
// customer claim is one comparison in the core too (ADR 0057, ADR 0370).
//
// # What stays in the modules
//
// This package is a pure function over strings. It holds no rule struct and
// knows no module (ADR 0001 forbids a shared model): each module keeps its own
// RuleOperator type, converts its constants from the words here, decides which
// of the words it admits ([Operator] values a module's Valid accepts), and
// composes its rules. Every consumer ANDs its rules; this package reads one.
package condition

import (
	"slices"
	"strconv"
)

// Operator is one word of the rule vocabulary.
type Operator string

// The nine words. A module admits a subset of them; `any_in` is promotion's
// alone (ADR 0144), and pricing refuses it by design (ADR 0327).
//
// eq/ne/in/nin are STRING comparisons; gt/gte/lt/lte read both sides as base-10
// INTEGERS and compare them NUMERICALLY. Money is a minor-unit integer, so a
// threshold never passes through floating point (plan Section 8).
const (
	// Eq wants the value to equal the rule's single value.
	Eq Operator = "eq"
	// Ne wants the value to differ from the rule's single value.
	Ne Operator = "ne"
	// In wants the value to be one of the rule's values.
	In Operator = "in"
	// Nin wants the value to be none of the rule's values.
	Nin Operator = "nin"
	// Gt wants numeric greater-than.
	Gt Operator = "gt"
	// Gte wants numeric greater-than-or-equal.
	Gte Operator = "gte"
	// Lt wants numeric less-than.
	Lt Operator = "lt"
	// Lte wants numeric less-than-or-equal.
	Lte Operator = "lte"
	// AnyIn wants the context's LIST to share a member with the rule's values.
	// It is the only word that reads a list rather than a single value (ADR 0144).
	AnyIn Operator = "any_in"
)

// Operators is the whole vocabulary, in the order the words are declared.
var Operators = []Operator{Eq, Ne, In, Nin, Gt, Gte, Lt, Lte, AnyIn}

// Numeric reports whether the operator compares integers.
func (o Operator) Numeric() bool {
	return o == Gt || o == Gte || o == Lt || o == Lte
}

// MultiValue reports whether the operator takes more than one value in the
// RULE. Every other word takes exactly one.
func (o Operator) MultiValue() bool {
	return o == In || o == Nin || o == AnyIn
}

// ReadsAList reports whether the operator reads the context's LIST side.
//
// It separates the two halves of "multi-valued": every [Operator.MultiValue]
// word takes several values in the rule, and only this one takes several in the
// context. Folding them together is what would make a shipped `in` rule start
// reading a list it was never written against (ADR 0144).
func (o Operator) ReadsAList() bool {
	return o == AnyIn
}

// Readable reports whether a rule value is one [Match] can read under the
// operator: it is not empty, and for a numeric operator it is a base-10
// integer exactly as written, without surrounding spaces.
//
// Every writer of a rule asks it, so a rule that could never match is refused
// at the door rather than stored as a dead record; [Match] asks it too, so a
// row that reached the table some other way closes its rule instead of
// opening it.
func Readable(op Operator, value string) bool {
	if value == "" {
		return false
	}
	if op.Numeric() {
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	}
	return true
}

// Compare applies a numeric comparison to two integers. Any operator other
// than eq, ne, gt, gte, lt and lte answers false.
func Compare(op Operator, have, want int64) bool {
	switch op {
	case Eq:
		return have == want
	case Ne:
		return have != want
	case Gt:
		return have > want
	case Gte:
		return have >= want
	case Lt:
		return have < want
	case Lte:
		return have <= want
	default:
		return false
	}
}

// Match reports whether one condition holds: value is the context's value for
// the rule's attribute, present whether the context carries that attribute at
// all, and list the context's list for it, which only [AnyIn] reads.
//
// A condition that cannot be read does not match, and it does not panic. A
// rule with no values, a value [Readable] refuses, or an operator outside the
// vocabulary all answer false. Service validation never writes such a row, but
// a maintenance script running SQL directly or a partial restore can leave one,
// and a condition that cannot be read must not silently disable the rule and
// open a price, an option or a discount to everybody. A hand-written `ne ""`
// therefore matches nothing, where it used to match every present value.
//
// An ABSENT attribute does not match either, even under a negative operator
// such as ne or nin: otherwise a request with an empty context would satisfy
// every negative rule.
//
// [AnyIn] reads only the list and every other operator only the single value.
// Mixing the two would make a shipped `in` rule start reading the list one day,
// and a live discount would change with nothing announcing it (ADR 0144). An
// empty list does not match: which groups the customer is in is then unknown,
// and an unknown segment counted as matched opens the segment to everybody.
//
// A numeric operator reads the context's value as a base-10 integer exactly as
// written. A value that does not parse, including one with surrounding spaces
// or a fraction, makes the condition not match rather than fail: the context
// comes from outside, and one broken field must not bring down a whole price
// calculation, shipping list or discount computation. The comparison is
// numeric, not lexical: compared as strings, "9" would come out larger than
// "50000" and a free-shipping threshold would be inverted.
//
// It allocates nothing.
func Match(op Operator, values []string, value string, present bool, list []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		if !Readable(op, v) {
			return false
		}
	}

	if op.ReadsAList() {
		for _, item := range list {
			if slices.Contains(values, item) {
				return true
			}
		}
		return false
	}

	if !present {
		return false
	}

	switch op {
	case Eq:
		return value == values[0]
	case Ne:
		return value != values[0]
	case In:
		return slices.Contains(values, value)
	case Nin:
		return !slices.Contains(values, value)
	case Gt, Gte, Lt, Lte:
		have, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return false
		}
		want, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return false
		}
		return Compare(op, have, want)
	default:
		return false
	}
}
