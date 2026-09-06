package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/personaldata"
)

// Holder is the name this module answers an erasure request under.
//
// It is declared here rather than reused from the module package because the
// module package imports this one: naming it there and reading it here would be
// an import cycle. The two are pinned to each other by a test rather than by a
// comment (internal/modules/invoice/erasure_test.go), because a holder name
// that drifts from the module name produces a report in which one holder is
// missing and another one nobody registered has answered — and both halves of
// that look fine on their own.
const Holder = "invoice"

// buyerColumns are the columns this module writes a BUYER into.
//
// Every one of them is a printed field of a document, copied at the moment the
// invoice was issued (see migration 000001) and never updated afterwards.
var buyerColumns = []string{
	"invoices.buyer_name",
	"invoices.buyer_tax_number",
	"invoices.buyer_tax_office",
	"invoices.buyer_email",
	"invoices.buyer_address",
	"invoices.buyer_country_code",
}

// openColumns are the columns whose content the EMBEDDER controls.
//
// gobit never inspects them and never rewrites them. ADR 0029 places the
// judgement of whether a given free-form field holds personal data in a given
// deployment with the controller, and a framework that read a merchant's
// customer notes in order to classify them would have taken exactly that
// judgement back. status_reason is in this list and not in [buyerColumns] for
// the same reason: the module writes it, but what it writes is a sentence an
// operator typed when they canceled or re-sent the document.
var openColumns = []string{
	"invoices.status_reason",
	"invoices.metadata",
	"invoice_lines.description",
}

// whyRetained is the sentence a controller repeats to a data subject.
//
// It says both things the contract owes: WHY the refusal stands, and what the
// free-form half of [personaldata.Result.Kept] means. The second half is the one
// that is easy to leave out and the one that keeps the report honest — a list
// of columns with no note that three of them were never looked at would let a
// reader assume gobit knows what is in them.
const whyRetained = "an issued invoice is a legal document that gobit keeps rather than erases " +
	"(ADR 0032, enforced by the database itself), so the buyer columns listed here still name " +
	"this person; invoices.status_reason, invoices.metadata and invoice_lines.description are " +
	"free-form fields that gobit never inspects and never rewrites, so whatever was written into " +
	"them is retained under the same refusal and the controller answers for it."

// whyNothingHere is the sentence when the address was looked for and found in
// no document.
//
// It is the third sentence and the one that was missing. [whyRetained] says
// "the buyer columns listed here still name this person", which is FALSE of
// somebody who never bought anything, and it was being handed out with a count
// of zero and all nine columns beside it — a well-formed refusal about a person
// who is not in this table at all. The outcome stays [personaldata.Retained] and the
// sentence says why: Retained is what this module DOES, not what it found.
const whyNothingHere = "the address this request carried was looked for in every document's " +
	"buyer address and matched none, so this module holds nothing about this person and there " +
	"is nothing here to erase; the outcome is still RETAINED because it states this module's " +
	"answer rather than its findings — an issued invoice is a legal document that gobit keeps " +
	"rather than erases (ADR 0032, enforced by the database itself), so a document issued to " +
	"this address tomorrow would be kept in the same way."

// whyUnresolved is the sentence when the request carried no address to look for.
//
// It reports a refusal AND an admission, and keeping them in one sentence is
// deliberate: a controller who reads only "retained" would think a count of
// zero meant this person has no invoices, when it means nobody looked.
const whyUnresolved = "the only handle an invoice has on a person is the buyer address printed " +
	"on it — this table carries no customer id and no order id — and the request named no " +
	"address, so no document was counted; any invoice that does name this person is retained " +
	"all the same, as a legal document gobit keeps rather than erases (ADR 0032), including the " +
	"free-form invoices.status_reason, invoices.metadata and invoice_lines.description that " +
	"gobit never inspects."

