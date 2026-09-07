package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
)

// This file is the cart module's answer to an erasure request (ADR 0029,
// core/personaldata, and the mechanism in ADR 0033).
//
// # The answer is ANONYMIZED, and the alternative was DELETE
//
// A cart is not a commercial record the way an order is, so the question this
// module has to answer and the order module does not is whether an erasure
// should simply DELETE the shopper's carts: carts cascades to its three child
// tables, one statement would leave nothing behind, and no ADR gives a cart the
// invoice's immutability. The answer is still anonymize, on four measurements
// taken in this tree on 2026-09-07.
//
//  1. A cart IS read after checkout. orders.cart_id is an indexed TEXT column
//     with no foreign key (orders_cart_idx), written by the checkout saga, and
//     it is the only way back from a sale to the session it came from; the
//     module publishes "cart.query" to the Query layer, so a cart is joinable
//     and filterable from outside; /admin/v1/carts reads them. And the module
//     already answered the deletion question for itself in one direction:
//     [Service.DeleteCart] REFUSES to delete a completed cart even softly,
//     because "it is the record the order rests on and deleting it would be
//     destroying history". A hard delete from the erasure would do exactly what
//     every other path in the module refuses to do, to exactly the carts that
//     became sales.
//  2. A checkout that is still running writes to the cart AFTER the payment
//     pivot. complete_cart takes its snapshot once and works from the plan from
//     then on; its one remaining call into this module is clear_cart's
//     MarkCompleted, which reads completed_at, the lines and the two revision
//     counters. This erasure writes none of those columns — which is also why
//     it does not bump revision — so an anonymization landing mid-checkout
//     leaves the saga able to finish, while a delete would strand a paid order
//     against a cart that no longer exists.
//  3. Deleting would not remove the copy anyway. No link definition names a
//     cart (this module declares none), but the saga store keeps a VERBATIM
//     copy of the cart snapshot — the e-mail and both addresses — in
//     workflow_executions.input, and nothing prunes it; ADR 0033 reports that
//     copy as RETAINED on every sweep. A delete would buy a stronger word in
//     the report without removing more of the person.
//  4. Deleting would take non-personal data with it. The cascade reaches
//     cart_line_items and cart_shipping_methods, and neither holds a column
//     gobit writes a person into — what was in the basket, at what price, with
//     which delivery. The shape of the session would be destroyed as collateral
//     for personal data that lives in the other two tables.
//
// The honest cost of choosing anonymize is that the open columns survive it:
// carts.metadata, cart_line_items.metadata, cart_addresses.metadata and
// cart_shipping_methods.data are still there afterwards, and a delete is the
// only answer that would clear them. That is the strongest argument the other
// side has and it does not carry, because clearing them by dropping the row is
// blanking a metadata column with extra steps — the thing ADR 0033 rejected by
// name, since it destroys the embedder's data on a guess about its contents and
// takes back a judgement ADR 0029 leaves with the controller. What this module
// owes instead is to NAME them, which [personaldata.Result.Kept] does.
//
// # Why the report has to name what stayed
//
// gobit never rewrites a free-form column. The consequence is that
// "anonymized" is only honest if the answer SAYS which columns were left, which
// is what [personaldata.Result.Kept] carries and why [personalColumns] is a single
// table that produces both the declaration and the kept list: two lists written
// separately drift, and the day they drift the module reports a column it did
// not look at as clean.

// ErasureHolder is the name this module answers an erasure request under.
//
// It has to stay equal to cart.ModuleName and it is a SECOND string because
// this package cannot import the package that wires it (the ADR 0001
// direction); the repetition is the price this module's other cross-package
// name constants pay for the same isolation. The sweep overwrites
// [personaldata.Result.Holder] with the registry's name anyway
// (internal/workflows/datasubject) — this value is what a caller holding the
// service directly gets, and what the log line says.
const ErasureHolder = EntityName

