// Package personaldata is the vocabulary a data controller uses to erase a person.
//
// ADR 0029 makes the EMBEDDING application the data controller and gives
// gobit three obligations and no more: a contract with three outcomes, hooks so
// a holder of personal data can be asked, and a declaration of what each holder
// keeps. This package is those three things and nothing else. The sweep that
// calls them, the transaction each holder opens, the HTTP surface and the
// schema refusals all stay unpublished, because none of them is something an
// outside program has to NAME in order to compile.
//
// # Why it is published
//
// The rule for entering the published tree is written in
// internal/arch/public_surface_test.go and it is not a matter of taste: a
// package belongs there when a program outside this repository must name it to
// compile. An embedder's own module is exactly such a program, it is the module
// gobit knows nothing about, and it is therefore the one most likely to hold
// personal data the framework cannot see. If this vocabulary lived under
// internal/ that module could not implement [Eraser] at all, and the mechanism
// ADR 0029 promises would be unreachable by the only party that owes the
// legal duty.
//
// The failure mode of the alternative is not predicted, it is already in the
// tree: core/openapi.Describer is the same design — an optional
// capability found by type assertion — living under internal/. Every in-tree
// module implements it and the out-of-tree example module cannot, so that
// module's routes are simply missing from the document and no audit says a
// word. For a schema that costs a missing path. For an erasure it would cost a
// green build and a false answer to a person who asked to be forgotten.
//
// # Three outcomes, because two cannot tell a refusal from a fault
//
// A holder answers [Deleted], [Anonymized] or [Retained]. The third is the
// reason this needed designing rather than assuming. A module that is legally
// required to keep a document cannot answer "done", and a caller that receives
// only done-or-failed has no way to tell a lawful refusal from a broken query.
// [Retained] therefore carries WHAT was kept and WHY, because a controller
// answering a data subject has to be able to say so in a sentence.
//
// # The subject is a person, not a customer row
//
// [Subject] carries several identifiers and requires none of them in
// particular. That shape was forced by measurement rather than chosen for
// generality: the invoices table has no customer_id and no order_id column at
// all, so a document can only be found by the address printed on it, and an
// order placed by a guest carries an e-mail with a NULL customer_id. A contract
// keyed on a single customer id would have been unable to reach either.
//
// # What a holder owes
//
// Implementing [Eraser] is a promise that the answer is TRUE of the holder's
// own storage. A module that anonymizes nine columns and leaves a tenth is
// worse than one that never implemented the interface, because the report it
// produces is what the controller repeats to the data subject.
package personaldata

import (
	"context"
	"time"
)

// Subject identifies the natural person an erasure request is about.
//
// Every field is OPTIONAL and a holder uses whichever ones it can resolve. That
// is a consequence of how this repository stores people rather than a
// convenience: there are no cross-module foreign keys (Principle 2.2), so the
// customer id is a bare TEXT column where it appears at all, and two holders
// have no such column in the first place.
//
// A subject with no identifier at all is meaningless and the sweep refuses it;
// erasing "everyone" is not an erasure request.
type Subject struct {
	// CustomerID is the customer module's identifier for the person, when they
	// have a record there. A guest checkout does not create one.
	CustomerID string
	// Email is the address the person gave. It is the ONLY handle some holders
	// have — an issued invoice records the buyer's address and nothing that
	// points back at a customer record.
	Email string
}

// Outcome is what one holder did with what it held.
type Outcome string

const (
	// Deleted means the rows are gone.
	Deleted Outcome = "deleted"
	// Anonymized means the rows remain and no longer identify the person.
	//
	// It is the honest answer wherever a row has to keep existing for reasons
	// that have nothing to do with the person — a sale still has to add up
	// after its buyer is forgotten.
	Anonymized Outcome = "anonymized"
	// Retained means the holder kept personal data and can say what and why.
	//
	// It is not a failure and must not be reported as one. A holder answering
	// Retained is exercising a refusal somebody decided on; a holder that
	// cannot complete its work returns an error instead.
	Retained Outcome = "retained"
)