// Erase answers an erasure request about a person: it keeps the documents.
//
// This is the whole of ADR 0032 expressed in the [personaldata.Eraser] contract, and
// the answer is the same for every subject and every database: [personaldata.Retained].
// The reason is [Service.Issue]'s reason turned around — within a series the
// numbers run without a gap, a deleted document is a gap, and a tax authority
// reading one sees a document that was issued and then made to disappear. The
// module also has no way to redact the person in place: an issued document has
// no update path for its parties, and rewriting a buyer would give up the
// property that makes an invoice evidence at all.
//
// # What this method does and does not do
//
// It reads. It issues no DELETE and no UPDATE, and it could not succeed if it
// did: migration 000002 puts a BEFORE DELETE trigger on both tables and the
// database refuses the statement whatever the module intends. That makes
// idempotency a property rather than a promise — a second call, a tenth call
// and a call against an empty database all return the same outcome, because
// nothing about the storage has changed between them.
//
// # Why the count is worth taking at all
//
// The outcome does not depend on it, so a fixed Retained with no query would
// have been cheaper. It is taken because [personaldata.Result.Rows] is what a
// controller puts in front of a data subject, and "we are keeping documents
// about you" reads very differently from "we are keeping the four invoices
// issued to you". A refusal that cannot say how much it kept is the refusal
// ADR 0029 wrote [personaldata.Retained] to avoid.
//
// Rows counts INVOICES and not invoice lines. A line is a part of a document
// rather than a separate place the person is held, and a subject told that 37
// rows are retained about them, where 33 are the item descriptions of four
// invoices, has been given a number that sounds like an answer and is not.
//
// # Three answers, not one, and only one of them lists columns
//
// The OUTCOME is [personaldata.Retained] in all three cases, because the outcome is
// this module's policy and not a summary of what the query found. Answering
// [personaldata.Deleted] with zero rows would be the contract's own idiom for
// "nothing here" and it is refused anyway: read by a controller it says the
// invoice module deletes on request, which is the single thing about this
// module that must never be believed. What varies is [personaldata.Result.Kept] and
// [personaldata.Result.Why]:
//
//   - documents were found — Kept lists the buyer columns and the free-form
//     ones, and Why is the refusal ([whyRetained]).
//   - no address was given — Kept lists them too, because invoices may well
//     name this person under an address the request did not carry, and Why
//     says nobody looked ([whyUnresolved]).
//   - the address matched nothing — Kept is EMPTY and Why says so
//     ([whyNothingHere]).
//
// The empty Kept is deliberate and is worth defending, because [personaldata.Result]
// calls Kept required with Retained. That requirement is there to stop a
// refusal that cannot say what it kept. This refusal kept nothing OF THIS
// PERSON: naming nine columns that hold nobody in order to satisfy the rule
// would be the dishonest way to obey a rule written to force honesty, and the
// sentence in Why is what a controller repeats instead.
func (s *Service) Erase(ctx context.Context, subject personaldata.Subject) (personaldata.Result, error) {
	result := personaldata.Result{
		Holder:  Holder,
		Outcome: personaldata.Retained,
	}

	email := strings.TrimSpace(subject.Email)
	if email == "" {
		// Not an error, and deliberately not a zero-row "nothing here" either.
		// The subject may well have invoices under an address this request did
		// not carry, and buyer_email defaults to the empty string in 000001 —
		// so looking for "" would match every document that never recorded one
		// and report them all as this person's. Kept lists every column for
		// that same reason: unlike the branch below, nothing here rules out a
		// document naming this person.
		result.Kept = keptColumns()
		result.Why = whyUnresolved

		return result, nil
	}

	count, err := s.repo.CountInvoicesByBuyerEmail(ctx, email)
	if err != nil {
		// A holder that cannot complete its work returns an error rather than
		// an outcome (see [personaldata.Eraser]). Answering Retained with a count of
		// zero here would be the worst available answer: it is the shape of a
		// real reply, and the controller would repeat it.
		return personaldata.Result{}, err
	}

	result.Rows = int(count)

	if count == 0 {
		// The address was resolvable and resolved to nothing. Kept stays empty
		// here and only here: it names what may still hold THIS person, and a
		// column of a table holding no row of theirs holds them nowhere. The
		// distinction this makes is the same one [whyUnresolved] makes one
		// branch up — "nobody looked" against "we looked and you are not
		// here" — and both are answers a controller can repeat without
		// telling somebody their invoices are being kept when they have none.
		result.Why = whyNothingHere

		return result, nil
	}

	result.Kept = keptColumns()
	result.Why = whyRetained

	return result, nil
}

// keptColumns returns the "table.column" entries a retained invoice keeps.
//
// A fresh slice each time, because [personaldata.Result] is handed to a caller this
// package knows nothing about and a shared backing array would let one
// report's Kept be reordered by whoever sorted another one.
func keptColumns() []string {
	kept := make([]string, 0, len(buyerColumns)+len(openColumns))
	kept = append(kept, buyerColumns...)
	kept = append(kept, openColumns...)

	return kept
}

// RetainedColumns returns the same entries [Service.Erase] reports as kept.
//
// It exists so that the module's declaration and its refusal can be checked
// against each other. A column named in a Retained result that the declaration
// never mentions is the failure ADR 0029 cares most about: the declaration is
// what an embedder reads to find out where to look, and a place the refusal
// admits to keeping but the declaration omits is a place nobody will ever
// audit.
func RetainedColumns() []string { return keptColumns() }
