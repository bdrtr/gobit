package service

import (
	"context"
	"fmt"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// This file is the cart module's answer to "what do you hold about this
// person" (ADR 0029, ADR 0033, core/personaldata).
//
// # Why the module owes a read at all
//
// The module could already resolve a person — by customer id and by e-mail —
// and until this file that resolution was wired to exactly one verb: erase.
// gobit would remove this shopper's address on request and would not show it to
// her. That asymmetry was a fact about the code rather than a policy anybody
// chose, and it is what [personaldata.Discloser] exists to close. The
// resolution is deliberately the SAME one the erasure uses, down to the WHERE
// clause: a module that could find more rows to delete than to show would have
// reopened the gap in a smaller form.
//
// # It reads, and it must be provably unable to do anything else
//
// No statement here writes, none takes a lock, and the whole answer is
// assembled outside any transaction (the argument is in repository/disclosure.go).
// A disclosure is triggered by an administrator answering a person, at an
// arbitrary moment, possibly twice; it may not change the data it is describing
// and it may not queue behind a checkout that is holding a cart's row lock.
//
// # The columns are the DECLARATION's, and not a list retyped here
//
// [personaldata.Record] may carry only the columns the module declared, because
// a disclosure that reached past the declaration would hand somebody data the
// declaration told the controller was not there — and one that fell short would
// promise a column and not produce it. The rule is enforced in three places
// that would each fail alone: the SELECT lists name exactly the declared
// columns (queries/disclosure.sql), the field lists are BUILT by walking
// [personalColumns] rather than written out, and a declared column with no
// reader is a loud error rather than a silent omission
// ([CodeDisclosureColumnUnreadable]).
//
// # What the answer says when the person is not here
//
// [personaldata.Nothing], with a sentence naming where it looked — not
// [personaldata.Disclosed] with an empty list, which cannot be told apart from
// a holder that never managed to search. [personaldata.Unresolvable] is never
// this module's answer: it holds no personal data it cannot attribute, since
// every column it declares hangs off a cart and a cart carries at least one of
// the two handles the moment it holds anything at all.

// MaxDisclosedCarts is the largest number of carts one disclosure carries.
//
// # Why a bound exists here and not in the erasure
//
// The erasure walks every cart of the person because leaving one behind means
// leaving a name in the database; there is no honest way to stop early. A
// disclosure produces a DOCUMENT, in one value, assembled in memory, that a
// controller reads before sending it on — and a cart is opened per shopping
// session rather than per purchase, so a shop accumulates far more carts than
// orders and a regular shopper of several years can hold hundreds of them. Left
// unbounded, the size of one holder's part of a person's dossier would be
// decided by how often that person shopped.
//
// # Why 200
//
// It is far above what a real shopper accumulates — carts are created by
// checkout attempts, and a person with two hundred of them is a rarity or a
// script — and low enough that the worst case is a document somebody can still
// read. The number is not a retention rule and nothing is deleted at it: the
// carts beyond the bound are still held, and the answer says so.
//
// # Why the truncation is never silent
//
// A short dossier that does not say it is short is worse than a big one,
// because the person cannot tell an omission from an absence — she would read
// "these are your carts" and conclude the rest were gone. So the count of what
// matched is read in the SAME statement as the rows (see
// queries/disclosure.sql), and when it exceeds what was listed the disclosure's
// Why states how many carts are held, how many are listed, and the moment
// everything older than is not in the document.
const MaxDisclosedCarts int64 = 200

// CodeDisclosureSubjectEmpty reports that the disclosure request named nobody.
//
// It is a SEPARATE code from [CodeErasureSubjectEmpty] although the check is
// the same one: the code is what an operator reads in a log and what a client
// branches on, and telling somebody their ERASURE request was empty when they
// asked to see their data would send them looking at the wrong request.
const CodeDisclosureSubjectEmpty = "cart_disclosure_subject_empty"

// CodeDisclosureColumnUnreadable reports that the module declares a personal
// column the disclosure cannot produce a value for.
//
// It exists because the alternative to an error is silence. The field lists are
// built by walking the declaration, so a column added to [personalColumns] and
// forgotten in the readers below would otherwise be skipped — and the person
// would receive a document that looks complete and is short of exactly the
// column somebody had just decided was personal. Failing the whole request is
// the lesser harm: a controller sees a fault and asks again, rather than
// forwarding an answer that is quietly wrong. A unit test walks the same list
// against a fully populated cart so that the failure lands in CI rather than in
// front of a data subject.
const CodeDisclosureColumnUnreadable = "cart_disclosure_column_unreadable"

// PersonalDataOf shows what this module holds about one person (ADR 0033).
//
// # What it returns
//
// One [personaldata.Record] per ROW, in the order a person reads a session:
// the cart, then its addresses, then the lines and the shipping methods that
// carry a note. The carts come newest first and the children follow their own
// cart, so the document is a sequence of shopping sessions rather than four
// tables printed one after another.
//
// A cart record is produced for every cart found, even in the rare case where
// the only column left on it is the handle it was found by; the cart's
// existence is itself a fact about the person. A CHILD record is produced only
// when it still holds something — after an erasure an address row survives with
// its country and nothing else, and a record whose every declared column is
// empty would tell the reader a row exists and say nothing about them.
//
// # Both handles, not either
//
// A guest cart carries an e-mail and no customer id, a cart opened for a
// signed-in shopper may carry the id and no e-mail, and the same person often
// has both. When the request carries only one of the two the answer says so:
// what could not be searched for is written into the document rather than left
// for the person to discover.
//
// # It reaches the rows every other read hides
//
// Soft-deleted and completed carts are included, for the reason the erasure
// includes them: the question is what the database still HOLDS. A cart hidden
// from every storefront read holds an address exactly as a live one does, and
// answering "you have nothing" about a row the installation is still storing is
// the failure this whole mechanism exists to prevent.
//
// # The subject is never logged
//
// The log line carries counts and the state and no identifier of the person, as
// the erasure's does. A disclosure is the one request whose whole content is
// somebody's personal data, and copying it into the log would spread it into a
// place no erasure reaches.
func (s *Service) PersonalDataOf(
	ctx context.Context, subject personaldata.Subject,
) (personaldata.Disclosure, error) {
	customerID, email, err := normalizeSubject(subject, CodeDisclosureSubjectEmpty, "a disclosure request")
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	carts, total, err := s.store.CartsForDisclosure(ctx, customerID, email, MaxDisclosedCarts)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	if len(carts) == 0 {
		s.log.InfoContext(ctx, "a personal data disclosure was answered",
			"holder", ErasureHolder, "state", string(personaldata.Nothing), "carts", 0, "records", 0)

		return personaldata.Disclosure{
			Holder: ErasureHolder,
			State:  personaldata.Nothing,
			Why:    whyNothingHere(customerID, email),
		}, nil
	}

	ids := make([]string, 0, len(carts))
	for i := range carts {
		ids = append(ids, carts[i].ID)
	}

	addresses, err := s.store.CartAddressesForDisclosure(ctx, ids)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	lineNotes, err := s.store.CartLineItemNotesForDisclosure(ctx, ids)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	shippingNotes, err := s.store.CartShippingNotesForDisclosure(ctx, ids)
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	records, err := disclosureRecords(carts, addresses, lineNotes, shippingNotes)
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	oldest := carts[len(carts)-1].CreatedAt
	s.log.InfoContext(ctx, "a personal data disclosure was answered",
		"holder", ErasureHolder, "state", string(personaldata.Disclosed),
		"carts", len(carts), "carts_held", total, "records", len(records))

	return personaldata.Disclosure{
		Holder:  ErasureHolder,
		State:   personaldata.Disclosed,
		Records: records,
		Why:     whyDisclosed(customerID, email, total, int64(len(carts)), oldest),
	}, nil
}

// disclosureRecords lays the rows out as the document reads.
//
// It is split out of [Service.PersonalDataOf] because it is the part with no
// database in it: given four slices, the shape of the answer is decided by
// rules, and rules that can be read without a connection are rules a test can
// hold to account.
//
// The children are grouped by cart id ONCE rather than searched per cart. The
// alternative is a scan of every address for every cart, which at the bound
// above is two hundred passes over the same slice — the same N+1 shape this
// repository bans at the query level, moved into memory where nobody would see
// it.
func disclosureRecords(
	carts []models.PersonalCart,
	addresses []models.PersonalAddress,
	lineNotes, shippingNotes []models.PersonalNote,
) ([]personaldata.Record, error) {
	addressesByCart := groupByCart(addresses, func(a models.PersonalAddress) string { return a.CartID })
	lineNotesByCart := groupByCart(lineNotes, func(n models.PersonalNote) string { return n.CartID })
	shippingByCart := groupByCart(shippingNotes, func(n models.PersonalNote) string { return n.CartID })

	records := make([]personaldata.Record, 0, len(carts)+len(addresses)+len(lineNotes)+len(shippingNotes))
	for i := range carts {
		fields, err := cartFields(carts[i])
		if err != nil {
			return nil, err
		}
		records = append(records, personaldata.Record{
			Table: tableCarts, ID: carts[i].ID, Fields: fields,
		})

		// The address loop is walked by INDEX for the reason the repository's
		// conversions are: an address row is a couple of hundred bytes of
		// strings and copying one per iteration buys nothing. The two note
		// loops below are two strings and a map reference, so they are not
		// worth the same contortion.
		cartAddresses := addressesByCart[carts[i].ID]
		for j := range cartAddresses {
			fields, err := addressFields(cartAddresses[j])
			if err != nil {
				return nil, err
			}
			if len(fields) == 0 {
				continue
			}
			records = append(records, personaldata.Record{
				Table: tableCartAddresses, ID: cartAddresses[j].ID, Fields: fields,
			})
		}

		for _, note := range lineNotesByCart[carts[i].ID] {
			record, err := noteRecord(tableCartLineItems, columnMetadata, note)
			if err != nil {
				return nil, err
			}
			if len(record.Fields) == 0 {
				continue
			}
			records = append(records, record)
		}

		for _, note := range shippingByCart[carts[i].ID] {
			record, err := noteRecord(tableCartShippingMethods, columnShippingData, note)
			if err != nil {
				return nil, err
			}
			if len(record.Fields) == 0 {
				continue
			}
			records = append(records, record)
		}
	}

	return records, nil
}

// groupByCart indexes rows by the cart they hang off, keeping their order.
func groupByCart[T any](rows []T, cartID func(T) string) map[string][]T {
	out := make(map[string][]T, len(rows))
	for i := range rows {
		key := cartID(rows[i])
		out[key] = append(out[key], rows[i])
	}

	return out
}

// declaredFields builds the fields of one row by WALKING the declaration.
//
// This is the mechanism that keeps a disclosure and a declaration from
// drifting, and it only works in one direction if it is written this way round:
// the loop is over [personalColumns], so a column the module declares is a
// column the row is asked for, and the value function decides only what the
// value IS. Written the other way — a list of fields per row type, checked
// against the declaration by a test — the two lists would agree on the day they
// were written and the test would be the only thing standing between them
// afterwards.
//
// [personaldata.Field.Kind] is copied from the same declaration row as the
// column name, so the judgement traveling with a value is the judgement the
// module published about that column, and the two cannot come apart.
//
// A column with no value is left OUT rather than carried as an empty one. NULL
// is what most of these columns hold most of the time — a guest cart has no
// customer id, an abandoned cart has no address, a metadata column is '{}' by
// default — and a document listing every one of them as empty would bury the
// values that are there. The declaration, which the same request can be asked
// for, is where somebody learns that the column exists at all.
func declaredFields(table string, value func(column string) (any, bool, error)) ([]personaldata.Field, error) {
	fields := make([]personaldata.Field, 0, len(personalColumns))
	for i := range personalColumns {
		holding := personalColumns[i].holding
		if holding.Table != table {
			continue
		}

		v, ok, err := value(holding.Column)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		fields = append(fields, personaldata.Field{Column: holding.Column, Kind: holding.Kind, Value: v})
	}

	return fields, nil
}

// cartFields builds the declared fields of one carts row.
func cartFields(cart models.PersonalCart) ([]personaldata.Field, error) {
	return declaredFields(tableCarts, func(column string) (any, bool, error) {
		switch column {
		case columnCustomerID:
			return textField(cart.CustomerID)
		case columnEmail:
			return textField(cart.Email)
		case columnMetadata:
			return openField(cart.Metadata)
		default:
			return nil, false, unreadableColumn(tableCarts, column)
		}
	})
}

// addressFields builds the declared fields of one cart_addresses row.
func addressFields(address models.PersonalAddress) ([]personaldata.Field, error) {
	return declaredFields(tableCartAddresses, func(column string) (any, bool, error) {
		switch column {
		case columnSourceAddressID:
			return textField(address.SourceAddressID)
		case columnFirstName:
			return textField(address.FirstName)
		case columnLastName:
			return textField(address.LastName)
		case columnCompany:
			return textField(address.Company)
		case columnAddress1:
			return textField(address.Address1)
		case columnAddress2:
			return textField(address.Address2)
		case columnCity:
			return textField(address.City)
		case columnProvince:
			return textField(address.Province)
		case columnPostalCode:
			return textField(address.PostalCode)
		case columnCountryCode:
			return textField(address.CountryCode)
		case columnPhone:
			return textField(address.Phone)
		case columnMetadata:
			return openField(address.Metadata)
		default:
			return nil, false, unreadableColumn(tableCartAddresses, column)
		}
	})
}

// noteRecord builds the record of a row whose only declared column is a
// free-form one.
//
// The column's NAME is a parameter because the two tables call theirs
// differently — cart_line_items.metadata and cart_shipping_methods.data — and
// the caller takes it from the same constants the declaration is written with.
// Guessing it from the table here would put a second spelling of a column name
// in the module.
func noteRecord(table, column string, note models.PersonalNote) (personaldata.Record, error) {
	fields, err := declaredFields(table, func(declared string) (any, bool, error) {
		if declared != column {
			return nil, false, unreadableColumn(table, declared)
		}

		return openField(note.Data)
	})
	if err != nil {
		return personaldata.Record{}, err
	}

	return personaldata.Record{Table: table, ID: note.ID, Fields: fields}, nil
}

// textField carries a text column, or reports that it holds nothing.
func textField(value string) (field any, held bool, err error) {
	if value == "" {
		return nil, false, nil
	}

	return value, true, nil
}

// openField carries a free-form column, or reports that it holds nothing.
//
// The value is handed over EXACTLY as it is stored and is not inspected: ADR
// 0029 leaves the judgement of whether such a document holds personal data with
// the controller, and a framework that read it in order to decide what to
// disclose would have taken that judgement back in the reading direction after
// refusing it in the writing one. What "holds nothing" means here is therefore
// structural and not semantic — an absent or empty document, which the storage
// layer produces from the '{}' every one of these columns defaults to.
func openField(value map[string]any) (field any, held bool, err error) {
	if len(value) == 0 {
		return nil, false, nil
	}

	return value, true, nil
}

// unreadableColumn is the error a declared column with no reader produces.
func unreadableColumn(table, column string) error {
	return errors.Internal(CodeDisclosureColumnUnreadable,
		"the %s module declares %s.%s as personal data and its disclosure has no reader for that "+
			"column; answering without it would hand the person a document that is short of what "+
			"the declaration promised", EntityName, table, column)
}

// whyNothingHere is the sentence a [personaldata.Nothing] answer carries.
//
// The contract REQUIRES one, and the reason is worth restating where it is
// written: "we found nothing" is an answer a person may come back and question,
// and the sentence saying where it was looked for is the only thing that makes
// it checkable. It names the rows that were searched — including the ones every
// other read in this module hides — and which handles were available.
func whyNothingHere(customerID, email string) string {
	return searchedText(customerID, email) + ", and holds no cart of this person"
}

// whyDisclosed is the sentence a [personaldata.Disclosed] answer carries.
//
// The contract does not require a Why here, and one is written anyway, because
// the two things it says are things only this module knows: what it was able to
// search by, and whether the document is all of it. A dossier that was cut at a
// bound and did not say so would read as a complete answer, and the person
// would have no way to find that out.
func whyDisclosed(customerID, email string, held, listed int64, oldestListed time.Time) string {
	searched := searchedText(customerID, email)
	if held <= listed {
		return searched + ", and this document carries every cart it found"
	}

	return fmt.Sprintf("%s, and found %d carts of this person; this document carries the %d most "+
		"recent of them, so the remaining %d — every cart opened before %s — are still held here "+
		"and are not listed. A shop opens a cart per shopping session rather than per purchase, so "+
		"the number of carts one document carries is bounded at %d; nothing was deleted at that "+
		"bound and the rest can be asked for",
		searched, held, listed, held-listed, oldestListed.UTC().Format(time.RFC3339), MaxDisclosedCarts)
}

// searchedText says which rows were searched and by which handles.
//
// The handles are named rather than counted because the gap they leave is
// specific and the person is the only one who can tell whether it matters: a
// request carrying a customer id alone cannot reach the carts she opened as a
// guest, and one carrying an e-mail alone cannot reach the carts she opened
// after signing in. Saying "we searched by e-mail" writes that limit into the
// document instead of leaving it to be discovered.
//
// No identifier VALUE appears in the text. The sentence is assembled into a
// document about the person and repeated in a log line, and an answer that
// quoted the address back would put it into places the erasure does not reach.
func searchedText(customerID, email string) string {
	const prefix = "the cart module searched every cart row it has, the soft-deleted and the " +
		"completed ones included, by "

	switch {
	case customerID != "" && email != "":
		return prefix + "customer id and e-mail address"
	case customerID != "":
		return prefix + "customer id alone, because the request carried no e-mail address — a cart " +
			"this person opened as a guest carries an e-mail and no customer id and could not be found"
	default:
		return prefix + "e-mail address alone, because the request carried no customer id — a cart " +
			"this person opened while signed in may carry the customer id and no e-mail and could " +
			"not be found"
	}
}