// CodeErasureSubjectEmpty reports that the erasure request named nobody.
//
// It is its own code because it is the one refusal this path makes and it is a
// caller's mistake rather than a fault: answering "anonymized, zero rows" to a
// subject with no identifier would report an erasure that never looked
// anywhere.
const CodeErasureSubjectEmpty = "cart_erasure_subject_empty"

// The tables the declaration names.
//
// They are spelled once because the declaration repeats them and a mistyped
// table name in a declaration is not a compile error — it is an auditor sent to
// a table that does not exist.
const (
	tableCarts               = "carts"
	tableCartLineItems       = "cart_line_items"
	tableCartAddresses       = "cart_addresses"
	tableCartShippingMethods = "cart_shipping_methods"
)

// columnMetadata is the free-form column three of the four tables carry; the
// fourth calls its own "data". Naming it once keeps the declaration's rows
// short enough to read as a table.
const columnMetadata = "metadata"

// columnShippingData is that fourth column's name.
//
// It is a constant for the same reason [columnMetadata] is, and it earned one
// when the disclosure arrived: that path has to ASK for a row's declared column
// by name (see noteRecord in disclosure.go), and a second literal "data" spelled
// there would be a place where the disclosure and the declaration could disagree
// about what the column is called — a disagreement that produces a person's
// dossier missing a column rather than a compile error.
const columnShippingData = "data"

// The names of the personal columns themselves.
//
// They are constants for the reason the table names above are, and they earned
// it when a THIRD place in this package started spelling the same nine strings.
// The declaration below names them, [Service.setAddress] names them in the error
// it returns when a field is too long, and the disclosure asks a row for a
// declared column BY NAME (disclosure.go). A literal repeated across three files
// is three chances for a typo that is not a compile error, and each of the three
// fails differently and quietly: an auditor sent to a column that does not
// exist, an error naming a field that is not the one being validated, or a
// declared column that no reader answers for.
const (
	columnCustomerID      = "customer_id"
	columnEmail           = "email"
	columnSourceAddressID = "source_address_id"
	columnFirstName       = "first_name"
	columnLastName        = "last_name"
	columnCompany         = "company"
	columnAddress1        = "address_1"
	columnAddress2        = "address_2"
	columnCity            = "city"
	columnProvince        = "province"
	columnPostalCode      = "postal_code"
	columnCountryCode     = "country_code"
	columnPhone           = "phone"
)

// personalColumn is one declared place this module keeps personal data, plus
// whether the erasure rewrites it.
//
// The two facts sit in one row on purpose. The module's PersonalData answers
// "where could this person be" and [personaldata.Result.Kept] answers "where could
// they still be afterwards"; the second is the first minus the columns the
// anonymizing statements null. Kept as separate lists they would agree on the
// day they were written and diverge on the day a column was added to one of
// them — and the failure would be silent, because the report would still look
// complete.
//
// What this table cannot prove BY ITSELF is that the erased flags match the SQL
// in queries/erasure.sql: the statements are text and a Go value cannot run
// them. Two tests take that as far as it goes without a database —
// erasure_internal_test.go reads the statements and requires the columns they
// set to NULL to be exactly the ones flagged erased here, and
// internal/modules/cart/erasure_test.go requires this table to cover every
// column the migration creates. What neither can see is the WHERE clause,
// because a statement that finds no rows passes both; that is what the
// integration test is for.
type personalColumn struct {
	// holding is what the declaration says about the column.
	holding personaldata.Holding
	// erased reports that the anonymizing statements set the column to NULL.
	erased bool
}