// Result is one holder's answer.
type Result struct {
	// Holder names who answered. It is usually a module name, and deliberately
	// not typed as one: personal data also sits in stores that are not modules.
	Holder string
	// Outcome is what the holder did.
	Outcome Outcome
	// Rows is how many rows the holder touched. It is informational; zero with
	// a Deleted or Anonymized outcome means the subject had nothing here, which
	// is a normal answer and not an error.
	Rows int
	// Kept names what may still hold the person, as "table.column" entries.
	//
	// It is REQUIRED with Retained WHENEVER THE HOLDER ACTUALLY HOLDS
	// SOMETHING — that is, whenever Rows is non-zero — because a refusal that
	// cannot say what it kept is not something a controller can pass on.
	//
	// The exception is the case a holder answering Retained on POLICY runs
	// into: an invoice module refuses erasure for every document it has, and
	// for a person who never bought anything it has none. Listing the buyer
	// columns there would tell the controller that somebody is in places they
	// have never been, and the controller would repeat it. So Rows zero with an
	// empty Kept is the honest shape for "nothing of this person is here, and
	// had there been any it would have been kept".
	//
	// It is also EXPECTED with
	// Anonymized, and that is the less obvious half: gobit never rewrites a
	// free-form column, because ADR 0029 leaves the judgement of whether a
	// metadata blob holds personal data with the controller. A holder that
	// anonymized its named columns and left an untouched jsonb beside them has
	// to say so, or the word Anonymized covers a field nobody looked at.
	Kept []string
	// Why explains what Kept lists, in one sentence addressed to whoever has to
	// answer the data subject. It is required whenever Kept is non-empty.
	Why string
}

// Report is the whole answer to one erasure request.
//
// It is a value rather than a stream because the caller is a controller
// answering a person, and an answer that arrives in pieces over an unspecified
// period is not an answer.
type Report struct {
	// Subject is the person the request was about.
	Subject Subject
	// Results is one entry per holder that was asked, in the order they were
	// asked. A holder that was asked and had nothing still appears: silence and
	// absence are the two things this report exists to tell apart.
	Results []Result
	// At is when the sweep ran, in UTC.
	At time.Time
}

// Kind says how sure gobit is that a declared place holds personal data.
type Kind string

const (
	// Named marks a column gobit itself writes a person into. The framework
	// knows what is in it because the framework put it there.
	Named Kind = "named"
	// Open marks a column whose content the embedder controls — a metadata
	// jsonb, a free-text description, a note somebody types.
	//
	// gobit does not inspect these and does not guess. ADR 0029 places the
	// judgement of whether a given field is personal data in a given deployment
	// with the controller, and a framework that read customers' free text in
	// order to classify it would have taken exactly that judgement back.
	Open Kind = "open"
)

// Holding is one declared place a holder keeps personal data.
type Holding struct {
	// Table is the table the column lives in.
	Table string
	// Column is the column.
	Column string
	// Kind says whether gobit wrote the person in there or the embedder might
	// have.
	Kind Kind
	// Why says what the column holds about the person, in the words somebody
	// answering a data-subject request would use.
	Why string
}

// Declaration is what one holder says it keeps about people.
//
// It is the third of ADR 0029's obligations and the one that makes the other
// two honest: without it an embedder cannot answer a data-subject request even
// in principle, because nothing tells it where to look.
type Declaration struct {
	// Holder names who is declaring, matching [Result.Holder].
	//
	// It is OPTIONAL and a module may leave it empty: the sweep overwrites it
	// with the name the registry knows, because a declaration attributed to the
	// wrong holder is worse than one with a blank name. Setting it is still
	// useful — a caller holding the module directly, outside any sweep, gets a
	// name — so both are correct and neither is a defect.
	Holder string
	// Holdings is every place this holder keeps personal data. An empty slice
	// is a real declaration and means "none" — the notification module's
	// delivery log deliberately has no recipient column, and saying so is worth
	// more than saying nothing.
	Holdings []Holding
}

// Eraser is the optional capability of answering an erasure request.
//
// It is found by type assertion, the way this repository already finds
// provider.SessionInspector, so a holder that has no personal data implements
// nothing and costs nothing. The interface carries ONE method deliberately: a
// method set is what a new major version costs, while the structs above can
// grow fields without breaking anybody.
//
// Erase must be idempotent. A controller who runs a sweep twice — because the
// first report was mislaid, or because a second request arrived — must get the
// same outcome rather than an error, and a holder that already anonymized its
// rows answers [Anonymized] again rather than pretending it found nothing.
type Eraser interface {
	Erase(ctx context.Context, s Subject) (Result, error)
}

