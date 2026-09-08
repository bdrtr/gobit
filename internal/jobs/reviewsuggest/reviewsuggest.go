// Package reviewsuggest asks a model about the reviews waiting for an operator
// and stores what it says.
//
// # What it does and, more importantly, what it cannot do
//
// It reads the oldest reviews nobody has decided about and no model has been
// asked about, sends each one to the configured [provider.Classifier], and
// writes the answer back as a PROPOSAL (ADR 0071). It moves no review, it
// approves nothing, and nothing it writes can reach a shopper: the columns it
// fills are not the ones the storefront reads, and the statement behind
// [reviews.Suggest] names neither the status nor the moderation moment.
//
// That is the whole reason this can be a scheduled process at all. ADR 0017
// refuses scheduled COMPENSATION — side effects nobody watched — and ADR 0019
// says nothing scheduled acts. Storing an opinion in a column an operator reads
// is not acting: the operator still decides, and until they do the review is
// exactly where it was.
//
// # Why the work is stored rather than done when the queue is opened
//
// Computing on read makes the operator wait for a model on every page and lets
// the answer change under them between two loads. ADR 0071 has the argument;
// this job is the other half of it — the thing that fills the column.
//
// # It is registered only when a provider is configured
//
// Most installations will run no model at all, and a job that appeared in
// `gobit jobs` reporting "no provider" every quarter of an hour would be noise
// an operator learns to skip. The composition root registers this one only when
// the container holds a classifier, which is the same conditional shape the
// error reporter uses (ADR 0014).
package reviewsuggest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/core/job"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// Name is the job's name, and it is the primary key of the run history an
// operator already has; it does not change.
const Name = "review-suggest"

// Every is how often the pass runs.
//
// A quarter of an hour rather than a minute: a review nobody has read yet is
// not urgent, and the pass costs money at somebody else's API. It is not hours
// either — a proposal that arrives after the operator has already read the
// review is a proposal nobody needed.
const Every = 15 * time.Minute

// MaxRun bounds one pass.
//
// It is generous next to the other jobs' because every item in the batch is a
// call to an external model, and those are slow in a way a database read is
// not. The bound is what stops a provider that has stopped answering from
// holding the scheduler's slot until the next occurrence.
const MaxRun = 5 * time.Minute

// Batch is how many reviews one pass asks about.
//
// The number is the appetite, and it is small on purpose. A pass that emptied
// the queue would turn a year of unmoderated reviews into one very large bill
// on the day somebody installed this; twenty every quarter of an hour is a
// rate a shop can watch for an hour and then decide about.
const Batch = 20

// perItem bounds ONE classification.
//
// Without it a single provider that never answers consumes the whole pass and
// the nineteen reviews behind it are not attempted at all — which is the shape
// the outbox relay's ceiling exists to prevent one layer down.
const perItem = 20 * time.Second

// Error codes.
const (
	// CodeReadFailed reports that the waiting reviews could not be read.
	CodeReadFailed = "review_suggest_read_failed"
	// CodeNothingSucceeded reports a pass that had work and completed none of it.
	CodeNothingSucceeded = "review_suggest_nothing_succeeded"
)

// instruction is what the model is asked, and it is the CALLER's sentence.
//
// It lives here rather than in the provider contract because the provider knows
// nothing about shops, reviews or what this one considers acceptable — a
// contract that supplied the question would be a contract with an opinion in
// it. It is a constant rather than configuration for a different reason: a
// shop that could write its own prompt could write one that asks the model to
// approve everything, and the answer would still arrive in a column an operator
// reads as a recommendation.
const instruction = "You are helping a shop's moderator triage a customer's product review. " +
	"Answer with 'approved' if the text is a genuine opinion about the product, " +
	"and 'rejected' if it is spam, an advertisement for somewhere else, abuse, or " +
	"if it publishes another person's private information. " +
	"Give one short sentence of reasoning. You are advising a person, not deciding."

// reviews is the review module's surface this job needs, declared HERE.
//
// Two methods, and neither of them can move a review. That is the property
// TestNoJobWritesThroughAModuleService in internal/arch is about: a job names
// the narrow surface it needs instead of holding a module's whole service, so
// what a scheduled process can do is visible in one place — here — rather than
// being everything the module can do.
type reviews interface {
	// AwaitingSuggestion returns the oldest reviews waiting for a human that no
	// model has been asked about.
	AwaitingSuggestion(ctx context.Context, limit int64) ([]models.Review, error)
	// Suggest stores a proposal about one of them.
	Suggest(ctx context.Context, id string, in service.SuggestInput) (models.Review, error)
}

