package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// CodeDisclosureSubjectEmpty reports a disclosure request that named nobody.
const CodeDisclosureSubjectEmpty = "customer_disclosure_subject_empty"

// disclosureSearched is the clause that says WHERE this module looked.
//
// It is one string used by two answers on purpose. The sentence a person is
// given when nothing was found and the sentence they are given beside their
// records have to describe the SAME search, or the two answers could disagree
// about what was looked at while both were true of themselves — and the reader
// comparing them would have no way to tell which one had gone stale.
const disclosureSearched = "the customer records this person resolves to, by customer id and by e-mail " +
	"address, together with every address saved on them, soft-deleted rows included"

// disclosureWhyNothing is the sentence that goes with [personaldata.Nothing].
//
// It has to say where the search went, because "we found nothing" is an answer
// a person may query and the sentence naming the places searched is the only
// part of it that can be checked. "Nothing" without it is indistinguishable
// from a holder that never looked.
const disclosureWhyNothing = "Nothing here belongs to this person. What was searched: " +
	disclosureSearched + "."

// disclosureWhyDisclosed is the sentence that travels WITH the records.
//
// The published contract requires Why for the two states that are not
// [personaldata.Disclosed] and does not forbid it here, and this module fills it in
// for the reason ADR 0033 makes Result.Kept mandatory on an anonymized erasure:
// a holder that DECLARED a place and hands over nothing from it has to say so,
// or the silence reads as "we hold nothing of yours there". Two declared places
// produce no record for anybody and both are named here.
//
// customer_group.metadata is a blob the shop writes about a SEGMENT
// ("wholesalers") and shares with every member of it. It is declared because
// gobit never looks inside it and therefore cannot say it holds nobody — that
// judgement is the controller's (ADR 0029) — but the same not-looking is why it
// cannot be attributed to one member: handing a segment's blob to whoever asks
// first would disclose, to this person, whatever the shop happened to write
// about the OTHERS in it. The declaration is what points a controller at the
// column; a person's file is not the place it can be answered from.
//
// The membership row is left out on a stricter rule: the module declares no
// column of customer_group_customer at all, because the row holds two
// identifiers and the moment it was made, and an identifier names a row rather
// than a person — the very fact the anonymization rests on. Disclosing from it
// would contradict the declaration in the direction nobody watches: the
// controller was told that table holds nothing about people and would then be
// handed data out of it. If a deployment decides a segment assignment IS
// personal data about its members, the honest fix is to declare the column;
// because the records below are DERIVED from the declaration, the disclosure
// follows from that one edit instead of being a second thing to remember.
const disclosureWhyDisclosed = "This is " + disclosureSearched + ". Two declared places are " +
	"deliberately not in it: a customer group's metadata, which the shop writes about a segment and " +
	"shares with everyone in it, and the group membership row, which holds identifiers and nothing else."

