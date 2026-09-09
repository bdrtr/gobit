// Package providertest is the compliance suite a gobit provider passes.
//
// # Who it is for
//
// Anybody writing a provider — a payment integration, a shipping carrier, a
// notification transport, a model client — inside this repository or outside
// it. gobit is a library (ADR 0025), so a plugin written by somebody else is
// the ordinary case rather than the exception, and the rules their provider has
// to obey were until now written in godoc and enforced by nothing.
//
// Call it from an ordinary test:
//
//	func TestCompliance(t *testing.T) {
//		providertest.Identity(t, myProvider)
//	}
//
// # What it checks and what it CANNOT
//
// It checks the contract's rules that hold without an upstream: the identity
// every registry keys on, and the input a contract says a provider must refuse
// before it calls anything. It does NOT check that a provider talks to its
// service correctly — nothing here can, and a suite that pretended to would be
// worse than none, because a green run would read as "the integration works".
//
// # Why it takes an interface rather than *testing.T
//
// This package is part of the published surface (ADR 0026), and a published
// package that imports `testing` puts the testing flags into every binary that
// links it. [T] is the two methods the suite needs, and `*testing.T` satisfies
// it without anybody writing an adapter.
//
// # Why it asserts by hand
//
// No assertion library. An embedder importing this package would otherwise
// inherit one, and a published package's dependencies are the embedder's
// dependencies — the same reasoning that has the error reporters writing their
// own request bodies rather than taking an SDK (ADR 0014).
package providertest

import (
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/core/provider"
)

// T is the part of *testing.T this suite uses.
//
// Errorf rather than Fatalf: a compliance run should report every rule the
// provider breaks, not the first. A provider with a blank identity usually has
// more than one thing wrong with it, and stopping at the first turns one test
// run into four.
type T interface {
	// Helper marks the calling function as a test helper.
	Helper()
	// Errorf reports a failure and continues.
	Errorf(format string, args ...any)
}

// Identity checks the rules every provider's ID obeys.
//
// The identity is not a label. It is the key a registry stores the provider
// under, the value an operator types into configuration, and — for payment and
// for a model's proposal — a string written into a database column that
// outlives the release. [provider.Provider] says it "MUST NOT CHANGE from one
// release to the next"; these are the rules that make that sentence
// enforceable inside one.
func Identity(t T, p provider.Provider) {
	t.Helper()

	if p == nil {
		t.Errorf("the provider is nil; there is nothing to check")

		return
	}

	id := p.ID()

	// A registry refuses a blank identity at registration, which is a failure
	// at BOOT — in front of an operator, on a deploy. Here it is a failure at
	// test time, in front of the person who can fix it.
	if strings.TrimSpace(id) == "" {
		t.Errorf("ID() is empty or blank; a registry refuses to register it and the "+
			"installation will not come up (got %q)", id)

		return
	}

	// A registry stores the TRIMMED identity as its key while every other
	// reader — the startup log, an error message naming the registered
	// providers, a column recording which provider acted — takes p.ID() as it
	// is. An identity with surrounding space is therefore two different strings
	// in one installation, and the two are compared in different places.
	if id != strings.TrimSpace(id) {
		t.Errorf("ID() is %q, which is not the identity a registry would store (%q): "+
			"the registry trims, and every other reader does not",
			id, strings.TrimSpace(id))
	}

	// The identity is typed into configuration — PAYMENT_PROVIDER, FILE_PROVIDER
	// — and printed into error messages as one word among several. Whitespace
	// inside it makes both ambiguous, and a control character makes a log line
	// something other than what it appears to be.
	for _, r := range id {
		switch {
		case unicode.IsSpace(r):
			t.Errorf("ID() %q contains whitespace; it is a value an operator types into "+
				"an environment variable and one word of a comma-separated list", id)
		case unicode.IsControl(r):
			t.Errorf("ID() %q contains a control character", id)
		case r > unicode.MaxASCII:
			// This repository has twice been bitten by a value whose bytes
			// depended on a locale (ADR 0038, ADR 0039). An identity is
			// compared byte for byte across a Go constant, an environment
			// variable and a database column, and the comparison is exact
			// everywhere — so a non-ASCII identity is not wrong today and is
			// one encoding away from being wrong tomorrow.
			t.Errorf("ID() %q is not ASCII; it is compared byte for byte between a "+
				"constant, a config value and a stored column", id)
		}
	}

	// An identity computed rather than returned — from a clock, a counter, a
	// map iteration — registers under one key and answers with another. Nothing
	// would report it: the registry keeps the first value and the provider goes
	// on returning new ones to every other reader.
	if again := p.ID(); again != id {
		t.Errorf("ID() answered %q and then %q; the registry keeps the first and every "+
			"later reader gets a different one", id, again)
	}
}

// UniqueIdentities checks that a set of providers can share one registry.
//
// A registry refuses the second registration of an identity, and the plugin
// that loses is whichever loaded second — an ordering nobody chose. The
// conflict is worth finding in a test rather than in an operator's boot log,
// which is why this takes a SET rather than one provider.
func UniqueIdentities(t T, providers ...provider.Provider) {
	t.Helper()

	seen := map[string]int{}
	for index, p := range providers {
		if p == nil {
			t.Errorf("provider %d is nil", index)

			continue
		}

		id := strings.TrimSpace(p.ID())
		if first, clash := seen[id]; clash {
			t.Errorf("providers %d and %d both answer %q; a registry keeps whichever "+
				"registered first and the other is refused at boot", first, index, id)

			continue
		}

		seen[id] = index
	}
}
