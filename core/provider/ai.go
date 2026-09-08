package provider

import "context"

// Classification is what a model concluded about a piece of text.
//
// The three fields are what a caller needs to STORE the conclusion and let
// somebody weigh it afterwards. A bare label would be a verdict with no way to
// check it, and the only sound thing a person can do with an unweighable
// machine verdict is ignore it.
type Classification struct {
	// Label is the answer, and it is one of the labels the request offered.
	//
	// A provider that returns anything else has failed the call rather than
	// answered it: the caller chose a closed set precisely so the answer would
	// fit somewhere it has already decided is safe — a column with a CHECK, a
	// switch with no default.
	Label string
	// Reason is why, in the model's own words.
	//
	// It is not optional in practice, because it is the whole basis on which a
	// person decides whether to act on the label; a provider that has nothing
	// to say should say so rather than return an empty string.
	Reason string
	// Model is which model produced this, as the provider names it.
	//
	// It is the provider's answer rather than the caller's configuration
	// because those two drift: an installation naming a model family gets
	// whichever version the service is serving that week, and a conclusion
	// stored without it cannot be retired when the shop stops trusting one.
	Model string
}

// ClassifyInput is a piece of text and the closed set of answers a model may
// choose between.
type ClassifyInput struct {
	// Instruction is what the caller is asking, in the caller's own words.
	//
	// It belongs to the CALLER and not to the provider: the provider knows
	// nothing about reviews, or products, or what a shop considers acceptable,
	// and a contract that let it supply the question would be a contract with
	// an opinion in it.
	Instruction string
	// Text is what is being judged.
	Text string
	// Labels is the closed set of answers, and it must hold at least two.
	//
	// One label is not a question. An open answer is not this contract: a
	// provider that may reply with anything hands the caller something it
	// cannot store and cannot switch on, and every caller would then write the
	// same brittle parsing.
	Labels []string
}

// Classifier is a provider that answers a closed question about a text.
//
// # Why a classification and not a completion
//
// A completion contract — text in, text out — is the general one, and it was
// refused for the reason the general one is worse here. What the callers in
// this tree need is an answer they can put in a column and act on; a free-text
// answer makes every caller invent its own parsing, and the day a model phrases
// itself differently each of those parsers breaks separately and silently.
// Closing the answer set moves that failure to the provider, once, where it is
// an error rather than a misread.
//
// # It decides nothing
//
// A classification is an OPINION and this contract cannot express anything
// else. Nothing here writes, moves or approves, and the caller is expected to
// store the answer somewhere a person still stands between it and its effect.
// That is not a property of this interface — an interface cannot enforce what
// its caller does — which is exactly why it is written down.
type Classifier interface {
	Provider

	// Classify answers the question, choosing from the labels in the input.
	//
	// The call BLOCKS and goes to an external service; the caller must put a
	// deadline on ctx. An error means no answer was obtained — including the
	// case where the service replied with something outside the label set,
	// which is a failure of the call and not a conclusion.
	Classify(ctx context.Context, in ClassifyInput) (Classification, error)
}