// personalColumns is every place the cart module keeps personal data.
//
// # What is deliberately NOT here
//
//   - cart_line_items.title and unit_price: a catalog copy and a price. What
//     somebody put in a basket is a fact about the basket, not a name.
//   - carts.region_id, carts.currency_code, cart_line_items.variant_id,
//     cart_shipping_methods.shipping_option_id: identifiers and codes belonging
//     to OTHER modules' records — a region, a product variant, a delivery
//     option. Each of those modules answers for its own rows. carts.customer_id
//     IS declared, and the difference is what the id points at: the customer id
//     is the installation's stable handle for the PERSON, while a variant id
//     names a thing.
//   - cart_shipping_methods.name: the label of the delivery method the shop
//     offers ("standard", "next day"); it is written about the service, not
//     about the shopper.
//   - cart_addresses.address_type, every amount, both revision counters and
//     every stamp: they describe the session and its shape.
//
// The order of the entries is the order they appear in the report, so it is
// stable and readable: table by table, in the order the migration creates the
// tables, and within a table in the order the columns are declared.
var personalColumns = []personalColumn{
	{
		holding: personaldata.Holding{
			Table: tableCarts, Column: columnCustomerID, Kind: personaldata.Named,
			Why: "the customer module's identifier for the shopper; a guest cart has none",
		},
		// It is the handle a repeated sweep finds these rows by
		// (carts_customer_idx). Nulling it would leave the second sweep — the
		// one idempotence requires to answer the same thing — unable to find
		// the rows it already erased.
		erased: false,
	},
	{
		holding: personaldata.Holding{
			Table: tableCarts, Column: columnEmail, Kind: personaldata.Named,
			Why: "the address the shopper gave; on a guest cart it is the only handle to them",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCarts, Column: columnMetadata, Kind: personaldata.Open,
			Why: "the caller's own data on the cart; gobit does not look inside it",
		},
		erased: false,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartLineItems, Column: columnMetadata, Kind: personaldata.Open,
			Why: "the caller's own data on a line — a personalisation, an engraving, a gift note",
		},
		erased: false,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnSourceAddressID, Kind: personaldata.Named,
			Why: "which entry of the shopper's address book this copy was taken from",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnFirstName, Kind: personaldata.Named,
			Why: "the shopper's given name as it was written on the cart",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnLastName, Kind: personaldata.Named,
			Why: "the shopper's family name as it was written on the cart",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnCompany, Kind: personaldata.Named,
			Why: "the company on the address; a one-person business is a person",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnAddress1, Kind: personaldata.Named,
			Why: "the street the cart would have been shipped to or billed to",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnAddress2, Kind: personaldata.Named,
			Why: "the rest of the street address — the building, the floor, the flat",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnCity, Kind: personaldata.Named,
			Why: "the city of the address",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnProvince, Kind: personaldata.Named,
			Why: "the province or district of the address",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnPostalCode, Kind: personaldata.Named,
			Why: "the postal code, which in a small district reaches a household on its own",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnCountryCode, Kind: personaldata.Named,
			Why: "the country the cart was addressed to; it is the one address column the erasure keeps",
		},
		// The row itself has to survive — an absent address row already means
		// "this cart never reached the address step" — so what is left has to
		// stay readable AS an address. A country is jurisdiction rather than
		// identity and does not reach a person on its own.
		erased: false,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnPhone, Kind: personaldata.Named,
			Why: "the number given for the delivery",
		},
		erased: true,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartAddresses, Column: columnMetadata, Kind: personaldata.Open,
			Why: "the caller's own data on the address — delivery instructions are typed here",
		},
		erased: false,
	},
	{
		holding: personaldata.Holding{
			Table: tableCartShippingMethods, Column: columnShippingData, Kind: personaldata.Open,
			Why: "the delivery provider's own data on the chosen method — a pickup branch or a locker is typed here",
		},
		erased: false,
	},
}

// PersonalDataHoldings returns everything this module says it keeps about
// people.
//
// The module type turns it into an [personaldata.Declaration]; the list lives here
// because the erasure that acts on it lives here, and a declaration written in
// a second place is a declaration that can disagree with the code that erases.
//
// A fresh slice is returned on every call: the declaration is read by an audit
// that has no reason to be careful with it, and handing out the package's own
// slice would let one caller's append reach every later one.
func PersonalDataHoldings() []personaldata.Holding {
	out := make([]personaldata.Holding, 0, len(personalColumns))
	for i := range personalColumns {
		out = append(out, personalColumns[i].holding)
	}

	return out
}

