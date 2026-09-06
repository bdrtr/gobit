package pgstore

import (
	"context"

	"github.com/bdrtr/gobit/core/personaldata"
)

// ErasureHolder is the name this store answers an erasure request under.
//
// It is a package path rather than a module name because that is what this is:
// the saga store is not a commerce module and looking for a directory under
// internal/modules called "workflow" would waste somebody's afternoon.
const ErasureHolder = "internal/core/workflow/pgstore"

// The capabilities are pinned at compile time, the way every module pins
// openapi.Describer. Both are found by TYPE ASSERTION at the composition root,
// so a renamed method or a changed signature would break nothing at build time
// and would silently drop this store out of every erasure report — the one
// failure this mechanism exists to prevent.
var (
	_ personaldata.Eraser   = (*store)(nil)
	_ personaldata.Declarer = (*store)(nil)
)

// tableExecutions and tableSteps are the two tables this store owns.
const (
	tableExecutions = "workflow_executions"
	tableSteps      = "workflow_execution_steps"
)

// redaction is what an erased execution's input keeps instead of the person.
//
// It is a single JSON object passed as ONE parameter and used TWICE in the
// statement — once to write and once to decide whether writing would change
// anything. Spelling it out in both places would be two literals that agree on
// the day they are written; as one parameter they cannot drift.
//
// The three keys are the whole of the personal data this store holds, and the
// list is short because it was measured rather than assumed: the persisted
// input is the checkout plan, and of its fields only `email`,
// `shipping_address` and `billing_address` carry a person. The addresses become
// JSON null rather than being removed, and the address becomes the empty string
// rather than disappearing, so a reader can still see that the field existed
// and was emptied — "redacted" and "never had one" are different facts.
const redaction = `{"email": "", "shipping_address": null, "billing_address": null}`

// redactInputSQL empties the personal fields of a subject's execution records.
//
// # Why the input is edited rather than dropped
//
// Nulling the whole column would have been simpler and it would have broken the
// one thing that genuinely reads it. `gobit recover <execution-id> -confirm`
// rebuilds a half-finished saga's definition FROM the record's input
// (ADR 0017), and that rebuild refuses a plan with no cart id. Editing the
// three personal keys leaves every field the rebuild uses — the cart id, the
// amounts, the lines — exactly where it was.
//
// That the rest is genuinely unused was measured, not assumed: the engine's own
// recovery path copies the input into the step context and NOTHING reads it
// back (a search over internal/workflows, internal/modules, plugins and core
// returns zero uses of StepContext.Input), and the five compensations of the
// only workflow that runs here read `cart_id`, `amount` and `currency_code` and
// nothing else. Terminal executions never touch the input at all — the engine
// answers them from `output` and `failure`.
//
// # Why there is no status filter
//
// Because there does not need to be one. An earlier design pruned only
// TERMINAL executions, on the theory that a live one still needs its input;
// once the fields a recovery actually reads were measured, the distinction
// stopped buying anything and cost a question nobody could answer cheaply —
// `status` is free text in this schema with no enum behind it, so "terminal"
// would have been a list in Go that the database does not enforce.
//
// # Idempotence
//
// The last predicate is the whole of it: a row is written only if the redaction
// would change it. A second sweep therefore reports zero rows rather than
// touching `updated_at` again, and a sweep by e-mail finds nothing the second
// time because the address it matched on is now empty.
//
// # There is no guard against a malformed input, and there was one
//
// `input` is a nullable jsonb with no constraint keeping a non-object out of
// it, so an earlier version of this statement opened with
// `jsonb_typeof(input) = 'object'`. It was removed because it could not fire,
// and the removal is recorded rather than done quietly because the REASON
// written beside it had also been wrong.
//
// The claim was that `||` raises on a scalar. Measured against PostgreSQL 16:
// it does not — concatenating a scalar with an object succeeds. What actually
// protects the statement is the predicate above it: `->>` on anything that is
// not an object returns SQL NULL, so both arms are false and the row is never
// selected. A guard that cannot fire, defended by a sentence that is not true,
// is worse than no guard — it invites the next reader to trust it.
//
// The property is still pinned, by
// TestErasureIsUnmovedByAMalformedInput in the integration tests.
const redactInputSQL = `
UPDATE ` + tableExecutions + `
SET input = input || $3::jsonb,
    updated_at = now()
WHERE (
        ($1::text <> '' AND input->>'customer_id' = $1::text)
     OR ($2::text <> '' AND input->>'email' = $2::text)
      )
  AND input IS DISTINCT FROM (input || $3::jsonb)
`

