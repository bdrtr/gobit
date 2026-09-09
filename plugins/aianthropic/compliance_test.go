package aianthropic_test

import (
	"testing"

	"github.com/bdrtr/gobit/core/providertest"
	"github.com/bdrtr/gobit/plugins/aianthropic"
)

// TestTheProviderIsCompliant runs the published compliance suite for a
// classifier.
//
// [providertest.Classifier] checks the identity AND the one contract rule that
// holds with no service behind it: a closed question needs at least two
// answers, and refusing it must be reported as a bad REQUEST rather than a bad
// connection. A caller branches on that difference — the review job counts a
// refusal against the provider and a conflict against the operator — so a
// provider reporting the wrong kind sends a scheduled run into the wrong arm.
//
// The zero value is enough: the rule is refused before the HTTP client is
// touched, which is itself part of what the suite proves.
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	providertest.Classifier(t, &aianthropic.Classifier{})
}