// keptAfterAnonymize lists the "table.column" entries an anonymized cart still
// holds.
func keptAfterAnonymize() []string {
	out := make([]string, 0, len(personalColumns))
	for i := range personalColumns {
		if personalColumns[i].erased {
			continue
		}
		out = append(out, columnPath(personalColumns[i].holding))
	}

	return out
}

// columnPath spells one holding the way [personaldata.Result.Kept] wants it.
func columnPath(h personaldata.Holding) string { return h.Table + "." + h.Column }

// whyAnonymized explains the kept list of an anonymized answer.
//
// It is one sentence in three clauses because the list has three groups, and a
// controller repeating this to a data subject has to be able to say what each
// group is. The free-form clause is the one that must not be dropped: without
// it "anonymized" would cover five columns nobody looked at.
const whyAnonymized = "the name, address and phone on every cart of this person were set to NULL along " +
	"with the e-mail on the cart itself, and what is listed stayed: gobit never rewrites a free-form " +
	"column, because ADR 0029 leaves the judgement of whether a metadata blob holds personal data in " +
	"this deployment with the controller; carts.customer_id stayed because it is the handle by which a " +
	"repeated request finds these carts again, and it reaches the person only through the customer " +
	"module's record, which answers the same request on its own account; cart_addresses.country_code " +
	"stayed so that the surviving row is still readable as an address the cart HAD, which a deleted row " +
	"could not be, and a country does not identify anybody on its own."

// Erase answers an erasure request about one person (ADR 0029).
//
// # What it does
//
// In a SINGLE transaction it locks the person's carts, sets the e-mail on each
// one to NULL, and sets the name, the address, the phone and the address-book
// pointer of every address on them to NULL. Nothing is deleted: the argument
// for anonymizing rather than deleting a cart is at the top of this file. The
// lines, the totals, both revision counters and the completion stamp survive
// untouched, so what the person had in the basket and what it came to still
// reads, without them.
//
// Both handles are used, not one or the other. A guest cart has no customer id
// and can only be found by e-mail, a cart opened for a signed-in shopper may
// carry the id and no e-mail, and the same person often has both.
//
// # It is idempotent, and where that has a limit
//
// A second call returns the same outcome. The anonymizing statements are
// unconditional over the same set of carts, so they rewrite the same rows to
// the same values and report the same count.
//
// The limit is worth stating plainly, because it is a property of the data and
// not of this code: for a GUEST cart — one with no customer id — the e-mail is
// the only handle, and erasing it is erasing the handle. A second call for such
// a subject finds nothing, so it answers [personaldata.Anonymized] with zero rows
// and an empty kept list. The outcome is unchanged, which is what the contract
// requires; the FIRST report is the one that names what stayed, and the
// controller has to keep it.
//
// # Why there is no Retained branch
//
// The order module refuses while an order is still being performed, because an
// order that is still moving is performed WITH the contact on it. A cart has no
// such fact: it is completed or it is not, and a completed cart is not being
// performed — the order born from it is, and that order answers for itself.
// Nothing in this module's tables can make keeping a name necessary.
//
// # The subject is never logged
//
// The log line carries counts and the outcome and no identifier of the person.
// Writing the e-mail of somebody who asked to be forgotten into a log would put
// it back into the installation through the one door the erasure does not
// reach.
func (s *Service) Erase(ctx context.Context, subject personaldata.Subject) (personaldata.Result, error) {
	customerID, email, err := normalizeErasureSubject(subject)
	if err != nil {
		return personaldata.Result{}, err
	}

	var (
		rows  int64
		carts int
	)

	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		ids, err := s.store.CartsForErasure(ctx, customerID, email)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		contacts, err := s.store.AnonymizeCartContacts(ctx, ids)
		if err != nil {
			return err
		}
		addresses, err := s.store.AnonymizeCartAddresses(ctx, ids)
		if err != nil {
			return err
		}

		rows = contacts + addresses
		carts = len(ids)

		return nil
	})
	if err != nil {
		return personaldata.Result{}, err
	}

	result := erasureResult(rows, carts)
	s.log.InfoContext(ctx, "an erasure request was answered",
		"holder", ErasureHolder, "outcome", string(result.Outcome),
		"anonymized_carts", carts, "rows", result.Rows)

	return result, nil
}

