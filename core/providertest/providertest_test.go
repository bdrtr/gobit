package providertest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/providertest"
)

// recorder is a [providertest.T] that keeps what the suite reported.
//
// The suite's whole value is that it FAILS on a bad provider, and a suite
// tested only against a good one proves that it compiles. So every rule below
// is exercised by a provider that breaks it, and the recorder is how a failure
// is observed without failing this test.
type recorder struct{ failures []string }

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, format)
	_ = args
}

// saw reports whether any failure message contains the phrase.
func (r *recorder) saw(phrase string) bool {
	for _, failure := range r.failures {
		if strings.Contains(failure, phrase) {
			return true
		}
	}

	return false
}

// fixedID is a provider whose identity is whatever it was built with.
type fixedID string

func (f fixedID) ID() string { return string(f) }

// countingID answers a different identity every time it is asked.
type countingID struct{ n int }

func (c *countingID) ID() string {
	c.n++

	return "provider-" + strings.Repeat("x", c.n)
}

// TestEveryIdentityRuleFires is the suite's own mutation proof.
//
// Each row is a provider breaking exactly one rule, and the assertion is that
// the suite REPORTS it. Without this file a rule could be deleted, or written
// so it never matches, and every provider in the tree would keep passing.
func TestEveryIdentityRuleFires(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		provider provider.Provider
		expect   string
	}{
		"an empty identity":     {fixedID(""), "empty or blank"},
		"an identity of spaces": {fixedID("   "), "empty or blank"},
		"a surrounding space":   {fixedID(" stripe"), "would store"},
		"a trailing space":      {fixedID("stripe "), "would store"},
		"whitespace inside":     {fixedID("my provider"), "contains whitespace"},
		"a control character":   {fixedID("stri\x00pe"), "control character"},
		// The escape rather than the letter: a non-ASCII rune written into a
		// Go file here would be one more line in the language ledger for what
		// is DATA — an example of the shape being refused (ADR 0012).
		"a non-ASCII identity":     {fixedID("caf\u00e9"), "not ASCII"},
		"an identity that changes": {&countingID{}, "and then"},
		"no provider at all":       {nil, "nil"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := &recorder{}
			providertest.Identity(got, testCase.provider)

			if !got.saw(testCase.expect) {
				t.Errorf("the suite did not report %q for a provider breaking that rule; "+
					"reported: %v", testCase.expect, got.failures)
			}
		})
	}
}

// TestAGoodIdentityIsNotReported is the counterpart, and it is what makes the
// table above mean something.
//
// A suite that failed everything would satisfy every row and be useless: no
// provider could ever pass it.
func TestAGoodIdentityIsNotReported(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"stripe", "s3", "log-only", "paytr", "a"} {
		got := &recorder{}
		providertest.Identity(got, fixedID(id))

		if len(got.failures) != 0 {
			t.Errorf("the suite refused the ordinary identity %q: %v", id, got.failures)
		}
	}
}

// TestAClashIsReportedAndTheOrderIsNamed covers the registry's own refusal.
func TestAClashIsReportedAndTheOrderIsNamed(t *testing.T) {
	t.Parallel()

	got := &recorder{}
	providertest.UniqueIdentities(got, fixedID("stripe"), fixedID("paytr"), fixedID("stripe"))

	if !got.saw("registered first") {
		t.Errorf("two providers sharing an identity were not reported: %v", got.failures)
	}

	clean := &recorder{}
	providertest.UniqueIdentities(clean, fixedID("stripe"), fixedID("paytr"))

	if len(clean.failures) != 0 {
		t.Errorf("distinct identities were reported as a clash: %v", clean.failures)
	}
}

// stubClassifier answers with whatever it was built to answer.
type stubClassifier struct {
	id  string
	err error
}

func (s stubClassifier) ID() string { return s.id }

func (s stubClassifier) Classify(
	_ context.Context, in provider.ClassifyInput,
) (provider.Classification, error) {
	if len(in.Labels) < 2 {
		return provider.Classification{}, s.err
	}

	return provider.Classification{Label: in.Labels[0], Reason: "because", Model: "m"}, nil
}

// TestTheClassifierRuleSeparatesABadRequestFromABadConnection is the assertion
// the suite would be worthless without.
//
// "Returns an error" is satisfied by a provider that ignored the input, called
// its service and failed — which is exactly the provider the rule exists to
// catch. The KIND is what tells them apart.
func TestTheClassifierRuleSeparatesABadRequestFromABadConnection(t *testing.T) {
	t.Parallel()

	obedient := &recorder{}
	providertest.Classifier(obedient, stubClassifier{
		id:  "good",
		err: errors.Invalid("bad_input", "a classification needs at least two labels"),
	})

	if len(obedient.failures) != 0 {
		t.Errorf("a provider refusing one label with KindInvalid was reported: %v",
			obedient.failures)
	}

	// The one the rule exists for: it did not look at the labels, went to the
	// network and reported a transport failure.
	transport := &recorder{}
	providertest.Classifier(transport, stubClassifier{
		id:  "calls-out",
		err: errors.Unavailable("upstream", "the model could not be reached"),
	})

	if !transport.saw("bad CONNECTION") {
		t.Errorf("a provider reporting a transport failure for a bad input was not "+
			"reported: %v", transport.failures)
	}

	// And a provider that answers a one-label question at all.
	silent := &recorder{}
	providertest.Classifier(silent, stubClassifier{id: "answers-anyway"})

	if !silent.saw("no error") {
		t.Errorf("a provider answering a closed question with one label was not "+
			"reported: %v", silent.failures)
	}
}
