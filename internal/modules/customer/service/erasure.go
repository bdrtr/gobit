package service

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// ErasureHolder is the name customer answers an erasure request under.
//
// It must be the MODULE name, because that is what a controller reads off the
// report when it has to say which store still holds something. The constant
// lives here rather than being taken from the module package because the
// service cannot import its own module (that would be a cycle), and it is
// deliberately NOT [Entity]: the entity name is the Query layer's handle and
// happens to spell the same word today, so tying the two together would make a
// rename of one silently rewrite the other. The two names are held equal by a
// test against the real module (see TestTheHolderIsTheModuleName).
const ErasureHolder = "customer"

// CodeErasureSubjectEmpty reports an erasure request that named nobody.
const CodeErasureSubjectEmpty = "customer_erasure_subject_empty"

// erasureKept names what stays behind after this module anonymizes a person,
// as "table.column" entries for [erasure.Result.Kept].
//
// Every entry is a deliberate refusal rather than an oversight:
//
//   - customer.metadata is a free-form jsonb the embedding application writes.
//     gobit never rewrites such a column, because ADR 0029 leaves the judgement
//     of whether a given blob holds personal data with the controller, and a
//     framework that read customers' metadata in order to classify it would
//     have taken that judgement back. Saying so is what keeps the word
//     "anonymized" honest: the alternative is a report that covers a field
//     nobody looked at.
//   - customer_group.metadata is the same kind of column on the segment rather
//     than on the person. A group is "wholesalers" and not anybody, so nothing
//     here is erased per subject — but the blob is written by the same hand
//     under the same rule, and a controller auditing one has to be told about
//     the other. It is listed for exactly the reason the customer one is: an
//     open column that no sweep touches is a column no report may stay silent
//     about.
//   - customer_address.country_code names a JURISDICTION rather than a person.
//     Tax and retention rules are read from it, a two-letter code points at
//     tens of millions of people, and customer_address_country_check would
//     refuse an empty value anyway.
//
// The list is REPORTED even when the subject turned out to have no rows here.
// Kept describes what this holder refuses to touch, which is a property of the
// module and not of one person's data; making it conditional would mean two
// runs of the same sweep produced two different descriptions of gobit.
var erasureKept = []string{
	"customer.metadata",
	"customer_group.metadata",
	"customer_address.country_code",
}

// erasureWhy is the sentence a controller repeats to the data subject about
// [erasureKept]. It is one sentence because that is what an answer to a person
// has room for.
const erasureWhy = "The metadata on a customer and on a customer group is free-form and gobit " +
	"never rewrites it — whether it holds personal data is the controller's judgement (ADR 0029) — " +
	"and the address country code is kept because it names a jurisdiction rather than a person."

// Erase anonymizes the person's customer records; it never deletes them.
//
// # Why anonymized and not deleted
//
// The row cannot go. Carts and orders carry the customer id as a bare TEXT
// column with NO foreign key — there are no cross-module foreign keys
// (Principle 2.2) — so deleting the customer row would leave those references
// pointing at nothing, silently, with no constraint anywhere to notice. The
// honest answer is therefore [erasure.Anonymized]: the row stays, the id stays,
// and everything the row said about the person is overwritten.
//
// # Why this is not DeleteCustomer
//
// [Service.DeleteCustomer] is a SOFT delete: it writes deleted_at and
// updated_at and clears not one personal column. That is bookkeeping — a shop
// taking a record out of its lists — and an erasure is a legal answer to a
// person. They are kept apart in both directions: deleting a customer does not
// erase them, and erasing a customer does not delete them. That is also why
// this path reaches ALREADY DELETED rows; their e-mail, name and phone are
// still sitting in the table (see queries/customer.sql,
// LockCustomerForErasure).
//
// # Resolving the subject
//
// [erasure.Subject] requires no particular identifier and this module uses
// EVERY one it is given. A subject carrying both an id and an e-mail resolves
// to the union of what each reaches, not to the id alone: a registered customer
// who has also checked out as a guest under the same address has one row under
// their id and further rows under their e-mail, the admin endpoint passes both
// fields straight through, and preferring the id would leave the guest rows
// fully personal. The e-mail deliberately reaches SEVERAL rows, since any
// number of guest records can share one address and all of them are the same
// person. A subject carrying neither is refused: erasing "everyone" is not an
// erasure request.
//
// A subject that matches nothing returns Anonymized with Rows 0 and no error.
// The sweep asks every holder about the same person and most holders have never
// seen them; reporting that as a failure would make an ordinary sweep look
// broken.
//
// # Idempotence
//
// A second call answers Anonymized again. It writes nothing when there is
// nothing left to write, so Rows comes back 0 — and that is decided by
// comparing the stored columns with what the erasure would put in them, inside
// the UPDATE itself, never by reading the e-mail as a flag. The difference is
// not academic: an anonymized record stays LIVE and UpdateCustomer is a
// per-column patch, so an admin or the storefront can write a fresh first name
// and phone onto an erased row without touching its e-mail, and a second pass
// that trusted the e-mail would skip that row and report a person's name as
// anonymized. Rows is the receipt for THIS call; the outcome, not the count, is
// the answer.
//
// One asymmetry is worth knowing before it surprises somebody: a second call
// made with the E-MAIL cannot find the rows at all, because the address that
// pointed at them is exactly what the first call destroyed. It still answers
// Anonymized, and it must — the alternative is a holder that reports a person
// as unknown moments after erasing them.
func (s *Service) Erase(ctx context.Context, subject erasure.Subject) (erasure.Result, error) {
	if err := s.ready(); err != nil {
		return erasure.Result{}, err
	}

	customerID := strings.TrimSpace(subject.CustomerID)
	// The e-mail is normalized rather than validated. A subject carrying a
	// malformed address is not a bad request here, it is an address this module
	// stores nothing under; validating it would turn "nothing to erase" into an
	// error in the middle of somebody else's sweep.
	email := models.NormalizeEmail(subject.Email)

	if customerID == "" && email == "" {
		return erasure.Result{}, errors.Invalid(CodeErasureSubjectEmpty,
			"an erasure request must name a customer id or an e-mail address")
	}

	count, err := s.repo.AnonymizeCustomers(ctx, customerID, email, s.clock())
	if err != nil {
		return erasure.Result{}, err
	}

	// Neither the e-mail nor the id is enough to identify the person in a log
	// line, and the e-mail is sensitive data that is not logged at all (plan
	// Section 8). What is worth recording is that the erasure ran and how much
	// it touched, which is what an audit asks afterwards.
	//
	// Both handles are recorded, not one: they are not alternatives, and a
	// single "by_customer_id" flag would hide the case this module has to get
	// right — a subject carrying both, whose rows come from the id AND from the
	// address.
	s.log.InfoContext(ctx, "customer records anonymized",
		slog.Bool("by_customer_id", customerID != ""),
		slog.Bool("by_email", email != ""),
		slog.Int("matched", count.Matched),
		slog.Int("rewritten", count.Rewritten),
	)

	return erasure.Result{
		Holder:  ErasureHolder,
		Outcome: erasure.Anonymized,
		Rows:    count.Rewritten,
		// The slice is copied because the caller receives it: a report handed
		// out is read by whoever answers the data subject, and a caller sorting
		// or appending to it would edit this module's declaration of what it
		// refuses to touch.
		Kept: slices.Clone(erasureKept),
		Why:  erasureWhy,
	}, nil
}