// PersonalDataOf answers what this module holds about one person.
//
// # Why the module owes this at all
//
// It can already FIND her. The erasure resolves both handles, guests included,
// in order to overwrite what it finds; a module that can search in order to
// destroy and cannot search in order to show has made an asymmetry out of its
// own code rather than out of anybody's policy (ADR 0034). This method is the
// other direction of the search [Service.Erase] already does, and it resolves
// the subject exactly the way that one does — both handles, the union of what
// each reaches, soft-deleted rows included.
//
// # It is a READ
//
// No transaction is opened, no row is locked and nothing is written. The
// erasure locks FOR UPDATE because it reads a row and then overwrites it, and a
// concurrent write landing in between would be lost or would land after the
// erasure and leave the row personal again. Nothing here overwrites anything,
// so there is no such window — and taking those locks would make a report being
// READ block a customer editing their own name in the storefront. The cost is
// that a customer row and its addresses are read in two statements and a write
// could land between them; the dossier is then a snapshot of a person's data a
// few milliseconds apart, which is what any answer to this question is.
//
// # Why soft-deleted rows are in it
//
// For the same reason the erasure reaches them: the question is what the
// database still HOLDS. A soft delete writes deleted_at and updated_at and
// clears not one personal column, so a deleted customer's e-mail, name and phone
// are sitting in the table exactly as they were. Answering from live rows only
// would tell a person "this is everything we have about you" while their name
// stayed in a row the listing screens no longer show.
//
// # What the records are
//
// One record per ROW. The customer's own row is one record and each of their
// addresses is another, with Table set to the real table name and ID to the
// row's id, because a person reading their file has to be able to see that two
// addresses are two things rather than one merged blob. A subject that resolves
// to several customer rows — a registered account plus the guest checkouts made
// under the same address — produces a record for each, since all of them are
// this person.
//
// The fields of a record are DERIVED from the declaration (see
// [personalDataHoldings]) and are therefore exactly the declared columns of that
// table, in the declared order, whether or not they hold anything. A column that
// is empty still appears: a file whose shape changed with its content would
// leave a reader unable to tell "this column is empty" from "this column was
// not looked at", and those are the two things this whole mechanism exists to
// tell apart.
//
// An already anonymized record is disclosed as it stands, placeholders and all.
// That is not a leak of the erasure's mechanics into somebody's file — it is the
// truthful answer to what the database holds, and hiding it would tell a person
// who asked to be forgotten that their row is gone when it is still there.
//
// # The two empties
//
// A subject this module has never seen answers [personaldata.Nothing] with a
// sentence naming where the search went, never Disclosed with an empty list:
// the sweep asks every holder about the same person and most holders have never
// seen them, so "searched and not here" is a normal answer and a useful one.
// [personaldata.Unresolvable] is not among the answers because this module can
// always search when it is given a handle — and a subject carrying NO handle is
// refused as an invalid request rather than dressed up as a state, exactly as
// the erasure refuses it. Disclosing "everyone" is not a disclosure request.
func (s *Service) PersonalDataOf(ctx context.Context, subject personaldata.Subject) (personaldata.Disclosure, error) {
	if err := s.ready(); err != nil {
		return personaldata.Disclosure{}, err
	}

	customerID := strings.TrimSpace(subject.CustomerID)
	// The e-mail is normalized rather than validated, for the reason
	// [Service.Erase] gives: a malformed address is not a bad request here, it
	// is an address this module stores nothing under, and rejecting it would
	// turn "nothing to show" into an error in the middle of somebody else's
	// dossier.
	email := models.NormalizeEmail(subject.Email)

	if customerID == "" && email == "" {
		return personaldata.Disclosure{}, errors.Invalid(CodeDisclosureSubjectEmpty,
			"a disclosure request must name a customer id or an e-mail address")
	}

	customers, err := s.repo.CustomersForDisclosure(ctx, customerID, email)
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	if len(customers) == 0 {
		// The addresses are not looked up at all in this branch, and cannot
		// hide anything by being skipped: an address reaches a person only
		// through the customer row it hangs off, so no customer row means no
		// address of this person exists to be missed.
		s.log.InfoContext(ctx, "customer disclosure found nothing",
			slog.Bool("by_customer_id", customerID != ""),
			slog.Bool("by_email", email != ""),
		)

		return personaldata.Disclosure{
			Holder: ErasureHolder,
			State:  personaldata.Nothing,
			Why:    disclosureWhyNothing,
		}, nil
	}

	ids := make([]string, 0, len(customers))
	for i := range customers {
		ids = append(ids, customers[i].ID)
	}

	addresses, err := s.repo.AddressesForDisclosure(ctx, ids)
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	// Grouped rather than appended in one run so that each customer's addresses
	// follow their own record. A subject that resolved to a registered account
	// and four guest checkouts otherwise produces five customer records and then
	// a pile of addresses, and the person reading it cannot tell which street
	// belonged to which purchase.
	byCustomer := make(map[string][]models.CustomerAddress, len(ids))
	for i := range addresses {
		owner := addresses[i].CustomerID
		byCustomer[owner] = append(byCustomer[owner], addresses[i])
	}

	records := make([]personaldata.Record, 0, len(customers)+len(addresses))
	for i := range customers {
		records = append(records,
			recordOf(TableCustomer, customers[i].ID, customerValues(customers[i])))

		owned := byCustomer[customers[i].ID]
		for j := range owned {
			records = append(records,
				recordOf(TableAddress, owned[j].ID, addressValues(owned[j])))
		}
	}

	// The e-mail is not logged (plan Section 8) and neither handle would
	// identify the person in a log line anyway. What is worth recording is that
	// somebody's file was ASSEMBLED and how much came out of it: this is the
	// most concentrated personal data this module can produce, and an audit
	// asking who read what afterwards finds nothing if the read left no trace.
	s.log.InfoContext(ctx, "customer personal data disclosed",
		slog.Bool("by_customer_id", customerID != ""),
		slog.Bool("by_email", email != ""),
		slog.Int("customers", len(customers)),
		slog.Int("addresses", len(addresses)),
	)

	return personaldata.Disclosure{
		Holder:  ErasureHolder,
		State:   personaldata.Disclosed,
		Records: records,
		Why:     disclosureWhyDisclosed,
	}, nil
}