// Declarer is the optional capability of saying what personal data is held.
//
// It is separate from [Eraser] and stays separate on purpose. Some holders can
// declare but cannot erase: the review module stores the byline an author typed
// and nothing that could identify which person that is, so it can say what it
// keeps and cannot resolve a subject. Folding the two into one interface would
// force such a holder either to lie or to stay silent.
//
// PersonalData takes no context and returns no error because a declaration is a
// property of the code, not of the data: it is the same sentence on an empty
// database and a full one, and an audit reads it without a connection.
type Declarer interface {
	PersonalData() Declaration
}

// State is what a holder was able to say when asked what it holds about
// somebody.
//
// There are three because two cannot tell the two kinds of empty apart, which
// is the same argument [Outcome] rests on. A holder that searched and found
// nothing has told the controller something true and useful; a holder that
// cannot search at all has not, and reporting both as an empty list would let
// the second hide inside the first.
type State string

const (
	// Disclosed means the holder searched and is handing over what it found.
	// A Disclosed state with no records is not possible — that is [Nothing].
	Disclosed State = "disclosed"
	// Nothing means the holder searched and this person is not in it.
	Nothing State = "nothing"
	// Unresolvable means the holder keeps personal data and cannot tell WHOSE.
	//
	// It is the state that keeps a dossier honest. The review module stores the
	// byline an author typed and deliberately nothing that says which person
	// that is, so it cannot answer for anybody — and a dossier that simply left
	// it out would read as "you have written no reviews", which nobody checked.
	// A holder that has not YET been given a search path says the same thing,
	// so the gap is visible in the document rather than in a backlog.
	Unresolvable State = "unresolvable"
)

// Field is one personal value on one record.
type Field struct {
	// Column is the column it came from, matching a declared [Holding].
	Column string
	// Kind repeats the declaration's judgement: Named where gobit wrote the
	// person there itself, Open where the content is the embedder's.
	//
	// It travels WITH the value rather than being looked up, and that is the
	// point: an Open value is one gobit never inspects, so whoever reads the
	// dossier has to know which of these the framework can vouch for. A parallel
	// map keyed by column would answer the same question and would be able to
	// drift from the values it describes.
	Kind Kind
	// Value is what is stored. It is any because a column may hold a string, a
	// number, a time or a whole jsonb document.
	Value any
}

// Record is one row a holder found about the subject.
type Record struct {
	// Table is the table the row lives in.
	Table string
	// ID identifies the row when the holder can name it, and is empty when it
	// cannot. It is not required: a dossier is read by a person, and a row
	// without an identifier is still that person's data.
	ID string
	// Fields are the personal values on the row. A holder lists the columns it
	// DECLARED and no others — a disclosure that reached past the declaration
	// would be answering with data the declaration told the controller was not
	// there.
	Fields []Field
}

// Disclosure is one holder's answer to "what do you hold about this person".
type Disclosure struct {
	// Holder names who answered, matching [Result.Holder].
	Holder string
	// State says whether the holder could look, and what it found.
	State State
	// Records are what it found. Empty unless the state is [Disclosed].
	Records []Record
	// Why explains a state that is not [Disclosed], in the words somebody
	// answering the person would use. It is REQUIRED for [Unresolvable] and for
	// [Nothing]: "we found nothing" is an answer a person may query, and the
	// sentence that says WHERE it was looked for is what makes it checkable.
	Why string
}

// Dossier is the whole answer to one disclosure request.
//
// It is deliberately not called an export. What comes back is organized and
// complete about the columns gobit declared, and it still contains free-form
// values the framework never inspects — so it is material a controller reviews
// before sending, not a document that can be forwarded unread. Naming it an
// export would promise the second.
type Dossier struct {
	// Subject is the person the request was about.
	Subject Subject
	// Parts is one entry per holder that was asked, in the order they were
	// asked. A holder that was asked and found nothing still appears.
	Parts []Disclosure
	// At is when the request ran, in UTC.
	At time.Time
}

// Discloser is the optional capability of showing what is held about somebody.
//
// It is separate from [Eraser] for a reason this repository can point at: five
// holders can already resolve a person, and every one of those resolutions is
// wired to a destructive verb. gobit will remove this person's rows on request
// and, without this interface, will not show them to her — which is a fact
// about the code rather than a policy the embedder chose.
//
// A holder that implements [Declarer] and not this one is NOT silently absent
// from a dossier: the coordinator enters it as [Unresolvable] with what it
// declared, so the gap is written into the document a person receives.
type Discloser interface {
	PersonalDataOf(ctx context.Context, s Subject) (Disclosure, error)
}
