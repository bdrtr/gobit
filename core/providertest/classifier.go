package providertest

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
)

// Classifier runs the compliance suite for [provider.Classifier].
//
// It calls [Identity] and then the one rule the contract states that can be
// checked without the model's service behind it.
//
// # The context is already CANCELED, and that is the point
//
// A provider that obeys the rule below refuses the input and never reaches the
// network; one that does not would try, and a canceled context stops it at the
// door. So this suite can run in an ordinary unit test — no key, no service, no
// clock — and a provider that calls out anyway fails fast rather than turning
// somebody's test run into a bill.
//
// # Why the KIND is asserted and not merely an error
//
// "Fewer than two labels is refused" would be satisfied by any provider that
// simply failed — including one that ignored the input, called its service and
// got a connection error. That assertion would pass for exactly the provider it
// is meant to catch.
//
// The kind separates them. [errors.KindInvalid] means the CALLER got it wrong
// and must change the request; a transport failure is [errors.KindUnavailable]
// or [errors.KindInternal] and means try again. Callers branch on that
// difference — internal/jobs/reviewsuggest counts a refusal against the
// provider and a conflict against the operator — so a provider that reports the
// wrong kind sends a scheduled job into the wrong arm of a retry.
func Classifier(t T, p provider.Classifier) {
	t.Helper()

	Identity(t, p)

	if p == nil {
		return
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, labels := range [][]string{nil, {}, {"approved"}} {
		_, err := p.Classify(canceled, provider.ClassifyInput{
			Instruction: "decide whether this text may be published",
			Text:        "a piece of text",
			Labels:      labels,
		})

		if err == nil {
			t.Errorf("Classify answered with %d label(s) and no error; a closed question "+
				"needs at least two answers, and a caller cannot store a label that came "+
				"from a set of one", len(labels))

			continue
		}

		if kind := errors.KindOf(err); kind != errors.KindInvalid {
			t.Errorf("Classify refused %d label(s) with kind %v; it must be %v.\n"+
				"A caller tells a bad REQUEST from a bad CONNECTION by the kind, and "+
				"reporting a transport failure here would make a scheduled caller retry "+
				"an input that can never succeed. Error: %v",
				len(labels), kind, errors.KindInvalid, err)
		}
	}
}