// recordOf builds one row's record out of the DECLARATION.
//
// This function is the whole of the anti-drift argument in ADR 0034's fourth
// point, and it is three lines long because that is what deriving costs. The
// caller hands over what the row holds, keyed by column; the declared holdings
// of the table decide which of those keys become fields, in which order, and
// with which [personaldata.Kind]. A column the row can produce and the declaration
// does not name is never disclosed, and the Kind cannot disagree with the
// declaration because it is not copied from anywhere else.
//
// A declared column the values map has no entry for yields a nil value rather
// than a panic or a missing field, which is the one silent failure left in this
// design; [TestEveryDeclaredColumnHasAValue] is what closes it, by holding the
// two key sets equal without a database.
func recordOf(table, id string, values map[string]any) personaldata.Record {
	holdings := holdingsOf(table)

	fields := make([]personaldata.Field, 0, len(holdings))
	for _, holding := range holdings {
		fields = append(fields, personaldata.Field{
			Column: holding.Column,
			Kind:   holding.Kind,
			Value:  values[holding.Column],
		})
	}

	return personaldata.Record{Table: table, ID: id, Fields: fields}
}

// customerValues is what a customer row holds, keyed by COLUMN name.
//
// The keys are column names and not Go field names because the declaration
// speaks in columns: this map is the join between a row and what gobit told the
// controller it keeps, and translating between the two spellings anywhere else
// would put a second mapping in the way of the derivation.
//
// The map carries only columns that could be declared. has_account, the
// timestamps and the id are absent by construction rather than by filtering —
// they say something about the record and nothing about the person, which is the
// same line the module's PersonalData draws.
//
// Metadata is handed over as the map it is stored as, not as raw bytes: a
// dossier is read by a person, and a base64 blob answers nothing. It may be nil,
// and a nil value is the honest reading of a column holding '{}'.
func customerValues(c models.Customer) map[string]any {
	return map[string]any{
		columnEmail:     c.Email,
		columnFirstName: c.FirstName,
		columnLastName:  c.LastName,
		columnPhone:     c.Phone,
		columnMetadata:  c.Metadata,
	}
}

// addressValues is what an address row holds, keyed by COLUMN name.
//
// country_code is in it. It is the one column the erasure deliberately does NOT
// overwrite — it names a jurisdiction rather than a person — and that is a
// decision about what may be DESTROYED, which has no bearing on what may be
// SHOWN. It is declared, so it is disclosed; a column kept out of a person's
// file because the module refuses to erase it would be answering a question
// nobody asked.
func addressValues(a models.CustomerAddress) map[string]any {
	return map[string]any{
		columnFirstName:   a.FirstName,
		columnLastName:    a.LastName,
		columnCompany:     a.Company,
		columnAddress1:    a.Address1,
		columnAddress2:    a.Address2,
		columnCity:        a.City,
		columnPostalCode:  a.PostalCode,
		columnPhone:       a.Phone,
		columnCountryCode: a.CountryCode,
	}
}