// Erase empties the personal fields of every execution record about the subject.
//
// The outcome is ANONYMIZED rather than DELETED: the rows stay, because they
// are the evidence that a saga ran and the only thing an operator has when one
// has to be finished by hand. What leaves is the person.
//
// A subject this store has never seen is not an error. The sweep hands every
// holder the same subject and most holders will not have heard of them; zero
// rows with an Anonymized outcome is the honest answer, and the caller can tell
// it apart from a refusal by the outcome rather than by the count.
func (s *store) Erase(ctx context.Context, subject personaldata.Subject) (personaldata.Result, error) {
	result := personaldata.Result{
		Holder:  ErasureHolder,
		Outcome: personaldata.Anonymized,
		Kept:    keptAfterErasure(),
		Why:     whyKept,
	}

	if subject.CustomerID == "" && subject.Email == "" {
		return result, nil
	}

	pool, err := s.rawPool()
	if err != nil {
		return personaldata.Result{}, err
	}

	tag, err := pool.Exec(ctx, redactInputSQL, subject.CustomerID, subject.Email, redaction)
	if err != nil {
		return personaldata.Result{}, wrapDB(err, CodeQueryFailed,
			"the personal fields of the execution records could not be emptied")
	}

	result.Rows = int(tag.RowsAffected())

	return result, nil
}

// whyKept explains what an erased execution record still carries.
const whyKept = "the execution record keeps what the saga DID — the identifiers, the amounts and " +
	"the step results — because it is the evidence a checkout ran and the only thing an operator has " +
	"when one has to be finished by hand. The free-text failure columns are left as they were: gobit " +
	"does not rewrite a free-form field, and an error string can quote whatever caused it"

// keptAfterErasure names the columns that may still hold the person.
//
// The two `output` columns are deliberately NOT here, and that is a measured
// exclusion rather than an omission: the execution's output and every step's
// output are identifiers and amounts, so no shape written to them carries a
// person. The two `failure` columns ARE here, because an error string is free
// text and commonly quotes the input that produced it.
func keptAfterErasure() []string {
	return []string{
		tableExecutions + ".failure",
		tableSteps + ".failure",
	}
}

// PersonalData declares what this store keeps about people.
//
// The declaration is narrower than the one the erasure coordinator carried
// before this eraser existed, and the narrowing is the measurement: the earlier
// version listed both `output` columns as possibly personal because nobody had
// looked at what goes into them. They hold identifiers and amounts.
func (s *store) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holdings: []personaldata.Holding{
			{
				Table: tableExecutions, Column: "input", Kind: personaldata.Named,
				Why: "the saga's arguments; for the one workflow that runs here it is the checkout " +
					"plan, which carries the shopper's e-mail address and the shipping and billing " +
					"addresses in full. An erasure empties exactly those three and leaves the rest, " +
					"because the rest is what an abandoned saga is recovered from",
			},
			{
				Table: tableExecutions, Column: "failure", Kind: personaldata.Open,
				Why: "the error text of a failed run, which commonly echoes the input that caused it",
			},
			{
				Table: tableSteps, Column: "failure", Kind: personaldata.Open,
				Why: "one step's error text, with the same echo problem as the execution's",
			},
		},
	}
}