// classifier is the model side, and it is [provider.Classifier] narrowed to the
// one method this job calls plus the identity it logs.
type classifier interface {
	ID() string
	Classify(ctx context.Context, in provider.ClassifyInput) (provider.Classification, error)
}

// Definition builds the job.
func Definition(r reviews, c classifier, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, r, c, log) },
	}
}

// pass is what one run did, and it is what the operator reads afterwards.
type pass struct {
	// read is how many reviews were waiting and unasked.
	read int
	// proposed is how many proposals were stored.
	proposed int
	// refused counts the answers the module would not take — a label outside
	// the pair, an empty reason. They are the model's failures, not the
	// review's, and they are counted separately from the moved ones because
	// they mean something different: a provider answering badly, rather than an
	// operator getting there first.
	refused int
	// moved counts the reviews an operator decided about between the read and
	// the write. It is the normal outcome of racing a person and it is not an
	// error.
	moved int
	// failed counts the classifications that did not come back at all.
	failed int
}

// summarize is the operator's line.
func (p pass) summarize(providerID string) string {
	return fmt.Sprintf(
		"%d waiting, %d proposed by %q, %d refused, %d moved under us, %d failed",
		p.read, p.proposed, providerID, p.refused, p.moved, p.failed)
}

// run is one pass.
func run(ctx context.Context, r reviews, c classifier, log *slog.Logger) error {
	waiting, err := r.AwaitingSuggestion(ctx, Batch)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), CodeReadFailed,
			"the reviews awaiting a proposal could not be read, so this pass cannot say "+
				"whether there was anything to do")
	}

	result := pass{read: len(waiting)}
	for i := range waiting {
		classify(ctx, r, c, waiting[i], &result, log)
	}

	// Reported on EVERY pass, including the empty ones. A job that speaks only
	// when it did something leaves an operator unable to tell "nothing was
	// waiting" from "this has not run since the provider was misconfigured".
	jobreport.Report(ctx, result.summarize(c.ID()))

	// A pass that had work and completed NONE of it fails. Reporting success
	// over a batch where every call errored would be the write-only ledger this
	// repository has built once already: the listing would be green, the column
	// would stay empty, and the first sign of trouble would be an operator
	// wondering why the model never says anything.
	if result.read > 0 && result.proposed == 0 && result.moved < result.read {
		return coreerrors.Internal(CodeNothingSucceeded,
			"%d reviews were waiting and not one proposal was stored (%s)",
			result.read, result.summarize(c.ID()))
	}

	return nil
}

// classify asks about one review and stores what comes back.
//
// Every outcome is COUNTED and none of them stops the pass. One review the
// model cannot answer about is not a reason to leave the other nineteen
// unasked, and the counts are what make the difference visible afterwards.
func classify(
	ctx context.Context, r reviews, c classifier, review models.Review,
	result *pass, log *slog.Logger,
) {
	call, cancel := context.WithTimeout(ctx, perItem)
	defer cancel()

	answer, err := c.Classify(call, provider.ClassifyInput{
		Instruction: instruction,
		Text:        text(review),
		Labels:      []string{models.StatusApproved.String(), models.StatusRejected.String()},
	})
	if err != nil {
		result.failed++
		log.WarnContext(ctx, "a review could not be classified",
			"review_id", review.ID, "provider", c.ID(), "error", err)

		return
	}

	stored, err := r.Suggest(ctx, review.ID, service.SuggestInput{
		Status: models.Status(answer.Label),
		Note:   answer.Reason,
		Model:  answer.Model,
	})
	if err == nil {
		result.proposed++
		log.DebugContext(ctx, "a proposal was stored", "review_id", stored.ID)

		return
	}

	// The two failures are told apart by KIND, not by the message. A conflict
	// is an operator having decided in the meantime, which is expected and
	// costs nothing; anything else is the module refusing what the model said,
	// which is the provider's problem and has to be visible as one.
	if coreerrors.KindOf(err) == coreerrors.KindConflict {
		result.moved++

		return
	}

	result.refused++
	log.WarnContext(ctx, "the module refused what the model proposed",
		"review_id", review.ID, "provider", c.ID(), "label", answer.Label, "error", err)
}

// text is what is sent, and it is the review and nothing else.
//
// The author's name is deliberately NOT included. It is the one thing this
// module stores about a person (ADR 0071's migration), it says nothing about
// whether the text is spam, and sending it would put a customer's name into a
// third party's request log for no gain at all.
func text(review models.Review) string {
	if review.Title == "" {
		return fmt.Sprintf("Rating: %d/5\n\n%s", review.Rating, review.Body)
	}

	return fmt.Sprintf("Rating: %d/5\nTitle: %s\n\n%s", review.Rating, review.Title, review.Body)
}
