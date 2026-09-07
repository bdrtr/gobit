// Package erasing runs an erasure request across every holder of personal data.
//
// It is the first consumer of core/personaldata, and it is a flow rather than a
// module method for the reason ADR 0006 gives: erasing a person touches the
// customer record, the sales, the documents and whatever the embedder added,
// and no module may know another (Principle 2.1/2.4). Deciding across them is
// this layer's job, exactly as it is for the checkout saga and for invoicing.
//
// # Why the sweep is synchronous
//
// The obvious alternative was an event — the customer module announces an
// erasure and every holder subscribes. It was measured and rejected on three
// findings, and the first is decisive.
//
// The bus cannot carry an ANSWER. core/eventbus's own package doc says Publish
// does not wait, a handler's error never reaches the publisher, the in-memory
// backend is at-most-once and loses the event if the process dies, and no
// backend retries. ADR 0029's first obligation is an outcome per holder with a
// reason attached; a design whose only return channel is a log line cannot
// produce one, and a controller who has to answer a person cannot be handed a
// promise that something was probably logged somewhere.
//
// Second, the audit that would police such a topic counts to ONE.
// TestTheEventTopicsHaveASubscriber requires a published topic to have a
// subscriber, and it is satisfied by the first one it finds — so an erasure
// topic with a single listener is green while every other holder of personal
// data never hears it. The gate cannot make the event honest, and an erasure
// that silently reaches half the holders is worse than one that refuses.
//
// Third, an event fired from the customer module's delete path would make gobit
// choose POLICY. A shop deleting a customer record is bookkeeping; an erasure
// is a legal answer to a request. ADR 0029 puts that judgement with the
// embedder, and wiring the two together here would take it back.
//
// So B8 — "the customer module publishes customer.deleted" — is NOT what
// shipped, and ADR 0033 records why the obligation it stood for is met a
// different way: the hook is the interface, and it returns an answer.
//
// # Why the modules come in as a slice
//
// [FromContainer] takes the registry's modules rather than resolving a list of
// service names. A name list reaches exactly the modules somebody typed, while
// core/plugin's Host.AddModule puts a plugin's module into the SAME registry —
// so module.Registry.Modules() is the only surface that reaches gobit's own
// modules, the ones plugins bring, and the ones the embedding application adds.
// Those last are precisely the modules gobit knows nothing about and therefore
// the ones most likely to hold personal data it cannot see.
//
// The *container.Container parameter carries one real resolution — the saga
// store, which owns two tables outside every module and therefore owns the
// statement that empties them (see [sagaStoreHolder]). It would be worth
// keeping even if it carried none: internal/arch's
// TestEveryWorkflowIsSetUpInTheCompositionRoot finds a workflow package by
// looking for an exported constructor that takes a container, so dropping the
// parameter as a tidy-up would leave this package built by the composition root
// and outside the audit that checks it still is.
package erasing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/personaldata"
)

// Error codes.
const (
	// CodeNoSubject reports that the request named nobody.
	CodeNoSubject = "erasing_no_subject"
	// CodeHolderFailed reports that at least one holder could not finish.
	//
	// It is its own code because the consequence is specific and has to reach
	// the operator intact: the sweep is PARTIAL, some holders erased and one
	// did not, and the controller must not tell the data subject the work is
	// done.
	CodeHolderFailed = "erasing_holder_failed"
)

// Coordinator asks every holder of personal data to answer an erasure request.
type Coordinator struct {
	holders []holder
	// now is the clock, injectable so a test can pin the report's instant.
	now func() time.Time
}

// holder is one thing that may hold personal data.
//
// The two capabilities are kept apart because a holder may honestly have one
// and not the other. The review module stores the byline an author typed and
// deliberately stores nothing that says WHICH person that is — so it can
// declare what it keeps and cannot resolve a subject to erase.
type holder struct {
	name     string
	eraser   personaldata.Eraser
	declarer personaldata.Declarer
}

// FromContainer builds the coordinator from the registered modules.
//
// It never fails today and returns an error anyway: the signature is the one
// the composition root's other flows have, and a constructor that cannot fail
// becoming one that can is a change to every call site.
func FromContainer(c *container.Container, mods []module.Module) (*Coordinator, error) {
	co := &Coordinator{now: func() time.Time { return time.Now().UTC() }}

	for _, mod := range mods {
		h := holder{name: mod.Name()}

		// The capabilities are OPTIONAL and found by type assertion, the way
		// this repository already finds provider.SessionInspector: a module
		// with no personal data implements neither and pays nothing.
		if e, ok := mod.(personaldata.Eraser); ok {
			h.eraser = e
		}
		if d, ok := mod.(personaldata.Declarer); ok {
			h.declarer = d
		}

		if h.eraser == nil && h.declarer == nil {
			continue
		}

		co.holders = append(co.holders, h)
	}

	co.holders = append(co.holders, outsideTheModuleTree(c)...)

	return co, nil
}