// erasureResult turns the counts into the answer the controller reads.
//
// It is split out of [Service.Erase] because it is the part with no database in
// it: given the two numbers the outcome, the kept list and the sentence are
// decided by rules, and rules that can be read without a transaction around
// them are rules that can be checked.
//
// # What Rows means here
//
// It is the number of database ROWS the two statements wrote: one per cart the
// person has, plus one per address on those carts. It is not the number of
// carts and not the number of columns — a controller comparing two reports is
// comparing how much was touched, and a cart with a shipping and a billing
// address is twice the writing of a cart with neither. The figure is stable
// across a repeated sweep whenever the subject has a customer id, because both
// statements are unconditional over the same set of ids.
func erasureResult(rows int64, carts int) personaldata.Result {
	result := personaldata.Result{Holder: ErasureHolder, Outcome: personaldata.Anonymized, Rows: int(rows)}
	if carts == 0 {
		// The person had nothing here. The kept list stays EMPTY rather than
		// listing the free-form columns: those columns hold nothing about
		// somebody who never opened a cart, and a report naming them would send
		// a controller looking through rows that do not exist.
		return result
	}

	result.Kept = keptAfterAnonymize()
	result.Why = whyAnonymized

	return result
}

// normalizeErasureSubject validates an ERASURE's subject and puts its
// identifiers into the form the columns hold.
//
// The work is [normalizeSubject]'s, shared with the disclosure. What is
// specific to this caller is what the refusal at the end of it prevents: a
// subject with no identifier is refused because erasing everybody is not an
// erasure request. The sweep refuses it first
// (internal/workflows/datasubject) and this is the last defense, because a
// holder reached directly would otherwise take a zero-value subject and lock
// every cart in the installation.
func normalizeErasureSubject(subject personaldata.Subject) (customerID, email string, err error) {
	return normalizeSubject(subject, CodeErasureSubjectEmpty, "an erasure request")
}

// normalizeSubject is the resolution both data-subject answers share.
//
// The erasure and the disclosure (disclosure.go) must resolve a person the SAME
// way, and this function is where that is true rather than a coincidence: a
// module able to find more rows to delete than to show would reopen, in a
// smaller form, exactly the asymmetry the disclosure was added to close. It is
// therefore one function with two callers rather than two functions that agree
// today.
//
// The e-mail is folded to lower case with [normalizeEmail], the same function
// that folded it on the way IN (see [Service.CreateCart] and
// [Service.UpdateCart]): the column holds what that function produced, so
// matching with anything else would silently miss the rows. An address that
// cannot pass it is rejected rather than run as a filter that can only match
// nothing — a subject that reaches this module malformed is a caller's mistake,
// and answering "nothing found" to it would report a search that never looked
// anywhere.
//
// What differs between the callers is only the refusal: the code and the noun
// are parameters because an operator reading "your erasure request named
// nobody" after asking to SEE somebody's data would go looking at the wrong
// request. The validation, the folding and the rule itself are one copy.
func normalizeSubject(
	subject personaldata.Subject, emptyCode, request string,
) (customerID, email string, err error) {
	if subject.CustomerID != "" {
		if err := requireID("customer_id", subject.CustomerID); err != nil {
			return "", "", err
		}
	}
	email, err = normalizeEmail(subject.Email)
	if err != nil {
		return "", "", err
	}
	if subject.CustomerID == "" && email == "" {
		return "", "", errors.Invalid(emptyCode,
			"%s has to name somebody: give a customer id, an e-mail address, or both", request)
	}

	return subject.CustomerID, email, nil
}