// Erase asks every holder about the subject and returns one report.
//
// The sweep does NOT stop at the first failure. A holder that cannot finish
// leaves the person's data where it is, and abandoning the remaining holders
// would leave MORE of it — so the rest are asked, the successful answers stay
// in the report, and the error names every holder that failed. The caller gets
// both halves and must treat the report as partial.
//
// A failure is not a fourth outcome. ADR 0029's three outcomes describe what a
// holder DID; a holder that could not act did not do any of them, and folding
// a fault into [personaldata.Retained] would make a broken query indistinguishable
// from a lawful refusal — the exact confusion the third outcome exists to end.
func (co *Coordinator) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Report, error) {
	if s.CustomerID == "" && s.Email == "" {
		return personaldata.Report{}, coreerrors.Invalid(CodeNoSubject,
			"an erasure request has to name somebody: give a customer id, an e-mail address, or both")
	}

	report := personaldata.Report{Subject: s, At: co.now()}

	var failures []error

	for _, h := range co.holders {
		if h.eraser == nil {
			// A holder that DECLARES personal data and offers no erasure is
			// still in the report, and this is the line that keeps the report
			// complete by construction: adding a declaration to a module makes
			// it visible in every sweep from that moment, with no second list
			// to maintain and nothing to forget. Leaving it out would produce
			// the failure this whole mechanism exists to avoid — an answer that
			// is true of everything it mentions and silent about the rest.
			if res, ok := undeclaredEraser(h); ok {
				report.Results = append(report.Results, res)
			}

			continue
		}

		result, err := h.eraser.Erase(ctx, s)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", h.name, err))
			continue
		}

		// The holder's own name wins over whatever it filled in: a report that
		// attributes an answer to the wrong holder is worse than one with a
		// missing name, and the registry is the authority on what a module is
		// called.
		result.Holder = h.name
		report.Results = append(report.Results, result)
	}

	if len(failures) > 0 {
		return report, coreerrors.Wrap(errors.Join(failures...), coreerrors.KindInternal,
			CodeHolderFailed,
			"the erasure is PARTIAL: %d of %d holders could not finish, and what they hold is still there",
			len(failures), len(report.Results)+len(failures))
	}

	return report, nil
}

// undeclaredEraser builds the answer for a holder that declares personal data
// and cannot erase it.
//
// The outcome is Retained because that is what happened: the data is kept, the
// holder can say exactly what and where, and there is a reason. The reason is
// not always the same one — the review module cannot resolve a subject at all,
// the auth module's subject is a staff member rather than a shopper — so the
// sentence says what is true generically and the declaration says what is held.
// A holder that declares NOTHING produces no result: it has already said it
// keeps nothing about anybody, and repeating that per sweep would bury the
// holders that do.
func undeclaredEraser(h holder) (personaldata.Result, bool) {
	if h.declarer == nil {
		return personaldata.Result{}, false
	}

	declared := h.declarer.PersonalData()
	if len(declared.Holdings) == 0 {
		return personaldata.Result{}, false
	}

	kept := make([]string, 0, len(declared.Holdings))
	for _, hold := range declared.Holdings {
		kept = append(kept, hold.Table+"."+hold.Column)
	}

	return personaldata.Result{
		Holder:  h.name,
		Outcome: personaldata.Retained,
		Kept:    kept,
		Why: "this holder declares personal data and offers no erasure; what it keeps is listed above " +
			"and the embedder, as controller, decides what to do about it",
	}, true
}

// PersonalData returns what every holder says it keeps about people.
//
// This is ADR 0029's third obligation and the one that makes the other two
// honest: without it an embedder cannot answer a data-subject request even in
// principle, because nothing tells it where to look. It takes no context and
// touches no database — a declaration is a property of the code, so it reads
// the same on an empty installation as on a full one.
func (co *Coordinator) PersonalData() []personaldata.Declaration {
	out := make([]personaldata.Declaration, 0, len(co.holders))

	for _, h := range co.holders {
		if h.declarer == nil {
			continue
		}

		d := h.declarer.PersonalData()
		d.Holder = h.name
		out = append(out, d)
	}

	return out
}
